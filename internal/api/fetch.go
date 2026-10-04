package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/su-ni/ServerBox/internal/daemon"
	"github.com/su-ni/ServerBox/internal/fetch"
)

// 服务端 jar 的下载接口。
//
// 分两层：上面是「有哪些可下」（sources / versions / builds 只读查询），
// 下面是「下一个」（起任务 + 轮询进度）。查询都走内存缓存，构建列表会被反复点。

const buildLimit = 20

func (s *Server) fetchRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fetch/sources", s.fetchSources)
	mux.HandleFunc("GET /api/fetch/versions", s.fetchVersions)
	mux.HandleFunc("GET /api/fetch/builds", s.fetchBuilds)
	mux.HandleFunc("GET /api/fetch/tasks", s.fetchTasks)
	mux.HandleFunc("GET /api/fetch/tasks/{taskId}", s.fetchTask)
	mux.HandleFunc("POST /api/fetch/tasks/{taskId}/cancel", s.fetchCancel)
	mux.HandleFunc("POST /api/instances/{id}/server-jar", s.downloadServerJar)
}

// fetchSources 支持哪些服务端类型 + 每种类型能用哪些下载源。
// mirrors 跟着 type 走：给 Paper 挂 BMCLAPI 是没意义的（那个路径实测 404），
// 所以界面上不该出现这个选项，而不是选了之后才发现下不动。
func (s *Server) fetchSources(w http.ResponseWriter, _ *http.Request) {
	type srcView struct {
		fetch.Source
		Mirrors []fetch.Mirror `json:"mirrors"`
	}
	srcs := fetch.Sources()
	out := make([]srcView, 0, len(srcs))
	for _, src := range srcs {
		out = append(out, srcView{Source: src, Mirrors: fetch.MirrorsFor(src.Type)})
	}
	writeJSON(w, 200, map[string]any{"items": out})
}

func kindOf(r *http.Request) (fetch.Kind, error) {
	k := fetch.Kind(r.URL.Query().Get("type"))
	if k == "" {
		return fetch.KindPaper, nil
	}
	switch k {
	case fetch.KindPaper, fetch.KindVanilla:
		return k, nil
	}
	return "", fmt.Errorf("不认识的服务端类型：%s", k)
}

// mirrorOf 取本次请求用哪个下载源；没指定就用全局设置里的默认值。
// 这样「设置页选了默认源」是真的生效，而不是只存了个字段没人读。
func (s *Server) mirrorOf(r *http.Request) string {
	if m := r.URL.Query().Get("mirror"); m != "" {
		return m
	}
	return s.set.Get().DefaultMirror
}

func (s *Server) fetchVersions(w http.ResponseWriter, r *http.Request) {
	k, err := kindOf(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	var vs []fetch.Version
	switch k {
	case fetch.KindPaper:
		vs, err = fetch.PaperVersions()
	case fetch.KindVanilla:
		vs, err = fetch.VanillaVersions(s.mirrorOf(r))
	}
	if err != nil {
		// 上游挂了 / 结构变了都要说清楚是哪个源的问题，别只回「失败」
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": vs, "limited": false})
}

// fetchBuilds 某个版本的构建列表。
// 原版没有构建号，也返回一个「唯一构建」占位 —— 这样前端的「版本→构建」两步
// 对所有类型都是同一个形状，不用为原版写特例。
func (s *Server) fetchBuilds(w http.ResponseWriter, r *http.Request) {
	k, err := kindOf(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ver := strings.TrimSpace(r.URL.Query().Get("version"))
	if ver == "" {
		writeErr(w, 400, "缺少 version")
		return
	}
	limit := buildLimit
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}

	var bs []fetch.Build
	switch k {
	case fetch.KindPaper:
		bs, err = fetch.PaperBuilds(ver, limit)
	case fetch.KindVanilla:
		bs, err = fetch.VanillaBuilds(s.mirrorOf(r), ver)
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}

	// 补上「这个版本要哪个 Java」。Paper 的 API 不给，去查同版本的原版元数据；
	// 原版自己就有。拿不到就让它是 0，前端按版本号兜底算，别编一个数出来。
	if len(bs) > 0 && bs[0].JavaMajor == 0 {
		jm := fetch.RequiredJava(s.mirrorOf(r), ver)
		for i := range bs {
			bs[i].JavaMajor = jm
		}
	}
	writeJSON(w, 200, map[string]any{"items": bs, "limited": len(bs) >= limit})
}

func (s *Server) fetchTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"items": s.dl.List(r.URL.Query().Get("instance"))})
}

func (s *Server) fetchTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.dl.Get(r.PathValue("taskId"))
	if !ok {
		writeErr(w, 404, "任务不存在（可能已完成超过半小时，被清理了）")
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) fetchCancel(w http.ResponseWriter, r *http.Request) {
	if !s.dl.Cancel(r.PathValue("taskId")) {
		writeErr(w, 404, "任务不存在，或已经结束了")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// downloadServerJar 给某个实例下服务端 jar。
//
// 落盘位置用的是实例自己的 JarPath（不是固定叫 server.jar）——
// 必须和 Start() 找的是同一个路径，否则就是「下完了但起不来」。
func (s *Server) downloadServerJar(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	// Instance 本身不带运行状态（那个在 View 里），所以去列表里找一下
	for _, v := range s.mgr.List() {
		if v.ID != id {
			continue
		}
		if v.Status == daemon.StatusRunning || v.Status == daemon.StatusStarting || v.Status == daemon.StatusStopping {
			writeErr(w, 409, "实例正在运行，先停服再换服务端文件（Windows 上运行中的 jar 是锁着的，覆盖不了）")
			return
		}
		break
	}

	var body struct {
		Type    string `json:"type"`
		Version string `json:"version"`
		Build   int    `json:"build"`
		Mirror  string `json:"mirror"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if body.Version == "" {
		writeErr(w, 400, "缺少 version")
		return
	}

	// 请求体没带源就用全局默认值（设置页里配的那个）
	if body.Mirror == "" {
		body.Mirror = s.set.Get().DefaultMirror
	}

	var build fetch.Build
	var err error
	switch fetch.Kind(body.Type) {
	case fetch.KindPaper:
		if body.Build > 0 {
			bs, e := fetch.PaperBuilds(body.Version, 0)
			if e != nil {
				err = e
				break
			}
			for _, b := range bs {
				if b.ID == body.Build {
					build = b
					break
				}
			}
			if build.URL == "" {
				err = fmt.Errorf("版本 %s 下没有构建 %d", body.Version, body.Build)
			}
		} else {
			build, err = fetch.PaperLatestBuild(body.Version)
		}
	case fetch.KindVanilla:
		var bs []fetch.Build
		bs, err = fetch.VanillaBuilds(body.Mirror, body.Version)
		if err == nil && len(bs) > 0 {
			build = bs[0]
		}
	default:
		err = fmt.Errorf("还不支持给 %s 自动下载服务端（Forge 是 installer 两段式，Fabric/NeoForge 待接）", body.Type)
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if build.JavaMajor == 0 {
		build.JavaMajor = fetch.RequiredJava(body.Mirror, body.Version)
	}

	dest := daemon.JarPathOf(inst)
	// 记下这次下的到底是什么 —— 下载完顺手把这个信息落到实例配置里，
	// 之后「概览」页就能显示「Paper 1.21.4 build 232」而不是一个光秃秃的文件名
	jarName := build.Name

	task, err := s.dl.Start(fetch.Options{
		InstanceID: id,
		Build:      build,
		DestPath:   dest,
		OnDone: func(o fetch.Options) error {
			// ① 服务端第一次启动会读 server.properties。文件不存在时按实例配置生成一份，
			//    否则用户在向导里选的端口根本不生效 —— 服务端会用默认 25565。
			//    已经存在就一个字都不动，那是用户自己的配置。
			props := map[string]string{
				"server-port": strconv.Itoa(inst.Port),
				"motd":        inst.Name,
			}
			if werr := s.mgr.WritePropertiesIfAbsent(id, props); werr != nil {
				return fmt.Errorf("写 server.properties 失败：%w", werr)
			}
			// ② 往实例控制台写一行，让用户在日志里能看见「什么时候换了服务端文件」
			s.mgr.Log(id, fmt.Sprintf("[ServerBox] 服务端文件已就绪：%s（%d 字节，校验%s）",
				jarName, build.Size, map[bool]string{true: "通过", false: "未提供"}[build.SHA != ""]))
			return nil
		},
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, task)
}
