package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 这一组是最容易被写错、又最难靠肉眼发现的两处：
//   ① 安装器日志里「名字里带 Exception 的正常类」会被当成报错灌进用户日志
//   ② Forge 两代产物的启动 jar 认错（1.16.5 的目录里躺着原版 jar，认错就是「模组全没了」）
// 两条都不需要联网、也不需要真下载 125 MB 就能测。

func TestInterestingLineFiltersClassNameNoise(t *testing.T) {
	// 这些是实测从 1.20.4 安装输出里抓下来的原行。
	// 朴素筛法（含 exception/error 就当报错）会把它们全放进来 —— 一次一百多行假报错，
	// 真正的错误就被埋了。
	noise := []string{
		"  net/minecraft/DefaultUncaughtExceptionHandler.class",
		"  net/minecraft/ReportedException.class",
		"  net/minecraft/ResourceLocationException.class",
		"  net/minecraft/commands/BrigadierExceptions.class",
		"  net/minecraft/commands/execution/CustomCommandExecutor$WithErrorHandling.class",
		// ↓↓↓ 这几行是**逐字抄自 1.20.4 真机输出**的，不是按想象写的。
		// 第一版正则只认「整行一个裸路径」，栽就栽在这层 Copying / Patching 前缀上。
		"  Copying net/minecraft/DefaultUncaughtExceptionHandler.class",
		"  Copying net/minecraft/network/SkipPacketException.class",
		"  Copying net/minecraft/util/ExceptionCollector.class",
		"  Copying data/minecraft/tags/blocks/snow_layer_cannot_survive_on.json",
		"  Patching net/minecraft/server/level/ChunkHolder$ChunkLoadingFailure 1/1",
		"  Patching com/mojang/realmsclient/gui/screens/RealmsGenericErrorScreen 1/1",
		// ↓ 这一族最阴：类名里带 Failed / Exception，会被关键字规则当信号放行，
		//   只能在「形状」那一关拦下来。中间那个 patch 词是关键特征。
		"  Reading patch com.mojang.blaze3d.platform.Window$WindowInitFailed.binpatch",
		"  Reading patch net.minecraft.client.gui.screens.TitleScreen$WarningLabel.binpatch",
		"  Reading patch net.minecraft.client.renderer.LevelRenderer$TransparencyShaderException.binpatch",
		"  Can't Find Class: com/mojang/brigadier/exceptions/SimpleCommandExceptionType",
		"  Host: maven.minecraftforge.net [172.67.161.211, 104.21.58.163]",
		"  Host: piston-meta.mojang.com [150.171.110.106]",
		"Target Directory: F:\\sbx-forge-test\\instances\\i1791103605633",
		"  Downloading library from https://maven.creeprhost.net/x.jar",
		"    Download completed: Checksum validated.",
		"  Considering library net.minecraftforge:forge:1.20.4-49.0.30:universal",
		// ↓ 库名 failureaccess 里带 fail —— 关键字规则会把它当报错捞起来（实测两份都被捞了）
		"  Considering library com.google.guava:failureaccess:1.0.1",
		"  Downloading library from https://libraries.minecraft.net/com/google/guava/failureaccess/1.0.1/failureaccess-1.0.1.jar",
		"    Checksum: 0 Exists: false",
	}
	for _, ln := range noise {
		if interestingLine(ln) {
			t.Errorf("这行是噪音，不该写进用户日志：%q", ln)
		}
	}

	// 这些是真正值得让用户看到的
	keep := []string{
		"Extracting main jar:",
		"Downloading libraries",
		"Building Processors",
		"The server installed successfully",
		"Data kindly mirrored by CreeperHost at https://www.creeperhost.net/",
		"java.lang.UnsupportedClassVersionError: has been compiled by a more recent version",
		"Error: Unable to access jarfile forge-1.12.2-installer.jar",
		"Exception in thread \"main\" java.io.FileNotFoundException",
		"Failed to download library from https://example.invalid/x.jar",
		// 带「动词」的真报错不能被形状规则误伤 —— 路径里必须含 / 或 . 就是为了挡住它
		"Patching failed: could not apply patch",
		"Adding mod failed",
	}
	for _, ln := range keep {
		if !interestingLine(ln) {
			t.Errorf("这行必须让用户看到，却被滤掉了：%q", ln)
		}
	}
}

// TestInterestingLineOnRealInstallerOutput 拿一份**逐字抄自 1.20.4 安装器真实输出**的
// 样本（testdata/forge-installer-sample.txt，1204 行，按“形状”抽样，覆盖所有噪音族）跑过滤规则。
//
// 这份样本的存在本身就是一条教训：上一轮单测全绿、真机却满屏 `Copying xxx.class`，
// 因为样本是我手写的「裸路径」—— 而真输出前面还挂着一层 `Copying `。
// 手写的样本只能证明「我以为的形状」是对的。真输出 29733 行，过滤完只该剩 5 条。
func TestInterestingLineOnRealInstallerOutput(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "forge-installer-sample.txt"))
	if err != nil {
		t.Fatalf("读不到真实输出样本：%v", err)
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")

	var survivors []string
	for _, ln := range strings.Split(text, "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if interestingLine(ln) {
			survivors = append(survivors, ln)
		}
	}

	want := []string{
		"Data kindly mirrored by CreeperHost at https://www.creeperhost.net/",
		"Extracting main jar:",
		"Downloading libraries",
		"Building Processors",
		"The server installed successfully",
	}
	if len(survivors) != len(want) {
		t.Fatalf("过滤后该只剩 %d 条，实际 %d 条 —— 多半是又漏了一族噪音：\n%s",
			len(want), len(survivors), strings.Join(survivors, "\n"))
	}
	for i := range want {
		if survivors[i] != want[i] {
			t.Errorf("第 %d 条对不上：\n  实际 %q\n  期望 %q", i, survivors[i], want[i])
		}
	}

	// 兜底断言：无论样本怎么换，文件路径类和几个过程族都绝不该出现在用户日志里
	for _, ln := range survivors {
		low := strings.ToLower(strings.TrimSpace(ln))
		for _, ext := range []string{".class", ".binpatch", ".jar", ".json", ".lzma", ".tsrg", ".java", ".txt"} {
			if strings.HasSuffix(low, ext) {
				t.Errorf("存活行以 %s 结尾，是文件路径噪音：%q", ext, ln)
			}
		}
		for _, pre := range []string{"copying ", "patching ", "reading ", "checksum:", "considering ", "can't find class"} {
			if strings.HasPrefix(low, pre) {
				t.Errorf("存活行是安装器过程噪音：%q", ln)
			}
		}
	}
}

func TestFindForgeLaunchJarPicksShimForModern(t *testing.T) {
	// spec=1（1.17+）：根目录出 shim，真身在 libraries/ 里
	dir := t.TempDir()
	touch(t, dir, "forge-1.20.4-49.2.0-shim.jar")
	touch(t, dir, "forge-1.20.4-49.2.0-installer.jar")
	mkdir(t, dir, "libraries")

	got, err := findForgeLaunchJar(dir, "1.20.4-49.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "forge-1.20.4-49.2.0-shim.jar" {
		t.Errorf("认成了 %s，1.17+ 该启动的是 shim", got)
	}
}

func TestFindForgeLaunchJarPicksForgeJarNotVanilla(t *testing.T) {
	// spec=0（≤1.16.5）：目录里同时躺着 forge-<版本>.jar 和 minecraft_server.<MC>.jar。
	// 认错成原版 jar 的后果是「你的模组全没了」，所以这一条必须精确。
	dir := t.TempDir()
	touch(t, dir, "forge-1.16.5-36.2.39.jar")
	touch(t, dir, "minecraft_server.1.16.5.jar")
	touch(t, dir, "forge-1.16.5-36.2.39-installer.jar")
	mkdir(t, dir, "libraries")

	got, err := findForgeLaunchJar(dir, "1.16.5-36.2.39")
	if err != nil {
		t.Fatal(err)
	}
	if got != "forge-1.16.5-36.2.39.jar" {
		t.Errorf("认成了 %s，必须是没有模组就会崩的那个原版 jar", got)
	}
}

func TestFindForgeLaunchJarRefusesToGuess(t *testing.T) {
	// 名字全对不上、且有多个候选 —— 宁可报错让人去配置页手填，也不能瞎猜一个
	dir := t.TempDir()
	touch(t, dir, "aaa.jar")
	touch(t, dir, "bbb.jar")
	if _, err := findForgeLaunchJar(dir, "1.20.4-49.2.0"); err == nil {
		t.Error("两个无法区分的 jar，应该报错而不是猜一个")
	} else if !strings.Contains(err.Error(), "2") {
		t.Errorf("报错里应该说清有几个候选，实际：%v", err)
	}
}

func TestFindForgeLaunchJarFallsBackToSoleJar(t *testing.T) {
	// 上游改了命名（名字对不上）但目录里只有一个可启动的 jar —— 这种可以认
	dir := t.TempDir()
	touch(t, dir, "forge-something-new.jar")
	touch(t, dir, "forge-something-new-installer.jar")
	got, err := findForgeLaunchJar(dir, "1.20.4-49.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "forge-something-new.jar" {
		t.Errorf("认成了 %s，应该退回那个唯一的非安装器 jar", got)
	}
}

func TestFindForgeLaunchJarErrorsWhenInstallProducedNothing(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "forge-1.20.4-49.2.0-installer.jar") // 只有安装器，说明装没成功
	if _, err := findForgeLaunchJar(dir, "1.20.4-49.2.0"); err == nil {
		t.Error("目录里没有任何可启动的 jar，必须报错")
	}
}

func touch(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
		t.Fatal(err)
	}
}
