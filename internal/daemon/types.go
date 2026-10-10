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
	// 混合端：本体是 Forge 系，另外挂了一套 Bukkit 实现，所以模组和插件能同时装。
	//
	// ⚠ 为什么要给它们**独立类型**，而不是像以前那样归进 TypeForge：
	// 类型这一栏是好几处的判据 —— 其中「服务端文件怎么下」（preflight 的一键补齐、
	// 配置页的重新下载）最关键。归成 forge 的话，一条 Mohist 实例点「补齐服务端文件」
	// 会去下 Forge 安装器并跑 --installServer —— 那是另一套完全不同的东西。
	// 靠启动文件名猜（jar 名里带 mohist）只能在文件已经在的时候才管用，
	// 而「文件不在、需要下载」恰恰是这个判据最该起作用的时刻。
	//
	// 和它们的关系是「Forge 系」这件事仍然成立（isHybridType / hybridBaseType 记着），
	// 只是不再等于「类型就是 forge」。
	TypeMohist   Type = "mohist"
	TypeArclight Type = "arclight"
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
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          Type   `json:"type"`
	MCVersion     string `json:"mcVersion"`
	LoaderVersion string `json:"loaderVersion,omitempty"`
	Dir           string `json:"dir"`
	JavaPath      string `json:"javaPath,omitempty"`
	JarPath       string `json:"jarPath"`
	JVMArgs       string `json:"jvmArgs,omitempty"`
	MinMem        int    `json:"minMem"` // MB
	MaxMem        int    `json:"maxMem"` // MB
	Port          int    `json:"port"`
	AutoStart     bool   `json:"autoStart"`
	AutoRestart   bool   `json:"autoRestart"`
	// Companion 要不要往这个实例里放面板自带的只读插件（实时玩家数据）。
	// ⚠ 用指针：nil 表示「用户从没设置过」，这时按**默认开**算（用户拍的默认值）。
	// 用 bool 的话，一个从没被设置过的实例会被当成「关」，界面上就白丢一个功能。
	Companion *bool `json:"companion,omitempty"`
	// Backup 自动备份策略。按实例配：一个长期档和一次性的测试档，备份频率差得远。
	Backup    BackupPolicy `json:"backup"`
	CreatedAt time.Time    `json:"createdAt"`
}

// 备份范围。
const (
	// BackupScopeAll 整个实例目录（只排除 logs/）。还原时插件/模组/配置一起回到一致状态。
	BackupScopeAll = "all"
	// BackupScopeWorlds 只打世界存档。体积小很多，但换过插件/模组之后还原会对不上。
	BackupScopeWorlds = "worlds"
)

// BackupPolicy 自动备份策略。
//
// 两种触发方式可以同时用：AtTimes 是「每天几点」这种固定时刻，EveryHours 是「每隔几小时」。
// 用户拍板两种都要 —— 固定时刻适合「夜里没人的时候备」，间隔式适合「开得频、怕丢进度」。
type BackupPolicy struct {
	Enabled bool `json:"enabled"`
	// EveryHours 间隔式触发；0 = 不用这种方式
	EveryHours int `json:"everyHours"`
	// AtTimes 每天固定时刻，格式 "HH:MM"，最多 6 个；空 = 不用这种方式
	AtTimes []string `json:"atTimes"`
	// Keep 只保留最近 N 份（手动/自动都算在内）；0 = 不限。超出时删最旧的。
	Keep int `json:"keep"`
	// Scope 见 BackupScopeAll / BackupScopeWorlds
	Scope string `json:"scope"`
	// Since 策略被开启的时刻。用于两件事：
	//   ①开启之前的固定时刻不补跑（否则「每天 04:00」一打开就立刻备一份）；
	//   ②间隔式从开启时刻开始计时。
	Since time.Time `json:"since"`
	// LastRun 上次**真的**跑完自动备份的时间。零值 = 从没跑过。
	// **要持久化**：不存的话守护进程一重启，「每天 04:00」就会因为
	// 「今天还没跑过」而立刻又跑一次。
	//
	// ⚠ 别拿它当「计时的起点」用（曾经为了抑制开启瞬间误触发这么干过），
	// 那样界面会把「刚打开」显示成「上次自动备份：现在」，是在撒谎。
	LastRun time.Time `json:"lastRun"`
}

// BackupPolicyView 给前端的策略视图 = 策略本身 + 算出来的下次时间。
//
// 为什么「下次什么时候跑」由后端算：调度规则只能有一份实现。
// 前端再算一遍就是第二个口径，早晚和后端对不上（这个项目刚因为「两处各判一次稳定」
// 栽过一次，见 Version 字段那件事）。
type BackupPolicyView struct {
	BackupPolicy
	NextRun *time.Time `json:"nextRun,omitempty"`
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

// TypeDisplay 类型名，给用户看的那个（不是 JSON 里那个内部名）。
//
// 别把 internal 的字符串直接印进界面：用户看到的是「velocity 是代理端」这种半截英文，
// 而不是「Velocity 是代理端」。前端的 serverLabel() 走的是启动文件名（要区分 Purpur/Leaves
// 这些同一类型的 fork），这里走类型 —— 两者用途不同，别合并。
func TypeDisplay(t Type) string {
	switch t {
	case TypeVanilla:
		return "原版"
	case TypePaper:
		return "Paper"
	case TypeFabric:
		return "Fabric"
	case TypeForge:
		return "Forge"
	case TypeNeoForge:
		return "NeoForge"
	case TypeVelocity:
		return "Velocity"
	case TypeBungee:
		return "BungeeCord"
	case TypeMohist:
		return "Mohist"
	case TypeArclight:
		return "Arclight"
	}
	return string(t)
}

// JavaForVersion 返回该 MC 版本所需的 Java 主版本号
//
// ⚠ 调用方必须先确认版本是**已知的**：空串会被 versionLT 当成 0.0.0，
// 于是一个「没有 MC 版本这回事」的代理端（Velocity / BungeeCord）会被算出 Java 8，
// 拿着这个数字去下结论就是撒谎。判据：mc == "" 时不许用它的返回值。
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
