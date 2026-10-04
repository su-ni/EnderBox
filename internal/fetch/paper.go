package fetch

import (
	"fmt"
	"strings"
)

// PaperMC 的 v3 API。
// ⚠ 域名是 fill.papermc.io，不是 api.papermc.io（后者 v3 路径直接 403，踩过）。
//
// Paper 和 Velocity 是**同一个源站、同一套响应结构**（实测 2026-10-04）：
// 都是 /projects/{id}、都是 downloads["server:default"]、都带 sha256 和 size。
// 所以这里做成项目参数化，而不是给 Velocity 复制一份 —— 复制出来的那份迟早会跟这边跑偏。
const paperMCBase = "https://fill.papermc.io/v3"

// paperProjectResp 项目信息：versions 是个「版本族 → 具体版本列表」的 map。
// 族名是 1.21 / 26.3 / 4.0.0 这种，族内既有正式版也有 rc / pre / SNAPSHOT，得自己筛。
type paperProjectResp struct {
	Project struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"project"`
	Versions map[string][]string `json:"versions"`
}

// paperBuild 一次构建。v3 的下载项键名是 "server:default"（v2 时代叫 application）。
type paperBuild struct {
	ID        int    `json:"id"`
	Time      string `json:"time"`
	Channel   string `json:"channel"`
	Downloads map[string]struct {
		Name      string            `json:"name"`
		Size      int64             `json:"size"`
		URL       string            `json:"url"`
		Checksums map[string]string `json:"checksums"`
	} `json:"downloads"`
}

// paperMCVersions 拉某个项目的版本列表（Paper / Velocity 共用）。
// 只保留正式版：rc / pre / SNAPSHOT 是给测试用的，摆在新手面前只会让人选错。
func paperMCVersions(project string) ([]Version, error) {
	var d paperProjectResp
	if err := getJSON(paperMCBase+"/projects/"+project, &d); err != nil {
		return nil, err
	}
	var out []Version
	for family, list := range d.Versions {
		for _, v := range list {
			if strings.Contains(v, "-") {
				continue // -rc-1 / -pre2 / -SNAPSHOT
			}
			out = append(out, Version{ID: v, Family: family, Prerelease: false, Stable: true})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 返回的版本列表是空的（API 结构可能又变了，检查 %s/projects/%s）",
			project, paperMCBase, project)
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// paperMCBuilds 某个版本的构建列表，新的在前（Paper / Velocity 共用）。
// 全量有 200 多个，界面用不上 —— 只给最近 limit 个，但要保留 channel 让人能分辨
// 哪几个是测试版（Paper 偶尔会有 BETA 构建放出来）。
func paperMCBuilds(project, version string, limit int) ([]Build, error) {
	var raw []paperBuild
	url := fmt.Sprintf("%s/projects/%s/versions/%s/builds", paperMCBase, project, version)
	if err := getJSON(url, &raw); err != nil {
		return nil, err
	}
	var out []Build
	for _, b := range raw {
		dl, ok := b.Downloads["server:default"]
		if !ok {
			continue // 结构变了的话宁可少给一个，也别给个空 URL 让人点下去
		}
		out = append(out, Build{
			ID:        b.ID,
			Channel:   b.Channel,
			Time:      b.Time,
			Name:      dl.Name,
			Size:      dl.Size,
			SHA:       dl.Checksums["sha256"],
			SHAKind:   "sha256",
			URL:       dl.URL,
			JavaMajor: 0, // 上游不给：Paper 由上层查原版元数据补，Velocity 走 velocityJava()
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("版本 %s 没有可下载的构建", version)
	}
	// API 已经是新的在前，但不依赖它 —— 显式按 id 降序排一次
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID > out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ── Paper ──

func PaperVersions() ([]Version, error) { return paperMCVersions("paper") }

func PaperBuilds(version string, limit int) ([]Build, error) {
	return paperMCBuilds("paper", version, limit)
}

// PaperLatestBuild 取最新一个构建（自动配置用的）
func PaperLatestBuild(version string) (Build, error) {
	bs, err := PaperBuilds(version, 1)
	if err != nil {
		return Build{}, err
	}
	return bs[0], nil
}

// ── Velocity ──
//
// 注意 channel 取值和 Paper 不一样：Paper 是 STABLE / BETA / ALPHA，
// Velocity 是 RECOMMENDED / STABLE（实测 3.5.1 是 RECOMMENDED，3.4.0 和 4.2.0 是 STABLE）。
// 前端判定「稳定」时两个都得放行，否则会把推荐版误报成测试版。

func VelocityVersions() ([]Version, error) { return paperMCVersions("velocity") }

func VelocityBuilds(version string, limit int) ([]Build, error) {
	bs, err := paperMCBuilds("velocity", version, limit)
	if err != nil {
		return nil, err
	}
	jm := velocityJava(version)
	for i := range bs {
		bs[i].JavaMajor = jm
		bs[i].JavaFrom = "versionline"
	}
	return bs, nil
}

func VelocityLatestBuild(version string) (Build, error) {
	bs, err := VelocityBuilds(version, 1)
	if err != nil {
		return Build{}, err
	}
	return bs[0], nil
}

// velocityJava Velocity 要哪个 Java 大版本。
//
// ⚠ 上游 API **不提供**这个信息，所以只能自己定。下面这三档不是猜的，
// 是把 jar 下下来读 class 文件版本（major - 44 = Java 版本）实测出来的：
//
//	Velocity 3.4.0 → class major 61 → Java 17
//	Velocity 3.5.1 → class major 65 → Java 21   （当时的 RECOMMENDED）
//	Velocity 4.2.0 → class major 69 → Java 25
//
// 三档跨度恰好说明「取一个固定值」是错的。新增版本线而这里算不出来时，
// 宁可返回 0（前端会如实说「拿不到权威值」），也不要往上顶一个偏小的数 ——
// 偏小会让用户下一个起不来的包，正是这次要根除的那类问题。
func velocityJava(version string) int {
	parts := strings.SplitN(version, ".", 3)
	var major, minor int
	_, _ = fmt.Sscanf(parts[0], "%d", &major)
	if len(parts) > 1 {
		_, _ = fmt.Sscanf(parts[1], "%d", &minor)
	}
	switch {
	case major >= 4:
		return 25
	case major == 3 && minor >= 5:
		return 21
	case major == 3:
		return 17
	case major == 1:
		return 8
	}
	return 0
}
