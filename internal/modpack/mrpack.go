// Package modpack 处理 Minecraft「整合包」（modpack）。
//
// 本项目的定位决定了这里的取舍：**只做服务端整合包**。
// 客户端整合包（光影、优化、按键配置那一类）装到服务端要么是死重量、要么直接起不来 ——
// 所以解析层的第一职责不是「怎么装」，而是**判断这个包能不能装到服务端**，
// 并在不能装的时候给出具体证据与下一步（而不是一句「不支持」）。
//
// 格式事实（2026-10-06 拆真包实测，别凭记忆改。样本：Zombie Invade 100 Days 2.3，28.6MB）：
//
//	modrinth.index.json          ← 唯一的数据源
//	  dependencies { "forge": "47.4.10", "minecraft": "1.20.1" }   ← 建实例需要的加载器与版本
//	  files[129]  { path, hashes{sha1,sha512}, env{client,server}, downloads[] }
//	overrides/        1243 个文件   ← 共享覆盖（config/…）
//	server-overrides/    0 个文件   ← **服务端专属**覆盖
//
// ⚠ 两件凭直觉一定会写错的事：
//  1. **模组本体不在包里**。包里只有 overrides/；模组全靠 files[].downloads 去 CDN 取。
//     所以安装/升级必须复用现有的下载管理器，不要另写一条下载路径。
//  2. 判定服务端该装哪些看 `env.server` 三档：required 装 / optional 让用户勾 / unsupported 不装。
//     覆盖文件只取 overrides/ 与 server-overrides/ 两个前缀。
//     （参考实现 nothub/mrpack-install 的 overrides.go 与 update/command.go 用的是同一套判据。）
package modpack

import (
	"archive/zip"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// 覆盖文件的两个前缀。服务端相关的覆盖文件只会落在这两个前缀下。
const (
	overridesPrefix       = "overrides/"
	serverOverridesPrefix = "server-overrides/"
)

// indexName 包里那份索引的文件名。
const indexName = "modrinth.index.json"

// 加载器在 dependencies 里可能出现的键。按「优先级」排列 ——
// 一个包里理论上只会有其中一个，但真出现两个就按这个顺序取，别让结果随 map 遍历顺序变。
var loaderKeys = []string{"forge", "neoforge", "fabric-loader", "quilt-loader"}

// Side 一个文件在某一端是必需、可选，还是根本不支持。
type Side string

const (
	SideRequired    Side = "required"
	SideOptional    Side = "optional"
	SideUnsupported Side = "unsupported"
)

// 合法值集合。⚠ 上游以后加档位时，这里要跟着认 ——
// 不认识的档位**不能**当成 unsupported 静默丢掉（那会把该装的模组悄悄漏掉）。
var knownSides = map[Side]bool{SideRequired: true, SideOptional: true, SideUnsupported: true}

// Env 一个文件对两端的要求。
type Env struct {
	Client Side `json:"client"`
	Server Side `json:"server"`
}

// File index 里的一个待下载文件。
type File struct {
	Path      string            `json:"path"`
	Hashes    map[string]string `json:"hashes"`
	Env       Env               `json:"env"`
	Downloads []string          `json:"downloads"`
	FileSize  int64             `json:"fileSize"`

	// Extra 用来放 index 里我们不认识、但也不想丢掉的键（例如作者加了自定义字段）。
	// 「不认识的键不丢」和前端那两个「列出来但标原因」是同一条规矩。
	Extra map[string]json.RawMessage `json:"-"`
}

// Index modrinth.index.json 的完整解析结果。
type Index struct {
	Game          string            `json:"game"`
	FormatVersion int               `json:"formatVersion"`
	VersionID     string            `json:"versionId"`
	Name          string            `json:"name"`
	Summary       string            `json:"summary"`
	Files         []File            `json:"files"`
	Dependencies  map[string]string `json:"dependencies"`
}

// SHA 取这个文件记的 sha512（没有就退回 sha1）。升级比对用的就是它。
func (f File) SHA() (sum, kind string) {
	if f.Hashes == nil {
		return "", ""
	}
	if s := f.Hashes["sha512"]; s != "" {
		return s, "sha512"
	}
	if s := f.Hashes["sha1"]; s != "" {
		return s, "sha1"
	}
	if s := f.Hashes["sha256"]; s != "" {
		return s, "sha256"
	}
	return "", ""
}

// DownloadURL 首选下载地址。空串表示这个文件取不到（调用方要如实报出来，别静默跳过）。
func (f File) DownloadURL() string {
	if len(f.Downloads) == 0 {
		return ""
	}
	return f.Downloads[0]
}

// ParseIndex 从 .mrpack（一个 zip）里读索引。
func ParseIndex(zipPath string) (*Index, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("这个文件不是合法的 zip：%w", err)
	}
	defer func() { _ = zr.Close() }()
	for _, f := range zr.File {
		if f.Name != indexName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("读不了包里的 %s：%w", indexName, err)
		}
		defer func() { _ = rc.Close() }()
		// 索引本身有大小上限，防的是「解压炸弹」：一个几百字节的条目声明自己几 GB。
		raw, err := io.ReadAll(io.LimitReader(rc, 16<<20))
		if err != nil {
			return nil, fmt.Errorf("读 %s 失败：%w", indexName, err)
		}
		var ix Index
		if err := json.Unmarshal(raw, &ix); err != nil {
			return nil, fmt.Errorf("包里的 %s 解析失败（可能不是 Modrinth 格式）：%w", indexName, err)
		}
		if err := ix.validate(); err != nil {
			return nil, err
		}
		return &ix, nil
	}
	return nil, fmt.Errorf("这个 zip 里没有 %s，不是 Modrinth 整合包（.mrpack）", indexName)
}

// validate 只挡「结构上不可能继续」的情况，不做业务判断。
func (ix *Index) validate() error {
	if len(ix.Files) == 0 {
		// 空 files 会让后面所有的统计都变成 0，看着像「这包很干净」—— 其实是我们读错了。
		return fmt.Errorf("索引里一个文件都没有，这份 %s 不像有效的整合包", indexName)
	}
	for i, f := range ix.Files {
		if strings.TrimSpace(f.Path) == "" {
			return fmt.Errorf("索引里第 %d 个文件没有 path", i+1)
		}
		// ⚠ 路径必须挡穿越：它在安装时会被拼到实例目录后面。
		// 整合包是**第三方下载来的文件**，这条不是洁癖（zip-slip）。
		if !safeRelPath(f.Path) {
			return fmt.Errorf("索引里有可疑路径（可能是路径穿越）：%q", f.Path)
		}
	}
	return nil
}

// safeRelPath 判断一条相对路径能不能安全地拼到实例目录下。
func safeRelPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`) {
		return false
	}
	if strings.Contains(p, `\`) {
		return false // 统一用 /，反斜杠一律不收
	}
	if len(p) >= 2 && p[1] == ':' {
		return false // Windows 盘符
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// ServerInstallable 这个包里「服务端需要装」的文件，以及「明确跳过」的。
//
// 分档规则（与参考实现一致）：
//
//	required    → 装
//	optional    → 也放进 install（列表里带上，界面可让用户取消）
//	unsupported → 跳过
//	认不出的档位 → 当 required 处理，**不能当 unsupported** ——
//	               那会把该装的模组悄悄漏掉，而且界面上看不出来。
func (ix *Index) ServerInstallable() (install, skipped []File) {
	for _, f := range ix.Files {
		switch f.Env.Server {
		case SideUnsupported:
			skipped = append(skipped, f)
		default:
			install = append(install, f)
		}
	}
	return install, skipped
}

// Verdict 「这个包能不能装到服务端」的结论。
type Verdict struct {
	OK bool `json:"ok"`
	// Reason 不能装时说清**证据**（几个文件、什么标记），不是一句「不支持」。
	Reason string `json:"reason,omitempty"`
	// Install / Skipped 分别是服务端要装的、和明确跳过的文件数。
	Install int `json:"install"`
	Skipped int `json:"skipped"`
	// SkippedAll 整包都被跳过（＝纯客户端整合包）。
	SkippedAll bool `json:"skippedAll"`
}

// ServerVerdict 判断能不能当服务端整合包用。这是「不能作为客户端模组包导入」那条要求的落点。
func (ix *Index) ServerVerdict() Verdict {
	install, skipped := ix.ServerInstallable()
	v := Verdict{Install: len(install), Skipped: len(skipped)}
	if len(install) == 0 {
		v.OK = false
		v.SkippedAll = true
		v.Reason = fmt.Sprintf(
			"这个包里 %d 个文件全部标记为 server: unsupported —— 它是给玩家客户端用的整合包。"+
				"装到服务端不但没用，还会因为缺客户端专属的依赖而起不来。"+
				"如果作者另外发布过服务端包，请用那一份；Modrinth 上这类包通常标着 server_side: required。",
			len(skipped))
		return v
	}
	v.OK = true
	return v
}

// Loader 这个包基于哪个加载器。返回 ("forge", "47.4.10") 这样的一对。
// 都不认识时返回 ("", "")——调用方要如实说「这个包用的加载器我们不支持」。
func (ix *Index) Loader() (kind, version string) {
	for _, k := range loaderKeys {
		if v := strings.TrimSpace(ix.Dependencies[k]); v != "" {
			return k, v
		}
	}
	return "", ""
}

// Minecraft 这个包基于哪个游戏版本。
func (ix *Index) Minecraft() string {
	return strings.TrimSpace(ix.Dependencies["minecraft"])
}

// NormalizeLoader 把加载器键名换成面板内部的服务端类型名
// （"fabric-loader" → "fabric"，"quilt-loader" → ""）。
//
// 认不出返回**空串**，调用方必须如实说「这个包用的加载器我们不支持」——
// 别猜一个默认值：猜成 forge 会让用户装上一个根本起不来的端，而且界面看着还挺成功。
// 「认不出」与「认得出但不能自动下载」（neoforge）是两件事，前者空串、后者原样返回，
// 后者要不要拦由调用方按能不能自动准备服务端文件来决定。
func NormalizeLoader(kind string) string {
	switch kind {
	case "forge":
		return "forge"
	case "neoforge":
		return "neoforge"
	case "fabric-loader":
		return "fabric"
	case "quilt-loader":
		return "" // 面板没有 Quilt 服务端类型，如实说不支持
	}
	return ""
}

// isOverride 判断一条相对路径是不是覆盖文件（带两个覆盖前缀之一）。
func isOverride(p string) bool {
	return strings.HasPrefix(p, overridesPrefix) || strings.HasPrefix(p, serverOverridesPrefix)
}

// DiskRelOfOverride 把带前缀的覆盖路径换算成「相对实例根」的真实落盘路径。
//
// 覆盖前缀（overrides/、server-overrides/）只是 mrpack 的打包约定，解压后都落在实例根下
// （config/、world/…），所以要去掉前缀才能对上磁盘上的真实文件。但 Path 字段里**保留前缀**
// 做身份标识 —— 两种前缀语义不同（共享 vs 服务端专属），不能归一化成同一个 key。
// diff 问磁盘、执行器落盘、api 层给「磁盘哈希按哪种算」建索引，都用这一个换算。
func DiskRelOfOverride(p string) string {
	switch {
	case strings.HasPrefix(p, overridesPrefix):
		return strings.TrimPrefix(p, overridesPrefix)
	case strings.HasPrefix(p, serverOverridesPrefix):
		return strings.TrimPrefix(p, serverOverridesPrefix)
	default:
		return p
	}
}

// ReadOverrides 读出包里的覆盖文件清单（overrides/ 与 server-overrides/ 下的条目）。
//
// ⚠ 覆盖文件不在 modrinth.index.json 里 —— 它们是 zip 条目，必须单独走一遍目录。
// 路径**保留前缀**（两种前缀解压位置不同，不能归一化掉，否则会和同名模组撞 key）。
// 没有覆盖文件时返回空切片（不是 nil），调用方不必为「没有」特判。
// sha 用 sha1 流式算，避免把大文件整块读进内存。
func ReadOverrides(zipPath string) ([]OverrideRef, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("打不开整合包（%s）：%w", zipPath, err)
	}
	defer func() { _ = zr.Close() }()

	out := []OverrideRef{}
	for _, f := range zr.File {
		name := f.Name
		switch {
		case strings.HasPrefix(name, overridesPrefix), strings.HasPrefix(name, serverOverridesPrefix):
			// 落在两个覆盖前缀之下，继续处理
		default:
			continue // 索引、mods 的下载走别处，这里不收
		}
		if strings.HasSuffix(name, "/") {
			continue // 目录条目，文件会在自己的条目里建父目录
		}
		// 路径也要过一遍穿越校验（zip 条目名可以是 ../../x）。校验的是「带前缀的整条路径」。
		if !safeRelPath(name) {
			return nil, fmt.Errorf("覆盖文件路径可疑（可能是穿越）：%q", name)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("读覆盖文件 %q 失败：%w", name, err)
		}
		h := sha1.New()
		if _, err := io.Copy(h, rc); err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("算覆盖文件 %q 的哈希失败：%w", name, err)
		}
		_ = rc.Close()
		out = append(out, OverrideRef{
			Path:    name,
			SHA:     hex.EncodeToString(h.Sum(nil)),
			SHAKind: "sha1",
		})
	}
	return out, nil
}
