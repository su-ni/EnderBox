package daemon

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ansiRE 匹配服务端写到 stdout 上的 ANSI 控制序列。
//
// 为什么必须有这个：日志**文件**里是没有颜色码的（Paper 自己写文件前剥掉了），
// 但走管道给我们的那一路 stdout 带着全套颜色码。实测（2026-10-04，用户真机
// F:/sbx-realrun/logs/i1791098663892.log 含 1180 个 ESC）出现过的形态：
//
//	\x1b[33;1m  \x1b[91m  \x1b[93m  \x1b[97m  \x1b[37m  \x1b[3m  \x1b[4m  \x1b[0m  \x1b[m（参数为空）
//
// 结果就是用户在界面上看到一行 `[91m[37m[4m/help` 这种鬼东西 —— 不是服务端坏了，
// 是我们把终端控制码当正文存下来了。CSI 要排在 Fe 前面：`\x1b[` 本身也能被
// `\x1b[@-_]`（`[` 在 0x40–0x5F 里）吃掉，顺序反了就只剥掉一个 `\x1b[` 留下 "91m"。
var ansiRE = regexp.MustCompile(
	`\x1b\[[0-9;?]*[ -/]*[@-~]` + // CSI：颜色、加粗、下划线…
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)` + // OSC：改窗口标题之类
		`|\x1b[@-_]`) // 其余两字节转义

// stripANSI 去掉 ANSI 控制序列，正文原样保留。
// 不含 ESC 的行（绝大多数）走快路径，不做正则。
func stripANSI(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	return ansiRE.ReplaceAllString(s, "")
}

// normalizeConsoleLine 还原「终端上肉眼真正看到的那一行」，没内容的行返回空串。
//
// 服务端的控制台是终端语义：输出前先写提示符 "> "，再用 "\r  \r" 把它擦掉，
// 而 \r 只回行首、不换行。我们按 \n 切行，于是切出来的「一行」长这样：
//
//	"> \r  \r[18:14:45 INFO]: /weather: A Mojang provided command."
//
// 取最后一个 \r 之后的内容 —— 这正是终端上的效果（\r 后的内容覆盖写在同一行）。
// 不做这一步，日志行首就不是 [ 而是 "> \r"，
// 前端按 "^[HH:MM:SS LEVEL]" 解析级别的正则全部落空，整屏日志都显示成 LOG 级别。
func normalizeConsoleLine(raw string) string {
	line := raw
	if i := strings.LastIndexByte(line, '\r'); i >= 0 {
		line = line[i+1:]
	}
	// 只剩提示符本身（">" / "> " / 纯空格）的行没有信息量。
	// 不丢的话，服务端每输出一条日志，界面上就会多两条空行 —— 这正是之前
	// 日志里那些孤零零的 "> " 和 "  " 的来源。
	if strings.Trim(line, " >") == "" {
		return ""
	}
	return line
}

// Process 一个正在运行（或曾经运行）的服务端进程
type Process struct {
	inst *Instance

	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	status    Status
	startedAt *time.Time
	errMsg    string
	done      chan struct{}
	players   map[string]struct{}
	// intentional 人为停服标记。收尾 goroutine 靠它区分「崩溃」和「正常停止」——
	// 不能拿 status 当依据：Kill 要让状态立刻可读会先写成 Stopped，
	// 而 goroutine 只认 Stopping，于是「强制停止」被误记成崩溃、还会触发自动重启。
	//
	// 三条置位路径，缺一条就会出现「明明正常关服却被判崩溃」：
	//   ①Stop() / Kill()   —— 我们主动停机；
	//   ②Send("stop")      —— 用户在控制台手敲（这条曾经漏了，就是这次的 bug）；
	//   ③track() 看见 "Stopping server" —— 服务端自己承认在关服，最可靠。
	intentional bool
	// everReady 服务端成功启动过（日志里出现过就绪那行，见 isReadyLine）。
	// 只给「退出码 0 = 正常关机」这条兜底判据当前提：起不来就退出的
	// （eula 没同意、端口被占）绝不会先打出就绪行，所以不会被这条误判成正常。
	everReady bool

	logs *LogBuffer
	job  jobHandle
}

func newProcess(inst *Instance, logs *LogBuffer) *Process {
	return &Process{
		inst:   inst,
		status: StatusStopped,
		logs:   logs,
		done:   make(chan struct{}),
	}
}

// buildArgs 构造 java 启动参数
// 注意：Windows 中文系统下 java 的 stdout 默认跟随系统 locale（GBK），
// 强制指定 UTF-8 能避免大量乱码。latest.log 文件通道在 M2 接入。
func buildArgs(inst *Instance) []string {
	minMem, maxMem := inst.MinMem, inst.MaxMem
	if minMem <= 0 {
		minMem = 1024
	}
	if maxMem <= 0 {
		maxMem = 4096
	}
	args := []string{
		fmt.Sprintf("-Xms%dM", minMem),
		fmt.Sprintf("-Xmx%dM", maxMem),
		"-Dfile.encoding=UTF-8",
		"-Dstdout.encoding=UTF-8",
		"-Dstderr.encoding=UTF-8",
	}
	if inst.JVMArgs != "" {
		args = append(args, strings.Fields(inst.JVMArgs)...)
	}
	// Forge 新版（1.20.1-47.4.26 这种）根本不产出 jar：启动方式是安装器生成的参数文件
	// （run.bat 里那句 `java @user_jvm_args.txt @libraries/.../win_args.txt`）。
	// 这时要发 `@<文件>` 而不是 `-jar <文件>` —— java 对这两者的处理完全不同，
	// 用 -jar 打开一个 .txt 只会得到「Invalid or corrupt jarfile」。
	target := resolveJar(*inst)
	if isArgsFile(target) {
		// 用配置里存的那个路径（相对实例目录），别换成绝对路径：参数文件里写的是
		// 相对 classpath（libraries/...），java 按 cwd 解析，而 cwd 就是实例目录。
		args = append(args, "@"+inst.JarPath)
	} else {
		args = append(args, "-jar", target)
	}
	// nogui 是 Minecraft **服务端**的参数（别开那个图形界面）。
	// 代理端（Velocity / BungeeCord）既没有界面、也没有这个参数，
	// 塞进去只会被它当成未知参数。
	if !isProxy(inst.Type) {
		args = append(args, "nogui")
	}
	return args
}

// isArgsFile 这个「服务端文件」其实是 java 的参数文件（@file），不是 jar。
// Forge 1.17+ 的一部分构建就是这么发的（见 daemon.findForgeArgsFile）。
func isArgsFile(p string) bool {
	return strings.HasSuffix(strings.ToLower(filepath.FromSlash(p)), "_args.txt")
}

// Start 拉起进程
func (p *Process) Start(javaPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.status == StatusRunning || p.status == StatusStarting {
		return fmt.Errorf("实例已在运行")
	}

	cmd := exec.Command(javaPath, buildArgs(p.inst)...)
	cmd.Dir = p.inst.Dir
	// ⚠⚠ 这里**不能**图省事地加 `DETACHED_PROCESS` 让 java 脱离控制台。
	//
	// 试过了，代价是 `internal/daemon` 的核心护栏测试 `TestE2EConsoleStopIsNotCrash`
	// （停服三层判定，用真 Java 跑）**直接挂掉**：
	//     不加 → PASS，2.2 秒
	//     加   → FAIL，60 秒超时（等 "Done" 那一行等不到）
	//
	// 已排除两个想当然的解释：
	//   ① 不是「java 起不来」—— 单独跑的 flag 对照实验（`-version`）里，
	//      无 flag / DETACHED / NEW_PROCESS_GROUP / 两者都有，四个组合全部 67ms 正常返回；
	//   ② 不是「输出没 flush」—— 假服务端 `tools/fakeserver/FakeServer.java` 里
	//      明确调了 `System.out.flush()`。
	//
	// 结论：detached 之后，**这个常驻进程的 stdout 管道拿不到数据**，但根因未定位。
	// 想彻底解决「关面板别把服也关了」，需要的是**绕开管道**的方案（读 `logs/latest.log` +
	// 命令走 RCON），那是另一件事，不能靠一个 flag 蒙过去。详见 `docs/PROCESS-LIFECYCLE.md`。

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	p.cmd = cmd
	p.stdin = stdin
	p.status = StatusRunning
	now := time.Now()
	p.startedAt = &now
	p.errMsg = ""
	p.intentional = false
	p.everReady = false
	p.done = make(chan struct{})

	// 挂到 Job Object，保证崩了也能把整棵进程树带走
	p.job, _ = assignToJob(cmd.Process)

	go p.pump(stdout)
	go p.pump(stderr)

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if stoppedGracefully(p.intentional, p.everReady, err) {
			p.status = StatusStopped
		} else {
			p.status = StatusCrashed
			if err != nil {
				p.errMsg = err.Error()
			}
		}
		p.mu.Unlock()
		closeJob(p.job)
		p.job = 0
		close(p.done)
	}()

	return nil
}

func (p *Process) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		// 两步都得做，而且必须在最前面：
		//   剥颜色码 —— track() 认「Done (」这种关键字，带码的 Done 会匹配不上；
		//   剥提示符 —— 行首的 "> \r" 会让前端的级别正则全部落空。
		line := normalizeConsoleLine(stripANSI(sc.Text()))
		if line == "" {
			continue
		}
		p.logs.Append(line)
		p.track(line)
	}
}

// stoppedGracefully 这次退出算「正常停止」吗？收尾 goroutine 靠它决定写不写崩溃档、
// 要不要触发自动重启。
//
// 三条判据，任意一条成立就算正常：
//
//	①intentional —— 我们或用户明确要求的（Stop / Kill / 控制台手敲 stop）；
//	②服务端自曝关服 —— 看到 "Stopping server" 时 track 置的那一位（也在 intentional 上）；
//	③退出码 0（cmd.Wait 返回 nil）且确实成功启动过 —— 它自己把 main 跑完了，
//	  不是被人打死的。强杀（taskkill /F）和 JVM 抛异常都会是非 0 退出码。
//
// 全不成立才是崩溃 —— 这正是「服务端起不来」和「被 OOM/插件搞死」的形态。
//
// 抽成纯函数是为了能被单测钉死：这套判据一旦漏掉某条停机路径，表现就是
// 「明明正常关服却报崩溃 + 乱重启」，用户直接看得见，不能只靠真机去试。
// waitErr 就是 exec.Cmd.Wait 的返回值：nil == 退出码 0。
func stoppedGracefully(intentional, everReady bool, waitErr error) bool {
	if intentional {
		return true
	}
	return waitErr == nil && everReady
}

// Send 向控制台写入一条命令，并把这条命令回显进日志。
// 不回显的话，用户敲完 list 会盯着屏幕怀疑自己到底按没按回车 ——
// 服务端对很多命令的回应要等好几秒。
func (p *Process) Send(cmd string) error {
	cmd = normalizeCommand(cmd)
	if cmd == "" {
		return fmt.Errorf("命令是空的")
	}
	p.mu.Lock()
	// 用户手敲 stop 走的是这条通用命令通道，不是 Stop() 那个专用停机流程。
	// 标记必须在这里补上：否则服务端优雅退出了，收尾 goroutine 却因为没有标记
	// 把它判成崩溃 —— 存档、报「已崩溃」、还自动重启，用户一脸问号。
	//
	// 必须在写 stdin **之前**置位：服务端可能几毫秒内就退出了，
	// 收尾 goroutine 一旦抢先判定，这一笔就再也补不回来。
	if stopCommands[strings.ToLower(cmd)] {
		p.intentional = true
	}
	w := p.stdin
	p.mu.Unlock()
	if w == nil {
		return fmt.Errorf("进程未运行")
	}
	p.logs.Append("[EnderBox] > " + cmd)
	_, err := io.WriteString(w, cmd+"\n")
	return err
}

// stopCommands 敲下去就等于「我要关服」的控制台命令。
// 按各服务端实现的别名收：原版/Paper/Forge/Fabric 都是 stop，
// CraftBukkit 系另认 end。代理端（Velocity/BungeeCord）同样认 stop。
var stopCommands = map[string]bool{
	"stop": true,
	"end":  true,
}

// normalizeCommand 把用户敲的东西收拾成服务端控制台认识的样子。
//
// 关键的一条：**服务端控制台不认开头的斜杠**。`/help` 是游戏内聊天的写法，
// 直接喂进 stdin 会被判成「未知或不完整的命令」。这不是猜的 ——
// 实测 2026-10-04 同一个 Paper 1.21.4：先敲 help 正常返回帮助索引，
// 再敲 /help 立刻报 Unknown or incomplete command（错误光标就停在那根斜杠上）。
// 用户从 wiki/攻略复制命令时几乎都带斜杠，所以在这里替他抹掉，而不是让他自己发现。
func normalizeCommand(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	// 只剥一层：`//foo` 在服务端没有意义，剥一层后剩下的斜杠会让服务端自己报错，
	// 比我们猜他要什么更诚实。
	cmd = strings.TrimPrefix(cmd, "/")
	return strings.TrimSpace(cmd)
}

// Stop 优雅停止：先发 stop 命令，超时再强杀
func (p *Process) Stop(timeout time.Duration) error {
	p.mu.Lock()
	if p.status != StatusRunning {
		p.mu.Unlock()
		return nil
	}
	p.status = StatusStopping
	p.intentional = true
	p.mu.Unlock()

	_ = p.Send("stop")

	select {
	case <-p.done:
		return nil
	case <-time.After(timeout):
		return p.Kill()
	}
}

// Kill 强杀整棵进程树
func (p *Process) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	terminateJob(p.job)
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
		// 收尾 goroutine 已经跑完并定过状态了（真的崩了 / 早就停了），别覆盖它的结论
	default:
		p.intentional = true
		p.status = StatusStopped
	}
	return nil
}

func (p *Process) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Process) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) StartedAt() *time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedAt
}

func (p *Process) Error() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errMsg
}

func (p *Process) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}
