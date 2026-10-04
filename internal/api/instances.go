package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/su-ni/ServerBox/internal/daemon"
)

// 这一组是「实例本身」的读写：编辑、启动前体检、EULA、server.properties。
// 放在独立文件里，是为了让 server.go 只留生命周期动作（起停杀、命令、日志流）。

func (s *Server) instanceExtraRoutes(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/instances/{id}", s.update)
	mux.HandleFunc("GET /api/instances/{id}/preflight", s.preflight)
	mux.HandleFunc("POST /api/instances/{id}/eula", s.acceptEULA)
	mux.HandleFunc("POST /api/instances/{id}/fix", s.applyFix)
	mux.HandleFunc("GET /api/instances/{id}/properties", s.readProps)
	mux.HandleFunc("PUT /api/instances/{id}/properties", s.writeProps)
}

// update 整体覆盖式更新。前端手里本来就有完整实例对象（列表接口返回的就是全量），
// 所以不做字段合并 —— 合并语义在布尔字段上没法猜（false 是「要改成关」还是「没传」）。
func (s *Server) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	old, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	var inst daemon.Instance
	if err := json.NewDecoder(r.Body).Decode(&inst); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	inst.ID = id
	// 不允许改的东西由 Manager.Update 兜住 CreatedAt/Dir，这里再挡一层明显非法的值
	if strings.TrimSpace(inst.Name) == "" {
		inst.Name = old.Name
	}
	if strings.TrimSpace(inst.JarPath) == "" {
		inst.JarPath = old.JarPath
	}
	if inst.MaxMem <= 0 {
		inst.MaxMem = old.MaxMem
	}
	if inst.MinMem <= 0 {
		inst.MinMem = old.MinMem
	}
	if inst.Port < 1 || inst.Port > 65535 {
		writeErr(w, 400, "端口要在 1-65535 之间")
		return
	}
	if inst.MaxMem < inst.MinMem {
		inst.MinMem = inst.MaxMem
	}
	if err := s.mgr.Update(&inst); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, inst)
}

func (s *Server) preflight(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.mgr.Preflight(r.PathValue("id")))
}

func (s *Server) acceptEULA(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.AcceptEULA(r.PathValue("id")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "preflight": s.mgr.Preflight(r.PathValue("id"))})
}

// applyFix 把 Preflight Issue 里的 Fix 标识变成一次真实操作。
// 前端只认得 code，怎么修是后端的事 —— 这样加新检查项不用改前端。
func (s *Server) applyFix(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Code == "" {
		writeErr(w, 400, "缺少 code")
		return
	}
	inst, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}

	switch body.Code {
	case "accept_eula":
		if err := s.mgr.AcceptEULA(id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	case "sync_port":
		if err := s.mgr.WriteProperties(id, map[string]string{
			"server-port": strconv.Itoa(inst.Port),
		}); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	case "rescan_java":
		// 不需要改动什么，重新扫一遍就是「修」—— 新装的 Java 得被发现
	default:
		writeErr(w, 400, "不认识的修复项: "+body.Code)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "preflight": s.mgr.Preflight(id)})
}

func (s *Server) readProps(w http.ResponseWriter, r *http.Request) {
	rows, err := s.mgr.ReadProperties(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": rows, "groupDesc": daemon.GroupDescs()})
}

func (s *Server) writeProps(w http.ResponseWriter, r *http.Request) {
	var updates map[string]string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.WriteProperties(r.PathValue("id"), updates); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
