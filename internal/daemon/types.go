package daemon

import "time"

// Type 服务端类型
type Type string

const (
	TypeVanilla  Type = "vanilla"
	TypePaper    Type = "paper"
	TypeFabric   Type = "fabric"
	TypeForge    Type = "forge"
	TypeNeoForge Type = "neoforge"
	TypeVelocity Type = "velocity"
	TypeBungee   Type = "bungeecord"
)

// Status 实例运行状态
type Status string

const (
	StatusStopped  Status = "stopped"
	StatusStarting Status = "starting"
	StatusRunning  Status = "running"
	StatusStopping Status = "stopping"
	StatusCrashed  Status = "crashed"
)

// Instance 一个服务器实例
type Instance struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Type          Type      `json:"type"`
	MCVersion     string    `json:"mcVersion"`
	LoaderVersion string    `json:"loaderVersion,omitempty"`
	Dir           string    `json:"dir"`
	JavaPath      string    `json:"javaPath,omitempty"`
	JarPath       string    `json:"jarPath"`
	JVMArgs       string    `json:"jvmArgs,omitempty"`
	MinMem        int       `json:"minMem"` // MB
	MaxMem        int       `json:"maxMem"` // MB
	Port          int       `json:"port"`
	AutoStart     bool      `json:"autoStart"`
	AutoRestart   bool      `json:"autoRestart"`
	CreatedAt     time.Time `json:"createdAt"`
}

// RuntimeState 运行时状态，不持久化
type RuntimeState struct {
	Status    Status     `json:"status"`
	PID       int        `json:"pid"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Players   int        `json:"players"`
	Error     string     `json:"error,omitempty"`
}

// View 给前端看的完整视图 = 配置 + 状态
type View struct {
	Instance `json:",inline"`
	RuntimeState
}

// JavaForVersion 返回该 MC 版本所需的 Java 主版本号
func JavaForVersion(mc string) int {
	switch {
	case versionLT(mc, "1.17"):
		return 8
	case versionLT(mc, "1.20.5"):
		return 17
	default:
		return 21
	}
}

func versionLT(a, b string) bool {
	pa := splitNum(a)
	pb := splitNum(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return false
}

func splitNum(v string) [3]int {
	var out [3]int
	cur := 0
	idx := 0
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c >= '0' && c <= '9' {
			cur = cur*10 + int(c-'0')
			continue
		}
		if idx < 3 {
			out[idx] = cur
			idx++
			cur = 0
		}
	}
	if idx < 3 {
		out[idx] = cur
	}
	return out
}
