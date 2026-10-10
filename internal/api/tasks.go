package api

import (
	"encoding/json"
	"net/http"

	"github.com/su-ni/EnderBox/internal/daemon"
)

// 计划任务四条路由 + 一条只改启用状态的 PATCH。
//
// 和备份策略同一套理由：**不走整体 PUT 实例**。
// 任务页只关心任务，让它在整份实例对象上做「读-改-写」，迟早和配置页互相覆盖。
func (s *Server) taskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/tasks", s.taskList)
	mux.HandleFunc("POST /api/instances/{id}/tasks", s.taskCreate)
	mux.HandleFunc("PUT /api/instances/{id}/tasks/{taskId}", s.taskUpdate)
	// 开关单独一条：它是最频繁的操作，不该提交整份表单 ——
	// 否则在一个别处已经改过的旧表单上点一下开关，会把那条任务回退成旧值。
	mux.HandleFunc("PATCH /api/instances/{id}/tasks/{taskId}", s.taskToggle)
	mux.HandleFunc("DELETE /api/instances/{id}/tasks/{taskId}", s.taskDelete)
}

func (s *Server) taskList(w http.ResponseWriter, r *http.Request) {
	list, err := s.mgr.ListTasks(r.PathValue("id"))
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}

func (s *Server) taskCreate(w http.ResponseWriter, r *http.Request) {
	var t daemon.ScheduledTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	v, err := s.mgr.SaveTask(r.PathValue("id"), "", t)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 201, v)
}

func (s *Server) taskUpdate(w http.ResponseWriter, r *http.Request) {
	var t daemon.ScheduledTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	v, err := s.mgr.SaveTask(r.PathValue("id"), r.PathValue("taskId"), t)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) taskToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	v, err := s.mgr.SetTaskEnabled(r.PathValue("id"), r.PathValue("taskId"), body.Enabled)
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) taskDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.DeleteTask(r.PathValue("id"), r.PathValue("taskId")); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
