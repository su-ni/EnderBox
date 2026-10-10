package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
)

// 这些路由服务于「插件 / 备份 / 设置」三页。
// 单独放一个文件是为了不让 server.go 变成几百行的杂物抽屉。

func (s *Server) pluginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/plugins", s.pluginList)
	mux.HandleFunc("POST /api/instances/{id}/plugins", s.pluginUpload)
	mux.HandleFunc("POST /api/instances/{id}/plugins/install-url", s.pluginInstallURL)
	// 核对「已装模组在服务端能不能用」：按 jar 的 sha1 去 Modrinth 反查项目。
	// 路径比 /plugins/{file} 更具体（多一段），ServeMux 会优先命中这条。
	mux.HandleFunc("POST /api/instances/{id}/plugins/side-audit", s.pluginSideAudit)
	mux.HandleFunc("GET /api/instances/{id}/plugins/{file}/icon", s.pluginIcon)
	mux.HandleFunc("PATCH /api/instances/{id}/plugins/{file}", s.pluginToggle)
	mux.HandleFunc("DELETE /api/instances/{id}/plugins/{file}", s.pluginDelete)
}

// sideAuditFile 核对结果里的一条「建议禁用」。
type sideAuditFile struct {
	File      string `json:"file"`
	ProjectID string `json:"projectId,omitempty"`
	Title     string `json:"title,omitempty"`
}

// pluginSideAudit 核对已装模组「在服务端能不能用」，给出建议禁用的清单。
//
// 为什么必须联网：本地只读得出 Fabric 的 environment（见 daemon/modMeta.Side），
// **Forge / NeoForge 的 mods.toml 压根没这个字段** —— 而踩坑的恰恰是 Forge 整合包。
// 走 Modrinth 的「按文件 sha1 反查项目」，绕开了「文件名 / modId 不等于 slug」的不确定性。
//
// 核对不上（离线、或模组没发在 Modrinth 上）**不算失败**：scanned / identified 两个计数
// 如实回传，前端据此说明「这次有多少没核出来」—— **不能反过来说「其余都安全」**。
func (s *Server) pluginSideAudit(w http.ResponseWriter, r *http.Request) {
	refs, err := s.mgr.PluginJarPaths(r.PathValue("id"))
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	resp := struct {
		Scanned    int             `json:"scanned"`
		Identified int             `json:"identified"`
		ClientOnly []sideAuditFile `json:"clientOnly"`
	}{ClientOnly: []sideAuditFile{}} // 集合字段先补空集合：Go 的 nil slice 会序列化成 null，前端 .length 直接崩
	resp.Scanned = len(refs)
	if len(refs) == 0 {
		writeJSON(w, 200, resp)
		return
	}

	// ① 本地算哈希（读不了的单个跳过，不拖垮整次核对）
	hashes := make([]string, 0, len(refs))
	byHash := make(map[string]daemon.PluginFileRef, len(refs))
	for _, ref := range refs {
		sum, herr := daemon.FileSHA1(ref.Path)
		if herr != nil {
			continue
		}
		hashes = append(hashes, sum)
		byHash[sum] = ref
	}
	base := s.set.Get().MarketBase

	// ② 哈希 → 项目 id
	hashToProj, err := fetch.FileHashesToProjects(base, hashes)
	if err != nil {
		writeErr(w, 502, "连不上模组市场，核不了这批模组："+err.Error())
		return
	}
	resp.Identified = len(hashToProj)
	if len(hashToProj) == 0 {
		writeJSON(w, 200, resp)
		return
	}

	// ③ 项目 id → 两侧归属
	ids := make([]string, 0, len(hashToProj))
	for _, pid := range hashToProj {
		ids = append(ids, pid)
	}
	projs, err := fetch.MarketProjects(base, ids)
	if err != nil {
		writeErr(w, 502, "连不上模组市场，核不了这批模组："+err.Error())
		return
	}
	meta := make(map[string]fetch.MarketProject, len(projs))
	for _, p := range projs {
		meta[p.ID] = p
	}
	for h, pid := range hashToProj {
		p, ok := meta[pid]
		if !ok || p.ServerSide != "unsupported" {
			continue
		}
		ref := byHash[h]
		resp.ClientOnly = append(resp.ClientOnly, sideAuditFile{File: ref.File, ProjectID: pid, Title: p.Title})
	}
	// 按文件名排序：两次点「核对」的顺序要一致 —— 顺序变来变去会让人以为结果变了。
	sort.Slice(resp.ClientOnly, func(i, j int) bool { return resp.ClientOnly[i].File < resp.ClientOnly[j].File })
	writeJSON(w, 200, resp)
}

// modMcmodRelations 查一个模组在 MC 百科上的关系星图（前置 / 拓展 / 联动 / 中心）。
//
// 数据来自 mcmod.cn 的**公开静态 JSON**（它自己画关系图用的那份），不是猜的网页结构：
// 先搜索定位模组 → 打开模组页取关系图按钮的 data-id → 拉 <data-id>.json。
// 合规前提（robots 允许范围、非营利站的克制要求、三级缓存）写在 fetch/mcmod.go 顶部，改之前先读那段。
//
// query:
//
//	name   模组名（中文 / 英文都行）—— 必填
//	force  1 = 跳过缓存强制重抓（给「页面一直开着 → 定时刷新」用）
//
// ⚠ 必须让失败**说话**：百科改版 / 站方没画关系图 都会落到 404 或 note，要说清楚，
// 而不是画一张空星图 —— 用户会以为「这个模组没有前置」，然后装上起不来。
func (s *Server) modMcmodRelations(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		writeErr(w, 400, "缺少 name（模组名，中文名或英文名都行）")
		return
	}
	force := r.URL.Query().Get("force") == "1"

	var (
		rel *fetch.McmodRelations
		err error
	)
	if force {
		rel, err = fetch.McmodRefresh(name)
	} else {
		rel, err = fetch.McmodRelationsByName(name)
	}
	if err != nil {
		if errors.Is(err, fetch.ErrMarketNotFound) {
			writeErr(w, 404, fmt.Sprintf(
				"MC 百科里没搜到「%s」的关系图。可能名字不对（百科常用中文名或缩写，比如「钠」对应 Sodium），"+
					"也可能站方没给这个小模组画关系图。", name))
			return
		}
		writeErr(w, 502, "查 MC 百科失败："+err.Error())
		return
	}
	if rel.Center == nil || len(rel.Nodes) == 0 {
		// 拉到了文件但没有节点：要么真没登记，要么百科改版了。两种都得让用户知道，别静默画空图。
		writeJSON(w, 200, map[string]any{
			"center":    rel.Center,
			"nodes":     []fetch.McmodNode{},
			"links":     []fetch.McmodLink{},
			"cats":      rel.Cats,
			"source":    rel.Source,
			"cached":    rel.Cached,
			"fetchedAt": rel.FetchedAt,
			"note":      "百科这个模组没有登记关系图（也可能是页面结构变了）。可以点下面的链接去百科自己看一眼。",
		})
		return
	}
	writeJSON(w, 200, rel)
}

// fileRoutes 文件管理器：浏览实例目录、读写文本、解压、上传、删除。
// 全部路径都相对实例目录，越界由 daemon 层拒绝。
func (s *Server) fileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/files", s.fileList)
	mux.HandleFunc("GET /api/instances/{id}/file", s.fileRead)
	mux.HandleFunc("GET /api/instances/{id}/file/raw", s.fileRaw)
	mux.HandleFunc("PUT /api/instances/{id}/file", s.fileWrite)
	mux.HandleFunc("DELETE /api/instances/{id}/file", s.fileDelete)
	mux.HandleFunc("POST /api/instances/{id}/files/upload", s.fileUpload)
	mux.HandleFunc("POST /api/instances/{id}/files/extract", s.fileExtract)
	mux.HandleFunc("POST /api/instances/{id}/files/batch-delete", s.fileBatchDelete)
	mux.HandleFunc("POST /api/instances/{id}/files/mkdir", s.fileMkdir)
	mux.HandleFunc("POST /api/instances/{id}/files/move", s.fileMove)
	mux.HandleFunc("POST /api/instances/{id}/files/copy", s.fileCopy)
}

// marketRoutes 在线模组市场（Modrinth）。地址来自全局设置，默认官方 API。
func (s *Server) marketRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/mods/search", s.modSearch)
	mux.HandleFunc("GET /api/mods/versions", s.modVersions)
	mux.HandleFunc("GET /api/mods/categories", s.modCategories)
	mux.HandleFunc("GET /api/mods/game-versions", s.modGameVersions)
	mux.HandleFunc("GET /api/mods/translate", s.modTranslate)
	mux.HandleFunc("GET /api/mods/projects", s.modProjects)
	mux.HandleFunc("GET /api/mods/project", s.modProject)
	// 模组关系星图的数据：查 MC 百科上这个模组的前置/拓展/联动。
	// ⚠ 这是**网页解析**（MC 百科没有官方 API），会失败、会过时 —— 见 fetch/mcmod.go 顶部。
	mux.HandleFunc("GET /api/mods/mcmod-relations", s.modMcmodRelations)
}

func (s *Server) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/backups", s.backupList)
	mux.HandleFunc("POST /api/instances/{id}/backups", s.backupCreate)
	mux.HandleFunc("POST /api/instances/{id}/backups/{file}/restore", s.backupRestore)
	mux.HandleFunc("DELETE /api/instances/{id}/backups/{file}", s.backupDelete)
	// 自动备份策略单独一条路由，不走整体 PUT 实例：
	// 备份页只关心策略，让它在整份实例对象上做「读-改-写」迟早和配置页互相覆盖。
	mux.HandleFunc("GET /api/instances/{id}/backup-policy", s.getBackupPolicy)
	mux.HandleFunc("PUT /api/instances/{id}/backup-policy", s.saveBackupPolicy)
}

func (s *Server) systemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/java", s.javaList)
	mux.HandleFunc("GET /api/java/releases", s.javaReleases)
	mux.HandleFunc("GET /api/java/releases/{major}", s.javaRelease)
	mux.HandleFunc("POST /api/java/download", s.javaDownload)
	mux.HandleFunc("GET /api/system", s.systemInfo)
}

func (s *Server) pluginList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := s.mgr.ListPlugins(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	dir, _ := s.mgr.ContentDirPath(id)
	writeJSON(w, 200, map[string]any{"dir": dir, "items": list})
}

func (s *Server) pluginUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 32MB 以内走内存，更大的落临时文件 —— 模组包动辄几十 MB
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "上传解析失败: "+err.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少文件字段 file: "+err.Error())
		return
	}
	defer f.Close()

	name, err := s.mgr.SavePlugin(id, hdr.Filename, f)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "file": name})
}

func (s *Server) pluginToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.SetPluginEnabled(r.PathValue("id"), r.PathValue("file"), body.Enabled); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) pluginDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.DeletePlugin(r.PathValue("id"), r.PathValue("file")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// pluginIcon 把 jar 里的图标吐出来。前端按需请求 + 用文件修改时间做 URL 版本。
func (s *Server) pluginIcon(w http.ResponseWriter, r *http.Request) {
	data, mime, err := s.mgr.PluginIcon(r.PathValue("id"), r.PathValue("file"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	w.Header().Set("Content-Type", mime)
	// 私有缓存一小时：图标跟着文件走，文件没换就不该反复解压 jar。
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(200)
	_, _ = w.Write(data)
}

// installURLReq 直链安装的入参。市场安装也走这条 —— 前端在市场上选好版本后，
// 手里已经有 URL / 文件名 / 校验和，再让后端去问一遍源站是多此一举。
type installURLReq struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	SHA1     string `json:"sha1"`
	Size     int64  `json:"size"`
	Label    string `json:"label"`
}

// pluginInstallURL 从直链装一个模组 / 插件进 mods / plugins 目录。
func (s *Server) pluginInstallURL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.mgr.Get(id); !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	var body installURLReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	raw := strings.TrimSpace(body.URL)
	if raw == "" {
		writeErr(w, 400, "缺少下载地址")
		return
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		writeErr(w, 400, "下载地址要写成 http:// 或 https:// 开头")
		return
	}
	name := strings.TrimSpace(body.Filename)
	if name == "" {
		name = path.Base(u.Path) // 地址最后一段通常就是文件名
	}
	dest, base, err := s.mgr.PluginDest(id, name)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	shaKind := ""
	if body.SHA1 != "" {
		shaKind = "sha1"
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = "模组 · " + base
	}
	task, err := s.dl.Start(fetch.Options{
		InstanceID: id,
		Label:      label,
		Build: fetch.Build{
			Name: base, Size: body.Size, SHA: body.SHA1, SHAKind: shaKind, URL: raw,
		},
		DestPath: dest,
		OnDone: func(_ fetch.Options, written int64) error {
			s.mgr.Log(id, fmt.Sprintf("[EnderBox] 已安装 %s（%d 字节），重启服务器后生效", base, written))
			return nil
		},
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, task)
}

// ── 文件管理 ──

// fileErrCode 把 daemon 层分好的三类错误映射成状态码。
// 「找不到」和「你填得不对」必须分开：前者界面该提示刷新，后者该提示改输入。
func fileErrCode(err error) int {
	switch {
	case errors.Is(err, daemon.ErrInstanceNotFound), errors.Is(err, daemon.ErrPathNotFound):
		return 404
	case errors.Is(err, daemon.ErrPathExists):
		return 409
	default:
		return 400
	}
}

func (s *Server) fileList(w http.ResponseWriter, r *http.Request) {
	l, err := s.mgr.ListFiles(r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, l)
}

func (s *Server) fileRead(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	text, err := s.mgr.ReadTextFile(r.PathValue("id"), rel)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"path": rel, "content": text})
}

func (s *Server) fileWrite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.WriteTextFile(r.PathValue("id"), body.Path, body.Content); err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) fileDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.DeletePath(r.PathValue("id"), r.URL.Query().Get("path")); err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// fileRaw 直接把文件字节流吐出去，供界面上的「下载」用。
//
// 为什么不能用 GET /file（读文本）代替：那个接口只接受文本文件，读 jar / 图片会回
// 「这不是文本文件」—— 于是「下载」这个按钮对二进制文件点了永远报错，
// 属于「看得见点不动」（守则：别把可见不可点当默认答案）。
func (s *Server) fileRaw(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	abs, err := s.mgr.ResolveFilePath(id, rel)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(abs)))
	http.ServeFile(w, r, abs)
}

// fileBatchDelete 一次删多个路径。
//
// 为什么单独一个接口而不是让前端循环调 DELETE：勾 20 个文件就要打 20 个来回，
// 中途网络抖一下就成了「删了一半」—— 而批量接口能**一次说清哪几条成了、哪几条没成**。
func (s *Server) fileBatchDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if len(body.Paths) == 0 {
		writeErr(w, 400, "没有要删的东西")
		return
	}
	writeJSON(w, 200, s.mgr.DeletePaths(r.PathValue("id"), body.Paths))
}

// fileMkdir 新建文件夹。
func (s *Server) fileMkdir(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	clean, err := s.mgr.MkdirAt(r.PathValue("id"), body.Path)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": clean})
}

// fileMove 移动 / 重命名（同一件事，目标位置不同而已）。
func (s *Server) fileMove(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	clean, err := s.mgr.MovePath(r.PathValue("id"), body.From, body.To)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": clean})
}

// fileCopy 复制文件或目录。
func (s *Server) fileCopy(w http.ResponseWriter, r *http.Request) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	clean, err := s.mgr.CopyPath(r.PathValue("id"), body.From, body.To)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": clean})
}

// fileUpload 把文件放进指定目录。带 extract=1 时按 zip 直接解开
// —— 「装整合包」就是这个动作，不该拆成「先传 zip、再点解压」两步。
func (s *Server) fileUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir := r.FormValue("path")
	extract := r.FormValue("extract") == "1"
	into := r.FormValue("into")
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "上传解析失败: "+err.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少文件字段 file: "+err.Error())
		return
	}
	defer f.Close()

	if extract {
		rel, err := s.mgr.ExtractUploadedZip(id, dir, hdr.Filename, f, into)
		if err != nil {
			writeErr(w, fileErrCode(err), err.Error())
			return
		}
		s.mgr.Log(id, fmt.Sprintf("[EnderBox] 已解压 %s 到 %s/", hdr.Filename, rel))
		writeJSON(w, 201, map[string]any{"ok": true, "extractedTo": rel})
		return
	}
	name, err := s.mgr.SaveUploadTo(id, dir, hdr.Filename, f)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "file": name})
}

// fileExtract 解开实例目录里已经存在的 zip。
func (s *Server) fileExtract(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Path string `json:"path"`
		Into string `json:"into"` // 空 / folder = 解到同名文件夹；here = 解到当前位置
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	rel, err := s.mgr.ExtractZipAt(id, body.Path, body.Into)
	if err != nil {
		writeErr(w, fileErrCode(err), err.Error())
		return
	}
	s.mgr.Log(id, fmt.Sprintf("[EnderBox] 已解压 %s 到 %s/", body.Path, rel))
	writeJSON(w, 200, map[string]any{"ok": true, "extractedTo": rel})
}

// ── 在线市场 ──

func (s *Server) modSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base := s.set.Get().MarketBase
	raw := q.Get("q")

	// 一次搜索。抽成闭包是为了「中文关键词换英文名后可能要试好几个候选」。
	run := func(query string) ([]fetch.MarketHit, string, error) {
		// sort 空串交给后端决定：有关键词→相关度，没关键词→下载量榜。
		// 这样前端「进来就看到东西」不需要自己知道该用哪个口径。
		return fetch.MarketSearch(fetch.MarketQuery{
			Base:     base,
			Query:    query,
			Loader:   q.Get("loader"),
			MC:       q.Get("mc"),
			Category: q.Get("cat"),
			Sort:     q.Get("sort"),
			Source:   q.Get("src"),
			Offset:   atoiOr(q.Get("offset"), 0),
			Limit:    atoiOr(q.Get("limit"), 20),
		})
	}

	reply := func(hits []fetch.MarketHit, used string, zh *fetch.ZhCandidate) {
		// 能配上中文名的就补上（译名表里精确对得上才给）——市场本来只有英文名，
		// 列表里能同时看到中文名，找东西快得多。配不上的留空，界面照旧显示英文名。
		fetch.DecorateZhNames(hits)
		body := map[string]any{
			"items": hits,
			"base":  used,
			// used = 真正答上来的那个源站。前端要显示它，否则结果来源变了用户一头雾水。
			"mirrored": used != fetch.NormalizeMarketBase(base),
		}
		if zh != nil {
			// 换了词就如实说 —— 换错的时候用户得一眼看得出来，不能悄悄换。
			// 用户原话：「我都不知道它到底装的是哪个前置」那类抱怨的根源就是这个。
			body["zh"] = zh
		}
		writeJSON(w, 200, body)
	}

	// 中文关键词先换成英文名再搜：市场索引里没有译名，不换就基本搜不到正主。
	// 候选按把握从大到小排，第一个搜不出东西就试下一个。
	if cands := fetch.ResolveZh(raw); len(cands) > 0 {
		best := cands[0] // 把握最大的那个：一条都没搜出来时，如实报的是它
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
		// 502：这是我们和源站之间的问题，不是用户请求写错了
		writeErr(w, 502, err.Error())
		return
	}
	reply(hits, used, nil)
}

func (s *Server) modVersions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	loader, mc := q.Get("loader"), q.Get("mc")
	// all=1：不按当前实例的加载器 / MC 版本筛，把这个项目的**全部**构建列出来。
	// 默认（不带 all）只列这台服务器能用的 —— 「装了必定生效」比「能看全」重要。
	// 想看全的人点开关时才会走这条路（JEI 有 3763 个构建，不该一进页面就拉）。
	if q.Get("all") == "1" {
		loader, mc = "", ""
	}
	vs, _, err := fetch.MarketVersions(s.set.Get().MarketBase, q.Get("project"), loader, mc)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": vs})
}

// modCategories 分类栏的名单。放后端是为了「能查的类」和「能点的类」永远同一份。
func (s *Server) modCategories(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"items": fetch.CuratedCategories()})
}

// modGameVersions 版本下拉框的候选。同样从后端取 —— 前端不维护第二份版本名单。
func (s *Server) modGameVersions(w http.ResponseWriter, r *http.Request) {
	vs, err := fetch.MarketGameVersions(s.set.Get().MarketBase, atoiOr(r.URL.Query().Get("limit"), 40))
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": vs})
}

// modTranslate 一屏结果的中文简介。
//
// ⚠ 它**永远返回 200**：翻译拿不到不是错误，只是这一条显示原文。
// 返回 5xx 会让前端被迫整屏报错 —— 那才是真的错：用户本来只是想看看有什么模组。
func (s *Server) modTranslate(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ids")
	ids := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			ids = append(ids, p)
		}
	}
	if len(ids) > 60 { // 别让人拿这个接口当爬虫入口
		ids = ids[:60]
	}
	items := fetch.MarketTranslate(ids)
	if items == nil {
		items = map[string]string{}
	}
	// 集合字段不许是 null：前端一个 Object.keys 就炸，而错误边界会把整页换掉。
	writeJSON(w, 200, map[string]any{"items": items})
}

// modProjects 按 ID 取项目信息 —— 把版本的「前置 ID 列表」变成人能认的名字。
func (s *Server) modProjects(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("ids")
	ids := []string{}
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			ids = append(ids, p)
		}
	}
	ps, err := fetch.MarketProjects(s.set.Get().MarketBase, ids)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": ps})
}

/*
单个模组项目的详情 —— 独立详情页要的那一屏数据。

为什么不复用批量接口 `/api/mods/projects`：那条**不带作者正文**（body 中位 4KB、
最大能到 53KB，批量拉十个就是几百 KB，只为给列表补个标题不值得）。
详情页要的恰恰就是正文，所以单独一条，和 `/api/modpacks/project` 对称。

守门条件和整合包那边**反过来**：整合包入口只放整合包进来，这里只放模组/插件进来。
单页 URL 用户能手改，静默渲染会让「模组市场里怎么出来一个整合包」变成没人解释得了的现象。
*/
func (s *Server) modProject(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	project := strings.TrimSpace(q.Get("project"))
	if project == "" {
		project = strings.TrimSpace(q.Get("id"))
	}
	if project == "" {
		writeErr(w, 400, "缺少 project（Modrinth 项目 id 或 slug）")
		return
	}
	d, used, err := fetch.FetchProjectDetail(s.set.Get().MarketBase, project)
	if err != nil {
		// 和 /mods/versions 同一个口径：源站明确说「没这个项目」≠ 我们连不上。
		if errors.Is(err, fetch.ErrMarketNotFound) {
			writeErr(w, 404, fmt.Sprintf("市场里找不到项目「%s」—— 它可能已经被作者下架或改名了。"+
				"回搜索页重新挑一个（面板读的是 Modrinth 的实时列表，下架的查不到了）。", project))
			return
		}
		writeErr(w, 502, err.Error())
		return
	}
	// project_type 为空时不下结论 —— 源站没给这一栏，不该因此把用户挡在门外。
	switch d.ProjectType {
	case "", "mod", "plugin":
	default:
		writeErr(w, 400, fmt.Sprintf("「%s」是一个 %s，不是模组或插件 —— "+
			"这个入口只列出能放进 mods/ 或 plugins/ 的东西。要装它请去整合包市场。",
			plainProjectTitle(d), projectTypeLabel(d.ProjectType)))
		return
	}
	writeJSON(w, 200, map[string]any{
		"item": d,
		"base": used,
		// 这次是从镜像读的还是从官方读的 —— 源站不通时会走镜像，
		// 不写出来用户会在「信息怎么对不上」的时候没线索。
		"mirrored": used != fetch.NormalizeMarketBase(s.set.Get().MarketBase),
	})
}

// atoiOr 解析分页参数。非法值一律回退到默认 —— 用户手改 URL 不该看到 500。
func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func (s *Server) backupList(w http.ResponseWriter, r *http.Request) {
	list, err := s.mgr.ListBackups(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}

func (s *Server) backupCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	b, err := s.mgr.CreateBackup(r.PathValue("id"), body.Label)
	if err != nil {
		// 实例没了 → 404；「没找到世界存档」是用户换个范围就能解决的 → 400；
		// 其余（磁盘满、没权限）才是我们这边的 500。
		switch {
		case errors.Is(err, daemon.ErrInstanceNotFound):
			writeErr(w, 404, err.Error())
		case errors.Is(err, daemon.ErrNoWorldArchive):
			writeErr(w, 400, err.Error())
		default:
			writeErr(w, 500, err.Error())
		}
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) backupRestore(w http.ResponseWriter, r *http.Request) {
	safety, err := s.mgr.RestoreBackup(r.PathValue("id"), r.PathValue("file"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 返回还原前那份安全备份的文件名，界面要告诉用户「后悔药在这」
	writeJSON(w, 200, map[string]any{"ok": true, "safety": safety})
}

func (s *Server) backupDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.DeleteBackup(r.PathValue("id"), r.PathValue("file")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ── 自动备份策略 ──

func (s *Server) getBackupPolicy(w http.ResponseWriter, r *http.Request) {
	v, ok := s.mgr.GetBackupPolicy(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) saveBackupPolicy(w http.ResponseWriter, r *http.Request) {
	var p daemon.BackupPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	v, err := s.mgr.SaveBackupPolicy(r.PathValue("id"), p)
	if err != nil {
		// 实例没了 → 404；其余都是用户填的值不合法 → 400（界面直接把这句话显示出来）
		if errors.Is(err, daemon.ErrInstanceNotFound) {
			writeErr(w, 404, err.Error())
			return
		}
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) javaList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"items": s.mgr.JavaRuntimes()})
}

// ── 下载 JDK ──
//
// 三步分开，是为了不在「进页面」的时候就打一堆外部请求：
//   releases            → 静态清单（有哪些主版本可选），零外部请求
//   releases/{major}    → 真正去问源站「这个主版本现在最新是哪一份、多大、有没有校验和」
//   download            → 起一个下载任务，下完解压进 runtimes/

func (s *Server) javaReleases(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"items": fetch.JDKMajors()})
}

func (s *Server) javaRelease(w http.ResponseWriter, r *http.Request) {
	major, err := strconv.Atoi(r.PathValue("major"))
	if err != nil || major <= 0 {
		writeErr(w, 400, "主版本号不对")
		return
	}
	rel, err := fetch.JDKLatest(major)
	if err != nil {
		// 502：这是我们和源站之间的问题，不是用户请求写错了
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, rel)
}

// javaDownload 一键把某个主版本的 Temurin JDK 下下来并装好。
//
// 复用服务端 jar 那套下载器（多线程 / 断点续传 / 自动重试 / 校验），
// 差别只有两点：没有归属实例（Instance 留空，走 Label 显示），以及下完要解压。
func (s *Server) javaDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Major int `json:"major"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	rel, err := fetch.JDKLatest(body.Major)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}

	runName := fmt.Sprintf("temurin-%d", rel.Major)
	// 归档先落在 runtimes/.download/ 下（点开头，daemon 扫运行时会跳过），
	// 解压完就删掉 —— 一份 200 MB 的 zip 留着没有任何用处。
	dest := filepath.Join(s.mgr.BaseDir(), "runtimes", ".download", rel.Name)

	task, err := s.dl.Start(fetch.Options{
		Label: fmt.Sprintf("Java %d · Temurin %s", rel.Major, rel.Version),
		Build: fetch.Build{
			Name:    rel.Name,
			Size:    rel.Size,
			SHA:     rel.SHA,
			SHAKind: "sha256",
			URL:     rel.URL,
		},
		DestPath: dest,
		PostInstall: func(ctx context.Context, report func(string)) error {
			if _, ierr := s.mgr.InstallJDK(dest, runName, report); ierr != nil {
				return ierr
			}
			_ = os.Remove(dest)
			_ = os.Remove(filepath.Dir(dest))
			return nil
		},
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, task)
}

var startedAt = time.Now()

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version":   version,
		"goVersion": strings.TrimPrefix(runtime.Version(), "go"),
		"goos":      runtime.GOOS,
		"goarch":    runtime.GOARCH,
		"pid":       os.Getpid(),
		"startedAt": startedAt,
		"uptime":    strconv.FormatInt(int64(time.Since(startedAt).Seconds()), 10),
		"dataDir":   s.mgr.BaseDir(),
	})
}

// 版本串。构建时可覆盖：go build -ldflags "-X github.com/su-ni/EnderBox/internal/api.version=1.2.0"
var version = "0.2.0-dev"
