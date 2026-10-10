package daemon

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/su-ni/EnderBox/internal/jobs"
)

// SaveArchive 一次存档导出的产物。
//
// 与「备份」的区别说清楚，免得后来者混用：备份按**范围**打包（整目录 / 多个世界），
// 是「回到过去某个时间点」用的；这里是**单个世界**，是「把这个存档搬去别处」用的。
// 两者都走 zipDir，但语义、落脚目录、命名都不同，不要合并。
type SaveArchive struct {
	World string `json:"world"`
	Name  string `json:"name"` // 建议给用户的文件名（下载接口按它取）
	Size  int64  `json:"size"`
	// File 是磁盘绝对路径，只给后端内部用，不进 JSON（别把本机路径漏给前端）。
	File string `json:"-"`
}

// exportsDir 存档导出的落脚目录。**刻意与 backups 分开** ——
// 混在一起会被 ListBackups 当成一份「备份」（它按 .zip 后缀认），用户会看到一堆
// 自己没存过的「备份」，还以为存错了。
func (m *Manager) exportsDir(id string) string {
	return filepath.Join(m.baseDir, "exports", id)
}

// worldDir 校验并返回某世界目录的绝对路径。
//
// ⚠ 两道防线防目录穿越：世界名来自 URL，可能被塞 `..` / 绝对路径。
// 名里不许出现路径分隔符；解析后必须仍在实例目录内；目录里必须有 level.dat。
func (m *Manager) worldDir(inst Instance, world string) (string, error) {
	name := strings.TrimSpace(world)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("存档名不合法")
	}
	dir := filepath.Join(inst.Dir, name)
	rel, err := filepath.Rel(inst.Dir, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("存档名不合法")
	}
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return "", fmt.Errorf("没有这个存档：%s", name)
	}
	if _, err := os.Stat(filepath.Join(dir, "level.dat")); err != nil {
		return "", fmt.Errorf("%s 不是有效的存档（目录里没有 level.dat）", name)
	}
	return dir, nil
}

// ResolveSave 只做校验，不打包 —— 供上层在起作业前先确认「这个存档真的存在」。
func (m *Manager) ResolveSave(id, world string) error {
	inst, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}
	_, err := m.worldDir(inst, world)
	return err
}

// ExportSave 把一个存档目录打成 zip。进度走 onProgress（由调用方接到统一作业表上）。
func (m *Manager) ExportSave(id, world string, onProgress jobs.ProgressFunc) (SaveArchive, error) {
	inst, ok := m.Get(id)
	if !ok {
		return SaveArchive{}, fmt.Errorf("实例不存在: %s", id)
	}
	src, err := m.worldDir(inst, world)
	if err != nil {
		return SaveArchive{}, err
	}

	// 运行中：先让服务端刷盘再打包，否则会打到半个区块（与备份同一条理由，别省）。
	if p, ok := m.proc(id); ok && p.Status() == StatusRunning {
		_ = p.Send("save-off")
		_ = p.Send("save-all flush")
		time.Sleep(3 * time.Second)
		defer func() { _ = p.Send("save-on") }()
	}

	outDir := m.exportsDir(inst.ID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return SaveArchive{}, err
	}
	name := fmt.Sprintf("%s-%s.zip", sanitizeLabel(world), time.Now().Format("20060102-150405"))
	dest := filepath.Join(outDir, name)

	f, err := os.Create(dest)
	if err != nil {
		return SaveArchive{}, err
	}
	zw := zip.NewWriter(f)
	// keepTop = nil：单个世界整棵收下（世界目录里没有 logs，skipDirs 在这里不生效也无妨）。
	err = zipDir(zw, src, "", nil, onProgress)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return SaveArchive{}, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return SaveArchive{}, err
	}
	return SaveArchive{World: world, Name: name, Size: info.Size(), File: dest}, nil
}

// SaveArchivePath 把下载接口给的「文件名」解析成磁盘路径。
// ⚠ 只取 basename 再拼目录：绝不能让 URL 里的东西直接当路径用。
func (m *Manager) SaveArchivePath(id, file string) (string, error) {
	base := filepath.Base(strings.TrimSpace(file))
	if base == "" || base == "." || base == ".." {
		return "", fmt.Errorf("文件名不合法")
	}
	p := filepath.Join(m.exportsDir(id), base)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("导出文件不存在（可能还没导出，或已被清理）")
	}
	return p, nil
}
