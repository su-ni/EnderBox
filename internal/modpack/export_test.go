package modpack

import (
	"archive/zip"
	"bytes"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/su-ni/EnderBox/internal/fetch"
)

// 造一个实例目录 + 一个假的反查函数，返回导出选项。
func setupInstance(t *testing.T) (string, map[string]string, func([]string) (map[string]fetch.RemoteVersion, error)) {
	t.Helper()
	root := t.TempDir()
	inst := filepath.Join(root, "inst")
	write := func(rel, content string) {
		p := filepath.Join(inst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// 收的
	write("mods/fabric-api.jar", "AAA")
	write("mods/jei.jar", "BBB")          // 客户端专用
	write("mods/local-自制.jar", "CCC")    // 反查不到
	write("config/x.toml", "cfg")
	// 不收的（白名单之外）
	write("world/level.dat", "SAVE")       // 用户存档，绝不能进包
	write("logs/latest.log", "log")
	write("server.jar", "SERVERJAR")
	write("server.properties", "port=25565")

	sha := map[string]string{}
	for rel, sum := range map[string]string{
		"mods/fabric-api.jar": sha1Of(t, filepath.Join(inst, "mods", "fabric-api.jar")),
		"mods/jei.jar":        sha1Of(t, filepath.Join(inst, "mods", "jei.jar")),
		"mods/local-自制.jar":  sha1Of(t, filepath.Join(inst, "mods", "local-自制.jar")),
		"config/x.toml":       sha1Of(t, filepath.Join(inst, "config", "x.toml")),
	} {
		sha[rel] = sum
	}

	lookup := func(hashes []string) (map[string]fetch.RemoteVersion, error) {
		out := map[string]fetch.RemoteVersion{}
		want := map[string]struct {
			env  string
			name string
		}{
			sha["mods/fabric-api.jar"]: {"*", "fabric-api.jar"},
			sha["mods/jei.jar"]:        {"client_only", "jei.jar"},
			sha["config/x.toml"]:       {"*", "x.toml"},
			// local-自制.jar 故意不返回 —— 复刻「查不到」的真实行为
		}
		for h, w := range want {
			for _, got := range hashes {
				if got == h {
					out[h] = fetch.RemoteVersion{
						ProjectID:   "p",
						VersionID:   "v",
						Environment: w.env,
						Files: []fetch.RemoteFile{{
							URL:      "https://cdn.example/" + w.name,
							Filename: w.name,
							Size:     3,
							Primary:  true,
							Hashes:   map[string]string{"sha1": h, "sha512": "s512"},
						}},
					}
				}
			}
		}
		return out, nil
	}
	return inst, sha, lookup
}

func sha1Of(t *testing.T, p string) string {
	t.Helper()
	s, err := sha1File(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// 造一个字符串的 sha1（用来断言「某个内容确实没被打进包」）
func sha1OfString(s string) string {
	f, _ := os.CreateTemp("", "sha")
	defer os.Remove(f.Name())
	_, _ = f.WriteString(s)
	f.Close()
	h, _ := sha1File(f.Name())
	return h
}

func zipEntries(t *testing.T, p string) []string {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var out []string
	for _, f := range zr.File {
		out = append(out, f.Name)
	}
	return out
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ⚠ 最要紧的一条：**用户的 world/ 绝不能进包**。
// 整合包是「模组内容的分发件」，不是「实例备份」。把存档打进去发出去，
// 等于把别人世界的种子、玩家数据、建筑全泄露了 —— 而且收包那边还会覆盖自己的存档。
func TestExportNeverIncludesWorldLogsOrServerJar(t *testing.T) {
	inst, _, lookup := setupInstance(t)
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "测试服",
		WantClient: true, WantServer: true,
		Minecraft: "1.20.1", LoaderKey: "fabric-loader", LoaderVer: "0.15.0",
		Lookup: lookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []*PackResult{res.Client, res.Server} {
		names := zipEntries(t, p.Path)
		for _, bad := range []string{"world/level.dat", "logs/latest.log", "server.jar", "server.properties"} {
			if contains(names, bad) || contains(names, "overrides/"+bad) {
				t.Errorf("包 %s 里混进了不该收的东西：%s\n实际条目：%v", filepath.Base(p.Path), bad, names)
			}
		}
	}
}

// 服务端包该收 fabric-api（*），客户端包该收 jei（client_only），
// 而 jei **不能**出现在服务端包里 —— 那是 env 分流的核心。
func TestExportSplitsByEnvironment(t *testing.T) {
	inst, _, lookup := setupInstance(t)
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "测试服",
		WantClient: true, WantServer: true,
		Lookup: lookup,
	})
	if err != nil {
		t.Fatal(err)
	}

	// 服务端包：fabric-api 进清单；jei 被剔到 Skipped
	srvNames := zipEntries(t, res.Server.Path)
	if !contains(srvNames, "modrinth.index.json") {
		t.Fatal("服务端包没有清单")
	}
	if !contains(res.Server.Skipped, "mods/jei.jar") {
		t.Errorf("client_only 的 jei 该被服务端包剔掉，实际 Skipped=%v", res.Server.Skipped)
	}
	if contains(res.Server.Skipped, "mods/fabric-api.jar") {
		t.Error("environment=* 的文件不该被剔掉")
	}

	// 客户端包：反过来
	if !contains(res.Client.Skipped, "mods/fabric-api.jar") &&
		contains(res.Client.Skipped, "mods/jei.jar") {
		t.Error("客户端包不该剔掉 jei")
	}
	if contains(res.Client.Skipped, "mods/jei.jar") {
		t.Errorf("jei 是 client_only，客户端包不该剔它，实际 Skipped=%v", res.Client.Skipped)
	}
}

// 反查不到的模组必须**连内容一起打进 overrides** —— 只列清单的话收包的人装不起来。
func TestExportPutsUnknownModsIntoOverrides(t *testing.T) {
	inst, _, lookup := setupInstance(t)
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "测试服",
		WantServer: true,
		Lookup:     lookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	names := zipEntries(t, res.Server.Path)
	if !contains(names, "overrides/mods/local-自制.jar") {
		t.Errorf("查不到的模组该进 overrides 并带上文件本体，实际条目：%v", names)
	}
	if !contains(res.Server.LocalFiles, "mods/local-自制.jar") {
		t.Error("LocalFiles 该如实报出哪些是本地模组（界面上要显示）")
	}
	if res.Server.OverrideFiles != 1 {
		t.Errorf("该有 1 个 override，实际 %d", res.Server.OverrideFiles)
	}
}

// 包里的清单必须能被**我们自己的解析器**读回来 —— 导出和导入用同一个格式，
// 这条断了就意味着「自己做出来的包自己装不了」。
func TestExportIndexRoundTrips(t *testing.T) {
	inst, _, lookup := setupInstance(t)
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "测试服",
		WantServer: true,
		Minecraft:  "1.20.1", LoaderKey: "fabric-loader", LoaderVer: "0.15.0",
		Lookup: lookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	ix, err := ParseIndex(res.Server.Path)
	if err != nil {
		t.Fatalf("自己产的包自己解析不了：%v", err)
	}
	if ix.Minecraft() != "1.20.1" {
		t.Errorf("游戏版本丢了：%q", ix.Minecraft())
	}
	if k, v := ix.Loader(); k != "fabric-loader" || v != "0.15.0" {
		t.Errorf("加载器丢了：%s %s", k, v)
	}
	// 能被判成「可以装到服务端」
	if v := ix.ServerVerdict(); !v.OK {
		t.Errorf("该判成可装，实际 %+v", v)
	}
}

// ExtraDirs 里手填 `../` 不能把实例目录外的文件带进来。
func TestExportRejectsEscapingExtraDirs(t *testing.T) {
	inst, _, lookup := setupInstance(t)
	// 在实例目录的**外面**放一个敏感文件
	outside := filepath.Join(filepath.Dir(inst), "secret.txt")
	if err := os.WriteFile(outside, []byte("SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "测试服",
		WantServer: true,
		ExtraDirs:  []string{"../", "..", "../.."},
		Lookup:     lookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range zipEntries(t, res.Server.Path) {
		if filepath.Base(n) == "secret.txt" {
			t.Errorf("实例目录外的文件被打进包了：%s", n)
		}
	}
	_ = sha1OfString
}

// 下载中的临时分片（xxx.jar.part.0）**不能**进包。
//
// 这条是拿真实例跑预览时撞出来的：某个实例的 mods/ 里躺着 12 个 .part 共 51MB。
// 它们不是完整模组，打进包收包那边装不上；而且会让「包有多大」的估算虚高到离谱。
// 单测里造得出来，但**不主动造就永远测不到** —— 所以补上。
func TestExportSkipsPartialDownloads(t *testing.T) {
	root := t.TempDir()
	inst := filepath.Join(root, "inst")
	mk := func(rel, content string) {
		p := filepath.Join(inst, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("mods/real.jar", "REAL")
	mk("mods/big.jar.part.0", "HALF")
	mk("mods/big.jar.part.1", "HALF")
	mk("mods/big.jar.part", "HALF")

	// 全部查不到 → 全都会走 overrides，正好能看出谁被收进来了
	empty := func([]string) (map[string]fetch.RemoteVersion, error) {
		return map[string]fetch.RemoteVersion{}, nil
	}
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "t",
		WantServer: true, Lookup: empty,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Server.LocalFiles {
		if strings.Contains(f, ".part") {
			t.Errorf("临时分片被打进包了：%s（全部 LocalFiles=%v）", f, res.Server.LocalFiles)
		}
	}
	if res.Server.OverrideBytes != 4 { // 只有 real.jar 的 "REAL" 4 字节
		t.Errorf("包体估算把分片算进去了：%d 字节（期望 4）", res.Server.OverrideBytes)
	}
}

// 两个列表字段**不能是 nil** —— Go 的 nil slice 序列化成 JSON 的 null，
// 前端就得为「没有」和「没这个字段」写两套判断。
// 这条是拿真实例跑预览时暴露的：客户端包有 Skipped，服务端包没有 → 回了个 null。
func TestExportListsAreNeverNil(t *testing.T) {
	inst, _, lookup := setupInstance(t)
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "t",
		WantClient: true, WantServer: true, Lookup: lookup,
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]*PackResult{"client": res.Client, "server": res.Server} {
		if p.LocalFiles == nil {
			t.Errorf("%s 包的 LocalFiles 是 nil（会序列化成 null）", name)
		}
		if p.Skipped == nil {
			t.Errorf("%s 包的 Skipped 是 nil（会序列化成 null）", name)
		}
	}
}

// 包里的 overrides 文件必须**真的有内容**。
//
// ⚠ 这条是用来抓「静默产出坏包」的：原先把 io.Copy 的错误丢给 `_` 之后，
//   产出的包只有 6380 字节，而 zip 头里写着那个 jar 有 4.19MB —— 外壳成功、
//   条目存在、长度也写着，唯独正文是空的。收包那边装不起来还查不出原因。
//
// 所以这里刻意：① 用 1MB 的文件（小文件可能碰巧"); 蒙对）；② **逐字节比对**内容。
// 只看「条目在不在」是抓不住它的。
func TestExportOverridesContainRealBytes(t *testing.T) {
	root := t.TempDir()
	inst := filepath.Join(root, "inst")
	dir := filepath.Join(inst, "mods")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 1MB **随机**数据 —— 不可压缩，和真实 jar 一样。
	// ⚠ 一开始用的是循环字节（i%251），结果 zip 把它压到了 4866 字节，
	//   让「产物大小」这个断言假失败了一次。用固定种子保证测试可重复。
	rng := rand.New(rand.NewSource(1))
	payload := make([]byte, 1<<20)
	_, _ = rng.Read(payload)
	if err := os.WriteFile(filepath.Join(dir, "big.jar"), payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// 全部查不到 → 都会进 overrides
	empty := func([]string) (map[string]fetch.RemoteVersion, error) {
		return map[string]fetch.RemoteVersion{}, nil
	}
	res, err := Export(ExportOptions{
		InstanceDir: inst, InstanceName: "t",
		WantServer: true, Lookup: empty,
	})
	if err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(res.Server.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	found := false
	for _, f := range zr.File {
		if f.Name != "overrides/mods/big.jar" {
			continue
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		got, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(payload) {
			t.Fatalf("包里的文件是空的 / 不完整的：期望 %d 字节，实际读到 %d 字节", len(payload), len(got))
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("包里的内容和原文件不一致（写坏了）")
		}
	}
	if !found {
		t.Fatal("包里没有 overrides/mods/big.jar")
	}
	if res.Server.Bytes < int64(len(payload)) {
		t.Errorf("产物大小反常：%d 字节（至少该有 %d）", res.Server.Bytes, len(payload))
	}
}
