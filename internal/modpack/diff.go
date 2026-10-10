package modpack

import (
	"fmt"
	"sort"
	"strings"
)

// 升级 = 一次差异比对。这里是整个升级功能的**决策核心**，刻意做成纯函数：
// 不碰网络、不碰磁盘（磁盘现状由一个回调喂进来），所以规格表能把它全部覆盖住。
//
// 三条安全底线（写在这里，因为这里的每一行都在服务它们）：
//  ① 删除的判据是「它出现在**旧 packstate** 里」，不是「它不在新包里」——
//     用户自己丢进 mods/ 的模组一个都不能碰。
//  ② 磁盘上那份与 packstate 记的对不上（用户改过）→ **不覆盖**，列入跳过并说明。
//     默认覆盖等于悄悄吃掉用户的改动。
//  ③ 游戏版本或加载器变了 → 这不是「更新一下」，是换了一台服：必须明确警告。

// Action 对某个路径要做的动作。
type Action string

const (
	ActionAdd     Action = "add"     // 新包里新增
	ActionReplace Action = "replace" // 有变化，要覆盖
	ActionRemove  Action = "remove"  // 新包里没有了，要删
	ActionKeep    Action = "keep"    // 完全没变，不动
	ActionSkip    Action = "skip"    // 该动，但因为用户改过而**不动**
)

// Change 一条具体动作。
type Change struct {
	Path   string   `json:"path"`
	Action Action   `json:"action"`
	Reason string   `json:"reason,omitempty"`
	Old    *FileRef `json:"old,omitempty"`
	New    *FileRef `json:"new,omitempty"`
}

// Plan 一次升级的完整计划。给用户看的是它的计数与「跳过/删除」两份清单。
type Plan struct {
	ToAdd     []Change `json:"toAdd"`
	ToReplace []Change `json:"toReplace"`
	ToRemove  []Change `json:"toRemove"`
	Kept      []Change `json:"kept"`
	Skipped   []Change `json:"skipped"`

	FromMC     string `json:"fromMc,omitempty"`
	ToMC       string `json:"toMc,omitempty"`
	FromLoader string `json:"fromLoader,omitempty"`
	ToLoader   string `json:"toLoader,omitempty"`
	// LevelJump 游戏版本或加载器变了。
	LevelJump bool `json:"levelJump"`
	// LevelJumpNote 给用户看的原话（为什么这事比「更新」严重）。
	LevelJumpNote string `json:"levelJumpNote,omitempty"`

	// Refusal 非空 = 不能升级，值是原因。
	Refusal string `json:"refusal,omitempty"`

	// DownloadBytes 预计要下的字节数（拿得到 size 的才算），用来在按钮前给个量级。
	DownloadBytes int64 `json:"downloadBytes"`
}

// Summary 一行计数，界面直接用。
type Summary struct {
	Add, Replace, Remove, Keep, Skip int
}

func (p *Plan) Summary() Summary {
	return Summary{len(p.ToAdd), len(p.ToReplace), len(p.ToRemove), len(p.Kept), len(p.Skipped)}
}

// BuildPlan 算出「从 old 升到 next」要做什么。
//
// diskSHA 用来问「磁盘上这个文件现在是什么哈希」（相对实例目录的路径，'/' 分隔）。
// 第二个返回值为 false 表示文件不存在。
// 传 nil 表示「不检查磁盘现状」—— 只算增量、不防用户改动（解析阶段用得上，升级时必须传）。
// nextOverrides 是目标版本覆盖文件的清单（不在索引里，得单独从 zip 读出来传进来）。
func BuildPlan(old *State, next *Index, diskSHA func(rel string) (sum string, ok bool), nextOverrides []OverrideRef) *Plan {
	p := &Plan{
		ToAdd: []Change{}, ToReplace: []Change{}, ToRemove: []Change{},
		Kept: []Change{}, Skipped: []Change{},
	}
	if next == nil {
		p.Refusal = "没有拿到目标版本的包索引"
		return p
	}
	p.ToMC = next.Minecraft()
	if k, v := next.Loader(); k != "" {
		p.ToLoader = k + " " + v
	}
	if old != nil {
		p.FromMC = old.Minecraft
		for k, v := range old.Loader {
			p.FromLoader = k + " " + v
		}
	}

	// 新版本要装的（服务端侧）
	install, _ := next.ServerInstallable()
	v := next.ServerVerdict()
	if !v.OK && len(nextOverrides) == 0 {
		// 新版本连一个服务端文件都没有、也没有任何覆盖文件 —— 它本质是个客户端整合包，
		// 真按它升级会把服务端清空成一堆客户端模组，必须拦住。
		// 但若只是「配置包」（没有服务端模组、但带 overrides/ 或 server-overrides/），
		// 那是合法的升级，照常走下面的覆盖比对。
		p.Refusal = "目标版本装不到服务端：" + v.Reason
		return p
	}
	// 新包里所有 path（含被跳过的那些），用来区分「新包里没有」和「新包把它改成客户端专属了」
	nextAll := make(map[string]File, len(next.Files))
	for _, f := range next.Files {
		nextAll[f.Path] = f
	}

	oldByPath := map[string]FileRef{}
	if old != nil {
		oldByPath = old.FileRefByPath()
	}

	for _, f := range install {
		sum, kind := f.SHA()
		want := FileRef{Path: f.Path, SHA: sum, SHAKind: kind, Side: f.Env.Server}
		prev, hadPrev := oldByPath[f.Path]

		disk, hasDisk := "", false
		if diskSHA != nil {
			disk, hasDisk = diskSHA(f.Path)
		}

		switch {
		case hadPrev && prev.SHA == want.SHA && (!hasDisk || disk == want.SHA):
			// 记录一致、磁盘也一致 → 不动（省流量的主要来源）
			p.Kept = append(p.Kept, Change{Path: f.Path, Action: ActionKeep, Old: &prev, New: &want})

		case hasDisk && disk != want.SHA && (!hadPrev || disk != prev.SHA):
			// 磁盘上这个文件**不是我们记的那一份** → 用户改过它（或用户自己放了个同名文件）。
			// 不覆盖、不删除，摊出来让用户决定。这是底线 ②。
			reason := "这个文件不是你装的整合包那一份（内容对不上），保留你的，没有覆盖"
			if !hadPrev {
				reason = "这个路径下已经有你自己的文件了，保留你的，没有覆盖"
			}
			p.Skipped = append(p.Skipped, Change{Path: f.Path, Action: ActionSkip, Reason: reason, New: &want})

		case hadPrev:
			p.ToReplace = append(p.ToReplace, Change{Path: f.Path, Action: ActionReplace, Old: &prev, New: &want})
			p.DownloadBytes += f.FileSize

		default:
			p.ToAdd = append(p.ToAdd, Change{Path: f.Path, Action: ActionAdd, New: &want})
			p.DownloadBytes += f.FileSize
		}
	}

	// ── 覆盖文件：与模组走完全同一套三档判断 ──
	// 覆盖文件不在索引里，靠 nextOverrides 单独传进来；旧状态里的是 old.Overrides。
	// 磁盘上覆盖文件的真实位置是「去掉前缀」之后，所以问 diskSHA 前要先把前缀剥掉。
	oldOvByPath := map[string]OverrideRef{}
	if old != nil {
		oldOvByPath = old.OverrideRefByPath()
	}
	for _, o := range nextOverrides {
		want := FileRef{Path: o.Path, SHA: o.SHA, SHAKind: o.SHAKind}
		prev, hadPrev := oldOvByPath[o.Path]
		var prevFR FileRef
		if hadPrev {
			prevFR = FileRef{Path: prev.Path, SHA: prev.SHA, SHAKind: prev.SHAKind}
		}

		disk, hasDisk := "", false
		if diskSHA != nil {
			disk, hasDisk = diskSHA(DiskRelOfOverride(o.Path))
		}

		switch {
		case hadPrev && prev.SHA == want.SHA && (!hasDisk || disk == want.SHA):
			// 记录一致、磁盘也一致 → 不动（省流量的主要来源）
			p.Kept = append(p.Kept, Change{Path: o.Path, Action: ActionKeep, Old: &prevFR, New: &want})

		case hasDisk && disk != want.SHA && (!hadPrev || disk != prev.SHA):
			// 磁盘上不是我们装的那一份 → 用户改过它（或用户自己放了个同名文件）。
			// 不覆盖、不删除，摊出来让用户决定。这是底线 ②。
			reason := "这个文件不是你装的整合包那一份（内容对不上），保留你的，没有覆盖"
			if !hadPrev {
				reason = "这个路径下已经有你自己的文件了，保留你的，没有覆盖"
			}
			p.Skipped = append(p.Skipped, Change{Path: o.Path, Action: ActionSkip, Reason: reason, New: &want})

		case hadPrev:
			p.ToReplace = append(p.ToReplace, Change{Path: o.Path, Action: ActionReplace, Old: &prevFR, New: &want})

		default:
			p.ToAdd = append(p.ToAdd, Change{Path: o.Path, Action: ActionAdd, New: &want})
		}
	}

	// 旧装过、新版本不再需要（或改成了客户端专属）的
	if old != nil {
		wanted := make(map[string]bool, len(install))
		for _, f := range install {
			wanted[f.Path] = true
		}
		for _, prev := range old.Files {
			if wanted[prev.Path] {
				continue
			}
			reason := "新版本里没有这个文件了"
			if nf, ok := nextAll[prev.Path]; ok && nf.Env.Server == SideUnsupported {
				reason = "新版本把它改成了纯客户端文件"
			}
			disk, hasDisk := "", false
			if diskSHA != nil {
				disk, hasDisk = diskSHA(prev.Path)
			}
			switch {
			case !hasDisk:
				// 已经不在了 —— 不必给用户看一条「删除」，因为它本来就没东西可删
				continue
			case prev.SHA != "" && disk != prev.SHA:
				// 改过 → 不删（底线 ②）
				p.Skipped = append(p.Skipped, Change{
					Path: prev.Path, Action: ActionSkip, Old: &prev,
					Reason: reason + "；但这个文件你改过，保留你的，没有删",
				})
			default:
				p.ToRemove = append(p.ToRemove, Change{Path: prev.Path, Action: ActionRemove, Old: &prev, Reason: reason})
			}
		}
	}

	// 旧装过、新版本不再需要的覆盖文件（或改成了客户端专属的）
	if old != nil {
		wantedOv := make(map[string]bool, len(nextOverrides))
		for _, o := range nextOverrides {
			wantedOv[o.Path] = true
		}
		for _, prev := range old.Overrides {
			if wantedOv[prev.Path] {
				continue
			}
			disk, hasDisk := "", false
			if diskSHA != nil {
				disk, hasDisk = diskSHA(DiskRelOfOverride(prev.Path))
			}
			prevFR := FileRef{Path: prev.Path, SHA: prev.SHA, SHAKind: prev.SHAKind}
			switch {
			case !hasDisk:
				// 已经不在了 —— 不必给用户看一条「删除」，因为它本来就没东西可删
				continue
			case prev.SHA != "" && disk != prev.SHA:
				// 改过 → 不删（底线 ②）
				p.Skipped = append(p.Skipped, Change{
					Path: prev.Path, Action: ActionSkip, Old: &prevFR,
					Reason: "新版本里没有这个文件了；但这个文件你改过，保留你的，没有删",
				})
			default:
				p.ToRemove = append(p.ToRemove, Change{Path: prev.Path, Action: ActionRemove, Old: &prevFR, Reason: "新版本里没有这个文件了"})
			}
		}
	}

	// 底线 ③：版本/加载器变了要说清严重性
	if old != nil && (p.FromMC != p.ToMC || p.FromLoader != p.ToLoader) {
		p.LevelJump = true
		switch {
		case p.FromMC != "" && p.ToMC != "" && p.FromMC != p.ToMC:
			p.LevelJumpNote = fmt.Sprintf(
				"游戏版本要从 %s 换到 %s。这不是「更新一下」—— 存档的世界生成、模组、配置都要跟着变，"+
					"出问题通常回不去。先做一次备份再升。", p.FromMC, p.ToMC)
		case p.FromLoader != p.ToLoader:
			p.LevelJumpNote = fmt.Sprintf(
				"加载器要从 %s 换到 %s。模组是按加载器发布的，换了之后可能有一批模组不可用。"+
					"先做一次备份再升。", p.FromLoader, p.ToLoader)
		}
	}

	sortPlan(p)
	return p
}

// sortPlan 让清单顺序稳定（按路径），否则同样的输入两次跑出来的 JSON 不一样，
// 界面上会莫名闪动，测试也没法做逐字节比较。
func sortPlan(p *Plan) {
	byPath := func(list []Change) {
		sort.SliceStable(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	}
	byPath(p.ToAdd)
	byPath(p.ToReplace)
	byPath(p.ToRemove)
	byPath(p.Kept)
	byPath(p.Skipped)
}

// HasWork 这次升级到底有没有事要做（全是 keep 就是没有）。
func (p *Plan) HasWork() bool {
	return len(p.ToAdd)+len(p.ToReplace)+len(p.ToRemove) > 0
}

// OneLine 给日志用的一句话。
func (p *Plan) OneLine() string {
	s := p.Summary()
	return strings.TrimSpace(fmt.Sprintf(
		"新增 %d · 变更 %d · 删除 %d · 保留 %d · 跳过（你改过的）%d",
		s.Add, s.Replace, s.Remove, s.Keep, s.Skip))
}
