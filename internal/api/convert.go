package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
)

// 「普通模组服 → 对应的混合端」。纯逻辑在 daemon/convert.go，这里只负责
// 把实例、下载器、审计接起来。
//
// 路由拆成两条：GET 问「能转成什么、代价是什么」，POST 才真转。
// 不做成「前端自己拼一个 type 再调下载接口」—— 那样类型会先于文件被改掉，
// 下载一失败就留下一台自称混合端、启动文件还是 Forge 的实例（见 convert.go 文件头）。

func (s *Server) instanceConvertRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/convert", s.convertInfo)
	mux.HandleFunc("POST /api/instances/{id}/convert", s.convertDo)
}

// instanceRunning 实例是不是正处在「文件被锁着」的状态。
//
// 换启动文件前必须过这一道：Windows 上运行中的 jar 覆盖不了。
// 抽出来是因为下载与转换两条路都要问同一个问题 —— 各写一份的话，
// 早晚会有一处漏掉 Stopping（那时候文件已经可以写了，但进程还没退干净）。
func (s *Server) instanceRunning(id string) bool {
	for _, v := range s.mgr.List() {
		if v.ID != id {
			continue
		}
		return v.Status == daemon.StatusRunning ||
			v.Status == daemon.StatusStarting ||
			v.Status == daemon.StatusStopping
	}
	return false
}

// convertInfo 这个实例现在能转成什么、代价是什么、以及哪些路我们不做。
func (s *Server) convertInfo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	targets := daemon.ConversionTargetsOf(inst.Type)
	for i := range targets {
		targets[i].NewJar = daemon.ConversionNewJarName(inst, targets[i].Type)
		// 实例自己不满足条件时（没记版本、目录空）把可用性降下来，并说清是为什么。
		// 这和 refusals 的区别：那个要等我们改代码，这个用户自己就能解决。
		if why := daemon.ConversionAllowed(inst, targets[i].Type); why != "" {
			targets[i].Available = false
			targets[i].Note = why
		}
	}
	writeJSON(w, 200, map[string]any{
		"current":      inst.Type,
		"currentLabel": daemon.TypeDisplay(inst.Type),
		"mcVersion":    inst.MCVersion,
		"jarPath":      inst.JarPath,
		"running":      s.instanceRunning(id),
		"targets":      targets,
		"refusals":     daemon.ConversionRefusalsOf(inst.Type),
		// 代价写在按钮之前，不是点完再说
		"warning": daemon.ConversionWarning,
	})
}

type convertReq struct {
	Target string `json:"target"`
	Build  int    `json:"build"`
	Tag    string `json:"tag"`
	Mirror string `json:"mirror"`
}

// convertDo 真转。顺序是硬性的：**先把文件下好、校验过，再改实例**。
func (s *Server) convertDo(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inst, ok := s.mgr.Get(id)
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}
	var body convertReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	target := daemon.Type(body.Target)
	// ⚠ 先校验再有副作用。这一条同时挡住三种情况：不认识的目标、跨模组系、
	// 以及「实例自己缺版本」—— 它们的下一步动作完全不同，所以原因由 daemon 给。
	if why := daemon.ConversionAllowed(inst, target); why != "" {
		writeErr(w, 400, why)
		return
	}
	if s.instanceRunning(id) {
		writeErr(w, 409, "实例正在运行，先停服再转换 —— Windows 上运行中的 jar 是锁着的，覆盖不了。"+
			"而且转换后要重启才会生效，跑着换等于换了也不起作用")
		return
	}
	if body.Mirror == "" {
		body.Mirror = s.set.Get().DefaultMirror
	}

	resolve, ok := jarResolvers[fetch.Kind(target)]
	if !ok {
		// 能出现在 targets 里却没有下载实现 —— 说明那两个名单又走散了，说清楚别装傻
		writeErr(w, 500, fmt.Sprintf("内部错误：%s 没有对应的下载实现（这是面板的 bug，麻烦反馈一下）",
			daemon.TypeDisplay(target)))
		return
	}
	build, err := resolve(serverJarReq{
		Type:    string(target),
		Version: inst.MCVersion,
		Build:   body.Build,
		Tag:     body.Tag,
		Mirror:  body.Mirror,
	})
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	if build.URL == "" {
		writeErr(w, 500, "内部错误：挑出来的构建没有下载地址（这是面板的 bug，麻烦反馈一下）")
		return
	}

	dest := daemon.ConversionNewJarPath(inst, target)
	jarName := filepath.Base(dest)
	label := daemon.TypeDisplay(target)

	opts := fetch.Options{
		InstanceID: id,
		Build:      build,
		DestPath:   dest,
		Label:      "转换 → " + label,
		OnDone: func(o fetch.Options, written int64) error {
			// 到这里文件已经落盘且校验过了，才可以动这个实例。
			next := daemon.ConvertedInstance(inst, target)
			if uerr := s.mgr.Update(&next); uerr != nil {
				return fmt.Errorf("文件已经下好了，但实例配置没写成：%w", uerr)
			}
			s.mgr.Log(id, fmt.Sprintf(
				"[EnderBox] 已转换为 %s · 启动文件 %s（%d 字节，校验%s）· "+
					"原启动文件 %s 保留未动，mods/ 与 world/ 原样保留",
				label, jarName, written,
				map[bool]string{true: "通过", false: "未提供"}[build.SHA != ""],
				inst.JarPath))
			s.mgr.AppendAudit(daemon.AuditEntry{
				At:     time.Now().Format(time.RFC3339),
				Inst:   id,
				Op:     "convert.done",
				Target: string(target),
				Args:   map[string]any{"from": string(inst.Type), "jar": jarName, "bytes": written},
				OK:     true,
			})
			return nil
		},
	}
	task, err := s.dl.Start(opts)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	// 发起也记一条：转换是可逆但很重的动作，事后要能回答「谁在什么时候把它换成了什么」。
	// （成功还有一条 convert.done；两条配对看，就能分辨「发起了但下载失败」这种半途。）
	s.mgr.AppendAudit(daemon.AuditEntry{
		At:     time.Now().Format(time.RFC3339),
		Who:    r.RemoteAddr,
		Inst:   id,
		Op:     "convert.request",
		Target: string(target),
		Args:   map[string]any{"from": string(inst.Type), "mc": inst.MCVersion, "build": build.ID},
		OK:     true,
	})
	writeJSON(w, 202, task)
}
