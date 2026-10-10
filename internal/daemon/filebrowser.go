package daemon

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 文件管理器。
//
// 为什么需要它：模组 / 插件只是实例目录里的一部分 —— 改配置要进 config/，
// 装整合包要解开 zip，删掉一个捣乱的目录…… 这些以前都只能让用户自己去开资源管理器。
//
// 安全边界（不是可选项）：所有路径都相对**实例目录**解析，且必须落在实例目录之内。
// 一个 `../../` 就足以把面板变成「任意文件读写器」。面板是有鉴权的本地服务，
// 但它不该比它守护的东西更危险。

const maxTextBytes = 1 << 20 // 1 MB：再大就不适合在面板里编辑了

// 当作文本对待的扩展名。判不出来也没关系 —— 读取接口还会再做一次二进制检查，
// 这里只是决定列表里给不给「打开」这个动作。
var textExts = map[string]bool{
	".txt": true, ".yml": true, ".yaml": true, ".json": true, ".toml": true,
	".properties": true, ".conf": true, ".cfg": true, ".ini": true, ".md": true,
	".log": true, ".sh": true, ".bat": true, ".csv": true, ".xml": true,
	".mcmeta": true, ".html": true, ".env": true, ".js": true, ".ts": true,
}

// 文件管理里的三种「用户能看懂的错误」，API 层据此分派状态码。
var (
	ErrPathNotFound = errors.New("找不到这个文件")
	// ErrPathExists 目标位置已经有东西了。新建/移动/复制撞上同名都用它 ——
	// 单独一个错误类型，API 层才能回 409（冲突）而不是笼统的 400，
	// 前端也才好据此提示「换个名字」而不是只甩一句「操作失败」。
	ErrPathExists = errors.New("这里已经有同名的东西了")
	ErrNotText    = errors.New("这不是文本文件")
	ErrTooLarge   = errors.New("文件太大")
)

// FileEntry 列表里的一行
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"` // 相对实例目录，始终用 / 分隔
	IsDir   bool      `json:"isDir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
	Text    bool      `json:"text"` // 可以当文本打开
	Zip     bool      `json:"zip"`  // 可以解压
}

// FileListing 一个目录的列表结果
type FileListing struct {
	Path    string      `json:"path"`   // 当前目录（相对实例目录，空 = 根）
	Parent  string      `json:"parent"` // 上一级，根目录时为空
	Root    string      `json:"root"`   // 实例目录的绝对路径，界面顶部要显示
	Entries []FileEntry `json:"entries"`
}

func isTextName(name string) bool {
	return textExts[strings.ToLower(filepath.Ext(name))]
}

func isZipName(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".zip")
}

// resolvePath 把相对路径解析成实例目录下的绝对路径，顺手做完越界检查。
// 返回的第二个值是规范化后的相对路径（统一 / 分隔），用于对外回显。
func (m *Manager) resolvePath(id, rel string) (abs string, clean string, err error) {
	root, _, err := m.instanceDir(id)
	if err != nil {
		return "", "", err
	}
	clean = filepath.Clean(filepath.FromSlash(strings.TrimSpace(rel)))
	if clean == "." || clean == string(filepath.Separator) {
		clean = ""
	}
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("只支持实例目录内的相对路径")
	}
	abs = filepath.Join(root, clean)
	r, err := filepath.Rel(root, abs)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("路径越界，已拒绝")
	}
	return abs, filepath.ToSlash(clean), nil
}

// instanceDir 取实例目录；实例不存在时返回 ErrInstanceNotFound。
func (m *Manager) instanceDir(id string) (string, Instance, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", Instance{}, ErrInstanceNotFound
	}
	return inst.Dir, inst, nil
}

// ListFiles 列目录。rel 为空表示实例根目录。
func (m *Manager) ListFiles(id, rel string) (FileListing, error) {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return FileListing{}, err
	}
	root, _, _ := m.instanceDir(id)
	info, err := os.Stat(abs)
	if err != nil {
		return FileListing{}, fmt.Errorf("%w：%s", ErrPathNotFound, clean)
	}
	if !info.IsDir() {
		return FileListing{}, fmt.Errorf("这不是一个目录：%s", clean)
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return FileListing{}, err
	}
	out := make([]FileEntry, 0, len(des))
	for _, e := range des {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		p := name
		if clean != "" {
			p = clean + "/" + name
		}
		out = append(out, FileEntry{
			Name:    name,
			Path:    p,
			IsDir:   e.IsDir(),
			Size:    fi.Size(),
			ModTime: fi.ModTime(),
			Text:    !e.IsDir() && (isTextName(name) || fi.Size() == 0),
			Zip:     !e.IsDir() && isZipName(name),
		})
	}
	// 目录在前、同类按名字排（忽略大小写）—— 和用户习惯的资源管理器一致，
	// 换个界面就该换个找东西的方式，是没必要的负担。
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	parent := ""
	if clean != "" {
		if p := filepath.ToSlash(filepath.Dir(clean)); p != "." {
			parent = p
		}
	}
	return FileListing{Path: clean, Parent: parent, Root: root, Entries: out}, nil
}

// ReadTextFile 读一个文本文件。超过 1 MB 或含 NUL 字节的一律拒绝 ——
// 把二进制读成字符串再让用户「编辑」，是制造文件损坏的最快方式。
func (m *Manager) ReadTextFile(id, rel string) (string, error) {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", fmt.Errorf("要读的是文件，不是实例根目录")
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", fmt.Errorf("%w：%s", ErrPathNotFound, clean)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("这是一个目录：%s", clean)
	}
	if info.Size() > maxTextBytes {
		return "", fmt.Errorf("%w（%s 有 %d 字节，上限 1 MB）", ErrTooLarge, clean, info.Size())
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTextBytes+1))
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", fmt.Errorf("%w：%s 看起来是二进制", ErrNotText, clean)
	}
	return string(data), nil
}

// WriteTextFile 覆盖写一个文本文件。原文件不存在时按 0644 新建。
func (m *Manager) WriteTextFile(id, rel, content string) error {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("不能写实例根目录")
	}
	if len(content) > maxTextBytes {
		return fmt.Errorf("%w：内容超过 1 MB", ErrTooLarge)
	}
	mode := os.FileMode(0o644)
	if st, serr := os.Stat(abs); serr == nil {
		if st.IsDir() {
			return fmt.Errorf("这是一个目录：%s", clean)
		}
		mode = st.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), mode)
}

// DeletePath 删除文件或整个目录。根目录永远删不得。
func (m *Manager) DeletePath(id, rel string) error {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return err
	}
	if clean == "" {
		return fmt.Errorf("不能删除实例根目录")
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("%w：%s", ErrPathNotFound, clean)
	}
	return os.RemoveAll(abs)
}

// ResolveFilePath 给「下载文件」这类需要真实路径的场合用 —— 越界检查在这里做完，
// 调用方拿到的路径保证落在实例目录内。
//
// 为什么单独开一个口子而不是把 resolvePath 导出：resolvePath 返回的是「规范化后的相对路径 + 绝对路径」，
// 而下载只需要绝对路径，多给一个返回值就多一处误用（把相对路径当绝对路径打开）。
func (m *Manager) ResolveFilePath(id, rel string) (string, error) {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", fmt.Errorf("不能下载整个实例目录")
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%w：%s", ErrPathNotFound, clean)
	}
	if fi.IsDir() {
		return "", fmt.Errorf("这是个目录 —— 目录下载要打包成 zip，这个功能还没做")
	}
	return abs, nil
}

// ── 批量删除 ────────────────────────────────────────────

// PathError 一个路径操作没做成。批量操作必须能说出「哪一条、为什么」——
// 只回「失败 3 条」等于让用户自己猜是哪 3 条。
type PathError struct {
	Path string `json:"path"`
	Err  string `json:"err"`
}

type BatchDeleteResult struct {
	OK     []string    `json:"ok"`
	Failed []PathError `json:"failed"`
}

// DeletePaths 批量删除。**一条失败不影响其它条** —— 用户勾了 20 个文件，
// 第 3 个正被占用不该让剩下 17 个白勾一遍。
func (m *Manager) DeletePaths(id string, rels []string) BatchDeleteResult {
	out := BatchDeleteResult{OK: []string{}, Failed: []PathError{}}
	for _, rel := range rels {
		abs, clean, err := m.resolvePath(id, rel)
		if err != nil {
			out.Failed = append(out.Failed, PathError{Path: rel, Err: err.Error()})
			continue
		}
		if clean == "" {
			out.Failed = append(out.Failed, PathError{Path: rel, Err: "不能删除实例根目录"})
			continue
		}
		if _, err := os.Stat(abs); err != nil {
			out.Failed = append(out.Failed, PathError{Path: clean, Err: ErrPathNotFound.Error()})
			continue
		}
		if err := os.RemoveAll(abs); err != nil {
			out.Failed = append(out.Failed, PathError{Path: clean, Err: err.Error()})
			continue
		}
		out.OK = append(out.OK, clean)
	}
	return out
}

// ── 新建文件夹 / 移动（重命名）/ 复制 ─────────────────────
//
// 三件事共用一个规矩：**目标已经存在就报错**。静默覆盖是最快的毁数据方式 ——
// 用户以为「又建了一个」，实际是「把旧的顶掉了」。

// MkdirAt 新建文件夹，父目录不存在时一起建出来。
func (m *Manager) MkdirAt(id, rel string) (string, error) {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return "", fmt.Errorf("不能把实例根目录当成文件夹来新建")
	}
	if _, err := os.Stat(abs); err == nil {
		return "", fmt.Errorf("%w：%s", ErrPathExists, clean)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", err
	}
	return clean, nil
}

// MovePath 移动或重命名 —— 对用户是同一件事（改名就是「移到同目录的另一个名字下」）。
// 目标所在目录不存在时建出来，和资源管理器拖放的行为一致。
func (m *Manager) MovePath(id, from, to string) (string, error) {
	src, srcClean, err := m.resolvePath(id, from)
	if err != nil {
		return "", err
	}
	if srcClean == "" {
		return "", fmt.Errorf("不能移动实例根目录")
	}
	dst, dstClean, err := m.resolvePath(id, to)
	if err != nil {
		return "", err
	}
	if dstClean == "" {
		return "", fmt.Errorf("不能把东西移动成实例根目录")
	}
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("%w：%s", ErrPathNotFound, srcClean)
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("%w：%s", ErrPathExists, dstClean)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	return dstClean, nil
}

// CopyPath 复制文件或目录（目录递归）。
func (m *Manager) CopyPath(id, from, to string) (string, error) {
	src, srcClean, err := m.resolvePath(id, from)
	if err != nil {
		return "", err
	}
	if srcClean == "" {
		return "", fmt.Errorf("不能复制实例根目录")
	}
	dst, dstClean, err := m.resolvePath(id, to)
	if err != nil {
		return "", err
	}
	if dstClean == "" {
		return "", fmt.Errorf("不能把东西复制成实例根目录")
	}
	fi, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("%w：%s", ErrPathNotFound, srcClean)
	}
	if _, err := os.Stat(dst); err == nil {
		return "", fmt.Errorf("%w：%s", ErrPathExists, dstClean)
	}
	// 把目录复制进它自己的子目录 = 无限递归（每拷一层，里面又多一层可拷的），
	// 结局是把磁盘写满。这条必须在递归**之前**挡住。
	if fi.IsDir() && isSameOrChild(dst, src) {
		return "", fmt.Errorf("不能把目录复制进它自己里面：%s", srcClean)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if fi.IsDir() {
		if err := copyDir(src, dst); err != nil {
			return "", err
		}
		return dstClean, nil
	}
	if err := copyFile(src, dst, fi.Mode()); err != nil {
		return "", err
	}
	return dstClean, nil
}

// isSameOrChild 判断 child 是 parent 本身，还是它的子路径。
func isSameOrChild(child, parent string) bool {
	c := filepath.Clean(child)
	p := filepath.Clean(parent)
	if c == p {
		return true
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	des, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range des {
		fi, err := e.Info()
		if err != nil {
			return err
		}
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if fi.IsDir() {
			if err := copyDir(s, d); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(s, d, fi.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// ExtractZipAt 把实例目录里的一个 zip 解开。
//
// into == "here" 时解到 zip 所在目录（整合包常用：它的 mods/、config/ 正好并进实例）；
// 否则解到 zip 同级的同名文件夹。
func (m *Manager) ExtractZipAt(id, rel, into string) (string, error) {
	abs, clean, err := m.resolvePath(id, rel)
	if err != nil {
		return "", err
	}
	if !isZipName(clean) {
		return "", fmt.Errorf("只支持 .zip 文件：%s", clean)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("%w：%s", ErrPathNotFound, clean)
	}
	destDir := filepath.Dir(abs)
	if into != "here" {
		destDir = filepath.Join(filepath.Dir(abs), strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs)))
		if _, err := os.Stat(destDir); err == nil {
			return "", fmt.Errorf("已经有一个同名文件夹「%s」了 —— 先删掉它，或者改用「直接解压到当前目录」",
				filepath.Base(destDir))
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if err := extractZipSmart(abs, destDir); err != nil {
		return "", err
	}
	return m.relToRoot(id, destDir)
}

// ExtractUploadedZip 收一个上传的 zip 并直接解开，省掉「先传上去再点解压」两步。
func (m *Manager) ExtractUploadedZip(id, relDir, filename string, r io.Reader, into string) (string, error) {
	dir, _, err := m.resolvePath(id, relDir)
	if err != nil {
		return "", err
	}
	base, err := safeUploadName(filename)
	if err != nil {
		return "", err
	}
	if !isZipName(base) {
		return "", fmt.Errorf("只支持 .zip 文件")
	}
	// 临时文件放在系统临时目录：不从实例目录里过一道，就不会在解压期间
	// 在用户的文件夹里多出一个莫名其妙的东西。
	f, err := os.CreateTemp("", "sbx-upload-*.zip")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}

	destDir := dir
	if into != "here" {
		destDir = filepath.Join(dir, strings.TrimSuffix(base, filepath.Ext(base)))
		if _, err := os.Stat(destDir); err == nil {
			return "", fmt.Errorf("已经有一个同名文件夹「%s」了 —— 先删掉它，或者改用「直接解压到当前目录」",
				filepath.Base(destDir))
		}
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	if err := extractZipSmart(tmp, destDir); err != nil {
		return "", err
	}
	return m.relToRoot(id, destDir)
}

// SaveUploadTo 把一个上传的文件写进实例目录里的子目录，返回落盘后的文件名。
// 与插件上传的区别：这里不限 .jar，任何文件都能放进任何目录（文件管理的本职）。
func (m *Manager) SaveUploadTo(id, relDir, filename string, r io.Reader) (string, error) {
	dir, _, err := m.resolvePath(id, relDir)
	if err != nil {
		return "", err
	}
	base, err := safeUploadName(filename)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dst, err := os.Create(filepath.Join(dir, base))
	if err != nil {
		return "", err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, r); err != nil {
		return "", err
	}
	return base, nil
}

// relToRoot 把绝对路径换算回「相对实例目录」的形式
func (m *Manager) relToRoot(id, abs string) (string, error) {
	root, _, err := m.instanceDir(id)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}

// safeUploadName 只留文件名：上传者给的路径里带任何分隔符都不可信。
func safeUploadName(filename string) (string, error) {
	base := filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("非法的文件名：%s", filename)
	}
	return base, nil
}

// extractZipSmart 解压时自动剥掉「整包只套了一层文件夹」的那层。
//
// 网上下来的整合包十有八九长这样：`MyPack/mods/...`。不剥的话用户会得到
// `MyPack/MyPack/mods/...`，还得自己再搬一次 —— 这是这类功能最常见的抱怨。
func extractZipSmart(archivePath, destDir string) error {
	prefix, err := singleRootPrefix(archivePath)
	if err != nil {
		return err
	}
	return extractZipTo(archivePath, destDir, prefix)
}

// layoutDirs 服务端目录布局里本来就存在的顶层目录。
// 如果 zip 里唯一的顶层目录叫这些名字，说明这个 zip **就是**内容本身
// （比如只打包了 mods/），不是「外面套了一层」——此时剥前缀会把东西放错地方。
var layoutDirs = map[string]bool{
	"mods": true, "plugins": true, "config": true, "libraries": true,
	"world": true, "worlds": true, "logs": true, "crash-reports": true,
	"defaultconfigs": true, "kubejs": true, "scripts": true,
	"resourcepacks": true, "datapacks": true, "saves": true,
	"cache": true, "run": true,
}

// singleRootPrefix 判断这个 zip 是不是「只有一个顶层目录、且没有顶层散文件」。
// 是则返回 `顶层目录/`，否则返回空串（表示不要剥）。
func singleRootPrefix(archivePath string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("打开压缩包失败（文件可能没下完整）：%w", err)
	}
	defer zr.Close()

	root := ""
	for _, f := range zr.File {
		name := strings.TrimPrefix(strings.ReplaceAll(f.Name, "\\", "/"), "./")
		if name == "" {
			continue
		}
		i := strings.Index(name, "/")
		if i < 0 {
			// 顶层有散文件 —— 剥掉那层会把散文件和文件夹里的内容混在一起
			return "", nil
		}
		top := name[:i+1]
		if root == "" {
			root = top
		} else if root != top {
			return "", nil
		}
	}
	if layoutDirs[strings.TrimSuffix(root, "/")] {
		return "", nil
	}
	return root, nil
}
