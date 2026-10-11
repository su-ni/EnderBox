package daemon

import (
	"archive/zip"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/su-ni/EnderBox/internal/jobs"
)

// ErrSaveNeedsStop：切换 level-name 必须停服 —— 运行中改它，当前进程不会换世界，
// 只会让「下次启动」的世界跟用户以为的不一样。要分流 HTTP 409，所以它必须是哨兵
// （#30 的教训：按错误分流状态码时，产生处就得包哨兵，别让处理器去猜文案）。
var ErrSaveNeedsStop = errors.New("请先停止服务端再切换存档 —— level-name 只在启动时读一次")

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

// validWorldName 校验「世界名」本身能不能用（不要求目录已存在）。
//
// 与 worldDir 分开是因为导入场景：目标目录还不存在，没法走 worldDir 的 stat/level.dat 检查，
// 但同样得拦住 `..`、绝对路径和 Windows 非法字符。两处口径必须一致，所以 worldDir 也调它。
func validWorldName(world string) (string, error) {
	name := strings.TrimSpace(world)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("存档名不合法")
	}
	// Windows 下这些字符会让 CreateDirectory 直接失败，早点说清楚比让它抛系统错误好。
	if strings.ContainsAny(name, `:*?"<>|`) {
		return "", fmt.Errorf("存档名不能包含 : * ? \" < > | 这些字符")
	}
	if strings.HasSuffix(name, ".") {
		return "", fmt.Errorf("存档名不能以点结尾")
	}
	return name, nil
}

// worldDir 校验并返回某世界目录的绝对路径。
//
// ⚠ 两道防线防目录穿越：世界名来自 URL，可能被塞 `..` / 绝对路径。
// 名里不许出现路径分隔符；解析后必须仍在实例目录内；目录里必须有 level.dat。
func (m *Manager) worldDir(inst Instance, world string) (string, error) {
	name, err := validWorldName(world)
	if err != nil {
		return "", err
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

// SaveImport 一次存档导入的结果。
type SaveImport struct {
	World string `json:"world"`
	Files int    `json:"files"`
	Size  int64  `json:"size"`
}

// dirStats 数一个目录里的文件数与总字节 —— 导入完立刻回报「搬进来多少」。
func dirStats(dir string) (int, int64) {
	var files int
	var size int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		files++
		size += info.Size()
		return nil
	})
	return files, size
}

// soleTopDir 返回「唯一顶层目录」的前缀（如 `myworld/`），否则空串。
//
// ⚠ 与 singleRootPrefix 的关键区别：**不看目录叫什么名字**。
// singleRootPrefix 会把 world/ 当 layoutDirs 而不剥 —— 那是为「整合包并进实例目录」
// 设计的（world/ 就是实例自己的目录）。可用户导出存档时也常把世界套在一个叫 world 的
// 文件夹里，同一套逻辑会漏剥，然后 level.dat 就找不到了。
// 存档导入只按**结构**判：只有一个顶层目录、且没有顶层散文件，才剥。
func soleTopDir(archivePath string) string {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return ""
	}
	defer zr.Close()

	root := ""
	seen := false
	for _, f := range zr.File {
		// ⚠ 只看**有内容的条目**：目录项（含空的 `xxx/` 占位）不参与判断。
		// 打包工具常顺手塞几个空目录项，把它们算进来的话「唯一顶层目录」会判失败，
		// 于是白剥不了一层，用户拿到 world/world/level.dat。
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.Trim(strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "./"), "/")
		if name == "" {
			continue
		}
		i := strings.Index(name, "/")
		if i < 0 {
			return "" // 顶层有散文件 → 剥了会跟文件夹里的内容混在一起
		}
		top := name[:i]
		if !seen {
			root, seen = top, true
		} else if root != top {
			return ""
		}
	}
	if !seen {
		return ""
	}
	return root + "/"
}

// ResolveImportTarget 预检「能不能导到这个名字下」，不动作。
// 供 API 在起作业**之前**拦掉必然失败的请求（重名、非法名），别让用户等一个白跑的作业。
func (m *Manager) ResolveImportTarget(id, world string) (string, error) {
	inst, ok := m.Get(id)
	if !ok {
		return "", fmt.Errorf("实例不存在: %s", id)
	}
	name, err := validWorldName(world)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, name)); err == nil {
		return "", fmt.Errorf("已经有叫「%s」的存档了 —— 换个名字，或者先把旧的删掉", name)
	}
	return name, nil
}

// ImportSave 把一个上传的存档 zip 解压成实例目录下的一个新世界。
//
// 全程先解到 `<名字>.enderbox-import.tmp` 再 rename 转正：失败一律清干净，
// 绝不在用户的世界列表里留半个「看着像导入成功、实际缺 level.dat」的目录。
func (m *Manager) ImportSave(id, world, zipPath string, onProgress jobs.ProgressFunc) (SaveImport, error) {
	name, err := m.ResolveImportTarget(id, world)
	if err != nil {
		return SaveImport{}, err
	}
	inst, _ := m.Get(id)
	dest := filepath.Join(inst.Dir, name)
	tmp := dest + ".enderbox-import.tmp"

	_ = os.RemoveAll(tmp) // 上一轮崩在中途可能留了残骸，先扫掉
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return SaveImport{}, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(tmp)
		}
	}()

	if err := extractZipToProgress(zipPath, tmp, soleTopDir(zipPath), onProgress); err != nil {
		return SaveImport{}, err
	}
	if _, err := os.Stat(filepath.Join(tmp, "level.dat")); err != nil {
		return SaveImport{}, fmt.Errorf("这个包里没有 level.dat —— 看着不像一个世界存档（要打包世界目录本身，不是它的上一层）")
	}
	if err := os.Rename(tmp, dest); err != nil {
		return SaveImport{}, err
	}
	ok = true
	files, size := dirStats(dest)
	return SaveImport{World: name, Files: files, Size: size}, nil
}

// ActivateSave 把 server.properties 的 level-name 切到指定世界。
//
// 必须停服：Minecraft 只在启动时读一次 level-name，运行中改它不会让当前进程换世界。
// 目标世界必须真的存在（含 level.dat），否则下次开服会生成一个空的新世界 —— 用户会
// 以为「我的存档被换了」。
func (m *Manager) ActivateSave(id, world string) error {
	inst, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}
	if p, ok := m.proc(id); ok && p.Status() == StatusRunning {
		return ErrSaveNeedsStop
	}
	name, err := validWorldName(world)
	if err != nil {
		return err
	}
	if _, err := m.worldDir(inst, name); err != nil {
		return err
	}
	return m.WriteProperties(id, map[string]string{"level-name": name})
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
