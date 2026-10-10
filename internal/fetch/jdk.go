package fetch

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Temurin JDK 的一键下载。
//
// 为什么是 Eclipse Temurin：它是 Adoptium 出的、MC 圈最通用的 JDK 发行版
// （PCL / HMCL 默认拉的也是它），Windows 上给的是 zip、解压即用，
// 不需要安装器 —— 我们绝不往用户机器上弹一个安装向导。
//
// ⚠ 实测记录（2026-10-04，别凭记忆改）：
//   - api.adoptium.net 本机连不通（域名解析到 Cloudflare，连接被重置）
//   - 清华 / 北外（Adoptium 路径）同样连不通（HTTP 000）
//   - 阿里云没有 adoptium 路径（404）
//   - 南大 mirrors.nju.edu.cn/adoptium  —— **通，而且是最新的**
//     （实测 17.0.20.1 / 21.0.12.1 / 25.0.4.1），但它只镜像了产物，没有 .sha256.txt
//   - 中科大 mirrors.ustc.edu.cn/adoptium/releases/… —— 通、带 .sha256.txt，
//     但它的 LatestRelease **明显滞后**（Java 17 只到 17.0.9，差了两年的安全更新）
//
// 所以取「南大优先（版本新最重要）、中科大兜底」，校验和按「有就校、没有就不装」处理：
// 南大那份虽然没有 .sha256.txt，但 zip 内部自带每条目的 CRC32，
// 解压时会全量读一遍 —— 下坏了的包在那一步必然报错（见 daemon/archive.go）。

// JDKOption 可选的主版本。这一步是静态的 —— 真正「最新是哪个小版本」
// 要去问索引页，放在用户选定之后再查，免得一进页面就打五个索引页 + 五次大小探测。
type JDKOption struct {
	Major int  `json:"major"`
	LTS   bool `json:"lts"`
	// ForMC 这个 Java 适合跑哪些 MC 版本。选 Java 的人八成是因为「服务端说缺 Java」，
	// 所以这一列必须按 MC 版本说人话，而不是列 JDK 的发布年份。
	ForMC string `json:"forMc"`
}

// JDKRelease 一次具体的 JDK 下载。字段全部来自索引页实况，不编造。
type JDKRelease struct {
	Major   int    `json:"major"`
	Version string `json:"version"` // 21.0.12.1+1（从文件名还原，不是猜的）
	Name    string `json:"name"`    // OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.zip
	Size    int64  `json:"size"`    // 0 = 没问到（界面按「大小未知」显示）
	SHA     string `json:"sha"`     // 空 = 这个镜像没提供校验和
	URL     string `json:"url"`
	Mirror  string `json:"mirror"`
	// MirrorLabel 如实告诉用户这份是从哪个镜像下的 —— 换了源之后出问题才知道该找谁。
	MirrorLabel string `json:"mirrorLabel"`
}

// jdkMirror 一个 JDK 镜像的**索引页**模板。文件名从索引页里读，
// 而不是自己拼 —— 各家版本号写法不一样（8u504b01 / 21.0.12.1_1），
// 拼接规则一改就是 404，而读索引页是跟着源站走的。
type jdkMirror struct {
	ID    string
	Label string
	// Index 里的 %d = 主版本、%s = 架构、%s = 系统
	Index string
}

var jdkMirrors = []jdkMirror{
	{ID: "nju", Label: "南京大学镜像", Index: "https://mirrors.nju.edu.cn/adoptium/%d/jdk/%s/%s/"},
	{ID: "ustc", Label: "中科大镜像", Index: "https://mirrors.ustc.edu.cn/adoptium/releases/temurin%d-binaries/LatestRelease/"},
}

// JDKMajors 列出来的主版本。只列还在维护的 GA 线 ——
// 9~16 已经 EOL，摆出来只会让人选到一个没人发安全补丁的 JDK。
func JDKMajors() []JDKOption {
	return []JDKOption{
		{Major: 8, LTS: true, ForMC: "1.16.5 及更早的服务端"},
		{Major: 11, LTS: true, ForMC: "1.17 之前的代理端、少数老服务端"},
		{Major: 17, LTS: true, ForMC: "1.17 ~ 1.20.4"},
		{Major: 21, LTS: true, ForMC: "1.20.5 及更新，现在最常用"},
		{Major: 25, LTS: true, ForMC: "最新一批服务端构建会要求它"},
	}
}

func jdkArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "aarch64"
	case "386":
		return "x32"
	}
	return "x64"
}

func jdkOSName() string {
	switch runtime.GOOS {
	case "windows":
		return "windows"
	case "darwin":
		return "mac"
	}
	return "linux"
}

// jdkExt 各系统用哪个归档。南大在 mac 上还给了 .pkg、windows 上给了 .msi ——
// 那两个都得跑安装向导，我们只要自己能解开的：Windows=zip、其余=tar.gz。
func jdkExt() string {
	if runtime.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// jdkFileRe 从索引页里抠出 jdk 归档的文件名。
// 文件名形如：
//
//	OpenJDK21U-jdk_x64_windows_hotspot_21.0.12.1_1.zip
//	OpenJDK8U-jdk_x64_windows_hotspot_8u504b01.zip
//	OpenJDK21U-jdk_x64_linux_hotspot_21.0.12.1_1.tar.gz
//
// ⚠ 版本段**不能**写死成 \d+\.\d+ —— Java 8 那条是 8u504b01，
// 只认点分就会把整个 Java 8 漏掉（实测确实漏过）。
// 子匹配：1=文件名，2=版本段。
// ⚠ 两处坑，都是单元测试反馈过来的真问题，别改回去：
//
//	① 版本段不能写死成 \d+\.\d+ —— Java 8 是 8u504b01，只认点分就把整个 Java 8 漏掉。
//	② **前缀不能要求是个引号**。南大的索引页是相对链接（href="OpenJDK21U-…zip"），
//	   引号紧挨着 OpenJDK；但中科大给的是完整路径
//	   （href="/adoptium/…/OpenJDK21U-…zip"），引号在 /adoptium 前面。
//	   要求前缀引号 = 中科大这条回落永远匹配不上 = 它等于根本不存在。
//	   同理尾部必须要求引号：否则 GET 到的第一行 .sha256.txt
//	   （…21.0.9_10.zip.sha256.txt）会被当成归档本身。
func jdkFileRe(arch, osName, ext string) *regexp.Regexp {
	return regexp.MustCompile(`(OpenJDK\d+U-jdk_` + regexp.QuoteMeta(arch) + `_` +
		regexp.QuoteMeta(osName) + `_hotspot_([0-9][0-9A-Za-z._]*)` + regexp.QuoteMeta(ext) + `)"`)
}

const jdkCacheTTL = 10 * time.Minute

var jdkCache struct {
	mu  sync.Mutex
	at  map[int]time.Time
	rel map[int]JDKRelease
}

// JDKLatest 查某个主版本目前能下的那一份。结果缓存 10 分钟 ——
// 用户会在几个主版本之间来回点，每点一次都去问源站没必要。
func JDKLatest(major int) (JDKRelease, error) {
	if major <= 0 {
		return JDKRelease{}, fmt.Errorf("主版本号不对：%d", major)
	}
	jdkCache.mu.Lock()
	if rel, ok := jdkCache.rel[major]; ok && time.Since(jdkCache.at[major]) < jdkCacheTTL {
		jdkCache.mu.Unlock()
		return rel, nil
	}
	jdkCache.mu.Unlock()

	rel, err := resolveJDK(major)
	if err != nil {
		return JDKRelease{}, err
	}

	jdkCache.mu.Lock()
	if jdkCache.rel == nil {
		jdkCache.rel = map[int]JDKRelease{}
		jdkCache.at = map[int]time.Time{}
	}
	jdkCache.rel[major] = rel
	jdkCache.at[major] = time.Now()
	jdkCache.mu.Unlock()
	return rel, nil
}

func resolveJDK(major int) (JDKRelease, error) {
	arch, osName, ext := jdkArch(), jdkOSName(), jdkExt()
	re := jdkFileRe(arch, osName, ext)

	var tried []string
	for _, m := range jdkMirrors {
		idx := fmt.Sprintf(m.Index, major, arch, osName)
		body, err := getText(idx)
		if err != nil {
			tried = append(tried, m.Label+"："+err.Error())
			continue
		}
		hit := re.FindStringSubmatch(body)
		if hit == nil {
			tried = append(tried, fmt.Sprintf("%s：索引页里没有 %s/%s 的 jdk 归档", m.Label, osName, arch))
			continue
		}
		rel := JDKRelease{
			Major:       major,
			Name:        hit[1],
			Version:     prettyJDKVersion(hit[2]),
			URL:         idx + hit[1],
			Mirror:      m.ID,
			MirrorLabel: m.Label,
		}
		// 大小探测放在这里（一次带 Range 的短请求）。拿不到就留 0 ——
		// 界面会显示「大小未知」，而不是编一个数字出来骗人。
		rel.Size = probeSize(rel.URL)
		// 校验和：中科大给、南大没给。没有就空着，让下载器跳过校验并如实标注。
		rel.SHA = jdkChecksum(rel.URL)
		return rel, nil
	}
	return JDKRelease{}, fmt.Errorf("没有可用的 JDK 下载源。%s", strings.Join(tried, "；"))
}

// prettyJDKVersion 把文件名里的版本段还原成人看的写法。
// 21.0.12.1_1 → 21.0.12.1+1（Adoptium 自己的写法就是这个）。
// Java 8 那种 8u504b01 保持原样 —— 硬套加号规则只会得到 8u504b01 这种四不像。
func prettyJDKVersion(v string) string {
	if strings.Contains(v, "u") {
		return v
	}
	return strings.Replace(v, "_", "+", 1)
}

// probeSize 问一次文件多大：Range: bytes=0-0 只会拿回 1 个字节，总量在 Content-Range 里。
// 任何一步不顺就返回 0（= 不知道），调用方按「大小未知」处理。
func probeSize(url string) int64 {
	req, err := newRequest(url)
	if err != nil {
		return 0
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := dlClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusPartialContent {
		return 0
	}
	return totalFromContentRange(resp)
}

// jdkChecksum 试着取 `<文件>.sha256.txt`。格式是一行「<64 位十六进制>  <文件名>」。
// 拿不到（404）返回空串 —— 这不算错误，南大确实不提供。
func jdkChecksum(fileURL string) string {
	body, err := getText(fileURL + ".sha256.txt")
	if err != nil {
		return ""
	}
	fields := strings.Fields(body)
	if len(fields) == 0 {
		return ""
	}
	h := strings.ToLower(fields[0])
	if len(h) != 64 {
		return ""
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return h
}
