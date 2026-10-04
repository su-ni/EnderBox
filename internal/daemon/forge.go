package daemon

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Forge 的安装跟别家不是一个形状：下载回来的只是 installer，必须在本机再跑一遍
// （`java -jar installer.jar --installServer <实例目录>`）才产出能启动的东西。
// 这一步会再下 100 MB 左右的库文件，实测 1.20.4 全流程 34 秒、1.12.2 31 秒。
//
// 这个文件只负责「把 installer 跑起来」＋「跑完到底该启动哪个 jar」。
// 版本/构建从哪来、installer 怎么下，都在 internal/fetch 里。

// ForgeInstallOpts 跑一次 Forge 安装需要的东西
type ForgeInstallOpts struct {
	Ctx        context.Context
	InstanceID string
	JavaPath   string // 用哪个 java 跑 installer
	Tag        string // 1.20.4-49.0.30 —— 用来认产物文件名
	Installer  string // installer jar 的绝对路径
	JavaMajor  int    // 这个版本要求的 Java，出错时用来给「装不上」一个可操作的解释
}

// InstallForge 跑一遍 installer，返回**该拿来启动的那个 jar 的文件名**（相对实例目录），
// 顺便把它写进实例配置。
//
// 为什么必须由产物决定、而不是由版本号推断：装出来的结构分两代，
// 差别写在 installer 自己的 install_profile.json 里（spec=1 出 shim、spec=0 出
// forge-<版本>.jar），上游哪天再改一版，按版本号写的推断就会静默指错文件。
// 所以流程是「跑完 → 看目录里实际有什么 → 按实况决定」（见 findForgeLaunchJar）。
func (m *Manager) InstallForge(o ForgeInstallOpts, onProgress func(string)) (string, error) {
	inst, ok := m.st.Get(o.InstanceID)
	if !ok {
		return "", fmt.Errorf("实例不存在: %s", o.InstanceID)
	}
	if onProgress == nil {
		onProgress = func(string) {}
	}
	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	m.Log(o.InstanceID, fmt.Sprintf("[ServerBox] 开始安装 Forge %s（要下载库文件，这一步比下载本身慢）", o.Tag))
	onProgress("正在准备安装")

	cmd := exec.CommandContext(ctx, o.JavaPath, "-jar", o.Installer, "--installServer", inst.Dir)
	cmd.Dir = inst.Dir

	// installer 的输出是按行给的（实测 1.20.4 有 29698 行），全塞进实例日志会把
	// 用户真正要看的东西冲掉，所以这里边解析边过滤：
	//   数「Downloading library from」的条数当进度，每 8 个回一次；
	//   只把阶段标记和报错行写进日志。
	// 同时留一份尾部，失败时要能告诉用户到底哪一句出错了。
	var (
		libs      int
		steps     int
		noMap     int // 「Can't Find Class」的条数，最后汇总成一行
		tail      []string
		tailLimit = 40
	)
	sink := &lineSink{fn: func(ln string) {
		if ln == "" {
			return
		}
		if len(tail) >= tailLimit {
			tail = tail[1:]
		}
		tail = append(tail, ln)

		if strings.Contains(ln, forgeNoMapReport) {
			noMap++
			return
		}

		switch {
		case strings.Contains(ln, "Downloading library from"):
			libs++
			if libs%8 == 0 {
				onProgress(fmt.Sprintf("正在下载库文件（已取 %d 个）", libs))
			}
			return
		case strings.Contains(ln, "Download completed"),
			strings.Contains(ln, "Checksum Validated"),
			strings.Contains(ln, "Considering library"),
			strings.Contains(ln, "Considering minecraft server jar"),
			strings.Contains(ln, "Considering minecraft client jar"):
			// 几百行噪音，别往日志里灌
			return
		}
		if strings.Contains(ln, "Downloading libraries") {
			onProgress("正在下载库文件")
		}
		if strings.Contains(ln, "Extracting main jar") {
			onProgress("正在展开原版服务端")
		}
		if strings.Contains(ln, "Building Processors") || strings.Contains(ln, "Processing ") {
			steps++
			onProgress(fmt.Sprintf("正在给服务端打补丁（第 %d 步）", steps))
		}
		if interestingLine(ln) {
			m.Log(o.InstanceID, "[forge] "+ln)
		}
	}}
	cmd.Stdout = sink
	cmd.Stderr = sink

	runErr := cmd.Run()
	sink.flush()

	if noMap > 0 {
		// 汇总成一行。这些是重命名工具对无法映射的类名做的逐条报告（实测一次 1.20.4 安装几百条），
		// 属于正常输出；但不写成「正常现象」——我们只能保证「装了这么多次都启动了」，
		// 不能替上游保证它永远无害。所以只陈述事实：有多少条、没逐条记录。
		m.Log(o.InstanceID, fmt.Sprintf("[forge] 重命名工具报告了 %d 个未能映射的类名（未逐条记录）", noMap))
	}

	if runErr != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("安装被取消了。已经下到一半的库文件留在目录里，重新装会接着用")
		}
		return "", forgeInstallError(runErr, tail, o.JavaMajor)
	}

	// installer 自己不说「装好了」的话也别信它退出码为 0 —— 实测成功时会打这句
	done := false
	for _, ln := range tail {
		if strings.Contains(ln, "installed successfully") {
			done = true
			break
		}
	}
	if !done {
		m.Log(o.InstanceID, "[ServerBox] 注意：安装器退出码是 0，但没打「installed successfully」，接着按目录实况判断")
	}

	launch, err := findForgeLaunchJar(inst.Dir, o.Tag)
	if err != nil {
		return "", err
	}

	inst.JarPath = launch
	// 顺手把 Forge 版本记进 loaderVersion。概览页要显示「Forge 1.20.4-49.2.0」这种东西，
	// 而这个信息只藏在文件名里（forge-1.20.4-49.2.0-shim.jar），不记一笔就得让前端去啃字符串。
	if i := strings.LastIndex(o.Tag, "-"); i > 0 && i < len(o.Tag)-1 {
		inst.LoaderVersion = o.Tag[i+1:]
	}
	if err := m.Update(&inst); err != nil {
		return "", fmt.Errorf("装好了但没记住该启动哪个文件（%w），去「配置」页把服务端文件填成 %s", err, launch)
	}

	m.Log(o.InstanceID, fmt.Sprintf("[ServerBox] Forge %s 装好了，服务端文件已指向 %s（%d 个库文件已就位）",
		o.Tag, launch, libs))
	m.Log(o.InstanceID, fmt.Sprintf("[ServerBox] 安装器留在目录里了（%s）—— 以后要重装直接用它，不用再下一遍",
		filepath.Base(o.Installer)))
	return launch, nil
}

// forgeInstallError 把安装器的失败翻译成用户能动手的东西。
// 只报「退出码 1」等于没报 —— 用户要的是「哪一句错了」和「我该干嘛」。
func forgeInstallError(runErr error, tail []string, javaMajor int) error {
	// 优先挑真正像报错的行。先过一遍噪音筛子，否则挑出来的可能是
	// ReportedException.class 这种「名字里带 Exception 的类」（见 interestingLine 的说明）。
	var errs []string
	for _, ln := range tail {
		if strings.Contains(ln, forgeNoMapReport) || isForgeNoise(ln) {
			continue
		}
		if forgeSignalRe.MatchString(ln) {
			errs = append(errs, ln)
		}
	}
	if len(errs) == 0 {
		// 一行都不像报错就退回最后几行 —— 有些失败只在最后给个笼统提示
		n := 6
		if len(tail) < n {
			n = len(tail)
		}
		errs = tail[len(tail)-n:]
	}
	if len(errs) > 6 {
		errs = errs[len(errs)-6:]
	}

	msg := fmt.Sprintf("Forge 安装器没能装成（%v）。它最后说的是：\n%s", runErr, strings.Join(errs, "\n"))

	// 老版本的安装器对 Java 版本很挑，而本机常常只装了新版 JDK —— 这是「明明下面
	// 这个库文件都下全了却装不上」最常见的原因，不说清楚用户会以为是源站的问题
	if javaMajor > 0 && javaMajor <= 8 {
		msg += fmt.Sprintf("\n它要的是 Java %d，本机如果只有新版 JDK，安装器本身可能会跑不起来 —— "+
			"去设置页的「Java 运行时」看看扫到了哪些，缺的话装一个 Java 8 再重试。", javaMajor)
	}
	return fmt.Errorf("%s", msg)
}

// findForgeLaunchJar 按目录实况决定启动哪个 jar。
//
// 实测两代产物在实例根目录各留一个「直接 -jar 就能起来」的 jar：
//
//	spec=1（1.17+） forge-<tag>-shim.jar      真身在 libraries/ 里，这个只是引子
//	spec=0（≤1.16.5）forge-<tag>.jar + minecraft_server.<MC>.jar
//
// 先按名字认；名字对不上（上游改了命名）就退回扫目录，但只在「排除掉安装器之后
// 只剩一个 jar」时才认 —— 1.16.5 那种目录里躺着两个 jar 的情况下瞎猜，
// 猜中的那个是原版 jar，启动起来就是「你的模组全没了」。
func findForgeLaunchJar(dir, tag string) (string, error) {
	for _, name := range []string{"forge-" + tag + "-shim.jar", "forge-" + tag + ".jar"} {
		if st, err := os.Stat(filepath.Join(dir, name)); err == nil && !st.IsDir() {
			return name, nil
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("读实例目录失败：%w", err)
	}
	var jars []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if !strings.HasSuffix(n, ".jar") || strings.HasSuffix(n, "-installer.jar") {
			continue
		}
		jars = append(jars, n)
	}
	if len(jars) == 1 {
		return jars[0], nil
	}
	if len(jars) == 0 {
		return "", fmt.Errorf("安装器跑完了，但实例目录里一个能启动的 jar 都没有 —— " +
			"这一版 Forge 的产物结构和我们认的不一样，把安装器的输出发出来看看")
	}
	return "", fmt.Errorf("安装器跑完了，但目录里有 %d 个 jar（%s），认不出该启动哪个。"+
		"去「配置」页手动把服务端文件填成 Forge 的那个", len(jars), strings.Join(jars, "、"))
}

// interestingLine 判断这行值不值得写进实例日志。
//
// ⚠ 这里踩过三个坑，全是真事，第三个最贵：
//
//  1. 「含 exception / error 就当报错」这种朴素筛法会灌进几百行假报错 ——
//     安装器会给它经手的每个类打一行路径，而里面净是
//     DefaultUncaughtExceptionHandler.class、ReportedException.class 这种
//     **名字里带 Exception 的正常类**。
//  2. 反过来，加词边界（要求 error 前后是空格/冒号）又会把真报错滤掉 ——
//     Java 的异常名是驼峰连写：UnsupportedClassVersionError、NoClassDefFoundError，
//     「Error」前面是字母，词边界判定一律不认。
//  3. 第三版按「整行就是一个文件路径」排噪音（^\s*\S+\.class$），单测全绿 ——
//     因为单测样本是我**按想象写的裸路径**。真机上这句前面还多一层 `Copying `，
//     是两个词，正则对不上。于是单测通过、真机满屏噪音。
//     教训：样本必须逐字抄自真实输出，不能凭印象造。
//
// 归纳出来的形状：噪音 =「动词 + 至多两个词 + 一段含 / 或 . 的路径」（后面可能再跟 1/1 进度）。
// 覆盖实测到的三族：
//
//	Copying net/minecraft/DefaultUncaughtExceptionHandler.class
//	Patching net/minecraft/server/level/ChunkHolder$ChunkLoadingFailure 1/1
//	Reading patch com.mojang.blaze3d.platform.Window$WindowInitFailed.binpatch   ← 中间多一个词
//
// 注意第三族：它的类名里带 Failed / Exception，会被 forgeSignalRe 当信号放行，
// 所以必须在噪音阶段就拦下 —— 这也说明「形状优先于关键字」是必需的。
// 路径里必须含 / 或 . 这条不能省，否则 "Patching failed" 这种真报错会被一起吃掉。
var forgeFileNoiseRe = regexp.MustCompile(`(?i)^\s*(?:copying|patching|reading|adding|writing|removing|renaming)\s+(?:\S+\s+){0,2}\S*[/.]\S*(?:\s+\d+/\d+)?\s*$`)

// 裸路径行（偶尔不带动词）
var forgeBarePathNoiseRe = regexp.MustCompile(`(?i)^\s*\S+\.(?:class|java|jar|tsrg|lzma|binpatch|json|txt)\s*$`)

// 「库文件记账」族：安装器给每个库报一行「要不要下、下没下成、校验过没」。
// 实测形状（前面的空格是原文就有的）：
//
//	    Checksum: 0 Exists: false
//	Considering library net.minecraftforge:forge:1.20.4-49.2.0:universal
//	    Download completed: Checksum validated.
//	Downloading library from https://libraries.minecraft.net/.../failureaccess-1.0.1.jar
//
// 它们本身没有信息量，本来就是靠「不在白名单里」被丢掉的。之所以还要单独挡住，
// 是因为库名里可能带 fail —— guava 的 **failureaccess** 就是活例子：
// 关键字规则会把这两行当报错捞起来。跟 DefaultUncaughtExceptionHandler.class 同一个病：
// **先按形状排除，再按关键字找信号**，顺序反了就会持续漏。
var forgeLedgerRe = regexp.MustCompile(`(?i)^\s*(?:Checksum:\s|Considering\s|Download completed\b|Downloading library from\s)`)

// isForgeNoise 判断这行是不是安装器的过程噪音。
func isForgeNoise(ln string) bool {
	return forgeFileNoiseRe.MatchString(ln) || forgeBarePathNoiseRe.MatchString(ln) || forgeLedgerRe.MatchString(ln)
}

// forgeSignalRe 值得让用户看到的行。
var forgeSignalRe = regexp.MustCompile(`(?i)(error|exception|fail|unable to|could not|cannot|unsupported|warn|denied|refused|not recognized|no such file)`)

// forgeNoMapReport 安装器里那句「Can't Find Class: com/mojang/brigadier/...」。
// 实测一次 1.20.4 安装有几百行（不同构建条数还不一样），全是重命名工具对无法映射的类名做的逐条报告 ——
// 装完之后服务端能正常启动，属于正常输出。逐条写进日志就是纯噪音，
// 但不写又不好（万一哪天真出问题，连痕迹都没有），所以改成最后汇总成一行。
const forgeNoMapReport = "Can't Find Class"

func interestingLine(ln string) bool {
	if strings.Contains(ln, forgeNoMapReport) || isForgeNoise(ln) {
		return false
	}
	if forgeSignalRe.MatchString(ln) {
		return true
	}
	// 白名单：宁可少写，别把 29698 行灌进去 —— 日志的意义是「出问题时能看」，
	// 不是「把源站的话原样转述一遍」。
	// 故意不含 "Host: " 和 "Target Directory"：那几行是给调试用的，
	// 对用户没有任何信息量，而我们自己记的「用哪个镜像」那行更准。
	for _, tag := range []string{
		"Extracting main jar", "Downloading libraries", "Building Processors",
		"installed successfully", "Data kindly mirrored",
	} {
		if strings.Contains(ln, tag) {
			return true
		}
	}
	return false
}

// lineSink 把子进程的输出切成行。stdout 和 stderr 会同时写进来，所以得带锁 ——
// 两个 goroutine 同时往一个 buffer 里塞，切出来的行会互相穿插。
type lineSink struct {
	mu  sync.Mutex
	buf []byte
	fn  func(string)
}

func (s *lineSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	for {
		i := bytes.IndexByte(s.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(s.buf[:i]), "\r")
		s.buf = s.buf[i+1:]
		s.fn(line)
	}
	// 防变态长行（处理器把整个 class 名列表打一行）把内存撑爆
	if len(s.buf) > 1<<20 {
		s.buf = s.buf[:0]
	}
	return len(p), nil
}

// flush 收尾时把没有换行结尾的最后一行也交出去
func (s *lineSink) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) > 0 {
		line := strings.TrimRight(string(s.buf), "\r\n")
		s.buf = s.buf[:0]
		if line != "" {
			s.fn(line)
		}
	}
}
