package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/jobs"
	"github.com/su-ni/EnderBox/internal/settings"
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
	// mpPacks 市场预览下下来、等着被安装取走的那份包（packToken）。
	// 见 modpack_market.go：没有它，用户点「安装」时那几十 MB 得重下一遍。
	mpPacks *packCache
	// jobStore 所有「分钟级慢操作」（备份压包/还原解包/整合包导出/模组升级安装…）
	// 共用的异步作业表。前端一份气泡组件轮询 /api/jobs 即可，不必每类各写一套。
	jobStore *jobs.Store
}

// New 起一个服务。addr 是本次实际监听的地址（用来和配置里的值对照）。
func New(mgr *daemon.Manager, set *settings.Store, addr string) *Server {
	return &Server{
		mgr:      mgr,
		dl:       fetch.NewManager(),
		set:      set,
		sess:     newSessions(),
		bindAddr: addr,
		mpPacks:  newPackCache(),
		jobStore: jobs.NewStore(),
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
	mux.HandleFunc("POST /api/instances/{id}/restart", s.restart)
	mux.HandleFunc("POST /api/instances/{id}/kill", s.kill)
	mux.HandleFunc("POST /api/instances/{id}/command", s.command)
	mux.HandleFunc("GET /api/instances/{id}/metrics", s.metrics)
	mux.HandleFunc("GET /api/instances/{id}/logs/history", s.logHistory)
	mux.HandleFunc("GET /api/instances/{id}/logs", s.logs)

	s.authRoutes(mux)
	s.settingsRoutes(mux)
	s.instanceExtraRoutes(mux)
	// 「普通模组服 → 对应的混合端」：独立一组，因为它做的事比「改个字段」重得多
	s.instanceConvertRoutes(mux)
	// 整合包：身份查询 + 升级预览（只读，真升级待接）
	s.instanceModpackRoutes(mux)
	// 整合包市场（P1）：搜索 / 选版本 / 看清代价 / 一键装成实例
	s.modpackMarketRoutes(mux)
	s.fetchRoutes(mux)
	s.pluginRoutes(mux)
	s.fileRoutes(mux)
	s.marketRoutes(mux)
	s.backupRoutes(mux)
	s.taskRoutes(mux)
	s.playerRoutes(mux)
	s.gameruleRoutes(mux)
	s.liveRoutes(mux)
	s.configRoutes(mux)
	s.systemRoutes(mux)
	s.importRoutes(mux)
	// 统一异步作业端点：备份/还原/导出/模组升级安装都走这里
	s.jobRoutes(mux)
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
	// 只有显式带 ?files=1 才连磁盘目录一起删。默认保留 ——
	// 实例目录里是 world/，用户最不可再生的东西，不该因为少传一个参数就没了。
	withFiles := r.URL.Query().Get("files") == "1"
	if err := s.mgr.Delete(r.PathValue("id"), withFiles); err != nil {
		// 「还在跑」是用户操作顺序问题，不是故障 —— 409 让界面能说人话
		code := 500
		if errors.Is(err, daemon.ErrInstanceRunning) {
			code = 409
		}
		writeErr(w, code, err.Error())
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

// restart 重启 = 先停、再起。走的是和计划任务里那条 restart 动作**完全一样**的路径，
// 免得两处各写一份、日后只改了一边。
//
// 同步等完再返回是有意的：停服最长要等 90 秒，但前端 req() 没有设总超时，
// 而且「停不下来」这种失败必须原样报回去 —— 后台异步跑的话这里就只能吞掉错误，
// 违反「不静默失败」。界面本来就每 3 秒轮询状态，等待期间会自己显示 停止中→启动中。
func (s *Server) restart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.mgr.Stop(id, 90*time.Second); err != nil {
		writeErr(w, 500, "停服失败，已取消这次重启："+err.Error())
		return
	}
	// 停完等两秒让端口彻底释放。不等的话新进程会撞 Address already in use，
	// 而那条英文报错用户要翻很久才看得懂。
	time.Sleep(2 * time.Second)
	if err := s.mgr.Start(id, ""); err != nil {
		writeErr(w, 500, "重启时启动失败："+err.Error())
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
	// 快照发完打个标记：前端靠它区分「打开页面时补的历史行」与「之后实时来的行」。
	// 源日志里不带时间戳的行（面板自己的 [EnderBox] 行、mod 列表…）要靠「面板收到的时刻」
	// 兜底，但把「打开页面那一刻」安在一条五分钟前的历史上就是撒谎 —— 历史行一律不给兜底。
	fmt.Fprint(w, "event: snapshot-end\ndata: 1\n\n")
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

// writeErrReason 在 writeErr 上多带一个机器可读的 reason 码。同一个状态码可以有
// 多种语义（比如 409 既可能是「计划变了」也可能是「已有作业在跑」），前端要按
// 语义分流「下一步该干嘛」—— 靠匹配文案太脆，改个措辞前端就哑了，必须给码。
func writeErrReason(w http.ResponseWriter, code int, reason, msg string) {
	writeJSON(w, code, map[string]string{"error": msg, "reason": reason})
}

// writeDomainErr 把 daemon 层返回的错误分流成 HTTP 状态码。
// 三种情形必须分开，否则界面没法告诉用户「下一步该干嘛」：
//   - 404：这东西没了（实例 / 任务不存在）——跟用户填什么无关，刷新一下就好
//   - 409：现在不该这么做（服务器没跑却要踢人）——要先去启动服务器
//   - 400：你填的值不合法——去改对应的那个字段
func writeDomainErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, daemon.ErrInstanceNotFound) || errors.Is(err, daemon.ErrTaskNotFound):
		writeErr(w, 404, err.Error())
	case errors.Is(err, daemon.ErrInstanceNotRunning) || errors.Is(err, daemon.ErrBanNeedsStop):
		writeErr(w, 409, err.Error())
	default:
		writeErr(w, 400, err.Error())
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
