package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/su-ni/EnderBox/internal/daemon"
)

// 游戏规则两条路由：读全部（GET）、改一条（POST）。
//
// 为什么走控制台命令、规则全集从哪来 —— 全在 daemon/gamerules.go 顶部那段。
// 这里只负责协议转换与错误分流。
func (s *Server) gameruleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/gamerules", s.gameruleList)
	mux.HandleFunc("POST /api/instances/{id}/gamerules", s.gameruleSet)
}

// gameruleList 一次给全：规则行 + 分组顺序。
//
// 分组顺序跟着行一起发，不在前端硬编码 —— 以后后端加组、调顺序，前端不用动。
// 只列「真的读到规则的组」（空组不显示）。
func (s *Server) gameruleList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.mgr.Gamerules(r.PathValue("id"))
	if err != nil {
		// 服务器没在跑 = 现在不该读（409），带 reason 码让前端直接切「停服说明」态。
		// 靠匹配文案太脆（写这条注释的工夫文案已经改过两稿），必须给码。
		// 「一条都没读到」（多半是还在启动）同样是 409，但码不同 —— 前端给的下一步
		// 也不一样：停服是「去启动」，没就绪是「稍等再点重新读取」。
		switch {
		case errors.Is(err, daemon.ErrGameruleNoServer):
			writeErrReason(w, 409, "no-server", err.Error())
		case errors.Is(err, daemon.ErrGameruleNoReply):
			writeErrReason(w, 409, "not-ready", err.Error())
		default:
			writeDomainErr(w, err)
		}
		return
	}

	groups := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, row := range rows {
		if !seen[row.Group] {
			seen[row.Group] = true
			groups = append(groups, row.Group)
		}
	}
	writeJSON(w, 200, map[string]any{"rules": rows, "groups": groups})
}

// gameruleSet 改一条规则，返回服务端确认后的实际值。
// 响应里回带 value 而不是只回 ok：个别规则的输入会被服务端夹到范围内，
// 前端要显示「到底生效成什么」，不能拿自己发的值当事实。
func (s *Server) gameruleSet(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	value, err := s.mgr.SetGamerule(r.PathValue("id"), body.Name, body.Value)
	if err != nil {
		switch {
		case errors.Is(err, daemon.ErrGameruleNoServer):
			writeErrReason(w, 409, "no-server", err.Error())
		case errors.Is(err, daemon.ErrGameruleNoConfirm):
			// 服务器还在起（或没应答）—— 让界面说「稍后重试」，别报成 400「你填错了」。
			writeErrReason(w, 409, "not-ready", err.Error())
		case errors.Is(err, daemon.ErrGameruleNotFound):
			writeErrReason(w, 404, "unknown-rule", err.Error())
		default:
			// 名字/值格式不合法（含防注入拒绝）—— 400，消息里带具体原因
			writeDomainErr(w, err)
		}
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "name": strings.TrimSpace(body.Name), "value": value})
}
