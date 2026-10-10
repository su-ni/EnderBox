package daemon

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 造一个最小的 jar：把给定的文件写进 zip。
// 只用来喂元数据解析器，不需要真的能跑。
func makeJar(t *testing.T, dir, name string, files map[string]string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, content := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// ── Fabric ──
//
// 样本结构抄自 fabric.mod.json 的实际字段（schemaVersion 1 与新版 authors 对象数组两种写法都覆盖）。
const fabricModJSON = `{
  "schemaVersion": 1,
  "id": "sodium",
  "version": "0.6.5+mc1.21.4",
  "name": "Sodium",
  "description": "Modern rendering engine and client-side optimization mod for Minecraft.",
  "authors": ["jellysquid3", "IMS212"],
  "contact": { "homepage": "https://caffeinemc.net" },
  "license": "LGPL-3.0-only",
  "icon": "assets/sodium/icon.png",
  "environment": "client",
  "entrypoints": { "main": ["net.caffeinemc.mods.sodium.fabric.SodiumFabricMod"] },
  "depends": {
    "fabricloader": ">=0.15.0",
    "minecraft": "~1.21.4",
    "java": ">=21",
    "fabric-api": "*"
  },
  "recommends": { "sodium-extra": "*" },
  "suggests": { "reeses-sodium-options": "*" }
}`

func TestReadModMetaFabric(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "sodium.jar", map[string]string{
		"fabric.mod.json":        fabricModJSON,
		"assets/sodium/icon.png": "PNG",
	})

	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "sodium" || m.Name != "Sodium" {
		t.Errorf("id/name 不对：%s / %s", m.ID, m.Name)
	}
	if m.Version != "0.6.5+mc1.21.4" {
		t.Errorf("版本不对：%s", m.Version)
	}
	if m.Loader != "fabric" {
		t.Errorf("加载器不对：%s", m.Loader)
	}
	if len(m.Authors) != 2 || m.Authors[0] != "jellysquid3" {
		t.Errorf("作者解析不对：%v", m.Authors)
	}
	if m.IconPath != "assets/sodium/icon.png" {
		t.Errorf("图标路径不对：%s", m.IconPath)
	}
	if m.Homepage != "https://caffeinemc.net" {
		t.Errorf("主页不对：%s", m.Homepage)
	}

	// 依赖：必需里要有 fabric-api，且 minecraft/java/fabricloader 必须被标成内置
	// （不标的话界面上每个模组都会提示「缺少 minecraft」，成了废话）
	var gotFabricAPI bool
	for _, d := range m.Requires {
		switch strings.ToLower(d.ID) {
		case "fabric-api":
			gotFabricAPI = true
			if d.Kind != "required" || d.Builtin {
				t.Errorf("fabric-api 该是必需且非内置：%+v", d)
			}
		case "minecraft", "java", "fabricloader":
			if !d.Builtin {
				t.Errorf("%s 该被标成内置（运行环境本身）：%+v", d.ID, d)
			}
		}
	}
	if !gotFabricAPI {
		t.Errorf("没解析出 fabric-api 依赖：%+v", m.Requires)
	}
	if len(m.Optional) != 2 {
		t.Errorf("recommends + suggests 该合成 2 条可选依赖，实际 %+v", m.Optional)
	}
}

// environment 是 Fabric 判定 side 的**唯一权威依据** —— 服务端加载器启动时就靠它拒绝纯客户端 mod。
// 读得出来，界面才能在列表里提前警告；读不出来，用户只能等服务器 exit 1 才知道。
// 真机案例：ETF(entity_texture_features) 装进 Forge 服务端，启动即崩。
func TestReadModMetaFabricSide(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		env  string
		want string
	}{
		{"仅客户端", `"client"`, "client"},
		{"仅服务端", `"server"`, "server"},
		{"两端（*）", `"*"`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `{"schemaVersion":1,"id":"modx","name":"ModX","version":"1.0.0","environment":` + c.env + `}`
			p := makeJar(t, dir, "modx-"+c.want+".jar", map[string]string{"fabric.mod.json": body})
			m, err := ReadModMeta(p)
			if err != nil {
				t.Fatal(err)
			}
			if m.Side != c.want {
				t.Errorf("environment=%s 读成 side=%q，想要 %q", c.env, m.Side, c.want)
			}
		})
	}

	// 老模组根本没有 environment 字段 —— 同样按「两端」收成空串。
	// ⚠ 这里必须是空串，不能是 "client"：缺字段猜成客户端会让用户删掉一个能用的模组。
	p := makeJar(t, dir, "modx-noenv.jar", map[string]string{
		"fabric.mod.json": `{"schemaVersion":1,"id":"noenv","name":"NoEnv","version":"1.0.0"}`,
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Side != "" {
		t.Errorf("缺 environment 该收成空串（两端都行），实际 %q", m.Side)
	}
}

// authors 写成 [{name, contact}] 的也要能读（新版 schema 的写法）
func TestReadModMetaFabricAuthorsAsObjects(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "m.jar", map[string]string{
		"fabric.mod.json": `{"schemaVersion":1,"id":"m","version":"1.0","name":"M",
			"authors":[{"name":"Alice","contact":{"email":"a@b.c"}},{"name":"Bob"}]}`,
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Authors) != 2 || m.Authors[0] != "Alice" || m.Authors[1] != "Bob" {
		t.Errorf("对象形式的作者没解析对：%v", m.Authors)
	}
}

// icon 写成 {路径: 尺寸} 映射的也要能取一个
func TestReadModMetaFabricIconMap(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "m.jar", map[string]string{
		"fabric.mod.json": `{"id":"m","version":"1","name":"M","icon":{"assets/m/icon64.png":64,"assets/m/icon32.png":32}}`,
		// 注意：这里**故意不把图标文件放进 jar** —— ReadModMeta 应该发现路径不存在并清空它，
		// 否则前端会去请求一个必然 404 的图标。
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.IconPath != "" {
		t.Errorf("jar 里不存在的图标路径该被清空，实际 %q", m.IconPath)
	}
}

// ── Forge / NeoForge ──
//
// 样本抄自 mods.toml 的实际字段：[[mods]] 段 + [[dependencies.<modid>]] 段。
const forgeModsToml = `modLoader="javafml"
loaderVersion="[47,)"
license="LGPL-2.1-only"

[[mods]]
modId="jei"
version="15.2.0.27"
displayName="Just Enough Items"
authors="mezz"
description='''
JEI is an item and recipe viewing mod for Minecraft.
'''
displayURL="https://github.com/mezz/JustEnoughItems"
logoFile="assets/jei/logo.png"

[[dependencies.jei]]
    modId="forge"
    mandatory=true
    versionRange="[47,)"
    ordering="NONE"
    side="BOTH"

[[dependencies.jei]]
    modId="minecraft"
    mandatory=true
    versionRange="[1.20.1,1.21)"
    ordering="NONE"
    side="BOTH"

[[dependencies.jei]]
    modId="cloth_config"
    mandatory=false
    versionRange="[11,)"
    ordering="AFTER"
    side="CLIENT"
`

func TestReadModMetaForgeToml(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "jei.jar", map[string]string{
		"META-INF/mods.toml":  forgeModsToml,
		"assets/jei/logo.png": "png-bytes",
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "jei" || m.Name != "Just Enough Items" {
		t.Errorf("id/name 不对：%s / %s", m.ID, m.Name)
	}
	if m.Version != "15.2.0.27" {
		t.Errorf("版本不对：%s", m.Version)
	}
	if m.Loader != "forge" {
		t.Errorf("加载器不对：%s", m.Loader)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "mezz" {
		t.Errorf("作者不对：%v", m.Authors)
	}
	if !strings.Contains(m.Description, "item and recipe viewing") {
		t.Errorf("描述不对：%q", m.Description)
	}
	if m.Homepage != "https://github.com/mezz/JustEnoughItems" {
		t.Errorf("主页不对：%s", m.Homepage)
	}
	// forge / minecraft 是内置；cloth_config 是可选（mandatory=false）
	if len(m.Requires) != 2 {
		t.Errorf("该有 2 条必需依赖（forge/minecraft），实际 %+v", m.Requires)
	}
	for _, d := range m.Requires {
		if !d.Builtin {
			t.Errorf("%s 该是内置：%+v", d.ID, d)
		}
	}
	if len(m.Optional) != 1 || m.Optional[0].ID != "cloth_config" || m.Optional[0].Kind != "optional" {
		t.Errorf("cloth_config 该是可选的，实际 %+v", m.Optional)
	}
	// Forge 的图标字段叫 logoFile（不是 Fabric 的 icon）—— 漏掉它所有 Forge 模组都是没图标的
	if m.IconPath != "assets/jei/logo.png" {
		t.Errorf("logoFile 该被当成图标路径，实际 %q", m.IconPath)
	}
}

// 用 Windows 上的工具（.NET 的 ZipFile 等）重新打包，条目名会变成反斜杠分隔。
// 不归一化的话这类包永远读不出元数据 —— 而报错只说「没有认识的元数据文件」，
// 谁都看不出根因是分隔符。用户自己重新压过的 jar 是真实存在的场景。
func TestReadModMetaBackslashEntries(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "win.jar")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range map[string]string{
		`META-INF\mods.toml`:  forgeModsToml,
		`assets\jei\logo.png`: "png-bytes",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatalf("反斜杠条目名的 jar 也该能读出元数据：%v", err)
	}
	if m.ID != "jei" {
		t.Errorf("id 该是 jei，实际 %q", m.ID)
	}
	if m.IconPath != "assets/jei/logo.png" {
		t.Errorf("图标路径该被归一成 / 分隔，实际 %q", m.IconPath)
	}
}

// NeoForge 用 META-INF/neoforge.mods.toml，字段与 Forge 一致
func TestReadModMetaNeoForge(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "n.jar", map[string]string{
		"META-INF/neoforge.mods.toml": `modLoader="javafml"
loaderVersion="[1,)"
[[mods]]
modId="create"
version="6.0.0"
displayName="Create"
[[dependencies.create]]
modId="neoforge"
mandatory=true
versionRange="[21,)"
`,
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Loader != "neoforge" {
		t.Errorf("该认出 neoforge，实际 %s", m.Loader)
	}
	if m.Source != "META-INF/neoforge.mods.toml" {
		t.Errorf("来源不对：%s", m.Source)
	}
}

// ── Bukkit / Paper 插件 ──

const bukkitPluginYML = `name: EssentialsX
version: 2.20.1
main: com.earth2me.essentials.Essentials
api-version: 1.13
author: Zenexer
description: The essential plugin suite for Minecraft servers.
website: https://essentialsx.net
depend: [Vault]
softdepend:
  - ProtocolLib
  - PlaceholderAPI
`

func TestReadModMetaBukkitPluginYML(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "EssentialsX-2.20.1.jar", map[string]string{
		"plugin.yml": bukkitPluginYML,
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "EssentialsX" || m.Version != "2.20.1" {
		t.Errorf("名字/版本不对：%s / %s", m.Name, m.Version)
	}
	if m.Loader != "bukkit" {
		t.Errorf("加载器不对：%s", m.Loader)
	}
	if len(m.Authors) != 1 || m.Authors[0] != "Zenexer" {
		t.Errorf("作者不对：%v", m.Authors)
	}
	// `depend: [Vault]` 是行内数组，`softdepend:\n  - x` 是块数组 —— 两种都要认
	if len(m.Requires) != 1 || m.Requires[0].ID != "Vault" || m.Requires[0].Kind != "required" {
		t.Errorf("depend 行内数组没解析对：%+v", m.Requires)
	}
	if len(m.Optional) != 2 {
		t.Errorf("softdepend 块数组该有 2 条，实际 %+v", m.Optional)
	}
}

// ── Velocity ──

func TestReadModMetaVelocity(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "v.jar", map[string]string{
		"velocity-plugin.json": `{"id":"luckperms","name":"LuckPerms","version":"5.4.102",
			"authors":["Luck"],"url":"https://luckperms.net",
			"dependencies":[{"id":"velocity","optional":false}]}`,
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "LuckPerms" || m.Version != "5.4.102" {
		t.Errorf("不对：%+v", m)
	}
	if m.Loader != "velocity" {
		t.Errorf("加载器不对：%s", m.Loader)
	}
	if len(m.Requires) != 1 || !m.Requires[0].Builtin {
		t.Errorf("velocity 自身该是内置必需项：%+v", m.Requires)
	}
}

// ── 兜底与边界 ──

// 不认识的文件不该报「解析成功」——调用方要据此退回文件名猜
func TestReadModMetaUnknownJar(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "plain.jar", map[string]string{"com/example/A.class": "x"})
	if _, err := ReadModMeta(p); err == nil {
		t.Error("没有元数据的 jar 该返回错误")
	}
}

// 损坏的元数据不能让整个列表崩 —— 返回错误即可
func TestReadModMetaBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "bad.jar", map[string]string{"fabric.mod.json": `{"id":`})
	if _, err := ReadModMeta(p); err == nil {
		t.Error("坏 JSON 该返回错误")
	}
}

// 嵌套 jar（Forge 的 jarjar 打包）在读不到顶层元数据时兜底
func TestReadModMetaNestedJar(t *testing.T) {
	dir := t.TempDir()
	inner := makeJar(t, dir, "inner.jar", map[string]string{"fabric.mod.json": fabricModJSON})
	innerData, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}
	outer := filepath.Join(dir, "outer.jar")
	f, err := os.Create(outer)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("META-INF/jarjar/inner.jar")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(innerData); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	m, err := readModMetaNested(outer)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "sodium" {
		t.Errorf("该从嵌套 jar 里读出 sodium，实际 %s", m.ID)
	}
	if !strings.Contains(m.Source, "inner.jar!") {
		t.Errorf("来源该标明是从哪个嵌套文件读的，实际 %s", m.Source)
	}
}

// 真实样本（逐字抄自 Fabric API 仓库，见 testdata/fabric-api-real.mod.json）：
// 里面有 `"version": "${version}"` 这种构建期占位符。
// 必须把它当「版本未知」，不能原样显示 —— 否则用户会看到模组版本叫 "${version}"。
func TestReadModMetaRealFabricApiSample(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "fabric-api-real.mod.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p := makeJar(t, dir, "fabric-resource-loader.jar", map[string]string{
		"fabric.mod.json": string(raw),
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "" {
		t.Errorf("${version} 占位符该被清成空，实际 %q", m.Version)
	}
	if m.ID != "fabric-resource-loader-v0" {
		t.Errorf("id 不对：%s", m.ID)
	}
	if m.Name != "Fabric Resource Loader (v0)" {
		t.Errorf("name 不对：%s", m.Name)
	}
	// 这份样本的 icon 指向 jar 内不存在的文件（我们没往 jar 里放）→ 该被清掉
	if m.IconPath != "" {
		t.Errorf("不存在的图标该被清空，实际 %q", m.IconPath)
	}
	// 依赖：fabricloader 内置，fabric-resource-loader-v1 必需且非内置
	var sawLoaderV1 bool
	for _, d := range m.Requires {
		switch d.ID {
		case "fabricloader":
			if !d.Builtin {
				t.Error("fabricloader 该是内置")
			}
		case "fabric-resource-loader-v1":
			sawLoaderV1 = true
			if d.Builtin || d.Kind != "required" {
				t.Errorf("fabric-resource-loader-v1 该是必需且非内置：%+v", d)
			}
		}
	}
	if !sawLoaderV1 {
		t.Errorf("没解析出 fabric-resource-loader-v1 依赖：%+v", m.Requires)
	}
	if m.Homepage != "https://fabricmc.net" {
		t.Errorf("主页不对：%s", m.Homepage)
	}
}

// 占位符清理的边界：正常版本号不能被误伤
func TestStripPlaceholder(t *testing.T) {
	cases := map[string]string{
		"${version}":      "",
		"v${mod_version}": "v",
		"1.2.3":           "1.2.3",
		"0.6.5+mc1.21.4":  "0.6.5+mc1.21.4",
		"${a}-${b}":       "-",
		"":                "",
		"15.2.0.27":       "15.2.0.27",
	}
	for in, want := range cases {
		if got := stripPlaceholder(in); got != want {
			t.Errorf("stripPlaceholder(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestModIconFile(t *testing.T) {
	dir := t.TempDir()
	p := makeJar(t, dir, "m.jar", map[string]string{
		"fabric.mod.json": `{"id":"m","version":"1","name":"M","icon":"icon.png"}`,
		"icon.png":        "\x89PNG\r\n\x1a\n",
	})
	m, err := ReadModMeta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.IconPath != "icon.png" {
		t.Fatalf("图标路径不对：%s", m.IconPath)
	}
	data, mime, err := ModIconFile(p, m.IconPath, m.Source)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || !strings.HasPrefix(mime, "image/png") {
		t.Errorf("图标数据/MIME 不对：%d 字节 %s", len(data), mime)
	}
	if _, _, err := ModIconFile(p, "nope.png", ""); err == nil {
		t.Error("不存在的图标该报错")
	}
}

// 造一个 jar，内容按字节给（二进制也能塞）。
func makeJarBin(t *testing.T, dir, name string, files map[string][]byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for n, content := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// Forge 的「外套 jar」：真正的模组、连同它声明的 logo.png，都在 META-INF/jarjar/ 里的**内层** jar 中。
//
// 这个测试是冲着一个真实 bug 来的（用户原话：「为什么已装界面，有时候没有模组图标？」）：
// 元数据能钻进内层读，所以列表标了 hasIcon=true；但取图标只翻外层 jar，
// 于是接口 404、界面只剩占位图标 —— 「说有图标、给了 404」比干脆没有还费解。
func TestModIconFromNestedJar(t *testing.T) {
	dir := t.TempDir()

	// 内层：真模组，带 logo.png
	inner := makeJarBin(t, dir, "inner-mod.jar", map[string][]byte{
		"META-INF/mods.toml": []byte("modLoader=\"javafml\"\n" +
			"loaderVersion=\"[47,)\"\nlicense=\"MIT\"\n\n[[mods]]\n" +
			"modId=\"connectormod\"\nversion=\"1.0\"\ndisplayName=\"Connector\"\nlogoFile=\"logo.png\"\n"),
		"logo.png": {0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00},
	})
	innerBytes, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}

	// 外层：真模组只藏在内层，自己不带任何元数据
	outer := makeJarBin(t, dir, "Connector-1.0.0.jar", map[string][]byte{
		"META-INF/MANIFEST.MF":          []byte("Manifest-Version: 1.0\n"),
		"META-INF/jarjar/inner-mod.jar": innerBytes,
	})

	m, err := readModMetaNested(outer)
	if err != nil {
		t.Fatalf("内层模组的元数据读不出来：%v", err)
	}
	if m.IconPath != "logo.png" {
		t.Fatalf("图标路径应为 logo.png，实得 %q", m.IconPath)
	}
	if !strings.Contains(m.Source, "!") {
		t.Fatalf("元数据来源应当带内层 jar 前缀（形如 a.jar!b.toml），实得 %q", m.Source)
	}

	data, mime, err := ModIconFile(outer, m.IconPath, m.Source)
	if err != nil {
		t.Fatalf("内层 jar 里的图标取不出来：%v —— 这就是「说有图标却 404」那个 bug", err)
	}
	if len(data) == 0 {
		t.Error("图标数据是空的")
	}
	if !strings.Contains(mime, "image") {
		t.Errorf("MIME 不该是 %q", mime)
	}
}

// 外层自己有图标时，照旧从外层取（别为了修内层把原来的路弄坏）。
func TestModIconFromOuterJarStillWorks(t *testing.T) {
	dir := t.TempDir()
	jar := makeJarBin(t, dir, "plain.jar", map[string][]byte{
		"fabric.mod.json":   []byte(`{"schemaVersion":1,"id":"x","version":"1","icon":"assets/x/icon.png"}`),
		"assets/x/icon.png": {0x89, 'P', 'N', 'G'},
	})
	data, mime, err := ModIconFile(jar, "assets/x/icon.png", "")
	if err != nil {
		t.Fatalf("外层图标取不出来：%v", err)
	}
	if len(data) == 0 || !strings.Contains(mime, "image") {
		t.Errorf("数据或 MIME 不对：len=%d mime=%q", len(data), mime)
	}
}
