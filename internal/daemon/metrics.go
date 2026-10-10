package daemon

import (
	"sort"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/process"
)

// Metrics 实例实时指标，前端仪表盘的数据源
type Metrics struct {
	// IsRunning 字段名不叫 Running，是为了不和 StatusRunning 这个常量撞名（读起来分不清）
	IsRunning bool    `json:"running"`
	CPU       float64 `json:"cpu"`       // 进程 CPU 占用百分比
	MemUsed   int64   `json:"memUsed"`   // MB，进程实际驻留
	MemMax    int64   `json:"memMax"`    // MB，实例配置的堆上限
	Players   int     `json:"players"`   // 在线玩家数
	UptimeSec int64   `json:"uptimeSec"` // 已运行秒数
	PID       int     `json:"pid"`
}

// isGracefulShutdownLine 判断这一行是不是服务端在宣布「我要关服了」。
//
// 措辞逐字抄自 2026-10-04 真机原始日志（Paper 1.21.4 与 Forge 1.20.1 都实测过）——
// 一次关服会**连着打两行**，缺一不可：
//
//	[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping the server  ← Paper/Forge 额外加的
//	[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping server      ← 原版 MinecraftServer 的
//
// 别图省事改成 (?i) + 子串匹配：插件完全可以打印 "Stopping Server Plugin"，
// 那就会把一次插件重载误判成关服。
func isGracefulShutdownLine(line string) bool {
	return strings.Contains(line, "Stopping the server") || strings.Contains(line, "Stopping server")
}

// isReadyLine 服务端宣布「我起来了」的那一行。
//
// 原版和各家的 fork 都打 `Done (12.345s)! For help, type "help"`。
// ⚠ 但 **Mohist 把这句本地化成了自己的话**，实测（2026-10-06，真机日志）：
//
//	Mohist 启动成功啦！╰(*°▽°*)╯，总耗时共 11.652s ，您可以使用 /help 命令查看服务器帮助！
//
// 那一行里**没有** "Done ("。只认原文的话，Mohist 上的 everReady 永远不成立，
// 于是「正常关服」的第三层兜底（退出码 0 且曾就绪）对它失效 —— 前两层还接得住
// （Stop 会置 intentional、日志里的 "Stopping server" 也在），所以没出过事，
// 但少一层就是少一层。
//
// ⚠ 判据要连 "Mohist" 一起要：光认「启动成功」太宽，任何插件往控制台打一句
// 「插件启动成功」都会把一次崩溃误判成正常关服（这个项目刚因为模糊匹配栽过一次，
// 见 isGracefulShutdownLine 上面那段）。
//
// Arclight 不用管：它打的就是原版那句 Done（实测确认）。
func isReadyLine(line string) bool {
	if strings.Contains(line, "Done (") {
		return true
	}
	return strings.Contains(line, "Mohist") && strings.Contains(line, "启动成功")
}

// track 从一行日志里更新可视化状态。
// 服务端自己的输出比额外发命令去问更省事，也不会污染控制台。
func (p *Process) track(line string) {
	// 服务端自己宣布要关了。这比「谁点的停止」可靠得多：
	// 我们点停止、用户在控制台手敲 stop、游戏内 /stop、插件调 API、RCON ——
	// 任何一条路走到关服，最后都会打出这一行。
	// 有了它，某条停机路径就算漏了标记，也不会被误记成崩溃。
	if isGracefulShutdownLine(line) {
		p.mu.Lock()
		p.intentional = true
		p.mu.Unlock()
		return
	}
	// 服务端宣布自己起来了 —— 记个档，好让收尾那边把「跑起来过、然后自己退的」
	// 和「压根没起来的」分开：后者（eula 没同意、端口被占）才是真该报崩的那一类。
	if isReadyLine(line) {
		p.mu.Lock()
		p.everReady = true
		p.mu.Unlock()
		return
	}
	if i := strings.Index(line, " joined the game"); i > 0 {
		if name := lastToken(line[:i]); name != "" {
			p.mu.Lock()
			// 懒初始化：不依赖构造函数，避免任何路径上拿到 nil map
			if p.players == nil {
				p.players = make(map[string]struct{})
			}
			p.players[name] = struct{}{}
			p.mu.Unlock()
		}
		return
	}
	if i := strings.Index(line, " left the game"); i > 0 {
		if name := lastToken(line[:i]); name != "" {
			p.mu.Lock()
			delete(p.players, name)
			p.mu.Unlock()
		}
		return
	}
	// list 命令的回执，用来**校正**在线名单：
	// There are 3 of a max of 20 players online: a, b, c
	//
	// 冒号后面就是权威名单，所以 n > 0 时也要解析出来覆盖 —— 原来只处理了 n == 0 的清空。
	// 只靠 join/leave 累积的话，漏掉一行（日志滚掉了、插件改了措辞、服务端重启过）
	// 名单就永远对不上，而且错得很安静。
	if i := strings.Index(line, " players online"); i > 0 {
		// 「…players online: Steve, Alex」—— 冒号后面就是**权威的完整名单**，
		// 直接用它覆盖。
		//
		// ⚠ 原来这段是从 " players online" 往前取最后一段数字当计数，
		// 但那一截的结尾是 **max-players 的值**（"There are 3 of a max of 20"），
		// 取到的是 20 不是 3 —— 于是 `n == 0` 几乎永不成立，
		// 这段「校正」其实**从来没生效过**。现在以名单本身为准，不再依赖那个数字。
		k := strings.Index(line[i:], ":")
		if k < 0 {
			return // 格式不认识（服务端换了语言或措辞），什么都不做
		}
		var names []string
		for _, s := range strings.Split(line[i+k+1:], ",") {
			if v := strings.TrimSpace(stripMCCodes(s)); v != "" {
				names = append(names, v)
			}
		}
		// 名单为空时，只有确认它就是「0 个」才清空；否则宁可不动 ——
		// 把在线的玩家凭空抹掉，比暂时不准更糟。
		if len(names) == 0 && !strings.Contains(line[:i], "are 0") {
			return
		}
		next := make(map[string]struct{}, len(names))
		for _, name := range names {
			next[name] = struct{}{}
		}
		p.mu.Lock()
		p.players = next
		p.mu.Unlock()
		return
	}
}

// PlayerNames 当前在线玩家名，已排序。
//
// 顺序必须稳定：前端每 3 秒轮询一次，而 map 的遍历顺序是随机的 ——
// 不排一下，界面上那个名单每次刷新都会自己重排一遍。
func (p *Process) PlayerNames() []string {
	p.mu.Lock()
	names := make([]string, 0, len(p.players))
	for n := range p.players {
		names = append(names, n)
	}
	p.mu.Unlock()
	sort.Strings(names)
	return names
}

// stripMCCodes 去掉 Minecraft 的格式化控制符（§ 后面跟一个字符）。
// 控制台回显里可能带着它，不剥掉的话名字会多一个看不见的字符，
// 拿去和白名单比对就永远对不上。
func stripMCCodes(s string) string {
	if !strings.ContainsRune(s, '§') {
		return s
	}
	var b strings.Builder
	skip := false
	for _, r := range s {
		if skip {
			skip = false
			continue
		}
		if r == '§' {
			skip = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// lastToken 取 "… INFO]: Steve" 里的 Steve
func lastToken(s string) string {
	s = strings.TrimRight(s, ": ")
	if j := strings.LastIndex(s, " "); j >= 0 {
		return strings.Trim(s[j+1:], "[]: ")
	}
	return strings.Trim(s, "[]: ")
}

// Metrics 采样一次。持锁期间不做慢操作，避免拖住控制台写入。
func (p *Process) Metrics() Metrics {
	p.mu.Lock()
	pid := 0
	if p.cmd != nil && p.cmd.Process != nil {
		pid = p.cmd.Process.Pid
	}
	started := p.startedAt
	players := len(p.players)
	status := p.status
	maxMem := p.inst.MaxMem
	p.mu.Unlock()

	m := Metrics{
		IsRunning: status == StatusRunning || status == StatusStopping,
		MemMax:    int64(maxMem),
		PID:       pid,
	}
	// 已经崩了/停了就别再报 PID 和运行时长 ——
	// p.cmd 还挂着上一个进程的残留信息，照实返回会让界面显示「已崩溃 · 已运行 12 分钟」。
	if !m.IsRunning {
		m.PID = 0
		m.CPU = 0
		m.MemUsed = 0
		m.Players = 0
		return m
	}
	if started != nil {
		m.UptimeSec = int64(time.Since(*started).Seconds())
	}
	m.Players = players
	if pid > 0 {
		if proc, err := process.NewProcess(int32(pid)); err == nil {
			if mi, err := proc.MemoryInfo(); err == nil && mi != nil {
				m.MemUsed = int64(mi.RSS / 1024 / 1024)
			}
			if c, err := proc.CPUPercent(); err == nil {
				m.CPU = c
			}
		}
	}
	return m
}

func (m *Manager) Metrics(id string) Metrics {
	m.mu.Lock()
	p, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		if inst, found := m.st.Get(id); found {
			return Metrics{MemMax: int64(inst.MaxMem)}
		}
		return Metrics{}
	}
	return p.Metrics()
}
