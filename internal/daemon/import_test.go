package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkServer4Test 搭一个假的已有服务端目录
func mkServer4Test(t *testing.T, files map[string]string, dirs []string) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDetectImportPaper(t *testing.T) {
	dir := mkServer4Test(t,
		map[string]string{
			"paper-1.21.4-123.jar": "jar",
			"server.properties":    "server-port=25566\nmotd=x\n",
			"eula.txt":             "eula=true\n",
		},
		[]string{"world"},
	)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.Type != TypePaper {
		t.Errorf("类型认成 %q，想要 paper", sc.Type)
	}
	if sc.MCVersion != "1.21.4" {
		t.Errorf("游戏版本认成 %q，想要 1.21.4", sc.MCVersion)
	}
	if sc.JarPath != "paper-1.21.4-123.jar" {
		t.Errorf("启动文件认成 %q", sc.JarPath)
	}
	if sc.Port != 25566 {
		t.Errorf("端口认成 %d，想要 25566（应以 server.properties 为准）", sc.Port)
	}
	if !sc.EULA {
		t.Error("eula=true 却没识别成已同意")
	}
	if !sc.HasWorld {
		t.Error("有 world/ 目录却没识别出来")
	}
}

func TestDetectImportForge(t *testing.T) {
	dir := mkServer4Test(t,
		map[string]string{
			"libraries/net/minecraftforge/forge/1.20.1-47.4.10/win_args.txt": "@libraries/...",
			"server.jar": "installer-not-really-a-server",
		}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.Type != TypeForge {
		t.Fatalf("类型认成 %q，想要 forge", sc.Type)
	}
	if sc.MCVersion != "1.20.1" || sc.LoaderVersion != "47.4.10" {
		t.Errorf("版本认成 mc=%q loader=%q，想要 1.20.1 / 47.4.10", sc.MCVersion, sc.LoaderVersion)
	}
	// Forge 的启动文件必须是 args.txt，不能是根目录那个 jar
	if !strings.HasSuffix(sc.JarPath, "win_args.txt") {
		t.Errorf("启动文件认成 %q，想要那个 win_args.txt", sc.JarPath)
	}
	if strings.Contains(sc.JarPath, "\\") {
		t.Errorf("启动文件路径 %q 用了反斜杠，配置里一律存正斜杠", sc.JarPath)
	}
}

// 混合端第一次启动会把 Forge 装进自己目录，于是根目录里同时有：
// 真正的启动 jar（arclight.jar）和 Forge 生成的 libraries/.../win_args.txt。
// args.txt 只有 JVM 参数、没有主类，拿它当启动目标起不来 ——
// 实测 Arclight 1.20.1 就是这样，导入后 JarPath 一度变成 win_args.txt。
// 这条测试盯的就是「混合端 jar 必须压过 args.txt」。
func TestDetectImportHybridBeatsForgeArgs(t *testing.T) {
	dir := mkServer4Test(t,
		map[string]string{
			"arclight.jar": "jar",
			"libraries/net/minecraftforge/forge/1.20.1-47.3.22/win_args.txt": "@libraries/...",
		},
		[]string{"world"},
	)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.JarPath != "arclight.jar" {
		t.Fatalf("启动文件认成 %q，想要 arclight.jar（args.txt 没有主类，起不来）", sc.JarPath)
	}
	if sc.Type != TypeArclight {
		t.Errorf("类型认成 %q，想要 arclight（它给的是自己的独立类型，不是 forge —— "+
			"类型是「服务端文件怎么下」的判据，归成 forge 会让「一键补齐」去跑 Forge 安装器）", sc.Type)
	}
	// 版本号该从它装好的那个 Forge 目录名里捡回来
	if sc.MCVersion != "1.20.1" {
		t.Errorf("游戏版本认成 %q，想要从 Forge 目录名捡到 1.20.1", sc.MCVersion)
	}
	if sc.LoaderVersion != "47.3.22" {
		t.Errorf("加载器版本认成 %q，想要 47.3.22", sc.LoaderVersion)
	}
}

// Mohist 也是给**独立类型**，不再归成 forge。原因是实打实的：
// 类型决定「服务端文件怎么下」—— 一条 Mohist 实例点「一键补齐服务端文件」时，
// 归成 forge 会去下 Forge 安装器并跑 --installServer，而 Mohist 下下来就能直接启动。
// 靠启动文件名猜（名字里带 mohist）在文件不在的时候恰恰不管用 —— 而那正是要下载的时刻。
func TestDetectImportMohistIsMohist(t *testing.T) {
	dir := mkServer4Test(t, map[string]string{"mohist-1.20.1-826-server.jar": "jar"}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.Type != TypeMohist {
		t.Errorf("类型认成 %q，想要 mohist", sc.Type)
	}
	if sc.JarPath != "mohist-1.20.1-826-server.jar" {
		t.Errorf("启动文件认成 %q", sc.JarPath)
	}
	if sc.MCVersion != "1.20.1" {
		t.Errorf("游戏版本认成 %q，想要 1.20.1", sc.MCVersion)
	}
}

// Cardboard 是 Fabric 系的混合端，不该被当成 Forge —— 否则模组目录、
// 启动参数全按 Forge 来，装进去的 Fabric 模组一个都认不出。
func TestDetectImportCardboardIsFabric(t *testing.T) {
	dir := mkServer4Test(t, map[string]string{"cardboard-1.20.1.jar": "jar"}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.Type != TypeFabric {
		t.Errorf("类型认成 %q，想要 fabric", sc.Type)
	}
}

// 反过来的护栏：普通 Forge（根目录没有混合端 jar）必须继续走 args.txt，
// 别被上面那条修复顺手改坏了。
func TestDetectImportPlainForgeStillUsesArgs(t *testing.T) {
	dir := mkServer4Test(t,
		map[string]string{
			"libraries/net/minecraftforge/forge/1.20.1-47.4.10/win_args.txt": "@libraries/...",
		}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if !strings.HasSuffix(sc.JarPath, "win_args.txt") {
		t.Fatalf("普通 Forge 的启动文件被改成了 %q，应该还是 win_args.txt", sc.JarPath)
	}
	if sc.Type != TypeForge {
		t.Errorf("类型认成 %q，想要 forge", sc.Type)
	}
}

func TestDetectImportNeoForgeInfersVersion(t *testing.T) {
	dir := mkServer4Test(t,
		map[string]string{
			"libraries/net/neoforged/neoforge/21.1.72/win_args.txt": "@args",
		}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.Type != TypeNeoForge {
		t.Fatalf("类型认成 %q，想要 neoforge", sc.Type)
	}
	if sc.MCVersion != "1.21.1" {
		t.Errorf("推断的游戏版本是 %q，想要 1.21.1", sc.MCVersion)
	}
	// 推断出来的值必须明说是推断 —— 不能假装很确定
	if !hasNote(sc.Notes, "推") {
		t.Errorf("NeoForge 版本是推断的，却没在 Notes 里说明：%v", sc.Notes)
	}
}

func TestDetectImportFabricName(t *testing.T) {
	dir := mkServer4Test(t, map[string]string{
		"fabric-server-mc.1.21.4-loader.0.16.9-launcher.1.0.1.jar": "jar",
	}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.Type != TypeFabric {
		t.Errorf("类型认成 %q，想要 fabric", sc.Type)
	}
	if sc.MCVersion != "1.21.4" {
		t.Errorf("游戏版本认成 %q，想要 1.21.4", sc.MCVersion)
	}
}

func TestDetectImportOnlyInstaller(t *testing.T) {
	dir := mkServer4Test(t, map[string]string{
		"forge-1.20.1-47.4.10-installer.jar": "installer",
	}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	// 安装器不是可启动的服务端，不能当成启动文件
	if sc.JarPath == "forge-1.20.1-47.4.10-installer.jar" {
		t.Fatal("把安装器当成了服务端启动文件")
	}
	if !hasNote(sc.Notes, "没找到") {
		t.Errorf("没有可启动文件时应给出说明，实际 Notes=%v", sc.Notes)
	}
}

func TestDetectImportVanillaVersionJSON(t *testing.T) {
	dir := mkServer4Test(t, map[string]string{
		"server.jar":   "jar",
		"version.json": `{"id":"1.20.4","name":"1.20.4"}`,
	}, nil)
	sc, err := DetectImport(dir)
	if err != nil {
		t.Fatalf("DetectImport: %v", err)
	}
	if sc.MCVersion != "1.20.4" {
		t.Errorf("从 version.json 读出的版本是 %q，想要 1.20.4", sc.MCVersion)
	}
	if sc.Type != TypeVanilla {
		t.Errorf("类型认成 %q，想要 vanilla", sc.Type)
	}
}

func TestImportCreatesInstanceAndRejectsDuplicate(t *testing.T) {
	base := filepath.Join(t.TempDir(), "data")
	m, err := NewManager(base)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dir := mkServer4Test(t, map[string]string{
		"paper-1.21.4-1.jar": "jar",
		"server.properties":  "server-port=25577\n",
		"eula.txt":           "eula=true\n",
	}, []string{"world"})

	inst, err := m.Import(ImportRequest{Dir: dir})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if inst.Type != TypePaper || inst.MCVersion != "1.21.4" || inst.Port != 25577 {
		t.Errorf("导入结果不对：%+v", inst)
	}
	if inst.ID == "" {
		t.Error("导入后没有 id")
	}
	// 目录必须原样指向用户那份，不能复制到 data/instances 下
	if !sameDir(inst.Dir, dir) {
		t.Errorf("实例目录变成 %q，应该还是原来的 %q", inst.Dir, dir)
	}
	// 原目录里的文件一个都不能少
	if _, err := os.Stat(filepath.Join(dir, "paper-1.21.4-1.jar")); err != nil {
		t.Error("导入动了用户目录里的文件")
	}

	// 同一个目录再导一次要挡住
	if _, err := m.Import(ImportRequest{Dir: dir}); err == nil {
		t.Fatal("同一个目录被重复纳管了 —— 两个实例指同一份存档会互相打架")
	}
}

func TestImportRejectsDirContainingDataDir(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "data")
	m, err := NewManager(base)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// 导入 root 会把它下面的 data/（面板自己的数据）一起圈进来，
	// 之后「删除并清空文件」就能把整个面板毁掉 —— 必须拒绝
	if _, err := m.Import(ImportRequest{Dir: root}); err == nil {
		t.Fatal("允许纳管包住数据目录的上级目录，删除时会连累整个面板")
	}
}

func TestImportHonorsOverrides(t *testing.T) {
	base := filepath.Join(t.TempDir(), "data")
	m, err := NewManager(base)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	dir := mkServer4Test(t, map[string]string{"server.jar": "jar"}, nil)
	inst, err := m.Import(ImportRequest{
		Dir:       dir,
		Name:      "我改的名字",
		Type:      TypeFabric,
		MCVersion: "1.21.1",
		JarPath:   "server.jar",
		Port:      25600,
		MaxMem:    6144,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if inst.Name != "我改的名字" || inst.Type != TypeFabric ||
		inst.MCVersion != "1.21.1" || inst.Port != 25600 || inst.MaxMem != 6144 {
		t.Errorf("用户覆盖的值没生效：%+v", inst)
	}
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
