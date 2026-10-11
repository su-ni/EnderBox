package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/jobs"
)

// 存档（world）相关路由。
//
// 「一个世界」的判据是**目录里有 level.dat**，不是目录叫 world ——
// level-name 用户可改，老版本还有 world_nether / world_the_end 与世界平级；
// 名字完全不可靠（备份的「只存存档」也是靠这个名字过滤，那里能行是因为先读了 level-name）。
// 有 level.dat 才是真的世界，这条对 vanilla / Paper / Forge 都成立。
func (s *Server) savesRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instances/{id}/saves", s.savesList)
	mux.HandleFunc("POST /api/instances/{id}/saves/import", s.saveImport)
	mux.HandleFunc("POST /api/instances/{id}/saves/{name}/export", s.saveExport)
	mux.HandleFunc("POST /api/instances/{id}/saves/{name}/activate", s.saveActivate)
	mux.HandleFunc("GET /api/instances/{id}/saves/download/{file}", s.saveDownload)
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

// saveExport 把一个存档打包成 zip。分钟级操作 → 起统一作业，立刻回 202 + jobId，
// 前端轮询 /api/jobs/{id} 拿进度（气泡自动就有，因为 kind 走的是同一张表）。
//
// 校验放在起作业**之前**：存档不存在/名字不合法这类「现在就能判定」的错，
// 当场回 404/400 比让用户盯着一个必然失败的作业强。
func (s *Server) saveExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	world := r.PathValue("name")
	if err := s.mgr.ResolveSave(id, world); err != nil {
		// 实例没了 → 404；存档名不合法 / 不是有效存档 → 400（换个存档就好）
		msg := err.Error()
		switch {
		case strings.Contains(msg, "实例不存在"):
			writeErr(w, 404, msg)
		case strings.Contains(msg, "不合法") || strings.Contains(msg, "没有这个存档") || strings.Contains(msg, "不是有效的存档"):
			writeErr(w, 400, msg)
		default:
			writeErr(w, 500, msg)
		}
		return
	}

	job := s.jobStore.New(id, "save-export")
	if job == nil {
		writeErrReason(w, 409, "busy", "该实例已有一笔作业在跑（备份/还原/导出…），等它结束再试")
		return
	}
	go func() {
		s.jobStore.SetStage(job, "zip")
		cb := func(fd, ft int, bd, bt int64, cur string) {
			s.jobStore.UpdateProgress(job, fd, ft, bd, bt, cur)
		}
		res, err := s.mgr.ExportSave(id, world, cb)
		if err != nil {
			s.jobStore.Finish(job, jobs.StateFailed, nil, err.Error())
			return
		}
		s.jobStore.Finish(job, jobs.StateDone, map[string]any{
			"world": res.World,
			"name":  res.Name,
			"size":  res.Size,
		}, "")
	}()
	writeJSON(w, 202, map[string]any{"jobId": job.ID})
}

// saveImport 收一个上传的存档 zip，解成实例目录下的一个新世界。
//
// 上传本身走 HTTP（前端拿 XHR 进度），解压是分钟级 → 落到临时文件后起统一作业，
// 回 202 + jobId，进度自然进右下角气泡（同一张作业表）。
// 名字非法 / 重名在起作业**之前**就拦掉 —— 别让用户等一个必然失败的作业。
func (s *Server) saveImport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, 400, "上传解析失败: "+err.Error())
		return
	}
	world := strings.TrimSpace(r.FormValue("name"))
	if _, err := s.mgr.ResolveImportTarget(id, world); err != nil {
		code := 400
		if strings.Contains(err.Error(), "实例不存在") {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, 400, "缺少文件字段 file: "+err.Error())
		return
	}
	defer f.Close()
	if !strings.HasSuffix(strings.ToLower(hdr.Filename), ".zip") {
		writeErr(w, 400, "存档要先打包成 .zip 再上传")
		return
	}

	// 先抢作业闸、再落盘：闸被占着（同类操作在跑）时当场回 409，
	// 省得把几十 MB 白写一遍临时文件再丢掉。抢到之后的失败都要把作业收尾，
	// 否则它会挂着一个永远不动的「运行中」，把这个实例的闸一直占死。
	job := s.jobStore.New(id, "save-import")
	if job == nil {
		writeErrReason(w, 409, "busy", "该实例已有一笔作业在跑（备份/还原/导出…），等它结束再试")
		return
	}

	// 落到系统临时文件：作业在后台跑，不能攥着 HTTP 的 multipart reader 不放。
	tmp, err := os.CreateTemp("", "sbx-save-import-*.zip")
	if err != nil {
		s.jobStore.Finish(job, jobs.StateFailed, nil, err.Error())
		writeErr(w, 500, err.Error())
		return
	}
	tmpPath := tmp.Name()
	if _, err := io.Copy(tmp, f); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		s.jobStore.Finish(job, jobs.StateFailed, nil, err.Error())
		writeErr(w, 400, "上传中断："+err.Error())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		s.jobStore.Finish(job, jobs.StateFailed, nil, err.Error())
		writeErr(w, 500, err.Error())
		return
	}

	go func() {
		defer os.Remove(tmpPath)
		s.jobStore.SetStage(job, "unzip")
		cb := func(fd, ft int, bd, bt int64, cur string) {
			s.jobStore.UpdateProgress(job, fd, ft, bd, bt, cur)
		}
		res, err := s.mgr.ImportSave(id, world, tmpPath, cb)
		if err != nil {
			s.jobStore.Finish(job, jobs.StateFailed, nil, err.Error())
			return
		}
		s.jobStore.Finish(job, jobs.StateDone, map[string]any{
			"world": res.World,
			"files": res.Files,
			"size":  res.Size,
		}, "")
	}()
	writeJSON(w, 202, map[string]any{"jobId": job.ID})
}

// saveActivate 切换当前生效的世界（改 server.properties 的 level-name）。
// 运行中拒绝（409 running）—— level-name 只有启动时读一次，热改无意义还会误导用户。
func (s *Server) saveActivate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	world := r.PathValue("name")
	if err := s.mgr.ActivateSave(id, world); err != nil {
		switch {
		case errors.Is(err, daemon.ErrSaveNeedsStop):
			writeErrReason(w, 409, "running", err.Error())
		case strings.Contains(err.Error(), "实例不存在"):
			writeErr(w, 404, err.Error())
		default:
			writeErr(w, 400, err.Error())
		}
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "active": world})
}

// saveDownload 把已导出的 zip 发给浏览器。
// ⚠ 路径只从「数据目录 + basename」拼出来，绝不让 URL 直接当路径（防目录穿越）。
func (s *Server) saveDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	file := r.PathValue("file")
	path, err := s.mgr.SaveArchivePath(id, file)
	if err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	base := filepath.Base(path)
	// 文件名可能是中文（世界名）：用 RFC 5987 的 filename* 兜底，老浏览器退回 ASCII 那份。
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", base, url.PathEscape(base)))
	http.ServeFile(w, r, path)
}
