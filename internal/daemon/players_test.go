package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── 离线 UUID ──
//
// ⚠ 下面这组基准值是**本机 JDK 21 实际跑出来的**，不是抄的：
//
//	java UuidProbe.java     // UUID.nameUUIDFromBytes(("OfflinePlayer:"+name).getBytes(UTF_8))
//
// 覆盖了大小写、下划线、单字符、以及多字节 UTF-8。
// UUID 算错的后果特别难查：白名单里明明有这个人，他就是进不来，
// 而服务端只会说一句「你不在白名单里」—— 完全指不到 UUID 头上去。
func TestOfflineUUIDMatchesJava(t *testing.T) {
	cases := map[string]string{
		"Notch":     "b50ad385-829d-3141-a216-7e7d7539ba7f",
		"Steve":     "5627dd98-e6be-3c21-b8a8-e92344183641",
		"jeb_":      "a762f560-4fce-3236-812a-b80efff0b62b",
		"Alex":      "36532b5e-c442-3dbb-a24c-c7e55d0f979a",
		"test_user": "109fb397-58f2-3a9a-92c1-5a21ac5113c2",
		"a":         "52428a0e-1e30-3cb1-976c-e728b2614047",
		"玩家一号":      "563807d4-80f7-3b64-9ebb-4dbce5bc9f41",
	}
	for name, want := range cases {
		if got := offlineUUID(name); got != want {
			t.Errorf("offlineUUID(%q) = %s，JDK 算出来是 %s", name, got, want)
		}
	}
}

// 把「第 6 字节置 3、第 8 字节置 10」这两步单独钉住。
// 漏掉它们算出来的仍然是一串 36 位的 hex，肉眼完全看不出差别 —— 唯二的破绽就是这两处。
func TestOfflineUUIDVersionAndVariantBits(t *testing.T) {
	for _, name := range []string{"Notch", "Steve", "a", "玩家一号"} {
		u := offlineUUID(name)
		if len(u) != 36 {
			t.Fatalf("%q 的 UUID 该是 36 字符，实际 %d（%s）", name, len(u), u)
		}
		// 第 3 段（版本段）以 3 开头 = 基于名字的 v3 UUID
		if u[14] != '3' {
			t.Errorf("%q 的版本位该是 3，实际 %q（%s）", name, u[14], u)
		}
		// 第 4 段（变体段）以 8/9/a/b 开头 = IETF 变体
		if c := u[19]; c != '8' && c != '9' && c != 'a' && c != 'b' {
			t.Errorf("%q 的变体位该是 8/9/a/b，实际 %q（%s）", name, c, u)
		}
	}
}

// ── 玩家名校验 ──

func TestValidPlayerName(t *testing.T) {
	good := []string{"Steve", "jeb_", "a", "Player_123", "AAAAAAAAAAAAAAAA"} // 16 位是上限
	for _, n := range good {
		if !validPlayerName(n) {
			t.Errorf("%q 该是合法玩家名", n)
		}
	}
	bad := []string{
		"", "AAAAAAAAAAAAAAAAA", // 17 位
		"玩家一号",    // 中文：12 字节，长度这关过得去，靠逐字节检查才挡得住
		"steve a", // 空格
		"steve-a", // 连字符
		"steve.a",
		"steve#",
	}
	for _, n := range bad {
		if validPlayerName(n) {
			t.Errorf("%q 不该被接受", n)
		}
	}
}

// ── 名单文件读写 ──

func TestReadJSONListHandlesMissingAndEmpty(t *testing.T) {
	dir := t.TempDir()

	// 文件不存在 = 空列表，不是错误。新实例就是这样：
	// 报错的话整个玩家页会红一片，而实际上什么问题都没有。
	list, err := readJSONList[PlayerEntry](filepath.Join(dir, "nope.json"))
	if err != nil {
		t.Errorf("文件不存在不该报错：%v", err)
	}
	if list == nil {
		t.Error("该返回空列表而不是 nil")
	}

	// 空文件同理
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if l, err := readJSONList[PlayerEntry](empty); err != nil || len(l) != 0 {
		t.Errorf("空文件该当成空列表，实际 len=%d err=%v", len(l), err)
	}

	// 写坏的 json 要报错（不能静默当空 —— 那会把服务端的名单读成「没人」）
	broken := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(broken, []byte(`[{"uuid":"x"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readJSONList[PlayerEntry](broken); err == nil {
		t.Error("半截 json 该报错")
	}
}

func TestWriteJSONListAtomicAndShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "whitelist.json")

	// 父目录不存在也要能写（实例目录可能是刚建的）
	if err := writeJSONList(path, []PlayerEntry{{UUID: "u1", Name: "Steve"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 字段名必须和服务端认的一致
	if !strings.Contains(string(b), `"uuid"`) || !strings.Contains(string(b), `"name"`) {
		t.Errorf("字段名不对：%s", b)
	}
	// 临时文件不能留下
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("写完该把 .tmp 移走，不该留在磁盘上")
	}

	// nil 要写成 []，不能写成 null —— 服务端读到 null 会当成空名单，
	// 但那是「碰巧」，不是我们该依赖的行为。
	if err := writeJSONList[PlayerEntry](path, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.TrimSpace(string(b)) != "[]" {
		t.Errorf("空列表该写成 []，实际 %s", b)
	}
}

// ── 停服轨：直接改文件 ──

func TestWhitelistSetOffline(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(inst.Dir, fileWhitelist)

	// 服务端没跑 → 走文件轨
	if err := m.WhitelistSet("i1", "Steve", true); err != nil {
		t.Fatal(err)
	}
	list, err := readJSONList[PlayerEntry](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Steve" {
		t.Fatalf("该有一条 Steve，实际 %+v", list)
	}
	// UUID 必须是离线算法算出来的那个，不是随便填的
	if list[0].UUID != offlineUUID("Steve") {
		t.Errorf("UUID 该是 %s，实际 %s", offlineUUID("Steve"), list[0].UUID)
	}

	// 重复加不该加出两条
	if err := m.WhitelistSet("i1", "Steve", true); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[PlayerEntry](path); len(list) != 1 {
		t.Errorf("重复添加不该多出一条，实际 %d 条", len(list))
	}

	// 再加一个，并确认按名字排序（顺序不稳定的话界面每次刷新都会重排）
	if err := m.WhitelistSet("i1", "Alex", true); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[PlayerEntry](path); len(list) != 2 || list[0].Name != "Alex" {
		t.Errorf("该按名字排序成 Alex, Steve，实际 %+v", list)
	}

	// 移除
	if err := m.WhitelistSet("i1", "Steve", false); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[PlayerEntry](path); len(list) != 1 || list[0].Name != "Alex" {
		t.Errorf("移除后该只剩 Alex，实际 %+v", list)
	}

	// 移除一个本来就不在的 —— 要报错，不能静默当成功
	if err := m.WhitelistSet("i1", "Notch", false); err == nil {
		t.Error("移除不在名单里的人该报错")
	}

	// 非法名字
	if err := m.WhitelistSet("i1", "玩家一号", true); err == nil {
		t.Error("中文名该被拒")
	}
	if err := m.WhitelistSet("i1", "", true); err == nil {
		t.Error("空名字该被拒")
	}

	// 实例不存在
	if err := m.WhitelistSet("nope", "Steve", true); err == nil {
		t.Error("实例不存在该报错")
	}
}

func TestOpSetOffline(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(inst.Dir, fileOps)

	if err := m.OpSet("i1", "Alex", 2, true); err != nil {
		t.Fatal(err)
	}
	list, err := readJSONList[OpEntry](path)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Level != 2 || list[0].Name != "Alex" {
		t.Fatalf("该有一条 2 级的 Alex，实际 %+v", list)
	}
	if list[0].UUID != offlineUUID("Alex") {
		t.Error("UUID 该按离线算法算")
	}
	// 服务端的 ops.json 必须带这个字段，缺了它服务端会当成默认值
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "bypassesPlayerLimit") {
		t.Errorf("该写出 bypassesPlayerLimit 字段：%s", b)
	}

	// 改等级
	if err := m.OpSet("i1", "Alex", 4, true); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[OpEntry](path); len(list) != 1 || list[0].Level != 4 {
		t.Errorf("等级该被改成 4，实际 %+v", list)
	}

	// 等级越界
	if err := m.OpSet("i1", "Alex", 5, true); err == nil {
		t.Error("等级 5 该被拒")
	}
	if err := m.OpSet("i1", "Alex", 0, true); err == nil {
		t.Error("等级 0 该被拒")
	}

	// 撤销
	if err := m.OpSet("i1", "Alex", 0, false); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[OpEntry](path); len(list) != 0 {
		t.Errorf("撤销后该是空的，实际 %+v", list)
	}
	if err := m.OpSet("i1", "Alex", 0, false); err == nil {
		t.Error("撤销一个本来就不是管理员的人该报错")
	}
}

func TestBanSetOffline(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}

	// 封一个玩家
	if err := m.BanSet("i1", "Steve", "player", "用外挂", time.Time{}, true); err != nil {
		t.Fatal(err)
	}
	list, err := readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("该有一条封禁记录，实际 %+v", list)
	}
	b := list[0]
	if b.Name != "Steve" || b.Reason != "用外挂" || b.Expires != "forever" {
		t.Errorf("封禁记录字段不对：%+v", b)
	}
	// 服务端要认这个时间格式 "yyyy-MM-dd HH:mm:ss +0800"
	if !strings.Contains(b.Created, "-") || !strings.Contains(b.Created, "+") {
		t.Errorf("时间格式该是 Java 的 yyyy-MM-dd HH:mm:ss Z，实际 %q", b.Created)
	}
	// 字段名必须和服务端一致，缺字段服务端会拒绝这条记录
	raw, _ := os.ReadFile(filepath.Join(inst.Dir, fileBanned))
	for _, key := range []string{"uuid", "name", "created", "source", "expires", "reason"} {
		if !strings.Contains(string(raw), `"`+key+`"`) {
			t.Errorf("banned-players.json 缺少字段 %q：%s", key, raw)
		}
	}

	// 重复封禁要报错，不能写出两条
	if err := m.BanSet("i1", "Steve", "player", "", time.Time{}, true); err == nil {
		t.Error("重复封禁该报错")
	}
	// 没写原因时给个默认值（服务端自己也是这么做的）
	if err := m.BanSet("i1", "Alex", "player", "", time.Time{}, true); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned)); len(list) != 2 {
		t.Fatalf("该有 2 条，实际 %+v", list)
	}

	// 解封
	if err := m.BanSet("i1", "Steve", "player", "", time.Time{}, false); err != nil {
		t.Fatal(err)
	}
	if list, _ = readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned)); len(list) != 1 || list[0].Name != "Alex" {
		t.Errorf("解封后该只剩 Alex，实际 %+v", list)
	}
	if err := m.BanSet("i1", "Notch", "player", "", time.Time{}, false); err == nil {
		t.Error("解封一个没被封的人该报错")
	}

	// 封 IP
	if err := m.BanSet("i1", "1.2.3.4", "ip", "刷屏", time.Time{}, true); err != nil {
		t.Fatal(err)
	}
	ips, err := readJSONList[IPBanEntry](filepath.Join(inst.Dir, fileBannedIPs))
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].IP != "1.2.3.4" || ips[0].Reason != "刷屏" {
		t.Fatalf("IP 封禁记录不对：%+v", ips)
	}
	// 玩家和 IP 是两份不同的文件，别写串了
	if list, _ = readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned)); len(list) != 1 {
		t.Error("封 IP 不该往 banned-players.json 里写")
	}
	// 非法 IP
	if err := m.BanSet("i1", "不是IP", "ip", "", time.Time{}, true); err == nil {
		t.Error("非法 IP 该被拒")
	}
	if err := m.BanSet("i1", "1.2.3.4", "ip", "", time.Time{}, false); err != nil {
		t.Fatal(err)
	}
}

// ── 限时封禁 ──

// 运行态在单元测试里造不出来（要真起一个进程），所以判据被抽成了纯函数
// banRejectReason，这里直接覆盖它 —— 否则「运行中 + 限时」这条分支永远没有测试，
// 而它恰恰是最容易写错、后果又最坑的一条：用户以为封了 7 天，实际是永久。
func TestBanRejectReasonSpec(t *testing.T) {
	future := time.Now().Add(7 * 24 * time.Hour)
	cases := []struct {
		name    string
		running bool
		on      bool
		expires time.Time
		wantErr bool
	}{
		{"停服+永久", false, true, time.Time{}, false},
		{"停服+限时", false, true, future, false},
		{"停服+解封", false, false, time.Time{}, false},
		{"运行中+永久", true, true, time.Time{}, false},
		{"运行中+限时→拒", true, true, future, true},
		{"运行中+解封（expires 不参与判断）", true, false, future, false},
	}
	for _, c := range cases {
		err := banRejectReason(c.running, c.on, c.expires)
		if c.wantErr && err == nil {
			t.Errorf("%s：该被拒但过了", c.name)
			continue
		}
		if !c.wantErr && err != nil {
			t.Errorf("%s：不该拒，实际 %v", c.name, err)
			continue
		}
		if c.wantErr && !errors.Is(err, ErrBanNeedsStop) {
			t.Errorf("%s：错误要能被 errors.Is(ErrBanNeedsStop) 认出来（上层据此给 409），实际 %v",
				c.name, err)
		}
	}
}

// javaBanTime 解析 mcTime 写出来的时间串。规格表在这里算「反向操作」：
// 只测格式串等于没测，得把它解析回时间点再和入参比。
func javaBanTime(t *testing.T, s string) time.Time {
	t.Helper()
	got, err := time.Parse("2006-01-02 15:04:05 -0700", s)
	if err != nil {
		t.Fatalf("expires 不是服务端认的格式（Java 的 yyyy-MM-dd HH:mm:ss Z）：%q", s)
	}
	return got
}

func TestBanSetTimedWritesExpiry(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}

	until := time.Now().Add(72 * time.Hour).Truncate(time.Second)
	if err := m.BanSet("i1", "Steve", "player", "刷屏", until, true); err != nil {
		t.Fatal(err)
	}
	list, err := readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("该有一条封禁，实际 %+v", list)
	}
	if list[0].Expires == "forever" || list[0].Expires == "" {
		t.Fatalf("限时封禁不该写成永久，实际 %q", list[0].Expires)
	}
	if got := javaBanTime(t, list[0].Expires); !got.Equal(until) {
		t.Errorf("到期时间对不上：入参 %s，写出来的解回来是 %s", until, got)
	}

	// IP 那份文件走的是另一条分支（applyIPBan），漏了它等于只修了一半
	if err := m.BanSet("i1", "1.2.3.4", "ip", "", until, true); err != nil {
		t.Fatal(err)
	}
	ips, err := readJSONList[IPBanEntry](filepath.Join(inst.Dir, fileBannedIPs))
	if err != nil {
		t.Fatal(err)
	}
	if len(ips) != 1 || ips[0].Expires == "forever" {
		t.Fatalf("IP 限时封禁也不该写成永久：%+v", ips)
	}

	// 永久封禁仍然是字面量 "forever" —— 服务端认这个值，空串它解析不了
	if err := m.BanSet("i1", "Alex", "player", "", time.Time{}, true); err != nil {
		t.Fatal(err)
	}
	list, _ = readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned))
	for _, b := range list {
		if b.Name == "Alex" && b.Expires != "forever" {
			t.Errorf("永久封禁该写 forever，实际 %q", b.Expires)
		}
	}
}

// 到点时间在过去 → 拒。否则会写出一条「一进游戏就自动解封」的记录：
// 界面显示已封禁，实际毫无作用。
func TestBanSetRejectsPastExpiry(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	err := m.BanSet("i1", "Steve", "player", "", time.Now().Add(-time.Hour), true)
	if err == nil {
		t.Fatal("到点时间在过去该被拒")
	}
	if _, statErr := os.Stat(filepath.Join(inst.Dir, fileBanned)); !os.IsNotExist(statErr) {
		t.Error("被拒的请求不该留下封禁文件")
	}
}

// ── 玩家档案（世界目录里的只读痕迹）──

func writeProps(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "server.properties"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFileAt(t *testing.T, path, body string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPlayerProfileReadsWorldFiles(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// 离线服才会由面板按名字算 UUID
	writeProps(t, inst.Dir, "online-mode=false\n")

	uuid := offlineUUID("Steve")
	// play_time 单位是 tick：720000 tick = 10 小时
	writeFileAt(t, filepath.Join(inst.Dir, "world", "stats", uuid+".json"),
		`{"stats":{"minecraft:custom":{"minecraft:play_time":720000,`+
			`"minecraft:deaths":3,"minecraft:player_kills":5,"minecraft:mob_kills":120},`+
			`"minecraft:mined":{"minecraft:stone":99}},"DataVersion":4189}`, time.Time{})

	seen := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	writeFileAt(t, filepath.Join(inst.Dir, "world", "playerdata", uuid+".dat"), "\x0a", seen)

	p, err := m.PlayerProfile("i1", "Steve", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.UUID != uuid {
		t.Errorf("离线服该按名字算 UUID：want %s got %s", uuid, p.UUID)
	}
	if !p.HasStats || p.PlayTicks != 720000 || p.Deaths != 3 || p.PlayerKills != 5 || p.MobKills != 120 {
		t.Errorf("统计读错了：%+v", p)
	}
	if p.Note != "" {
		t.Errorf("有数据时不该给解释文案，实际 %q", p.Note)
	}
	if p.LastSeen == "" {
		t.Fatal("有 playerdata 时该给出最后进服时间")
	}
	ls, err := time.Parse(time.RFC3339, p.LastSeen)
	if err != nil {
		t.Fatalf("LastSeen 该是 RFC3339，实际 %q", p.LastSeen)
	}
	if !ls.Equal(seen) {
		t.Errorf("最后进服时间该取 playerdata 的 mtime：want %s got %s", seen, ls)
	}
}

// 1.17 之前统计项叫 play_one_minute（名字里的「分钟」是历史遗留，单位同样是 tick）。
// 不兜这个键的话，老存档的游玩时长会显示成 0 小时。
func TestPlayerProfileFallsBackToPlayOneMinute(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeProps(t, inst.Dir, "online-mode=false\n")

	uuid := offlineUUID("Alex")
	writeFileAt(t, filepath.Join(inst.Dir, "world", "stats", uuid+".json"),
		`{"stats":{"minecraft:custom":{"minecraft:play_one_minute":360000}}}`, time.Time{})

	p, err := m.PlayerProfile("i1", "Alex", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.PlayTicks != 360000 {
		t.Errorf("该回落到 play_one_minute，实际 PlayTicks=%d", p.PlayTicks)
	}
}

// 拿不到数据时必须给出原因。显示成「0 小时 / 0 次死亡」是在编数据 ——
// 玩家可能只是还没进过服。
func TestPlayerProfileExplainsMissingData(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeProps(t, inst.Dir, "online-mode=false\n")

	p, err := m.PlayerProfile("i1", "Nobody", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.HasStats {
		t.Error("没有 stats 文件时不该说 HasStats")
	}
	if p.LastSeen != "" {
		t.Errorf("没有 playerdata 时 LastSeen 该是空的，实际 %q", p.LastSeen)
	}
	if p.Note == "" {
		t.Error("拿不到数据时必须说明原因，不能静默显示 0")
	}
}

// 正版服拿不到 UUID 时**不编**。编了用户会拿一个错 UUID 去别处对，然后发现对不上，
// 却查不出是面板自己造的。
func TestPlayerProfileOnlineModeWithoutUUID(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeProps(t, inst.Dir, "online-mode=true\n")

	p, err := m.PlayerProfile("i1", "Steve", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.UUID != "" {
		t.Errorf("正版服又不给 uuid 时不该编一个，实际 %q", p.UUID)
	}
	if p.Note == "" {
		t.Error("该说明为什么读不到档案")
	}
}

// level-name 改过名的实例：写死 "world" 会去读一个不存在的目录，
// 然后安静地报「没数据」—— 而数据其实在 myworld/ 里躺着。
func TestPlayerProfileHonoursLevelName(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeProps(t, inst.Dir, "online-mode=false\nlevel-name=myworld\n")

	uuid := offlineUUID("Steve")
	writeFileAt(t, filepath.Join(inst.Dir, "myworld", "stats", uuid+".json"),
		`{"stats":{"minecraft:custom":{"minecraft:play_time":20000}}}`, time.Time{})

	p, err := m.PlayerProfile("i1", "Steve", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.WorldName != "myworld" {
		t.Errorf("世界目录名该取 level-name，实际 %q", p.WorldName)
	}
	if !p.HasStats || p.PlayTicks != 20000 {
		t.Errorf("该从 myworld/ 里读到统计，实际 %+v", p)
	}
}

// 实例不存在 → ErrInstanceNotFound（上层据此给 404），不能悄悄返回一份空档案。
func TestPlayerProfileUnknownInstance(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.PlayerProfile("nope", "Steve", ""); err == nil {
		t.Error("实例不存在该报错")
	}
}

func TestKickRequiresRunningServer(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// 没跑就是没跑，要说清楚，别发一条注定没人接的命令
	err := m.KickPlayer("i1", "Steve", "测试")
	if err == nil {
		t.Fatal("服务器没跑时踢人该报错")
	}
	if !strings.Contains(err.Error(), "没在运行") {
		t.Errorf("错误信息该说清楚原因，实际 %q", err.Error())
	}
	if err := m.KickPlayer("i1", "中文名", "x"); err == nil {
		t.Error("非法玩家名该被拒")
	}
}

// ── 汇总视图 ──

// nil slice 序列化成 null 会把前端的 .length 打崩 —— 这个坑备份策略栽过一次。
func TestPlayerListsNeverSerializeNull(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}

	r, err := m.PlayerLists("i1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Online == nil || r.Whitelist == nil || r.Ops == nil || r.Banned == nil || r.BannedIPs == nil {
		t.Fatal("四个名单和在线列表都必须非 nil")
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"online":null`, `"whitelist":null`, `"ops":null`, `"banned":null`, `"bannedIps":null`} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("序列化里出现了 %s：%s", bad, raw)
		}
	}
	if r.Running {
		t.Error("没在跑的实例不该报 Running")
	}
	if r.WhitelistEnabled {
		t.Error("读不到 server.properties 时该保守地报「白名单没开」")
	}

	// 读一份真名单回来
	if err := m.WhitelistSet("i1", "Steve", true); err != nil {
		t.Fatal(err)
	}
	if r, _ = m.PlayerLists("i1"); len(r.Whitelist) != 1 {
		t.Errorf("该读到 1 条白名单，实际 %d", len(r.Whitelist))
	}

	if _, err := m.PlayerLists("nope"); err == nil {
		t.Error("实例不存在该报错")
	}
}

// 白名单开关要如实读出来 —— 没开的时候往里加人，加了也不生效，
// 界面得能提前说这句话。
func TestPlayerListsReadsWhitelistSwitch(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.Dir, "server.properties"), []byte("white-list=true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := m.PlayerLists("i1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.WhitelistEnabled {
		t.Error("server.properties 里 white-list=true，该如实报出来")
	}
}

// ── 在线玩家解析 ──

func TestTrackOnlinePlayers(t *testing.T) {
	p := &Process{}

	p.track("[12:00:00] [Server thread/INFO]: Steve joined the game")
	p.track("[12:00:05] [Server thread/INFO]: Alex joined the game")
	if got := p.PlayerNames(); len(got) != 2 || got[0] != "Alex" || got[1] != "Steve" {
		t.Fatalf("该有 Alex、Steve（且已排序），实际 %v", got)
	}

	p.track("[12:01:00] [Server thread/INFO]: Steve left the game")
	if got := p.PlayerNames(); len(got) != 1 || got[0] != "Alex" {
		t.Fatalf("Steve 走了之后该只剩 Alex，实际 %v", got)
	}

	// /list 的回执是最权威的校正：冒号后面就是完整名单，直接覆盖
	p.track("[12:02:00] [Server thread/INFO]: There are 3 of a max of 20 players online: Steve, Alex, Notch")
	if got := p.PlayerNames(); len(got) != 3 {
		t.Fatalf("该被 /list 校正成 3 个，实际 %v", got)
	}

	// 一个都没有
	p.track("[12:03:00] [Server thread/INFO]: There are 0 of a max of 20 players online:")
	if got := p.PlayerNames(); len(got) != 0 {
		t.Fatalf("该清空，实际 %v", got)
	}
}

// 名单和计数对不上（服务端换了语言、改了措辞）时，宁可不动 ——
// 把在线的玩家凭空抹掉比暂时不准更糟。
func TestTrackDoesNotWipeOnMismatch(t *testing.T) {
	p := &Process{}
	p.track("[12:00:00] [Server thread/INFO]: Steve joined the game")
	p.track("[12:02:00] [Server thread/INFO]: There are 3 of a max of 20 players online: Steve")
	// 说是 3 个但只列了 1 个 → 不采信
	if got := p.PlayerNames(); len(got) != 1 || got[0] != "Steve" {
		t.Errorf("计数与名单对不上时该保持原样，实际 %v", got)
	}
}

// 颜色控制符要剥掉，否则名字里带一个看不见的字符，和白名单比对永远对不上。
func TestStripMCCodes(t *testing.T) {
	if got := stripMCCodes("§aSteve"); got != "Steve" {
		t.Errorf("该剥成 Steve，实际 %q", got)
	}
	if got := stripMCCodes("Steve"); got != "Steve" {
		t.Errorf("没有控制符时该原样返回，实际 %q", got)
	}
}

// 服务端没跑时不该报在线玩家 —— 进程对象可能还挂着上次的记录。
func TestOnlinePlayersEmptyWhenStopped(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	if got := m.OnlinePlayers("i1"); len(got) != 0 {
		t.Errorf("没跑的运行实例该报 0 个在线，实际 %v", got)
	}
}

// 名册批量读时长。要验证两件事：
// ① 同一个人在不同名单里出现多次只读一遍（白名单 + OP 常是同一个人）；
// ② 没有档案的人**返回空条目**，不是编一个 0 —— 0 在界面上会被读成「他玩了 0 秒」。
func TestRosterExtrasBatchDedupesAndLeavesMissingEmpty(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeProps(t, inst.Dir, "online-mode=false\n")

	uuid := offlineUUID("Steve")
	writeFileAt(t, filepath.Join(inst.Dir, "world", "stats", uuid+".json"),
		`{"stats":{"minecraft:custom":{"minecraft:play_time":720000}}}`, time.Time{})

	got := m.RosterExtras("i1", []rosterWant{
		{Name: "Steve"}, {Name: "Steve"}, // 故意重复：白名单 + OP 是同一个人
		{Name: "Alex"},
	})
	if len(got) != 2 {
		t.Fatalf("按小写名去重后该是 2 个人，实际 %d：%+v", len(got), got)
	}
	if got["steve"].PlayTicks != 720000 {
		t.Errorf("Steve 的时长该读出来，实际 %+v", got["steve"])
	}
	if e := got["alex"]; e.PlayTicks != 0 || e.LastSeen != "" {
		t.Errorf("没有档案的人该给空条目，实际 %+v", e)
	}
}

// 正版服里面板算不出 UUID（不是 MD5 出来的），但 whitelist.json 里记着 ——
// 不把名单里的 UUID 带进去，正版服一个人都读不出时长。
func TestRosterExtrasUsesUUIDFromRoster(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeProps(t, inst.Dir, "online-mode=true\n")

	const premium = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	writeFileAt(t, filepath.Join(inst.Dir, "world", "stats", premium+".json"),
		`{"stats":{"minecraft:custom":{"minecraft:play_time":360000}}}`, time.Time{})

	got := m.RosterExtras("i1", []rosterWant{{Name: "Notch", UUID: premium}})
	if got["notch"].PlayTicks != 360000 {
		t.Errorf("正版服该用名单里的 UUID 去读档案，实际 %+v", got)
	}

	// 没有 UUID 的人读不出 → 给空条目，不编数据
	got2 := m.RosterExtras("i1", []rosterWant{{Name: "Someone"}})
	if e := got2["someone"]; e.PlayTicks != 0 || e.LastSeen != "" {
		t.Errorf("正版服拿不到 UUID 时不该编数据，实际 %+v", e)
	}
}
