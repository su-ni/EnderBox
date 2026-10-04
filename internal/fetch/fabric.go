package fetch

import (
	"fmt"
)

// Fabric 的元数据 API（实测 2026-10-04，全部 200）。
//
//	GET /v2/versions/game      游戏版本，带 stable 标记（529 个里 48 个是正式版）
//	GET /v2/versions/loader    Loader 版本，带 stable 标记
//	GET /v2/versions/installer 安装器版本，带 stable 标记
//	GET /v2/versions/loader/{游戏版本}/{Loader}/{安装器}/server/jar   ← 直接就是服务端 jar
//
// ⚠ 最后那个地址**必须是三段**（游戏/loader/installer）。只写两段会 404 ——
// 我第一遍就是照两段打的，三个组合全 404，差点误判成「Fabric 不支持直连」。
//
// 与 Paper 的关键差别：这个 jar 只有一百多 KB，是个**启动器**，
// 真正玩的那套（原版 jar + 一堆库）由它在首次启动时自己拉。
// 所以 ①首次开服必须联网且会慢一些 ②它不给 Content-Length、也不给校验和（都实测过）。
const fabricMetaBase = "https://meta.fabricmc.net/v2"

type fabricEntry struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

// FabricVersions Fabric 支持的游戏版本，只要正式版。
// 这里直接用 Fabric 自己给的 stable 字段，不靠「名字里有没有横杠」去猜 ——
// 上游已经把答案给了，再去猜一遍就是重复造一个更容易错的轮子。
func FabricVersions() ([]Version, error) {
	var games []fabricEntry
	if err := getJSON(fabricMetaBase+"/versions/game", &games); err != nil {
		return nil, err
	}
	var out []Version
	for _, g := range games {
		if !g.Stable {
			continue
		}
		out = append(out, Version{ID: g.Version, Prerelease: false, Stable: true})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("Fabric 返回的游戏版本列表是空的（检查 %s/versions/game）", fabricMetaBase)
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// pickPreferred 挑一个版本：优先第一个被上游标为 stable 的，都没有就退回第一个。
// 上游给的是「新的在前」顺序（实测：loader 0.19.5/0.19.4/0.19.3，installer 1.1.2/1.1.1/1.1.0），
// 所以「第一个 stable」就是最新的稳定版。
func pickPreferred(list []fabricEntry) string {
	for _, e := range list {
		if e.Stable {
			return e.Version
		}
	}
	if len(list) > 0 {
		return list[0].Version
	}
	return ""
}

// FabricBuilds Fabric 只出一个构建：「最新稳定 Loader + 最新稳定安装器」。
//
// 故意不给一长串 Loader 让用户挑：Loader 与游戏版本基本解耦（实测 1.20.1 配最新的
// 0.19.5 也能正常构建出 jar），新手挑这个没有任何意义，只是多两次点击和一个选错的机会。
// 这也和原版那边「一个版本一个包」的形状一致，前端不用为 Fabric 写特例。
func FabricBuilds(mirror, gameVersion string) ([]Build, error) {
	var loaders, installers []fabricEntry
	if err := getJSON(fabricMetaBase+"/versions/loader", &loaders); err != nil {
		return nil, err
	}
	if err := getJSON(fabricMetaBase+"/versions/installer", &installers); err != nil {
		return nil, err
	}
	loader, installer := pickPreferred(loaders), pickPreferred(installers)
	if loader == "" || installer == "" {
		return nil, fmt.Errorf("Fabric 没给出可用的 Loader / 安装器版本（上游结构可能变了）")
	}

	// Size / SHA 空着是**如实**，不是偷懒：这个地址既不给 Content-Length 也不给校验和。
	// 界面会分别显示「大小未知」和「未校验」，比编一个数出来强。
	return []Build{{
		ID:      0,
		Label:   "Loader " + loader,
		Channel: "STABLE",
		Name:    fmt.Sprintf("fabric-server-mc.%s-loader.%s-launcher.%s.jar", gameVersion, loader, installer),
		URL:     fmt.Sprintf("%s/versions/loader/%s/%s/%s/server/jar", fabricMetaBase, gameVersion, loader, installer),
		SHAKind: "sha256",
		// Fabric 服务端跑的还是那个 MC 版本的原版 jar，所以 Java 要求 = 该 MC 版本的要求。
		// 这是权威值（Mojang 元数据），不是猜的。
		JavaMajor: RequiredJava(mirror, gameVersion),
		JavaFrom:  "mojang",
	}}, nil
}

func FabricLatestBuild(mirror, gameVersion string) (Build, error) {
	bs, err := FabricBuilds(mirror, gameVersion)
	if err != nil {
		return Build{}, err
	}
	return bs[0], nil
}
