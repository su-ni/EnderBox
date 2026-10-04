package daemon

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// PluginInfo 一个已安装的插件 / 模组
type PluginInfo struct {
	File    string    `json:"file"`
	Name    string    `json:"name"`
	Version string    `json:"version"`
	Enabled bool      `json:"enabled"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// contentDirName 返回该服务端放插件/模组的目录名。
// 插件端是 plugins，模组端是 mods —— 合成一个会让用户把 mod 丢进 plugins 然后困惑为什么没生效。
func contentDirName(t Type) string {
	switch t {
	case TypeFabric, TypeForge, TypeNeoForge:
		return "mods"
	default:
		return "plugins"
	}
}

// DisabledSuffix 禁用不是删文件，而是改后缀 —— 玩家反悔了改回来就行，不用重新下载。
const DisabledSuffix = ".disabled"

var (
	jarRe      = regexp.MustCompile(`(?i)\.jar$`)
	disabledRe = regexp.MustCompile(`(?i)\.jar\.disabled$`)
	verRe      = regexp.MustCompile(`^v?\d`)
)

// ParsePluginName 从文件名猜显示名与版本：EssentialsX-2.20.1.jar → EssentialsX / 2.20.1
// 猜不出来（没版本号、命名随意）就把整个文件名当名字，绝不编造版本。
func ParsePluginName(file string) (name, version string) {
	base := jarRe.ReplaceAllString(disabledRe.ReplaceAllString(file, ""), "")
	parts := strings.Split(base, "-")
	if len(parts) > 1 && verRe.MatchString(parts[len(parts)-1]) {
		return strings.Join(parts[:len(parts)-1], "-"), strings.TrimPrefix(parts[len(parts)-1], "v")
	}
	return base, ""
}

func (m *Manager) contentDir(id string) (string, Instance, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", Instance{}, fmt.Errorf("实例不存在: %s", id)
	}
	dir := filepath.Join(inst.Dir, contentDirName(inst.Type))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", inst, err
	}
	return dir, inst, nil
}

// ContentDirPath 给前端看「东西放在哪」，也用来提示用户手动丢文件进去
func (m *Manager) ContentDirPath(id string) (string, error) {
	dir, _, err := m.contentDir(id)
	return dir, err
}

func (m *Manager) ListPlugins(id string) ([]PluginInfo, error) {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PluginInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !jarRe.MatchString(e.Name()) && !disabledRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name, version := ParsePluginName(e.Name())
		out = append(out, PluginInfo{
			File:    e.Name(),
			Name:    name,
			Version: version,
			Enabled: !disabledRe.MatchString(e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
	}
	// 启用的排前面，同组按名字排 —— 用户扫一眼就知道现在生效的是哪些
	sort.Slice(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// safeName 挡住路径穿越：只允许文件名，不允许任何分隔符与 ..
func safeName(file string) (string, error) {
	base := filepath.Base(file)
	if base != file || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("非法文件名: %s", file)
	}
	if !jarRe.MatchString(base) && !disabledRe.MatchString(base) {
		return "", fmt.Errorf("只支持 .jar 文件: %s", base)
	}
	return base, nil
}

// SetPluginEnabled 通过改后缀来启停。停服状态下改才生效，所以界面要提示重启。
func (m *Manager) SetPluginEnabled(id, file string, enabled bool) error {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return err
	}
	base, err := safeName(file)
	if err != nil {
		return err
	}
	src := filepath.Join(dir, base)
	target := base
	if enabled {
		target = strings.TrimSuffix(base, DisabledSuffix)
		if target == base {
			return nil // 本来就是启用状态
		}
	} else {
		if disabledRe.MatchString(base) {
			return nil
		}
		target = base + DisabledSuffix
	}
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("文件不存在: %s", base)
	}
	return os.Rename(src, filepath.Join(dir, target))
}

func (m *Manager) DeletePlugin(id, file string) error {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return err
	}
	base, err := safeName(file)
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, base))
}

// SavePlugin 保存上传的插件文件。存在同名文件时覆盖 —— 用户重传就是要替换。
func (m *Manager) SavePlugin(id, filename string, r io.Reader) (string, error) {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return "", err
	}
	base := filepath.Base(filename)
	if base == "." || base == ".." || strings.ContainsAny(base, `/\`) || !jarRe.MatchString(base) {
		return "", fmt.Errorf("只支持 .jar 文件")
	}
	dst, err := os.Create(filepath.Join(dir, base))
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, r); err != nil {
		return "", err
	}
	return base, nil
}
