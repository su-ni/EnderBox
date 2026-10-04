package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这一组盯的是 Forge 1.17+ 的**第三种产物形态**：根目录一个 jar 都没有，
// 启动靠 libraries/ 里的参数文件（1.20.1-47.4.26 实测就是这样）。
// 它不是边角情况 —— 1.20.1 是模组包里最常被锁定的版本之一，认不出来就等于
// 「装成功了却起不来」，而且报的是一句让人摸不着头脑的错。

// 参数文件的路径要从安装器**自己生成的启动脚本**里读出来，而且要跳过 user_jvm_args.txt。
// 那个文件后缀同样是 _args.txt、在脚本里还排在前面；不跳过就会拿一个纯注释文件去启动。
func TestFindForgeArgsFileSkipsUserJvmArgs(t *testing.T) {
	dir := t.TempDir()
	argsDir := filepath.Join(dir, "libraries", "net", "minecraftforge", "forge", "1.20.1-47.4.26")
	if err := os.MkdirAll(argsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(argsDir, "win_args.txt"), []byte("-p a;b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// run.bat 的形状逐字抄自 1.20.1-47.4.26 的安装产物
	bat := "@echo off\n" +
		"REM Forge requires a configured set of both JVM and program arguments.\n" +
		"java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.26/win_args.txt %*\n" +
		"pause\n"
	if err := os.WriteFile(filepath.Join(dir, "run.bat"), []byte(bat), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := findForgeArgsFile(dir)
	if !ok {
		t.Fatal("应该能从 run.bat 里读出参数文件")
	}
	want := "libraries/net/minecraftforge/forge/1.20.1-47.4.26/win_args.txt"
	if got != want {
		t.Errorf("读到 %q，期望 %q（绝不能是 user_jvm_args.txt）", got, want)
	}
}

// 脚本里提到、但文件其实不存在（安装断在半路留下的脚本）时不能认。
func TestFindForgeArgsFileRequiresExistingTarget(t *testing.T) {
	dir := t.TempDir()
	bat := "java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.26/win_args.txt %*\n"
	if err := os.WriteFile(filepath.Join(dir, "run.bat"), []byte(bat), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := findForgeArgsFile(dir); ok {
		t.Error("参数文件不存在时不该认它")
	}
}

// 根目录没有 jar 的产物：必须退到参数文件，而不是报「一个能启动的 jar 都没有」。
func TestFindForgeLaunchTargetFallsBackToArgsFile(t *testing.T) {
	dir := t.TempDir()
	argsDir := filepath.Join(dir, "libraries", "net", "minecraftforge", "forge", "1.20.1-47.4.26")
	if err := os.MkdirAll(argsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(argsDir, "win_args.txt"), []byte("-p x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "run.bat"),
		[]byte("java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.1-47.4.26/win_args.txt %*\n"), 0o644)
	// 根目录只有安装器本身（它不算「能启动的服务端文件」）
	_ = os.WriteFile(filepath.Join(dir, "forge-1.20.1-47.4.26-installer.jar"), []byte("x"), 0o644)

	got, err := findForgeLaunchTarget(dir, "1.20.1-47.4.26")
	if err != nil {
		t.Fatalf("不该报错：%v", err)
	}
	if !strings.HasSuffix(got, "win_args.txt") {
		t.Errorf("该退到参数文件，实际 %q", got)
	}
}

// 有根目录 shim 的产物仍然优先走 jar —— 那条路更简单、更不容易出错。
func TestFindForgeLaunchTargetPrefersRootJar(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "forge-1.20.4-49.2.0-shim.jar"), []byte("x"), 0o644)
	argsDir := filepath.Join(dir, "libraries", "net", "minecraftforge", "forge", "1.20.4-49.2.0")
	_ = os.MkdirAll(argsDir, 0o755)
	_ = os.WriteFile(filepath.Join(argsDir, "win_args.txt"), []byte("-p x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "run.bat"),
		[]byte("java @user_jvm_args.txt @libraries/net/minecraftforge/forge/1.20.4-49.2.0/win_args.txt %*\n"), 0o644)

	got, err := findForgeLaunchTarget(dir, "1.20.4-49.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "forge-1.20.4-49.2.0-shim.jar" {
		t.Errorf("有 shim 时应优先用它，实际 %q", got)
	}
}

// 启动命令：参数文件发 @file，jar 发 -jar。混了就是「Invalid or corrupt jarfile」。
func TestBuildArgsUsesAtFormForArgsFile(t *testing.T) {
	inst := &Instance{
		Type: TypeForge, Dir: "D:/x", MinMem: 1024, MaxMem: 2048,
		JarPath: "libraries/net/minecraftforge/forge/1.20.1-47.4.26/win_args.txt",
	}
	got := buildArgs(inst)
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "@libraries/net/minecraftforge/forge/1.20.1-47.4.26/win_args.txt") {
		t.Errorf("参数文件该用 @ 形式，实际：%v", got)
	}
	if strings.Contains(joined, "-jar") {
		t.Errorf("参数文件不该走 -jar：%v", got)
	}
	if n := len(got); n == 0 || got[n-1] != "nogui" {
		t.Errorf("服务端仍然要带 nogui，实际末位是 %v", got[maxInt(0, len(got)-1):])
	}

	// 普通 jar 必须还是 -jar
	inst2 := &Instance{Type: TypePaper, Dir: "D:/x", MinMem: 1024, MaxMem: 2048, JarPath: "server.jar"}
	if j := strings.Join(buildArgs(inst2), " "); !strings.Contains(j, "-jar ") {
		t.Errorf("普通 jar 仍要 -jar：%v", j)
	}
}

// 本机路径写成 Windows 反斜杠时也要认得出来是参数文件。
func TestIsArgsFile(t *testing.T) {
	yes := []string{"libraries/x/win_args.txt", `libraries\x\win_args.txt`, "a/unix_args.txt", "A/UNIX_ARGS.TXT"}
	for _, p := range yes {
		if !isArgsFile(p) {
			t.Errorf("isArgsFile(%q) 应该是 true", p)
		}
	}
	no := []string{"server.jar", "forge-1.20.1-47.4.26-shim.jar", "", "args.txt"}
	for _, p := range no {
		if isArgsFile(p) {
			t.Errorf("isArgsFile(%q) 应该是 false", p)
		}
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
