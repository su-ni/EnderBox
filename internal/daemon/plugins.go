package daemon

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
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

	// Meta 从 jar 里读出来的真实信息。读不到时为 nil（老 jar、非标准打包、损坏），
	// 此时上面的 Name/Version 是**按文件名猜的** —— 前端要如实标注，别让人以为猜的就是真的。
	Meta *modMeta `json:"meta,omitempty"`
	// MetaErr 读不出元数据的原因
	MetaErr string `json:"metaErr,omitempty"`
	// HasIcon jar 内有没有图标（前端据此决定要不要请求图标接口）
	HasIcon bool `json:"hasIcon,omitempty"`
	// Issues 依赖问题：缺前置、或前置被禁用
	Issues []modIssue `json:"issues,omitempty"`
}

// modIssue 一条依赖问题
type modIssue struct {
	DepID   string `json:"depId"`
	DepName string `json:"depName,omitempty"` // 前置被禁用时它的显示名
	Kind    string `json:"kind"`              // required | optional
	Problem string `json:"problem"`           // missing | disabled
}

// metaCache 按「文件大小 + 修改时间」缓存元数据。
// 列表页会被反复刷新（切实例、点刷新、下载完自动刷新），每次都解压 20 个 jar 太浪费。
// 键里带 size+mtime：用户换了同名的另一个版本时会自动失效，不用手动清缓存。
type metaCache struct {
	mu sync.Mutex
	m  map[string]metaCacheEntry
}

type metaCacheEntry struct {
	size  int64
	mtime int64
	meta  *modMeta
	err   string
}

var modMetaCache = &metaCache{m: map[string]metaCacheEntry{}}

// cachedModMeta 带缓存地读元数据
func cachedModMeta(path string, size int64, mtime int64) (*modMeta, string) {
	modMetaCache.mu.Lock()
	if e, ok := modMetaCache.m[path]; ok && e.size == size && e.mtime == mtime {
		modMetaCache.mu.Unlock()
		return e.meta, e.err
	}
	modMetaCache.mu.Unlock()

	m, err := readModMetaNested(path)
	entry := metaCacheEntry{size: size, mtime: mtime, meta: m}
	if err != nil {
		entry.err = err.Error()
	}

	modMetaCache.mu.Lock()
	// 缓存别无限长：实例被删/文件被换掉之后这些条目没人再访问，
	// 简单地在超过一定数量时整体丢弃，比重做一套 LRU 划算。
	if len(modMetaCache.m) > 500 {
		modMetaCache.m = map[string]metaCacheEntry{}
	}
	modMetaCache.m[path] = entry
	modMetaCache.mu.Unlock()
	return entry.meta, entry.err
}

// contentDirName 返回该服务端放插件/模组的目录名。
// 插件端是 plugins，模组端是 mods —— 合成一个会让用户把 mod 丢进 plugins 然后困惑为什么没生效。
//
// ⚠ 混合端（Mohist / Arclight）两边都有，这里归到 mods：这个面板的「插件与模组」页
// 主体是 Modrinth 市场（模组生态），点进去看到的应该是 mods/。
// 这和它们以前被归成 forge 时的行为一致，不是这次新改的。
func contentDirName(t Type) string {
	switch t {
	case TypeFabric, TypeForge, TypeNeoForge, TypeMohist, TypeArclight:
		return "mods"
	default:
		return "plugins"
	}
}

// DisabledSuffix 禁用不是删文件，而是改后缀 —— 玩家反悔了改回来就行，不用重新下载。
const DisabledSuffix = ".disabled"

// FileSHA1 算一个文件的 sha1（小写 hex）。
//
// 用途只有一个：把本地 jar 对回 Modrinth 上的项目（`POST /version_files`），
// 从而问出「这个模组服务端到底能不能装」。选 sha1 是因为 Modrinth 认它、也算得快 ——
// 这里只是「认身份」，不需要抗碰撞强度。
func FileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

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

// PluginFileRef 一个待核对的模组文件：文件名（给用户看） + 绝对路径（给我们读）。
type PluginFileRef struct {
	File string `json:"file"`
	Path string `json:"path"`
}

// PluginJarPaths 列出该实例「启用中的」模组 jar（带绝对路径）。
//
// 只给启用的：已禁用的不在加载范围里，再核对它们只会让「该禁用」的清单里混进
// 用户已经处理过的东西 —— 点了还会重复计数。
// 也只认 `.jar`：`.jar.part.0` 这类下载中间态必须排除（它连 zip 都不是，读了会报错）。
func (m *Manager) PluginJarPaths(id string) ([]PluginFileRef, error) {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]PluginFileRef, 0, len(ents))
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !jarRe.MatchString(name) || disabledRe.MatchString(name) {
			continue
		}
		out = append(out, PluginFileRef{File: name, Path: filepath.Join(dir, name)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
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
		item := PluginInfo{
			File:    e.Name(),
			Name:    name,
			Version: version,
			Enabled: !disabledRe.MatchString(e.Name()),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		// 读 jar 内元数据（带缓存）。读不到不隐藏这个文件，只是退回文件名猜的版本，
		// 并把原因带出去让界面能说清「这个 jar 读不出信息」。
		meta, metaErr := cachedModMeta(
			filepath.Join(dir, e.Name()), info.Size(), info.ModTime().UnixNano())
		if meta != nil {
			item.Meta = meta
			item.HasIcon = meta.IconPath != ""
			// 元数据是权威的：名字/版本以 jar 里写的为准，文件名只是猜的
			if meta.Name != "" {
				item.Name = meta.Name
			}
			if meta.Version != "" {
				item.Version = meta.Version
			}
		} else {
			item.MetaErr = metaErr
		}
		out = append(out, item)
	}

	m.checkModDeps(out)

	// 启用的排前面，同组按名字排 —— 用户扫一眼就知道现在生效的是哪些
	sort.Slice(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// checkModDeps 检查「依赖的前置在不在、有没有被禁用」，结果写进每项的 Issues。
//
// 为什么这件事值得单独做：装了模组起不来，服务端只会打一行英文说缺某个 modId；
// 用户既不知道那是什么、也不知道该去哪装。把它在界面上提前说清楚，是这一整块的核心价值。
//
// 判据用**jar 里的 modId**，不是文件名 —— 作者怎么命名文件是他的自由，
// 而 modId 是加载器真正用来匹配的键。
func (m *Manager) checkModDeps(list []PluginInfo) {
	// 已启用 / 已禁用的 modId 索引
	enabled := map[string]string{}  // modId → 显示名
	disabled := map[string]string{} // modId → 显示名
	for _, p := range list {
		if p.Meta == nil || p.Meta.ID == "" {
			continue
		}
		id := strings.ToLower(p.Meta.ID)
		if p.Enabled {
			enabled[id] = p.Name
		} else {
			disabled[id] = p.Name
		}
	}

	for i := range list {
		p := &list[i]
		if p.Meta == nil {
			continue
		}
		issues := make([]modIssue, 0, 2)
		addIssues := func(deps []modDep) {
			for _, d := range deps {
				if d.Builtin {
					continue // MC / Java / 加载器本身，不需要用户去装
				}
				id := strings.ToLower(strings.TrimSpace(d.ID))
				if id == "" {
					continue
				}
				if _, ok := enabled[id]; ok {
					continue // 前置已启用，没问题
				}
				is := modIssue{DepID: d.ID, Kind: d.Kind}
				if name, ok := disabled[id]; ok {
					// 装是装了，但被禁用了 —— 这个区分很关键：
					// 用户会以为「我明明装了」，其实是他自己关掉的。
					is.Problem = "disabled"
					is.DepName = name
				} else {
					is.Problem = "missing"
				}
				issues = append(issues, is)
			}
		}
		addIssues(p.Meta.Requires)
		addIssues(p.Meta.Optional)
		// 缺前置（必需）排前面 —— 那才是真正会导致起不来的
		sort.SliceStable(issues, func(a, b int) bool {
			if issues[a].Kind != issues[b].Kind {
				return issues[a].Kind == "required"
			}
			return issues[a].Problem == "missing" && issues[b].Problem != "missing"
		})
		p.Issues = issues
	}
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

// PluginDest 算出直链 / 市场安装时「往哪写」以及规范化后的文件名。
// 落点必须是模组目录里的 .jar —— 从网上拿到的东西更不能由它自己决定放到哪。
func (m *Manager) PluginDest(id, filename string) (string, string, error) {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return "", "", err
	}
	base := filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) || !jarRe.MatchString(base) {
		return "", "", fmt.Errorf("只支持 .jar 文件：%s", filename)
	}
	return filepath.Join(dir, base), base, nil
}

// PluginIcon 读 jar 内的图标，返回图片字节与 MIME。
//
// 单独一个接口而不是塞进列表里：一个带图标的模组包图标动辄几十 KB，
// 列表里有 30 个模组时 base64 进 JSON 会让整页慢得没法看。
// 前端按需请求，并用浏览器的缓存去兜（响应带 ETag/长度，不会重复拉）。
func (m *Manager) PluginIcon(id, file string) ([]byte, string, error) {
	dir, _, err := m.contentDir(id)
	if err != nil {
		return nil, "", err
	}
	base, err := safeName(file)
	if err != nil {
		return nil, "", err
	}
	full := filepath.Join(dir, base)
	info, err := os.Stat(full)
	if err != nil {
		return nil, "", fmt.Errorf("文件不存在: %s", base)
	}
	meta, _ := cachedModMeta(full, info.Size(), info.ModTime().UnixNano())
	if meta == nil || meta.IconPath == "" {
		return nil, "", fmt.Errorf("这个 jar 里没有图标")
	}
	// 把元数据的来源一起传进去：图标可能在内层 jar 里（Forge 外套包），
	// 只翻外层会「说有图标却 404」。
	return ModIconFile(full, meta.IconPath, meta.Source)
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
