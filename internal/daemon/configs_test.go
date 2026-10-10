package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newConfigTestManager 建实例并把一批文件铺进去。
func newConfigTestManager(t *testing.T, files map[string]string) (*Manager, string, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Create(&Instance{ID: "i1", Name: "测试服"}); err != nil {
		t.Fatal(err)
	}
	closeLogSinks(t, m, "i1")
	inst, _ := m.Get("i1")
	for rel, body := range files {
		p := filepath.Join(inst.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return m, "i1", inst.Dir
}

// ── 路径安全 ──────────────────────────────────

// 这是这个功能唯一的攻击面：路径从 HTTP 查询串来，
// `../../` 能一路读到 C:\Windows\System32。三层校验必须全过。
func TestResolveConfigPathRejectsEscape(t *testing.T) {
	m, id, _ := newConfigTestManager(t, map[string]string{"bukkit.yml": "a: 1\n"})

	bad := []string{
		"../outside.yml",
		"../../outside.yml",
		"a/../../outside.yml",
		"..\\outside.yml",
		"/etc/passwd",
		"C:\\Windows\\win.ini",
		"",
		"   ",
	}
	for _, rel := range bad {
		if _, _, err := m.resolveConfigPath(id, rel); err == nil {
			t.Errorf("路径 %q 应该被拒绝", rel)
		}
	}

	// 正常路径必须放行，别把安全做成「什么都干不了」
	for _, rel := range []string{"bukkit.yml", "config/paper-global.yml", "plugins/X/config.yml"} {
		if _, _, err := m.resolveConfigPath(id, rel); err != nil {
			t.Errorf("正常路径 %q 被误拒：%v", rel, err)
		}
	}
}

// 实例目录里放一个指向外部的软链，不能顺着它读出去。
// Windows 上创建软链要权限，建不出来就跳过（不算失败）。
func TestResolveConfigPathRejectsSymlinkEscape(t *testing.T) {
	m, id, instDir := newConfigTestManager(t, map[string]string{"bukkit.yml": "a: 1\n"})

	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.yml"), []byte("x: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(instDir, "escape.yml")
	if err := os.Symlink(filepath.Join(outside, "secret.yml"), link); err != nil {
		t.Skipf("这个环境建不了符号链接，跳过：%v", err)
	}
	if _, _, err := m.resolveConfigPath(id, "escape.yml"); err == nil {
		t.Error("顺着软链读到实例目录外面了")
	}
}

// ── 列表 ─────────────────────────────────────

func TestListConfigsFiltersAndExplains(t *testing.T) {
	m, id, _ := newConfigTestManager(t, map[string]string{
		"server.properties":           "level-name=world\n",
		"bukkit.yml":                  "settings:\n  allow-end: true\n",
		"spigot.yml":                  "world-settings:\n  default: {}\n",
		"config/paper-global.yml":     "proxies:\n  velocity: {}\n",
		"plugins/MyPlugin/config.yml": "enabled: true\n",
		"plugins/MyPlugin/plugin.jar": "\x00\x01binary",
		"whitelist.json":              "[]",
		"ops.json":                    "[]",
		"eula.txt":                    "eula=true",
		"world/level.dat":             "\x00\x00binary",
		"logs/latest.log":             "[INFO] 日志不是配置",
		"libraries/x/y/z/thing.yml":   "a: 1\n",
		"deep/a/b/c/d/too-deep.yml":   "a: 1\n",
	})

	list, err := m.ListConfigs(id)
	if err != nil {
		t.Fatal(err)
	}

	// 集合字段不能是 nil —— 前端 .length 会直接打崩
	if list.Files == nil || list.Excluded == nil {
		t.Fatal("Files / Excluded 是 nil，序列化成 null 会打崩前端")
	}

	got := map[string]bool{}
	for _, f := range list.Files {
		got[f.Path] = true
	}
	for _, want := range []string{"bukkit.yml", "spigot.yml", "config/paper-global.yml", "plugins/MyPlugin/config.yml"} {
		if !got[want] {
			t.Errorf("该列出的没列出：%s（实际 %v）", want, keysOf(got))
		}
	}
	// jar / 世界数据 / 日志 / 太深的目录都不该出现在编辑器列表里
	for _, no := range []string{
		"plugins/MyPlugin/plugin.jar", "world/level.dat", "logs/latest.log",
		"libraries/x/y/z/thing.yml", "deep/a/b/c/d/too-deep.yml",
	} {
		if got[no] {
			t.Errorf("不该列出的列出来了：%s", no)
		}
	}

	// server.properties 和玩家名单由别的页面维护，必须排除 **并且说清理由** ——
	// 静默不列会让用户以为文件丢了，然后去别处瞎找
	excluded := map[string]string{}
	for _, e := range list.Excluded {
		excluded[e.Path] = e.Reason
	}
	for _, must := range []string{"server.properties", "whitelist.json", "ops.json", "eula.txt"} {
		if excluded[must] == "" {
			t.Errorf("%s 被排除了却没给理由", must)
		}
	}
	if !strings.Contains(excluded["server.properties"], "配置") {
		t.Errorf("server.properties 的排除理由没提到有专门入口：%q", excluded["server.properties"])
	}
}

func TestListConfigsOrdersCommonFilesFirst(t *testing.T) {
	m, id, _ := newConfigTestManager(t, map[string]string{
		"zzz.yml":                "a: 1\n",
		"plugins/MyPlugin/c.yml": "a: 1\n",
		"server.properties":      "level-name=world\n",
		"bukkit.yml":             "a: 1\n",
	})

	list, err := m.ListConfigs(id)
	if err != nil {
		t.Fatal(err)
	}
	// 顶层常用文件要排在前面，别让 plugins 里的东西把 bukkit.yml 挤下去
	if len(list.Files) < 3 {
		t.Fatalf("列表太短：%v", list.Files)
	}
	if list.Files[0].Path != "bukkit.yml" {
		t.Errorf("第一个应该是 bukkit.yml，实际是 %s", list.Files[0].Path)
	}
	if list.Files[len(list.Files)-1].Path != "plugins/MyPlugin/c.yml" {
		t.Errorf("最深的那个该排最后，实际是 %s", list.Files[len(list.Files)-1].Path)
	}
}

func TestListConfigsMissingDirIsEmptyNotError(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Create(&Instance{ID: "i1", Name: "刚建的"}); err != nil {
		t.Fatal(err)
	}
	closeLogSinks(t, m, "i1")
	inst, _ := m.Get("i1")
	if err := os.RemoveAll(inst.Dir); err != nil {
		t.Fatal(err)
	}

	list, err := m.ListConfigs("i1")
	if err != nil {
		t.Fatalf("实例目录还不存在不该报错：%v", err)
	}
	if len(list.Files) != 0 || len(list.Excluded) != 0 {
		t.Errorf("应该是空列表，实际 %d 个文件", len(list.Files))
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── 读取 ─────────────────────────────────────

func TestReadConfigDetectsEOL(t *testing.T) {
	m, id, _ := newConfigTestManager(t, map[string]string{
		"crlf.yml": "a: 1\r\nb: 2\r\n",
		"lf.yml":   "a: 1\nb: 2\n",
	})

	crlf, err := m.ReadConfig(id, "crlf.yml")
	if err != nil {
		t.Fatal(err)
	}
	if crlf.EOL != "crlf" {
		t.Errorf("CRLF 文件被识别成 %q", crlf.EOL)
	}
	if strings.Contains(crlf.Text, "\r") {
		t.Error("交给编辑器的文本里不该还留着 \\r")
	}

	lf, err := m.ReadConfig(id, "lf.yml")
	if err != nil {
		t.Fatal(err)
	}
	if lf.EOL != "lf" {
		t.Errorf("LF 文件被识别成 %q", lf.EOL)
	}
}

// 行尾要原样写回：一份 CRLF 的文件被我们用 LF 存回去，git 上会显示整份文件都改了。
func TestWriteConfigPreservesEOL(t *testing.T) {
	m, id, instDir := newConfigTestManager(t, map[string]string{"crlf.yml": "a: 1\r\nb: 2\r\n"})

	cur, err := m.ReadConfig(id, "crlf.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.WriteConfig(id, "crlf.yml", "a: 1\nb: 3\n", cur.EOL, cur.ModTime); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(instDir, "crlf.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "b: 3\r\n") {
		t.Errorf("存回去没保留 CRLF：%q", raw)
	}
}

func TestReadConfigRefusesBinary(t *testing.T) {
	m, id, _ := newConfigTestManager(t, map[string]string{"blob.yml": "\x00\x01\x02not text"})

	c, err := m.ReadConfig(id, "blob.yml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Textual {
		t.Error("含 NUL 的文件不该被当成文本")
	}
	if c.Problem == "" {
		t.Error("打不开的文件必须给理由，不能静默返回空内容")
	}
}

func TestReadConfigRefusesHugeFile(t *testing.T) {
	big := strings.Repeat("key: value\n", 60_000) // ≈ 660 KB
	m, id, _ := newConfigTestManager(t, map[string]string{"big.yml": big})

	c, err := m.ReadConfig(id, "big.yml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Textual {
		t.Error("超大文件不该塞进编辑器")
	}
	if !strings.Contains(c.Problem, "KB") {
		t.Errorf("该说清楚为什么不开：%q", c.Problem)
	}
	// 但列表里还要能看到它 —— 用户得知道这文件存在
	list, _ := m.ListConfigs(id)
	found := false
	for _, f := range list.Files {
		if f.Path == "big.yml" {
			found = true
		}
	}
	if !found {
		t.Error("超大的文件在列表里也不见了")
	}
}

// ── 校验 ─────────────────────────────────────

func TestValidateConfigTextSpec(t *testing.T) {
	cases := []struct {
		name      string
		lang      string
		text      string
		wantCount int
		wantLine  int
	}{
		{"合法 yaml", "yaml", "a: 1\nb:\n  - c\n", 0, 0},
		// 少一个缩进：YAML 里这是最常见的翻车方式。
		// 行号 4 是**我们自己算**出来的（见 yamlFirstBadLine）——
		// yaml 库对这份文本报的是第 3 行。
		{"非法 yaml", "yaml", "a: 1\nb:\n  - c\n - d\n", 1, 4},
		{"合法 toml", "toml", "a = 1\n[b]\nc = 2\n", 0, 0},
		{"非法 toml", "toml", "a = = 1\n", 1, 1},
		{"合法 json", "json", `{"a": 1}`, 0, 0},
		{"非法 json", "json", "{\n  \"a\": 1,\n}\n", 1, 3},
		{"空 json", "json", "", 1, 1},
		// 空 YAML / TOML 是合法的（等于空配置），不该报错
		{"空 yaml", "yaml", "", 0, 0},
		{"空 toml", "toml", "", 0, 0},
		{"properties 重复键", "properties", "a=1\nb=2\na=3\n", 1, 3},
		{"properties 正常", "properties", "# 注释\na=1\nb=2\n", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := ValidateConfigText(c.lang, c.text)
			if len(issues) != c.wantCount {
				t.Fatalf("问题条数 = %d，想要 %d：%+v", len(issues), c.wantCount, issues)
			}
			if c.wantCount > 0 && issues[0].Line != c.wantLine {
				t.Errorf("行号 = %d，想要 %d（%s）", issues[0].Line, c.wantLine, issues[0].Message)
			}
		})
	}
}

// 行号必须是我们自己算出来的，不能拿 yaml 库的那个数。
//
// 这是本轮最值得留下的一条：实测 yaml 库的行号完全不能信 ——
// 一份 7 行的文件、第 7 行缩进错了，它报「第 1 行」；「引号没闭合」时文件只有
// 2 行它报到第 3 行（文件外面）；同一类缩进错误有时准有时偏一行。
// 把这些样本钉在这里，谁要是把 yamlFirstBadLine 换回「正则抠库里的行号」，这几条立刻红。
func TestYamlBadLineIsComputedNotFromLibrary(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"缩进错位", "a: 1\nb:\n  - c\n - d\n", 4},
		{"缺冒号", "a: 1\nb\nc: 2\n", 2},
		{"引号没闭合（库会报到文件外面）", "a: \"unterminated\nb: 2\n", 1},
		{"tab 缩进", "a:\n\tb: 1\n", 2},
		// 这一份就是演示实例里那份：库报「第 1 行」，真正的坏行是第 7 行
		{
			"长文件里的缩进错位",
			"# 注释\nenabled: true\nlanguage: zh_CN\nlimits:\n  max-homes: 5\n  max-warps: 3\n - this line breaks yaml\n",
			7,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			issues := ValidateConfigText("yaml", c.text)
			if len(issues) != 1 {
				t.Fatalf("要报 1 条，实际 %d：%+v", len(issues), issues)
			}
			if issues[0].Line != c.want {
				t.Errorf("坏行 = %d，想要 %d（库的原文是：%s）", issues[0].Line, c.want, issues[0].Message)
			}
			if issues[0].Line > RenderedLineCount(c.text) {
				t.Errorf("行号 %d 超出了编辑器会画出的 %d 行", issues[0].Line, RenderedLineCount(c.text))
			}
			if !issues[0].Approx {
				t.Error("YAML 的行号仍要标 Approx —— 前缀二分在极少数截断场景下可能偏，不能拍胸脯")
			}
		})
	}
}

// 界面上那句「第 7 行附近」是我们自己算的；库自带的「line 1:」必须被剥掉，
// 否则同一个错误在界面上会并排出现两个互相打架的行号。
// 旧实现只 TrimPrefix 了 "yaml: "，消息里带着 "line 1:" 透传 —— 这个用例在旧实现上是红的。
func TestYamlIssueMessageHasNoLibraryLinePrefix(t *testing.T) {
	broken := "# 注释\nenabled: true\nlanguage: zh_CN\nlimits:\n  max-homes: 5\n  max-warps: 3\n - this line breaks yaml\n"
	issues := ValidateConfigText("yaml", broken)
	if len(issues) != 1 {
		t.Fatalf("要报 1 条，实际 %d：%+v", len(issues), issues)
	}
	msg := issues[0].Message
	if strings.HasPrefix(msg, "line ") || strings.Contains(msg, "line 1:") {
		t.Errorf("消息里还留着库的行号，会和「第 %d 行附近」打架：%q", issues[0].Line, msg)
	}
	if !strings.Contains(msg, "did not find expected key") {
		t.Errorf("剥坐标前缀不该把真正的错因也剥掉：%q", msg)
	}
}

// 其余三种的行号是准的，别跟着一起标成「附近」—— 那会让本来能定位的问题变得模糊
func TestExactIssuesAreNotMarkedApprox(t *testing.T) {
	for _, c := range []struct{ lang, text string }{
		{"json", "{\n \"a\": 1,\n}\n"},
		{"toml", "a = = 1\n"},
		{"properties", "a=1\na=2\n"},
	} {
		issues := ValidateConfigText(c.lang, c.text)
		if len(issues) == 0 {
			t.Fatalf("%s 这段该报错", c.lang)
		}
		if issues[0].Approx {
			t.Errorf("%s 的行号是准的，不该标 Approx", c.lang)
		}
	}
}

// 重复键这一条值得单独立规矩：Java 读 Properties 是**静默取最后一个**，
// 文件里写着 true、你以为 false，能排查半天。
func TestDuplicatePropKeysExplainsWhichWins(t *testing.T) {
	issues := duplicatePropKeys("online-mode=true\nonline-mode=false\n")
	if len(issues) != 1 {
		t.Fatalf("要报 1 条，实际 %d", len(issues))
	}
	if issues[0].Line != 2 {
		t.Errorf("该报第二行，实际 %d", issues[0].Line)
	}
	if !strings.Contains(issues[0].Message, "后面那个生效") {
		t.Errorf("必须说清哪个生效，否则用户不知道该删哪行：%q", issues[0].Message)
	}
}

// propKeyOf 要按 Java 的规矩认分隔符（= 、: 和空白都算）
func TestPropKeyOf(t *testing.T) {
	cases := map[string]string{
		"a=1":             "a",
		"a: 1":            "a",
		"a 1":             "a",
		"a\t1":            "a",
		"key.with.dots=1": "key.with.dots",
		"# comment":       "#",
		"naked":           "naked",
	}
	for in, want := range cases {
		if got := propKeyOf(in); got != want {
			t.Errorf("propKeyOf(%q) = %q，想要 %q", in, got, want)
		}
	}
}

// ── 写入 ─────────────────────────────────────

// mtime 冲突检测：服务端在跑的时候会自己回写配置文件，
// 不看 mtime 就覆盖，等于把服务端刚写的东西抹掉。
func TestWriteConfigRejectsStaleWrite(t *testing.T) {
	m, id, instDir := newConfigTestManager(t, map[string]string{"bukkit.yml": "a: 1\n"})

	cur, err := m.ReadConfig(id, "bukkit.yml")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟「别人在别处改了它」
	touched := cur.ModTime.Add(3 * time.Second)
	if err := os.Chtimes(filepath.Join(instDir, "bukkit.yml"), touched, touched); err != nil {
		t.Fatal(err)
	}

	err = m.WriteConfig(id, "bukkit.yml", "a: 2\n", "lf", cur.ModTime)
	if err == nil {
		t.Fatal("文件被别处改过之后，用旧 mtime 覆盖应该被拒绝")
	}
	if !strings.Contains(err.Error(), "改过") {
		t.Errorf("错误信息要说清是冲突，不能只说失败：%v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(instDir, "bukkit.yml"))
	if strings.Contains(string(raw), "a: 2") {
		t.Error("被拒绝的写入不该改到文件")
	}
}

func TestWriteConfigKeepsBackupAndCanRestore(t *testing.T) {
	m, id, instDir := newConfigTestManager(t, map[string]string{"bukkit.yml": "原始内容: 1\n"})

	cur, err := m.ReadConfig(id, "bukkit.yml")
	if err != nil {
		t.Fatal(err)
	}
	if cur.HasBackup {
		t.Error("还没保存过就不该有备份")
	}

	if err := m.WriteConfig(id, "bukkit.yml", "改坏的内容: 2\n", cur.EOL, cur.ModTime); err != nil {
		t.Fatal(err)
	}

	// 备份不能落在实例目录里 —— 会被服务端或插件当成配置文件扫到
	if _, err := os.Stat(filepath.Join(instDir, "bukkit.yml.bak")); err == nil {
		t.Error("备份落在实例目录里了，会污染实例")
	}

	after, err := m.ReadConfig(id, "bukkit.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !after.HasBackup {
		t.Fatal("保存之后应该有一份上一版的备份")
	}

	if err := m.RestoreConfig(id, "bukkit.yml"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(instDir, "bukkit.yml"))
	if !strings.Contains(string(raw), "原始内容") {
		t.Errorf("还原没把内容捞回来：%q", raw)
	}
}

func TestWriteConfigAddsTrailingNewline(t *testing.T) {
	m, id, instDir := newConfigTestManager(t, map[string]string{"bukkit.yml": "a: 1\n"})
	cur, _ := m.ReadConfig(id, "bukkit.yml")

	if err := m.WriteConfig(id, "bukkit.yml", "a: 2", "lf", cur.ModTime); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(instDir, "bukkit.yml"))
	if !strings.HasSuffix(string(raw), "\n") {
		t.Errorf("存完该以换行结尾：%q", raw)
	}
}

func TestRestoreWithoutBackupExplains(t *testing.T) {
	m, id, _ := newConfigTestManager(t, map[string]string{"bukkit.yml": "a: 1\n"})
	err := m.RestoreConfig(id, "bukkit.yml")
	if err == nil {
		t.Fatal("没有备份时还原该失败")
	}
	if !strings.Contains(err.Error(), "没有") {
		t.Errorf("错误信息要说清是没有备份：%v", err)
	}
}

// 库消息里的坐标前缀必须被剥掉：界面外面已经标了「第 7 行附近」，
// 再冒出一个「line 1:」就是两个行号打架（旧实现只 TrimPrefix 了 "yaml: "，
// 消息里带着 "line 1:" 一起透传，这个用例在旧实现上是红的）。
func TestStripLocPrefixSpec(t *testing.T) {
	cases := []struct{ in, want string }{
		{"line 1: did not find expected key", "did not find expected key"},
		{"line 12: column 3: mapping values are not allowed here", "mapping values are not allowed here"},
		{"column 5: something odd", "something odd"},
		{"did not find expected key", "did not find expected key"},
		{"unmarshal errors:\n  line 3: cannot unmarshal !!str into int", "unmarshal errors:\n  line 3: cannot unmarshal !!str into int"},
		// 整条消息只有坐标 → 不能还回去空串（那等于把错误吞了）
		{"line 7:", "line 7:"},
		// 冒号在别处出现时不该被误剥
		{"expected ':' but got something else", "expected ':' but got something else"},
	}
	for _, c := range cases {
		if got := stripLocPrefix(c.in); got != c.want {
			t.Errorf("stripLocPrefix(%q)\n got=%q\nwant=%q", c.in, got, c.want)
		}
	}
}
