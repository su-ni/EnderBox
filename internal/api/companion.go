package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/materials"
)

// 这一页服务于「实时玩家数据」：读插件写的快照、下发写操作、开关插件、查审计。
//
// 命名用 live 而不是 companion：前者说的是用户看到的东西（实时数据），
// 后者是实现细节（那个插件叫什么）。用户不需要知道插件叫啥。

func (s *Server) liveRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/live", s.liveSnapshot)
	mux.HandleFunc("POST /api/instances/{id}/live/command", s.liveCommand)
	mux.HandleFunc("GET /api/instances/{id}/live/audit", s.liveAudit)
	// 开关单独一条路由，不走整体 PUT 实例：整体 PUT 只要有一处没带这个字段就会把
	// 用户的设置悄悄改回去（备份策略那次已经栽过一次）。
	mux.HandleFunc("PUT /api/instances/{id}/companion", s.companionToggle)
	mux.HandleFunc("GET /api/materials/icon", s.materialIcon)
	mux.HandleFunc("GET /api/materials/lookup", s.materialLookup)
}

func (s *Server) liveSnapshot(w http.ResponseWriter, r *http.Request) {
	snap, err := s.mgr.CompanionSnapshot(r.PathValue("id"))
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, snap)
}

// liveCommand 下发一条会**改玩家东西**的操作。
//
// 两件事必须在这一层做掉，不能丢给前端：
//
//	· op 白名单 —— 插件那边也会校验，但进了面板的边界就该先挡一次；
//	· 审计 —— 不管成功失败都记一条。事后有人问「谁清了我的背包」，
//	  这份日志是唯一能回答的东西。失败也要记，否则「点了但没生效」永远查不到。
func (s *Server) liveCommand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Op     string         `json:"op"`
		Target string         `json:"target"`
		Args   map[string]any `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if !daemon.CompanionOpOK(body.Op) {
		writeErr(w, 400, fmt.Sprintf("不支持的操作：%s（能用的是：%v）", body.Op, daemon.CompanionOps))
		return
	}
	if body.Target == "" {
		writeErr(w, 400, "没指定目标玩家")
		return
	}

	res, err := s.mgr.CompanionCommand(id, body.Op, body.Target, body.Args)
	// 面板是单用户模型（会话只证明「登录过」），所以 Who 记来源地址更有用 ——
	// 事后能回答「是从家里那台机器操作的，还是从别的设备」。
	s.mgr.AppendAudit(daemon.AuditEntry{
		At:     time.Now().Format(time.RFC3339),
		Who:    r.RemoteAddr,
		Inst:   id,
		Op:     body.Op,
		Target: body.Target,
		Args:   body.Args,
		OK:     err == nil && res.OK,
		Error:  errText(err, res),
	})
	if err != nil {
		writeDomainErr(w, err)
		return
	}
	// 插件执行失败（比如玩家不在线）不是 500 —— 那是「现在不该这么做」（409）
	// 或者「你填的值不对」（400）。一律塞 200 会让前端把失败当成功。
	if !res.OK {
		writeErr(w, 409, res.Error)
		return
	}
	writeJSON(w, 200, res)
}

func errText(err error, res daemon.CompanionResult) string {
	if err != nil {
		return err.Error()
	}
	return res.Error
}

func (s *Server) liveAudit(w http.ResponseWriter, r *http.Request) {
	n := 200
	if v := r.URL.Query().Get("n"); v != "" {
		fmt.Sscanf(v, "%d", &n)
	}
	list, err := s.mgr.AuditLog(n)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 只留这个实例的
	id := r.PathValue("id")
	out := make([]daemon.AuditEntry, 0, len(list))
	for _, e := range list {
		if e.Inst == id {
			out = append(out, e)
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) companionToggle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		On bool `json:"on"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	inst, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	sup := daemon.CompanionSupportOf(inst)
	if !sup.Supported && body.On {
		writeErr(w, 400, sup.Reason)
		return
	}
	// ⚠ 别用局部变量取地址然后写回 —— 那个地址在函数返回后还被引用着，
	// 这里必须让实例自己持有一个 bool。
	v := body.On
	inst.Companion = &v
	if err := s.mgr.Update(&inst); err != nil {
		writeDomainErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok":        true,
		"on":        v,
		"installed": false,
		// 改动要下次启动才生效 —— 明说出来，别让用户以为点了没反应
		"note": "改动会在下次启动服务器时生效",
	})
}

func (s *Server) materialIcon(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	b, ok := materials.Icon(id)
	if !ok {
		writeErr(w, 404, "这个物品没有图标")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// 贴图是嵌在二进制里的，内容永远不会变 —— 缓存一年。
	// 不给 Cache-Control 的话，一个 27 格的背包每次刷新都要重新拉 27 张图。
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Write(b)
}

func (s *Server) materialLookup(w http.ResponseWriter, r *http.Request) {
	ids := r.URL.Query()["id"]
	out := map[string]any{}
	for _, id := range ids {
		e, ok := materials.Item(id)
		if !ok {
			out[id] = nil
			continue
		}
		icon := ""
		if materials.IconName(id) != "" {
			icon = "/api/materials/icon?id=" + id
		}
		out[id] = map[string]string{"zh": e.ZH, "en": e.EN, "icon": icon}
	}
	writeJSON(w, 200, out)
}
