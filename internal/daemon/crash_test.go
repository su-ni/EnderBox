package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 规则命中：每条都是真实服务端日志里的原话，加了规则就不该再退化。
func TestAnalyzeCrashRules(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"Java 太低", []string{
			"Error: LinkageError occurred while loading main class net.minecraft.server.Main",
			"java.lang.UnsupportedClassVersionError: net/minecraft/server/Main has been compiled by a more recent version of the Java Runtime (class file version 65.0)",
		}, "java_too_old"},
		{"内存爆了", []string{
			"[Server thread/ERROR]: Encountered an unexpected exception",
			"java.lang.OutOfMemoryError: Java heap space",
		}, "out_of_memory"},
		{"端口被占", []string{
			"[Server thread/WARN]: **** FAILED TO BIND TO PORT!",
			"java.net.BindException: Address already in use: bind",
		}, "port_in_use"},
		{"没同意 EULA", []string{
			"[main/INFO]: You need to agree to the EULA in order to run the server. Go to eula.txt for more info.",
		}, "eula_pending"},
		{"模组缺依赖", []string{
			"Missing or unsupported mandatory dependencies:",
			"\tMod ID: 'create' requires 'flywheel' 1.0.0",
		}, "missing_dependency"},
		{"模组崩溃", []string{
			"java.lang.RuntimeException: Exception in server tick loop",
		}, "mod_crash"},
		{"jar 坏了", []string{
			"Error: Invalid or corrupt jarfile server.jar",
		}, "jar_corrupt"},
		{"世界读不出来", []string{
			"java.io.IOException: Failed to load level 'world'",
		}, "world_corrupt"},
		{"联网失败", []string{
			"java.net.UnknownHostException: libraries.minecraft.net",
		}, "network"},
		{"Forge 安装没跑完", []string{
			"There was an error during the installation",
		}, "forge_install_failed"},
		{"文件被占用", []string{
			"java.nio.file.AccessDeniedException: .\\world\\session.lock",
		}, "file_lock"},
		{"客户端模组混进服务端", []string{
			"[main/INFO] [STDERR/]: Caused by: java.lang.RuntimeException: Attempted to load class net/minecraft/client/gui/screens/Screen for invalid dist DEDICATED_SERVER",
		}, "client_mod_on_server"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AnalyzeCrash(c.lines, "exit status 1")
			if len(got) == 0 {
				t.Fatalf("一条都没判出来，想要 %s", c.want)
			}
			if got[0].Code != c.want {
				t.Errorf("首条判成 %s，想要 %s（判据顺序或关键词被改动了）", got[0].Code, c.want)
			}
			if got[0].Evidence == "" {
				t.Error("给了结论却没给证据行 —— 用户没法核对，等于让他信我们")
			}
			if got[0].Cause == "" || got[0].Action == "" {
				t.Error("现象/原因/怎么办 三段必须齐全")
			}
		})
	}
}

// 真实样本（2026-10-09，从市场装的「Zombie Invade 100 Days」整合包）：
// 客户端专属模组 entity_texture_features(ETF) 被一起打进服务端 mods/，
// Forge 在 DEDICATED_SERVER 下拒绝加载客户端界面类，主线程退出。
// 这条规则要同时做到两件事：
//  1. 认出这种死法 —— 在此之前它被兜底成「没匹配到常见死法」，而这其实是整合包开服的经典坑；
//  2. 从 mixin handler 名里把真凶模组抠出来 —— 这 200 行里没有别的线索指出是谁。
func TestClientModOnServerExtractsModName(t *testing.T) {
	lines := []string{
		"[15:50:24] [main/INFO] [STDERR/]: Caused by: java.lang.ExceptionInInitializerError",
		"[15:50:24] [main/INFO] [STDERR/]: \tat TRANSFORMER/minecraft@1.20.1/net.minecraft.commands.BrigadierExceptions.<clinit>(BrigadierExceptions.java:24)",
		"[15:50:24] [main/INFO] [STDERR/]: Caused by: java.lang.RuntimeException: Attempted to load class net/minecraft/client/gui/screens/Screen for invalid dist DEDICATED_SERVER",
		"[15:50:24] [main/INFO] [STDERR/]: \tat TRANSFORMER/minecraft@1.20.1/net.minecraft.resources.ResourceLocation.handler$bbf000$entity_texture_features$etf$illegalPathOverride(ResourceLocation.java:525)",
	}
	got := AnalyzeCrash(lines, "exit status 1")
	if len(got) == 0 {
		t.Fatal("一条都没判出来")
	}
	d := got[0]
	if d.Code != "client_mod_on_server" {
		t.Fatalf("首条判成 %s，想要 client_mod_on_server", d.Code)
	}
	if !strings.Contains(d.Title, "entity_texture_features") {
		t.Errorf("没从 handler 名里认出真凶模组：%q", d.Title)
	}
	if d.Fix != "audit_client_mods" {
		// 这个 fix 不由后端执行，而是让前端跳到「插件与模组」自动核对 ——
		// 所以它必须是这个**约定的码**，改成别的（或清空）前端就哑了。
		t.Errorf("Fix 应当是 audit_client_mods（前端靠它跳转+自动核对），实际 %q", d.Fix)
	}
}

// 反例：普通模组崩溃（没有 DEDICATED_SERVER 那条标记）不许被判成「客户端模组混入」——
// 两个修复方向完全不同：一个是「把客户端模组挪出 mods/」，一个是「查这个模组本身」。
func TestClientModRuleNeedsDistMarker(t *testing.T) {
	got := AnalyzeCrash([]string{
		"java.lang.RuntimeException: Exception in server tick loop",
		"Mod File: /mods/sodium.jar",
	}, "exit status 1")
	for _, d := range got {
		if d.Code == "client_mod_on_server" {
			t.Fatal("没有 DEDICATED_SERVER 标记，却判成了客户端模组混入服务端")
		}
	}
}

// jar 坏 ≠ 存档坏：裸关键词 "corrupt" 会把两者混成一个修复方向。
func TestJarCorruptDoesNotMatchWorldCorrupt(t *testing.T) {
	got := AnalyzeCrash([]string{"Error: Invalid or corrupt jarfile server.jar"}, "")
	for _, d := range got {
		if d.Code == "world_corrupt" {
			t.Fatal("「corrupt jarfile」被误判成了世界存档损坏")
		}
	}
}

// 负样本：一段正常的启动日志不该命中任何具体规则，兜底也要给出下一步。
func TestAnalyzeCrashNormalLogFallsBack(t *testing.T) {
	lines := []string{
		"[12:00:00] [main/INFO]: Starting minecraft server version 1.21.4",
		"[12:00:03] [Server thread/INFO]: Preparing level \"world\"",
		"[12:00:06] [Server thread/INFO]: Done (3.214s)! For help, type \"help\"",
	}
	got := AnalyzeCrash(lines, "")
	if len(got) != 1 || got[0].Code != "unknown" {
		t.Fatalf("正常日志应只给一条 unknown 兜底，实际 %+v", got)
	}
	if got[0].Action == "" {
		t.Error("兜底也要给下一步，不能只说「不知道」")
	}
}

// 我们自己的收尾提示里复述了错误名，不该被当成证据。
func TestAnalyzeCrashIgnoresEnderBoxLines(t *testing.T) {
	lines := []string{
		"[EnderBox] 进程异常退出，最后 200 行已存档：data/logs/i1-crash-20261004-101010.log",
	}
	got := AnalyzeCrash(lines, "")
	if len(got) != 1 || got[0].Code != "unknown" {
		t.Fatalf("EnderBox 自己的提示不该触发任何规则，实际 %+v", got)
	}
	if got[0].Evidence != "" {
		t.Errorf("证据不该取我们自己的提示行，实际 %q", got[0].Evidence)
	}
}

func TestExtractMod(t *testing.T) {
	if got := extractMod("\tMod ID: 'create' requires 'flywheel' 1.0.0"); got != "create" {
		t.Errorf("模组名认成 %q，想要 create", got)
	}
	if got := extractMod("java.lang.Exception from mod flywheel"); got != "flywheel" {
		t.Errorf("模组名认成 %q，想要 flywheel", got)
	}
	if got := extractMod("随便一行没有模组名"); got != "" {
		t.Errorf("认不出来时应返回空串，实际 %q", got)
	}
}

// 模组崩溃要把模组名带进标题，用户才知道该挪走哪一个。
func TestModCrashCarriesModName(t *testing.T) {
	got := AnalyzeCrash([]string{"Mod ID: 'create' requires 'flywheel'", "Exception in server tick loop"}, "")
	if len(got) == 0 {
		t.Fatal("没判出任何结论")
	}
	if !strings.Contains(got[0].Title, "create") {
		t.Errorf("标题里没带上模组名：%q", got[0].Title)
	}
}

func TestParseCrashFile(t *testing.T) {
	raw := "# EnderBox 崩溃现场\n" +
		"# 时间：2026-10-04 20:11:22\n" +
		"# 退出信息：exit status 1\n" +
		"# 下面是从内存缓冲里截到的最后 3 行\n" +
		"\n" +
		"java.lang.OutOfMemoryError: Java heap space\n" +
		"[Server thread/ERROR]: 崩了\n" +
		"bye\n"
	rep := parseCrashFile(raw)
	if rep.Time != "2026-10-04 20:11:22" {
		t.Errorf("时间解析成 %q", rep.Time)
	}
	if rep.ExitInfo != "exit status 1" {
		t.Errorf("退出信息解析成 %q", rep.ExitInfo)
	}
	if rep.LineCount != 3 {
		t.Errorf("正文行数 %d，想要 3", rep.LineCount)
	}
	if len(rep.Diagnoses) == 0 || rep.Diagnoses[0].Code != "out_of_memory" {
		t.Errorf("正文没判出内存问题：%+v", rep.Diagnoses)
	}
}

func TestCrashReportsNewestFirstAndIsolated(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	logsDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) {
		t.Helper()
		raw := "# EnderBox 崩溃现场\n# 时间：2026-10-04 10:10:10\n# 下面是从内存缓冲里截到的最后 1 行\n\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(logsDir, name), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("i1-crash-20261004-101010.log", "java.lang.OutOfMemoryError: Java heap space")
	write("i1-crash-20261004-111111.log", "java.net.BindException: Address already in use")
	write("i2-crash-20261004-121212.log", "java.lang.OutOfMemoryError: Java heap space")

	got := m.CrashReports("i1", 5)
	if len(got) != 2 {
		t.Fatalf("i1 应有 2 份崩溃现场，实际 %d（别把别的实例的算进来）", len(got))
	}
	if got[0].File != "i1-crash-20261004-111111.log" {
		t.Errorf("最新的一份没排在最前：%s", got[0].File)
	}

	txt, err := m.CrashReportText("i1", "i1-crash-20261004-101010.log")
	if err != nil {
		t.Fatalf("读原文失败：%v", err)
	}
	if !strings.Contains(txt, "OutOfMemoryError") {
		t.Error("读回来的原文不对")
	}
}

// 外部传进来的文件名必须挡住路径穿越和跨实例读取。
func TestCrashReportTextRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	bad := []string{
		"../instances.json",
		"..\\instances.json",
		"i2-crash-20261004-101010.log", // 别的实例的
		"i1-notes.log",                 // 不是崩溃档
		"i1-crash-notatime.log",
	}
	for _, name := range bad {
		if _, err := m.CrashReportText("i1", name); err == nil {
			t.Errorf("文件名 %q 本该被拒", name)
		}
	}
}

func TestCrashReportsEmptyWhenNoDir(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	got := m.CrashReports("i1", 5)
	if got == nil {
		t.Fatal("没有 logs 目录时应返回空切片，而不是 nil（前端要直接读它）")
	}
	if len(got) != 0 {
		t.Errorf("应有 0 份，实际 %d", len(got))
	}
}
