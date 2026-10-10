package fetch

import (
	"strings"
	"testing"
)

func ghReleaseOf(tag string, assets ...string) ghRelease {
	r := ghRelease{TagName: tag, Name: tag}
	for _, a := range assets {
		r.Assets = append(r.Assets, ghAsset{
			Name: a, Size: 5725384, UpdatedAt: "2025-01-21T00:00:00Z",
			BrowserDownloadURL: "https://github.com/IzzelAliz/Arclight/releases/download/" + tag + "/" + a,
		})
	}
	return r
}

// TestArclightAssetOf 从附件名里拆游戏版本与加载器版本。三种真实形状都得认：
// 带 commit 短哈希的、带 SNAPSHOT 的、什么都没有的。
func TestArclightAssetOf(t *testing.T) {
	ok := []struct {
		name string
		mc   string
		ver  string
	}{
		{"arclight-forge-1.20.1-1.0.6.jar", "1.20.1", "1.0.6"},
		{"arclight-forge-1.21.1-1.0.1-8ec9529.jar", "1.21.1", "1.0.1"},
		{"arclight-forge-1.21.11-1.0.0-SNAPSHOT-8a57510.jar", "1.21.11", "1.0.0-SNAPSHOT"},
		{"arclight-1.16.5-1.0.3.jar", "1.16.5", "1.0.3"}, // 老版本线不带 -forge-
	}
	for _, c := range ok {
		mc, ver, got := arclightAssetOf(c.name)
		if !got {
			t.Errorf("%s 没认出来", c.name)
			continue
		}
		if mc != c.mc || ver != c.ver {
			t.Errorf("%s → (%s, %s)，想要 (%s, %s)", c.name, mc, ver, c.mc, c.ver)
		}
	}

	// ⚠ 必须挡住的：其它加载器变体。面板把 Arclight 当 Forge 系管
	// （启动方式、库文件、mods 目录都是 Forge 那套），拿 fabric 变体下来会对不上。
	bad := []string{
		"arclight-fabric-1.21.1-1.0.1-8ec9529.jar",
		"arclight-neoforge-1.21.11-1.0.0-SNAPSHOT-8a57510.jar",
		"arclight-forge-1.21.1-1.0.1.jar.sha256",
		"README.md",
		"Trials_1.0.6 source code.zip",
	}
	for _, n := range bad {
		if _, _, got := arclightAssetOf(n); got {
			t.Errorf("%s 被当成 Forge 变体了 —— 它会和实例类型对不上", n)
		}
	}
}

// TestArclightURLsNeverGoBareGitHub 产出的下载地址**必须**经代理。
//
// 这条是硬约束，不是偏好：本机 GitHub 直连是死的（443/22 全 000），
// 裸的 github.com 地址在这里 100% 下不动。哪天有人「顺手简化」成官方地址，
// 这条会立刻变红。
func TestArclightURLsNeverGoBareGitHub(t *testing.T) {
	rel := []ghRelease{
		ghReleaseOf("Trials/1.0.6", "arclight-forge-1.20.1-1.0.6.jar"),
		ghReleaseOf("Whisper/1.0.4", "arclight-forge-1.20.4-1.0.4-80ec5df.jar", "arclight-fabric-1.20.4-1.0.4-80ec5df.jar"),
	}
	bs := arclightBuildsOf(rel, "1.20.1")
	if len(bs) != 1 {
		t.Fatalf("1.20.1 拿到 %d 个构建，想要 1 个（fabric 变体不该算进来）", len(bs))
	}
	for _, b := range bs {
		if !strings.HasPrefix(b.URL, arclightProxy) {
			t.Errorf("下载地址没走代理：%s（本机直连 GitHub 是死的）", b.URL)
		}
		if strings.HasPrefix(b.URL, "https://github.com/") {
			t.Errorf("下载地址是裸的 GitHub，本机下不动：%s", b.URL)
		}
	}
}

// TestArclightTagIsTheIdentity Arclight 没有构建号，身份只能挂 Tag。
//
// 如果改成「用 ID 当身份」，同一个游戏版本下的两个构建会都拿到 0 ——
// 前端把它们当成同一个（选中一个会高亮全部），下载时也定位不到用户选的那个。
func TestArclightTagIsTheIdentity(t *testing.T) {
	rel := []ghRelease{
		ghReleaseOf("Whisper/1.0.4", "arclight-forge-1.20.4-1.0.4-80ec5df.jar"),
		ghReleaseOf("Whisper/1.0.3", "arclight-forge-1.20.4-1.0.3-13f0d63.jar"),
	}
	bs := arclightBuildsOf(rel, "1.20.4")
	if len(bs) != 2 {
		t.Fatalf("1.20.4 拿到 %d 个构建，想要 2 个", len(bs))
	}
	if bs[0].Tag == "" || bs[1].Tag == "" {
		t.Fatal("有构建没带 Tag —— 下载时没法回源定位")
	}
	if bs[0].Tag == bs[1].Tag {
		t.Errorf("两个构建的 Tag 一样（%s）—— 它们会被当成同一个", bs[0].Tag)
	}
	// 版本号要显示出来：标签写着「1.0.4」。⚠ 不能靠字符串比大小来排序，
	// 1.0.12 和 1.0.6 按字符串比会得出 1.0.6 更新这种荒唐结论 —— 顺序沿用上游。
	if bs[0].Label != "1.0.4" || bs[1].Label != "1.0.3" {
		t.Errorf("Label 不对：%q / %q", bs[0].Label, bs[1].Label)
	}
	if bs[0].ID != 0 {
		t.Errorf("Arclight 不该有构建号，ID 却是 %d", bs[0].ID)
	}
	// digest 缺失时不许编一个校验和出来
	if bs[0].SHA != "" || bs[0].SHAKind != "" {
		t.Errorf("上游没给 digest，我们却编了一个：%q / %q", bs[0].SHA, bs[0].SHAKind)
	}
}

// TestArclightVersionsComeFromAssets 版本列表是从**真实附件**推的，不是写死的表。
// 写死的表在上游发新版本之后就开始撒谎 —— 用户会看到一个下不了的版本。
func TestArclightVersionsComeFromAssets(t *testing.T) {
	rel := []ghRelease{
		ghReleaseOf("GroupOfSoldiers/1.0.0-SNAPSHOT",
			"arclight-fabric-1.21.11-1.0.0-SNAPSHOT-8a57510.jar",
			"arclight-neoforge-1.21.11-1.0.0-SNAPSHOT-8a57510.jar"),
		ghReleaseOf("Trials/1.0.6", "arclight-forge-1.20.1-1.0.6.jar"),
		ghReleaseOf("1.18/1.0.12", "arclight-forge-1.18.2-1.0.12.jar"),
	}
	got := arclightVersionsOf(rel)
	want := []string{"1.20.1", "1.18.2"}
	if len(got) != len(want) {
		t.Fatalf("拿到 %v，想要 %v（只有 Forge 变体的版本才算数）", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("拿到 %v，想要 %v", got, want)
		}
	}
}

// TestSplitDigest GitHub 的 digest 是 "sha256:…" 形式，也可能整个是空的。
func TestSplitDigest(t *testing.T) {
	if sha, kind := splitDigest("sha256:9556a10ca7"); sha != "9556a10ca7" || kind != "sha256" {
		t.Errorf("拆出来 (%q, %q)", sha, kind)
	}
	if sha, kind := splitDigest(""); sha != "" || kind != "" {
		t.Errorf("空 digest 应该拆出两个空串，拿到 (%q, %q) —— 编一个假的会让校验误报失败", sha, kind)
	}
}
