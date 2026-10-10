package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// 通用配置文件编辑器。
//
// 为什么要有这个：server.properties 我们能做成表单，但 bukkit.yml / spigot.yml /
// paper-global.yml / 每个插件自己的 config.yml，还有模组的 config/*.toml ——
// 这些没有统一 schema，做成表单是妄想。而它们恰恰是最常需要手改的一批文件。
// 所以给一个「能看见、能改、改坏前有提示」的通用编辑器，而不是继续假装它们不存在。

const (
	// configReadLimit 超过这个大小就不在编辑器里开了。
	// 512 KB 的「配置文件」基本已经是数据文件了，浏览器里打开只会卡。
	configReadLimit = 512 * 1024
	// configDepthLimit 从实例根算起最多往下走几层。
	// plugins/EssentialsX/config.yml 是 3 层，够用；再深就是在扫数据目录了。
	configDepthLimit = 3
)

// ConfigFile 列表里的一项
type ConfigFile struct {
	Path    string    `json:"path"` // 相对实例目录，正斜杠
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Lang    string    `json:"lang"` // yaml | toml | json | properties | text
	Depth   int       `json:"depth"`
}

// ConfigExcluded 被排除掉的文件 + 理由。
// 不静默跳过：用户会以为「这文件丢了」，而且他会去别的地方瞎找。
type ConfigExcluded struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ConfigIssue 一条结构问题，带行号好让编辑器跳过去
// ConfigIssue 一条结构问题，带行号好让编辑器跳过去。
//
// Approx 表示行号只是「附近」，不是精确位置 —— YAML 库给的行号就是这样的：
// 实测 6 个样本里 3 个精确、1 个偏一行、1 个在「引号没闭合」时直接指到
// 文件末尾之外（文件只有 2 行，它报第 3 行）。界面必须照实说「附近」，
// 拍胸脯说「就是这一行」会把人带到一行完全正常的代码前面，比不给行号更糟。
type ConfigIssue struct {
	Line    int    `json:"line"`
	Col     int    `json:"col,omitempty"`
	Approx  bool   `json:"approx,omitempty"`
	Message string `json:"message"`
}

// ConfigList 列表响应。两个集合都要保证非 nil —— 前端的 .length 会直接打崩整页。
type ConfigList struct {
	Root     string           `json:"root"`
	Files    []ConfigFile     `json:"files"`
	Excluded []ConfigExcluded `json:"excluded"`
}

// ConfigContent 读回的内容
type ConfigContent struct {
	ConfigFile
	Text      string        `json:"text"`
	Textual   bool          `json:"textual"`
	EOL       string        `json:"eol"` // lf | crlf —— 保存时按原样写回，别把整份文件的行尾掀了
	Problem   string        `json:"problem,omitempty"`
	Issues    []ConfigIssue `json:"issues"`
	Backup    string        `json:"backup,omitempty"` // 上一次保存时留下的备份（相对数据目录）
	HasBackup bool          `json:"hasBackup"`
}

// configExtLang 扩展名 → 语言。决定高亮和校验方式。
var configExtLang = map[string]string{
	".yml": "yaml", ".yaml": "yaml",
	".toml":         "toml",
	".json":         "json",
	".properties":   "properties",
	".conf":         "properties",
	".cfg":          "properties",
	".txt":          "text",
	".md":           "text",
	".json5":        "json",
	".editorconfig": "properties",
}

// configSkipDirs 一律不进的目录。这些里面全是数据或缓存，进去只会刷出一屏噪音。
var configSkipDirs = map[string]bool{
	"logs": true, "libraries": true, "versions": true, "cache": true,
	"crash-reports": true, "backups": true, "backup": true,
	"playerdata": true, "region": true, "entities": true, "poi": true,
	"advancements": true, "stats": true, "session": true,
	"DIM-1": true, "DIM1": true,
	"node_modules": true, ".git": true, ".gradle": true, ".idea": true,
	"__pycache__": true, "run": true,
}

// configTopOrder 顶层的这几个文件排最前面 —— 它们是真正天天要看的那几个。
var configTopOrder = []string{
	"server.properties", "bukkit.yml", "spigot.yml",
	"paper-global.yml", "paper-world-defaults.yml",
	"config/paper-global.yml", "config/paper-world-defaults.yml",
}

// configManagedElsewhere 这些文件由我们自己的界面维护，不放进来手改。
//
// 放进来会发生什么：用户在玩家管理页点了「加白名单」，界面按磁盘重读显示成功；
// 然后他手改了 whitelist.json，界面下次读到的和他以为的不一样 —— 两边打架，
// 而且是我们给了他两个都能改的入口造成的。
//
// server.properties 不在这张表里：它有专属的表单入口，理由单独写。
var configManagedElsewhere = map[string]string{
	"eula.txt":            "同意 Minecraft EULA 在「启动前体检」里点，这里手改容易漏掉记录",
	"whitelist.json":      "由「玩家管理」页维护 —— 两个入口改同一份名单迟早对不上",
	"ops.json":            "由「玩家管理」页维护",
	"banned-players.json": "由「玩家管理」页维护",
	"banned-ips.json":     "由「玩家管理」页维护",
	"usercache.json":      "服务端自己维护的正版用户缓存，手改没有意义",
}

// langOf 按扩展名判断语言
func langOf(name string) (string, bool) {
	ext := strings.ToLower(filepath.Ext(name))
	lang, ok := configExtLang[ext]
	return lang, ok
}

// ── 路径安全 ──────────────────────────────────

// sameOrUnder 判断 p 是不是 root 本身或它的子孙。Windows 上大小写不敏感。
func sameOrUnder(root, p string) bool {
	if runtime.GOOS == "windows" {
		root, p = strings.ToLower(root), strings.ToLower(p)
	}
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

// resolveConfigPath 把「相对路径」解析成一个**确定在实例目录内**的绝对路径。
//
// 这是整个功能唯一的攻击面：路径是前端传上来的，`../../` 能一路读到 C:\Windows。
// 所以三层都要过：Clean 之后不许是绝对路径、不许以 .. 开头；拼出来的绝对路径
// 必须在实例目录下；符号链接解出来的真实路径也必须在实例目录下。
func (m *Manager) resolveConfigPath(id, rel string) (string, Instance, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", Instance{}, fmt.Errorf("实例不存在: %s", id)
	}
	if strings.TrimSpace(rel) == "" {
		return "", inst, fmt.Errorf("没给文件路径")
	}
	// 前导斜杠在「相对路径」里永远不合法，而且它的含义跟平台有关：
	// Windows 上 filepath.IsAbs("/etc/passwd") 是 false（那是「当前盘的根」），
	// 拼出来会落在实例目录里 —— 安全，但语义含糊。干脆一律拒掉，两个平台行为一致。
	if strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return "", inst, fmt.Errorf("路径不能以斜杠开头：%s", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", inst, fmt.Errorf("路径越出实例目录：%s", rel)
	}

	rootAbs, err := filepath.Abs(inst.Dir)
	if err != nil {
		return "", inst, err
	}
	// 实例目录本身可能是软链（用户就爱这么放），先归一化，否则后面比前缀会误判
	if real, err := filepath.EvalSymlinks(rootAbs); err == nil {
		rootAbs = real
	}
	full, err := filepath.Abs(filepath.Join(rootAbs, clean))
	if err != nil {
		return "", inst, err
	}
	if !sameOrUnder(rootAbs, full) {
		return "", inst, fmt.Errorf("路径越出实例目录：%s", rel)
	}
	// 真实路径也校验一次。否则实例目录里放一个指向 C:\ 的软链就能绕出去。
	if real, err := filepath.EvalSymlinks(full); err == nil {
		if !sameOrUnder(rootAbs, real) {
			return "", inst, fmt.Errorf("路径经过符号链接指向了实例目录外面：%s", rel)
		}
		full = real
	}
	return full, inst, nil
}

// ── 列表 ─────────────────────────────────────

// ListConfigs 列出实例目录下可以编辑的配置文件。
func (m *Manager) ListConfigs(id string) (ConfigList, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return ConfigList{}, fmt.Errorf("实例不存在: %s", id)
	}
	out := ConfigList{Root: inst.Dir, Files: []ConfigFile{}, Excluded: []ConfigExcluded{}}

	if st, err := os.Stat(inst.Dir); err != nil || !st.IsDir() {
		// 实例目录还没建出来不算错误 —— 刚建好还没下服务端就是这样
		return out, nil
	}

	// 世界目录名是动态的（properties 里的 level-name），别硬编码只跳 world
	worlds := map[string]bool{
		m.worldDirName(id): true,
		"world_nether":     true,
		"world_the_end":    true,
	}

	_ = filepath.WalkDir(inst.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// 单个条目读不了不致命，跳过就好（权限、占用都会走到这）
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(inst.Dir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		base := d.Name()
		if strings.HasPrefix(base, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			if configSkipDirs[base] || worlds[base] {
				return fs.SkipDir
			}
			if strings.Count(rel, "/") >= configDepthLimit {
				return fs.SkipDir
			}
			return nil
		}
		// 软链一律不列：它可能指向实例外面，编辑器点开就窜出去了
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		if !strings.Contains(rel, "/") {
			if base == "server.properties" {
				out.Excluded = append(out.Excluded, ConfigExcluded{
					Path:   rel,
					Reason: "由「配置」页的表单维护 —— 那边有范围校验和逐项解释，比手改安全",
				})
				return nil
			}
			if reason, ok := configManagedElsewhere[base]; ok {
				out.Excluded = append(out.Excluded, ConfigExcluded{Path: rel, Reason: reason})
				return nil
			}
		}

		lang, ok := langOf(base)
		if !ok {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		out.Files = append(out.Files, ConfigFile{
			Path:    rel,
			Name:    base,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Lang:    lang,
			Depth:   strings.Count(rel, "/"),
		})
		return nil
	})

	sort.SliceStable(out.Files, func(i, j int) bool {
		ri, rj := configRank(out.Files[i].Path), configRank(out.Files[j].Path)
		if ri != rj {
			return ri < rj
		}
		return out.Files[i].Path < out.Files[j].Path
	})
	sort.SliceStable(out.Excluded, func(i, j int) bool { return out.Excluded[i].Path < out.Excluded[j].Path })
	return out, nil
}

// configRank 排序权重：顶层那几个常用文件排前面，其余按路径。
func configRank(p string) int {
	for i, k := range configTopOrder {
		if p == k {
			return i
		}
	}
	return len(configTopOrder) + strings.Count(p, "/")
}

// ── 读取 ─────────────────────────────────────

// ReadConfig 读一个配置文件。读不了不是错误 —— 返回 Problem 让界面说清楚原因，
// 比抛一个 500 然后前端弹「未知错误」强得多。
func (m *Manager) ReadConfig(id, rel string) (ConfigContent, error) {
	full, _, err := m.resolveConfigPath(id, rel)
	if err != nil {
		return ConfigContent{}, err
	}
	st, err := os.Stat(full)
	if err != nil {
		return ConfigContent{}, fmt.Errorf("文件不存在：%s", rel)
	}
	if st.IsDir() {
		return ConfigContent{}, fmt.Errorf("%s 是个目录", rel)
	}
	lang, _ := langOf(st.Name())
	out := ConfigContent{
		ConfigFile: ConfigFile{
			Path: rel, Name: st.Name(), Size: st.Size(), ModTime: st.ModTime(), Lang: lang,
		},
		EOL:    "lf",
		Issues: []ConfigIssue{},
	}
	if b, ok := m.lastBackup(id, rel); ok {
		out.HasBackup = true
		out.Backup = b
	}

	if st.Size() > configReadLimit {
		out.Problem = fmt.Sprintf(
			"这个文件有 %s，超过 %d KB 就不在编辑器里开了 —— 这个体积的多半是数据不是配置。要用别的工具改。",
			humanSize(st.Size()), configReadLimit/1024)
		return out, nil
	}

	raw, err := os.ReadFile(full)
	if err != nil {
		out.Problem = "读不了这个文件：" + err.Error()
		return out, nil
	}
	if !isTextual(raw) {
		out.Problem = "这个文件不是文本（含二进制内容），没法当配置改。"
		return out, nil
	}

	text, eol := normalizeEOL(string(raw))
	out.Text = text
	out.Textual = true
	out.EOL = eol
	out.Issues = ValidateConfigText(lang, text)
	return out, nil
}

// isTextual 粗判是不是文本：有 NUL 字节、或者不是合法 UTF-8，就当二进制。
func isTextual(b []byte) bool {
	head := b
	if len(head) > 4096 {
		head = head[:4096]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	// BOM 之外的非法 UTF-8 一律当二进制
	return utf8.Valid(b)
}

// normalizeEOL 统一成 \n 交给编辑器，并记住原来是哪种行尾。
// 记住是必须的：一份 CRLF 的文件被我们用 LF 存回去，git 上会显示整份文件都改了。
func normalizeEOL(s string) (string, string) {
	crlf := strings.Count(s, "\r\n")
	lf := strings.Count(s, "\n")
	if crlf > 0 && crlf*2 >= lf {
		return strings.ReplaceAll(s, "\r\n", "\n"), "crlf"
	}
	return s, "lf"
}

func denormalizeEOL(s, eol string) string {
	if eol != "crlf" {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\n", "\r\n")
}

func humanSize(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.0f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%d B", n)
}

// ── 校验 ─────────────────────────────────────

// yamlFirstBadLine 自己算出「是从第几行开始解析不过的」。
//
// 为什么不直接用 yaml 库报的行号：那个数字**完全不能信**。实测：
//   - 一份 7 行的文件、第 7 行缩进错了，库报的是「第 1 行」；
//   - 「引号没闭合」时文件只有 2 行，库报到第 3 行（文件外面）；
//   - 同一类缩进错误，有时报准、有时偏一行。
//
// 拿这种行号去做「跳到出错行」，等于把用户带到一行完全正常的代码前面 ——
// 比不给行号更糟，因为他会开始怀疑那个正常的写法有问题。
//
// 做法：对「前 k 行」做二分。前 k 行能解析、前 k+1 行不能，问题就出在第 k+1 行。
// 只在已经确定整份文本解析失败时才调用，最多解析 log2(行数) 次，开销可以忽略。
func yamlFirstBadLine(text string) int {
	lines := strings.Split(text, "\n")
	bad := func(k int) bool {
		var v any
		return yaml.Unmarshal([]byte(strings.Join(lines[:k], "\n")), &v) != nil
	}
	if bad(1) {
		return 1
	}
	// bad(1) = false，bad(len(lines)) = true（调用方已经确认整份解析失败）
	lo, hi := 1, len(lines)
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if bad(mid) {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi
}

// RenderedLineCount 编辑器会画出多少行。
//
// 定义是「按 \n 切，末尾那个空行也算一行」—— 和文本编辑器、和前端 split('\n')
// 的口径一致。行号校验和跳转都必须用这一个口径，否则会出现
// 「报第 3 行、但界面只有 2 行」这种谁都看不懂的错位。
func RenderedLineCount(text string) int {
	return strings.Count(text, "\n") + 1
}

// ValidateConfigText 按语言做结构校验。
//
// 这是这个编辑器存在的主要理由之一：手改 YAML 少一个缩进，服务端启动时才炸，
// 而且报错信息藏在几十万行日志里。在这里当场标出来，省一整轮试错。
func ValidateConfigText(lang, text string) []ConfigIssue {
	issues := []ConfigIssue{}
	if strings.TrimSpace(text) == "" {
		// 空文件在 YAML/TOML 里是合法的（等于空配置），在 JSON 里不是
		if lang == "json" {
			return append(issues, ConfigIssue{Line: 1, Col: 1, Message: "JSON 文件不能为空"})
		}
		return issues
	}

	switch lang {
	case "json":
		var v any
		if err := json.Unmarshal([]byte(text), &v); err != nil {
			line, col := 1, 1
			var se *json.SyntaxError
			if errors.As(err, &se) {
				line, col = lineColAt(text, int(se.Offset))
			} else {
				line, col = 1, 1
			}
			issues = append(issues, ConfigIssue{Line: line, Col: col, Message: err.Error()})
		}
	case "toml":
		var v map[string]any
		if _, err := toml.Decode(text, &v); err != nil {
			line, col := 1, 1
			var pe toml.ParseError
			if errors.As(err, &pe) {
				line, col = pe.Position.Line, pe.Position.Col
				issues = append(issues, ConfigIssue{Line: line, Col: col, Message: pe.Message})
				break
			}
			issues = append(issues, ConfigIssue{Line: line, Col: col, Message: err.Error()})
		}
	case "yaml":
		var v any
		if err := yaml.Unmarshal([]byte(text), &v); err != nil {
			issues = append(issues, ConfigIssue{
				Line:    yamlFirstBadLine(text),
				Col:     1,
				Approx:  true,
				Message: stripLocPrefix(strings.TrimPrefix(err.Error(), "yaml: ")),
			})
		}
	case "properties":
		issues = append(issues, duplicatePropKeys(text)...)
	}
	return issues
}

// stripLocPrefix 剥掉错误消息开头自带的坐标前缀，形如「line 1:」/「line 1: column 3:」。
//
// 为什么必须剥：yaml.v3 给的行号本身就是错的（见 yamlFirstBadLine 的注释），
// 而外面已经用**我们算出来的**行号标注了「第 7 行附近」。不剥的话界面上会并排出现
// 「第 7 行附近 line 1: did not find expected key」—— 两个行号互相打架，
// 用户第一反应是「到底哪一行错了」，甚至以为文件里有两处错。
func stripLocPrefix(msg string) string {
	s := strings.TrimSpace(msg)
	for {
		// 形式固定为「line 12:」或「column 3:」—— 一个词 + 空格 + 纯数字 + 冒号
		j := strings.Index(s, ":")
		if j < 0 {
			break
		}
		f := strings.Fields(s[:j])
		if len(f) != 2 || (f[0] != "line" && f[0] != "column") || !isAllDigits(f[1]) {
			break
		}
		s = strings.TrimSpace(s[j+1:])
	}
	if s == "" {
		return msg // 整条消息只有坐标、没有别的信息：宁可原样留着，也别还回去一个空串
	}
	return s
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// lineColAt 把字节偏移换算成行列（都从 1 开始）。JSON 的 SyntaxError 只给 Offset。
func lineColAt(s string, offset int) (int, int) {
	if offset < 1 {
		return 1, 1
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset-1; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

// duplicatePropKeys 找重复键。
//
// Java 的 Properties 遇到重复键是**静默取最后一个** —— 文件里写着 true、
// 你以为是 false，排查半天。这个坑值得单独报一条。
func duplicatePropKeys(text string) []ConfigIssue {
	issues := []ConfigIssue{}
	first := map[string]int{}
	for i, raw := range strings.Split(text, "\n") {
		t := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
			continue
		}
		key := propKeyOf(t)
		if key == "" {
			continue
		}
		if at, ok := first[key]; ok {
			issues = append(issues, ConfigIssue{
				Line: i + 1,
				Message: fmt.Sprintf("键「%s」在第 %d 行已经出现过 —— Java 读配置时**后面那个生效**，前面那行是白写的",
					key, at),
			})
			continue
		}
		first[key] = i + 1
	}
	return issues
}

// propKeyOf 取一行的键名。`=`、`:` 和第一个空白都是分隔符（Java 的规矩）。
func propKeyOf(line string) string {
	cut := len(line)
	for i, r := range line {
		if r == '=' || r == ':' || r == ' ' || r == '\t' {
			cut = i
			break
		}
	}
	return strings.TrimSpace(line[:cut])
}

// ── 写入 ─────────────────────────────────────

// WriteConfig 保存。expectModTime 非零时会做冲突检测 ——
// 服务端在跑的时候可能自己重写过这个文件（比如 /difficulty 会回写 properties），
// 不看 mtime 就覆盖，等于把服务端刚写的东西抹掉。
func (m *Manager) WriteConfig(id, rel, text, eol string, expectModTime time.Time) error {
	full, _, err := m.resolveConfigPath(id, rel)
	if err != nil {
		return err
	}
	st, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("文件不存在：%s", rel)
	}
	if !expectModTime.IsZero() && !st.ModTime().Equal(expectModTime) {
		return fmt.Errorf(
			"文件在你打开之后被改过了（磁盘上是 %s，你打开时是 %s）—— 刷新看一眼再决定要不要覆盖，免得把别人的改动冲掉",
			st.ModTime().Format("15:04:05"), expectModTime.Format("15:04:05"))
	}

	old, _ := os.ReadFile(full)
	// 先留一份上一版。配置编辑器最大的风险就是「改坏了起不来」，
	// 有一份能捞回来的一版，用户才敢下手。
	if err := m.saveBackup(id, rel, old); err != nil {
		return fmt.Errorf("写之前想留一份备份，没成功：%w", err)
	}

	out := denormalizeEOL(text, eol)
	// 结尾补一个换行：POSIX 文本文件该有，而且不少解析器对最后一行没换行会报怪错
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	if err := os.WriteFile(full, []byte(out), st.Mode().Perm()); err != nil {
		return err
	}
	return nil
}

// configBackupPath 备份放在我们自己的数据目录里，不污染实例目录 ——
// 丢一个 .bak 到实例目录会被服务端或插件当成配置文件扫到。
func (m *Manager) configBackupPath(id, rel string) string {
	safe := strings.NewReplacer("/", "__", "\\", "__", ":", "_").Replace(rel)
	return filepath.Join(m.baseDir, "config-backups", id, safe)
}

func (m *Manager) saveBackup(id, rel string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	p := m.configBackupPath(id, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// lastBackup 有没有留下过备份，有就返回它的路径（相对数据目录，给界面显示用）
func (m *Manager) lastBackup(id, rel string) (string, bool) {
	p := m.configBackupPath(id, rel)
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return filepath.ToSlash(strings.TrimPrefix(p, m.baseDir+string(filepath.Separator))), true
	}
	return "", false
}

// RestoreConfig 用上一次保存前留下的备份覆盖当前文件。
func (m *Manager) RestoreConfig(id, rel string) error {
	full, _, err := m.resolveConfigPath(id, rel)
	if err != nil {
		return err
	}
	p := m.configBackupPath(id, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		return fmt.Errorf("没有可还原的备份")
	}
	st, err := os.Stat(full)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = st.Mode().Perm()
	}
	return os.WriteFile(full, b, mode)
}
