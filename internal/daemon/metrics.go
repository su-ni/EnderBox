package daemon

import (
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

// track 从一行日志里更新可视化状态。
// 服务端自己的输出比额外发命令去问更省事，也不会污染控制台。
func (p *Process) track(line string) {
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
	// list 命令的回执，用来校正计数：
	// There are 3 of a max of 20 players online: a, b, c
	if i := strings.Index(line, " players online"); i > 0 {
		seg := line[:i]
		j := strings.LastIndex(seg, " ")
		if j < 0 {
			return
		}
		n := 0
		for _, c := range strings.TrimSpace(seg[j+1:]) {
			if c < '0' || c > '9' {
				return
			}
			n = n*10 + int(c-'0')
		}
		if n == 0 {
			p.mu.Lock()
			p.players = map[string]struct{}{}
			p.mu.Unlock()
		}
		return
	}
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
