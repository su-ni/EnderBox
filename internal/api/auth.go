package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 鉴权模型：
//   - **Cookie 会话**（登录后发的）。选 Cookie 而不是自定义请求头，是因为日志流用的是
//     EventSource —— 它没法设请求头，只有 Cookie 会自动带上。用请求头就得给 SSE 单独开个
//     口子，那等于把整条链路都放出去。
//   - **令牌**（脚本 / 外部工具用）：支持 `Authorization: Bearer` 和 `?token=` 两种带法，
//     后者同样是给 EventSource 留的。
//   - **本机回环免登录**：自己在这台机器上用不该被密码挡着。设置里可以关掉。

const sessionCookie = "sbx_session"

type sessions struct {
	mu sync.Mutex
	m  map[string]time.Time // 会话 token → 过期时间
}

func newSessions() *sessions { return &sessions{m: map[string]time.Time{}} }

func (s *sessions) issue(hours int) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	s.m[tok] = time.Now().Add(time.Duration(hours) * time.Hour)
	// 顺手清过期的，免得长期运行的进程里越攒越多
	now := time.Now()
	for k, exp := range s.m {
		if exp.Before(now) {
			delete(s.m, k)
		}
	}
	s.mu.Unlock()
	return tok
}

func (s *sessions) valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[tok]
	if !ok {
		return false
	}
	if exp.Before(time.Now()) {
		delete(s.m, tok)
		return false
	}
	return true
}

func (s *sessions) revoke(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}

// revokeAll 改密码之后把所有人踢下线 —— 改密码的意图通常就是「怀疑别人知道了」
func (s *sessions) revokeAll() {
	s.mu.Lock()
	s.m = map[string]time.Time{}
	s.mu.Unlock()
}

// clientIP 从 RemoteAddr 里抠出 IP。
// 注意不能用 X-Forwarded-For 来判回环 —— 那个头是客户端可以随便伪造的，
// 拿它决定「免登录」等于把鉴权直接送人。
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	host = strings.Trim(host, "[]")
	return net.ParseIP(host)
}

func isLoopback(r *http.Request) bool {
	ip := clientIP(r)
	return ip != nil && ip.IsLoopback()
}

// bearerOrQuery 取令牌：优先 Authorization 头，其次 ?token= （给 EventSource 用）
func bearerOrQuery(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		if s, ok := strings.CutPrefix(h, "Bearer "); ok {
			return strings.TrimSpace(s)
		}
	}
	return r.URL.Query().Get("token")
}

// withAuth 包住整张路由表。
//
// 放行的只有三处：健康检查、鉴权自身的接口、以及主页面的静态资源
// （前端要先加载出来才能显示登录界面）。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// 健康检查永远开放：系统服务探活、反向代理探活都得在没密码时能用
		if path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}
		// 登录/登出/状态这三个接口本身必须能匿名访问，否则没法登录
		if strings.HasPrefix(path, "/api/auth/") {
			next.ServeHTTP(w, r)
			return
		}
		// 非 /api/ 的路径（前端静态资源）放行
		if !strings.HasPrefix(path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}

		cfg := s.set.Get()
		if !cfg.RequireAuth {
			next.ServeHTTP(w, r)
			return
		}
		if cfg.TrustLoopback && isLoopback(r) {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(sessionCookie); err == nil && s.sess.valid(c.Value) {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := s.set.CheckToken(bearerOrQuery(r)); ok {
			next.ServeHTTP(w, r)
			return
		}

		// 401 带上 needAuth，前端才能区分「要登录」和「接口报错」
		writeJSON(w, 401, map[string]any{
			"error":    "需要登录",
			"needAuth": true,
		})
	})
}

// ── 鉴权接口 ──

func (s *Server) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", s.authStatus)
	mux.HandleFunc("POST /api/auth/login", s.authLogin)
	mux.HandleFunc("POST /api/auth/logout", s.authLogout)
}

// authStatus 前端启动时先问这个，决定要不要弹登录界面
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.set.Get()
	loop := isLoopback(r)
	ok := !cfg.RequireAuth ||
		(cfg.TrustLoopback && loop) ||
		fnSessionOK(r, s) ||
		fnTokenOK(r, s)

	writeJSON(w, 200, map[string]any{
		"requireAuth":   cfg.RequireAuth,
		"trustLoopback": cfg.TrustLoopback,
		"isLoopback":    loop,
		"hasPassword":   cfg.PasswordHash != "",
		"authenticated": ok,
		"sessionHours":  cfg.SessionHours,
	})
}

func fnSessionOK(r *http.Request, s *Server) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && s.sess.valid(c.Value)
}

func fnTokenOK(r *http.Request, s *Server) bool {
	_, ok := s.set.CheckToken(bearerOrQuery(r))
	return ok
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	cfg := s.set.Get()
	if !cfg.RequireAuth {
		// 没开保护的时候还让前端走登录流程，只会让人以为哪里坏了
		writeJSON(w, 200, map[string]any{"ok": true, "note": "当前没开启访问保护，不需要登录"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败")
		return
	}
	if !s.set.CheckPassword(body.Password) {
		// 不区分「密码错」和「没设密码」—— 少给一点信息
		writeErr(w, 401, "密码不对")
		return
	}
	tok := s.sess.issue(cfg.SessionHours)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    tok,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// 家用环境走的是 http，设 Secure 会导致 Cookie 根本发不出去
		Secure: false,
		MaxAge: cfg.SessionHours * 3600,
	})
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sess.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
