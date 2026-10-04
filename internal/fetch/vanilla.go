package fetch

import (
	"fmt"
	"sync"
	"time"
)

// 原版走 Mojang 的元数据。两个域名都能用，挑一个当默认。
const mojangBase = "https://piston-meta.mojang.com"

// manifestResp 版本清单（约 277 KB，917 个版本）
type manifestResp struct {
	Latest struct {
		Release  string `json:"release"`
		Snapshot string `json:"snapshot"`
	} `json:"latest"`
	Versions []struct {
		ID          string `json:"id"`
		Type        string `json:"type"` // release / snapshot / old_beta / old_alpha
		URL         string `json:"url"`
		ReleaseTime string `json:"releaseTime"`
	} `json:"versions"`
}

// versionMeta 单个版本的完整元数据。我们只关心服务端下载项和 Java 要求。
type versionMeta struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	ReleaseTime string `json:"releaseTime"`
	Downloads   struct {
		Server struct {
			SHA1 string `json:"sha1"`
			Size int64  `json:"size"`
			URL  string `json:"url"`
		} `json:"server"`
	} `json:"downloads"`
	// Mojang 自己就写了这个版本要哪个 Java 大版本 ——
	// 这是权威值，比我们按版本号猜准得多（新版号制如 26.x 根本没法按 1.x 推断）
	JavaVersion struct {
		Component    string `json:"component"`
		MajorVersion int    `json:"majorVersion"`
	} `json:"javaVersion"`
}

// ── 元数据缓存 ──
// 清单 277 KB，而构建列表会被反复点；每点一次都重新下是浪费。
// 版本元数据更是同一次操作里要被查好几遍（下载 + 补 Java 要求），必须缓存。
type metaCache struct {
	mu       sync.Mutex
	manifest map[string]*manifestResp // 按 base 分桶：换镜像必须重新拉，不能命中上一次的缓存
	at       map[string]time.Time
	perVer   map[string]*versionMeta // 同样按 base 分桶
}

const manifestTTL = 10 * time.Minute

var cache = &metaCache{
	manifest: map[string]*manifestResp{},
	at:       map[string]time.Time{},
	perVer:   map[string]*versionMeta{},
}

func (c *metaCache) get(base string) (*manifestResp, error) {
	c.mu.Lock()
	if m, ok := c.manifest[base]; ok && time.Since(c.at[base]) < manifestTTL {
		c.mu.Unlock()
		return m, nil
	}
	c.mu.Unlock()

	var m manifestResp
	if err := getJSON(base+"/mc/game/version_manifest_v2.json", &m); err != nil {
		return nil, err
	}
	if len(m.Versions) == 0 {
		return nil, fmt.Errorf("原版版本清单是空的：%s 返回了 0 个版本", base)
	}
	c.mu.Lock()
	c.manifest[base] = &m
	c.at[base] = time.Now()
	c.mu.Unlock()
	return &m, nil
}

// VanillaVersions 原版正式版列表，新的在前。
// 快照版不列 —— 917 个版本里有 800 多个是快照，铺出来只会让人找不到想要的。
func VanillaVersions(mirror string) ([]Version, error) {
	base := mojangBase
	if m := mirrorByID(mirror); m.MojangBase != "" {
		base = m.MojangBase
	}
	m, err := cache.get(base)
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, v := range m.Versions {
		if v.Type != "release" {
			continue
		}
		out = append(out, Version{ID: v.ID, Prerelease: false, Stable: true})
	}
	sortVersionsNewestFirst(out)
	return out, nil
}

// vanillaMeta 取某个版本的元数据（带缓存）
func vanillaMeta(mirror, version string) (*versionMeta, error) {
	base := mojangBase
	useMirror := false
	if m := mirrorByID(mirror); m.MojangBase != "" {
		base = m.MojangBase
		useMirror = true
	}
	ck := base + "|" + version

	cache.mu.Lock()
	if vm, ok := cache.perVer[ck]; ok {
		cache.mu.Unlock()
		return vm, nil
	}
	cache.mu.Unlock()

	if useMirror {
		// BMCLAPI 的路径是 /version/{v}/json，和 Mojang 的 packages 结构不一样
		var vm versionMeta
		if err := getJSON(base+"/version/"+version+"/json", &vm); err != nil {
			return nil, err
		}
		cache.mu.Lock()
		cache.perVer[ck] = &vm
		cache.mu.Unlock()
		return &vm, nil
	}

	m, err := cache.get(base)
	if err != nil {
		return nil, err
	}
	for _, v := range m.Versions {
		if v.ID != version {
			continue
		}
		var vm versionMeta
		if err := getJSON(v.URL, &vm); err != nil {
			return nil, err
		}
		cache.mu.Lock()
		cache.perVer[ck] = &vm
		cache.mu.Unlock()
		return &vm, nil
	}
	return nil, fmt.Errorf("原版版本清单里没有 %s", version)
}

// VanillaBuilds 原版没有构建号，返回一个「唯一构建」占位。
// 这样前端的「版本 → 构建」两步对所有类型都是同一个形状，不用为原版写特例。
func VanillaBuilds(mirror, version string) ([]Build, error) {
	vm, err := vanillaMeta(mirror, version)
	if err != nil {
		return nil, err
	}
	if vm.Downloads.Server.URL == "" {
		return nil, fmt.Errorf("版本 %s 没有服务端下载项（很老的版本只发布了客户端）", version)
	}
	name := "server-" + version + ".jar"
	sha := vm.Downloads.Server.SHA1
	url := vm.Downloads.Server.URL
	if m := mirrorByID(mirror); m.MojangBase != "" {
		// 镜像下直接用它的直取路径，省一次跳转
		url = m.MojangBase + "/version/" + version + "/server"
	}
	return []Build{{
		ID:        0,
		Channel:   "",
		Time:      vm.ReleaseTime,
		Name:      name,
		Size:      vm.Downloads.Server.Size,
		SHA:       sha,
		SHAKind:   "sha1",
		URL:       url,
		JavaMajor: vm.JavaVersion.MajorVersion,
	}}, nil
}

// RequiredJava 查某个 MC 版本要求的 Java 大版本。
// 权威优先：原版元数据里有就直接用；拿不到再按版本号兜底。
// Paper 也能用这个 —— 它跟随同一个 MC 版本，Java 要求一致。
func RequiredJava(mirror, mcVersion string) int {
	if vm, err := vanillaMeta(mirror, mcVersion); err == nil && vm.JavaVersion.MajorVersion > 0 {
		return vm.JavaVersion.MajorVersion
	}
	return javaFallback(mcVersion)
}
