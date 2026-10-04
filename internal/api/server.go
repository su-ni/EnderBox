package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/su-ni/ServerBox/internal/daemon"
	"github.com/su-ni/ServerBox/internal/fetch"
	"github.com/su-ni/ServerBox/internal/settings"
)

type Server struct {
	mgr *daemon.Manager
	dl  *fetch.Manager
	set *settings.Store
	// sess 会话存内存：重启守护进程要重新登录。家用面板可以接受，
	// 换来的是「改密码 = 所有人下线」这种干脆的语义。
	sess *sessions
	// bindAddr 守护进程真正绑定的地址。和配置里写的那份对照，
	// 用来告诉用户「改完重启才生效」。
	bindAddr string
}

// New 起一个服务。addr 是本次实际监听的地址（用来和配置里的值对照）。
func New(mgr *daemon.Manager, set *settings.Store, addr string) *Server {
	return &Server{
		mgr:      mgr,
		dl:       fetch.NewManager(),
		set:      set,
		sess:     newSessions(),
		bindAddr: addr,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/instances", s.list)
	mux.HandleFunc("POST /api/instances", s.create)
	mux.HandleFunc("GET /api/instances/{id}", s.get)
	mux.HandleFunc("DELETE /api/instances/{id}", s.remove)
	mux.HandleFunc("POST /api/instances/{id}/start", s.start)
	mux.HandleFunc("POST /api/instances/{id}/stop", s.stop)
	mux.HandleFunc("POST /api/instances/{id}/kill", s.kill)
	mux.HandleFunc("POST /api/instances/{id}/command", s.command)
	mux.HandleFunc("GET /api/instances/{id}/metrics", s.metrics)
	mux.HandleFunc("GET /api/instances/{id}/logs/history", s.logHistory)
	mux.HandleFunc("GET /api/instances/{id}/logs", s.logs)

	s.authRoutes(mux)
	s.settingsRoutes(mux)
	s.instanceExtraRoutes(mux)
	s.fetchRoutes(mux)
	s.pluginRoutes(mux)
	s.backupRoutes(mux)
	s.systemRoutes(mux)
	// 整张表都过鉴权：新增路由不会被漏掉，比一个个挑着挂安全
	return s.withAuth(mux)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.mgr.List())
}

func (s *Server) get(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	writeJSON(w, 200, inst)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var inst daemon.Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if inst.Name == "" {
		writeErr(w, 400, "缺少 name")
		return
	}
	if err := s.mgr.Create(&inst); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, inst)
}

func (s *Server) remove(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Delete(r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) start(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Start(r.PathValue("id"), ""); err != nil {
		// 启动前检查没过时把整份体检报告一起给出去 ——
		// 只回一句错误字符串的话，界面就只剩「启动失败」四个字，
		// 用户根本不知道是 eula 没过还是 Java 版本不对，更不知道点哪里能修。
		var pe *daemon.PreflightError
		if errors.As(err, &pe) {
			writeJSON(w, 409, map[string]any{"error": err.Error(), "preflight": pe.Result})
			return
		}
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Stop(r.PathValue("id"), 60*time.Second); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) kill(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.Kill(r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) command(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Cmd == "" {
		writeErr(w, 400, "缺少 cmd")
		return
	}
	if err := s.mgr.Send(r.PathValue("id"), body.Cmd); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.mgr.Metrics(r.PathValue("id")))
}

// logHistory 一次性返回日志快照，给不建立长连接的场景用（截图、静态预览）
func (s *Server) logHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.mgr.Logs(r.PathValue("id")).Snapshot())
}

// logs 以 SSE 推送日志：先补历史，再实时跟随
func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	buf := s.mgr.Logs(id)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "不支持流式响应")
		return
	}

	for _, line := range buf.Snapshot() {
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(line))
	}
	flusher.Flush()

	subID, ch := buf.Subscribe()
	defer buf.Unsubscribe(subID)

	for {
		select {
		case <-r.Context().Done():
			return
		case lines := <-ch:
			for _, l := range lines {
				fmt.Fprintf(w, "data: %s\n\n", mustJSON(l))
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
