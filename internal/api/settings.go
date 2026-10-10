package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/settings"
)

// 全局设置接口。分两块：
//   - 读写配置本身（`GET|PUT /api/settings`）
//   - 密码与令牌这类「写进去就看不见了」的敏感操作，各自独立接口，
//     免得不小心被一次 PUT 覆盖掉
//
// 监听地址额外单独一个接口：它需要能被「真正绑在哪个地址」对照着看。

func (s *Server) settingsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	mux.HandleFunc("POST /api/settings/password", s.setPassword)
	mux.HandleFunc("GET /api/settings/tokens", s.listTokens)
	mux.HandleFunc("POST /api/settings/tokens", s.addToken)
	mux.HandleFunc("DELETE /api/settings/tokens/{id}", s.removeToken)
	mux.HandleFunc("GET /api/settings/mirrors", s.mirrorOptions)
}

// settingsView 给前端的视图。
// 刻意**不返回** PasswordHash / Token 的 Hash —— 那两样出去一次就算泄露了。
type settingsView struct {
	RequireAuth   bool         `json:"requireAuth"`
	TrustLoopback bool         `json:"trustLoopback"`
	SessionHours  int          `json:"sessionHours"`
	HasPassword   bool         `json:"hasPassword"`
	PasswordSetAt string       `json:"passwordSetAt,omitempty"`
	DefaultMirror string       `json:"defaultMirror"`
	MarketBase    string       `json:"marketBase"`
	ListenAddr    string       `json:"listenAddr"`
	Tokens        []tokenView  `json:"tokens"`
	Runtime       runtimeState `json:"runtime"`
}

type tokenView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	CreatedAt string `json:"createdAt"`
	LastUsed  string `json:"lastUsed,omitempty"`
}

// runtimeState 「实际在跑的是什么」，和上面「配置里写的是什么」对照着看。
// 监听地址改完要重启才生效，不给对照的话用户会以为改了没生效是 bug。
type runtimeState struct {
	// BindingAddr 守护进程真正绑定的地址
	BindingAddr string `json:"bindingAddr"`
	// SavedAt 配置里写的地址和实际绑定不一致时，这里为 true
	RestartNeeded bool `json:"restartNeeded"`
	// Exposed 当前是否暴露在非回环地址上（决定要不要显示安全告警）
	Exposed bool `json:"exposed"`
}

func (s *Server) settingsView() settingsView {
	cfg := s.set.Get()
	v := settingsView{
		RequireAuth:   cfg.RequireAuth,
		TrustLoopback: cfg.TrustLoopback,
		SessionHours:  cfg.SessionHours,
		HasPassword:   cfg.PasswordHash != "",
		DefaultMirror: cfg.DefaultMirror,
		MarketBase:    cfg.MarketBase,
		ListenAddr:    cfg.ListenAddr,
		Tokens:        make([]tokenView, 0, len(cfg.Tokens)),
	}
	if cfg.PasswordSetAt != nil {
		v.PasswordSetAt = cfg.PasswordSetAt.Format("2006-01-02 15:04")
	}
	for _, t := range cfg.Tokens {
		tv := tokenView{
			ID:        t.ID,
			Name:      t.Name,
			Prefix:    t.Prefix,
			CreatedAt: t.CreatedAt.Format("2006-01-02 15:04"),
		}
		if t.LastUsed != nil {
			tv.LastUsed = t.LastUsed.Format("2006-01-02 15:04")
		}
		v.Tokens = append(v.Tokens, tv)
	}

	bind := s.bindAddr
	if bind == "" {
		bind = cfg.ListenAddr
	}
	v.Runtime = runtimeState{
		BindingAddr:   bind,
		RestartNeeded: bind != cfg.ListenAddr,
		Exposed:       isExposed(bind),
	}
	return v
}

// isExposed 绑在非回环地址上就算暴露。
// `:23333` 这种没写主机的形式绑的是所有网卡，算暴露。
func isExposed(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return true // 空 = 0.0.0.0 / ::，所有网卡
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return true // 主机名解析不了，保守认为暴露
	}
	return !ip.IsLoopback()
}

func (s *Server) getSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, s.settingsView())
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RequireAuth   *bool   `json:"requireAuth"`
		TrustLoopback *bool   `json:"trustLoopback"`
		SessionHours  *int    `json:"sessionHours"`
		DefaultMirror *string `json:"defaultMirror"`
		MarketBase    *string `json:"marketBase"`
		ListenAddr    *string `json:"listenAddr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败")
		return
	}

	_, err := s.set.Update(func(v *settings.Settings) error {
		if body.TrustLoopback != nil {
			v.TrustLoopback = *body.TrustLoopback
		}
		if body.SessionHours != nil {
			if *body.SessionHours < 1 || *body.SessionHours > 24*30 {
				return errBadRequest("会话有效期要在 1 小时到 30 天之间")
			}
			v.SessionHours = *body.SessionHours
		}
		if body.DefaultMirror != nil {
			v.DefaultMirror = *body.DefaultMirror
		}
		if body.MarketBase != nil {
			// 只做最基本的形状校验：必须是 http(s) 地址。
			// 其余（能不能通）只能实际请求才知道，提前拦反而会挡住自建的局域网镜像。
			mb := strings.TrimRight(strings.TrimSpace(*body.MarketBase), "/")
			if mb != "" && !strings.HasPrefix(mb, "http://") && !strings.HasPrefix(mb, "https://") {
				return errBadRequest("市场地址要写成 http:// 或 https:// 开头")
			}
			v.MarketBase = mb
		}
		if body.ListenAddr != nil {
			if err := validateListenAddr(*body.ListenAddr); err != nil {
				return err
			}
			v.ListenAddr = *body.ListenAddr
		}
		if body.RequireAuth != nil {
			// 开保护但没密码也没令牌 = 把自己锁在外面。这种状态一律拒绝保存，
			// 并且明确告诉用户缺什么，而不是默默存下去让人下次连不上。
			if *body.RequireAuth && v.PasswordHash == "" && len(v.Tokens) == 0 {
				return errBadRequest("开启访问保护前，先设一个密码（或至少建一个 API 令牌）")
			}
			v.RequireAuth = *body.RequireAuth
		}
		return nil
	})
	if err != nil {
		writeErr(w, statusOf(err), err.Error())
		return
	}
	writeJSON(w, 200, s.settingsView())
}

// errBadRequest 把「用户填错了」和「内部出错」分开，好让上层回 400 而不是 500
type badRequest string

func (e badRequest) Error() string { return string(e) }

func errBadRequest(msg string) error { return badRequest(msg) }

func statusOf(err error) int {
	if _, ok := err.(badRequest); ok {
		return 400
	}
	return 500
}

// validateListenAddr 挡掉明显写不对的地址。
// 写错了下次就起不来，而这种错在「保存成功」的反馈里完全看不出来。
func validateListenAddr(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return errBadRequest("监听地址不能为空，比如 :23333 或 127.0.0.1:23333")
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return errBadRequest("监听地址要写成 主机:端口 的形式，比如 :23333")
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return errBadRequest("端口要填 1-65535 之间的数字")
	}
	return nil
}

func (s *Server) setPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Old     string `json:"old"`
		New     string `json:"new"`
		Enable  *bool  `json:"enable"` // 同时开启访问保护
		Disable bool   `json:"disable"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败")
		return
	}

	// 已经有密码时，改密码必须先给旧的 —— 否则任何能连上的人都能把你锁在外面
	if s.set.HasPassword() && !s.set.CheckPassword(body.Old) {
		writeErr(w, 401, "当前密码不对")
		return
	}
	if body.Disable {
		if err := s.set.SetPassword(""); err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		s.sess.revokeAll()
		writeJSON(w, 200, s.settingsView())
		return
	}
	if err := s.set.SetPassword(body.New); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if body.Enable != nil && *body.Enable {
		if _, err := s.set.Update(func(v *settings.Settings) error {
			v.RequireAuth = true
			return nil
		}); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	// 改完密码把现有会话全踢掉：改密码的动机通常就是「怀疑别人知道了」
	s.sess.revokeAll()
	writeJSON(w, 200, s.settingsView())
}

func (s *Server) listTokens(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"items": s.settingsView().Tokens})
}

// addToken 生成令牌。**明文只在这个响应里出现这一次**，
// 之后列表里只剩前缀 + 哈希，想再看得重新生成。
func (s *Server) addToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	t, plain, err := s.set.AddToken(strings.TrimSpace(body.Name))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"id":        t.ID,
		"name":      t.Name,
		"prefix":    t.Prefix,
		"createdAt": t.CreatedAt.Format("2006-01-02 15:04"),
		"token":     plain, // ← 只此一次
	})
}

func (s *Server) removeToken(w http.ResponseWriter, r *http.Request) {
	if !s.set.RemoveToken(r.PathValue("id")) {
		writeErr(w, 404, "没有这个令牌")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// mirrorOptions 下载源选项。取自 fetch 包的镜像表，不在前端再写一份。
func (s *Server) mirrorOptions(w http.ResponseWriter, _ *http.Request) {
	// 用 vanilla 取全集：它的 For 过滤最宽松，能把所有源列出来
	type opt struct {
		ID      string   `json:"id"`
		Label   string   `json:"label"`
		Applies []string `json:"appliesTo"`
	}
	var out []opt
	seen := map[string]bool{}
	for _, k := range []fetch.Kind{fetch.KindPaper, fetch.KindVanilla} {
		for _, m := range fetch.MirrorsFor(k) {
			if seen[m.ID] {
				continue
			}
			seen[m.ID] = true
			var applies []string
			if len(m.For) == 0 {
				applies = []string{string(fetch.KindPaper), string(fetch.KindVanilla)}
			} else {
				for _, f := range m.For {
					applies = append(applies, string(f))
				}
			}
			out = append(out, opt{ID: m.ID, Label: m.Label, Applies: applies})
		}
	}
	writeJSON(w, 200, map[string]any{"items": out})
}
