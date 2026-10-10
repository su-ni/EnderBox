package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// 崩溃日志判读：把「最后 200 行」变成「现象 / 原因 / 怎么办」。
//
// 设计原则和 preflight 一致 —— 只给结论是不够的，每条都要能落地：
//   · Title  现象：一句人话，说清楚发生了什么
//   · Cause  原因：为什么会这样（用户看不懂的那段英文到底在说什么）
//   · Action 怎么办：下一步具体做什么
//   · Evidence 命中的原始行：给人看证据，不是凭空下判断
//   · Fix    能一键做的就带上 preflight 那套 fix 标识（复用同一个 /fix 通道）
//
// 规则是纯函数，不发任何外部请求，可离线、可单测。顺序即「越具体越靠前」。

// Diagnosis 一条崩溃判读结论。
type Diagnosis struct {
	Code     string `json:"code"`               // 稳定标识，前端按它选图标 / 文案
	Severity string `json:"severity"`           // fatal=这次崩溃的直接原因；warn=可能有关系
	Title    string `json:"title"`              // 现象
	Cause    string `json:"cause"`              // 原因
	Action   string `json:"action"`             // 怎么办
	Evidence string `json:"evidence,omitempty"` // 命中的原始行（已截断）
	Fix      string `json:"fix,omitempty"`      // 复用 preflight 的 fix 通道；空=只能手动
}

// CrashReport 磁盘上的一份崩溃现场（<id>-crash-<时间>.log）。
type CrashReport struct {
	File      string      `json:"file"`
	Time      string      `json:"time"`
	ExitInfo  string      `json:"exitInfo,omitempty"`
	LineCount int         `json:"lineCount"`
	Diagnoses []Diagnosis `json:"diagnoses"`
}

// crashRule 一条匹配规则。keywords 全部小写，命中任意一个即算匹配。
type crashRule struct {
	code     string
	severity string
	title    string
	cause    string
	action   string
	fix      string
	keywords []string
	// extract 从整段日志里再抠一点可读信息（比如模组名），可为 nil。
	// 传整段而不是只传命中行：模组名往往写在别处（"Mod ID: 'xxx'" 常在异常栈前面）。
	extract func(lines []string) string
}

const crashEvidenceMax = 240

var (
	modIDRe   = regexp.MustCompile(`(?i)(?:mod\s*id|modid|mod_id|模组\s*id)\s*[:：]\s*'?"?([A-Za-z0-9_.\-]{2,40})`)
	fromModRe = regexp.MustCompile(`(?i)from\s+mod\s+([A-Za-z0-9_.\-]{2,40})`)
)

// extractMod 尽量从一行里认出「是哪个模组干的」。认不出就返回空串 ——
// 宁可不说，也不要随便安一个名字上去让用户删错东西。
func extractMod(line string) string {
	if m := modIDRe.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	if m := fromModRe.FindStringSubmatch(line); len(m) == 2 {
		return m[1]
	}
	return ""
}

// extractModAll 在整段日志里找第一个能认出的模组名。
func extractModAll(lines []string) string {
	for _, l := range lines {
		if m := extractMod(l); m != "" {
			return m
		}
	}
	return ""
}

// modHint 给「模组相关」的规则复用：认出模组名就附在标题后面。
func modHint(lines []string) string {
	if m := extractModAll(lines); m != "" {
		return "（疑似模组：" + m + "）"
	}
	return ""
}

// distModRe 从 Forge 的 mixin handler 名里抠模组 id。
// 实测形态（客户端模组撞专用服务器时）：
//
//	ResourceLocation.handler$bbf000$entity_texture_features$etf$illegalPathOverride
//	                  ^^^^^^^^^^^^^^^^^^^ 这段就是模组 id，后面的 $etf$ 是它的简写
//
// 前面的 6 位 hex 是 Forge 给每个 mixin 配的随机前缀，不能当模组名；所以从 handler$ 起算。
var distModRe = regexp.MustCompile(`handler\$[0-9a-fA-F]{4,}\$([a-z0-9_]{3,40})\$`)

// distModHint 给「客户端模组混进服务端」那条用：优先抠 handler 里的模组 id —— 那种崩溃现场
// 最准的线索就是它（模组自己的名字反倒不一定出现在这 200 行里）。抠不到再退回通用 modHint。
func distModHint(lines []string) string {
	for _, l := range lines {
		if m := distModRe.FindStringSubmatch(l); len(m) == 2 {
			return "（疑似模组：" + m[1] + "）"
		}
	}
	return modHint(lines)
}

// crashRules 规则表。顺序 = 展示顺序（同为 fatal 时）。
// 判据是真实服务端日志里的原话，改之前先去翻一份真的崩溃日志。
var crashRules = []crashRule{
	{
		code: "eula_pending", severity: "fatal",
		title:  "还没同意 Minecraft EULA",
		cause:  "服务端第一次启动会读 eula.txt，里面不是 eula=true 就直接退出，日志里只有一句英文。",
		action: "点下面的按钮替你写好 eula.txt（等同于同意 Minecraft 最终用户许可协议），然后重新启动。",
		fix:    "accept_eula",
		keywords: []string{
			"you need to agree to the eula", "agree to the eula",
			"failed to load eula.txt", "eula.txt",
		},
	},
	{
		code: "java_too_old", severity: "fatal",
		title:  "Java 版本比服务端要求的旧",
		cause:  "服务端是用更高版本的 Java 编译的，本机这个 Java 跑不动它，JVM 会在加载主类时直接拒绝。",
		action: "给这个实例换一个更新的 Java：到「配置 → Java 路径」里选一个版本够的（1.20.5 及以后要 Java 21，1.17–1.20.4 要 Java 17）。",
		fix:    "rescan_java",
		keywords: []string{
			"unsupportedclassversionerror", "has been compiled by a more recent version",
			"unsupported class file major version", "class file version",
			"requires at least java", "requires java",
		},
	},
	{
		code: "jar_corrupt", severity: "fatal",
		title:  "服务端文件不完整或根本不对",
		cause:  "这个 jar 要么没下完（半截文件），要么不是可启动的服务端（比如把安装器当成了服务端），JVM 找不到主类。",
		action: "重新下载一份服务端文件。如果是 Forge/NeoForge，要先跑完安装器、用安装生成的那个 jar 启动，别直接启动 -installer.jar。",
		fix:    "download_jar",
		keywords: []string{
			"invalid or corrupt jarfile", "no main manifest attribute",
			"could not find or load main class", "unable to access jarfile",
			"failed to load main-class", "error: unable to access jarfile",
		},
	},
	{
		code: "port_in_use", severity: "fatal",
		title:  "端口被别的程序占着",
		cause:  "服务端想绑定的端口已经有程序在监听了 —— 常见的是「上一个实例还没退干净」，或者别的软件占了这个端口。",
		action: "换一个端口（配置页里改），或者先把占用它的程序停掉。同一个端口不能同时开两个服务端。",
		keywords: []string{
			"address already in use", "failed to bind to port", "bindexception",
			"perhaps a server is already running on that port",
			"only one usage of each socket address",
		},
	},
	{
		code: "out_of_memory", severity: "fatal",
		title:  "内存不够，服务端被撑爆了",
		cause:  "分配给它内存用满了，JVM 直接抛 OutOfMemoryError。视距/模拟距离开太大、模组太多、同时在线的人太多都会推高占用。",
		action: "把实例的内存上限调大一些（配置页 → 内存），或者在 server.properties 里把 view-distance、simulation-distance 调小一档。",
		keywords: []string{
			"outofmemoryerror", "java heap space", "gc overhead limit exceeded",
			"unable to create native thread", "unable to allocate",
		},
	},
	{
		code: "missing_dependency", severity: "fatal",
		title:  "模组缺依赖，装不全",
		cause:  "装了一个模组，但它需要的前置模组（或对应版本）不在 mods 目录里，Forge/Fabric 在加载阶段就中止了。",
		action: "按日志里点名的缺失项补上前置模组；模组之间还要注意「游戏版本 + 加载器版本」要一致，混装不同 MC 版本的模组同样起不来。",
		keywords: []string{
			"missing or unsupported mandatory dependencies", "missingmodsexception",
			"incompatible mod set", "missing dependency", "which is missing",
		},
	},
	{
		code: "client_mod_on_server", severity: "fatal",
		title:  "整合包里混进了「客户端专属」模组",
		cause:  "有个模组只能在客户端跑（小地图、实体纹理、界面美化这类），装到服务端后它去加载客户端才有的界面类，Forge 在「专用服务器」模式下会当场拒绝加载、主线程直接退出。多数整合包是给单人游戏准备的，里边的客户端模组不会自动剔除 —— 哪怕它标着「服务端可用」。",
		action: "把日志里点名（或证据里出现）的那个模组从 mods 目录挪出去再启动。想事先确认某个模组属于哪一侧，到「模组市场」搜它，看「客户端 / 服务端」标记。",
		extract: distModHint,
		// 这条**没有**后端一键修复：该处置的是整个 mods 目录，而不是崩掉的那一个 ——
		// Forge 在第一个客户端模组就崩了，禁掉一个、重启还会崩下一个。
		// 前端认这个码：跳到「插件与模组」并自动跑一次核对（按 jar 哈希问源站）。
		fix: "audit_client_mods",
		keywords: []string{
			"invalid dist dedicated_server",
			"attempted to load class net/minecraft/client",
		},
	},
	{
		code: "mod_crash", severity: "fatal",
		title:   "某个模组把服务端搞崩了",
		cause:   "加载器在运行/加载模组时抛了异常，最后走到了 tick loop 崩溃或 Mod Loading 失败。多半是单个模组的 bug 或版本不兼容。",
		action:  "看下面证据里点名的模组，先把它单独挪出 mods 目录再启动：能起来就说明是这个模组的问题，去找它的兼容版本或作者反馈。",
		extract: modHint,
		keywords: []string{
			"exception in server tick loop", "the game crashed", "crashed on",
			"mod loading has failed", "loadingfailedexception", "failed to load mod",
			"has crashed",
		},
	},
	{
		code: "world_corrupt", severity: "fatal",
		title:  "世界存档读不出来",
		cause:  "服务端打不开 world 目录，可能是存档损坏（上次异常断电 / 磁盘写满），也可能是存档目录被挪走或权限不对。",
		action: "到「备份与还原」里用最近的备份还原回去。如果还没来得及备份过，就把 world 目录改名留档、让服务端重新生成一个新世界先跑起来。",
		keywords: []string{
			// 注意：这里不能放裸的 "corrupt" —— 它会误命中「Invalid or corrupt jarfile」，
			// 把「jar 坏了」也判成「存档坏了」，两个修复方向完全不同。
			"failed to load level", "not a valid level", "chunk file at",
			"world might be corrupted", "level.dat",
		},
	},
	{
		code: "forge_install_failed", severity: "fatal",
		title:  "Forge/NeoForge 安装没跑完",
		cause:  "服务端安装器在拉取库文件或打补丁时失败了（多半是网络中断），服务端本体并没有真正生成。",
		action: "重新跑一次「安装服务端」。安装过程需要联网下载几十到上百 MB 的库文件，网络不稳时容易断在中途。",
		keywords: []string{
			"there was an error during the installation", "installation failed",
			"failed to install", "the installation has failed",
		},
	},
	{
		code: "mixin_failed", severity: "warn",
		title:   "模组的 Mixin 没注入成功",
		cause:   "模组试图改写游戏代码（Mixin）时失败，通常发生在模组版本和游戏/加载器版本对不上时。",
		action:  "确认出问题的模组支持当前的游戏版本和加载器版本；同类功能模组不要装多个，容易互相打架。",
		extract: modHint,
		keywords: []string{
			"mixin apply failed", "mixinapplyexception",
			"mixintransformationexception", "mixin transformation of",
		},
	},
	{
		code: "duplicate_mod", severity: "warn",
		title:    "同一个模组装了重复的版本",
		cause:    "mods 目录里存在同一模组的多个版本，加载器不知道该用哪个，直接报错。",
		action:   "打开实例的 mods 目录，同一个模组只留一个版本（留和当前游戏版本匹配的那个），其余删掉。",
		keywords: []string{"duplicatemods", "duplicate mod", "found duplicate"},
	},
	{
		code: "file_lock", severity: "warn",
		title:  "文件被别的进程占用",
		cause:  "服务端要读写的文件被另一个程序锁住了 —— 常见的是「上一次的服务端进程其实还活着」，或者压缩软件/网盘在占用存档目录。",
		action: "确认没有残留在任务管理器里的 java.exe；把实例目录移出被网盘同步/压缩软件监控的路径，再启动。",
		keywords: []string{
			"accessdeniedexception", "the process cannot access the file",
			"being used by another process", "filesystemexception",
		},
	},
	{
		code: "network", severity: "warn",
		title:  "联网下载失败",
		cause:  "服务端在启动过程中需要联网（首次启动下依赖、装模组、正版验证等），这一步没连上。",
		action: "确认这台机器能上网；如果是模组服务端，第一次启动必须联网把库文件拉全。网络恢复后重新启动即可，已下好的部分不会白费。",
		keywords: []string{
			"unknownhostexception", "connection refused", "connectexception",
			"failed to download", "could not download", "network is unreachable",
		},
	},
	{
		code: "version_mismatch", severity: "warn",
		title:  "客户端和服务端版本对不上",
		cause:  "有玩家用不匹配的客户端版本连了进来，或者服务端协议版本与预期不一致。",
		action: "让玩家换成和服务端一致的版本再进；整合包服要确保大家用的是同一份整合包。",
		keywords: []string{
			"unsupported protocol version", "outdated server", "outdated client",
			"you are running an unsupported version",
		},
	},
}

// AnalyzeCrash 对崩溃现场做规则匹配。纯函数，单测直接调它。
// exitInfo 是进程的退出信息（「exit status 1」这类），单独传进来一起当作线索。
func AnalyzeCrash(lines []string, exitInfo string) []Diagnosis {
	// 退出信息本身也可能带线索（少数启动失败会写在这里）
	hay := make([]string, 0, len(lines)+1)
	if strings.TrimSpace(exitInfo) != "" {
		hay = append(hay, exitInfo)
	}
	hay = append(hay, lines...)

	out := []Diagnosis{}
	seen := map[string]bool{}

	for _, rule := range crashRules {
		line, ok := matchAny(hay, rule.keywords)
		if !ok {
			continue
		}
		if seen[rule.code] {
			continue
		}
		seen[rule.code] = true

		title := rule.title
		if rule.extract != nil {
			if extra := rule.extract(hay); extra != "" {
				title += extra
			}
		}
		out = append(out, Diagnosis{
			Code:     rule.code,
			Severity: rule.severity,
			Title:    title,
			Cause:    rule.cause,
			Action:   rule.action,
			Evidence: truncateLine(line, crashEvidenceMax),
			Fix:      rule.fix,
		})
	}

	if len(out) == 0 {
		out = append(out, unknownDiagnosis(hay))
	}
	return out
}

// matchAny 找第一个命中任意关键词的行。全部按小写比对，服务端日志大小写不统一。
func matchAny(lines []string, keywords []string) (string, bool) {
	for _, raw := range lines {
		// 我们自己的收尾提示不是证据（里面常常复述了错误名），跳过
		if strings.HasPrefix(raw, "[EnderBox]") {
			continue
		}
		low := strings.ToLower(raw)
		for _, kw := range keywords {
			if strings.Contains(low, kw) {
				return raw, true
			}
		}
	}
	return "", false
}

// unknownDiagnosis 一条规则都没命中时的兜底。
// 「不知道」也要给出下一步，而不是丢一句「未知错误」让用户干瞪眼。
func unknownDiagnosis(lines []string) Diagnosis {
	ev := ""
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" || strings.HasPrefix(l, "[EnderBox]") {
			continue
		}
		low := strings.ToLower(l)
		if strings.Contains(low, "exception") || strings.Contains(low, "error") ||
			strings.Contains(low, "caused by") || strings.Contains(low, "failed") ||
			strings.Contains(low, "crash") {
			ev = l
			break
		}
	}
	if ev == "" {
		for i := len(lines) - 1; i >= 0; i-- {
			if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "[EnderBox]") {
				ev = l
				break
			}
		}
	}
	return Diagnosis{
		Code:     "unknown",
		Severity: "warn",
		Title:    "服务端异常退出，但没匹配到常见死法",
		Cause:    "日志里没有我们认得的典型错误特征，可能是比较少见的问题，也可能是配置里某个值填得不对。",
		Action:   "把日志往下翻到最后一段 Java 堆栈，从「Caused by:」那几行往往能看到真正的原因；也可以把最后这段贴到社区或群里问。",
		Evidence: truncateLine(ev, crashEvidenceMax),
	}
}

func truncateLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max]) + "…"
}

// ── 崩溃现场的读取 ──────────────────────────────────────────────

// crashFileName 崩溃现场文件名是 <实例id>-crash-<yyyyMMdd-HHmmss>.log。
// 用它同时做「找文件」和「校验外部传进来的文件名」，防止路径穿越。
var crashFileRe = regexp.MustCompile(`-crash-\d{8}-\d{6}\.log$`)

func crashLogDir(baseDir string) string { return filepath.Join(baseDir, "logs") }

// CrashReports 列出某个实例的崩溃现场，最新的在前，并带上规则判读结果。
// limit <= 0 时默认取 5 份，最多 20 份。
func (m *Manager) CrashReports(id string, limit int) []CrashReport {
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	entries, err := os.ReadDir(crashLogDir(m.baseDir))
	if err != nil {
		return []CrashReport{}
	}

	prefix := id + "-crash-"
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasPrefix(n, prefix) || !crashFileRe.MatchString(n) {
			continue
		}
		names = append(names, n)
	}
	// 文件名里的时间戳是定长的，字典序倒排就是时间倒排
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > limit {
		names = names[:limit]
	}

	out := make([]CrashReport, 0, len(names))
	for _, n := range names {
		raw, err := os.ReadFile(filepath.Join(crashLogDir(m.baseDir), n))
		if err != nil {
			continue
		}
		rep := parseCrashFile(string(raw))
		rep.File = n
		out = append(out, rep)
	}
	return out
}

// CrashReportText 读回一份崩溃现场的原文，给界面「查看原始日志」用。
func (m *Manager) CrashReportText(id, file string) (string, error) {
	if file != filepath.Base(file) || !strings.HasPrefix(file, id+"-crash-") || !crashFileRe.MatchString(file) {
		return "", fmt.Errorf("崩溃文件名不合法")
	}
	b, err := os.ReadFile(filepath.Join(crashLogDir(m.baseDir), file))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// parseCrashFile 拆开 WriteCrashSnapshot 写的那份文件：先几行 # 头，空行之后是正文。
func parseCrashFile(raw string) CrashReport {
	rep := CrashReport{Diagnoses: []Diagnosis{}}
	lines := strings.Split(raw, "\n")

	exitInfo := ""
	i := 0
	for ; i < len(lines); i++ {
		l := lines[i]
		if !strings.HasPrefix(l, "# ") {
			break
		}
		switch {
		case strings.HasPrefix(l, "# 时间："):
			rep.Time = strings.TrimSpace(strings.TrimPrefix(l, "# 时间："))
		case strings.HasPrefix(l, "# 退出信息："):
			exitInfo = strings.TrimSpace(strings.TrimPrefix(l, "# 退出信息："))
		}
	}
	// 跳过头与正文之间的空行
	for ; i < len(lines) && strings.TrimSpace(lines[i]) == ""; i++ {
	}
	body := lines[i:]
	// 文件末尾的换行会 Split 出一个空串，别把它算成一行日志
	for len(body) > 0 && strings.TrimSpace(body[len(body)-1]) == "" {
		body = body[:len(body)-1]
	}

	rep.ExitInfo = exitInfo
	rep.LineCount = len(body)
	rep.Diagnoses = AnalyzeCrash(body, exitInfo)
	return rep
}
