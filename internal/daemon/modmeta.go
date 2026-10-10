package daemon

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// 从 jar 内部读取模组/插件的元数据。
//
// 为什么必须读 jar 而不是继续从文件名猜：
//  1. 「Sodium-fabric-0.6.5.jar」这种命名是**作者自己起的**，猜出来的版本经常是错的
//     （中间那段可能是 MC 版本、可能是加载器）；有些作者干脆不写版本。
//  2. **前置依赖只存在于 jar 里**。这是新手装模组起不来最常见的原因 ——
//     服务端只打一行英文说缺某个 modId，而用户根本不知道那是什么。
//  3. 有了真实的 modId，才能做「谁依赖谁」「哪些能安全删」这类判断。
//
// 全部在本地完成，不联网 —— 不依赖任何被墙的 API。

// modDep 一条依赖
type modDep struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind"` // required | optional
	// Builtin 表示这是 MC / Java / 加载器自身，不是「需要你去装的东西」。
	// 不标出来的话，每个模组都会提示「缺少 minecraft」这种废话。
	Builtin bool `json:"builtin,omitempty"`
}

// modMeta 从 jar 里读出来的元数据
type modMeta struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	Authors     []string `json:"authors,omitempty"`
	// Loader 元数据来自哪种格式：fabric / forge / neoforge / quilt / bukkit / paper / velocity
	Loader string `json:"loader,omitempty"`
	// Source 具体是哪个文件（排错用：用户说「信息不对」时得能说清是从哪读的）
	Source string `json:"source,omitempty"`
	// Homepage / IssueTracker 之类，能读到就带上
	Homepage string   `json:"homepage,omitempty"`
	Requires []modDep `json:"requires,omitempty"`
	Optional []modDep `json:"optional,omitempty"`
	// IconPath jar 内的图标路径（如 assets/sodium/icon.png）。空 = 没有。
	IconPath string `json:"iconPath,omitempty"`
	// Side 这个模组该装在哪一侧：client（仅客户端）/ server（仅服务端）/ 空（两端都行、或读不出）。
	//
	// ⚠ 只有 Fabric / Quilt 读得出来 —— fabric.mod.json 的 environment 字段就是干这个的，
	// 服务端加载器也会据此拒绝纯客户端 mod。
	// Forge / NeoForge 的 mods.toml **没有**对应字段（1.12 的 clientSideOnly 早废了），
	// 那边一律留空。**不许拿「引用了 net.minecraft.client 类」这种启发式去猜** ——
	// 双端模组普遍也引用客户端类（只是运行时按侧分支），猜错会把好模组赶出去；
	// 而猜错不报错，只会让用户删掉一个本来能用的东西。
	Side string `json:"side,omitempty"`
}

// builtinModIDs 这些 id 代表「运行环境本身」，不是需要安装的模组。
// 判定必须覆盖各家写法（大小写与别名都见过）。
var builtinModIDs = map[string]bool{
	"minecraft": true, "java": true, "mcp": true,
	"fabricloader": true, "fabric-loader": true, "fabric": true,
	"forge": true, "neoforge": true, "minecraftforge": true,
	"quilt_loader": true, "quilt-loader": true, "quilt": true,
	"bukkit": true, "spigot": true, "paper": true, "purpur": true,
	"velocity": true, "bungeecord": true, "waterfall": true,
}

func isBuiltinMod(id string) bool {
	return builtinModIDs[strings.ToLower(strings.TrimSpace(id))]
}

// ── Fabric / Quilt ──

// fabricLike fabric.mod.json 与 quilt.mod.json 的公共部分。
// authors 在旧版是字符串数组，新版可能是 [{name, contact}]，两种都要吃。
type fabricLike struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Description string            `json:"description"`
	Authors     json.RawMessage   `json:"authors"`
	Depends     map[string]any    `json:"depends"`
	Recommends  map[string]any    `json:"recommends"`
	Suggests    map[string]any    `json:"suggests"`
	Icon        json.RawMessage   `json:"icon"`
	Contact     map[string]string `json:"contact"`
	// Environment 客户端/服务端归属："client" / "server" / "*"（缺省同样按两端算）。
	// 这是 Fabric 生态里唯一权威的 side 声明 —— 服务端加载器启动时就靠它拒绝纯客户端 mod。
	Environment string `json:"environment"`
}

func parseAuthors(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var ss []string
	if err := json.Unmarshal(raw, &ss); err == nil {
		return ss
	}
	var objs []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &objs); err == nil {
		out := make([]string, 0, len(objs))
		for _, o := range objs {
			if o.Name != "" {
				out = append(out, o.Name)
			}
		}
		return out
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil && s != "" {
		return []string{s}
	}
	return nil
}

// parseIconPath icon 可能是字符串，也可能是 {路径: 尺寸} 的映射（新 schema）
func parseIconPath(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err == nil {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return keys[0]
		}
	}
	return ""
}

// depConstraint 把 depends 里的值统一成字符串。Fabric 里可能是 ">=1.0"、也可能是数组。
func depConstraint(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " || ")
	}
	return ""
}

func depsFromMap(m map[string]any, kind string) []modDep {
	if len(m) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]modDep, 0, len(ids))
	for _, id := range ids {
		out = append(out, modDep{
			ID:      id,
			Version: depConstraint(m[id]),
			Kind:    kind,
			Builtin: isBuiltinMod(id),
		})
	}
	return out
}

func parseFabricLike(data []byte, loader, source string) (*modMeta, error) {
	var f fabricLike
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	m := &modMeta{
		ID:          f.ID,
		Name:        f.Name,
		Version:     f.Version,
		Description: f.Description,
		Authors:     parseAuthors(f.Authors),
		Loader:      loader,
		Source:      source,
		IconPath:    parseIconPath(f.Icon),
		Side:        normalizeSide(f.Environment),
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	m.Requires = depsFromMap(f.Depends, "required")
	m.Optional = append(depsFromMap(f.Recommends, "optional"), depsFromMap(f.Suggests, "optional")...)
	if h := f.Contact["homepage"]; h != "" {
		m.Homepage = h
	}
	return m, nil
}

// normalizeSide 把源里的 environment 收成三种结果。
// "*" 与缺省都表示「两端都行」，收成空串 —— 绝大多数模组都是这种，
// 界面上每个都挂一个「两端」等于什么都没说。
func normalizeSide(env string) string {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "client":
		return "client"
	case "server":
		return "server"
	default:
		return ""
	}
}

// ── Forge / NeoForge（mods.toml）──

type forgeTomlMod struct {
	ModID       string `toml:"modId"`
	Version     string `toml:"version"`
	DisplayName string `toml:"displayName"`
	Authors     string `toml:"authors"`
	Description string `toml:"description"`
	DisplayURL  string `toml:"displayURL"`
	// LogoFile Forge 侧声明图标的字段（Fabric 那边叫 icon）。漏掉它的话
	// 所有 Forge 模组在界面里都是没有图标的一片灰。
	LogoFile string `toml:"logoFile"`
}

type forgeTomlDep struct {
	ModID        string `toml:"modId"`
	Mandatory    bool   `toml:"mandatory"`
	VersionRange string `toml:"versionRange"`
}

type forgeToml struct {
	Mods         []forgeTomlMod            `toml:"mods"`
	Dependencies map[string][]forgeTomlDep `toml:"dependencies"`
}

func parseForgeToml(data []byte, loader, source string) (*modMeta, error) {
	var ft forgeToml
	if err := toml.Unmarshal(data, &ft); err != nil {
		return nil, err
	}
	if len(ft.Mods) == 0 {
		return nil, fmt.Errorf("%s 里没有 [[mods]] 段", source)
	}
	// 一个 jar 理论上能声明多个 mod（少见）。取第一个当主身份，
	// 其余的把 id 记进描述 —— 免得用户以为列表少东西了。
	first := ft.Mods[0]
	m := &modMeta{
		ID:          first.ModID,
		Name:        first.DisplayName,
		Version:     first.Version,
		Description: strings.TrimSpace(first.Description),
		Loader:      loader,
		Source:      source,
		Homepage:    first.DisplayURL,
		IconPath:    strings.TrimSpace(first.LogoFile),
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	if a := strings.TrimSpace(first.Authors); a != "" {
		for _, s := range strings.Split(a, ",") {
			if s = strings.TrimSpace(s); s != "" {
				m.Authors = append(m.Authors, s)
			}
		}
	}
	if len(ft.Mods) > 1 {
		ids := make([]string, 0, len(ft.Mods)-1)
		for _, x := range ft.Mods[1:] {
			if x.ModID != "" {
				ids = append(ids, x.ModID)
			}
		}
		if len(ids) > 0 {
			m.Description += fmt.Sprintf("（这个 jar 里还包含：%s）", strings.Join(ids, "、"))
		}
	}

	// 依赖按「声明它的那个 mod」分组；我们只取主 mod 的那一组
	deps := ft.Dependencies[first.ModID]
	for _, d := range deps {
		if d.ModID == "" {
			continue
		}
		item := modDep{
			ID:      d.ModID,
			Version: d.VersionRange,
			Builtin: isBuiltinMod(d.ModID),
		}
		if d.Mandatory {
			item.Kind = "required"
			m.Requires = append(m.Requires, item)
		} else {
			item.Kind = "optional"
			m.Optional = append(m.Optional, item)
		}
	}
	return m, nil
}

// ── Bukkit / Paper 插件（plugin.yml）──

// parseSimpleYAML 极简 YAML 取值器：只认 `key: value` 与 `key: [a, b]` 和
// `key:\n  - a\n  - b` 这三种形态，覆盖 plugin.yml / bungee.yml 的全部需要。
//
// 为什么不上一个 YAML 库：只为了读 6 个字段而引入一个几百 KB、且历史上出过
// 安全问题的解析器不划算。这里的输入是我们自己从 jar 里读出来的固定格式，
// 不是用户可编辑的任意 YAML，简单解析的边界是可控的。
func parseSimpleYAML(text string) map[string][]string {
	out := map[string][]string{}
	lines := strings.Split(text, "\n")
	var curKey string
	for _, raw := range lines {
		line := strings.TrimRight(raw, "\r")
		// 跳注释与空行（值里也可能有 #，所以只在行首判）
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		indented := line != trimmed && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t"))
		if indented && strings.HasPrefix(trimmed, "- ") && curKey != "" {
			v := strings.Trim(strings.TrimSpace(trimmed[2:]), `"'`)
			out[curKey] = append(out[curKey], v)
			continue
		}
		i := strings.Index(trimmed, ":")
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:i])
		val := strings.TrimSpace(trimmed[i+1:])
		curKey = key
		if val == "" {
			out[key] = nil // 后面可能跟 `- item`
			continue
		}
		if strings.HasPrefix(val, "[") && strings.HasSuffix(val, "]") {
			inner := strings.TrimSpace(val[1 : len(val)-1])
			if inner == "" {
				out[key] = []string{}
				continue
			}
			parts := strings.Split(inner, ",")
			list := make([]string, 0, len(parts))
			for _, p := range parts {
				if s := strings.Trim(strings.TrimSpace(p), `"'`); s != "" {
					list = append(list, s)
				}
			}
			out[key] = list
			continue
		}
		out[key] = []string{strings.Trim(val, `"'`)}
	}
	return out
}

func firstOf(m map[string][]string, keys ...string) string {
	for _, k := range keys {
		if v := m[k]; len(v) > 0 && v[0] != "" {
			return v[0]
		}
	}
	return ""
}

func parsePluginYML(data []byte, loader, source string) (*modMeta, error) {
	kv := parseSimpleYAML(string(data))
	name := firstOf(kv, "name")
	if name == "" {
		return nil, fmt.Errorf("%s 里没有 name", source)
	}
	m := &modMeta{
		ID:          strings.ToLower(strings.ReplaceAll(name, " ", "")),
		Name:        name,
		Version:     firstOf(kv, "version"),
		Description: firstOf(kv, "description"),
		Loader:      loader,
		Source:      source,
		Homepage:    firstOf(kv, "website"),
	}
	for _, k := range []string{"author", "authors"} {
		for _, a := range kv[k] {
			if a != "" {
				m.Authors = append(m.Authors, a)
			}
		}
	}
	addDeps := func(key, kind string) {
		for _, id := range kv[key] {
			if id == "" {
				continue
			}
			d := modDep{ID: id, Kind: kind, Builtin: isBuiltinMod(id)}
			if kind == "required" {
				m.Requires = append(m.Requires, d)
			} else {
				m.Optional = append(m.Optional, d)
			}
		}
	}
	addDeps("depend", "required")
	addDeps("depends", "required")
	addDeps("softdepend", "optional")
	addDeps("soft-depends", "optional")
	return m, nil
}

// ── Velocity ──

func parseVelocityJSON(data []byte, loader, source string) (*modMeta, error) {
	var v struct {
		ID           string   `json:"id"`
		Name         string   `json:"name"`
		Version      string   `json:"version"`
		Description  string   `json:"description"`
		Authors      []string `json:"authors"`
		URL          string   `json:"url"`
		Dependencies []struct {
			ID       string `json:"id"`
			Optional bool   `json:"optional"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	m := &modMeta{
		ID:          v.ID,
		Name:        v.Name,
		Version:     v.Version,
		Description: v.Description,
		Authors:     v.Authors,
		Loader:      loader,
		Source:      source,
		Homepage:    v.URL,
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	for _, d := range v.Dependencies {
		if d.ID == "" {
			continue
		}
		item := modDep{ID: d.ID, Builtin: isBuiltinMod(d.ID)}
		if d.Optional {
			item.Kind = "optional"
			m.Optional = append(m.Optional, item)
		} else {
			item.Kind = "required"
			m.Requires = append(m.Requires, item)
		}
	}
	return m, nil
}

// ── 入口 ──

// 元数据候选文件，按检查顺序。放在包级变量里便于测试与将来扩展。
var metaCandidates = []struct {
	path   string
	loader string
	parse  func([]byte, string, string) (*modMeta, error)
}{
	{"fabric.mod.json", "fabric", parseFabricLike},
	{"quilt.mod.json", "quilt", parseFabricLike},
	{"META-INF/neoforge.mods.toml", "neoforge", parseForgeToml},
	{"META-INF/mods.toml", "forge", parseForgeToml},
	{"velocity-plugin.json", "velocity", parseVelocityJSON},
	{"paper-plugin.yml", "paper", parsePluginYML},
	{"plugin.yml", "bukkit", parsePluginYML},
	{"bungee.yml", "bungeecord", parsePluginYML},
}

// placeholderRe 构建期占位符，如 ${version} / ${mod_version}。
var placeholderRe = regexp.MustCompile(`\$\{[^}]*\}`)

// stripPlaceholder 去掉构建期占位符。
//
// 真实样本里见过（逐字抄自 Fabric API 仓库的
// fabric.mod.json，见 testdata/fabric-api-real.mod.json）：
//
//	"version": "${version}"
//
// 这个占位符本该被 Gradle 在打包时替换掉。真在 jar 里见到它，说明这份 jar 是从
// 源码目录直接打包、或替换步骤失败的产物 —— **把它当版本号显示出去就是个假信息**
// （用户会以为模组版本叫 "${version}"）。宁可留空，让界面如实说「版本未知」。
func stripPlaceholder(s string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	return strings.TrimSpace(placeholderRe.ReplaceAllString(s, ""))
}

// cleanMeta 清掉各字段里的构建期占位符
func (m *modMeta) cleanMeta() {
	m.ID = stripPlaceholder(m.ID)
	m.Name = stripPlaceholder(m.Name)
	m.Version = stripPlaceholder(m.Version)
	if m.Name == "" {
		m.Name = m.ID
	}
}

// normZipName 把归档条目名统一成 `/` 分隔。
//
// 为什么需要：zip 规范里分隔符就该是 `/`，Gradle 打出来的 jar 也是这么写的。
// 但用 Windows 上的一些工具（包括 .NET 的 ZipFile）重新打包时，条目名会变成
// `META-INF\mods.toml` —— 于是「用户自己重新压过的那个包」永远读不出元数据，
// 而报错只有一句「没有认识的元数据文件」，谁都看不出是这个原因。
func normZipName(s string) string {
	return strings.ReplaceAll(s, "\\", "/")
}

// ReadModMeta 打开 jar 读元数据。读不到（不是标准模组、或 jar 损坏）返回 error，
// 调用方据此退回「按文件名猜」—— 不能因为读不出元数据就把这个文件从列表里藏起来。
func ReadModMeta(jarPath string) (*modMeta, error) {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, fmt.Errorf("打不开 jar：%w", err)
	}
	defer zr.Close()

	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[normZipName(f.Name)] = f
	}
	for _, c := range metaCandidates {
		f, ok := byName[c.path]
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, 1<<20)) // 元数据不该超过 1MB
		rc.Close()
		if err != nil {
			continue
		}
		m, err := c.parse(data, c.loader, c.path)
		if err != nil || m == nil || m.ID == "" {
			continue
		}
		m.cleanMeta()
		if m.ID == "" {
			continue
		}
		// 图标路径可能是 {modid}.png 这种占位写法，确认它真在 jar 里
		if m.IconPath != "" {
			p := normZipName(m.IconPath)
			if _, ok := byName[p]; !ok {
				m.IconPath = ""
			} else {
				m.IconPath = p
			}
		}
		return m, nil
	}
	return nil, fmt.Errorf("这个 jar 里没有认识的元数据文件")
}

// ModIconFile 返回 jar 内的图标数据与 MIME（前端单独请求，避免列表接口背着一堆图片）。
//
// nestedSource 是元数据**实际**来自哪儿，形如
// `META-INF/jarjar/xxx.jar!META-INF/mods.toml`（见 readModMetaNested）。
//
// ⚠ 为什么必须管这一层：Forge 的「外套 jar」很常见 —— 真模组塞在
// `META-INF/jarjar/*.jar` 里，它声明的 `logo.png` 自然也在那个**内层** jar 里。
// 元数据能钻进去读，图标却只翻外层，结果就是列表说「有图标」、图标接口 404、
// 界面上只剩一个占位图标（用户原话：「为什么已装界面，有时候没有模组图标？」）。
// 两头必须用同一套解析，否则这种「说了有、给了 404」比干脆没有还费解。
func ModIconFile(jarPath, iconPath, nestedSource string) ([]byte, string, error) {
	if iconPath == "" {
		return nil, "", fmt.Errorf("没有图标")
	}
	// 元数据来自内层 jar 时，图案也去内层找。`!` 前是内层 jar 在外层里的路径。
	if i := strings.Index(nestedSource, "!"); i > 0 {
		data, mime, err := iconInNested(jarPath, nestedSource[:i], iconPath)
		if err == nil {
			return data, mime, nil
		}
		// 内层找不到就继续往外层找一遍：有些包把图标放在外层、元数据却在内层。
	}
	return iconInZip(jarPath, iconPath)
}

// iconInZip 在外层 jar 里找图标。
func iconInZip(jarPath, iconPath string) ([]byte, string, error) {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, "", err
	}
	defer zr.Close()
	want := normZipName(iconPath)
	for _, f := range zr.File {
		if normZipName(f.Name) != want {
			continue
		}
		return readZipEntry(f, iconPath)
	}
	return nil, "", fmt.Errorf("jar 里没有 %s", iconPath)
}

// iconInNested 先把内层 jar 取出来，再在里面找图标。
// 内层 jar 整个读进内存 —— 图标本身很小，但内层可能是几十 MB 的模组包，
// 所以给一个上限（跟读元数据时同一个量级），超了就放弃而不是把内存吃光。
func iconInNested(jarPath, innerPath, iconPath string) ([]byte, string, error) {
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, "", err
	}
	defer zr.Close()
	want := normZipName(innerPath)
	var raw []byte
	for _, f := range zr.File {
		if normZipName(f.Name) != want {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", err
		}
		raw, err = io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, "", err
		}
		break
	}
	if raw == nil {
		return nil, "", fmt.Errorf("jar 里没有内层 %s", innerPath)
	}
	zr2, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, "", err
	}
	// 内层 jar 里的路径同样要走 normZipName（Windows 上重打过的包会带反斜杠）
	for _, f := range zr2.File {
		if normZipName(f.Name) != normZipName(iconPath) {
			continue
		}
		return readZipEntry(f, iconPath)
	}
	return nil, "", fmt.Errorf("内层 jar 里没有 %s", iconPath)
}

func readZipEntry(f *zip.File, iconPath string) ([]byte, string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, "", err
	}
	defer rc.Close()
	// 图标不该超过 2MB；超了就是有人把别的东西当图标
	data, err := io.ReadAll(io.LimitReader(rc, 2<<20))
	if err != nil {
		return nil, "", err
	}
	return data, mimeByExt(iconPath), nil
}

func mimeByExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "application/octet-stream"
}

// ── jar 里嵌的 jar（Forge 常见）──
//
// Forge 模组经常把真正的模组类塞在 META-INF/jarjar/ 下，那种情况顶层
// mods.toml 已经声明了身份，不需要再往下钻。这里只做「顶层读不到时，
// 尝试读一层嵌套 jar」的兜底 —— 比如某些打包工具产出的外套 jar。
func readModMetaNested(jarPath string) (*modMeta, error) {
	if m, err := ReadModMeta(jarPath); err == nil {
		return m, nil
	}
	zr, err := zip.OpenReader(jarPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	tmpDir, err := os.MkdirTemp("", "sbx-modmeta-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	for _, f := range zr.File {
		name := normZipName(f.Name)
		if !jarRe.MatchString(name) || f.FileInfo().IsDir() {
			continue
		}
		if !strings.HasPrefix(name, "META-INF/jarjar/") && !strings.HasPrefix(name, "META-INF/libraries/") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		tmp := filepath.Join(tmpDir, strconv.Itoa(len(name))+".jar")
		out, err := os.Create(tmp)
		if err != nil {
			rc.Close()
			continue
		}
		_, cpErr := io.Copy(out, io.LimitReader(rc, 64<<20))
		rc.Close()
		out.Close()
		if cpErr != nil {
			continue
		}
		if m, err := ReadModMeta(tmp); err == nil {
			m.Source = name + "!" + m.Source
			return m, nil
		}
	}
	return nil, fmt.Errorf("这个 jar 里没有认识的元数据文件")
}
