package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
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
	// 认识的类型里，没接实现的要给出「为什么没接」，而不是一句「不认识」——
	// 前者用户知道下一步该干嘛，后者只会让人以为是自己选错了。
	if src, ok := fetch.SourceOf(k); ok {
		if !src.Available {
			return "", fmt.Errorf("%s 还不能自动下载，%s", src.Label, src.Note)
		}
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
	case fetch.KindFabric:
		vs, err = fetch.FabricVersions()
	case fetch.KindForge:
		vs, err = fetch.ForgeVersions()
	case fetch.KindVelocity:
		vs, err = fetch.VelocityVersions()
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
	case fetch.KindFabric:
		bs, err = fetch.FabricBuilds(s.mirrorOf(r), ver)
	case fetch.KindForge:
		bs, err = fetch.ForgeBuilds(s.mirrorOf(r), ver, limit)
	case fetch.KindVelocity:
		bs, err = fetch.VelocityBuilds(ver, limit)
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}

	// 补上「这个版本要哪个 Java」。只有 **Paper** 需要在这里补 ——
	// 原版自己就带；Fabric 跟随 MC 版本、在 FabricBuilds 里已经填了；
	// Velocity 是代理端、跟 MC 版本无关，走 velocityJava() 的版本线规则。
	// 拿不到就让它是 0，前端如实说「拿不到」，别编一个数出来。
	if len(bs) > 0 && bs[0].JavaMajor == 0 && k == fetch.KindPaper {
		jm := fetch.RequiredJava(s.mirrorOf(r), ver)
		if jm > 0 {
			for i := range bs {
				bs[i].JavaMajor = jm
				bs[i].JavaFrom = "mojang"
			}
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
		// Tag 上游对某个构建的稳定标识。Forge 必须带：它的版本号不是整数
		// （49.0.30 / 14.23.5.2860），只能按字符串回源站定位，
		// 靠 build 序号会因为上游加了新构建而错位成另一个版本。
		Tag string `json:"tag"`
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
	case fetch.KindFabric:
		// Fabric 只有一个构建（最新稳定 Loader），不需要按 build 号挑
		var bs []fetch.Build
		bs, err = fetch.FabricBuilds(body.Mirror, body.Version)
		if err == nil && len(bs) > 0 {
			build = bs[0]
		}
	case fetch.KindForge:
		// Forge 少了 tag 就没法准确定位（版本号不是整数），所以前端必须带。
		// 真没带也不报错 —— 退回「这个版本的推荐构建」，那是最合理的默认，
		// 报错只会让用户对着一个他无从下手的信息发呆。
		if body.Tag == "" {
			var bs []fetch.Build
			if bs, err = fetch.ForgeBuilds(body.Mirror, body.Version, 20); err == nil {
				if len(bs) > 0 {
					body.Tag = bs[0].Tag
				} else {
					err = fmt.Errorf("Forge 没有为 %s 出过任何构建", body.Version)
				}
			}
		}
		if err == nil {
			build, err = fetch.ForgeInstaller(body.Tag, body.Mirror)
		}
	case fetch.KindVelocity:
		if body.Build > 0 {
			var bs []fetch.Build
			bs, err = fetch.VelocityBuilds(body.Version, 0)
			if err == nil {
				for _, b := range bs {
					if b.ID == body.Build {
						build = b
						break
					}
				}
				if build.URL == "" {
					err = fmt.Errorf("版本 %s 下没有构建 %d", body.Version, body.Build)
				}
			}
		} else {
			build, err = fetch.VelocityLatestBuild(body.Version)
		}
	default:
		if src, ok := fetch.SourceOf(fetch.Kind(body.Type)); ok {
			err = fmt.Errorf("%s 还不能自动下载，%s", src.Label, src.Note)
		} else {
			err = fmt.Errorf("不认识的服务端类型：%s", body.Type)
		}
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if build.JavaMajor == 0 && fetch.Kind(body.Type) == fetch.KindPaper {
		build.JavaMajor = fetch.RequiredJava(body.Mirror, body.Version)
		if build.JavaMajor > 0 {
			build.JavaFrom = "mojang"
		}
	}

	dest := daemon.JarPathOf(inst)
	isForge := fetch.Kind(body.Type) == fetch.KindForge
	if isForge {
		// Forge 下的**不是**实例要启动的那个 jar，而是安装器。要是还落在 JarPathOf 上，
		// 就会拿安装器覆盖掉真正该启动的文件，然后启动时一脸茫然。
		// 该启动哪个由装完之后按目录实况决定（daemon.InstallForge 会写回实例配置）。
		dest = filepath.Join(inst.Dir, build.Name)
	}
	// 记下这次下的到底是什么 —— 下载完顺手把这个信息落到实例配置里，
	// 之后「概览」页就能显示「Paper 1.21.4 build 232」而不是一个光秃秃的文件名
	jarName := build.Name

	opts := fetch.Options{
		InstanceID: id,
		Build:      build,
		DestPath:   dest,
		OnDone: func(o fetch.Options, written int64) error {
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
			// ② 往实例控制台写一行，让用户在日志里能看见「什么时候换了服务端文件」。
			//    大小用真正落盘的字节数，不用 build.Size —— 列表里那个多数时候是 0，
			//    写出来就是「0 字节的安装器」这种一眼假的记录。
			s.mgr.Log(id, fmt.Sprintf("[ServerBox] 服务端文件已就绪：%s（%d 字节，校验%s）",
				jarName, written, map[bool]string{true: "通过", false: "未提供"}[build.SHA != ""]))
			return nil
		},
	}

	if isForge {
		// 第二阶段：把 installer 真跑一遍。挂在同一个任务里 ——
		// 「下个 Forge」对用户就是一件事，拆成两个任务等于让他自己知道要点第二次。
		wantJava := build.JavaMajor
		if wantJava <= 0 {
			// 拿不到权威值就按 8 起步，ResolveJava 会在这个基础上往上取更高的
			wantJava = 8
		}
		opts.PostInstall = func(ctx context.Context, report func(string)) error {
			rt, jerr := s.mgr.ResolveJava(wantJava)
			if jerr != nil {
				return fmt.Errorf("本机没有能跑 Forge 安装器的 Java：%w", jerr)
			}
			_, ierr := s.mgr.InstallForge(daemon.ForgeInstallOpts{
				Ctx:        ctx,
				InstanceID: id,
				JavaPath:   rt.Path,
				Tag:        build.Tag,
				Installer:  dest,
				JavaMajor:  build.JavaMajor,
				// 库文件也走同一个源。选了 BMCLAPI 却让安装器去官方下库，
				// 等于「因为官方慢所以换镜像」只换了一半。
				MirrorURL: fetch.ForgeMirrorURL(body.Mirror),
			}, report)
			return ierr
		}
	}

	task, err := s.dl.Start(opts)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 202, task)
}
