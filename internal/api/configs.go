package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
)

// 通用配置文件编辑器：列出 / 读 / 存 / 还原。
// 这一组和「配置」页的 properties 表单是两条路 ——
// properties 走表单（有 schema、有范围校验），其余配置文件走这个通用编辑器。

func (s *Server) configRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/configs", s.listConfigs)
	mux.HandleFunc("GET /api/instances/{id}/configs/file", s.readConfigFile)
	mux.HandleFunc("PUT /api/instances/{id}/configs/file", s.writeConfigFile)
	mux.HandleFunc("POST /api/instances/{id}/configs/restore", s.restoreConfigFile)
}

func (s *Server) listConfigs(w http.ResponseWriter, r *http.Request) {
	list, err := s.mgr.ListConfigs(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) readConfigFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	c, err := s.mgr.ReadConfig(r.PathValue("id"), rel)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, c)
}

type writeConfigReq struct {
	Path     string    `json:"path"`
	Text     string    `json:"text"`
	EOL      string    `json:"eol"`
	ModTime  time.Time `json:"modTime"`
	Validate *bool     `json:"validate"` // 默认 true；false 表示「我知道有问题，就是要存」
}

// writeConfigFile 保存。
//
// 结构校验不通过的**默认拒绝**：手改 YAML 少一个缩进，服务端启动时才炸，
// 而报错藏在几十万行日志里 —— 那种错让人试一整晚。想看原文可以显式带 validate=false。
func (s *Server) writeConfigFile(w http.ResponseWriter, r *http.Request) {
	var req writeConfigReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	id := r.PathValue("id")

	if req.Validate == nil || *req.Validate {
		// 用后端读出来的语言判定，别信前端传的扩展名 —— 校验方式只能有一个来源
		cur, err := s.mgr.ReadConfig(id, req.Path)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		if issues := daemon.ValidateConfigText(cur.Lang, req.Text); len(issues) > 0 {
			writeJSON(w, 422, map[string]any{
				"error":  "这份配置有结构问题，没有写入。确认要保留原样可以勾上「仍然保存」。",
				"issues": issues,
			})
			return
		}
	}

	if err := s.mgr.WriteConfig(id, req.Path, req.Text, req.EOL, req.ModTime); err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) restoreConfigFile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.RestoreConfig(r.PathValue("id"), req.Path); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
