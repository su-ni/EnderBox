package modpack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// 状态文件放在**实例目录里**（`.serverbox/packstate.json`），不放在面板的 data 目录。
// 理由：实例目录会被搬走、会被备份还原、会被导入到另一台机器上 ——
// 「这台服是哪个整合包装的」必须跟着实例走，否则换台机器就变回「来路不明」，
// 升级功能直接失效。
const (
	stateDirName     = ".serverbox"
	stateFileName    = "packstate.json"
	stateJSONVersion = 1
)

// ErrNoState 这个实例不是整合包装的（或状态文件不在了）。
// 单独一个错误值，因为调用方要据此把界面切成「不是整合包」而不是「读取出错」。
var ErrNoState = errors.New("这个实例不是由整合包安装的")

// FileRef 我们自己装进去的一个文件。
//
// ⚠ 这份清单是升级时「哪些文件可以动」的**唯一依据**：
// 不在清单里的东西（用户自己丢进 mods/ 的）一个都不能碰。
type FileRef struct {
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	SHAKind string `json:"shaKind,omitempty"`
	// Side 装的时候它在服务端是必需还是可选 —— 留个底，便于事后解释。
	Side Side `json:"side,omitempty"`
}

// OverrideRef 我们自己解压进去的一个覆盖文件。
type OverrideRef struct {
	Path    string `json:"path"`
	SHA     string `json:"sha"`
	SHAKind string `json:"shaKind,omitempty"`
}

// State 一个实例「属于哪个整合包、装的哪一版、装了什么」。
type State struct {
	JSONVersion int `json:"jsonVersion"`

	Format        string            `json:"format"` // mrpack
	ProjectID     string            `json:"projectId,omitempty"`
	Slug          string            `json:"slug,omitempty"`
	VersionID     string            `json:"versionId,omitempty"`
	VersionNumber string            `json:"versionNumber,omitempty"`
	Name          string            `json:"name,omitempty"`
	Minecraft     string            `json:"minecraft,omitempty"`
	Loader        map[string]string `json:"loader,omitempty"`
	InstalledAt   string            `json:"installedAt,omitempty"`
	// UpdatedAt 上一次升级的时间（没升过为空）。
	UpdatedAt string `json:"updatedAt,omitempty"`

	Files     []FileRef     `json:"files"`
	Overrides []OverrideRef `json:"overrides"`
}

// StatePath 状态文件的绝对路径。
func StatePath(instanceDir string) string {
	return filepath.Join(instanceDir, stateDirName, stateFileName)
}

// LoadState 读状态。⚠ 返回 ErrNoState 表示「这不是整合包装的」，
// 其他错误才是真的读坏了 —— 界面上这两种要分开说。
func LoadState(instanceDir string) (*State, error) {
	p := StatePath(instanceDir)
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNoState
		}
		return nil, fmt.Errorf("读整合包状态失败：%w", err)
	}
	var st State
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("整合包状态文件解析失败（%s）：%w", p, err)
	}
	if st.JSONVersion > stateJSONVersion {
		// 别假装读得懂未来的版本：宁可明说，也不要按老结构曲解出一堆默认值。
		return nil, fmt.Errorf("整合包状态是更高版本的面板写的（v%d > v%d），升级面板之后再试",
			st.JSONVersion, stateJSONVersion)
	}
	// 发出去的集合字段先补空集合（本项目的老坑：nil slice 序列化成 null，前端 .length 就炸）
	if st.Files == nil {
		st.Files = []FileRef{}
	}
	if st.Overrides == nil {
		st.Overrides = []OverrideRef{}
	}
	return &st, nil
}

// SaveState 原子写：先写临时文件再改名。
// 升级到一半断电时，宁可保留**旧的**状态，也不要留一个半截的 JSON ——
// 半截 JSON 会让这个实例直接变回「来路不明」，用户没有任何办法补救。
func SaveState(instanceDir string, st *State) error {
	st.JSONVersion = stateJSONVersion
	if st.Files == nil {
		st.Files = []FileRef{}
	}
	if st.Overrides == nil {
		st.Overrides = []OverrideRef{}
	}
	// ⚠ 这里**不能**顺手把 UpdatedAt 补成「现在」。两个时间戳各有归属：
	//   InstalledAt = 首次安装 / 导入的时刻；UpdatedAt = 上一次**升级**的时刻（没升过就是空）。
	// 界面按空串显示「从没升过」；在这里无条件补一个当下，刚装完的包立刻显示
	// 「上次升级：刚刚」—— 一条用户从没做过的操作记录。写哪个由调用方决定。
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化整合包状态失败：%w", err)
	}

	dir := filepath.Join(instanceDir, stateDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("建 %s 失败：%w", dir, err)
	}
	tmp := StatePath(instanceDir) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("写整合包状态失败：%w", err)
	}
	if err := os.Rename(tmp, StatePath(instanceDir)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("落盘整合包状态失败：%w", err)
	}
	return nil
}

// FileRefByPath 便于比对时按 path 查。
func (s *State) FileRefByPath() map[string]FileRef {
	m := make(map[string]FileRef, len(s.Files))
	for _, f := range s.Files {
		m[f.Path] = f
	}
	return m
}

// OverrideRefByPath 同上。
func (s *State) OverrideRefByPath() map[string]OverrideRef {
	m := make(map[string]OverrideRef, len(s.Overrides))
	for _, o := range s.Overrides {
		m[o.Path] = o
	}
	return m
}

// FromIndex 用一份解析好的包索引造出「刚装完」的状态。
// 供导入流程使用；升级流程则是在旧状态上改。
//
// overrides 是这份包里覆盖文件的清单（不在索引里，得单独从 zip 读），显式传进来保持纯函数。
func FromIndex(ix *Index, slug, projectID, versionNumber string, overrides []OverrideRef) *State {
	install, _ := ix.ServerInstallable()
	st := &State{
		Format:        "mrpack",
		ProjectID:     projectID,
		Slug:          slug,
		VersionID:     ix.VersionID,
		VersionNumber: versionNumber,
		Name:          ix.Name,
		Minecraft:     ix.Minecraft(),
		InstalledAt:   time.Now().Format(time.RFC3339),
		Files:         []FileRef{},
		Overrides:     []OverrideRef{},
	}
	if k, v := ix.Loader(); k != "" {
		st.Loader = map[string]string{k: v}
	}
	for _, f := range install {
		sum, kind := f.SHA()
		st.Files = append(st.Files, FileRef{Path: f.Path, SHA: sum, SHAKind: kind, Side: f.Env.Server})
	}
	for _, o := range overrides {
		st.Overrides = append(st.Overrides, o)
	}
	return st
}
