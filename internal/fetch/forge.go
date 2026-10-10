package fetch

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Forge 的接口（实测 2026-10-04，全部 200）：
//
//	GET https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json
//	    → {"promos":{"1.20.4-recommended":"49.2.0","1.20.4-latest":"49.2.8", …}}  117 条 / 77 个 MC 版本
//	GET https://maven.minecraftforge.net/net/minecraftforge/forge/maven-metadata.xml
//	    → <versions><version>1.20.4-49.0.30</version>…</versions>                  5058 条
//	GET {maven}/<MC>-<Forge>/forge-<MC>-<Forge>-installer.jar                  200，1.20.4 是 5.5 MB，有 Content-Length
//	GET 同上 + ".sha1"                                                          200，40 位小写十六进制，不带文件名
//
// ⚠ 实测：maven.minecraftforge.net **通得了**（installer 5.5 MB / 10 秒，1.16.5 的 7.5 MB / 6 秒）。
// 别拿 NeoForge 那次「只解析出 IPv6、连接被重置」的结论套到 Forge 上 —— 那是另一台主机（maven.neoforged.net）。
//
// ⚠ 别拿 maven-metadata 当「版本列表」。5058 条里混着 35 条发错仓库的脏数据
// （1.10.2-12.18.1.2016-failtests、1.9.4-12.17.0.1919-EHUnit、1.7.10-10.13.3.1401-1710ls，
// 还有 1.12.2-14.23.4.2720-4627 这种四段版本号）。按「最后一个横杠」切分就会切出
// 1.9-12.16.0.1938 这种根本不存在的 MC 版本。就算全滤干净，剩 400 多个版本、
// 每个下面几十到几百个构建编号，对「不知道自己要选什么」的用户来说就是个垃圾场。
// → 版本表用 promos（Forge 官网下载页的下拉框就是这份数据），构建列表再回 maven 按版本取。
//
// ⚠ 别信 maven-metadata 里的 <latest>/<release>：实测写着 1.20.1-47.4.26，
// 而 1.21-51.0.33 早就发布了。上游自己维护的这个字段是过期的。
const (
	forgeMavenBase = "https://maven.minecraftforge.net/net/minecraftforge/forge"
	forgePromosURL = "https://files.minecraftforge.net/net/minecraftforge/forge/promotions_slim.json"
)

// Forge 跟别家最大的不同：**安装是两段式**。
// 下下来的是 installer（4~8 MB），必须再跑一遍它才产出能启动的东西：
//
//	java -jar forge-<版本>-installer.jar --installServer <实例目录>
//
// 实测 1.20.4 全流程 34 秒、1.12.2 全流程 31 秒（含下载 100 MB 左右的库文件）。
// 装完的产物分两代，区别写在 installer 内部的 install_profile.json 里（字段 spec），实测：
//
//	spec=1（1.17+）根目录出 forge-<版本>-shim.jar，真身在 libraries/ 里
//	spec=0（≤1.16.5）根目录出 forge-<版本>.jar（本身可启动）+ minecraft_server.<MC>.jar
//
// **两代在根目录都有一个「直接 -jar 就能起来的 jar」**，所以实例配置里只需要存
// 这个 jar 的文件名，启动逻辑不用为 Forge 写特例（实测 spec=1 只给 -jar shim 也能起，
// 根目录那份 shim 和 libraries 里的 md5 完全一致，是 installer 自己为保证 run.bat
// 的相对路径能用而复制的）。
//
// 但**到底出哪一个不由我们猜** —— 装完去目录里看实际有什么（见 daemon.InstallForge）。
// 版本线推断只用来在界面上提前说明，不用来做决定。

type forgePromos struct {
	Promos map[string]string `json:"promos"`
}

func forgePromoTable() (map[string]string, error) {
	var p forgePromos
	if err := getJSON(forgePromosURL, &p); err != nil {
		return nil, err
	}
	if len(p.Promos) == 0 {
		return nil, fmt.Errorf("Forge 没给出任何版本推荐信息（%s 的结构可能变了）", forgePromosURL)
	}
	return p.Promos, nil
}

// ForgeVersions Forge 能开的世界版本，从新到旧。
//
// 数据源是 promos 而不是 maven —— 理由见文件头。这个表里有 1.7.10 / 1.12.2 / 1.16.5 /
// 1.20.1 这些老整合包常驻版本，也有 26.3 这种新版号制的，覆盖面够用。
func ForgeVersions() ([]Version, error) {
	promos, err := forgePromoTable()
	if err != nil {
		return nil, err
	}
	out := promoMCVersions(promos)
	if len(out) == 0 {
		return nil, fmt.Errorf("Forge 的版本推荐表里没有能解析的条目（上游结构可能变了）")
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// promoMCVersions 从推荐表的键里抠出 MC 版本。
// 单独抽出来是为了能脱离网络测 —— 键的两种后缀（-recommended / -latest）
// 和「不认识的键一律不认」这条规则，是这份文件里最容易写错又最难靠肉眼发现的一处。
func promoMCVersions(promos map[string]string) []Version {
	seen := map[string]bool{}
	var out []Version
	for k := range promos {
		// 键只有两种后缀，别的一律不认 —— 猜错了会往版本列表里塞垃圾
		mc := strings.TrimSuffix(k, "-recommended")
		if mc == k {
			mc = strings.TrimSuffix(k, "-latest")
		}
		if mc == k || mc == "" {
			continue
		}
		if seen[mc] {
			continue
		}
		seen[mc] = true
		// promos 里的都是正式版（Forge 不为快照出推荐），所以这里不需要再标「稳不稳定」。
		out = append(out, Version{ID: mc})
	}
	return out
}

// forgeInstallTag 把 MC 版本 + Forge 版本拼成 maven 上的目录名，例如 1.20.4-49.0.30。
// 这也是 Forge 自己在 install_profile.json 的 path 字段里用的写法。
func forgeInstallTag(mcVersion, forgeVersion string) string {
	return mcVersion + "-" + forgeVersion
}

// ForgeBuilds 某个 MC 版本下能装的 Forge。
//
// 顺序是「先给两个官方推荐，再把 maven 里的历史版本补在后面」：
// 前两个对应 Forge 官网自己摆出来的「Download Recommended / Latest」，是绝大多数人该选的；
// 后面的历史构建留给「整合包锁了某个 Forge 版本」的情况 —— 有得选，但需要自己往下翻。
// **不这样排的话，第一个位置会落到一个随机的历史构建上，新手没有任何判断依据。**
func ForgeBuilds(mirror, gameVersion string, limit int) ([]Build, error) {
	promos, err := forgePromoTable()
	if err != nil {
		return nil, err
	}
	// 构建列表回 maven 取：promos 只告诉你哪两个值得推荐，不告诉你一共有哪些
	raw, err := getText(forgeMavenBase + "/maven-metadata.xml")
	if err != nil {
		return nil, err
	}
	all, err := forgeVersionsFromMetadata(raw, gameVersion)
	if err != nil {
		return nil, err
	}

	// Java 要求算一次就够（vanillaMeta 内部有缓存，但没必要在循环里反复进锁）
	javaMajor, javaFrom := forgeJava(mirror, gameVersion)

	out := assembleForgeBuilds(gameVersion, promos, all, limit, javaMajor, javaFrom, forgeMavenBaseFor(mirror))
	if len(out) == 0 {
		return nil, fmt.Errorf("Forge 没有为 %s 出过任何构建", gameVersion)
	}
	return out, nil
}

// assembleForgeBuilds 把「官方推荐表 + maven 版本列表」拼成界面要的那个顺序。
//
// 前两个是 Forge 官方升级表里标的推荐 / 最新，后面按版本号从新到旧排。
// 后段里出现比官方「最新」号更大的版本是正常的（出了但官方升级表还没标）——
// 界面上专门有一句话说明这件事，不要为了让列表「看起来整齐」把那些版本藏掉。
//
// mavenBase 是下载地址的基地址（可能是镜像）—— 传进来而不是写死，是为了让
// 「界面上看到的地址」和「真的会去下的地址」永远是同一个。
//
// 抽成纯函数是为了能测：这段排序是「新手第一个看到什么」的直接决定者，
// 而它错起来很安静 —— 列表照样出得来，只是默认选中的是另一个版本。
func assembleForgeBuilds(gameVersion string, promos map[string]string, mavenVersions []string, limit, javaMajor int, javaFrom, mavenBase string) []Build {
	recommended := promos[gameVersion+"-recommended"]
	latest := promos[gameVersion+"-latest"]

	var out []Build
	seen := map[string]bool{}
	add := func(forgeVer, label, channel string) {
		if forgeVer == "" {
			return
		}
		tag := forgeInstallTag(gameVersion, forgeVer)
		if seen[tag] {
			return
		}
		seen[tag] = true
		out = append(out, Build{
			// Forge 的版本号不是整数（49.0.30 / 14.23.5.2860），没法塞进 Build.ID。
			// ID 只当界面上的行号用，真正认身份靠 Tag —— 下载时就按 Tag 回源站取，
			// 不靠「列表里第几个」这种会在上游加新构建后错位的定位方式。
			ID:        len(out) + 1,
			Tag:       tag,
			Label:     label,
			Channel:   channel,
			Name:      "forge-" + tag + "-installer.jar",
			URL:       mavenBase + "/" + tag + "/forge-" + tag + "-installer.jar",
			SHAKind:   "sha1",
			JavaMajor: javaMajor,
			JavaFrom:  javaFrom,
			// Size / SHA 这里故意留空：几百个构建一个个去问 Content-Length 就是打人家源站。
			// 真正要下的那一个在 ForgeInstaller() 里补，界面显示「大小未知」是实话。
		})
	}

	add(recommended, "推荐 · "+recommended, "RECOMMENDED")
	add(latest, "最新 · "+latest, "LATEST")

	budget := limit - len(out)
	if budget < 0 {
		budget = 0
	}
	for _, fv := range mavenVersions {
		if budget == 0 {
			break
		}
		before := len(out)
		add(fv, "Forge "+fv, "")
		if len(out) > before {
			budget--
		}
	}
	return out
}

// ForgeInstaller 按 tag（形如 1.20.4-49.0.30）取一个能交给下载器的构建。
//
// mirror 决定从哪个 maven 下（BMCLAPI 镜像与官方逐字节一致，且更稳）。
// 校验和从**同一个** maven 取：它跟着文件走，从别处取反而要担心两边对不上。
//
// 顺手把源站的 sha1 取回来：**不能因为「省一次请求」就把校验和丢掉** ——
// 十几 MB 的库文件都要跑安装，先坏在这里最亏。这个请求同时也起到
// 「这个版本到底存不存在」的作用：拿不到 sha1 就直接拒绝，好过下到一半 404。
func ForgeInstaller(tag, mirror string) (Build, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return Build{}, fmt.Errorf("没有指定要装哪个 Forge 版本")
	}
	if !validForgeTag(tag) {
		return Build{}, fmt.Errorf("Forge 版本号看起来不对：%s（期望形如 1.20.4-49.0.30）", tag)
	}
	base := forgeMavenBaseFor(mirror)
	url := base + "/" + tag + "/forge-" + tag + "-installer.jar"

	sha, err := forgeInstallerSHA(tag, mirror)
	if err != nil {
		return Build{}, fmt.Errorf("取不到 %s 这个版本的校验和（%s）—— 版本号可能不存在，也可能是网络问题", tag, err)
	}

	mc := tag[:strings.LastIndex(tag, "-")]
	javaMajor, javaFrom := forgeJava("", mc)
	return Build{
		ID:        1,
		Tag:       tag,
		Label:     "Forge " + tag,
		Name:      "forge-" + tag + "-installer.jar",
		URL:       url,
		SHA:       sha,
		SHAKind:   "sha1",
		JavaMajor: javaMajor,
		JavaFrom:  javaFrom,
	}, nil
}

// forgeJava Forge 不在 installer 里写 Java 要求（实测 version.json 的 javaVersion 是 null），
// 但它跑的就是同一个 MC 版本的原版服务端，所以直接查 Mojang 对那个版本的要求 ——
// 权威值，而且比自己维护一张「哪个 Forge 要哪个 Java」的表可靠得多。
func forgeJava(mirror, mcVersion string) (int, string) {
	if vm, err := vanillaMeta(mirror, mcVersion); err == nil && vm.JavaVersion.MajorVersion > 0 {
		return vm.JavaVersion.MajorVersion, "mojang"
	}
	return javaFallback(mcVersion), "versionline"
}

// validForgeTag 只认 <MC版本>-<Forge版本>，两段都得是纯数字点号。
// 上游那 35 条脏数据（failtests / EHUnit / 四段版本号）就是被这道门挡在外面的。
func validForgeTag(tag string) bool {
	i := strings.LastIndex(tag, "-")
	if i <= 0 || i == len(tag)-1 {
		return false
	}
	return isDottedNumber(tag[:i]) && isDottedNumber(tag[i+1:])
}

func isDottedNumber(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
	}
	return true
}

// forgeVersionsFromMetadata 从 maven-metadata.xml 里挑出属于某个 MC 版本的 Forge 版本，从新到旧。
func forgeVersionsFromMetadata(raw, gameVersion string) ([]string, error) {
	var md struct {
		Versioning struct {
			Versions struct {
				Version []string `xml:"version"`
			} `xml:"versions"`
		} `xml:"versioning"`
	}
	if err := xml.Unmarshal([]byte(raw), &md); err != nil {
		return nil, fmt.Errorf("解析 maven-metadata.xml 失败：%w", err)
	}
	var out []string
	for _, v := range md.Versioning.Versions.Version {
		v = strings.TrimSpace(v)
		// 只留属于这个 MC 版本、且形状正确的条目
		if !strings.HasPrefix(v, gameVersion+"-") || !validForgeTag(v) {
			continue
		}
		out = append(out, v[len(gameVersion)+1:])
	}
	// 上游给的顺序不能信（实测 1.21-51.0.33 排在 1.21-51.0.32 前面，
	// 但跨版本段就乱了），自己排一遍
	sort.SliceStable(out, func(i, j int) bool { return compareDotted(out[i], out[j]) > 0 })
	return out, nil
}

// compareDotted 比较点号分隔的数字串（Forge 版本号可能是 4 段：14.23.5.2860）。
// 返回 1 / 0 / -1。逐段按数值比，不是按字符串 —— 否则 49.9 会被排在 49.10 后面。
func compareDotted(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		x, y := 0, 0
		if i < len(as) {
			_, _ = fmt.Sscanf(as[i], "%d", &x)
		}
		if i < len(bs) {
			_, _ = fmt.Sscanf(bs[i], "%d", &y)
		}
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	return 0
}

// forgeInstallerSHA 取安装器的 sha1。来源按顺序试：**官方 → 镜像**。
//
// 为什么校验和要优先跟 jar 同源：校验和的意义就是「证明这一份 jar 是好的」，
// 从别处取的值万一和这份文件对不上，我们就会把完好的版本判成坏的。
// 同源不会出现这种分歧。另一个源只当兜底 —— 大文件被掐断的时候，
// 那个 40 字节的小请求往往是通的（用户报的故障正是「8.8 MB 的安装器传到一半被 RST」）。
func forgeInstallerSHA(tag, mirror string) (string, error) {
	var lastErr error
	for _, u := range forgeSHAURLs(tag, mirror) {
		raw, err := getText(u)
		if err != nil {
			lastErr = err
			continue
		}
		sha := strings.TrimSpace(raw)
		if i := strings.IndexAny(sha, " \t"); i > 0 {
			sha = sha[:i] // 有的仓库会把文件名跟在后面
		}
		if len(sha) != 40 {
			lastErr = fmt.Errorf("%s 给的校验和不对：%q", hostOf(u), sha)
			continue
		}
		return sha, nil
	}
	return "", lastErr
}

// forgeSHAURLs 校验和的候选地址：先给跟 jar 同源的那个，再退到另一个。
// 抽出来是为了能测住这个顺序 —— 顺序反了不会报错，
// 只会在两边不一致时静默地拿错那个当权威值。
func forgeSHAURLs(tag, mirror string) []string {
	base := forgeMavenBaseFor(mirror)
	urls := []string{base + "/" + tag + "/forge-" + tag + "-installer.jar.sha1"}
	if base != forgeMavenBase {
		urls = append(urls, forgeMavenBase+"/"+tag+"/forge-"+tag+"-installer.jar.sha1")
	}
	return urls
}

// getText 拉一段纯文本（maven-metadata.xml 是 XML、.sha1 是一行十六进制，都不适合 getJSON）。
func getText(url string) (string, error) {
	resp, err := doWithRetry(apiClient, url)
	if err != nil {
		return "", fmt.Errorf("%s 连不上：%w", hostOf(url), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s 返回 HTTP %d（%s）", hostOf(url), resp.StatusCode, url)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", fmt.Errorf("读取 %s 的响失败：%w", hostOf(url), err)
	}
	return string(b), nil
}
