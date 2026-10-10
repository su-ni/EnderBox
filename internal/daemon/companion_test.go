package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/su-ni/EnderBox/internal/companion"
)

// TestCompanionSupportSpec 「哪些端能用」的规格表。
//
// 这张表存在的意义：判定错了会有两种后果，方向相反但都很糟 ——
// 判「能用」而实际不能，用户会看到一个永远空白的实时面板；
// 判「不能用」而实际能用，用户白丢一个功能。所以每条都要写清**为什么**。
func TestCompanionSupportSpec(t *testing.T) {
	cases := []struct {
		name   string
		inst   Instance
		want   bool
		hybrid bool
		wantIn string // Reason 里必须出现的关键字
	}{
		{"paper 1.20.1 支持", Instance{Type: TypePaper, MCVersion: "1.20.1"}, true, false, ""},
		{"paper 1.21.8 支持", Instance{Type: TypePaper, MCVersion: "1.21.8"}, true, false, ""},
		{"原版没有插件系统", Instance{Type: TypeVanilla, MCVersion: "1.20.1"}, false, false, "插件系统"},
		{"forge 是模组端", Instance{Type: TypeForge, MCVersion: "1.20.1"}, false, true, "Bukkit"},
		{"fabric 是模组端", Instance{Type: TypeFabric, MCVersion: "1.20.1"}, false, true, "Bukkit"},
		{"代理端挂着别的服务器", Instance{Type: TypeVelocity, MCVersion: "1.20.1"}, false, false, "代理端"},
		// ⚠ 上面那条给代理端填了 MC 版本 —— 现实里代理端**没有**这个字段。
		// 真实导入 Velocity 之后 mcVersion 是空的，旧代码会因为 JavaForVersion("")==8
		// 把它判成「需要 Java 8」，连端名都印不出来（"而  需要 Java 8"）。
		// 下面三条就是照着真实的空版本写的。
		{"Velocity 没有 MC 版本", Instance{Type: TypeVelocity, MCVersion: ""}, false, false, "代理端"},
		{"BungeeCord 没有 MC 版本", Instance{Type: TypeBungee, MCVersion: ""}, false, false, "代理端"},
		{"原版没有 MC 版本", Instance{Type: TypeVanilla, MCVersion: ""}, false, false, "插件系统"},
		{"java8 装不了 java17 的插件", Instance{Type: TypePaper, MCVersion: "1.16.5"}, false, false, "Java 17"},
		// 版本未知的 Paper 不下结论：猜一个 Java 8 出来比不说更坏，
		// 让它去启动，服务端自己的 UnsupportedClassVersionError 才是真话。
		{"Paper 版本未知时先放行", Instance{Type: TypePaper, MCVersion: ""}, true, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := CompanionSupportOf(c.inst)
			if got.Supported != c.want {
				t.Fatalf("Supported = %v，想要 %v（原因：%s）", got.Supported, c.want, got.Reason)
			}
			if got.Hybrid != c.hybrid {
				t.Fatalf("Hybrid = %v，想要 %v", got.Hybrid, c.hybrid)
			}
			// 不支持就必须给原因。「不支持」三个字本身对用户没有任何用处。
			if !c.want && strings.TrimSpace(got.Reason) == "" {
				t.Fatal("不支持却没有给原因")
			}
			if c.wantIn != "" && !strings.Contains(got.Reason, c.wantIn) {
				t.Fatalf("原因里应该提到「%s」，实际是：%s", c.wantIn, got.Reason)
			}
		})
	}
}

// TestProxyReasonNeverBlamesJava 代理端的原因里不许出现 Java 那套说辞，也不许有空洞。
//
// 这条是实测逼出来的：把一个真的 Velocity 3.4.0 导进面板，界面上显示的是
//
//	这个插件要求 Java 17 以上，而  需要 Java 8。往 Java 8 的服务端里塞一个 Java 17 的 jar…
//
// ——「而」和「需要」之间是空的（%s 里没有东西可填），原因也是错的。
// 真实的拒绝原因服务端自己打着呢：「appears to be a Bukkit or BungeeCord plugin」。
//
// 判据用「不许出现」而不是「必须出现」：这类文案的错法就是**多说了不该说的**，
// 只查关键字存在的话，把错的句子附加在后面照样能过。
func TestProxyReasonNeverBlamesJava(t *testing.T) {
	for _, typ := range []Type{TypeVelocity, TypeBungee} {
		for _, mc := range []string{"", "1.20.1", "1.8.9"} {
			got := CompanionSupportOf(Instance{Type: typ, MCVersion: mc})
			if got.Supported {
				t.Fatalf("%v/%s：代理端被当成能用了", typ, mc)
			}
			// ⚠ 判据是「不许提 Java **版本要求**」，不是「不许出现 Java 这个词」：
			// BungeeCord 的实测原话里就有 JavaPlugin 这个类名（那是证据，不是结论）。
			for _, claim := range []string{"需要 Java", "要求 Java", "Java 17 以上"} {
				if strings.Contains(got.Reason, claim) {
					t.Errorf("%v/%s：原因里不该出现 Java 版本要求「%s」（那是对 Bukkit 端才成立的规矩）：%s", typ, mc, claim, got.Reason)
				}
			}
			if !strings.Contains(got.Reason, "代理端") {
				t.Errorf("%v/%s：原因里要说明这是代理端：%s", typ, mc, got.Reason)
			}
			// 空洞检测：文案里出现两个连续空格，八成就是某个 %s 没填上。
			if strings.Contains(got.Reason, "  ") {
				t.Errorf("%v/%s：原因里有连续空格 —— 十有八九是一个没填上的名字：%q", typ, mc, got.Reason)
			}
			// 引用的实测原话必须是这端自己的。BungeeCord 的拒绝方式和 Velocity 完全不同
			// （它是半加载后死在缺 Bukkit 类上），给它贴 Velocity 的报错就是在编。
			if typ == TypeBungee && strings.Contains(got.Reason, "Velocity") {
				t.Errorf("BungeeCord 的原因里引用了 Velocity 的报错 —— 那不是这台服务器说的话：%s", got.Reason)
			}
		}
	}
}

// TestCompanionSnapshotNeverSerializesNullPlayers /live 的每一条早退路径都要发出 []，
// 不许发 null。
//
// 这条是实测出来的：把一个 Velocity 导进面板，/live 返回的 JSON 里躺着
// `"online": null, "offline": null` —— 因为那几条早退路径根本不碰这两个字段，
// 而 Go 的 nil slice 序列化成 null。前端这次是在读的地方写了 `?? []` 才没崩，
// 但「每个读的地方都记得兜一下」正是这个项目已经吃过亏的老坑。
//
// 判据放在**序列化之后**：Go 里的空 slice 和 nil slice 看起来一样，
// 只有过一遍 JSON 才看得出区别。
func TestCompanionSnapshotNeverSerializesNullPlayers(t *testing.T) {
	m := newTestManager(t)
	dir := m.BaseDir()

	// 三条早退路径各来一发：不支持的端 / 插件没装 / 用户关掉了
	cases := []struct {
		name string
		inst Instance
	}{
		{"不支持的端（代理端）", Instance{
			ID: "p1", Type: TypeVelocity, MCVersion: "", Dir: filepath.Join(dir, "s1"),
		}},
		{"插件还没装", Instance{
			ID: "p2", Type: TypePaper, MCVersion: "1.20.1", Dir: filepath.Join(dir, "s2"),
		}},
	}
	off := false
	cases = append(cases, struct {
		name string
		inst Instance
	}{"用户把实时数据关掉了", Instance{
		ID: "p3", Type: TypePaper, MCVersion: "1.20.1", Dir: filepath.Join(dir, "s3"), Companion: &off,
	}})

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := os.MkdirAll(c.inst.Dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := m.st.Put(c.inst.ID, c.inst); err != nil {
				t.Fatal(err)
			}
			snap, err := m.CompanionSnapshot(c.inst.ID)
			if err != nil {
				t.Fatal(err)
			}
			if snap.Have {
				t.Fatal("这条路径不该有数据")
			}
			if strings.TrimSpace(snap.Note) == "" {
				t.Error("没数据却不给原因 —— 用户只会以为面板坏了")
			}
			b, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]json.RawMessage
			if err := json.Unmarshal(b, &doc); err != nil {
				t.Fatal(err)
			}
			for _, k := range []string{"online", "offline"} {
				got := string(doc[k])
				if got == "null" {
					t.Errorf("%s 是 null，应该是 []（前端就得在每个地方写 ?? []）", k)
				}
			}
		})
	}
}

// TestCompanionDefaultOn 没设置过时要按「开」算。
// 用 *bool 就是为了区分「从没设置」和「明确关掉」——
// 用 bool 的话新建的实例会默认把功能关掉，用户根本不知道有这回事。
func TestCompanionDefaultOn(t *testing.T) {
	var unset Instance
	if !unset.CompanionWanted() {
		t.Fatal("从没设置过应该默认开")
	}
	off := false
	explicit := Instance{Companion: &off}
	if explicit.CompanionWanted() {
		t.Fatal("明确关掉之后不该还按开算")
	}
}

// TestSyncCompanionInstallsAndRemoves 开关一动，jar 就得真的出现/消失。
func TestSyncCompanionInstallsAndRemoves(t *testing.T) {
	m := newTestManager(t)
	dir := m.BaseDir()
	inst := Instance{ID: "x", Type: TypePaper, MCVersion: "1.20.1", Dir: filepath.Join(dir, "srv")}
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 开 → 放进 plugins/
	if err := m.SyncCompanion(inst); err != nil {
		t.Fatalf("SyncCompanion(开): %v", err)
	}
	dst := filepath.Join(inst.Dir, "plugins", companion.FileName)
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("插件没被放进去: %v", err)
	}
	if len(got) != len(companion.Jar) {
		t.Fatalf("放进去的内容长度 %d，应该是 %d", len(got), len(companion.Jar))
	}
	// 幂等：再同步一次不该改文件时间
	st1, _ := os.Stat(dst)
	time.Sleep(1100 * time.Millisecond)
	if err := m.SyncCompanion(inst); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatal("重复同步不该重写文件（会篡改「什么时候装的」）")
	}

	// 关 → 拿掉
	off := false
	inst.Companion = &off
	if err := m.SyncCompanion(inst); err != nil {
		t.Fatalf("SyncCompanion(关): %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("关掉之后 jar 应该被移除")
	}
}

// TestSyncCompanionSkipsUnsupported 不支持的端不该被塞 jar。
// 往 Fabric 端里放一个 Bukkit 插件，它加载时会打一行错，
// 用户会以为是面板把服务器弄坏了。
func TestSyncCompanionSkipsUnsupported(t *testing.T) {
	m := newTestManager(t)
	dir := m.BaseDir()
	inst := Instance{ID: "y", Type: TypeFabric, MCVersion: "1.20.1", Dir: filepath.Join(dir, "srv")}
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.SyncCompanion(inst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "plugins", companion.FileName)); !os.IsNotExist(err) {
		t.Fatal("Fabric 端不该被塞 Bukkit 插件")
	}
}

// TestCompanionCommandRoundTrip 命令下发 → 插件回结果 → 面板拿到。
//
// 这里用一个「假插件」（一个 goroutine 扫 inbox 写 outbox）替掉真的服务端。
// 真的那条链路已经在 Paper 1.20.1 上手工验过了，这里守的是**面板这一半**：
// 文件名格式、原子落盘、等待与解析。
func TestCompanionCommandRoundTrip(t *testing.T) {
	m := newTestManager(t)
	dir := m.BaseDir()
	inst := Instance{ID: "z", Type: TypePaper, MCVersion: "1.20.1", Dir: filepath.Join(dir, "srv")}
	bridge := filepath.Join(append([]string{inst.Dir}, companion.BridgeRel...)...)
	inbox := filepath.Join(bridge, "inbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bridge, "outbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.st.Put(inst.ID, inst); err != nil {
		t.Fatal(err)
	}

	// 假插件
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			entries, _ := os.ReadDir(inbox)
			for _, e := range entries {
				if !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				b, err := os.ReadFile(filepath.Join(inbox, e.Name()))
				if err != nil {
					continue
				}
				var cmd map[string]any
				if err := json.Unmarshal(b, &cmd); err != nil {
					continue
				}
				id, _ := cmd["id"].(string)
				if id == "" {
					continue
				}
				os.Remove(filepath.Join(inbox, e.Name()))
				out, _ := json.Marshal(map[string]any{
					"id": id, "op": cmd["op"], "ok": true,
					"message": "已给 TestBot 3 个 diamond", "doneAt": time.Now().UnixMilli(),
				})
				os.WriteFile(filepath.Join(bridge, "outbox", "res-"+id+".json"), out, 0o644)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	defer close(done)

	res, err := m.CompanionCommand(inst.ID, "give", "TestBot", map[string]any{"item": "minecraft:diamond", "count": 3})
	if err != nil {
		t.Fatalf("CompanionCommand: %v", err)
	}
	if !res.OK {
		t.Fatalf("应该成功，实际：%s", res.Error)
	}
	if !strings.Contains(res.Message, "diamond") {
		t.Fatalf("结果里应该带上插件说的话，实际：%q", res.Message)
	}
	// 命令文件必须被消费掉，不然下次启动会重复执行
	if left, _ := os.ReadDir(inbox); len(left) != 0 {
		t.Fatalf("inbox 里还剩 %d 个文件", len(left))
	}
}

// TestCompanionCommandNoBridge 插件还没工作过 → 要说清，别干等 10 秒然后报超时。
func TestCompanionCommandNoBridge(t *testing.T) {
	m := newTestManager(t)
	dir := m.BaseDir()
	inst := Instance{ID: "nb", Type: TypePaper, MCVersion: "1.20.1", Dir: filepath.Join(dir, "srv")}
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := m.st.Put(inst.ID, inst); err != nil {
		t.Fatal(err)
	}
	_, err := m.CompanionCommand(inst.ID, "give", "A", nil)
	if !errors.Is(err, ErrCompanionNoBridge) {
		t.Fatalf("应该报 ErrCompanionNoBridge，实际：%v", err)
	}
}

// TestCompanionSnapshotEnrichesItems 快照里的机器名要翻成人话。
// 物品自带名字优先级最高：一把被改名成「老婆」的剑不该显示成「钻石剑」。
func TestCompanionSnapshotEnrichesItems(t *testing.T) {
	it := CompanionItem{ID: "minecraft:diamond_sword", Count: 1}
	enrichItem(&it)
	if it.Label == "" || it.Label == "minecraft:diamond_sword" {
		t.Fatalf("应该翻成中文名，实际：%q", it.Label)
	}
	named := CompanionItem{ID: "minecraft:diamond_sword", Count: 1, Name: "§6老婆"}
	enrichItem(&named)
	if named.Name != "老婆" {
		t.Fatalf("自定义名应该去掉颜色码，实际：%q", named.Name)
	}
	if named.Label != "老婆" {
		t.Fatalf("自定义名应该压过中文名，实际：%q", named.Label)
	}
}

// TestCompanionOpWhitelist 白名单之外的操作不许发出去。
func TestCompanionOpWhitelist(t *testing.T) {
	for _, ok := range []string{"give", "clear", "tp", "heal", "feed", "gamemode", "kill"} {
		if !CompanionOpOK(ok) {
			t.Fatalf("%s 应该在白名单里", ok)
		}
	}
	for _, bad := range []string{"", "rm -rf /", "op", "ban", "stop", "deop"} {
		if CompanionOpOK(bad) {
			t.Fatalf("%q 不该被放行", bad)
		}
	}
	// 大小写不敏感是有意的（用户手填 GIVE 不该被拒），
	// 但发出去之前必须归一化成小写 —— 插件那边是精确匹配。
	if !CompanionOpOK("GIVE") {
		t.Fatal("GIVE 应该被放行（大小写不敏感）")
	}
}

// TestCompanionZeroValuesSurviveJSON 零值字段不许在序列化时消失。
//
// 这一条是踩出来的：slot=0（背包第一格）、ping=0（本机）、effects=[]（没效果）
// 都因为 `omitempty` 被当成「没有这个字段」而整个消失。
// 前端拿到的是 undefined 而不是 0，渲染出来就是错位、空白、或者崩在 .map 上。
// 修一次记一次：现在凡是「0 或空数组是有效值」的字段，一律不许带 omitempty。
func TestCompanionZeroValuesSurviveJSON(t *testing.T) {
	p := enrich(CompanionPlayer{
		UUID:   "u1",
		Name:   "A",
		Online: true,
		Health: 20,
		Ping:   0,
	})
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	// slot 是物品层的字段，在下面的物品断言里单独查
	for _, k := range []string{"ping", "effects", "inventory", "enderChest"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("字段 %s 在 JSON 里不见了（0 和空数组都是有效值，不该被 omitempty 吞掉）", k)
		}
	}
	// 物品那一层同样要查：slot 0 是背包第一格
	it := CompanionItem{ID: "minecraft:diamond", Count: 1, Slot: 0}
	ib, _ := json.Marshal(it)
	var idoc map[string]json.RawMessage
	json.Unmarshal(ib, &idoc)
	if _, ok := idoc["slot"]; !ok {
		t.Error("物品的 slot=0 消失了 —— 背包第一格会渲染错位")
	}

	// snapshot 顶层的布尔同样是这个道理：Stale=false 是「我刚读过，是新的」，
	// 不是「后端没提这回事」。少一个字段，前端就只剩 undefined 可以兜。
	sb, _ := json.Marshal(CompanionSnapshot{Have: true})
	var sdoc map[string]json.RawMessage
	json.Unmarshal(sb, &sdoc)
	if _, ok := sdoc["stale"]; !ok {
		t.Error("快照的 stale=false 消失了 —— 前端分不清「确认是新的」和「没告诉我」")
	}
}

// TestCompanionSupportFolia 是实测出来的规格，不是推理。
//
// Folia 1.20.1 build 17 上把插件丢进 plugins/ 的原话：
//
//	Could not load plugin 'EnderBoxCompanion v0.2.0' as it is not marked as supporting Folia!
//
// 它属于「长得像 Paper，其实不是」这一类 —— 光看 Type 判不出来，
// 所以判据是启动文件的名字。这条测试在旧代码上会红：那时 TypePaper 一律算支持。
func TestCompanionSupportFolia(t *testing.T) {
	got := CompanionSupportOf(Instance{
		Type: TypePaper, MCVersion: "1.20.1",
		JarPath: "folia-1.20.1-17.jar",
	})
	if got.Supported {
		t.Fatal("Folia 被判成支持了 —— 塞进去只会被它在加载阶段拒绝")
	}
	if !got.Folia {
		t.Error("没有标出 Folia —— 前端就没法把它和普通的「不支持」区分开")
	}
	if !strings.Contains(got.Reason, "Folia") {
		t.Errorf("原因里必须点名 Folia，实际：%s", got.Reason)
	}
}

// TestCompanionSupportBukkitHybrids 混合端：本体是模组端，另外挂了一套 Bukkit 实现。
// 插件能装（照注入），但必须把「没人替你查」这句话说给用户。
//
// ⚠ 表里每对 (jar, typ) 都在考不同的路，别去重：
//   - 类型就是混合端（Mohist / Arclight）→ 走 isHybridType
//   - 类型还是 forge / fabric（老实例、用户自己丢进去的）→ 只能靠启动文件名认，走 isBukkitHybrid
//
// 两条路都得单独覆盖。只留一条的话，另一条哪天断了没人知道。
func TestCompanionSupportBukkitHybrids(t *testing.T) {
	cases := []struct {
		jar string
		typ Type
	}{
		{"mohist-1.20.1-824-server.jar", TypeMohist},
		{"mohist-1.20.1-824-server.jar", TypeForge}, // 老实例：类型还是 forge
		{"arclight-forge-1.20.1-1.0.1.jar", TypeArclight},
		{"arclight-forge-1.20.1-1.0.1.jar", TypeForge}, // 同上
		{"catserver-server-1.20.1.jar", TypeForge},
		{"cardboard-1.20.1.jar", TypeFabric},
	}
	for _, c := range cases {
		t.Run(c.jar+"/"+string(c.typ), func(t *testing.T) {
			got := CompanionSupportOf(Instance{Type: c.typ, MCVersion: "1.20.1", JarPath: c.jar})
			if !got.Supported {
				t.Fatalf("混合端被当成不能用了：%s", got.Reason)
			}
			if !got.Hybrid {
				t.Error("混合端必须标 Hybrid —— 不标的话界面上就没有风险提示")
			}
			if !strings.Contains(got.Reason, "混合端") {
				t.Errorf("原因里要说明这是混合端，实际：%s", got.Reason)
			}
		})
	}

	// 面板**新建**出来的混合端：这一刻 jarPath 还只是占位的 server.jar
	//（真 jar 要等下载完才有），名字里什么都认不出来。
	// 不看类型的话，一台刚建好的 Mohist 会被判成「原版服务端，没有插件系统」。
	for _, typ := range []Type{TypeMohist, TypeArclight} {
		fresh := CompanionSupportOf(Instance{Type: typ, MCVersion: "1.20.1", JarPath: "server.jar"})
		if !fresh.Supported || !fresh.Hybrid {
			t.Errorf("新建的 %s（jarPath 还是 server.jar）被判成 supported=%v hybrid=%v —— "+
				"它在界面上会被说成「原版服务端没有插件系统」", typ, fresh.Supported, fresh.Hybrid)
		}
	}

	// 反例：用户自己搭的普通 Forge 服（jar 名里没有混合端标记）不该被当成混合端。
	// 他多半没有 Bukkit，说「插件能装」就是骗人。
	plain := CompanionSupportOf(Instance{
		Type: TypeForge, MCVersion: "1.20.1",
		JarPath: "forge-1.20.1-47.2.0-shim.jar",
	})
	if plain.Supported || !plain.Hybrid {
		t.Fatalf("普通 Forge 服判错了：supported=%v hybrid=%v", plain.Supported, plain.Hybrid)
	}
}

// TestBukkitBridgeModMakesModdedServerHybrid Cardboard 这类混合端**不是**一个能直接
// 启动的 jar —— 启动文件是 fabric-server.jar，Cardboard 躺在 mods/ 里。
// 只看启动文件名的话，一台已经装好桥接、插件正在正常写数据的服务器会被判成「不支持」，
// 界面上就会「不支持实时数据」和「1 人在线」同时挂着。实测就是这套组合（Fabric 1.20.1
// + Cardboard + Fabric API + iCommon，插件加载正常），所以按真目录形状写测试。
func TestBukkitBridgeModMakesModdedServerHybrid(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mods"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{
		"Cardboard-1.20.jar",
		"fabric-api-0.92.12+1.20.1.jar",
		"iCommon-Fabric-bundle.jar",
	} {
		if err := os.WriteFile(filepath.Join(dir, "mods", f), []byte("jar"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got := CompanionSupportOf(Instance{
		Type: TypeFabric, MCVersion: "1.20.1", Dir: dir,
		JarPath: "fabric-server.jar",
	})
	if !got.Supported {
		t.Fatalf("装了 Cardboard 的 Fabric 服被当成用不了：%s", got.Reason)
	}
	if !got.Hybrid {
		t.Error("装了桥接模组就是混合端，必须标 Hybrid —— 不标界面上就没有风险提示")
	}
}

// 反例：Fabric 服但 mods/ 里全是普通模组（没有桥接），就该老实说「用不了」。
// 这条同时也是「读不到 mods/ 不误判」的护栏 —— 没有 mods/ 目录时行为要和以前一样。
func TestModdedServerWithoutBridgeStaysUnsupported(t *testing.T) {
	for _, mods := range [][]string{
		nil,                  // 连 mods/ 目录都没有
		{"sodium-0.5.8.jar"}, // 有 mods/，但里面没有桥接
		{"lithium-fabric.jar"},
	} {
		dir := t.TempDir()
		if mods != nil {
			if err := os.MkdirAll(filepath.Join(dir, "mods"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, f := range mods {
				if err := os.WriteFile(filepath.Join(dir, "mods", f), []byte("jar"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		got := CompanionSupportOf(Instance{
			Type: TypeFabric, MCVersion: "1.20.1", Dir: dir,
			JarPath: "fabric-server.jar",
		})
		if got.Supported {
			t.Errorf("mods=%v：没有桥接模组却说能用", mods)
		}
		if !got.Hybrid {
			t.Errorf("mods=%v：模组端仍要标 Hybrid（装上桥接就能用，得留话口）", mods)
		}
		if !strings.Contains(got.Reason, "不认") {
			t.Errorf("mods=%v：原因要说清模组端不认 plugins/，实际：%s", mods, got.Reason)
		}
	}
}

// TestBukkitFamilyClassifiedAsPaper Leaves / Folia 这些 fork 误判成「原版」的话，
// 面板会说「原版没有插件系统」—— 对一个明明有插件系统的端说这句话很难看。
func TestBukkitFamilyClassifiedAsPaper(t *testing.T) {
	for _, n := range []string{
		"paper-1.20.1-196.jar", "purpur-1.20.1-2062.jar", "spigot-1.20.1.jar",
		"craftbukkit-1.20.1.jar", "folia-1.20.1-17.jar", "leaves-1.20.1.jar",
		"Leaves-1.21.4.jar",
	} {
		if typ, rank := classifyJar(n); typ != TypePaper || rank != 1 {
			t.Errorf("%s → (%v, %d)，想要 (paper, 1)", n, typ, rank)
		}
	}
	// 混合端要认出来，否则掉进 default 变成「原版服务端」。
	// 能一键新建的两个给独立类型（理由见 classifyJar 的注释）。
	for _, c := range []struct {
		n   string
		typ Type
	}{
		{"mohist-1.20.1-824-server.jar", TypeMohist},
		{"Arclight-forge-1.20.1-1.0.1.jar", TypeArclight},
		{"cardboard-1.20.1.jar", TypeFabric},
	} {
		if typ, _ := classifyJar(c.n); typ != c.typ {
			t.Errorf("%s → %v，想要 %v", c.n, typ, c.typ)
		}
	}
}

// TestPluginTroubleReadsServerLog 「没有数据」这四个字对用户没有任何用。
// 服务端早就把真正的原因打在自己的日志里了，捞出来贴到界面上。
func TestPluginTroubleReadsServerLog(t *testing.T) {
	m := newTestManager(t)
	// 复刻 Folia 实际打出来的样子（带时间戳前缀和颜色）
	// 用反引号原样保留反斜杠：这行就是要复刻 Windows 上真实打出来的路径
	m.Logs("i1").Append(`[02:23:01 ERROR]: java.lang.RuntimeException: Directory 'plugins\EnderBoxCompanion.jar' failed to load!`)
	m.Logs("i1").Append("[02:23:01 ERROR]: Caused by: java.lang.RuntimeException: Could not load plugin 'EnderBoxCompanion v0.2.0' as it is not marked as supporting Folia!")
	m.Logs("i1").Append("[02:23:04 INFO]: Done (1.547s)! For help, type \"help\"")

	got := m.pluginTrouble("i1")
	if !strings.Contains(got, "not marked as supporting Folia") {
		t.Fatalf("没捞到那句原话，实际：%q", got)
	}
	// 前缀要剥掉：界面上这行是「服务端是这么说的：…」，前面再挂个时间戳就是噪音
	if strings.HasPrefix(got, "[02:23:01") {
		t.Errorf("时间戳/线程前缀没剥掉：%q", got)
	}
	// 一切正常时不许瞎报
	m.Logs("i2").Append("[02:23:18] [Server thread/INFO]: [EnderBoxCompanion] Enabling EnderBoxCompanion v0.2.0")
	m.Logs("i2").Append("[02:23:18] [Server thread/INFO]: Done (14.823s)! For help, type \"help\"")
	if got := m.pluginTrouble("i2"); got != "" {
		t.Errorf("正常启动却报了个故障出来：%q", got)
	}

	// 日志里没提过这个插件 → 一个字都不许说。
	// 服务端日志里全是红字，随便挑一条贴上去比不贴更坏。
	m.Logs("i3").Append("[02:23:18] [Server thread/ERROR]: 某个别的插件加载失败了")
	if got := m.pluginTrouble("i3"); got != "" {
		t.Errorf("日志里没提过我们的插件，却报了一条出来：%q", got)
	}

	// 旧伤不许当新病：用户自己试过坏 jar、失败过一次，后来又成功了 ——
	// 那次失败永远留在日志里，但拿它来吓人就是撒谎。判据是「最近一次」。
	m.Logs("i4").Append("[02:30:00] [Server thread/ERROR]: Directory 'plugins\\EnderBoxCompanion.jar' failed to load!")
	m.Logs("i4").Append("[02:35:00] [Server thread/INFO]: [EnderBoxCompanion] EnderBoxCompanion 0.2.0 已启用（MC 1.20.1-R0.1-SNAPSHOT）")
	if got := m.pluginTrouble("i4"); got != "" {
		t.Errorf("最近一次是成功启用的，不该把更早的报错翻出来：%q", got)
	}
	// 反过来：成功过之后又失败了，报新的那次
	m.Logs("i5").Append("[02:30:00] [Server thread/INFO]: [EnderBoxCompanion] Enabling EnderBoxCompanion v0.2.0")
	m.Logs("i5").Append("[02:40:00] [Server thread/ERROR]: Could not load plugin 'EnderBoxCompanion v0.2.0' as it is not marked as supporting Folia!")
	if got := m.pluginTrouble("i5"); !strings.Contains(got, "not marked as supporting Folia") {
		t.Errorf("最近一次的失败没报出来：%q", got)
	}
}
