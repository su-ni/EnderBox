package fetch

import (
	"fmt"
	"strings"
)

// PaperMC 的 v3 API。
// ⚠ 域名是 fill.papermc.io，不是 api.papermc.io（后者 v3 路径直接 403，踩过）。
const paperBase = "https://fill.papermc.io/v3"

// paperProject 项目信息：versions 是个「版本族 → 具体版本列表」的 map。
// 族名是 1.21 / 26.3 这种，族内既有正式版也有 rc / pre 预发布版，得自己筛。
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
	Channel   string `json:"channel"` // STABLE / BETA / ALPHA
	Downloads map[string]struct {
		Name      string            `json:"name"`
		Size      int64             `json:"size"`
		URL       string            `json:"url"`
		Checksums map[string]string `json:"checksums"`
	} `json:"downloads"`
}

// PaperVersions 拉 Paper 支持的版本列表。
// 只保留正式版：rc / pre 是给测试用的，摆在新手面前只会让人选错。
func PaperVersions() ([]Version, error) {
	var d paperProjectResp
	if err := getJSON(paperBase+"/projects/paper", &d); err != nil {
		return nil, err
	}
	var out []Version
	for family, list := range d.Versions {
		for _, v := range list {
			pre := strings.Contains(v, "-")
			if pre {
				continue
			}
			out = append(out, Version{ID: v, Family: family, Prerelease: false, Stable: true})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Paper 返回的版本列表是空的（API 结构可能又变了，检查 %s/projects/paper）", paperBase)
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// PaperBuilds 某个版本的构建列表，新的在前。
// 全量有 200 多个，界面用不上 —— 只给最近 limit 个，但要保留 channel 让人能分辨
// 哪几个是测试版（Paper 偶尔会有 BETA 构建放出来）。
func PaperBuilds(version string, limit int) ([]Build, error) {
	var raw []paperBuild
	url := fmt.Sprintf("%s/projects/paper/versions/%s/builds", paperBase, version)
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
			JavaMajor: 0, // Paper 不提供；由上层去查同版本的原版元数据补上
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

// PaperLatestBuild 取最新一个构建（自动配置用的）
func PaperLatestBuild(version string) (Build, error) {
	bs, err := PaperBuilds(version, 1)
	if err != nil {
		return Build{}, err
	}
	return bs[0], nil
}
