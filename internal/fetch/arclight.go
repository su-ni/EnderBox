package fetch

import (
	"fmt"
	"regexp"
	"strings"
)

// Arclight —— Forge / Fabric / NeoForge 系的混合端（Mixin 把 Bukkit API 注进模组环境）。
//
// ⚠ 它的官方发布**只在 GitHub Releases**，而本机 GitHub 直连是死的
// （实测 2026-10-06：443 / 22 全不通，api.github.com 与 github.com 都是 HTTP 000）。
// 官网站 arclight.izzel.io 只是个落地页 —— 版本列表写死在它的 JS 包里，
// 下载按钮指回 GitHub，没有任何自己的接口（CDP 抓过，一个数据请求都没有）。
//
// 所以走第三方加速代理转一道。实测（2026-10-06）：
//
//	gh-proxy.com  代理 release 附件 → 206 ✓   代理 api.github.com → 200 + 真 JSON ✓
//	ghfast.top    代理附件 206 ✓             代理 api.github.com → 403 ✗
//	ghproxy.net   代理附件 206 ✓             代理 api.github.com → 403 ✗
//	hub.gitmirror.com / github.moeyy.xyz / gh.llkk.cc / ghproxy.cc / gh.xxooo.cf → 000
//
// 只有 gh-proxy.com 两样都能代理，所以用它。⚠ 它是**第三方代理**，
// 不是官方通道：理论上能看到甚至改动这个 jar。界面上必须如实说清楚，
// 不能写「官方直连」—— 那是撒谎。
//
// 另一条备选是 SourceForge 上的 arclight.mirror（大厂 CDN，可下载，
// 实测下到的包 Implementation-Version 与真机跑出来的一致）。没用它做数据源，
// 是因为它的文件页要抓 HTML、翻页还可能不全 —— JSON 比网页可靠。
const (
	arclightProxy = "https://gh-proxy.com/"
	arclightAPI   = "https://api.github.com/repos/IzzelAliz/Arclight/releases"
)

// arclightAssetRe 认「Forge 变体的 release 附件」名字。实测见过的三种形状：
//
//	arclight-forge-1.20.1-1.0.6.jar
//	arclight-forge-1.21.1-1.0.1-8ec9529.jar      ← 尾部多了 7 位 commit 短哈希
//	arclight-forge-1.21.11-1.0.0-SNAPSHOT-8a57510.jar
//
// ⚠ 必须把 fabric / neoforge 变体挡在外面：它们叫 arclight-fabric-… / arclight-neoforge-…，
// 而面板把 Arclight 当 Forge 系管（启动方式、库文件、mods 目录都是 Forge 那套），
// 拿一个 fabric 变体下来会和实例类型对不上。靠 (?:forge-)? 后面紧跟数字来区分：
// "neoforge-" 的第一个字符是 n，匹配不上，自然被排除。
var arclightAssetRe = regexp.MustCompile(
	`^arclight-(?:forge-)?(\d+(?:\.\d+){1,2})-(.+?)(?:-[0-9a-f]{7,8})?\.jar$`)

// ghAsset GitHub Release 的一个附件（一个 jar 就是一个附件）。
type ghAsset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Digest 形如 "sha256:abc…"。⚠ 只有新一些的附件才有，老的是 null ——
	// 这种情况就不带校验和，不能编一个出来（编了会在校验时误报失败）。
	Digest    string `json:"digest"`
	UpdatedAt string `json:"updated_at"`
	// BrowserDownloadURL 是**官方 GitHub 地址**，本机下不动，必须外面套代理。
	BrowserDownloadURL string `json:"browser_download_url"`
}

// ghRelease GitHub Releases 里我们用得到的字段。
type ghRelease struct {
	TagName string    `json:"tag_name"`
	Name    string    `json:"name"`
	Assets  []ghAsset `json:"assets"`
}

// ArclightVersions 能选哪些游戏版本。**从真实的 release 附件里推出来的**，
// 不是写死一张表 —— 写死的表在上游发新版本后就会开始撒谎。
//
// 只有 Forge 变体算数：某些版本线（比如 1.21）上游只出了 fabric / neoforge 变体，
// 那时这个版本就不会出现在列表里 —— 这比列出来、点进去发现下不了要好。
func ArclightVersions() ([]Version, error) {
	rel, err := arclightReleases()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Version
	for _, mc := range arclightVersionsOf(rel) {
		if seen[mc] {
			continue
		}
		seen[mc] = true
		out = append(out, Version{ID: mc})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Arclight 没有可用的 Forge 变体（数据来自 %s）", arclightAPI)
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// ArclightBuilds 某个游戏版本下的构建。
//
// Arclight 没有「构建号」这个概念 —— 一个版本线发一次就给一个版本号（1.0.6 这种），
// 所以 Build.ID 一律是 0，身份挂在 Tag 上（release 的 tag，形如 Trials/1.0.6），
// 下载时靠它回源定位。⚠ 别拿 ID 当身份：同一个游戏版本可能有多个版本号（1.20.4 有 1.0.4 和 1.0.3），
// 全填 0 之后「列表里第几个」就成了唯一区别，而上游一加新发布这个下标就会错位。
func ArclightBuilds(mc string, limit int) ([]Build, error) {
	mc = strings.TrimSpace(mc)
	if mc == "" {
		return nil, fmt.Errorf("缺少游戏版本")
	}
	rel, err := arclightReleases()
	if err != nil {
		return nil, err
	}
	out := arclightBuildsOf(rel, mc)
	if len(out) == 0 {
		return nil, fmt.Errorf("Arclight 没有为 %s 出过 Forge 变体（1.21 这类版本线只发了 Fabric/NeoForge）", mc)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ArclightBuildByTag 按 release tag 精确定位一个构建。
func ArclightBuildByTag(tag, mc string) (Build, error) {
	rel, err := arclightReleases()
	if err != nil {
		return Build{}, err
	}
	for _, b := range arclightBuildsOf(rel, mc) {
		if b.Tag == tag {
			return b, nil
		}
	}
	return Build{}, fmt.Errorf("Arclight %s 下找不到构建 %s", mc, tag)
}

// ArclightLatestBuild 取最新一个构建。
func ArclightLatestBuild(mc string) (Build, error) {
	bs, err := ArclightBuilds(mc, 1)
	if err != nil {
		return Build{}, err
	}
	return bs[0], nil
}

// arclightReleases 经代理拉 releases。⚠ per_page 取 100（GitHub 的上限）——
// 这个仓库有 80 多个 tag，只拿前几十个会让老版本线（1.16.x / 1.17.x）从列表里消失。
func arclightReleases() ([]ghRelease, error) {
	var raw []ghRelease
	if err := getJSON(arclightProxy+arclightAPI+"?per_page=100", &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("GitHub 加速代理返回了空的 release 列表（%s）", arclightProxy)
	}
	return raw, nil
}

// ── 纯函数部分：不联网，单测盯着这几个 ──

// arclightAssetOf 从一个附件名里拆出 (游戏版本, 加载器版本)。拆不出返回 ok=false。
func arclightAssetOf(name string) (mc, ver string, ok bool) {
	m := arclightAssetRe.FindStringSubmatch(name)
	if len(m) != 3 {
		return "", "", false
	}
	return m[1], m[2], true
}

// arclightVersionsOf 从 releases 里推出有哪些游戏版本（去重，按出现顺序）。
func arclightVersionsOf(rel []ghRelease) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rel {
		for _, a := range r.Assets {
			mc, _, ok := arclightAssetOf(a.Name)
			if !ok || seen[mc] {
				continue
			}
			seen[mc] = true
			out = append(out, mc)
		}
	}
	return out
}

// arclightBuildsOf 挑出某个游戏版本的全部 Forge 构建，新的在前。
//
// ⚠ 排序口径是 release 的发布时间，不是版本号字符串 —— 1.0.12 和 1.0.6 按字符串比
// 会得出「1.0.6 更新」这种荒唐结论。上游给的顺序是从新到旧，直接沿用，
// 但只在同一版本线内保持（跨版本线本来就不会混在一起）。
func arclightBuildsOf(rel []ghRelease, mc string) []Build {
	var out []Build
	for _, r := range rel {
		for _, a := range r.Assets {
			amc, ver, ok := arclightAssetOf(a.Name)
			if !ok || amc != mc {
				continue
			}
			sha, kind := splitDigest(a.Digest)
			out = append(out, Build{
				ID:      0, // 没有构建号，身份看 Tag
				Tag:     r.TagName,
				Time:    a.UpdatedAt,
				Name:    a.Name,
				Size:    a.Size,
				SHA:     sha,
				SHAKind: kind,
				URL:     arclightProxy + a.BrowserDownloadURL,
				// 上游不给 Java 需求，按版本线兜底
				JavaMajor: javaFallback(mc),
				JavaFrom:  "versionline",
				Label:     ver,
			})
		}
	}
	return out
}

// splitDigest 把 GitHub 的 "sha256:abc…" 拆成 (哈希, 算法)。没有就返回空 ——
// 老附件确实是 null，这时候宁可不校验，也不能拿个编的哈希去误报「校验失败」。
func splitDigest(d string) (sha, kind string) {
	if d == "" {
		return "", ""
	}
	if i := strings.Index(d, ":"); i > 0 {
		return d[i+1:], d[:i]
	}
	return d, "sha256"
}
