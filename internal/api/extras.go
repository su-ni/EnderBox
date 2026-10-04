package api

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// 这些路由服务于「插件 / 备份 / 设置」三页。
// 单独放一个文件是为了不让 server.go 变成几百行的杂物抽屉。

func (s *Server) pluginRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/plugins", s.pluginList)
	mux.HandleFunc("POST /api/instances/{id}/plugins", s.pluginUpload)
	mux.HandleFunc("PATCH /api/instances/{id}/plugins/{file}", s.pluginToggle)
	mux.HandleFunc("DELETE /api/instances/{id}/plugins/{file}", s.pluginDelete)
}

func (s *Server) backupRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/backups", s.backupList)
	mux.HandleFunc("POST /api/instances/{id}/backups", s.backupCreate)
	mux.HandleFunc("POST /api/instances/{id}/backups/{file}/restore", s.backupRestore)
	mux.HandleFunc("DELETE /api/instances/{id}/backups/{file}", s.backupDelete)
}

func (s *Server) systemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/java", s.javaList)
	mux.HandleFunc("GET /api/system", s.systemInfo)
}

func (s *Server) pluginList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := s.mgr.ListPlugins(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	dir, _ := s.mgr.ContentDirPath(id)
	writeJSON(w, 200, map[string]any{"dir": dir, "items": list})
}

func (s *Server) pluginUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// 32MB 以内走内存，更大的落临时文件 —— 模组包动辄几十 MB
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "上传解析失败: "+err.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少文件字段 file: "+err.Error())
		return
	}
	defer f.Close()

	name, err := s.mgr.SavePlugin(id, hdr.Filename, f)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "file": name})
}

func (s *Server) pluginToggle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if err := s.mgr.SetPluginEnabled(r.PathValue("id"), r.PathValue("file"), body.Enabled); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) pluginDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.DeletePlugin(r.PathValue("id"), r.PathValue("file")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) backupList(w http.ResponseWriter, r *http.Request) {
	list, err := s.mgr.ListBackups(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"items": list})
}

func (s *Server) backupCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	b, err := s.mgr.CreateBackup(r.PathValue("id"), body.Label)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 201, b)
}

func (s *Server) backupRestore(w http.ResponseWriter, r *http.Request) {
	safety, err := s.mgr.RestoreBackup(r.PathValue("id"), r.PathValue("file"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// 返回还原前那份安全备份的文件名，界面要告诉用户「后悔药在这」
	writeJSON(w, 200, map[string]any{"ok": true, "safety": safety})
}

func (s *Server) backupDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.mgr.DeleteBackup(r.PathValue("id"), r.PathValue("file")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) javaList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"items": s.mgr.JavaRuntimes()})
}

var startedAt = time.Now()

func (s *Server) systemInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"version":   version,
		"goVersion": strings.TrimPrefix(runtime.Version(), "go"),
		"goos":      runtime.GOOS,
		"goarch":    runtime.GOARCH,
		"pid":       os.Getpid(),
		"startedAt": startedAt,
		"uptime":    strconv.FormatInt(int64(time.Since(startedAt).Seconds()), 10),
		"dataDir":   s.mgr.BaseDir(),
	})
}

// 版本串。构建时可覆盖：go build -ldflags "-X github.com/su-ni/ServerBox/internal/api.version=1.2.0"
var version = "0.2.0-dev"
