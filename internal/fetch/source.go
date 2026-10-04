// Package fetch 负责「服务端 jar 从哪来、怎么下」。
//
// 各家服务端的安装方式完全不同，所以不做一套通用抽象硬套，而是每种类型一个实现，
// 各自按自己的 API 取版本与构建；对外只暴露统一的版本/构建/下载语义。
//
// 实测记录（2026-10-04，别凭记忆改）：
//   - api.papermc.io/v2 → 410（已下线）
//   - api.papermc.io/v3 → 403（不是这个域名）
//   - fill.papermc.io/v3 → 200  ← 真正在用的
//   - 下载域名 fill-data.papermc.io 可直连，支持 Range
//   - Paper 的下载项键名是 "server:default"（v2 时代叫 application，别照抄老代码）
//   - piston-meta.mojang.com / launchermeta.mojang.com 都可用
//   - BMCLAPI 只镜像了原版那套；它的 papermc 路径是 404，别在界面上给 Paper 挂镜像
//   - Forge 的 maven.minecraftforge.net 是**通的**，别拿 NeoForge 的结论套过去（见 forge.go）
package fetch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Kind 服务端类型。已经接了下载的五种在这里；剩下两种明确标「还没接」，
// 不假装支持，也整块藏起来 —— 用户有权知道「有这个东西，只是现在走不通、卡在哪」。
type Kind string

const (
	KindPaper   Kind = "paper"
	KindVanilla Kind = "vanilla"
	KindFabric  Kind = "fabric"
	// Forge 是唯一一个「装完还要再跑一遍」的：它给的是 installer，不是服务端本身。
	// 见 forge.go 的文件头说明。
	KindForge    Kind = "forge"
	KindVelocity Kind = "velocity"

	// 下面这两个只是「界面上认识」，还没有对应的下载实现。
	KindNeoForge   Kind = "neoforge"
	KindBungeeCord Kind = "bungeecord"
)

// javaFallback 按 MC 版本猜需要的 Java 大版本。
// 只在拿不到权威值时兜底 —— 原版可以从 Mojang 元数据里直接读到 javaVersion，
// Paper 则去查同版本的原版元数据。小于 1.17 的一律 8，1.17~1.20.4 是 17，之后是 21。
func javaFallback(mc string) int {
	v := strings.SplitN(mc, ".", 3)
	if len(v) < 2 {
		return 21
	}
	var major, minor, patch int
	if _, err := fmt.Sscanf(v[0], "%d", &major); err != nil {
		return 21
	}
	if _, err := fmt.Sscanf(v[1], "%d", &minor); err != nil {
		return 21
	}
	if len(v) == 3 {
		_ = func() error { _, e := fmt.Sscanf(v[2], "%d", &patch); return e }()
	}
	// 新版号制（26.x 这种）没法按 1.x 推断，交给权威值
	if major != 1 {
		return 21
	}
	switch {
	case minor <= 16:
		return 8
	case minor <= 20 && !(minor == 20 && patch >= 5):
		return 17
	default:
		return 21
	}
}

// Version 一个可选的游戏版本
type Version struct {
	ID         string `json:"id"`         // 1.21.4
	Family     string `json:"family"`     // 1.21（Paper 的版本族，原版为空）
	Prerelease bool   `json:"prerelease"` // rc / pre / snapshot
	Stable     bool   `json:"stable"`     // 原版：type == release
}

// Build 某个版本下的一次构建。原版没有构建概念，用 0 占位。
type Build struct {
	ID        int    `json:"id"`
	Channel   string `json:"channel"` // STABLE / BETA / ALPHA；原版为空
	Time      string `json:"time"`
	Name      string `json:"name"` // 下载后的文件名
	Size      int64  `json:"size"`
	SHA       string `json:"sha"`     // sha256 或 sha1
	SHAKind   string `json:"shaKind"` // sha256 / sha1
	URL       string `json:"url"`
	JavaMajor int    `json:"javaMajor"` // 这个版本要求的 Java；0 表示拿不到

	// Label 构建行的显示名。留空时前端按「#编号」显示，没编号的显示「官方」——
	// Fabric 那种「构建 = Loader 版本」的，编号没有意义，得让它自己给个说法。
	Label string `json:"label,omitempty"`
	// Tag 上游对这个构建的**身份**标识，用来在下载时重新定位到同一个东西。
	// Forge 必须有它：它的版本号不是整数（49.0.30、14.23.5.2860），塞不进 ID，
	// 而「列表里第几个」这种定位方式会在上游加了新构建之后悄悄错位 ——
	// 用户选的是 A，下到的却是 B。留空表示用 ID 就够（Paper / Velocity）。
	Tag string `json:"tag,omitempty"`
	// JavaFrom 说明 JavaMajor 是哪来的：mojang=官方元数据（权威）、
	// versionline=按版本线推断（上游不提供）。留空 = 前端按原口径兜底。
	JavaFrom string `json:"javaFrom,omitempty"`
}

// Source 界面上要展示的一种服务端类型
type Source struct {
	Type      Kind   `json:"type"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	// Note 不可用时说清卡在哪；可用时说明这个源的镜像状况。
	// 「装了但不起作用」比明确说「没有」更伤信任，所以这里必须写实话。
	Note string `json:"note"`
}

// Mirror 下载源。只收真正验证过能用的 —— 挂一个 404 的镜像是负体验。
type Mirror struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// For 这个镜像适用于哪些类型；空表示全部
	For []Kind `json:"for,omitempty"`
	// MojangBase 换掉原版元数据的基地址（BMCLAPI 用）。不给前端看。
	MojangBase string `json:"-"`
}

var Mirrors = []Mirror{
	{ID: "direct", Label: "官方直连", For: nil},
	{
		ID: "bmclapi", Label: "BMCLAPI 镜像（仅原版）",
		For:        []Kind{KindVanilla},
		MojangBase: "https://bmclapi2.bangbang93.com",
	},
}

func mirrorByID(id string) Mirror {
	for _, m := range Mirrors {
		if m.ID == id {
			return m
		}
	}
	return Mirrors[0]
}

// MirrorsFor 返回某个类型可用的镜像。给 Paper 挂 BMCLAPI 是没意义的（那路径 404），
// 所以这里按类型过滤，界面上也就不会出现「选了镜像反而更糟」的情况。
func MirrorsFor(k Kind) []Mirror {
	var out []Mirror
	for _, m := range Mirrors {
		if len(m.For) == 0 {
			out = append(out, m)
			continue
		}
		for _, f := range m.For {
			if f == k {
				out = append(out, m)
				break
			}
		}
	}
	return out
}

// Sources 返回支持的服务端类型。没接的也列出来并说明原因 ——
// 界面上一句「暂不支持」比整块消失更容易让人理解。
func Sources() []Source {
	return []Source{
		{Type: KindPaper, Label: "Paper（含 Paper 系衍生）", Available: true,
			Note: "官方源直连可用；Paper 没有可用的国内镜像，走直连。"},
		{Type: KindVanilla, Label: "原版 Vanilla", Available: true,
			Note: "可用官方源，也可以切 BMCLAPI 镜像。"},
		{Type: KindFabric, Label: "Fabric", Available: true,
			Note: "官方源直连可用。给的是一百多 KB 的启动器 jar，首次启动它会自己去下依赖 —— 所以第一次开服会慢一些，而且必须联网。"},
		{Type: KindVelocity, Label: "Velocity", Available: true,
			Note: "和 Paper 用同一个源站，直连可用。"},
		{Type: KindForge, Label: "Forge", Available: true,
			Note: "官方源直连可用。它和别家不一样：下的是安装器，下完还要在本机跑一遍才算装好 —— " +
				"这一步会再下几十到一百多 MB 的库文件（实测 1.20.4 全流程 34 秒、1.12.2 31 秒）。" +
				"所以会比别的类型慢一截，但只慢这一次。"},
		{Type: KindNeoForge, Label: "NeoForge", Available: false,
			Note: "官方 maven 在本机连不通（只解析出 IPv6 地址，连接被重置）。Forge 那台是通的，NeoForge 换成自己独立的主机了，两者不能互相参照。"},
		{Type: KindBungeeCord, Label: "BungeeCord", Available: false,
			Note: "源站能连通但实测很慢（26 MB 的 jar 两分钟只跑到 9.9 MB），而且它是滚动构建、没有游戏版本概念，暂时没接。"},
	}
}

// SourceOf 查某个类型对应的描述。第二个返回值是「认不认识这个类型」——
// 认识了但没接实现，和压根不认识（拼错/前端传错），给用户的解释完全不一样。
func SourceOf(k Kind) (Source, bool) {
	for _, s := range Sources() {
		if s.Type == k {
			return s, true
		}
	}
	return Source{}, false
}

// 两个客户端，别合并 —— 它们的超时语义完全不同，混用的代价是「大文件必失败」。
//
// ⚠ 踩过的坑：http.Client.Timeout 是**整个请求**的时限，包含读完 response body。
// 早先只有这一个客户端、Timeout=30s，于是「下载服务端 jar」也被限死在 30 秒内 ——
// 一台 49 MB 的 Paper 在 19 MB/s 的机房网络 3 秒就完了，所以一直没暴露；
// 换成家宽（2 MB/s 要 25 秒、1 MB/s 要 50 秒）就会稳定报「下载失败」。
// 更坏的是它看起来像源站的问题，排查会先怀疑人家。
var apiClient = &http.Client{
	Timeout: 30 * time.Second, // 元数据接口：小、快，30 秒足够，超时就是真有问题
	Transport: &http.Transport{
		MaxIdleConns:        8,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 15 * time.Second,
	},
}

// dlClient 下载专用：**不设总超时**（几十 MB 的文件不该有时限），
// 只靠阶段性的超时兜住「连不上 / 不回 header」，以及用户点取消走的 ctx。
var dlClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // 连上了但迟迟不给 header，当成挂了
	},
}

func newRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ServerBox/0.1 (Minecraft server manager)")
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// getJSON 拉一个 JSON。错误信息带上 HTTP 状态码 ——
// 只报「请求失败」的话，403 和 404 在排查时完全是两回事。
func getJSON(url string, out any) error {
	req, err := newRequest(url)
	if err != nil {
		return err
	}
	resp, err := apiClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s 连不上：%w", hostOf(url), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s 返回 HTTP %d（%s）", hostOf(url), resp.StatusCode, url)
	}
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s 返回的内容解析失败：%w", hostOf(url), err)
	}
	return nil
}

func hostOf(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
	}
	if i := strings.IndexAny(u, "/?"); i >= 0 {
		u = u[:i]
	}
	return u
}

// sortVersionsNewestFirst 按版本号从新到旧。
// 上游给的顺序不能信 —— Paper 的版本族是倒序、族内也是倒序，但原版 manifest 又是另一套，
// 与其记住谁是什么顺序，不如自己统一排一遍。
func sortVersionsNewestFirst(vs []Version) {
	sort.SliceStable(vs, func(i, j int) bool {
		return compareVersion(vs[i].ID, vs[j].ID) > 0
	})
}

// compareVersion 比较两个 MC 版本号。返回 1 / 0 / -1。
// 预发布后缀（-pre1 / -rc2 / -snapshot-1）永远小于同名正式版。
func compareVersion(a, b string) int {
	an, ap := splitVersion(a)
	bn, bp := splitVersion(b)
	n := len(an)
	if len(bn) > n {
		n = len(bn)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(an) {
			x = an[i]
		}
		if i < len(bn) {
			y = bn[i]
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	switch {
	case ap == "" && bp != "":
		return 1 // 正式版 > 预发布
	case ap != "" && bp == "":
		return -1
	case ap > bp:
		return 1
	case ap < bp:
		return -1
	}
	return 0
}

func splitVersion(s string) ([]int, string) {
	main, pre := s, ""
	for i, c := range s {
		if c == '-' {
			main, pre = s[:i], s[i+1:]
			break
		}
	}
	var nums []int
	for _, part := range strings.Split(main, ".") {
		n := 0
		ok := len(part) > 0
		for _, c := range part {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			n = n*10 + int(c-'0')
		}
		if !ok {
			break
		}
		nums = append(nums, n)
	}
	return nums, pre
}
