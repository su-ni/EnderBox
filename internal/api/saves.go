package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 存档（world）相关路由。
//
// 「一个世界」的判据是**目录里有 level.dat**，不是目录叫 world ——
// level-name 用户可改，老版本还有 world_nether / world_the_end 与世界平级；
// 名字完全不可靠（备份的「只存存档」也是靠这个名字过滤，那里能行是因为先读了 level-name）。
// 有 level.dat 才是真的世界，这条对 vanilla / Paper / Forge 都成立。
func (s *Server) savesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/saves", s.savesList)
}

// saveInfo 存档列表里的一条。
type saveInfo struct {
	// Name 目录名（世界的物理标识）。改路径要按它，别按 level-name。
	Name string `json:"name"`
	// Active 是不是当前生效的那个（server.properties 里 level-name 指的那个）。
	Active bool `json:"active"`
	// Size / Files 给用户判断「这个存档多大、要不要搬」用；取不到时是 0，不编。
	Size  int64 `json:"size"`
	Files int   `json:"files"`
}

// savesList 列出实例目录里所有世界目录。
//
// 只读、不动盘 —— 上传/下载/切换是后续动作，这一步先把「有哪些世界、哪个在用」说清楚。
func (s *Server) savesList(w http.ResponseWriter, r *http.Request) {
	inst, ok := s.mgr.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "实例不存在")
		return
	}

	// 当前生效的世界名：读 level-name，与 daemon 里「只存存档」的口径一致。
	active := "world"
	if rows, err := s.mgr.ReadProperties(inst.ID); err == nil {
		for _, row := range rows {
			if row.Key == "level-name" {
				if v := strings.TrimSpace(row.Value); v != "" {
					active = v
				}
			}
		}
	}

	entries, err := os.ReadDir(inst.Dir)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	// ⚠ 恒返回 []，不是 null：前端直接读 .length，nil 切片序列化成 null 会让它崩。
	items := make([]saveInfo, 0)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(inst.Dir, e.Name())
		if _, err := os.Stat(filepath.Join(dir, "level.dat")); err != nil {
			continue // 没有 level.dat —— 不是世界，跳过（logs/ config/ 这类目录会在这里被滤掉）
		}
		var size int64
		var files int
		_ = filepath.Walk(dir, func(_ string, info os.FileInfo, werr error) error {
			if werr != nil || info == nil || info.IsDir() {
				return nil
			}
			size += info.Size()
			files++
			return nil
		})
		items = append(items, saveInfo{
			Name:   e.Name(),
			Active: strings.EqualFold(e.Name(), active),
			Size:   size,
			Files:  files,
		})
	}

	writeJSON(w, 200, map[string]any{"items": items, "active": active})
}
