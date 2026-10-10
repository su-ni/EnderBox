package modpack

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/su-ni/EnderBox/internal/fetch"
)

// 导出侧的三个硬规矩（和导入侧对称）：
//
//  ① **只收白名单目录**。不做「整个实例打包」—— 那会把 world/（用户的存档）、
//     logs/、server.jar（几百 MB 且能重下）、server.properties（含本机端口）一起塞进去。
//  ② **路径一律用固定前缀拼**，拒绝 `..` 与绝对路径，防 zip-slip（导入侧有
//     TestExecuteBlocksZipSlip，这边要对称地防）。
//  ③ **流式算 sha1**。整合包的 jar 加起来可能上百 MB，整个读进内存会直接把面板撑爆。

// defaultClientDirs / defaultServerDirs 默认要收的子目录。
//
// 分成两份而不是一份：`resourcepacks` / `shaderpacks` 对服务端毫无意义，
// 塞进服务端包只会白白变大（虽然它们走 overrides，但那也是体积）。
var (
	defaultCommonDirs = []string{"mods", "config", "defaultconfigs", "scripts", "kubejs"}
	clientOnlyDirs    = []string{"resourcepacks", "shaderpacks"}
)

// ExportOptions 一次导出的全部输入。
type ExportOptions struct {
	InstanceDir  string
	InstanceName string

	// PackName / PackVersion 缺省时按实例名与时间推。**界面里可改** ——
	// 发出去的包，名字和版本号是给人看的门面。
	PackName    string
	PackVersion string

	Minecraft string // 如 "1.20.1"
	LoaderKey string // "fabric-loader" / "forge" / "neoforge" / "quilt-loader"，空=原版
	LoaderVer string

	WantClient bool
	WantServer bool

	// ExtraDirs 用户额外勾的目录（相对实例目录），会并进白名单。
	ExtraDirs []string

	// Lookup 反查函数。**注入而不是写死**：测试要能不打网络地喂假数据。
	Lookup func(sha1s []string) (map[string]fetch.RemoteVersion, error)

	// DryRun 只算不写。预览走这个 —— 用户得先看清楚会打包什么再决定导不导，
	// 因为**发出去的包改不了**。
	DryRun bool

	// Now 时间源，测试要固定输出。
	Now func() time.Time
}

// PackResult 一个包的产出。
type PackResult struct {
	// Path 生成的 .mrpack 绝对路径
	Path string
	// IndexEntries 写进清单的文件数
	IndexEntries int
	// OverrideFiles 走了 overrides 的文件数（＝反查不到的本地文件）
	OverrideFiles int
	// LocalFiles 反查不到、因而必须打进包里的相对路径。
	// ⚠ 必须回给界面：用户得知道「这个包为什么有 200MB」，以及**哪些模组不是从市场装的**。
	LocalFiles []string
	// Skipped 因为 environment 被剔掉的文件（如客户端包剔掉 server_only 的模组）。
	// ⚠ 也要回给界面 —— 直接丢掉会让用户以为我们漏了文件。
	Skipped []string
	// Bytes 生成的文件大小。DryRun 时是 0（没写文件）
	Bytes int64
	// OverrideBytes 走 overrides 的那批文件**原始大小之和**。
	// 预览要显示它：用户看到「包会从 50KB 涨到 300MB」才有机会反悔。
	// 包体 ≈ OverrideBytes + 清单（清单本身很小，走 CDN 的文件不计入）。
	OverrideBytes int64
	// DryRun 这份结果是预览算出来的，没有产物。
	DryRun bool
}

// ExportResult 一次导出的产出（可能两个包）。
type ExportResult struct {
	Client *PackResult
	Server *PackResult
}

// scanned 扫出来的一个文件。
type scanned struct {
	// Rel 相对实例目录的路径，用正斜杠（zip 里一律用正斜杠，Windows 上也一样）
	Rel string
	// Abs 绝对路径
	Abs string
	// SHA1 十六进制小写
	SHA1 string
	Size int64
}

// Export 按选项生成一或两个 .mrpack。
func Export(o ExportOptions) (*ExportResult, error) {
	if !o.WantClient && !o.WantServer {
		return nil, fmt.Errorf("至少要选一种包（客户端 / 服务端）")
	}
	if o.Lookup == nil {
		return nil, fmt.Errorf("缺少反查函数")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.InstanceDir == "" {
		return nil, fmt.Errorf("缺少实例目录")
	}
	if fi, err := os.Stat(o.InstanceDir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("实例目录不存在或不是目录：%s", o.InstanceDir)
	}

	// ① 扫描 + 算哈希（一遍扫完，两个包共用 —— 这也是「一次导出两个包」的意义：
	//    省一遍 IO，且两个包里的文件哈希必然一致）
	dirs := append([]string{}, defaultCommonDirs...)
	if o.WantClient {
		dirs = append(dirs, clientOnlyDirs...)
	}
	dirs = append(dirs, o.ExtraDirs...)

	files, err := scanDirs(o.InstanceDir, dirs)
	if err != nil {
		return nil, err
	}

	// ② 批量反查（一次问完，不要按包问两遍）
	hashes := make([]string, 0, len(files))
	for _, f := range files {
		hashes = append(hashes, f.SHA1)
	}
	remote, err := o.Lookup(hashes)
	if err != nil {
		return nil, fmt.Errorf("反查 Modrinth 失败：%w", err)
	}

	res := &ExportResult{}
	if o.WantClient {
		p, err := buildPack(o, files, remote, true)
		if err != nil {
			return nil, err
		}
		res.Client = p
	}
	if o.WantServer {
		p, err := buildPack(o, files, remote, false)
		if err != nil {
			return nil, err
		}
		res.Server = p
	}
	return res, nil
}

// scanDirs 遍历白名单目录，收集常规文件并**流式**算 sha1。
//
// 目录不存在就跳过（整合包里 `kubejs` 这种目录经常没有），
// 但**符号链接不跟随** —— 跟着出去可能把实例目录外的东西打进包里。
func scanDirs(root string, dirs []string) ([]scanned, error) {
	var out []scanned
	seen := map[string]bool{}

	for _, d := range dirs {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		// ⚠ 目录名本身也要过一遍 safeRelPath：用户可能在 ExtraDirs 里手填 "../.."
		if !safeRelPath(d) {
			continue
		}
		base := filepath.Join(root, filepath.FromSlash(d))
		fi, err := os.Stat(base)
		if err != nil || !fi.IsDir() {
			continue
		}

		err = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // 单个文件读不到就跳过，不让整次导出失败
			}
			if info.IsDir() {
				return nil
			}
			// 不跟随符号链接：`filepath.Walk` 给的是 Lstat 结果
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if !safeRelPath(rel) {
				return nil
			}
			// ⚠⚠ 跳过**下载中的临时分片**（`xxx.jar.part.0`）。
			//   这不是洁癖，是实测撞出来的：某个真实实例的 mods/ 里躺着
			//   `maincity-map2.jar.part.0~7` 和 `voicechat-….jar.part.0~4` —— 共 51MB。
			//   它们**不是完整的模组**，打进包收包那边装不上；
			//   而且会让「包有多大」的估算虚高到离谱（用户看到 51MB 会以为包里有一堆模组）。
			//   单测造不出这种脏文件，只有拿真实例跑一遍才会发现。
			name := info.Name()
			if strings.HasSuffix(name, ".part") || strings.Contains(name, ".part.") {
				return nil
			}
			sum, err := sha1File(p)
			if err != nil {
				return nil
			}
			out = append(out, scanned{Rel: rel, Abs: p, SHA1: sum, Size: info.Size()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	// 排序：让同样的输入产出**字节一致**的包（便于比对，也让测试稳定）
	sort.Slice(out, func(i, j int) bool { return out[i].Rel < out[j].Rel })
	return out, nil
}

// sha1File 流式算 sha1。**不要** os.ReadFile —— 一个整合包的 jar 可能有 200MB。
func sha1File(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// envFromRemote 把 Modrinth 的 environment 单值翻成清单要的 Env 双值。
//
// Modrinth 的三个取值：
//
//	"client_only"           → 只给客户端
//	"dedicated_server_only" → 只给服务端
//	"*"（或空）             → 两端都要
//
// ⚠ 遇到**不认识的值**时按 `*` 处理（两端都要），不是当 unsupported。
//   当成 unsupported 会把它静默剔掉 —— 而这正是「装完少个模组、还查不出原因」的经典来源。
//   `mrpack.go` 里对 knownSides 也是同一套态度：不认识的档位不能静默丢。
func envFromRemote(env string) Env {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "client_only":
		return Env{Client: SideRequired, Server: SideUnsupported}
	case "dedicated_server_only":
		return Env{Client: SideUnsupported, Server: SideRequired}
	default:
		return Env{Client: SideRequired, Server: SideRequired}
	}
}

// ovFile 一个要打进 overrides/ 的文件（反查不到，只能连内容一起带上）。
type ovFile struct {
	// rel 相对实例目录的路径，正斜杠
	rel string
	// abs 绝对路径
	abs string
	// size 原始大小。预览要拿它估算包体 —— 用户看到「会从 50KB 涨到 300MB」才有机会反悔
	size int64
}

// buildPack 生成一个包（client=true 出客户端包，否则服务端包）。
func buildPack(o ExportOptions, files []scanned, remote map[string]fetch.RemoteVersion, client bool) (*PackResult, error) {
	idx := Index{
		Game:          "minecraft",
		FormatVersion: 1,
		VersionID:     o.packVersion(),
		Name:          o.packName(client),
		Summary:       "",
		Files:         []File{},
		Dependencies:  map[string]string{},
	}
	if o.Minecraft != "" {
		idx.Dependencies["minecraft"] = o.Minecraft
	}
	if o.LoaderKey != "" && o.LoaderVer != "" {
		idx.Dependencies[o.LoaderKey] = o.LoaderVer
	}

	// ⚠ 两个列表**必须初始化成空切片**，不能留 nil。
	//   Go 的 nil slice 序列化成 JSON 的 `null`，前端就得为「没有」和「没这个字段」写两套判断。
	//   （这条是项目里既有的规矩，我在这又踩了一次 —— 服务端包的 Skipped 返回了 null。）
	res := &PackResult{
		LocalFiles: []string{},
		Skipped:    []string{},
	}
	// 两边都要打的 overrides：反查不到的文件，只能连人带包一起塞进去
	var overrides []ovFile

	for _, f := range files {
		rv, ok := remote[f.SHA1]
		if !ok {
			// 反查不到 = 本地自制 / 改过的文件。
			// ⚠ 按定稿方案：**默认塞进 overrides**（包大但一定装得齐），
			//   而不是只列清单 —— 发出去的包改不了，收包的人装不起来他自己没法解决。
			overrides = append(overrides, ovFile{rel: f.Rel, abs: f.Abs, size: f.Size})
			res.LocalFiles = append(res.LocalFiles, f.Rel)
			continue
		}

		env := envFromRemote(rv.Environment)
		// 按目标端分流
		want := env.Server
		if client {
			want = env.Client
		}
		if want == SideUnsupported {
			res.Skipped = append(res.Skipped, f.Rel)
			continue
		}

		pf, ok := rv.PrimaryFile()
		if !ok || pf.URL == "" {
			// 反查到版本但没有可下载的主文件 —— 当成查不到处理，塞 overrides。
			// 宁可包大，也不能写一条下不动的 downloads 进清单。
			overrides = append(overrides, ovFile{rel: f.Rel, abs: f.Abs, size: f.Size})
			res.LocalFiles = append(res.LocalFiles, f.Rel)
			continue
		}

		hashes := map[string]string{}
		if pf.Hashes["sha1"] != "" {
			hashes["sha1"] = pf.Hashes["sha1"]
		}
		if pf.Hashes["sha512"] != "" {
			hashes["sha512"] = pf.Hashes["sha512"]
		}
		if len(hashes) == 0 {
			// 源站没给校验和：至少把我们自己算的 sha1 写进去，别留一个空的 hashes
			hashes["sha1"] = f.SHA1
		}
		idx.Files = append(idx.Files, File{
			Path:      f.Rel,
			Hashes:    hashes,
			Env:       env,
			Downloads: []string{pf.URL},
			FileSize:  pf.Size,
		})
	}

	res.IndexEntries = len(idx.Files)
	res.OverrideFiles = len(overrides)
	for _, ov := range overrides {
		res.OverrideBytes += ov.size
	}
	res.DryRun = o.DryRun
	if o.DryRun {
		// 预览：算完就返回，**不落盘**。目录、名字、大小照常回给界面 —— 用户要看的正是这些。
		return res, nil
	}

	// 写包
	dir := filepath.Join(o.InstanceDir, "..", "packs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("建 packs 目录失败：%w", err)
	}
	name := fmt.Sprintf("%s-%s-%s.mrpack", sanitizeFile(o.packName(client)), idx.VersionID, map[bool]string{true: "client", false: "server"}[client])
	out := filepath.Join(dir, name)

	if err := writePack(out, idx, overrides); err != nil {
		return nil, err
	}
	fi, err := os.Stat(out)
	if err == nil {
		res.Bytes = fi.Size()
	}
	res.Path = out
	return res, nil
}

// writePack 把清单 + overrides 打成 zip（.mrpack 就是 zip）。
func writePack(out string, idx Index, overrides []ovFile) error {
	f, err := os.Create(out)
	if err != nil {
		return fmt.Errorf("建包失败：%w", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	// ① modrinth.index.json
	body, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	w, err := zw.Create(indexName)
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}

	// ② overrides/ 下的真实文件。
	//
	// ⚠⚠ 这里的每一步**都必须检查错误**，一个都不能 `_ =` 掉。
	//   踩过的坑：原先把 `io.Copy` 的返回值丢给 `_`，结果产出了一个 **6380 字节的包** ——
	//   zip 头里写着 jar 有 4.19MB，正文是空的。外壳看着「导出成功」，
	//   里面的文件全是零长度，收包那边装不起来还查不出原因。
	//   这就是「不静默失败」原则要落地的地方：宁可导出报错，也不要给出一个坏包。
	for _, ov := range overrides {
		// ⚠ 再确认一次路径安全：zip 里的条目名必须是 `overrides/<安全相对路径>`
		if !safeRelPath(ov.rel) {
			return fmt.Errorf("覆盖文件的路径不合法，拒绝写入：%s", ov.rel)
		}
		w, err := zw.Create(overridesPrefix + ov.rel)
		if err != nil {
			return fmt.Errorf("在包里建条目 %s 失败：%w", ov.rel, err)
		}
		src, err := os.Open(ov.abs)
		if err != nil {
			// 不静默跳过：漏一个文件，包就装不起来
			return fmt.Errorf("打包时读不到 %s：%w", ov.rel, err)
		}
		n, err := io.Copy(w, src)
		_ = src.Close()
		if err != nil {
			return fmt.Errorf("把 %s 写进包时失败：%w", ov.rel, err)
		}
		if n != ov.size {
			return fmt.Errorf("把 %s 写进包时不完整：期望 %d 字节，实际写入 %d", ov.rel, ov.size, n)
		}
	}
	return nil
}

func (o ExportOptions) packName(client bool) string {
	n := strings.TrimSpace(o.PackName)
	if n == "" {
		n = o.InstanceName
	}
	if n == "" {
		n = "整合包"
	}
	if client {
		return n + "（客户端）"
	}
	return n + "（服务端）"
}

func (o ExportOptions) packVersion() string {
	if v := strings.TrimSpace(o.PackVersion); v != "" {
		return v
	}
	// 用日期当默认版本号：比 "1.0.0" 更有信息量，用户也一眼看得出什么时候导的
	return o.Now().Format("2006.01.02")
}

// sanitizeFile 把包名里不能进文件名的字符换掉。
// Windows 上 `\ / : * ? " < > |` 一个都不能留，否则 Create 直接失败。
func sanitizeFile(s string) string {
	repl := strings.NewReplacer(
		"\\", "_", "/", "_", ":", "_", "*", "_", "?", "_",
		"\"", "_", "<", "_", ">", "_", "|", "_",
	)
	out := repl.Replace(strings.TrimSpace(s))
	if out == "" {
		out = "pack"
	}
	return out
}
