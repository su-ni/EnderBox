package fetch

import (
	"fmt"
	"sort"
	"strings"
)

// Mohist —— Forge / NeoForge 系的「混合端」：模组和 Bukkit 插件能同时装。
//
// ⚠ 实测记录（2026-10-06，别凭记忆改）：这个项目**搬过家、还改了名**。
//
//	老地址 mohistmc.com/api/v2/projects/mohist/{mc}/builds  → 200 + **零字节**
//	老 maven / repo 域                                      → 404 / DNS 都解析不出
//	新地址 api.mohistmc.com/project/mohist/...              → 真的
//
// 「200 + 零字节」是废弃入口的典型形状（状态码对、Content-Type 对、body 是空的）。
// 判据：200 必须配 Content-Length > 0 才算真的能下。踩过一次，别再踩。
//
// 三件事实测拿到（都用真接口打过）：
//
//	GET {base}/project/mohist/versions
//	  → [{"name":"1.20.1"}, {"name":"1.21.1"}, ...]            顶层是**数组**
//	GET {base}/project/mohist/{mc}/builds
//	  → [{"id":921,"file_sha256":"ade896…","build_date":"2026-09-28T23:54:37Z",
//	      "commit":{"hash":"71a84ff…"},"loader":{"forge_version":"47.4.23"}}, ...]
//	GET {base}/project/mohist/{mc}/builds/{id}/download
//	  → 206，Content-Type: application/octet-stream
//	    Content-Disposition: attachment; filename="mohist-1.20.1-71a84ff7-server.jar"
//
// ⚠ 下载返回的**不是一个 jar 包的完整服务端**：Forge 本体塞在包里的 data/，
// 但 libraries/ 不在包内 —— 它靠包里的 installer.txt（24 个库，带 sha256）
// 在**第一次启动时**自己装进实例目录。所以首次开服会多花几十秒、必须联网，
// 这一点必须写进界面，否则用户会以为卡住了。
const mohistBase = "https://api.mohistmc.com"

type mohistVersion struct {
	Name string `json:"name"`
}

type mohistBuild struct {
	ID      int    `json:"id"`
	FileSHA string `json:"file_sha256"`
	Date    string `json:"build_date"`
	Commit  struct {
		Hash string `json:"hash"`
	} `json:"commit"`
	Loader struct {
		Forge    string `json:"forge_version"`
		NeoForge string `json:"neoforge_version"`
	} `json:"loader"`
}

// MohistVersions 能选哪些游戏版本。上游给的是数组（不是 map），别照抄 Paper 那套解析。
func MohistVersions() ([]Version, error) {
	var raw []mohistVersion
	if err := getJSON(mohistBase+"/project/mohist/versions", &raw); err != nil {
		return nil, err
	}
	var out []Version
	for _, v := range raw {
		if strings.TrimSpace(v.Name) == "" {
			continue
		}
		out = append(out, Version{ID: v.Name})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Mohist 返回的版本列表是空的（API 结构可能又变了，检查 %s/project/mohist/versions）", mohistBase)
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// MohistBuilds 某个游戏版本下的构建。Mohist 的 build 号是自增整数，越大越新。
//
// 每个构建绑定了一个 Forge 版本（loader.forge_version）—— 那才是用户真正关心的差异，
// 所以塞进 Label：列表里直接显示，不用点进去猜。
func MohistBuilds(mc string, limit int) ([]Build, error) {
	mc = strings.TrimSpace(mc)
	if mc == "" {
		return nil, fmt.Errorf("缺少游戏版本")
	}
	var raw []mohistBuild
	if err := getJSON(fmt.Sprintf("%s/project/mohist/%s/builds", mohistBase, mc), &raw); err != nil {
		return nil, err
	}
	return mohistBuildsFrom(raw, mc, limit)
}

// mohistBuildsFrom 是 MohistBuilds 的纯函数内核：不联网，单测盯着它。
//
// ⚠ 空列表必须**报错**，不能返回一个空切片。这里的教训是实的：
// 老域名那个废弃入口返回的是「HTTP 200 + 零字节」，如果解析层把「解出来是空的」
// 当成「这个版本没有构建」，界面就会显示成一个空列表、用户永远不知道自己撞上了废弃入口。
func mohistBuildsFrom(raw []mohistBuild, mc string, limit int) ([]Build, error) {
	var out []Build
	for _, b := range raw {
		if b.ID <= 0 {
			continue // 没有构建号的行没法回源定位，宁可不要
		}
		out = append(out, mohistBuildOf(mc, b))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Mohist 没有为 %s 出过任何构建（如果上游明明有，多半是 API 又换地址了：%s）", mc, mohistBase)
	}
	// 上游是新的在前，但不依赖它 —— 显式按 build 号降序排一次
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MohistBuild 按 build 号精确取一个 —— 下载时用它回源站定位，
// 而不是信「列表里第几个」（上游加了新构建之后那个下标就会悄悄错位）。
func MohistBuild(mc string, id int) (Build, error) {
	bs, err := MohistBuilds(mc, 0)
	if err != nil {
		return Build{}, err
	}
	for _, b := range bs {
		if b.ID == id {
			return b, nil
		}
	}
	return Build{}, fmt.Errorf("Mohist %s 下没有构建 %d", mc, id)
}

// MohistLatestBuild 取最新一个构建（「自动挑」和 preflight 一键补齐用）。
func MohistLatestBuild(mc string) (Build, error) {
	bs, err := MohistBuilds(mc, 1)
	if err != nil {
		return Build{}, err
	}
	return bs[0], nil
}

func mohistBuildOf(mc string, b mohistBuild) Build {
	label := ""
	if b.Loader.Forge != "" {
		label = fmt.Sprintf("#%d · Forge %s", b.ID, b.Loader.Forge)
	}
	return Build{
		ID:      b.ID,
		Time:    b.Date,
		Name:    fmt.Sprintf("mohist-%s-%d-server.jar", mc, b.ID),
		SHA:     b.FileSHA,
		SHAKind: "sha256",
		URL:     fmt.Sprintf("%s/project/mohist/%s/builds/%d/download", mohistBase, mc, b.ID),
		// Java 需求上游不给，按版本线兜底 —— 1.20.1 → 17、1.21.1 → 21 都对得上。
		JavaMajor: javaFallback(mc),
		JavaFrom:  "versionline",
		Label:     label,
	}
}
