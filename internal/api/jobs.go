package api

import (
	"net/http"
)

// jobRoutes 统一的异步作业端点。
//
// 所有「分钟级慢操作」（备份压包 / 还原解包 / 整合包导出 / 模组升级安装…）都从这里进出：
// 发起方拿到 jobId，前端一份气泡组件轮询 /api/jobs/{id} 即可，不必每类各写一套轮询 + 取消 + 进度逻辑。
func (s *Server) jobRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/jobs", s.jobList)
	mux.HandleFunc("GET /api/jobs/{id}", s.jobGet)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.jobCancel)
	mux.HandleFunc("POST /api/jobs/{id}/dismiss", s.jobDismiss)
}

// jobList 列出作业。?owner= 非空时只返回该归属（实例 id）的；否则全部。
// 终态按 startedAt 倒序（新在前），前端直接拿来渲染「最近的几次操作」。
func (s *Server) jobList(w http.ResponseWriter, r *http.Request) {
	owner := r.URL.Query().Get("owner")
	list := s.jobStore.List(owner)
	out := make([]map[string]any, 0, len(list))
	for _, j := range list {
		out = append(out, s.jobStore.Snapshot(j))
	}
	// ⚠ 与其它列表端点同一口径：裸数组会让前端为这一处写特例分支，
	// 而 items 包装 {length:0} 的恒无害依赖也在各列表默认成立。
	writeJSON(w, 200, map[string]any{"items": out})
}

// jobGet 取单笔作业快照。不存在回 404 —— 前端轮询时这代表「这作业早被 dismiss 了 / 进程重启过」。
func (s *Server) jobGet(w http.ResponseWriter, r *http.Request) {
	j := s.jobStore.Get(r.PathValue("id"))
	if j == nil {
		writeErr(w, 404, "作业不存在")
		return
	}
	writeJSON(w, 200, s.jobStore.Snapshot(j))
}

// jobCancel 取消一笔在跑的作业。终态或不存在回 409 —— 取消键在作业跑完后就该消失，
// 不该还能点到，点到也只会拿到这个 409。
func (s *Server) jobCancel(w http.ResponseWriter, r *http.Request) {
	if !s.jobStore.Cancel(r.PathValue("id")) {
		writeErr(w, 409, "作业不存在或已结束")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// jobDismiss 把一笔看过的终态作业从表里移除。仍在跑的不能 dismiss（回 409），
// 否则会误删一个正在被轮询的作业、让前端误以为进程重启了。
func (s *Server) jobDismiss(w http.ResponseWriter, r *http.Request) {
	if !s.jobStore.Dismiss(r.PathValue("id")) {
		writeErr(w, 409, "作业不存在或仍在运行")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
