package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/su-ni/EnderBox/internal/daemon"
)

// 导入已有服务器 + 目录浏览。
// 目录浏览是「让用户选一个已经在跑的目录」的前提：让他手打一整条路径不现实，
// 手机上也打不出来。只列目录名，不读文件内容。

func (s *Server) importRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/fs/dirs", s.fsDirs)
	mux.HandleFunc("POST /api/import/scan", s.importScan)
	mux.HandleFunc("POST /api/import", s.doImport)
}

type dirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	// LooksLikeServer 给界面做提示用：像服务端根目录的先标出来
	LooksLikeServer bool `json:"looksLikeServer"`
}

func (s *Server) fsDirs(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("path"))
	if raw == "" {
		// 没给路径 = 从「这台机器有哪些盘」开始
		writeJSON(w, 200, map[string]any{"path": "", "parent": "", "dirs": rootDrives()})
		return
	}
	path := filepath.Clean(raw)
	entries, err := os.ReadDir(path)
	if err != nil {
		writeErr(w, 400, "打不开这个目录："+err.Error())
		return
	}
	dirs := make([]dirEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue // 隐藏目录不列，减少噪音
		}
		full := filepath.Join(path, n)
		dirs = append(dirs, dirEntry{Name: n, Path: full, LooksLikeServer: looksLikeServerDir(full)})
	}
	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name) < strings.ToLower(dirs[j].Name)
	})
	parent := filepath.Dir(path)
	if parent == path {
		parent = ""
	}
	writeJSON(w, 200, map[string]any{"path": path, "parent": parent, "dirs": dirs})
}

// rootDrives Windows 上没给路径时列出可用盘符；其它系统从根目录开始。
func rootDrives() []dirEntry {
	if filepath.Separator != '\\' {
		return []dirEntry{{Name: "/", Path: "/"}}
	}
	out := []dirEntry{}
	for c := 'A'; c <= 'Z'; c++ {
		p := string(c) + ":\\"
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			out = append(out, dirEntry{Name: string(c) + ":", Path: p})
		}
	}
	return out
}

// looksLikeServerDir 一个目录像不像服务端根目录（有服务端 jar / 世界 / 配置 / 启动脚本）。
// 只看第一层，不递归。
func looksLikeServerDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if e.IsDir() {
			if n == "world" || n == "worlds" || n == "libraries" {
				return true
			}
			continue
		}
		if n == "server.properties" || n == "eula.txt" || n == "server.jar" || n == "user_jvm_args.txt" {
			return true
		}
		if (strings.HasSuffix(n, ".bat") || strings.HasSuffix(n, ".sh")) && strings.Contains(n, "start") {
			return true
		}
		if strings.HasSuffix(n, ".jar") && !strings.HasSuffix(n, "-installer.jar") {
			return true
		}
	}
	return false
}

func (s *Server) importScan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Dir) == "" {
		writeErr(w, 400, "缺少 dir")
		return
	}
	sc, err := daemon.DetectImport(body.Dir)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, sc)
}

func (s *Server) doImport(w http.ResponseWriter, r *http.Request) {
	var req daemon.ImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "请求体解析失败: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Dir) == "" {
		writeErr(w, 400, "缺少 dir")
		return
	}
	inst, err := s.mgr.Import(req)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{
		"instance":  inst,
		"preflight": s.mgr.Preflight(inst.ID),
	})
}
