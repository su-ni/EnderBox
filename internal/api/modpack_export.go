package api

import (
	"net/http"
	"strings"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/jobs"
	"github.com/su-ni/EnderBox/internal/modpack"
)

// 导出整合包：预览（只算不写）与真导出（落盘）。
//
// ⚠ 两者**必须走同一个构造函数**（`exportOptions`）—— 和升级预览那边同一个道理：
// 两条路各算各的，预览说「会打包 12 个模组」而实导打了 13 个，预览就白做了。
// 而这里比升级更强调这点：**发出去的包改不了**，预览是用户唯一的反悔机会。

func (s *Server) modpackExportPreview(w http.ResponseWriter, r *http.Request) {
	s.doExport(w, r, true, nil)
}

func (s *Server) modpackExportStart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	opts, err := s.exportOptions(inst, r, false)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	// 异步化：导出是分钟级（上百 MB 的 jar 逐个算哈希 + 打进包）。handler 立刻回 202 + jobId，
	// 前端轮询 /api/jobs/{id} 拿进度。「同实例只准一笔在跑」的闸在 jobStore.New 里。
	job := s.jobStore.New(id, "modpack-export")
	if job == nil {
		writeErrReason(w, 409, "busy", "该实例已有一笔作业在跑，等它结束再试")
		return
	}
	cb := func(fd, ft int, bd, bt int64, cur string) {
		s.jobStore.UpdateProgress(job, fd, ft, bd, bt, cur)
	}
	go func() {
		s.jobStore.SetStage(job, "export")
		opts.OnProgress = cb
		res, err := modpack.Export(opts)
		if err != nil {
			s.jobStore.Finish(job, jobs.StateFailed, nil, err.Error())
			return
		}
		s.jobStore.Finish(job, jobs.StateDone, map[string]any{
			"client": packPath(res.Client),
			"server": packPath(res.Server),
		}, "")
	}()
	writeJSON(w, 202, map[string]any{"jobId": job.ID})
}

// packPath 从一个包的产出里取出落盘路径；nil 包（没选那种包）返回空串。
func packPath(p *modpack.PackResult) string {
	if p == nil {
		return ""
	}
	return p.Path
}

func (s *Server) doExport(w http.ResponseWriter, r *http.Request, dryRun bool, onProgress jobs.ProgressFunc) {
	inst, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	opts, err := s.exportOptions(inst, r, dryRun)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	opts.OnProgress = onProgress

	res, err := modpack.Export(opts)
	if err != nil {
		// 导出失败的常见原因是市场连不上（反查走不通）。
		// 这时候**要说清是哪一步断的** —— 一句「导出失败」会让用户去翻 server.jar 之类的无关东西。
		writeErr(w, 500, "导出失败（多半是反查 Modrinth 时连不上，检查一下下载源设置）："+err.Error())
		return
	}
	writeJSON(w, 200, res)
}

// exportOptions 把 HTTP 入参 + 实例信息**一次性**翻成 ExportOptions。
//
// 预览与真导出共用它，是为了让「用户看到的」和「真导出的」必然一致。
func (s *Server) exportOptions(inst daemon.Instance, r *http.Request, dryRun bool) (modpack.ExportOptions, error) {
	q := r.URL.Query()

	// 默认两个包都要：用户心智里是「我有一台服，要分发给两类人」。
	// 传 `client=0` / `server=0` 才关掉对应的那个。
	wantClient := q.Get("client") != "0"
	wantServer := q.Get("server") != "0"
	if !wantClient && !wantServer {
		return modpack.ExportOptions{}, errStr("客户端包和服务端包至少要选一个")
	}

	base := s.set.Get().MarketBase
	if strings.TrimSpace(base) == "" {
		base = fetch.DefaultMarketBase
	}

	return modpack.ExportOptions{
		InstanceDir:  inst.Dir,
		InstanceName: inst.Name,
		PackName:     strings.TrimSpace(q.Get("name")),
		PackVersion:  strings.TrimSpace(q.Get("version")),
		Minecraft:    inst.MCVersion,
		// 加载器只推得出「哪种」（fabric/forge/…），**推不出具体版本号** ——
		// 版本号不写在实例里。所以由调用方给（前端可以从 packstate 带出来，或让用户填）；
		// 没给就不写这条依赖，而不是编一个数字：
		// 编错版本号会让收包那边的启动器**装成另一个加载器版本**，比不写更糟。
		LoaderKey:  modpack.NormalizeLoader(string(inst.Type)),
		LoaderVer:  strings.TrimSpace(q.Get("loaderVer")),
		WantClient: wantClient,
		WantServer: wantServer,
		ExtraDirs:  splitList(q.Get("extra")),
		DryRun:     dryRun,
		Lookup: func(sha1s []string) (map[string]fetch.RemoteVersion, error) {
			return fetch.VersionFilesByHash(base, sha1s)
		},
	}, nil
}

// splitList 把 `a,b,c` 切成去空去重的列表。空串返回 nil（不是 [""]）。
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// errStr 小工具：把字符串变成 error，省得为一句校验信息去 import errors。
type errStr string

func (e errStr) Error() string { return string(e) }
