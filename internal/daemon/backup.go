package daemon

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupInfo 一份备份文件
type BackupInfo struct {
	File string    `json:"file"`
	Kind string    `json:"kind"` // manual | pre-restore
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

// 备份里不收录的目录：日志又大又没价值，收进去只会让备份体积翻好几倍
var skipDirs = map[string]bool{"logs": true}

func (m *Manager) backupsDir(id string) string {
	return filepath.Join(m.baseDir, "backups", id)
}

func (m *Manager) ListBackups(id string) ([]BackupInfo, error) {
	dir := m.backupsDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]BackupInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := "manual"
		if strings.HasPrefix(e.Name(), "pre-restore-") {
			kind = "pre-restore"
		}
		out = append(out, BackupInfo{
			File: e.Name(),
			Kind: kind,
			Size: info.Size(),
			Time: info.ModTime(),
		})
	}
	// 新的排前面：找「最近那份」是最高频操作
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// sanitizeLabel 把用户填的备注变成能当文件名的东西
func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	repl := strings.NewReplacer(" ", "_", "/", "-", `\`, "-", ":", "-", "*", "-", "?", "-", `"`, "", "<", "", ">", "", "|", "-")
	s = repl.Replace(s)
	r := []rune(s)
	if len(r) > 24 {
		s = string(r[:24])
	}
	return s
}

// zipDir 把 src 目录递归打包进 zw。rel 用来记录相对路径，保证解压后层级不变。
func zipDir(zw *zip.Writer, src, rel string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			if rel == "" && skipDirs[name] {
				continue
			}
			if err := zipDir(zw, filepath.Join(src, name), filepath.Join(rel, name)); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(rel, name)
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(p)
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(src, name))
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// CreateBackup 打包实例目录。实例在跑就先让服务器把地图刷盘再打包 ——
// 不刷盘会拿到半个区块，这是 MC 备份最经典的坑。
func (m *Manager) CreateBackup(id, label string) (BackupInfo, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return BackupInfo{}, fmt.Errorf("实例不存在: %s", id)
	}
	if err := os.MkdirAll(m.backupsDir(id), 0o755); err != nil {
		return BackupInfo{}, err
	}

	kind := "manual"
	prefix := sanitizeLabel(label)
	if prefix == "" {
		prefix = sanitizeLabel(inst.Name)
	}
	if prefix == "" {
		prefix = "backup"
	}

	// 运行中：先关自动存档 → 强制刷盘 → 等它落盘 → 再打包 → 恢复自动存档
	running := false
	if p, ok := m.procs[id]; ok && p.Status() == StatusRunning {
		running = true
		_ = p.Send("save-off")
		_ = p.Send("save-all flush")
		time.Sleep(3 * time.Second)
		defer func() { _ = p.Send("save-on") }()
	}
	_ = running

	name := fmt.Sprintf("%s-%s.zip", prefix, time.Now().Format("20060102-150405"))
	dest := filepath.Join(m.backupsDir(id), name)

	f, err := os.Create(dest)
	if err != nil {
		return BackupInfo{}, err
	}
	zw := zip.NewWriter(f)
	err = zipDir(zw, inst.Dir, "")
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return BackupInfo{}, err
	}

	info, err := os.Stat(dest)
	if err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{File: name, Kind: kind, Size: info.Size(), Time: info.ModTime()}, nil
}

func (m *Manager) DeleteBackup(id, file string) error {
	base := filepath.Base(file)
	if base != file || strings.ContainsAny(base, `/\`) || !strings.HasSuffix(strings.ToLower(base), ".zip") {
		return fmt.Errorf("非法备份文件名: %s", file)
	}
	return os.Remove(filepath.Join(m.backupsDir(id), base))
}

// RestoreBackup 还原：停服 → 先给当前状态存一份 pre-restore → 清空实例目录 → 解压覆盖 → 之前在跑就重新开
func (m *Manager) RestoreBackup(id, file string) (string, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", fmt.Errorf("实例不存在: %s", id)
	}
	base := filepath.Base(file)
	if base != file || strings.ContainsAny(base, `/\`) || !strings.HasSuffix(strings.ToLower(base), ".zip") {
		return "", fmt.Errorf("非法备份文件名: %s", file)
	}
	src := filepath.Join(m.backupsDir(id), base)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("备份文件不存在: %s", base)
	}

	wasRunning := false
	if p, ok := m.procs[id]; ok && p.Status() == StatusRunning {
		wasRunning = true
		_ = m.Stop(id, 60*time.Second)
	}

	// 安全网：把「还原前」的现场单独存一份，后悔了还能回来
	safety, err := m.CreateBackup(id, "pre-restore")
	if err != nil {
		return "", fmt.Errorf("还原前备份失败，已中止: %w", err)
	}

	// 清空实例目录（保留目录本身），再解压
	entries, err := os.ReadDir(inst.Dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(inst.Dir, e.Name())); err != nil {
			return "", err
		}
	}

	zr, err := zip.OpenReader(src)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, f := range zr.File {
		// 二次防线：zip 里的路径也可能带 ..，解压前必须校验
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		target := filepath.Join(inst.Dir, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		out, err := os.Create(target)
		if err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return "", err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return "", err
		}
	}

	if wasRunning {
		if err := m.Start(id, inst.JavaPath); err != nil {
			return safety.File, fmt.Errorf("已还原，但重新启动失败: %w", err)
		}
	}
	return safety.File, nil
}
