package fetch

import (
	"strings"
	"testing"
)

// 这些是从真实 maven-metadata.xml 里抄下来的脏数据（实测 2026-10-04，5058 条里有 35 条）。
// 它们混在版本列表里会切出「1.9-12.16.0.1938」这种根本不存在的 MC 版本，
// 所以 validForgeTag 必须把它们全挡掉。
func TestValidForgeTagRejectsRealGarbage(t *testing.T) {
	garbage := []string{
		"1.12.2-14.23.4.2720-4627",
		"1.11-13.19.0.2129-1.11.x",
		"1.10.2-12.18.1.2016-failtests",
		"1.9.4-12.17.0.1919-EHUnit",
		"1.7.10-10.13.3.1401-1710ls",
		"1.11.2-13.20.0.2245-3630",
		"",
		"1.20.4",
		"-49.2.0",
		"1.20.4-",
		"1.20.4-49.2.x",
	}
	for _, g := range garbage {
		if validForgeTag(g) {
			t.Errorf("validForgeTag(%q) = true，这种脏数据不该通过", g)
		}
	}

	good := []string{
		"1.20.4-49.2.0",
		"1.16.5-36.2.39",
		"1.7.10-10.13.4.1614", // 四段版本号，但形状合法，必须放行
		"26.3-66.0.9",
	}
	for _, g := range good {
		if !validForgeTag(g) {
			t.Errorf("validForgeTag(%q) = false，这是合法版本号", g)
		}
	}
}

func TestCompareDotted(t *testing.T) {
	cases := []struct {
		a, b string
		want int
		why  string
	}{
		{"49.2.10", "49.2.9", 1, "逐段按数值比，不是按字符串（字符串里 \"10\" < \"9\"）"},
		{"49.2.9", "49.2.10", -1, ""},
		// 注意这两条：点号比较是**逐段**的，短的那边缺位按 0 算。
		// 所以 49.9（次版本 9）确实比 49.2.10（次版本 2）新 —— 这不是 bug，
		// 别为了「看起来符合直觉」把它改成按整串比，那会让 49.10 排到 49.9 前面。
		{"49.2.10", "49.9", -1, "次版本 2 < 9"},
		{"49.9", "49.2.10", 1, ""},
		{"49.2.0", "49.2.0", 0, ""},
		{"14.23.5.2860", "14.23.5.2859", 1, "四段版本号"},
		{"51.0.33", "49.0.30", 1, ""},
		{"66.0.9", "66.0.10", -1, ""},
	}
	for _, c := range cases {
		if got := compareDotted(c.a, c.b); got != c.want {
			t.Errorf("compareDotted(%q,%q) = %d，想要 %d %s", c.a, c.b, got, c.want, c.why)
		}
	}
}

func TestPromoMCVersionsParsing(t *testing.T) {
	promos := map[string]string{
		"1.20.4-recommended": "49.2.0",
		"1.20.4-latest":      "49.2.8", // 同一个 MC 版本的两条键只能出一个 Version
		"26.3-latest":        "66.0.9",
		"1.7.10-recommended": "10.13.4.1614",
		"homepage":           "https://example.invalid/", // 不认识的键必须无视
		"-recommended":       "x.y",                      // 抠出来是空串，也不能要
	}
	got := promoMCVersions(promos)
	seen := map[string]int{}
	for _, v := range got {
		seen[v.ID]++
		if !v.Stable {
			t.Errorf("%s 应该被标为正式版", v.ID)
		}
	}
	for _, want := range []string{"1.20.4", "26.3", "1.7.10"} {
		if seen[want] != 1 {
			t.Errorf("版本 %s 出现 %d 次（应为 1 次）", want, seen[want])
		}
	}
	if len(got) != 3 {
		t.Errorf("解析出 %d 个版本，期望 3 个：%+v", len(got), got)
	}
}

func TestForgeVersionsFromMetadataFiltersAndSorts(t *testing.T) {
	// 形状照抄真实的 maven-metadata.xml，混进 4 条脏数据和 2 条别的 MC 版本
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>net.minecraftforge</groupId><artifactId>forge</artifactId>
  <versioning>
    <latest>1.20.1-47.4.26</latest><release>1.20.1-47.4.26</release>
    <versions>
      <version>1.20.4-49.2.9</version>
      <version>1.20.4-49.2.10</version>
      <version>1.20.4-49.9.1</version>
      <version>1.20.4-49.0.30</version>
      <version>1.20.4-49.2.0</version>
      <version>1.12.2-14.23.4.2720-4627</version>
      <version>1.10.2-12.18.1.2016-failtests</version>
      <version>1.20.1-47.4.26</version>
      <version>1.21-51.0.33</version>
    </versions>
  </versioning>
</metadata>`

	got, err := forgeVersionsFromMetadata(raw, "1.20.4")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"49.9.1", "49.2.10", "49.2.9", "49.2.0", "49.0.30"}
	if len(got) != len(want) {
		t.Fatalf("拿到 %d 条 %v，期望 %d 条 %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 条是 %s，期望 %s（整体 %v）", i, got[i], want[i], got)
		}
	}
}

func TestAssembleForgeBuildsPutsOfficialOnesFirst(t *testing.T) {
	promos := map[string]string{"1.20.4-recommended": "49.2.0", "1.20.4-latest": "49.2.8"}
	// maven 里 49.2.9 比官方标的「最新」还新 —— 这是真实情况（实测就是这么排的），
	// 它必须出现在列表里，但只能排在官方那两个后面
	maven := []string{"49.2.9", "49.2.8", "49.2.0", "49.0.30"}

	got := assembleForgeBuilds("1.20.4", promos, maven, 20, 17, "mojang", forgeMavenBase)
	if len(got) != 4 {
		t.Fatalf("拿到 %d 条，期望 4 条（官方 2 条 + 历史 2 条，重复的要并掉）", len(got))
	}
	if got[0].Tag != "1.20.4-49.2.0" || got[0].Channel != "RECOMMENDED" {
		t.Errorf("第 1 条应该是官方推荐 49.2.0，实际是 %+v", got[0])
	}
	if got[1].Tag != "1.20.4-49.2.8" || got[1].Channel != "LATEST" {
		t.Errorf("第 2 条应该是官方最新 49.2.8，实际是 %+v", got[1])
	}
	if got[2].Tag != "1.20.4-49.2.9" {
		t.Errorf("第 3 条应该是 maven 里更新的 49.2.9，实际是 %s", got[2].Tag)
	}
	for i, b := range got {
		if b.ID != i+1 {
			t.Errorf("第 %d 条的 ID 是 %d，行号应该和位置一致", i, b.ID)
		}
		if b.URL != forgeMavenBase+"/"+b.Tag+"/forge-"+b.Tag+"-installer.jar" {
			t.Errorf("%s 的下载地址不对：%s", b.Tag, b.URL)
		}
		if b.Name != "forge-"+b.Tag+"-installer.jar" {
			t.Errorf("%s 的文件名不对：%s", b.Tag, b.Name)
		}
		if b.SHAKind != "sha1" || b.JavaMajor != 17 || b.JavaFrom != "mojang" {
			t.Errorf("%s 的校验/Java 信息不对：%+v", b.Tag, b)
		}
	}

	// limit 只留官方那两条时，也不能把推荐挤掉
	if got2 := assembleForgeBuilds("1.20.4", promos, maven, 2, 17, "mojang", forgeMavenBase); len(got2) != 2 || got2[0].Channel != "RECOMMENDED" {
		t.Errorf("limit=2 时应该只剩官方推荐 + 官方最新，实际 %+v", got2)
	}
}

func TestAssembleForgeBuildsOnlyLatest(t *testing.T) {
	// 有些版本官方只给了 -latest 没有 -recommended（实测 26.3 就是这样）。
	// 这时第一条是「最新」，界面上不该因此报「一个能用的构建都没有」。
	promos := map[string]string{"26.3-latest": "66.0.9"}
	got := assembleForgeBuilds("26.3", promos, []string{"66.0.9", "66.0.8"}, 20, 21, "mojang", forgeMavenBase)
	if len(got) != 2 {
		t.Fatalf("拿到 %d 条，期望 2 条：%+v", len(got), got)
	}
	if got[0].Channel != "LATEST" || got[0].Tag != "26.3-66.0.9" {
		t.Errorf("第一条应该是官方最新 66.0.9，实际 %+v", got[0])
	}
	if got[1].Channel != "" || got[1].Tag != "26.3-66.0.8" {
		t.Errorf("第二条应该是历史构建 66.0.8，实际 %+v", got[1])
	}
}

func TestForgeInstallTagMatchesUpstreamLayout(t *testing.T) {
	// Forge 在 install_profile.json 的 path 字段里用的就是这个形状：net.minecraftforge:forge:1.20.4-49.0.30
	if got := forgeInstallTag("1.20.4", "49.0.30"); got != "1.20.4-49.0.30" {
		t.Errorf("forgeInstallTag = %q，期望 1.20.4-49.0.30", got)
	}
}

// 镜像的基地址必须真的透到下载地址里 —— 否则用户选了镜像、界面显示的地址却还是官方的，
// 「换了源」就只是个心理安慰。
func TestForgeBuildsUseMavenBase(t *testing.T) {
	const base = "https://bmclapi2.bangbang93.com/maven"
	got := assembleForgeBuilds("1.20.4", map[string]string{"1.20.4-recommended": "49.2.0"}, nil, 20, 17, "mojang", base)
	if len(got) != 1 {
		t.Fatalf("拿到 %d 条，期望 1 条", len(got))
	}
	want := base + "/1.20.4-49.2.0/forge-1.20.4-49.2.0-installer.jar"
	if got[0].URL != want {
		t.Errorf("下载地址 = %s，期望 %s", got[0].URL, want)
	}
}

// 结尾斜杠是硬要求：安装器拿它直接拼库路径，少了斜杠会拼出 404（实测 52 个库全挂）。
func TestForgeMirrorURLAlwaysTrailingSlash(t *testing.T) {
	if got := ForgeMirrorURL("bmclapi"); got != "https://bmclapi2.bangbang93.com/maven/" {
		t.Errorf("ForgeMirrorURL(bmclapi) = %q", got)
	}
	if got := ForgeMirrorURL("direct"); got != "" {
		t.Errorf("官方直连不该给镜像地址，拿到 %q", got)
	}
	if got := ForgeMirrorURL("nonsense"); got != "" {
		t.Errorf("认不出的镜像应该退回官方，拿到 %q", got)
	}
}

// 校验和的候选顺序：**先给跟 jar 同源的那个**，再退到另一个。
// 顺序就是这条修复本身 —— 校验和的意义是证明「这一份」jar 是好的，同源才不会打架。
func TestForgeSHAURLsFollowTheJarSource(t *testing.T) {
	got := forgeSHAURLs("1.20.1-47.4.26", "bmclapi")
	want := []string{
		"https://bmclapi2.bangbang93.com/maven/net/minecraftforge/forge/1.20.1-47.4.26/forge-1.20.1-47.4.26-installer.jar.sha1",
		"https://maven.minecraftforge.net/net/minecraftforge/forge/1.20.1-47.4.26/forge-1.20.1-47.4.26-installer.jar.sha1",
	}
	if len(got) != len(want) {
		t.Fatalf("候选 %d 个，期望 %d 个：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 个候选是 %s，期望 %s", i, got[i], want[i])
		}
	}

	// 直连时不该重复列同一个地址
	if g := forgeSHAURLs("1.20.4-49.2.0", "direct"); len(g) != 1 {
		t.Errorf("直连时应该只有 1 个候选，实际 %v", g)
	}
}

// 镜像的制品目录必须含完整 group 路径。这里踩过一次大的：只给到 .../maven，
// 而拼 URL 的代码只会在后面接 /<tag>/<文件名>，结果拼出
// .../maven/1.20.1-47.4.26/forge-...jar → 404，整个镜像下不动。
func TestForgeMavenBaseForKeepsGroupPath(t *testing.T) {
	got := forgeMavenBaseFor("bmclapi")
	if !strings.HasSuffix(got, "/net/minecraftforge/forge") {
		t.Errorf("镜像的制品目录少了 group 路径：%s", got)
	}
	if want := "https://bmclapi2.bangbang93.com/maven/net/minecraftforge/forge"; got != want {
		t.Errorf("forgeMavenBaseFor(bmclapi) = %s，期望 %s", got, want)
	}
	// 直连没有镜像时，必须退回官方那个常量
	if got := forgeMavenBaseFor("direct"); got != forgeMavenBase {
		t.Errorf("直连应该用官方制品目录，实际 %s", got)
	}
}

// --mirror 要的是 maven 根（完整库路径由安装器自己接），和制品目录不是一回事。
func TestForgeMirrorRootIsMavenRootWithSlash(t *testing.T) {
	if got := ForgeMirrorURL("bmclapi"); got != "https://bmclapi2.bangbang93.com/maven/" {
		t.Errorf("ForgeMirrorURL(bmclapi) = %q，--mirror 需要的是 maven 根且带结尾斜杠", got)
	}
}

// Forge 的默认源必须是镜像（官方那条 Cloudflare 路径实测会被掐断），
// 而原版的默认源必须还是官方直连 —— 这个顺序就是前端的默认值，别搞反。
func TestMirrorsForOrdering(t *testing.T) {
	fg := MirrorsFor(KindForge)
	if len(fg) < 2 || fg[0].ID != "bmclapi" {
		t.Fatalf("Forge 的镜像列表应该以 bmclapi 开头，实际 %+v", fg)
	}
	if vs := MirrorsFor(KindVanilla); vs[0].ID != "direct" {
		t.Errorf("原版的默认源应该还是 direct，实际 %s", vs[0].ID)
	}
	for _, m := range MirrorsFor(KindPaper) {
		if m.ID == "bmclapi" {
			t.Errorf("Paper 不该出现 BMCLAPI 选项（那路径 404）")
		}
	}
}
