package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/su-ni/EnderBox/internal/daemon"
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
	// 运行中改配置、不重启就生效：只认「有对应原版指令」的那几个键
	mux.HandleFunc("POST /api/instances/{id}/properties/apply", s.applyProps)
	// 崩溃现场判读：列出历史崩溃（含规则结论），以及读回某一份的原文
	mux.HandleFunc("GET /api/instances/{id}/crash-reports", s.crashReports)
	mux.HandleFunc("GET /api/instances/{id}/crash-reports/{file}", s.crashReportText)
}

// crashReports 崩溃诊断。最新的一份在最前，每份都带「现象/原因/怎么办」。
func (s *Server) crashReports(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.mgr.Get(id); !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	limit := 5
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = n
	}
	writeJSON(w, 200, map[string]any{"items": s.mgr.CrashReports(id, limit)})
}

// crashReportText 读回一份崩溃现场的原文，给「查看原始日志」用。
func (s *Server) crashReportText(w http.ResponseWriter, r *http.Request) {
	file := r.PathValue("file")
	text, err := s.mgr.CrashReportText(r.PathValue("id"), file)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"file": file, "text": text})
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
	case "download_jar":
		// 按实例自己的类型/版本重新拉一份服务端文件。走的是和「下服务端 jar」
		// 完全相同的那条路径（startServerJar），所以 Forge 的二次安装之类都不会漏。
		// 类型/版本/build/tag 全留空 = 全按实例配置与「自动挑最适合的构建」来。
		t, derr := s.startServerJar(inst, serverJarReq{})
		if derr != nil {
			writeErr(w, 502, derr.Error())
			return
		}
		writeJSON(w, 200, map[string]any{
			"ok":        true,
			"task":      t,
			"preflight": s.mgr.Preflight(id),
		})
		return
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

// applyProps 运行中让改动**立刻生效**，不用重启。
//
// 为什么不是所有键都能走这里：只有 difficulty / gamemode / pvp / white-list
// 在原版指令里有对应命令。别的键（motd、view-distance…）改完只能重启才读得到，
// 这里会明确拒绝并告诉用户走「保存 + 重启」—— 假装生效比不生效更糟。
//
// 顺序是「先写文件、再发指令」：万一指令发失败，文件里至少是对的，
// 用户重启一次就恢复正常。
func (s *Server) applyProps(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var updates map[string]string
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if len(updates) == 0 {
		writeErr(w, 400, "没有要生效的改动")
		return
	}

	// 先全量校验：有一个键不支持就整体拒绝。
	// 允许「一半生效一半没生效」的话，用户会以为全好了 —— 那是最坑的一种结果。
	cmds := make([]string, 0, len(updates))
	for k, v := range updates {
		cmd, ok := daemon.LiveCommand(k, v)
		if !ok {
			writeErr(w, 400, fmt.Sprintf(
				"「%s」没有对应的服务器指令，改不了立刻生效 —— 请用「保存」写进文件后重启服务器", k))
			return
		}
		cmds = append(cmds, cmd)
	}
	sort.Strings(cmds) // 定序，方便排查也方便测试

	inst, ok := s.mgr.View(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	if inst.RuntimeState.Status != daemon.StatusRunning {
		writeErr(w, 409, "服务器没在运行 —— 直接保存进配置文件就行，下次启动自然会读到")
		return
	}

	if err := s.mgr.WriteProperties(id, updates); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	for _, cmd := range cmds {
		if err := s.mgr.Send(id, cmd); err != nil {
			writeErr(w, 500, "已写进配置文件，但控制台指令没发出去："+err.Error())
			return
		}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "commands": cmds})
}
