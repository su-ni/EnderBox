package daemon

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/mem"
)

// Issue 一条启动前检查结果。
// Level 只有两档：block 必须解决才能起服；warn 提个醒，起不起来由服务端自己决定。
// Code 是给前端做图标/一键修复用的稳定标识，别拿 Title 当条件判断。
type Issue struct {
	Code   string `json:"code"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // 空表示只能手动处理
}

// PreflightResult 启动前的体检报告
type PreflightResult struct {
	OK        bool    `json:"ok"` // 没有任何 blocker
	JarPath   string  `json:"jarPath"`
	JarFound  bool    `json:"jarFound"`
	JavaPath  string  `json:"javaPath"`
	JavaMajor int     `json:"javaMajor"`
	NeedJava  int     `json:"needJava"`
	Port      int     `json:"port"`
	Issues    []Issue `json:"issues"`
}

// PreflightError 带体检报告的启动失败。
// 只返回一个字符串的话，前端就只剩「启动失败」四个字可显示 ——
// 把结构化结果一起带出去，界面才能告诉用户到底卡在哪、点哪里能修。
type PreflightError struct {
	Result PreflightResult
}

func (e *PreflightError) Error() string {
	var parts []string
	for _, i := range e.Result.Issues {
		if i.Level == "block" {
			parts = append(parts, i.Title)
		}
	}
	if len(parts) == 0 {
		return "启动前检查未通过"
	}
	return "启动前检查未通过：" + strings.Join(parts, "；")
}

// noEULA 代理端没有 eula 这一步，拿原版那套去卡它会永远起不来
func noEULA(t Type) bool {
	return t == TypeVelocity || t == TypeBungee
}

// JarPathOf 导出给 api 层用：下载服务端 jar 时要落在同一个位置，
// 否则「下完了却起不来」—— 因为 Start 找的是 resolveJar 的结果。
func JarPathOf(inst Instance) string { return resolveJar(inst) }

// resolveJar 把 jarPath 还原成绝对路径。
// 配置里存的是相对实例目录的名字（server.jar），也可能有人填了绝对路径。
func resolveJar(inst Instance) string {
	if inst.JarPath == "" {
		return filepath.Join(inst.Dir, "server.jar")
	}
	if filepath.IsAbs(inst.JarPath) {
		return inst.JarPath
	}
	return filepath.Join(inst.Dir, inst.JarPath)
}

// EULAAccepted 读 eula.txt 判断有没有同意。
// 只认「eula=」这一项，注释行和别的键一律无视 —— 别因为文件里出现 true 就误判。
func EULAAccepted(inst Instance) bool {
	b, err := os.ReadFile(filepath.Join(inst.Dir, "eula.txt"))
	if err != nil {
		return false
	}
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, "=")
		if i <= 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(line[:i]), "eula") {
			return strings.EqualFold(strings.TrimSpace(line[i+1:]), "true")
		}
	}
	return false
}

// AcceptEULA 替用户把 eula.txt 写好。
// 原文件存在就只替换 eula= 那一行，保留其它内容和注释（那是服务端自己生成的文件，别整个覆盖掉）。
func (m *Manager) AcceptEULA(id string) error {
	inst, ok := m.st.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}
	p := filepath.Join(inst.Dir, "eula.txt")

	head := "# 由 ServerBox 写入。eula=true 表示你同意 Minecraft 最终用户许可协议\n" +
		"# https://aka.ms/MinecraftEULA\n"

	if b, err := os.ReadFile(p); err == nil {
		lines := strings.Split(string(b), "\n")
		replaced := false
		for i, raw := range lines {
			t := strings.TrimSpace(raw)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if j := strings.Index(t, "="); j > 0 && strings.EqualFold(strings.TrimSpace(t[:j]), "eula") {
				lines[i] = "eula=true"
				replaced = true
				break
			}
		}
		if !replaced {
			lines = append(lines, "eula=true")
		}
		return os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644)
	}

	return os.WriteFile(p, []byte(head+"eula=true\n"), 0o644)
}

// portFree 试着占一下这个端口。占得上说明空着，占不上就认为被占用。
// 注意：Windows 上 Hyper-V / WSL 会预留端口段，会出现「明明没有程序在跑却绑不上」的假占用，
// 所以这条只算 warn，不能当 blocker 把用户卡死。
func portFree(port int) bool {
	if port <= 0 {
		return true
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// Preflight 启动前体检。检查顺序按「用户最容易踩、报错最难懂」排。
func (m *Manager) Preflight(id string) PreflightResult {
	inst, ok := m.st.Get(id)
	if !ok {
		return PreflightResult{Issues: []Issue{{
			Code: "no_instance", Level: "block", Title: "实例不存在",
			Detail: "这个实例已经不在配置里了。",
		}}}
	}

	need := JavaForVersion(inst.MCVersion)
	res := PreflightResult{
		NeedJava: need,
		JarPath:  resolveJar(inst),
		Port:     inst.Port,
		Issues:   []Issue{},
	}

	// ── 1. 服务端文件 ──
	if st, err := os.Stat(res.JarPath); err != nil || st.IsDir() {
		detail := fmt.Sprintf("找不到 %s。", res.JarPath)
		if alt := findAnyJar(inst); alt != "" {
			detail += fmt.Sprintf("不过这个目录里有 %s —— 如果就是想用它，把「服务端文件」改成这个名字就行。", filepath.Base(alt))
		} else {
			// 目录是空的：直接说清楚要干什么，别只说「文件不存在」
			detail += "服务端 jar 得先放进去，这个目录现在是空的。"
		}
		res.Issues = append(res.Issues, Issue{
			Code: "jar_missing", Level: "block",
			Title:  "缺少服务端文件",
			Detail: detail,
		})
	} else {
		res.JarFound = true
	}

	// ── 2. eula ──
	if !noEULA(inst.Type) && !EULAAccepted(inst) {
		res.Issues = append(res.Issues, Issue{
			Code: "eula_pending", Level: "block",
			Title:  "还没同意 Minecraft EULA",
			Detail: "服务端第一次启动会读 eula.txt，里面不是 eula=true 就直接退出，日志里只有一句英文。",
			Fix:    "accept_eula",
		})
	}

	// ── 3. Java ──
	if inst.JavaPath != "" {
		// 用户指定过路径，尊重它，但仍然验一下版本够不够
		if rt, ok := probeJava(inst.JavaPath); ok {
			res.JavaPath = rt.Path
			res.JavaMajor = rt.Major
			if rt.Major < need {
				res.Issues = append(res.Issues, Issue{
					Code: "java_too_old", Level: "block",
					Title:  fmt.Sprintf("Java 版本太低（有 %d，需要 %d）", rt.Major, need),
					Detail: "实例里指定的这个 java 跑不动该版本的服务端，会直接抛 UnsupportedClassVersionError。",
					Fix:    "rescan_java",
				})
			}
		} else {
			res.Issues = append(res.Issues, Issue{
				Code: "java_broken", Level: "block",
				Title:  "指定的 Java 用不了",
				Detail: fmt.Sprintf("实例里填的 %s 跑不出 java -version，可能已经被卸载或挪走了。", inst.JavaPath),
				Fix:    "rescan_java",
			})
		}
	} else if rt, err := m.ResolveJava(need); err == nil {
		res.JavaPath = rt.Path
		res.JavaMajor = rt.Major
	} else {
		res.Issues = append(res.Issues, Issue{
			Code: "java_missing", Level: "block",
			Title:  fmt.Sprintf("这台机器上没有可用的 Java %d", need),
			Detail: err.Error() + "。到设置页的「Java 运行时」扫一遍，或者装一个 Eclipse Temurin。",
			Fix:    "rescan_java",
		})
	}

	// ── 4. 端口 ──
	// 实例正在跑的时候，占用它的就是它自己，跳过检查免得自己告自己
	running := false
	if p, ok := m.proc(id); ok {
		running = p.Status() == StatusRunning || p.Status() == StatusStarting
	}
	if !running && !portFree(inst.Port) {
		res.Issues = append(res.Issues, Issue{
			Code: "port_busy", Level: "warn",
			Title: fmt.Sprintf("端口 %d 现在被占着", inst.Port),
			Detail: "可能是另一个实例、别的程序，也可能是 Windows 给 Hyper-V/WSL 预留的端口段。" +
				"真起不来就把端口换一个，或者先停掉占用的程序。",
		})
	}

	// ── 5. server.properties 端口一致性 ──
	if props, err := m.ReadProperties(id); err == nil {
		for _, row := range props {
			if row.Key != "server-port" {
				continue
			}
			if n, err := strconv.Atoi(row.Value); err == nil && n != inst.Port {
				res.Issues = append(res.Issues, Issue{
					Code: "port_mismatch", Level: "warn",
					Title: fmt.Sprintf("server.properties 里的端口是 %d，实例配置是 %d", n, inst.Port),
					Detail: "服务端实际监听的是 server.properties 里那个值 —— 实例配置只是显示用的。" +
						"以实例配置为准的话，点一下同步。",
					Fix: "sync_port",
				})
			}
			break
		}
	}

	// ── 6. 内存上限 ──
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		totalMB := int(vm.Total / 1024 / 1024)
		if totalMB > 0 && inst.MaxMem > totalMB-(totalMB/10) {
			res.Issues = append(res.Issues, Issue{
				Code: "mem_over", Level: "warn",
				Title: fmt.Sprintf("分配的内存（%d MB）超过本机可用", inst.MaxMem),
				Detail: fmt.Sprintf("这台机器一共 %d MB 内存，留给系统的起码要有几百 MB。"+
					"给太满会让整台机器开始换页，服务器反而更卡。", totalMB),
			})
		}
	}

	res.OK = true
	for _, i := range res.Issues {
		if i.Level == "block" {
			res.OK = false
			break
		}
	}
	return res
}

// findAnyJar 在实例目录里找有没有别的 jar，用来提示「是不是名字填错了」
func findAnyJar(inst Instance) string {
	entries, err := os.ReadDir(inst.Dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && jarRe.MatchString(e.Name()) {
			return e.Name()
		}
	}
	return ""
}
