package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/jobs"
	"github.com/su-ni/EnderBox/internal/modpack"
)

// 整合包市场（P1）：搜索 → 选版本 → 看清代价 → 一键装成实例。
//
// 契约在 docs/MODPACK.md 第九节，**改两边之前先改那一节**。
//
// 与升级（第八节）的分界：这里**永远新建实例**；装进已有实例是升级页（?version=）的事。
// 复用升级那三样东西，一行都不重写：
//   - computeModpackPlan 算计划（同一份代码，所以预览/执行的口径不会漂）
//   - modpack.Execute     执行
//   - 统一作业表 jobStore 看进度、取消、同实例单作业闸（/api/jobs）

func (s *Server) modpackMarketRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/modpacks/search", s.modpackSearch)
	mux.HandleFunc("GET /api/modpacks/project", s.modpackProject)
	mux.HandleFunc("GET /api/modpacks/versions", s.modpackVersions)
	mux.HandleFunc("POST /api/modpacks/inspect", s.modpackInspect)
	mux.HandleFunc("POST /api/modpacks/install", s.modpackInstall)
}

// serverSideFilter 默认要的服务端侧档位。
//
// 实测（2026-10-06）：整合包总数 18669，其中 server_side ∈ {required, optional} 的 13019，
// `unsupported` 5755（余 105 是 unknown）。**滤掉的那 30% 正是装到服务端没有用的客户端包**，
// 把它们留在列表里，用户点进去才在下一步被拒 —— 那是拿他的时间替我们的列表充数。
var serverSideFilter = []string{"required", "optional"}

// modpackStartJar 起服务端文件下载。做成**包级变量**是为了测试能换掉它。
//
// 不换的话，接口级测试里每装一次 forge 包都会真去连 maven.minecraftforge.net ——
// 测试结果就取决于当时网络通不通（薛定谔的测试）。这与 modpackRunning 是同一条理由。
var modpackStartJar = (*Server).startServerJar

// ── 搜索 ──────────────────────────────────────────────────

func (s *Server) modpackSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base := s.set.Get().MarketBase
	// all=1 关掉服务端过滤。默认滤，且响应里如实回报 onlyServer，界面要据此说明。
	onlyServer := q.Get("all") != "1"

	run := func(query string) ([]fetch.MarketHit, string, error) {
		mq := fetch.MarketQuery{
			Base:        base,
			Query:       query,
			Loader:      strings.TrimSpace(q.Get("loader")),
			MC:          strings.TrimSpace(q.Get("mc")),
			Sort:        strings.TrimSpace(q.Get("sort")),
			Source:      strings.TrimSpace(q.Get("src")),
			Offset:      atoiOr(q.Get("offset"), 0),
			Limit:       atoiOr(q.Get("limit"), 20),
			ProjectType: "modpack",
		}
		if onlyServer {
			mq.ServerSide = serverSideFilter
		}
		return fetch.MarketSearch(mq)
	}

	reply := func(hits []fetch.MarketHit, used string, zh *fetch.ZhCandidate) {
		fetch.DecorateZhNames(hits)
		if hits == nil {
			hits = []fetch.MarketHit{} // ⚠ 一律 []：前端直接读 .length
		}
		body := map[string]any{
			"items": hits,
			"base":  used,
			// used = 真正答上来的那个源站。来源变了要让用户看得见，否则他会以为换了内容。
			"mirrored":   used != fetch.NormalizeMarketBase(base),
			"onlyServer": onlyServer,
		}
		if zh != nil {
			body["zh"] = zh
		}
		writeJSON(w, 200, body)
	}

	raw := strings.TrimSpace(q.Get("q"))
	// 中文关键词先换成英文名再搜：市场索引里没有译名，不换就基本搜不到正主。
	// 与模组市场共用同一层，行为一致；查不到候选就走原词。
	if cands := fetch.ResolveZh(raw); len(cands) > 0 {
		best := cands[0]
		for i, c := range cands {
			hits, used, err := run(c.English)
			if err != nil {
				writeErr(w, 502, err.Error())
				return
			}
			if len(hits) > 0 {
				pick := c
				reply(hits, used, &pick)
				return
			}
			if i == len(cands)-1 {
				reply(hits, used, &best)
				return
			}
		}
	}

	hits, used, err := run(raw)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	reply(hits, used, nil)
}

// ── 项目详情（独立详情页用的那一屏数据）──────────────────────

func (s *Server) modpackProject(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := strings.TrimSpace(q.Get("project"))
	if project == "" {
		writeErr(w, 400, "缺少 project（Modrinth 项目 id 或 slug）")
		return
	}
	d, used, err := fetch.FetchProjectDetail(s.set.Get().MarketBase, project)
	if err != nil {
		// 与 /versions 同一个口径：源站明确说「没这个项目」≠ 我们连不上。
		if errors.Is(err, fetch.ErrMarketNotFound) {
			writeErr(w, 404, fmt.Sprintf("市场里找不到项目「%s」—— 它可能已经被作者下架或改名了。"+
				"回搜索页重新挑一个（面板读的是 Modrinth 的实时列表，下架的包查不到了）。", project))
			return
		}
		writeErr(w, 502, err.Error())
		return
	}
	// 整合包市场只该展示整合包。拿到一个模组 / 材质包也要说出来 ——
	// 详情页是单页 URL（?view=market&pk=xxx），用户可能手改地址，
	// 静默展示会让「整合包市场里怎么是个模组」变成一个没人解释为的现象。
	if d.ProjectType != "" && d.ProjectType != "modpack" {
		writeErr(w, 400, fmt.Sprintf("「%s」是一个 %s，不是整合包 —— 这个入口只列出能装成服务端的包。"+
			"要装它请去模组市场。", plainProjectTitle(d), projectTypeLabel(d.ProjectType)))
		return
	}
	writeJSON(w, 200, map[string]any{
		"item":     d,
		"base":     used,
		"mirrored": used != fetch.NormalizeMarketBase(s.set.Get().MarketBase),
	})
}

// projectTypeLabel 源站的 project_type 翻成界面语言。
// 直接把 `resourcepack` 这种键印给用户等于把内部结构漏出去。
func projectTypeLabel(t string) string {
	switch t {
	case "mod":
		return "模组"
	case "modpack":
		return "整合包"
	case "resourcepack":
		return "材质包"
	case "shader":
		return "光影包"
	case "plugin":
		return "插件"
	case "datapack":
		return "数据包"
	}
	return t
}

// plainProjectTitle 报错文案里要叫出这个项目名字，标题为空时用 id/slug 兜底。
func plainProjectTitle(d *fetch.MarketProjectDetail) string {
	if n := strings.TrimSpace(d.Title); n != "" {
		return n
	}
	if d.Slug != "" {
		return d.Slug
	}
	return d.ID
}

// ── 版本列表 ──────────────────────────────────────────────

func (s *Server) modpackVersions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := strings.TrimSpace(q.Get("project"))
	if project == "" {
		writeErr(w, 400, "缺少 project（Modrinth 项目 id 或 slug）")
		return
	}
	vs, used, err := fetch.MarketVersions(s.set.Get().MarketBase, project,
		strings.TrimSpace(q.Get("loader")), strings.TrimSpace(q.Get("mc")))
	if err != nil {
		// 源站明确说「没这个项目」≠ 我们连不上。报 502 会让用户去查网络，
		// 而真正该做的是回搜索页重挑一个（可能是作者下架了，或者链接过期了）。
		if errors.Is(err, fetch.ErrMarketNotFound) {
			writeErr(w, 404, fmt.Sprintf("市场里找不到项目「%s」—— 它可能已经被作者下架或改名了。"+
				"回搜索页重新挑一个（面板读的是 Modrinth 的实时列表，下架的包搜不到了）。", project))
			return
		}
		writeErr(w, 502, err.Error())
		return
	}
	// 只留带 .mrpack 的版本：能装的就是它，没有它的版本在界面上是个点了没用的按钮。
	// 一个都没有时回空数组 —— 界面要说「这个包没发过可安装的版本」，而不是「加载失败」。
	out := make([]fetch.MarketVersion, 0, len(vs))
	for _, v := range vs {
		if v.HasMrpack {
			out = append(out, v)
		}
	}
	writeJSON(w, 200, map[string]any{
		"items":    out,
		"base":     used,
		"mirrored": used != fetch.NormalizeMarketBase(s.set.Get().MarketBase),
	})
}

// ── packToken：把预览下下来的那份包留到安装那一刻 ──────────────

// packTokenTTL 预览结果的有效期。
//
// 取 20 分钟：fastly 那条线路实测只有 ~60KB/s，一个 28MB 的包要下 8 分钟，
// 用户看完清单再犹豫一下几分钟就过去了。太短会让「预览完去接杯水回来」变成白干，
// 而重下意味着再等 8 分钟 —— 那比过期本身更劝退。
const packTokenTTL = 20 * time.Minute

// cachedPack 一次 inspect 下到临时目录、等着被 install 取走的那份包。
type cachedPack struct {
	token    string
	tmpDir   string // 空 = 用户给的本地文件，**不许删**
	packPath string

	index     *modpack.Index
	overrides []modpack.OverrideRef

	slug          string
	projectID     string
	versionID     string
	versionNumber string
	name          string

	cachedAt time.Time
}

// release 收尾：临时目录删掉，本地文件一个字节都不动。
func (c *cachedPack) release() {
	if c.tmpDir != "" {
		_ = os.RemoveAll(c.tmpDir)
		c.tmpDir = ""
	}
}

// packCache 取件码表。带锁内存 map，进程重启即清空 —— 临时目录随之变成孤儿，
// 由操作系统的临时目录清理负责（家里那台机器重启一次就没了，不值得为它写一个扫描器）。
type packCache struct {
	mu    sync.Mutex
	packs map[string]*cachedPack
}

func newPackCache() *packCache { return &packCache{packs: map[string]*cachedPack{}} }

func (c *packCache) put(p *cachedPack) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, ex := range c.packs {
		if now.Sub(ex.cachedAt) > packTokenTTL {
			ex.release()
			delete(c.packs, k)
		}
	}
	c.packs[p.token] = p
}

// claim 取件。成功即从表里摘掉 —— **一次性消费**：
// 同一份预览能被用两次的话，第二次安装会拿着已经被 Execute 动过的临时包再来一遍，
// 而用户以为自己在做一件全新的事。过期 / 用过 / 不存在都返回 nil，调用方统一回一句「重新预览」。
func (c *packCache) claim(token string) *cachedPack {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.packs[token]
	if !ok {
		return nil
	}
	delete(c.packs, token)
	if time.Since(p.cachedAt) > packTokenTTL {
		p.release()
		return nil
	}
	return p
}

// ── 只算不装（但要下包）────────────────────────────────────

// loaderHumanName 把包 dependencies 里的键翻成人话。
// 用户在 Modrinth 页面上看到的是「Quilt」「Fabric」，不是 `quilt-loader` 这个 JSON 键；
// 拿键去写文案等于把内部结构印到界面上。
func loaderHumanName(key string) string {
	switch key {
	case "forge":
		return "Forge"
	case "neoforge":
		return "NeoForge"
	case "fabric-loader":
		return "Fabric"
	case "quilt-loader":
		return "Quilt"
	}
	return ""
}

// modpackRefusal 加载器认不出时的拒绝文案。
//
// 为什么这里必须**拒绝**而不是「用 vanilla 兜着继续装」：那个包里的模组是 Quilt 的，
// 装进原版服一个都不会被加载 —— 界面会显示「安装成功」，服务器也起得来，
// 但模组全没生效。这是一次**静默失败**，比当场拒绝坏得多。
func modpackRefusal(loaderKind string) string {
	key := strings.TrimSpace(loaderKind)

	// base = 用来读的短名（「自己装一个 X 服务端」用这个，塞个括号注释进去会读不通）；
	// label = 首次点名的完整形式：人话名 + 括号里保留原始键。
	// 键要留着 —— 它能跟包里的 dependencies 对上，排查时一搜就着。
	base := key
	if h := loaderHumanName(key); h != "" {
		base = h
	}
	label := base
	switch {
	case key == "":
		base, label = "未知", "未知"
	case base != key:
		label = base + "（" + key + "）"
	}

	return fmt.Sprintf("这个包基于 %s，面板没有对应的服务端类型（现有：原版 / Paper / Fabric / Forge / "+
		"NeoForge / Velocity / Mohist / Arclight），装上去模组一个都不会被加载。三条路："+
		"①自己装一个 %s 服务端，再用面板的「导入实例」把它纳管进来；"+
		"②把 .mrpack 下到本地，在已有服务端的整合包页用 pack 参数装；"+
		"③回市场找这个包的 Fabric / Forge 版本（很多包会同时发布）。",
		label, base)
}

// canAutoPrepareJar 这个服务端类型能不能由面板把启动文件准备好。
//
// ⚠ 只看 jarResolvers 表 —— 那张表就是下载链路真正会走的表。别另立一份「支持列表」：
// 两份名单迟早会不一致，本项目已经在这上面栽过一次（界面说可用、点下去说不能下）。
func canAutoPrepareJar(serverType string) bool {
	if serverType == "" {
		return false
	}
	_, ok := jarResolvers[fetch.Kind(serverType)]
	return ok
}

// jarNoteFor 服务端文件准备不了时，给一句**能照着做**的话。
// 认得出的类型（NeoForge）走源站自己给的现象说明；认不出的类型由 refusal 拦在前面，不会走到这。
func jarNoteFor(serverType string) string {
	if serverType == "" || canAutoPrepareJar(serverType) {
		return ""
	}
	if src, ok := fetch.SourceOf(fetch.Kind(serverType)); ok {
		// ⚠ Note 是我们自己写的**整句**，本身就以「。」收尾。这里直接再补一个句号，
		// 界面上就成了「…… 两者不能互相参照。。模组会照样装好 ……」—— 双句号。
		// 断言查不出这种东西，只有把响应打出来看才发现。拼之前先把结尾的句号吃掉。
		note := strings.TrimRight(strings.TrimSpace(src.Note), "。.")
		return fmt.Sprintf("%s 还不能自动下载 —— %s。模组会照样装好，实例类型也是对的；"+
			"你只要自己下一个 %s 服务端文件（官方安装器跑一遍即可）放进实例目录，就能启动。",
			src.Label, note, src.Label)
	}
	return fmt.Sprintf("面板还不能自动准备 %s 的服务端文件。模组会照样装好，启动文件需要你自己放进去。", serverType)
}

// suspectFile 交叉核对出来的「.mrpack 说该装、实则服务端装不了」的文件。
type suspectFile struct {
	File  string `json:"file"`
	Title string `json:"title,omitempty"`
}

// modrinthProjectID 从下载直链里抠 Modrinth 的 project id。
// 形态：https://cdn.modrinth.com/data/<projectId>/versions/<versionId>/<file>
// 非 Modrinth 直链（作者自己的 GitHub / 自建源）→ 返回空串，调用方跳过。
func modrinthProjectID(u string) string {
	const marker = "cdn.modrinth.com/data/"
	i := strings.Index(u, marker)
	if i < 0 {
		return ""
	}
	rest := u[i+len(marker):]
	j := strings.IndexByte(rest, '/')
	if j <= 0 {
		return ""
	}
	return rest[:j]
}

// suspectClientFiles 用 Modrinth 的真实 server_side 核对 .mrpack 的 env 标注。
//
// 为什么必须核对：安装流程完全信作者在 .mrpack 里写的 `env.server`
// （required 装 / unsupported 跳过），但那个标注**经常是错的、或者压根没填**。
// 真机案例：Zombie Invade 100 Days 在包层面标着服务端可用，包里的 ETF（纯客户端）
// 却没标 unsupported → 被当 required 装进服务端 → Forge 启动即 exit 1。
//
// 核对不上（离线、或文件来自非 Modrinth 源）**不算失败**：宁可少一条警告，
// 也不能让整个 inspect 因网络问题失败 —— 那是拿掉用户的主线功能去换一条提示。
func suspectClientFiles(base string, ix *modpack.Index) []suspectFile {
	type cand struct{ file, id string }
	cands := make([]cand, 0, len(ix.Files))
	ids := make([]string, 0, len(ix.Files))
	for _, f := range ix.Files {
		if f.Env.Server == modpack.SideUnsupported {
			continue // 这种安装时会跳过，不必再警告
		}
		if id := modrinthProjectID(f.DownloadURL()); id != "" {
			cands = append(cands, cand{f.Path, id})
			ids = append(ids, id)
		}
	}
	if len(cands) == 0 {
		return nil
	}
	// Modrinth 一次最多认 100 个 id，分批发问。
	const chunk = 100
	serverSide := map[string]string{}
	titles := map[string]string{}
	for i := 0; i < len(ids); i += chunk {
		end := i + chunk
		if end > len(ids) {
			end = len(ids)
		}
		projs, err := fetch.MarketProjects(base, ids[i:end])
		if err != nil {
			return nil
		}
		for _, p := range projs {
			serverSide[p.ID] = p.ServerSide
			titles[p.ID] = p.Title
		}
	}
	out := make([]suspectFile, 0, 4)
	for _, c := range cands {
		if serverSide[c.id] == string(modpack.SideUnsupported) {
			out = append(out, suspectFile{File: c.file, Title: titles[c.id]})
		}
	}
	return out
}

type inspectResp struct {
	Token         string `json:"token"`
	OK            bool   `json:"ok"`
	Refusal       string `json:"refusal"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	ProjectID     string `json:"projectId"`
	VersionID     string `json:"versionId"`
	VersionNumber string `json:"versionNumber"`
	Minecraft     string `json:"minecraft"`
	LoaderKind    string `json:"loaderKind"`
	LoaderVersion string `json:"loaderVersion"`
	ServerType    string `json:"serverType"`
	Install       int    `json:"install"`
	Skipped       int    `json:"skipped"`
	OverrideCount int    `json:"overrideCount"`
	PackBytes     int64  `json:"packBytes"`
	DownloadBytes int64  `json:"downloadBytes"`
	CanAutoJar    bool   `json:"canAutoJar"`
	JarNote       string `json:"jarNote"`
	// SuspectClient 「.mrpack 说该装、但 Modrinth 说服务端用不了」的文件。
	// 装前把名字亮出来 —— 这是「装完启动崩」最主要的来源。
	SuspectClient []suspectFile `json:"suspectClient,omitempty"`
}

func (s *Server) modpackInspect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version string `json:"version"`
		Pack    string `json:"pack"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	versionID := strings.TrimSpace(body.Version)
	packPath := strings.TrimSpace(body.Pack)
	if (versionID == "") == (packPath == "") {
		writeErr(w, 400, "两个参数必须二选一：version=<Modrinth 版本 id>（从市场装）或 pack=<本地 .mrpack 绝对路径>")
		return
	}

	cp := &cachedPack{}
	ctx := r.Context()
	if packPath != "" {
		// 本地包：直接用用户给的那份，不复制。tmpDir 留空 = release 时不许动它 ——
		// 那是用户自己的文件，删了就找不回来了。
		cp.packPath = packPath
	} else {
		ver, _, err := fetch.MarketVersionByID(s.set.Get().MarketBase, versionID)
		if err != nil {
			// 版本 id 不存在：这不是「我们的故障」，是给过来的 id 已经没了
			// （多半是刚在下拉列表里挑的、这中间作者传了新版本把它挤掉，或抄错一位）。
			// 说清下一步 —— 别让用户对着 502 去查自己的网络。
			if errors.Is(err, fetch.ErrVersionNotFound) {
				writePlanErr(w, badInput("市场里找不到版本 %s。它可能已经被新版本顶掉、或者作者删了。"+
					"回上一步重新拉一次版本列表再挑。", versionID))
				return
			}
			writePlanErr(w, &planError{kind: planErrUpstream, msg: err.Error() +
				"。可以先把 .mrpack 下载到本地后用 pack 参数指给面板，或稍后再试。"})
			return
		}
		f := pickMrpackFile(ver.Files)
		if f == nil {
			writeErr(w, 400, fmt.Sprintf("版本 %s（%s）的文件清单里没有 .mrpack 包本体 —— 它可能只发了客户端包，"+
				"或者作者还没打服务端包。换个版本试试。", ver.VersionNumber, ver.ID))
			return
		}
		tmpDir, dst, derr := s.downloadMrpack(ctx, f, packDownloadLabel(f, ver.VersionNumber), nil)
		if derr != nil {
			writePlanErr(w, derr)
			return
		}
		cp.tmpDir = tmpDir
		cp.packPath = dst
		cp.versionID = ver.ID
		cp.versionNumber = ver.VersionNumber
		cp.projectID = ver.ProjectID
		cp.name = ver.Name
	}

	ix, err := modpack.ParseIndex(cp.packPath)
	if err != nil {
		cp.release()
		writeErr(w, 400, err.Error())
		return
	}
	ov, err := modpack.ReadOverrides(cp.packPath)
	if err != nil {
		cp.release()
		writeErr(w, 400, err.Error())
		return
	}
	cp.index = ix
	cp.overrides = ov
	// 包内索引里的 name 是作者写的包名，比市场那份版本名（常带版本号尾巴）更适合当实例名；
	// 它为空时才退回市场给的名字。
	if ix.Name != "" {
		cp.name = ix.Name
	}
	cp.token = fmt.Sprintf("pk_%d", time.Now().UnixNano())
	cp.cachedAt = time.Now()

	resp := inspectResp{
		Token:         cp.token,
		Name:          cp.name,
		Slug:          cp.slug,
		ProjectID:     cp.projectID,
		VersionID:     cp.versionID,
		VersionNumber: cp.versionNumber,
		Minecraft:     ix.Minecraft(),
		OverrideCount: len(ov),
		PackBytes:     packSizeOf(cp.packPath),
	}

	verdict := ix.ServerVerdict()
	resp.Install, resp.Skipped = verdict.Install, verdict.Skipped

	kind, lv := ix.Loader()
	resp.LoaderKind, resp.LoaderVersion = kind, lv
	resp.ServerType = modpack.NormalizeLoader(kind)

	switch {
	case !verdict.OK:
		// 纯客户端包：证据由 ServerVerdict 给（几个文件、什么标记），这里不加工
		resp.OK = false
		resp.Refusal = verdict.Reason
	case resp.ServerType == "":
		resp.OK = false
		resp.Refusal = modpackRefusal(kind)
	default:
		resp.OK = true
		resp.CanAutoJar = canAutoPrepareJar(resp.ServerType)
		resp.JarNote = jarNoteFor(resp.ServerType)
		resp.DownloadBytes = downloadBytesOf(ix)
		// 装前核对：作者标错的、或压根没标 side 的客户端模组。核对不上就安静地不报。
		resp.SuspectClient = suspectClientFiles(s.set.Get().MarketBase, ix)
	}

	// 拒了就不留取件码：留着的话用户改天还能拿它装 —— 而装上去是个静默失败的东西。
	// ⚠ 临时目录也要一起收，否则那几十 MB 会一直躺在 %TEMP% 里。
	if !resp.OK {
		cp.release()
		resp.Token = ""
	} else {
		s.mpPacks.put(cp)
	}

	writeJSON(w, 200, resp)
}

// packSizeOf 真实磁盘上的包大小。不用市场报的 size —— 中间层给的数字要复核
// （镜像会悄悄截断，报 28MB 下成 3MB 的事真发生过）。
func packSizeOf(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// downloadBytesOf 这次要下多少字节。只算**服务端要装的那些**，跳过的不算 ——
// 把跳过的一起算进去，用户会看到一个比实际大的数字，然后觉得我们算错了。
func downloadBytesOf(ix *modpack.Index) int64 {
	install, _ := ix.ServerInstallable()
	var n int64
	for _, f := range install {
		n += f.FileSize
	}
	return n
}

// ── 一键装成实例 ──────────────────────────────────────────

func (s *Server) modpackInstall(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token    string `json:"token"`
		Name     string `json:"name"`
		JavaPath string `json:"javaPath"`
		Port     int    `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	cp := s.mpPacks.claim(body.Token)
	if cp == nil {
		writeErr(w, 400, "预览结果已经过期或者用过了 —— 回到「整合包市场」重新预览一次，看过清单再点安装"+
			"（预览结果二十分钟内有效，而且只能用一次）")
		return
	}

	// 判定再走一遍。反正在 inspect 已经算过，这里是**边界上再确认一次**：
	// 取件码是客户端传上来的，不能因为它「应该」是合格的就不验。
	verdict := cp.index.ServerVerdict()
	if !verdict.OK {
		cp.release()
		writeErr(w, 400, verdict.Reason)
		return
	}
	kind, loaderVersion := cp.index.Loader()
	serverType := modpack.NormalizeLoader(kind)
	if serverType == "" {
		cp.release()
		writeErr(w, 400, modpackRefusal(kind))
		return
	}

	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = strings.TrimSpace(cp.name)
	}
	if name == "" {
		name = "整合包实例"
	}
	port := body.Port
	if port <= 0 {
		port = s.nextFreePort()
	}

	inst := daemon.Instance{
		Name:          s.uniqueInstanceName(name),
		Type:          daemon.Type(serverType),
		MCVersion:     cp.index.Minecraft(),
		LoaderVersion: loaderVersion,
		Port:          port,
		JavaPath:      strings.TrimSpace(body.JavaPath),
		// Dir 留空 → Manager.Create 按 id 生成 instances/<id>，与向导同一条路。
	}
	if err := s.mgr.Create(&inst); err != nil {
		cp.release()
		writeErr(w, 500, "建实例失败："+err.Error())
		return
	}

	op := s.newModpackOp(inst.ID, "install")
	if op == nil {
		// 刚建的实例上不可能已有作业；真撞上就是并发重复提交，把实例收干净再回 409。
		_ = s.mgr.Delete(inst.ID, true)
		cp.release()
		writeErrReason(w, 409, "op_running", "这个实例上已经有一笔整合包作业在跑了。等它结束再试")
		return
	}
	s.jobStore.SetStage(op, "jar")
	go s.runModpackInstall(op, inst, cp)

	writeJSON(w, 202, map[string]any{"instanceId": inst.ID, "opId": op.ID})
}

// runModpackInstall 真装。顺序刻意是「先服务端文件、再装模组」：
//
//  1. 服务端文件先起（不等待）—— Forge 的安装器要跑在自己目录里，和 mods/ 不冲突；
//     先起它，等装模组那几十分钟跑完时，它早就好了。
//  2. 模组走 modpack.Execute —— 与升级完全同一条路（同一份 plan/executor）。
//  3. 任何一步发现装不了，**把刚建的实例删掉**：留一个空壳实例比报错更糟，
//     用户会以为「装好了但没生效」，然后在配置页里找半天。
func (s *Server) runModpackInstall(op *jobs.Job, inst daemon.Instance, cp *cachedPack) {
	defer cp.release()

	ctx := s.jobStore.Ctx(op)

	var warnings []string

	// ① 服务端文件
	taskID := ""
	if canAutoPrepareJar(string(inst.Type)) {
		task, err := modpackStartJar(s, inst, serverJarReq{
			Type:    string(inst.Type),
			Version: inst.MCVersion,
			// Forge 必须带 tag：它的版本号不是整数，只能按字符串回源站定位。
			Tag: inst.LoaderVersion,
		})
		if err != nil {
			// 起不了下载任务不是致命的（模组照样能装），但必须如实说。
			warnings = append(warnings, "服务端文件没能自动准备："+err.Error()+
				"。模组会照样装好，启动文件需要你自己放进实例目录。")
		} else {
			taskID = task.ID
		}
	} else {
		warnings = append(warnings, jarNoteFor(string(inst.Type)))
	}

	// ② 模组：复用算计划那条路 —— 与升级是同一份代码，口径不会漂。
	cplan, err := s.computeModpackPlan(ctx, inst, cp.packPath, "")
	if err != nil {
		s.abortInstall(op, inst, err.Error())
		return
	}
	// 身份从取件码补上：本地包那条路 computeModpackPlan 拿不到市场信息。
	cplan.projectID = cp.projectID
	cplan.versionNumber = cp.versionNumber
	cplan.slug = cp.slug

	s.jobStore.SetStage(op, "files")
	res, err := modpack.Execute(ctx, modpack.ExecuteOptions{
		InstanceDir:   inst.Dir,
		PackPath:      cplan.packPath,
		Plan:          cplan.plan,
		Index:         cplan.index,
		Old:           cplan.old,
		Slug:          cplan.slug,
		ProjectID:     cplan.projectID,
		VersionNumber: cplan.versionNumber,
		Fetcher:       standaloneFetcher{m: s.dl},
		OnProgress: func(p modpack.Progress) {
			s.jobStore.UpdateProgress(op, p.FilesDone, p.FilesTotal, p.BytesDone, p.BytesTotal, p.Current)
		},
	})
	switch {
	case err == nil:
		if res.State != nil {
			// 首次安装写 InstalledAt，UpdatedAt 留空 —— 界面上「安装时间」与「上次升级」是两个字段，
			// 把安装时间同时写进两个，用户会看到「上次升级：刚才」这种假记录。
			res.State.InstalledAt = time.Now().Format(time.RFC3339)
		}
		if serr := modpack.SaveState(inst.Dir, res.State); serr != nil {
			s.finishOp(op, cplan.plan, opFailed, res,
				"包已装好，但写回状态失败："+serr.Error()+"（下次预览会按磁盘现状重新比对）", warnings)
			return
		}
		warnings = append(warnings, jarTaskWarning(s, taskID)...)
		s.finishOp(op, cplan.plan, opDone, res, "", warnings)
	case errors.Is(err, context.Canceled):
		// 取消：包与实例都留着。用户可以自己接着装（面板的整合包页会按磁盘现状重新算），
		// 删实例反而把他已经下好的东西一起带走。
		s.finishOp(op, cplan.plan, opCanceled, res, "", warnings)
	default:
		s.finishOp(op, cplan.plan, opFailed, res, err.Error(), warnings)
	}
}

// abortInstall 装不下去了：把实例收干净，再落一笔失败。
//
// 只在**还没产出任何东西**的失败上用（计划算不出来）。装到一半失败的走另一条路 ——
// 那时磁盘上已经有用户下好的模组，删掉等于惩罚他。
func (s *Server) abortInstall(op *jobs.Job, inst daemon.Instance, msg string) {
	if err := s.mgr.Delete(inst.ID, true); err != nil {
		s.finishOp(op, nil, opFailed, nil, msg+"（另外，回收这个空实例也失败了："+err.Error()+"）", nil)
		return
	}
	s.finishOp(op, nil, opFailed, nil, msg+"。已经把这个刚建的空实例收掉了，不会留下占着名字的壳子", nil)
}

// jarTaskWarning 服务端文件那条下载任务后来怎么样了。
//
// 装模组期间它多半已经跑完，这里补一次查询就能把「启动文件到底好没好」如实带出来 ——
// 不查的话，用户在界面上只能看到「安装成功」，然后去启动发现没有 jar，
// 那一刻他既不知道该怪谁，也看不到任何线索。
func jarTaskWarning(s *Server, taskID string) []string {
	if taskID == "" {
		return nil
	}
	t, ok := s.dl.Get(taskID)
	if !ok {
		return nil
	}
	switch t.State {
	case fetch.StateDone:
		return nil
	case fetch.StateRunning:
		return []string{"服务端文件还在下载（可以到下载列表看进度）。它下完之前这个实例还不能启动。"}
	default:
		// 取消在下载管理器里也落在 failed 上（错误文案是「已取消」），
		// 所以这里要分开说 —— 对用户来说「我等得无聊点了取消」和「它下坏了」是两件事。
		if strings.Contains(t.Err, "已取消") {
			return []string{"服务端文件下载被取消了，启动文件还没就位。"}
		}
		return []string{"服务端文件下载失败：" + t.Err +
			"。模组已经装好了，启动文件要到「概览 → 体检」里按提示补齐。"}
	}
}

// ── 小工具 ────────────────────────────────────────────────

// uniqueInstanceName 让新实例的名字不与现有的撞。
// 不做的话，同一个包装两次会得到两个一模一样的名字，配置页里根本分不出谁是谁。
func (s *Server) uniqueInstanceName(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return "整合包实例"
	}
	used := map[string]bool{}
	for _, v := range s.mgr.List() {
		used[v.Name] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		cand := fmt.Sprintf("%s %d", base, i)
		if !used[cand] {
			return cand
		}
	}
	return fmt.Sprintf("%s %d", base, time.Now().Unix())
}

// nextFreePort 从 25565 往上找第一个没被别的实例占用的端口。
//
// 一键安装必须自己把这件事办了：全落在 25565 上的话，第二个实例一起来就撞端口，
// 而用户此刻既没选过端口、也不知道要去哪改 —— 这正是「开箱即用」最容易漏的一环。
func (s *Server) nextFreePort() int {
	taken := map[int]bool{}
	for _, v := range s.mgr.List() {
		taken[v.Port] = true
	}
	for p := 25565; p < 25610; p++ {
		if !taken[p] {
			return p
		}
	}
	return 25565
}
