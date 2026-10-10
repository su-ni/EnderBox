package fetch

// MC 百科（mcmod.cn）的模组关系星图 —— 用户点名要的数据源。
//
// ▍为什么用「静态 JSON」而不是「解析网页」
//
// MC 百科自己的关系图不是服务器渲染的，而是页面里一个按钮带 data-id，前端再去拉一个
// **静态 JSON**：
//
//	按钮：  <button id="class-relation-chart-btn" data-id="XXXX">…</button>
//	数据：  https://www.mcmod.cn/static/public/json/relation/class/XXXX.json
//
// 这个 JSON 就是 echarts 的图数据（categories / nodes / links），内容**远超**网页上能看到的
// 那三个关系组：实测钠(sodium) 302 节点/428 边、机械动力(Create) 515 节点/1003 边，
// 节点自带 `classID`（可点进详情）、`content`（简介）、`symbolSize`（权重→点大小）、
// `deep`（层级）。而解析 HTML 只能拿到「直接邻居」，还漏「拓展」。
// 既然站方自己就是拉这个文件画图的，我们直接拉它对双方都更省事（少解析一步、少一个请求）。
//
// ▍站方 JSON 的真实形状（2026-10-09 逐字段核过，别凭印象改）
//
//	{
//	  "categories": [ {"name":"前置模组","itemStyle":{"normal":{"color":"#ce4844"}}}, … ],
//	  "nodes": [ {"id":0,"name":"钠 (Sodium)","symbolSize":40,"category":3},
//	             {"id":1,"classID":"3413","name":"铟 (Indium)","category":1,"deep":1,
//	              "content":"<span …>概述</span>铟（Indium）是…","symbolSize":20}, … ],
//	  "links": [ {"source":0,"target":1}, … ]
//	}
//
// ⚠⚠ 四个「凭印象一定会写错」的点，写死在注释里：
//   ① **响应是 GBK，不是 UTF-8**（直接当 UTF-8 读会满屏「閽� (Sodium)」）→ 必须转码；
//   ② `id` / `source` / `target` / `category` **都是数字**，不是字符串；
//      且实测 `id` 恒等于数组下标（302 个节点，id≠index 的数量为 0），所以下标引用安全；
//   ③ 分类色在 **`itemStyle.normal.color`**，不是顶层 `color`；
//      且**有一个分类的 `name` 是 `null`**（它就是「中心节点」那一类，色 #13406C）；
//   ④ `content` 是 **HTML 片段**（含 `<span class="common-text-title">概述</span>` 这种），
//      要剥标签 + 反转义才能上屏。
//
// ▍合规与克制（改这个文件前先读这段）
//
// robots.txt **只 Disallow 后台编辑路径**（`/class/edit/`、`/item/add/`…），
// 公开模组页、搜索页、以及这个 static 目录 **都不在禁止列表**里，站方还提供了 sitemap —— 抓这些是它允许的。
// 但 MC 百科是**非营利**站（「关于」页写着不赚钱、靠捐款），所以：
//   ① **三级缓存**：内存 30 分钟 → 磁盘 7 天 → 都没有才联网。用户来回翻模组不该反复打它；
//   ② 请求**串行**、带真实 UA，不并发轰炸；
//   ③ 抓不到就**如实报错**，绝不用旧数据假装成功 —— 用户可能拿这张图去装模组。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ── 数据结构（对外的扁平版，直接喂前端画图）────────────────

// McmodNode 星图里的一个模组节点。
type McmodNode struct {
	ID      string `json:"id"`                // 节点 id（字符串化，links 用它引用）
	Name    string `json:"name"`              // 英文名 / 主标题
	ClassID string `json:"classId,omitempty"` // /class/<classId>.html，可点进百科
	Content string `json:"content,omitempty"` // 简介（站方给的是 HTML，已剥成纯文本）
	Size    int    `json:"size,omitempty"`    // 原始 symbolSize，越大越核心
	Cat     string `json:"cat,omitempty"`     // 分类名（前置模组 / 拓展模组 / 联动模组 / 中心）
	Color   string `json:"color,omitempty"`   // 分类色（站方配色，前端直接用）
	Deep    int    `json:"deep,omitempty"`    // 相对中心的层级（0=中心）
}

// McmodLink 两个节点间的一条关系边。
type McmodLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// McmodCat 一个分类（含站方给的配色）。
type McmodCat struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// McmodRelations 一次查询的完整结果，直接喂给前端画图。
type McmodRelations struct {
	Center    *McmodNode  `json:"center"`
	Nodes     []McmodNode `json:"nodes"`
	Links     []McmodLink `json:"links"`
	Cats      []McmodCat  `json:"cats"`
	Source    string      `json:"source"`              // 数据来源说明，界面要如实标注
	Cached    bool        `json:"cached"`              // 本次是否命中缓存（磁盘/内存）
	FetchedAt time.Time   `json:"fetchedAt,omitempty"` // 数据抓取时间
}

var (
	mcmodClient = &http.Client{Timeout: 15 * time.Second}

	// 关系图按钮：<button id="class-relation-chart-btn" data-id="yCuvlBxnGhqkdtET">…</button>
	// data-id 是**非数字的短串**（实测 16 位混合大小写），别写成 \d+ —— 会一个都匹配不到。
	// 两个正则覆盖「id 在前 / data-id 在前」两种属性顺序。
	mcmodDataIDRe  = regexp.MustCompile(`id="class-relation-chart-btn"[^>]*\bdata-id="([A-Za-z0-9_-]+)"`)
	mcmodDataIDRe2 = regexp.MustCompile(`\bdata-id="([A-Za-z0-9_-]+)"[^>]*id="class-relation-chart-btn"`)

	// 搜索结果里指向模组页的链接（标题里夹着标签，所以整段取再剥）。
	mcmodSearchRe = regexp.MustCompile(`(?s)<a\b[^>]*href="(?:https?:)?//www\.mcmod\.cn(/class/(\d+)\.html)"[^>]*>(.*?)</a>`)
	mcmodTagsRe   = regexp.MustCompile(`(?s)<[^>]+>`)
)

// ── 缓存：内存 + 磁盘 ──────────────────────────────────

const (
	mcmodMemTTL  = 30 * time.Minute
	mcmodDiskTTL = 7 * 24 * time.Hour
)

type mcmodEntry struct {
	val *McmodRelations
	at  time.Time
}

var (
	mcmodMu    sync.Mutex
	mcmodCache = map[string]mcmodEntry{}

	mcmodCacheDirMu sync.RWMutex
	mcmodCacheDir   string // 由 SetMcmodCacheDir 在启动时设；为空则只走内存缓存
)

// SetMcmodCacheDir 指定磁盘缓存目录（通常是 <dataDir>/mcmod-cache）。
// 不设也能跑，只是没有「重启后仍然秒开」的能力。
func SetMcmodCacheDir(dir string) {
	mcmodCacheDirMu.Lock()
	mcmodCacheDir = dir
	mcmodCacheDirMu.Unlock()
	if dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
}

func mcmodDiskDir() string {
	mcmodCacheDirMu.RLock()
	defer mcmodCacheDirMu.RUnlock()
	return mcmodCacheDir
}

// McmodRelationsByName 按模组名（英文 / 中文都行）取它的关系星图。
//
// 三级取数：内存 → 磁盘 → 联网。命中缓存时 Cached=true，前端据此显示「已缓存 · 时间」，
// 并可自行决定要不要在页面开着时定时刷新。名字在百科里没有对应条目 → 返回 ErrMarketNotFound，
// 让界面能说「百科里没找到这个模组」，而不是一句「失败了」。
func McmodRelationsByName(name string) (*McmodRelations, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("缺少模组名")
	}
	key := mcmodKey(name)

	// ① 内存
	mcmodMu.Lock()
	if e, ok := mcmodCache[key]; ok && time.Since(e.at) < mcmodMemTTL {
		val := e.val
		mcmodMu.Unlock()
		cp := *val
		cp.Cached = true
		return &cp, nil
	}
	mcmodMu.Unlock()

	// ② 磁盘
	if r := mcmodReadDisk(key); r != nil {
		mcmodMu.Lock()
		mcmodCache[key] = mcmodEntry{val: r, at: time.Now()}
		mcmodMu.Unlock()
		cp := *r
		cp.Cached = true
		return &cp, nil
	}

	// ③ 联网
	r, err := mcmodFetchLive(key, name)
	if err != nil {
		return nil, err
	}
	mcmodMu.Lock()
	mcmodCache[key] = mcmodEntry{val: r, at: time.Now()}
	mcmodMu.Unlock()
	return r, nil
}

// McmodRefresh 强制跳过缓存重新抓（给「页面一直开着 → 定时刷新」用）。
func McmodRefresh(name string) (*McmodRelations, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("缺少模组名")
	}
	key := mcmodKey(name)
	r, err := mcmodFetchLive(key, name)
	if err != nil {
		return nil, err
	}
	mcmodMu.Lock()
	mcmodCache[key] = mcmodEntry{val: r, at: time.Now()}
	mcmodMu.Unlock()
	return r, nil
}

func mcmodKey(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	return strings.Join(strings.Fields(k), " ")
}

// ── 联网链路 ───────────────────────────────────────────

func mcmodFetchLive(key, name string) (*McmodRelations, error) {
	classID, err := mcmodSearchClassID(name)
	if err != nil {
		return nil, err
	}
	page, err := mcmodGet("https://www.mcmod.cn/class/" + classID + ".html")
	if err != nil {
		return nil, err
	}
	dataID := mcmodDataID(page)
	if dataID == "" {
		// 页面上没有关系图按钮：多半是这个小模组站方没画关系图。
		// 如实返回「没有」，绝不用一个空图冒充成功。
		return nil, ErrMarketNotFound
	}
	raw, err := mcmodGet("https://www.mcmod.cn/static/public/json/relation/class/" + dataID + ".json")
	if err != nil {
		return nil, err
	}
	r, err := parseMcmodGraph(raw, classID)
	if err != nil {
		return nil, err
	}
	r.Cached = false
	r.FetchedAt = time.Now()
	mcmodWriteDisk(key, r)
	return r, nil
}

// mcmodSearchClassID 打搜索页，取第一条模组结果的 classID。
func mcmodSearchClassID(name string) (string, error) {
	body, err := mcmodGet("https://search.mcmod.cn/s?key=" + urlQueryEscape(name))
	if err != nil {
		return "", err
	}
	// ⚠ 正则有 3 个捕获组，FindStringSubmatch 返回的是 [完整匹配 + 3 组] = **4** 个元素。
	// 一度写成 `len(m) == 3`，于是永远进不去分支、每次都报「找不到」—— 单测在真实搜索页上逮到过。
	if m := mcmodSearchRe.FindStringSubmatch(body); len(m) >= 3 {
		return m[2], nil
	}
	return "", ErrMarketNotFound
}

// mcmodDataID 从模组页里抠出关系图的 data-id。
func mcmodDataID(page string) string {
	if m := mcmodDataIDRe.FindStringSubmatch(page); len(m) == 2 {
		return m[1]
	}
	if m := mcmodDataIDRe2.FindStringSubmatch(page); len(m) == 2 {
		return m[1]
	}
	return ""
}

func mcmodGet(u string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "EnderBox/0.2 (Minecraft server panel; personal use)")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := mcmodClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("连不上 MC 百科：%w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return "", ErrMarketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("MC 百科返回 %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("读取 MC 百科返回的内容失败：%w", err)
	}
	return decodeMcmodBytes(b), nil
}

// decodeMcmodBytes 把站方返回的字节转成 UTF-8。
// ⚠ 站方这两个端点都返回 **GBK**（Content-Type 甚至不写 charset），直接 string(b) 会得到
// 满屏「閽� (Sodium)」这种乱码 —— 实测过，不是猜的。转码失败就原样返回（宁可乱码也不要丢数据）。
func decodeMcmodBytes(b []byte) string {
	if utf8Valid(b) {
		return string(b)
	}
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewDecoder(), b)
	if err != nil {
		return string(b)
	}
	return string(out)
}

// utf8Valid 轻量判断：全是合法 UTF-8 就不折腾转码（避免把合法 UTF-8 误转）。
func utf8Valid(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c < 0x80:
			i++
		case c&0xE0 == 0xC0:
			if i+1 >= len(b) || b[i+1]&0xC0 != 0x80 {
				return false
			}
			i += 2
		case c&0xF0 == 0xE0:
			if i+2 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 {
				return false
			}
			i += 3
		case c&0xF8 == 0xF0:
			if i+3 >= len(b) || b[i+1]&0xC0 != 0x80 || b[i+2]&0xC0 != 0x80 || b[i+3]&0xC0 != 0x80 {
				return false
			}
			i += 4
		default:
			return false
		}
	}
	return true
}

// ── JSON 解析（纯函数，单测直接喂字符串）─────────────────

// mcmodRawGraph 站方 JSON 的原始形状。
//
// ⚠ 所有 id/下标类字段都用 json.RawMessage：站方这几处**都是数字**，
// 但为防它哪天改成字符串，统一走「数字或字符串都能收」的辅助函数解析。
type mcmodRawGraph struct {
	Categories []struct {
		Name      *string `json:"name"` // ⚠ 可为 null（中心分类）
		ItemStyle *struct {
			Normal *struct {
				Color string `json:"color"`
			} `json:"normal"`
		} `json:"itemStyle"`
	} `json:"categories"`
	Nodes []struct {
		ID         json.RawMessage `json:"id"`
		Name       string          `json:"name"`
		ClassID    json.RawMessage `json:"classID"` // 字符串（"3413"），中心节点缺省
		Content    string          `json:"content"` // HTML 片段
		SymbolSize json.RawMessage `json:"symbolSize"`
		Category   json.RawMessage `json:"category"` // 数字下标
		Deep       int             `json:"deep"`
	} `json:"nodes"`
	Links []struct {
		Source json.RawMessage `json:"source"`
		Target json.RawMessage `json:"target"`
	} `json:"links"`
}

// parseMcmodGraph 把站方 JSON 转成我们前端要的扁平结构。
//
// 关键工作：把 echarts 的「数字下标」引用（category / source / target）解析成**节点 id 串**，
// 并把 `itemStyle.normal.color` 提到节点上（前端直接上色，不用自己维护分类色表），
// 最后回填中心节点的 classID（JSON 里中心节点常缺 classID，用搜索拿到的补上，这样中心点也能点进百科）。
func parseMcmodGraph(raw, centerClassID string) (*McmodRelations, error) {
	var rg mcmodRawGraph
	if err := json.Unmarshal([]byte(raw), &rg); err != nil {
		return nil, fmt.Errorf("MC 百科关系数据格式变了：%w", err)
	}
	if len(rg.Nodes) == 0 {
		return nil, ErrMarketNotFound
	}

	// 分类：name 为 null 的那个是「中心」那一类（站方这么给的，实测）。
	cats := make([]McmodCat, 0, len(rg.Categories))
	for _, c := range rg.Categories {
		name := ""
		if c.Name != nil {
			name = strings.TrimSpace(*c.Name)
		}
		col := ""
		if c.ItemStyle != nil && c.ItemStyle.Normal != nil {
			col = strings.TrimSpace(c.ItemStyle.Normal.Color)
		}
		cats = append(cats, McmodCat{Name: name, Color: col})
	}

	idByIndex := make([]string, len(rg.Nodes))
	nodes := make([]McmodNode, 0, len(rg.Nodes))
	for i, n := range rg.Nodes {
		id := mcmodRawString(n.ID)
		if id == "" {
			id = fmt.Sprintf("n%d", i)
		}
		idByIndex[i] = id
		catName, catColor := mcmodCatOf(n.Category, cats)
		nodes = append(nodes, McmodNode{
			ID:      id,
			Name:    strings.TrimSpace(n.Name),
			ClassID: mcmodRawString(n.ClassID),
			Content: mcmodPlainText(n.Content),
			Size:    mcmodRawInt(n.SymbolSize),
			Cat:     catName,
			Color:   catColor,
			Deep:    n.Deep,
		})
	}

	// 找中心：deep==0 优先（能识别时）；没有就用 symbolSize 最大的；再没有就用第一个。
	centerIdx := mcmodCenterIndex(rg, nodes)
	center := nodes[centerIdx]
	if center.ClassID == "" {
		center.ClassID = centerClassID // 补上搜索拿到的，中心点也能点进百科
	}
	if center.Cat == "" {
		center.Cat = "中心"
	}
	nodes[centerIdx] = center

	// 中心节点沉到最前（前端按数组顺序画，也是 echarts 的惯例）。
	ordered := make([]McmodNode, 0, len(nodes))
	ordered = append(ordered, center)
	for i, n := range nodes {
		if i == centerIdx {
			continue
		}
		ordered = append(ordered, n)
	}

	links := make([]McmodLink, 0, len(rg.Links))
	linkSet := map[string]bool{}
	validID := map[string]bool{}
	for _, n := range nodes {
		validID[n.ID] = true
	}
	for _, l := range rg.Links {
		s := mcmodRawRef(l.Source, idByIndex, validID)
		t := mcmodRawRef(l.Target, idByIndex, validID)
		if s == "" || t == "" || s == t {
			continue
		}
		// 去重：站方 links 里同一对偶尔会出现两次（正反各一，实测 51/428 是反向的）。
		a, b := s, t
		if a > b {
			a, b = b, a
		}
		k := a + "\x00" + b
		if linkSet[k] {
			continue
		}
		linkSet[k] = true
		links = append(links, McmodLink{Source: s, Target: t})
	}

	return &McmodRelations{
		Center: &ordered[0],
		Nodes:  ordered,
		Links:  links,
		Cats:   cats,
		Source: "MC 百科（mcmod.cn）关系数据",
	}, nil
}

// mcmodCenterIndex 选中心节点。
//
// ⚠ 不能简单地「找第一个 deep==0」：站方有些图**整个不给 deep 字段**，那样所有节点都解码成 0，
// 「第一个 deep==0」就退化成「永远选第 0 个」—— 实测被单测逮住过（TestParseMcmodGraphCenterFallback）。
// 正确做法：只有当**确实存在 deep 字段**（用原始 JSON 判断，而不是解码后的 int）时才信 deep，
// 否则退回「symbolSize 最大者」。
func mcmodCenterIndex(rg mcmodRawGraph, nodes []McmodNode) int {
	hasDeep := false
	for _, n := range rg.Nodes {
		if n.Deep != 0 {
			hasDeep = true
			break
		}
	}
	if hasDeep {
		for i, n := range rg.Nodes {
			if n.Deep == 0 {
				return i
			}
		}
	}
	best, bestSize := 0, -1
	for i, n := range nodes {
		if n.Size > bestSize {
			best, bestSize = i, n.Size
		}
	}
	return best
}

// mcmodRawRef 把一个 links 端点（数字下标 → 节点 id）解析成节点 id 串。
func mcmodRawRef(raw json.RawMessage, idByIndex []string, validID map[string]bool) string {
	s := mcmodRawString(raw)
	if s == "" {
		return ""
	}
	// 纯数字 → echarts 默认当下标（实测 id 恒等于下标，两条路等价，优先下标）。
	if n, err := parseIndex(s); err == nil && n >= 0 && n < len(idByIndex) {
		return idByIndex[n]
	}
	if validID[s] {
		return s
	}
	return ""
}

// mcmodCatOf 把 category（数字下标）解析成 (分类名, 分类色)。
func mcmodCatOf(raw json.RawMessage, cats []McmodCat) (string, string) {
	s := mcmodRawString(raw)
	if s == "" {
		return "", ""
	}
	if n, err := parseIndex(s); err == nil && n >= 0 && n < len(cats) {
		return cats[n].Name, cats[n].Color
	}
	return s, ""
}

func parseIndex(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("非数字")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// mcmodRawString 兼容「字符串 / 数字」两种 JSON 形态，统一成字符串。
func mcmodRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

// mcmodRawInt 兼容「数字 / [宽,高] / null」，统一成整数（取较大边）。
func mcmodRawInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		if f, e := n.Float64(); e == nil {
			return int(f)
		}
	}
	var arr []float64
	if err := json.Unmarshal(raw, &arr); err == nil && len(arr) > 0 {
		m := arr[0]
		for _, v := range arr {
			if v > m {
				m = v
			}
		}
		return int(m)
	}
	return 0
}

// mcmodPlainText 把站方的 content（HTML 片段）剥成纯文本并截断。
// 站方 content 形如 `<span class="common-text-title">概述</span>铟（Indium）是渲染优化模组…`，
// 我们要的是「标签去掉、实体还原、太长截断」—— 上屏的应该是给人看的一句话，不是一段 HTML。
func mcmodPlainText(s string) string {
	s = mcmodTagsRe.ReplaceAllString(s, "")
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	).Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	s = mcmodTrimLead(s)
	// 截断：星图节点上放不了长文，给个能看清的开头就够（前端 hover 会显示全文时再截更狠）。
	const maxRunes = 60
	r := []rune(s)
	if len(r) > maxRunes {
		s = string(r[:maxRunes]) + "…"
	}
	return s
}

// mcmodTrimLead 剥掉站方摘要开头的「📖简介」小节标题残留。
// 站方把详情页的小节标题连着正文一起塞进 content（「概述」「📖简介」两种形态都见过），
// 剥掉标签后正文就成了 "📖简介xxx"。面板自己已经有「简介」小标题，项目又全站禁 emoji ——
// 不剥的话上屏就是「简介 / 📖简介xxx」顶着重复。只认 emoji 打头的形态，正常标题（如「概述」）
// 原文保留。⚠ 对已剥过的串再跑是幂等 no-op，所以读盘老缓存时可以放心补一刀。
func mcmodTrimLead(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "📖") {
		return s
	}
	s = strings.TrimSpace(strings.TrimPrefix(s, "📖"))
	s = strings.TrimSpace(strings.TrimPrefix(s, "简介"))
	s = strings.TrimSpace(strings.TrimLeft(s, "：:"))
	return s
}

// ── 磁盘缓存 ───────────────────────────────────────────

type mcmodDiskFile struct {
	Key       string          `json:"key"`
	SavedAt   time.Time       `json:"savedAt"`
	Relations *McmodRelations `json:"relations"`
}

func mcmodDiskPath(key string) string {
	dir := mcmodDiskDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, mcmodSafeName(key)+".json")
}

func mcmodSafeName(key string) string {
	var b strings.Builder
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r > 127:
			b.WriteString(fmt.Sprintf("_u%x", r))
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

func mcmodReadDisk(key string) *McmodRelations {
	p := mcmodDiskPath(key)
	if p == "" {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var f mcmodDiskFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil
	}
	if f.Relations == nil || len(f.Relations.Nodes) == 0 || time.Since(f.SavedAt) > mcmodDiskTTL {
		return nil
	}
	r := f.Relations
	r.Cached = true
	r.FetchedAt = f.SavedAt
	// 老缓存可能是清洗规则升级前存的（例如 content 还带着「📖简介」前缀）：
	// 读盘时补一遍头部清洗，免得用户不清缓存就一直看到旧脏数据。幂等，不伤新数据。
	for i := range r.Nodes {
		r.Nodes[i].Content = mcmodTrimLead(r.Nodes[i].Content)
	}
	return r
}

func mcmodWriteDisk(key string, r *McmodRelations) {
	p := mcmodDiskPath(key)
	if p == "" {
		return
	}
	f := mcmodDiskFile{Key: key, SavedAt: time.Now(), Relations: r}
	b, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// McmodCacheStats 返回磁盘缓存里有多少条、共多少字节（给设置页/调试用）。
func McmodCacheStats() (count int, bytes int64) {
	dir := mcmodDiskDir()
	if dir == "" {
		return 0, 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if fi, err := e.Info(); err == nil {
			count++
			bytes += fi.Size()
		}
	}
	return count, bytes
}

// ── 小工具 ─────────────────────────────────────────────

// cleanText 去标签 + 反转义实体 + 压空白（搜索结果标题用）。
func cleanText(s string) string {
	s = mcmodTagsRe.ReplaceAllString(s, "")
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
	).Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// urlQueryEscape 只做我们需要的那点转义（中文 → UTF-8 百分号编码）。
func urlQueryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}
