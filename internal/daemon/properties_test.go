package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 规格表是靠人往里加字段的，最容易出的错不是逻辑写错，而是「新加了一项但忘了填 Group」——
// 那样界面会多出一个没有名字的分组，而且没有任何报错。用测试把它变成编译期之外的第二道闸。
//
// 键表从 23 个扩到 50 个之后，这里顺带把「重复键」也盯死了：propSpecIndex 只留最后一个，
// 两条 spec 撞车时界面显示的说明和实际写进去的行为会对不上，而且完全不报错。
func TestPropSpecsComplete(t *testing.T) {
	seen := map[string]bool{}
	groups := []string{}

	for i, s := range propSpecs {
		if s.Key == "" {
			t.Errorf("propSpecs[%d] 缺少 Key", i)
			continue
		}
		if seen[s.Key] {
			t.Errorf("propSpecs[%d] Key %q 重复 —— propSpecIndex 只会留最后一个", i, s.Key)
		}
		seen[s.Key] = true

		if s.Group == "" {
			t.Errorf("%s 缺少 Group —— 界面上会出现一个没有名字的分组", s.Key)
		} else if !hasStr(groups, s.Group) {
			groups = append(groups, s.Group)
		}
		if s.Label == "" {
			t.Errorf("%s 缺少 Label", s.Key)
		}
		switch s.Kind {
		case "string", "int", "bool":
		case "enum":
			if len(s.Options) == 0 {
				t.Errorf("%s 是 enum 但没给 Options", s.Key)
			}
			// 枚举必须带中文名。把 survival / peaceful 原样丢给用户不算「每项解释」。
			for _, o := range s.Options {
				if s.Labels[o] == "" {
					t.Errorf("%s 的选项 %q 没有中文名", s.Key, o)
				}
			}
		default:
			t.Errorf("%s 的 Kind %q 不认识", s.Key, s.Kind)
		}
		if s.Value == "" && s.Kind != "string" && s.Kind != "enum" {
			t.Errorf("%s 是非字符串类型但没给默认值", s.Key)
		}

		// 范围只对 int 有意义，且不能写反
		if s.Kind != "int" && (s.Min != nil || s.Max != nil) {
			t.Errorf("%s 不是 int 却设了范围", s.Key)
		}
		if s.Min != nil && s.Max != nil && *s.Min > *s.Max {
			t.Errorf("%s 的范围写反了：min=%d max=%d", s.Key, *s.Min, *s.Max)
		}

		// 默认值必须能过自己的校验。否则「文件里没写这个键」时我们填进去展示的
		// 默认值自己就是非法的 —— 用户看着它点保存，被我们自己拒了。
		if err := validateProp(s.Key, s.Value); err != nil {
			t.Errorf("%s 的默认值 %q 通不过自己的校验：%v", s.Key, s.Value, err)
		}
	}

	// 只增不减：这个键表的价值就在覆盖面，被谁改回 23 个是退化
	if len(propSpecs) < 45 {
		t.Errorf("键表只有 %d 个 —— 覆盖面退化了？", len(propSpecs))
	}
	for _, g := range groups {
		if propGroupDesc[g] == "" {
			t.Errorf("分组「%s」没有说明文字", g)
		}
	}

	// 1.21.2（快照 25w35a）起这三个键已从 server.properties 迁到游戏规则，
	// 写进去服务端只会当未知键忽略 —— 加进来就是给用户一个按了没反应的开关。
	for _, dead := range []string{"spawn-monsters", "spawn-animals", "spawn-npcs"} {
		if _, ok := propSpecIndex[dead]; ok {
			t.Errorf("%s 在当前版本已被移除（迁到游戏规则了），不该出现在键表里", dead)
		}
	}
}

func TestValidateProp(t *testing.T) {
	cases := []struct {
		key, val string
		wantErr  bool
	}{
		{"max-players", "20", false},
		{"max-players", "abc", true},
		{"max-players", "", true},
		{"online-mode", "true", false},
		{"online-mode", "TRUE", false},
		{"online-mode", "yes", true},
		{"difficulty", "hard", false},
		{"difficulty", "hell", true},
		// 插件往 server.properties 里塞的自定义键也要能写，不能因为「不认识」就拒收
		{"some-plugin-key", "whatever", false},
		{"motd", "两行\n标语", true},
	}
	for _, c := range cases {
		err := validateProp(c.key, c.val)
		if (err != nil) != c.wantErr {
			t.Errorf("validateProp(%q, %q) 错误 = %v，期望出错 = %v", c.key, c.val, err, c.wantErr)
		}
	}
}

// 范围校验的回归测试。
//
// 这就是修 bug 的那条：旧 validateProp 只做 strconv.Atoi，**只验「是不是整数」不验范围**，
// 视距填 999、端口填 0 一律照收 —— 服务端读不懂会把整份配置回退成默认值，且不给任何提示。
// 把范围检查拆掉，下面 wantErr=true 的用例必须全红。
func TestValidatePropRangesSpec(t *testing.T) {
	cases := []struct {
		key, val string
		wantErr  bool
	}{
		{"view-distance", "2", true},
		{"view-distance", "3", false},
		{"view-distance", "32", false},
		{"view-distance", "33", true},
		{"view-distance", "999", true},
		{"simulation-distance", "1", true},
		{"simulation-distance", "32", false},
		{"server-port", "0", true},
		{"server-port", "1", false},
		{"server-port", "65534", false},
		{"server-port", "65535", true},
		{"op-permission-level", "0", false},
		{"op-permission-level", "5", true},
		{"function-permission-level", "0", true},
		{"function-permission-level", "4", false},
		{"max-world-size", "0", true},
		{"max-world-size", "29999984", false},
		{"max-world-size", "29999985", true},
		{"entity-broadcast-range-percentage", "9", true},
		{"entity-broadcast-range-percentage", "1000", false},
		{"rcon.port", "65535", true},
		{"query.port", "65534", false},
		{"spawn-protection", "0", false},
		{"player-idle-timeout", "0", false},
		{"rate-limit", "-1", true},
		// min = -1 是专门为这两个键设的：-1 有特殊含义（不压缩 / 不自动重启）
		{"max-tick-time", "-1", false},
		{"max-tick-time", "-2", true},
		{"max-tick-time", "0", false},
		{"network-compression-threshold", "-1", false},
	}
	for _, c := range cases {
		err := validateProp(c.key, c.val)
		if c.wantErr && err == nil {
			t.Errorf("validateProp(%q, %q) 该报错却放行了", c.key, c.val)
		}
		if !c.wantErr && err != nil {
			t.Errorf("validateProp(%q, %q) 不该报错：%v", c.key, c.val, err)
		}
	}
}

// ── 立即生效 ──────────────────────────────────

func TestLiveCommandSpec(t *testing.T) {
	cases := []struct {
		key, val, want string
		ok             bool
	}{
		{"difficulty", "hard", "difficulty hard", true},
		{"gamemode", "creative", "defaultgamemode creative", true},
		{"pvp", "false", "gamerule pvp false", true},
		{"white-list", "true", "whitelist on", true},
		{"white-list", "false", "whitelist off", true},
		// 值非法时不能硬拼一条指令发出去
		{"white-list", "maybe", "", false},
		// 这些键**没有**对应指令，必须老老实实说不行。
		// 假装「立即生效」比不生效更坑：用户以为改好了，重启后打回原形。
		{"motd", "hi", "", false},
		{"view-distance", "10", "", false},
		{"max-players", "20", "", false},
		{"level-name", "world2", "", false},
	}
	for _, c := range cases {
		got, ok := liveCommand(c.key, c.val)
		if ok != c.ok || got != c.want {
			t.Errorf("liveCommand(%q, %q) = (%q, %v)，想要 (%q, %v)", c.key, c.val, got, ok, c.want, c.ok)
		}
	}

	// Live 标记和 liveCommand 必须是同一个口径 ——
	// 对不上就会在界面上给出一个「点了没反应」或「点了直接报错」的按钮。
	for _, s := range propSpecs {
		sample := s.Value
		if s.Kind == "bool" {
			sample = "true"
		}
		_, ok := liveCommand(s.Key, sample)
		if s.Live != ok {
			t.Errorf("%s 的 Live 标记是 %v，但 liveCommand 说 %v —— 两处口径不一致", s.Key, s.Live, ok)
		}
	}
}

// ── 读取：枚举兜底与 InFile ────────────────────

// newPropTestManager 建一个实例并把 server.properties 原样写进去。
// props 传空字符串表示「这个实例还没有 server.properties」。
func newPropTestManager(t *testing.T, props string) (*Manager, string, string) {
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
	inst, ok := m.Get("i1")
	if !ok {
		t.Fatal("实例没建出来")
	}
	path := filepath.Join(inst.Dir, "server.properties")
	if props != "" {
		if err := os.WriteFile(path, []byte(props), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return m, "i1", path
}

func rowOf(t *testing.T, rows []PropertyRow, key string) PropertyRow {
	t.Helper()
	for _, r := range rows {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("字段列表里没有 %s", key)
	return PropertyRow{}
}

func hasStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// 文件里的枚举值不在预设里时，必须把它补进 Options。
// 不补的话 <select> 找不到匹配项会**静默显示第一项** —— 界面撒谎，
// 用户随手一保存就把一个他从没选过的值写进配置文件。这是最隐蔽的一类配置事故。
func TestReadPropertiesAppendsUnknownEnumValue(t *testing.T) {
	m, id, _ := newPropTestManager(t, "region-file-compression=zstd\n")

	rows, err := m.ReadProperties(id)
	if err != nil {
		t.Fatal(err)
	}
	rc := rowOf(t, rows, "region-file-compression")
	if rc.Value != "zstd" {
		t.Errorf("读到的值是 %q，想要 zstd", rc.Value)
	}
	if !hasStr(rc.Options, "zstd") {
		t.Errorf("zstd 没被补进 Options（%v）—— 下拉框会显示成第一项，等于撒谎", rc.Options)
	}
	if rc.Labels["zstd"] == "" {
		t.Error("兜底补进来的选项没有说明文字，用户不知道这是什么")
	}
}

// 老档的裸值要补命名空间：1.19 之前写 flat，现在写 minecraft:flat。
func TestReadPropertiesNormalizesLegacyLevelType(t *testing.T) {
	m, id, _ := newPropTestManager(t, "level-type=flat\n")

	rows, err := m.ReadProperties(id)
	if err != nil {
		t.Fatal(err)
	}
	lt := rowOf(t, rows, "level-type")
	if lt.Value != "minecraft:flat" {
		t.Errorf("老写法 flat 没被归一化，读成 %q", lt.Value)
	}
	if !hasStr(lt.Options, lt.Value) {
		t.Error("归一化后的值不在选项里 —— 下拉框又会显示成第一项")
	}
}

func TestReadPropertiesReportsInFile(t *testing.T) {
	m, id, _ := newPropTestManager(t, "motd=我的服\n\n# 注释行\n这一行没有等号\n")

	rows, err := m.ReadProperties(id)
	if err != nil {
		t.Fatal(err)
	}
	if r := rowOf(t, rows, "motd"); !r.InFile || r.Value != "我的服" {
		t.Errorf("motd 应该在文件里且值为「我的服」，实际 InFile=%v Value=%q", r.InFile, r.Value)
	}
	// 文件里没写的键也要有条目（用户能提前把配置定下来），但必须标明
	// 「这不是文件里的值」—— 否则界面会把我们填的默认值当成当前配置展示。
	vd := rowOf(t, rows, "view-distance")
	if vd.InFile {
		t.Error("view-distance 不在文件里，InFile 却是 true")
	}
	if vd.Value != "10" || vd.Default != "10" {
		t.Errorf("不在文件里的键该显示官方默认值 10，实际 value=%q default=%q", vd.Value, vd.Default)
	}
	// 没有等号的行不能被当成键读进来
	for _, r := range rows {
		if strings.Contains(r.Key, "这一行") {
			t.Errorf("没有等号的行被当成配置读进来了：%q", r.Key)
		}
	}
}

func TestReadPropertiesListsUnknownKeys(t *testing.T) {
	m, id, _ := newPropTestManager(t, "motd=x\nzzz-plugin-toggle=true\n")

	rows, err := m.ReadProperties(id)
	if err != nil {
		t.Fatal(err)
	}
	u := rowOf(t, rows, "zzz-plugin-toggle")
	if u.Known {
		t.Error("插件自己加的键不该被当成我们认得的键")
	}
	if u.Value != "true" {
		t.Errorf("未知键的值读错了：%q", u.Value)
	}
	if u.Group != "其它（不认得）" {
		t.Errorf("未知键该归到「其它（不认得）」，实际 %q", u.Group)
	}
}

func TestReadPropertiesNoFileYieldsDefaults(t *testing.T) {
	m, id, _ := newPropTestManager(t, "")

	rows, err := m.ReadProperties(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(propSpecs) {
		t.Errorf("没有配置文件时该返回全部 %d 个默认字段，实际 %d 个", len(propSpecs), len(rows))
	}
	for _, r := range rows {
		if r.InFile {
			t.Errorf("%s 的 InFile 是 true，但文件根本不存在", r.Key)
		}
	}
}

// ── 写入：坏键名与坏范围都不许碰文件 ────────────

func TestWritePropertiesRejectsBadKey(t *testing.T) {
	m, id, path := newPropTestManager(t, "motd=hi\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// 键里带 = 会把值截断；带换行会凭空多出一行配置；
	// 带空格/制表符会被 Java 的 Properties 解析成另一个键（空白也是分隔符）。
	// 三种都能把文件写坏，必须全部挡在写盘之前。
	for _, bad := range []string{"a=b", "a\nb", "  spaced", "", "tab\tkey", "a:b"} {
		if err := m.WriteProperties(id, map[string]string{bad: "x"}); err == nil {
			t.Errorf("键 %q 应该被拒绝", bad)
		}
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("校验失败时不该动文件：\nbefore=%s\nafter=%s", before, after)
	}
}

func TestWritePropertiesRangeRejectedKeepsFileIntact(t *testing.T) {
	m, id, path := newPropTestManager(t, "view-distance=10\n")

	if err := m.WriteProperties(id, map[string]string{"view-distance": "999"}); err == nil {
		t.Fatal("视距 999 应该被拒绝")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "view-distance=10") {
		t.Errorf("被拒绝的写入不该改到文件：%s", b)
	}
}

func TestWritePropertiesKeepsOtherLinesAndComments(t *testing.T) {
	m, id, path := newPropTestManager(t, "# 我自己的注释\nmotd=旧标语\nview-distance=8\n")

	if err := m.WriteProperties(id, map[string]string{"motd": "新标语"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "# 我自己的注释") {
		t.Errorf("注释被吃掉了：\n%s", got)
	}
	if !strings.Contains(got, "motd=新标语") {
		t.Errorf("motd 没改成功：\n%s", got)
	}
	if !strings.Contains(got, "view-distance=8") {
		t.Errorf("没让改的键被动了：\n%s", got)
	}
}

// 未知键仍然要能写进去（插件靠这个），别被这一轮校验误伤
func TestWritePropertiesAllowsUnknownKey(t *testing.T) {
	m, id, path := newPropTestManager(t, "motd=hi\n")

	if err := m.WriteProperties(id, map[string]string{"zzz-plugin-toggle": "true"}); err != nil {
		t.Fatalf("插件键被误拒了：%v", err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "zzz-plugin-toggle=true") {
		t.Errorf("插件键没写进去：\n%s", b)
	}
}
