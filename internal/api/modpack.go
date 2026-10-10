package api

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/jobs"
	"github.com/su-ni/EnderBox/internal/modpack"
)

// 整合包：身份查询 + 升级「只算不装」+ 真升级作业。
//
// 预览（GET）与开始（POST）**必须走同一个算计划的函数**（computeModpackPlan）——
// POST 会把 planHash 和「现在重新算出来的计划」对一遍，两条路各算各的的话，
// 临时目录名、下载时序任何一点不同都会让哈希永远对不上，409 就成了常态而不是护栏。

func (s *Server) instanceModpackRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/modpack", s.modpackInfo)
	mux.HandleFunc("GET /api/instances/{id}/modpack/upgrade", s.modpackUpgradePreview)
	mux.HandleFunc("POST /api/instances/{id}/modpack/upgrade", s.modpackUpgradeStart)
	// 制作整合包 —— 导入的反向操作，所以挂在同一前缀下。
	// preview 是 GET（只算不写），导出是 POST（会落盘）。分开两个动词不是洁癖：
	// preview 会被反复调用（用户改一次参数就重算一次），而导出只该发生一次且必须显式触发。
	mux.HandleFunc("GET /api/instances/{id}/modpack/export/preview", s.modpackExportPreview)
	mux.HandleFunc("POST /api/instances/{id}/modpack/export", s.modpackExportStart)
	// 升级/安装这类长任务的进度、取消、历史记录统一走 /api/jobs（见 server.go 的 jobRoutes），
	// 不再各搞一套 /api/modpacks/ops* —— 备份、导出、模组升级/安装共用同一张作业表与同一个前端气泡。
}

// modpackRunning 查实例是否在跑。默认指向 Server.instanceRunning；
// 做成包级变量是为了测试：「实例在跑时必须 409」这条闸，要造一个真的在跑的实例
// 就得拉起真进程再过一遍 preflight 的 java 探测，既慢又把测试绑死在机器环境上。
// 测试里换成桩，闸本身的真实实现由其它路由共用，不在这里重复验。
var modpackRunning = (*Server).instanceRunning

// modpackInfo 这个实例装的是哪个整合包、哪一版。
func (s *Server) modpackInfo(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	st, err := modpack.LoadState(inst.Dir)
	if err == modpack.ErrNoState {
		// have 恒存在：前端靠它分流（「不是整合包装的」与「读取出错」是两件事，
		// 让它靠 undefined 去猜，这两种情况在界面上就分不开了）
		writeJSON(w, 200, map[string]any{"have": false, "note": "这个实例不是由整合包安装的"})
		return
	}
	if err != nil {
		// 状态文件真的坏了 —— 这条要让人看见，不能和「不是整合包」混为一谈
		writeErr(w, 500, err.Error())
		return
	}
	loader := map[string]string{}
	for k, v := range st.Loader {
		loader[k] = v
	}
	writeJSON(w, 200, map[string]any{
		"have":          true,
		"format":        st.Format,
		"slug":          st.Slug,
		"projectId":     st.ProjectID,
		"versionId":     st.VersionID,
		"versionNumber": st.VersionNumber,
		"name":          st.Name,
		"minecraft":     st.Minecraft,
		"loader":        loader,
		"installedAt":   st.InstalledAt,
		"updatedAt":     st.UpdatedAt,
		"fileCount":     len(st.Files),
		"overrideCount": len(st.Overrides),
	})
}

// ── 算计划：预览与开始共用的那条路 ──────────────────────────

// computedPlan 算好的升级计划，以及「照着它执行」所需的全部材料。
type computedPlan struct {
	plan  *modpack.Plan
	index *modpack.Index
	// old 升级前的 packstate；nil = 全新安装（面板里大多数实例都是这种）。
	old *modpack.State
	// packPath .mrpack 的本地路径。可能是临时目录里那份（按版本号下载来的），
	// 那时 tmpDir 非空：预览用完就删；作业必须活到 Execute 结束之后才删
	// —— Execute 解 overrides/ 时还要读它。
	packPath string
	tmpDir   string
	// versionNumber 展示与哈希共用的版本号。市场版本用 version_number；
	// 本地包没有市场信息，退回索引里的 name。
	versionNumber string
	// slug / projectID 写进新 packstate 的身份。市场版本自带 project_id；
	// 本地包没有，沿用旧状态的（升级时包还是那个包）。
	slug      string
	projectID string
	// planHash 计划指纹。输入里严禁出现临时路径、绝对路径、时间戳 ——
	// 预览和 POST 各下一次包，临时目录名必然不同，混进去哈希永远对不上。
	planHash string
}

// release 释放临时目录。预览用 defer 调；作业在 Execute 结束后调。
func (c *computedPlan) release() {
	if c.tmpDir != "" {
		_ = os.RemoveAll(c.tmpDir)
		c.tmpDir = ""
	}
}

// planError 计算阶段的可预期失败，HTTP 状态码跟着分类走：
//   - badInput → 400：输入换一换就能解决（本地包打不开 / 版本不存在）
//   - upstream → 502：我们和市场 / CDN 之间的问题（重试或换本地包）
//   - state    → 500：实例的 packstate 坏了，不是用户输入的错
type planError struct {
	kind int
	msg  string
}

const (
	planErrBadInput = iota
	planErrUpstream
	planErrState
)

func (e *planError) Error() string { return e.msg }

func badInput(format string, a ...any) *planError {
	return &planError{kind: planErrBadInput, msg: fmt.Sprintf(format, a...)}
}

// writePlanErr 把计算失败映射成响应。文案本身已经给了下一步，这里不加工。
func writePlanErr(w http.ResponseWriter, err error) {
	var pe *planError
	if errors.As(err, &pe) {
		code := 500
		switch pe.kind {
		case planErrBadInput:
			code = 400
		case planErrUpstream:
			code = 502
		}
		writeErr(w, code, pe.msg)
		return
	}
	writeErr(w, 500, err.Error())
}

// computeModpackPlan 算一份升级计划。packPath 与 versionID 二选一（调用方已校验）。
//
// 这条函数是 planHash 能对上的**全部前提**：GET 预览和 POST 开始都走这里，
// 输入相同就产出完全相同的计划与哈希。
func (s *Server) computeModpackPlan(ctx context.Context, inst daemon.Instance, packPath, versionID string) (cp *computedPlan, err error) {
	old, err := modpack.LoadState(inst.Dir)
	if err != nil && err != modpack.ErrNoState {
		return nil, &planError{kind: planErrState, msg: err.Error()}
	}
	if err == modpack.ErrNoState {
		old = nil // 没装过整合包 → 这是一次全新安装，不是升级
	}

	var (
		next          *modpack.Index
		ov            []modpack.OverrideRef
		versionNumber string
		projectID     string
	)
	if packPath != "" {
		// 本地包：直接解析。文件是用户指的，打不开就原样把话说清。
		next, err = modpack.ParseIndex(packPath)
		if err != nil {
			return nil, badInput("%s", err.Error())
		}
		ov, err = modpack.ReadOverrides(packPath)
		if err != nil {
			return nil, badInput("%s", err.Error())
		}
		versionNumber = next.Name
	} else {
		ver, ferr := s.fetchModpackVersion(ctx, versionID, old)
		if ferr != nil {
			return nil, ferr
		}
		f := pickMrpackFile(ver.Files)
		if f == nil {
			return nil, badInput("版本 %s（%s）的文件清单里没有 .mrpack 包本体 —— 它可能只发了客户端包，"+
				"或者作者还没打服务端包。换个版本试试，或把 .mrpack 下载到本地后用 pack 参数指给面板。",
				ver.VersionNumber, ver.ID)
		}
		var tmpDir, packDst string
		tmpDir, packDst, ferr = s.downloadMrpack(ctx, f, packDownloadLabel(f, ver.VersionNumber), nil)
		if ferr != nil {
			return nil, ferr
		}
		packPath = packDst
		cp = &computedPlan{tmpDir: tmpDir}
		// 下载成功之后任何一步失败，临时目录都不能留在盘上；
		// 成功返回时所有权移交给调用方（预览即删，作业留到 Execute 结束）。
		defer func() {
			if err != nil {
				cp.release()
			}
		}()
		next, err = modpack.ParseIndex(packPath)
		if err != nil {
			return nil, badInput("%s", err.Error())
		}
		ov, err = modpack.ReadOverrides(packPath)
		if err != nil {
			return nil, badInput("%s", err.Error())
		}
		versionNumber = ver.VersionNumber
		projectID = ver.ProjectID
	}

	plan := modpack.BuildPlan(old, next, diskProbe(inst.Dir, old, next, ov), ov)
	if cp == nil {
		cp = &computedPlan{}
	}
	cp.plan = plan
	cp.index = next
	cp.old = old
	cp.packPath = packPath
	cp.versionNumber = versionNumber
	// 身份优先用市场给的（这次升的是哪个项目就是哪个）；本地包没这信息，沿用旧状态 ——
	// 升级时包还是那个包，packstate 的 project_id 丢了下次就没法按版本号反查了。
	if projectID != "" {
		cp.projectID = projectID
	} else if old != nil {
		cp.projectID = old.ProjectID
	}
	if old != nil {
		cp.slug = old.Slug
	}
	cp.planHash = planHashOf(next, versionNumber, plan)
	return cp, nil
}

// fetchModpackVersion 把用户输入解析成市场版本。
//
// 输入可能是 8 位版本 id（Modrinth 地址栏里那串），也可能是好记的版本号（「2.4」）。
// 前者直接查；后者只有当实例装过市场里的包（packstate 记着 ProjectID），
// 才能在该项目的版本列表里按 version_number 反查出来 —— 没装过就真的没路可走，
// 只能把三条路原原本本告诉用户。
func (s *Server) fetchModpackVersion(ctx context.Context, input string, old *modpack.State) (*fetch.MarketVersionDetail, error) {
	base := s.set.Get().MarketBase
	ver, _, err := fetch.MarketVersionByID(base, input)
	if err == nil {
		return ver, nil
	}
	if !errors.Is(err, fetch.ErrVersionNotFound) {
		// 市场连不上 / 超时：这是面板和源站之间的问题，得给两条能走通的路，
		// 不能让用户对着一句「请求失败」猜是自己的错还是网络的错。
		return nil, &planError{kind: planErrUpstream, msg: err.Error() +
			"。可以先把 .mrpack 下载到本地后用 pack 参数指给面板，或稍后再试。"}
	}
	if old == nil || strings.TrimSpace(old.ProjectID) == "" {
		return nil, badInput("%s", versionNotFoundMsg(input))
	}
	vs, _, lerr := fetch.MarketVersions(base, old.ProjectID, "", "")
	if lerr != nil {
		return nil, &planError{kind: planErrUpstream, msg: lerr.Error() +
			"。可以先把 .mrpack 下载到本地后用 pack 参数指给面板，或稍后再试。"}
	}
	for i := range vs {
		if vs[i].VersionNumber != input {
			continue
		}
		ver, _, err = fetch.MarketVersionByID(base, vs[i].ID)
		if err == nil {
			return ver, nil
		}
		if !errors.Is(err, fetch.ErrVersionNotFound) {
			return nil, &planError{kind: planErrUpstream, msg: err.Error() +
				"。可以先把 .mrpack 下载到本地后用 pack 参数指给面板，或稍后再试。"}
		}
	}
	return nil, badInput("%s", versionNotFoundMsg(input))
}

// versionNotFoundMsg 版本实在找不到时的说明。三条路都要摆出来，
// 不然用户不知道「版本号」「版本 id」「本地包」是三种不同的输入。
func versionNotFoundMsg(input string) string {
	return fmt.Sprintf("市场里找不到版本「%s」。三条路任选其一："+
		"1) 去 Modrinth 这个包的 Versions 页，复制 8 位版本 id（地址栏 /version/ 后面那一段）填进来；"+
		"2) 如果你填的是版本号（比如 2.4），只有这个实例装过市场里的整合包时才能反查出来；"+
		"3) 把 .mrpack 下载到本地，用 pack 参数指给面板。", input)
}

// packDownloadLabel 这条包下载在界面上叫什么。
//
// 不直接用原始文件名：.mrpack 的名字里常常重复一遍版本号、还带空格，
// 挂在下载气泡那个窄条里会挤成一团。用「整合包 <名字>（版本）」—— 扫一眼就知道在等哪个。
// 名字取不到就退回文件名：**宁可丑，也不能让这条下载变成无名氏**
// —— 界面上一条没有名字的进度条，用户只会问「这是在下什么」。
func packDownloadLabel(f *fetch.MarketFile, version string) string {
	var title string
	if f != nil {
		title = strings.TrimSuffix(filepath.Base(f.FileName), filepath.Ext(f.FileName))
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return "整合包"
	}
	// 文件名里往往已经带着版本号（…100 Days 2.3.mrpack），别再叠一遍
	if v := strings.TrimSpace(version); v != "" && !strings.Contains(title, v) {
		return fmt.Sprintf("整合包 %s（%s）", title, v)
	}
	return "整合包 " + title
}

// downloadMrpack 把市场版本指向的 .mrpack 下到临时目录。
//
// 地址就是版本 JSON 里给的 CDN 地址，不发明任何镜像规则。
// 下载失败必须给两条可操作的路 —— 用户此刻既不知道是网络问题还是源站问题，
// 也不知道还有「本地包」这条路可走。
// 返回 (临时目录, 包路径, 错误)；失败时临时目录已在函数内清掉。
//
// label 是这条下载在界面上显示的名字。**必须给一个名副其实的**：
// 这是一次「用户点了按钮、要等几十 MB」的下载，不给名字就只能显示成一个文件名
// （或者干脆看不见）—— 界面上要能看出「在等的是哪个包」。
func (s *Server) downloadMrpack(
	ctx context.Context,
	f *fetch.MarketFile,
	label string,
	onDelta func(int64),
) (string, string, error) {
	tmp, err := os.MkdirTemp("", "enderbox-mrpack-")
	if err != nil {
		return "", "", &planError{kind: planErrUpstream, msg: "建临时目录失败：" + err.Error()}
	}
	name := filepath.Base(f.FileName)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "pack.mrpack"
	}
	dst := filepath.Join(tmp, name)
	// Label 非空 ⇒ 这条下载会作为**可见任务**出现（气泡与下载记录里都有百分比）。
	// 以前传的是隐藏壳，于是「预览」那一步在界面上完全看不到进度 ——
	// 一个几十 MB、慢的时候好几分钟的等待，用户只能对着一个不动的按钮猜。
	if err := s.dl.FetchStandalone(ctx, fetch.StandaloneOpts{
		URL: f.URL, Dest: dst, WantSHA: f.SHA1, SHAKind: "sha1", Size: f.Size,
		Label: label, OnDelta: onDelta,
	}); err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", &planError{kind: planErrUpstream, msg: fmt.Sprintf(
			"整合包下载失败：%v。两条路：把 .mrpack 从 Modrinth 下载到本地后用 pack 参数指给面板；或稍后网络好了再试一次。", err)}
	}
	return tmp, dst, nil
}

// pickMrpackFile 从版本文件清单里挑 .mrpack 本体。
//
// 一个版本常同时挂几个文件（服务端包 / 客户端包 / 更新日志），primary 不一定落在包本体上，
// 所以先挑「标了 primary 且以 .mrpack 结尾」的；没有标 primary 的，
// 只在恰好只有一个 .mrpack 时才认 —— 多于一个时不猜，宁可让用户走 ?pack=。
func pickMrpackFile(files []fetch.MarketFile) *fetch.MarketFile {
	var mps []fetch.MarketFile
	for _, f := range files {
		if f.URL != "" && strings.EqualFold(filepath.Ext(f.FileName), ".mrpack") {
			mps = append(mps, f)
		}
	}
	for i := range mps {
		if mps[i].Primary {
			return &mps[i]
		}
	}
	if len(mps) == 1 {
		return &mps[0]
	}
	return nil
}

// planHashOf 给计划算指纹（sha256 十六进制，64 位）。
//
// ⚠ 输入里严禁出现临时路径、绝对路径、时间戳：预览和 POST 重算各下一次包，
// 两次的临时目录名必然不同，混进去哈希永远对不上，409 护栏就永远误伤。
// 输入 = 目标版本身份（loader 按 key 排序）+ 摘要五个数 + downloadBytes
//   - 四个清单每条 "action|path|reason"（组内排序后拼接）。
//
// 同一进程内两次同输入必须逐字节一致；任何用户看得见的变化（多删一个文件、
// 换个目标版本）都必须改变哈希 —— 否则「批准 A 执行 B」的竞态就挡不住了。
func planHashOf(next *modpack.Index, versionNumber string, plan *modpack.Plan) string {
	h := sha256.New()
	line := func(k, v string) { fmt.Fprintf(h, "%s=%s\n", k, v) }

	line("versionId", next.VersionID)
	line("versionNumber", versionNumber)
	line("minecraft", next.Minecraft())
	loader := map[string]string{}
	if k, v := next.Loader(); k != "" {
		loader[k] = v
	}
	keys := make([]string, 0, len(loader))
	for k := range loader {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		line("loader."+k, loader[k])
	}

	sum := plan.Summary()
	line("summary.add", strconv.Itoa(sum.Add))
	line("summary.replace", strconv.Itoa(sum.Replace))
	line("summary.remove", strconv.Itoa(sum.Remove))
	line("summary.keep", strconv.Itoa(sum.Keep))
	line("summary.skip", strconv.Itoa(sum.Skip))
	line("downloadBytes", strconv.FormatInt(plan.DownloadBytes, 10))

	entries := func(name string, list []modpack.Change) {
		lines := make([]string, 0, len(list))
		for _, c := range list {
			lines = append(lines, string(c.Action)+"|"+c.Path+"|"+c.Reason)
		}
		sort.Strings(lines)
		for _, l := range lines {
			line(name, l)
		}
	}
	entries("add", plan.ToAdd)
	entries("replace", plan.ToReplace)
	entries("remove", plan.ToRemove)
	entries("skip", plan.Skipped)

	return hex.EncodeToString(h.Sum(nil))
}

// ── 只算不装（GET 预览）────────────────────────────────────

// modpackUpgradePreview **只算不装**：把「升上去会怎样」摊开。
//
// 为什么单独一条、而不是让前端自己拼：升一次可能会**删掉**实例里的文件，
// 而「要删哪些」「你改过哪些所以我们没动」必须在用户按下按钮**之前**看到。
func (s *Server) modpackUpgradePreview(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	packPath := strings.TrimSpace(r.URL.Query().Get("pack"))
	versionID := strings.TrimSpace(r.URL.Query().Get("version"))
	if (packPath == "") == (versionID == "") {
		writeErr(w, 400, "两个参数必须二选一：?version=<Modrinth 版本 id>（市场版本）或 ?pack=<本地 .mrpack 绝对路径>")
		return
	}
	cp, err := s.computeModpackPlan(r.Context(), inst, packPath, versionID)
	if err != nil {
		writePlanErr(w, err)
		return
	}
	defer cp.release()
	writeJSON(w, 200, s.planView(inst, cp))
}

// planView 预览响应的统一形态。GET 直接给；POST 校验通过后不带它（只回 opId），
// 但两处对「这份计划」的理解必须完全一致，planHash 才对得上。
func (s *Server) planView(inst daemon.Instance, cp *computedPlan) map[string]any {
	plan := cp.plan
	sum := plan.Summary()
	loader := map[string]string{}
	if k, v := cp.index.Loader(); k != "" {
		loader[k] = v
	}
	return map[string]any{
		"ok":            plan.Refusal == "",
		"refusal":       plan.Refusal,
		"planHash":      cp.planHash,
		"summary":       map[string]int{"add": sum.Add, "replace": sum.Replace, "remove": sum.Remove, "keep": sum.Keep, "skip": sum.Skip},
		"downloadBytes": plan.DownloadBytes,
		"levelJump":     plan.LevelJump,
		"levelJumpNote": plan.LevelJumpNote,
		"running":       modpackRunning(s, inst.ID),
		"toAdd":         changes(plan.ToAdd),
		"toReplace":     changes(plan.ToReplace),
		"toRemove":      changes(plan.ToRemove),
		"skipped":       changes(plan.Skipped),
		"target": map[string]any{
			"versionId":     cp.index.VersionID,
			"versionNumber": cp.versionNumber,
			"minecraft":     cp.index.Minecraft(),
			"loader":        loader,
		},
	}
}

// changeView 一条动作的对外形态。只暴露前端需要的字段 ——
// 内部的 FileRef 带着哈希，前端用不上，也没必要发出去。
type changeView struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Reason string `json:"reason,omitempty"`
}

func changes(list []modpack.Change) []changeView {
	out := make([]changeView, 0, len(list)) // ⚠ 空切片不是 nil：nil 会序列化成 null，前端 .length 就炸
	for _, c := range list {
		out = append(out, changeView{Path: c.Path, Action: string(c.Action), Reason: c.Reason})
	}
	return out
}

// ── 真升级（POST 开始）────────────────────────────────────

// modpackUpgradeStart 真升级：重算计划、对指纹、起后台作业。
//
// 校验顺序是刻意的（每一步都对应一种「用户能自己解决」的情形）：
// 404 实例 → 400 参数二选一 → 400 没带 planHash（先预览）→ 409 实例在跑 →
// 重算计划 → 400 拒装 → 409 指纹对不上（计划变了）→ 409 已有作业在跑 → 202。
//
// 三种 409 是三个不同的「下一步」，reason 码必须分开给（前端按码分流，不猜文案）：
//   - instance_running：先把服务器停了再来；
//   - stale_plan：重新预览、看过新清单、再点一次确认；
//   - op_running：等正在跑的那笔作业结束（或取消）——这条绝不能被当成「计划变了」，
//     否则前端重预览会把跑着的作业从界面上收掉，用户就看不到进度了。
func (s *Server) modpackUpgradeStart(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	var body struct {
		Version      string `json:"version"`
		Pack         string `json:"pack"`
		PlanHash     string `json:"planHash"`
		ConfirmStale bool   `json:"confirmStale"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	packPath := strings.TrimSpace(body.Pack)
	versionID := strings.TrimSpace(body.Version)
	if (packPath == "") == (versionID == "") {
		writeErr(w, 400, "version 与 pack 必须二选一：市场版本传 version（Modrinth 版本 id），本地包传 pack（.mrpack 绝对路径）")
		return
	}
	if body.PlanHash == "" {
		writeErr(w, 400, "缺少 planHash —— 先点「预览」把计划算出来看过，再开始升级；没有预览就没有指纹可比")
		return
	}
	if modpackRunning(s, inst.ID) {
		writeErrReason(w, 409, "instance_running", "实例正在运行，升级前要先停服 —— Windows 上运行中的文件是锁着的，覆盖不进去。请先到概览页把服务器停掉再试")
		return
	}
	// 重算：预览与开始走同一个函数。没有这份重算，planHash 就无从校验，
	// 「用户批准 A、系统执行 B」的竞态就挡不住了。
	cp, err := s.computeModpackPlan(r.Context(), inst, packPath, versionID)
	if err != nil {
		writePlanErr(w, err)
		return
	}
	if cp.plan.Refusal != "" {
		cp.release()
		writeErr(w, 400, cp.plan.Refusal)
		return
	}
	if cp.planHash != body.PlanHash && !body.ConfirmStale {
		cp.release()
		writeErrReason(w, 409, "stale_plan", "计划变了，请重新预览 —— 你预览之后，包或实例里的文件有过变动，现在要执行的和你当初看到的不是同一份。"+
			"重新预览、看过新清单之后，再点一次确认才会按新计划继续")
		return
	}
	// confirmStale=true：用户已看过 409 之后重新给出的最新计划并再次确认 ——
	// 按契约本意，**执行的就是这份重算出来的新计划**，不是当初预览的旧计划。
	// 旧计划从头到尾没有被保存，也不应该被保存：它已经过时了。
	op := s.startModpackOp(inst, cp)
	if op == nil {
		// 同一实例只准一笔作业在跑。这道闸在 startModpackOp 里与入表**同一把锁**内
		// 完成 check-then-insert —— 原来是查一把锁、再开另一把插，两段之间没有原子性，
		// 并发的两个 POST 理论上能同时过闸、起两个执行器写同一批文件。
		cp.release()
		writeErrReason(w, 409, "op_running", "这个实例已经有一个整合包升级在进行中 —— 两个执行器对着同一批文件各写各的，结果必然损坏。等它结束（或取消）再试")
		return
	}
	writeJSON(w, 202, map[string]string{"opId": op.ID})
}

// ── 作业表 ────────────────────────────────────────────────

// 作业的四种状态。终态（done/failed/canceled）之后不再变。
// 值与 jobs 包里的 State* 常量一致，测试与前端用这两个名字即可。
const (
	opRunning  = jobs.StateRunning
	opDone     = jobs.StateDone
	opFailed   = jobs.StateFailed
	opCanceled = jobs.StateCanceled
)

// modpackOpResult 升级终态的结算。⚠ 部分失败不算成功：failed 非空时 state 仍是
// done，但 failed 必须如实带出，界面要把它列出来 —— 不能报「全部成功」。
// 两个集合字段一律 []，不是 null（前端直读 .length）。
type modpackOpResult struct {
	Added        int      `json:"added"`
	Replaced     int      `json:"replaced"`
	Removed      int      `json:"removed"`
	Skipped      int      `json:"skipped"`
	SkippedPaths []string `json:"skippedPaths"`
	Failed       []string `json:"failed"`
	// Warnings 「我们没接、你自己来」类的话（服务端文件没能自动准备等），一律 []。
	// ⚠ 与 Failed 不是一回事：Failed 是「该做的没做成」（红），Warnings 是「这条我们没接」（黄）。
	// 混在一起会让「装好了但少个启动文件」被报成「安装失败」，用户就去重装了 —— 而重装也没用。
	Warnings []string `json:"warnings"`
}

// startModpackOp 起一笔升级作业并把所持资源交给 goroutine。
// cp 的所有权（含临时包目录）一并移交：作业要活到 Execute 结束之后才能删它。
// 返回 nil 表示该实例已有一笔 running 作业（同 owner 闸在 jobs.Store.New 里与入表同锁），
// 此时所有权不移交，调用方负责 release 并回 409。
//
// 作业挂统一的 s.jobStore：整合包升级/安装与备份、导出共用同一张表和同一套 /api/jobs
// 端点。早年每个慢操作各开一张作业表（modpack 一套、备份一套、下载一套），
// 前端就得跟着写三份轮询 + 取消 + 进度，改一处漏两处。
func (s *Server) startModpackOp(inst daemon.Instance, cp *computedPlan) *jobs.Job {
	job := s.jobStore.New(inst.ID, "upgrade")
	if job == nil {
		return nil
	}
	// 预置空集合结算：取消可能早于 Finish 落地，那一瞬的快照也得带着全字段。
	s.jobStore.PrimeResult(job, modpackOpResult{SkippedPaths: []string{}, Failed: []string{}, Warnings: []string{}})
	go s.runModpackOp(job, inst, cp)
	return job
}

// newModpackOp 建一笔整合包作业并入表。返回 nil = 该实例已有一笔 running 作业。
// kind 只影响界面文案与「去看这个实例」入口，不影响进度字段 —— 别拿它分流数据。
//
// 「建」与「跑」分开，是因为安装与升级跑的**不是同一个函数**，
// 但「同实例只准一笔」这条闸必须共用一份实现：各写一遍的话总有一天两边不一致，
// 症状是两个执行器同时写同一批文件。
func (s *Server) newModpackOp(instID, kind string) *jobs.Job {
	job := s.jobStore.New(instID, kind)
	if job == nil {
		return nil
	}
	s.jobStore.PrimeResult(job, modpackOpResult{SkippedPaths: []string{}, Failed: []string{}, Warnings: []string{}})
	return job
}

// standaloneFetcher 把 modpack.Fetcher 适配到下载管理器的 FetchStandalone：
// 分片 / 续传 / 重试 / 哈希校验 / 原子落盘全在下载链路里，这里一行都不用再写。
// onDelta 传 nil：执行器按「文件数」报进度，字节进度走计划的 BytesTotal，
// 再单独接一路字节增量只会让两套数字对不上。
type standaloneFetcher struct{ m *fetch.Manager }

func (f standaloneFetcher) Fetch(ctx context.Context, url, dst, wantSHA, shaKind string, size int64) error {
	// Label 留空 = 隐藏壳。这批是**包里的一个个模组文件**，一个包能有上百个；
	// 每个都挂一条可见任务会把用户真正在等的那条（包本体）淹掉。
	// 这批的进度走 op.progress（按文件数 + 字节数），不进下载列表。
	return f.m.FetchStandalone(ctx, fetch.StandaloneOpts{
		URL: url, Dest: dst, WantSHA: wantSHA, SHAKind: shaKind, Size: size,
	})
}

// runModpackOp 真正执行升级。三件事必须做对：
//
//  1. 临时包文件活到 Execute 结束之后 —— Execute 解 overrides/ 时还要读它，
//     所以 release 放在整个 goroutine 收尾的 defer 里，不能在起作业时就删。
//  2. 取消不写 packstate：Execute 被打断时磁盘是「做到一半」的现实，
//     配不上任何一份完整状态；宁可让下次预览重新算（多算的部分只会进跳过清单，
//     不会覆盖用户的改动）。
//  3. 部分失败不算成功：state 照样是 done，但 failed 原样带出。
func (s *Server) runModpackOp(op *jobs.Job, inst daemon.Instance, cp *computedPlan) {
	defer cp.release()

	ctx := s.jobStore.Ctx(op)
	s.jobStore.SetStage(op, "files")

	res, err := modpack.Execute(ctx, modpack.ExecuteOptions{
		InstanceDir:   inst.Dir,
		PackPath:      cp.packPath,
		Plan:          cp.plan,
		Index:         cp.index,
		Old:           cp.old, // computeModpackPlan 里 LoadState 的结果（ErrNoState → nil）
		Slug:          cp.slug,
		ProjectID:     cp.projectID,
		VersionNumber: cp.versionNumber,
		Fetcher:       standaloneFetcher{m: s.dl},
		OnProgress: func(p modpack.Progress) {
			s.jobStore.UpdateProgress(op, p.FilesDone, p.FilesTotal, p.BytesDone, p.BytesTotal, p.Current)
		},
	})

	switch {
	case err == nil:
		// 成功：写回 packstate。UpdatedAt 写当前时刻 —— 界面靠它显示「上次升级」，
		// 之前装的那份时间戳是「安装时间」，两个字段不能混。
		if res.State != nil {
			res.State.UpdatedAt = time.Now().Format(time.RFC3339)
		}
		if serr := modpack.SaveState(inst.Dir, res.State); serr != nil {
			s.finishOp(op, cp.plan, opFailed, res,
				"升级完成，但写回状态失败："+serr.Error()+"（文件已装好；下次预览会按磁盘现状重新比对）", nil)
			return
		}
		s.finishOp(op, cp.plan, opDone, res, "", nil)
	case errors.Is(err, context.Canceled):
		// 取消：带着已做到的结果撤退，如实记账，不写状态。
		// ⚠ 只认「错误本身就是取消」，不能再叠加 ctx.Err() != nil 这种宽条件 ——
		// Execute 带着真实错误返回之后、读到这里之前用户点一下取消，ctx.Err() 就非 nil 了，
		// 真实失败会被错记成 canceled、原因被吞。执行器对取消的返回就是 ctx.Err()
		// （executor.go 只在 ctx 结束时才返回它），所以错误里包着 context.Canceled 就足以归因。
		s.finishOp(op, cp.plan, opCanceled, res, "", nil)
	default:
		s.finishOp(op, cp.plan, opFailed, res, err.Error(), nil)
	}
}

// finishOp 给作业落一笔终态。res 可能为 nil（还没产出任何东西就被取消）。
// warnings 是「我们没接、你自己来」类的话（升级没有这类，一律传 nil）。
func (s *Server) finishOp(op *jobs.Job, plan *modpack.Plan, state string, res *modpack.Result, errMsg string, warnings []string) {
	r := opResultOf(res, plan)
	r.Warnings = append(r.Warnings, warnings...)
	s.jobStore.Finish(op, state, r, errMsg)
}

// opResultOf 把执行结果折成对外的结算。
// skippedPaths 取 Result.Skipped；执行器还没走到填充那一步（中途取消）时，
// 退回计划里的跳过清单 —— 那些文件本来就一个字节都不会动，说「没动它们」永远是真话。
func opResultOf(res *modpack.Result, plan *modpack.Plan) modpackOpResult {
	r := modpackOpResult{SkippedPaths: []string{}, Failed: []string{}, Warnings: []string{}}
	if res == nil {
		return r
	}
	r.Added, r.Replaced, r.Removed, r.Skipped = res.Added, res.Replaced, res.Removed, res.SkippedCount
	r.SkippedPaths = append(r.SkippedPaths, res.Skipped...)
	r.Failed = append(r.Failed, res.Failed...)
	if len(r.SkippedPaths) == 0 && plan != nil {
		for _, c := range plan.Skipped {
			r.SkippedPaths = append(r.SkippedPaths, c.Path)
		}
	}
	return r
}

// ── 磁盘探针（比对「磁盘上现在是什么」用）──────────────────

// diskProbe 给比对用的「磁盘上现在是什么」查询。
//
// 两个要点：
//  1. **按需要的那种哈希去算**。packstate 里记的可能只有 sha1（包只提供 sha1 时），
//     而新索引可能只提供 sha512 —— 算错种类会让「一致」被判成「不一致」，
//     结果是升级该覆盖的没覆盖。这里从新旧两边收集每个路径该用的种类。
//     ⚠ 覆盖文件也要收：执行器给覆盖记账用的是落盘时自算的 sha1（包里对覆盖不给哈希），
//     键是剥掉前缀后的磁盘相对路径（BuildPlan 问磁盘问的就是这个形态）。漏收的话
//     覆盖路径查不到种类、按默认 sha512 去算，与 sha1 记录永远对不上 —— 上一版就有的、
//     一字未动的覆盖全被误判成「用户改过」跳过，升级该覆盖的覆盖不了（真机验收踩过）。
//  2. **记忆化**：比对过程里同一个路径可能被问两次（新增/变更一次、删除一次），
//     129 个模组各读一遍已经是几百 MB，重复读没必要。
//
// ⚠ 取不到（不存在 / 读不动）一律返回 false。返回 false 的后果是「按没被改过处理」，
// 也就是**更倾向于执行计划**；而只有当我们能确证「磁盘这份和记录不一样」时才跳过。
// 这个方向是刻意选的：读不动不等于用户改过，不能因此把该做的升级停掉。
func diskProbe(instanceDir string, old *modpack.State, next *modpack.Index, nextOv []modpack.OverrideRef) func(string) (string, bool) {
	kindByPath := map[string]string{}
	for _, f := range next.Files {
		if _, k := f.SHA(); k != "" {
			kindByPath[f.Path] = k
		}
	}
	if old != nil {
		for _, fr := range old.Files {
			if fr.SHAKind != "" && kindByPath[fr.Path] == "" {
				kindByPath[fr.Path] = fr.SHAKind
			}
		}
		for _, or := range old.Overrides {
			if or.SHAKind != "" && kindByPath[modpack.DiskRelOfOverride(or.Path)] == "" {
				kindByPath[modpack.DiskRelOfOverride(or.Path)] = or.SHAKind
			}
		}
	}
	for _, or := range nextOv {
		if or.SHAKind != "" && kindByPath[modpack.DiskRelOfOverride(or.Path)] == "" {
			kindByPath[modpack.DiskRelOfOverride(or.Path)] = or.SHAKind
		}
	}
	cache := map[string]string{}
	return func(rel string) (string, bool) {
		if v, ok := cache[rel]; ok {
			return v, v != ""
		}
		abs := filepath.Join(instanceDir, filepath.FromSlash(rel))
		// 再挡一次路径穿越：比对只是读，但「读实例目录外的文件」同样不该发生
		if !insideDir(instanceDir, abs) {
			cache[rel] = ""
			return "", false
		}
		st, err := os.Stat(abs)
		if err != nil || st.IsDir() {
			cache[rel] = ""
			return "", false
		}
		kind := kindByPath[rel]
		if kind == "" {
			kind = "sha512"
		}
		sum, err := hashFile(abs, kind)
		if err != nil {
			cache[rel] = ""
			return "", false
		}
		cache[rel] = sum
		return sum, true
	}
}

func hashFile(path, kind string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	var h io.Writer
	var sum func() string
	switch kind {
	case "sha1":
		s := sha1.New()
		h, sum = s, func() string { return hex.EncodeToString(s.Sum(nil)) }
	case "sha256":
		s := sha256.New()
		h, sum = s, func() string { return hex.EncodeToString(s.Sum(nil)) }
	default:
		s := sha512.New()
		h, sum = s, func() string { return hex.EncodeToString(s.Sum(nil)) }
	}
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return sum(), nil
}

// insideDir 目标是否落在 base 目录内（含 base 自身）。
// 用 Rel 判，别用字符串前缀 —— `F:/a` 与 `F:/ab` 用前缀比会误判。
func insideDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
