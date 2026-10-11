package daemon

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/su-ni/EnderBox/internal/jobs"
)

// JDK 归档的解压与安装。单独一个文件是因为这是纯文件操作，
// 和「探测本机有哪些 Java」是两件事。

// extractArchive 按扩展名选解压方式。只认我们自己会下的两种格式 ——
// 多认一种就多一条「猜错格式然后把目录搅乱」的路。
func extractArchive(archivePath, destDir string) error {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, destDir)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(archivePath, destDir)
	}
	return fmt.Errorf("不认识的压缩格式：%s", filepath.Base(archivePath))
}

// safeJoin 把归档里的条目名落到 destDir 之内，越界就报错。
//
// 为什么必须校验：归档是**从网上下的**，里面一条 `../../Windows/System32/x.dll`
// 就足够把用户系统写坏。zip-slip 不是理论问题，是这类「自动解压」功能的经典翻车点。
func safeJoin(destDir, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == string(filepath.Separator) {
		return "", fmt.Errorf("归档里的条目名是空的")
	}
	target := filepath.Join(destDir, clean)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("归档里的路径越界，已拒绝：%s", name)
	}
	return target, nil
}

func extractZip(archivePath, destDir string) error {
	return extractZipTo(archivePath, destDir, "")
}

// extractZipTo 解压 zip。stripPrefix 非空时只解压该前缀下的条目，并把前缀去掉
// —— 文件管理器用它剥掉「整合包里多套的那一层文件夹」。
func extractZipTo(archivePath, destDir, stripPrefix string) error {
	return extractZipToProgress(archivePath, destDir, stripPrefix, nil)
}

// zipEntryName 按 stripPrefix 规则算出条目在目标目录里的相对名。ok=false 表示这条要跳过。
func zipEntryName(raw, stripPrefix string) (string, bool) {
	if stripPrefix == "" {
		return raw, true
	}
	if !strings.HasPrefix(raw, stripPrefix) {
		return "", false
	}
	n := strings.TrimPrefix(raw, stripPrefix)
	if n == "" {
		return "", false
	}
	return n, true
}

// extractZipToProgress 是 extractZipTo 的带进度版本（onProgress 为 nil 则静默）。
//
// 为什么需要它：存档导入是分钟级操作（大世界几十万个区块文件），没有进度
// 用户不知道到底在解还是卡死了。分母取 zip 条目 UncompressedSize 之和 —— 那是
// 解压后真正要落盘的字节数，比压缩包大小更贴近「还剩多少」的体感。
func extractZipToProgress(archivePath, destDir, stripPrefix string, onProgress jobs.ProgressFunc) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("打开压缩包失败（文件可能没下完整）：%w", err)
	}
	defer zr.Close()

	// 先扫一遍算分母：被 stripPrefix 滤掉的条目、目录项都不该进分母。
	var total int
	var totalBytes int64
	for _, f := range zr.File {
		if _, ok := zipEntryName(f.Name, stripPrefix); !ok || f.FileInfo().IsDir() {
			continue
		}
		total++
		totalBytes += int64(f.UncompressedSize64)
	}
	if onProgress != nil {
		onProgress(0, total, 0, totalBytes, "")
	}

	var done int
	var doneBytes int64
	for _, f := range zr.File {
		name, ok := zipEntryName(f.Name, stripPrefix)
		if !ok {
			continue
		}
		target, err := safeJoin(destDir, name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("建目录 %s 失败：%w", target, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("建目录失败：%w", err)
		}
		// 读条目时 archive/zip 会校验它自己的 CRC32 —— 下坏了的包在这一步必然报错。
		// 这正是「源站没给 sha256」时的兜底完整性检查，不是可有可无的一步。
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("解压 %s 失败：%w", f.Name, err)
		}
		werr := writeFile(target, rc, f.Mode())
		rc.Close()
		if werr != nil {
			return werr
		}
		done++
		doneBytes += int64(f.UncompressedSize64)
		if onProgress != nil {
			onProgress(done, total, doneBytes, totalBytes, name)
		}
	}
	return nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("打开压缩包失败（文件可能没下完整）：%w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("读取归档失败：%w", err)
		}
		target, err := safeJoin(destDir, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("建目录 %s 失败：%w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("建目录失败：%w", err)
			}
			if err := writeFile(target, tr, hdr.FileInfo().Mode()); err != nil {
				return err
			}
		default:
			// 软链接 / 硬链接 / 设备文件一律跳过。JDK 包里本来就没有，
			// 而「跟着一条指向 /etc 的软链去写文件」正是另一个经典翻车点。
		}
	}
	return nil
}

// writeFile 落一个归档条目。非 Windows 上保留可执行位 ——
// Linux/mac 的 bin/java 没有 x 位就是「装了但起不来」，而且报错很含糊。
func writeFile(target string, r io.Reader, mode os.FileMode) error {
	if runtime.GOOS == "windows" {
		mode = 0o644
	} else if mode&0o777 == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode&0o777)
	if err != nil {
		return fmt.Errorf("写 %s 失败：%w", filepath.Base(target), err)
	}
	defer out.Close()
	if _, err := io.Copy(out, r); err != nil {
		return fmt.Errorf("解压 %s 失败：%w", filepath.Base(target), err)
	}
	return nil
}

// findJavaIn 在解出来的目录里找 java 可执行文件。
// 归档里通常还多一层（jdk-21.0.12.1+1/bin/java），所以最多往下找两层。
func findJavaIn(root string) (string, error) {
	exe := javaExeName()
	if hits, _ := filepath.Glob(filepath.Join(root, "bin", exe)); len(hits) > 0 {
		return hits[0], nil
	}
	if hits, _ := filepath.Glob(filepath.Join(root, "*", "bin", exe)); len(hits) > 0 {
		return hits[0], nil
	}
	return "", fmt.Errorf("解压完了却没找到 %s —— 归档内部结构可能变了", filepath.Join("bin", exe))
}

// InstallJDK 把下好的 JDK 归档装到 runtimes/<name>/ 下，返回产出的 java 可执行文件路径。
//
// 名字用固定的 temurin-<主版本>（而不是把版本号写进目录名）：
// 同一主版本重下一次就地覆盖，不会在 runtimes/ 下堆出一串越来越长的目录，
// 也不会让实例里存着的 javaPath 在下一次更新后指向一个已经不存在的路径。
//
// 换目录用的是「先解到旁边、验证过再换」：
// 解压失败时原来那份还在，不会出现「更新到一半，Java 没了」。
func (m *Manager) InstallJDK(archivePath, name string, report func(string)) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", fmt.Errorf("非法的运行时名字：%s", name)
	}
	// 中间产物统一放在 runtimes/.staging/ 下。放别处的两个原因：
	//   ① 目录名以点开头，javaCandidates 会跳过它 —— 一个「解到一半」的目录
	//      绝不能被当成「本机可用的 Java」；
	//   ② 和最终目录同在 runtimes/ 下 → 同一个卷，rename 是原子的，不会退化成复制。
	stagingRoot := filepath.Join(m.baseDir, "runtimes", ".staging")
	dest := filepath.Join(m.baseDir, "runtimes", name)
	staging := filepath.Join(stagingRoot, name)
	backup := filepath.Join(stagingRoot, name+".old")

	_ = os.RemoveAll(staging)
	_ = os.RemoveAll(backup)
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", fmt.Errorf("建临时目录失败：%w", err)
	}

	if report != nil {
		report("正在解压")
	}
	if err := extractArchive(archivePath, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}

	// 先在临时目录里确认能拿到 java，再去动 dest ——
	// 顺序反过来的话，一个结构不对的包会先把好端端的那份换掉。
	found, err := findJavaIn(staging)
	if err != nil {
		_ = os.RemoveAll(staging)
		return "", err
	}

	if _, err := os.Stat(dest); err == nil {
		// Windows 上有 JVM 正从 runtimes/<name>/ 里跑着的话，这一步会失败。
		// 这不是「故障」，是用户操作顺序问题，所以要说清楚该先干嘛。
		if err := os.Rename(dest, backup); err != nil {
			_ = os.RemoveAll(staging)
			return "", fmt.Errorf("换不掉旧的那份（%s）—— 有服务端正在用这个 Java，先把它停掉再试：%w", name, err)
		}
	}
	if err := os.Rename(staging, dest); err != nil {
		_ = os.Rename(backup, dest) // 尽量还原，别把能用的那份弄丢
		_ = os.RemoveAll(staging)
		return "", fmt.Errorf("安装到 %s 失败：%w", dest, err)
	}
	_ = os.RemoveAll(backup)
	// .staging 空了就顺手删掉，别在用户的 runtimes/ 下留一个空目录让人猜是干嘛的
	_ = os.Remove(stagingRoot)

	rel, rerr := filepath.Rel(dest, found)
	if rerr != nil || strings.HasPrefix(rel, "..") {
		rel = filepath.Base(found)
	}
	return filepath.Join(dest, rel), nil
}
