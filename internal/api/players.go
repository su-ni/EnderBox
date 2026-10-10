package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// 玩家管理六条路由。
//
// 数据全部来自服务端自己写的那几个原生 json —— **我们没有服务端插件**，
// 也不去猜它的内存状态。设计说明见 daemon/players.go 顶部那段。
func (s *Server) playerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/players", s.playerList)
	mux.HandleFunc("GET /api/instances/{id}/players/profile", s.playerProfile)
	mux.HandleFunc("POST /api/instances/{id}/players/whitelist", s.playerWhitelist)
	mux.HandleFunc("POST /api/instances/{id}/players/op", s.playerOp)
	mux.HandleFunc("POST /api/instances/{id}/players/ban", s.playerBan)
	mux.HandleFunc("POST /api/instances/{id}/players/kick", s.playerKick)
}

// playerList 一次给全：在线 + 白名单 + OP + 玩家封禁 + IP 封禁 + 两个开关状态。
// 拆成五条接口的话前端进页面要打五次，中间任何一个失败都会让页面处于半残状态。
func (s *Server) playerList(w http.ResponseWriter, r *http.Request) {
	roster, err := s.mgr.PlayerLists(r.PathValue("id"))
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, roster)
}

// playerWhitelist 加进 / 移出白名单。
func (s *Server) playerWhitelist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		On   bool   `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.WhitelistSet(r.PathValue("id"), body.Name, body.On); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// playerOp 给 / 撤管理员。level 只在「给」的时候有意义。
func (s *Server) playerOp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Level int    `json:"level"`
		On    bool   `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.OpSet(r.PathValue("id"), body.Name, body.Level, body.On); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// playerBan 封禁 / 解封。kind 区分「玩家」和「IP」；on=false 表示解封。
// expiresAt 是 RFC3339，空串 = 永久（服务端那条命令不支持时长，限时只能在停服时写）。
func (s *Server) playerBan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target    string `json:"target"`
		Kind      string `json:"kind"`
		Reason    string `json:"reason"`
		On        bool   `json:"on"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	var expires time.Time
	if v := strings.TrimSpace(body.ExpiresAt); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeErr(w, 400, "解封时间格式不对（要 RFC3339，例如 2026-10-12T21:00:00+08:00）")
			return
		}
		expires = t
	}
	if err := s.mgr.BanSet(r.PathValue("id"), body.Target, body.Kind, body.Reason, expires, body.On); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// playerProfile 一个玩家的只读档案（游玩时长 / 死亡 / 最后进服时间）。
//
// 单独一条路由而不是塞进玩家列表：列表每 5 秒轮询一次，而档案要按玩家逐个去
// 世界目录里翻文件 —— 摊平到列表上等于每 5 秒读一整目录的文件。
// 只在详情抽屉打开时拉一次就够。
func (s *Server) playerProfile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("name"))
	if name == "" {
		writeErr(w, 400, "缺少 name 参数")
		return
	}
	p, err := s.mgr.PlayerProfile(r.PathValue("id"), name, q.Get("uuid"))
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, p)
}

// playerKick 把在线玩家踢出去。服务器没在跑就是 409，不是 400。
func (s *Server) playerKick(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.KickPlayer(r.PathValue("id"), body.Name, body.Reason); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
