package daemon

import (
	"archive/zip"
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─────────────────────────────────────────── 解析（最要命的一段，先测）

// 判据：一行里「名字 + 行尾值」两条同时成立才认。英文/中文/乱码三端都要过，
// 面板回显、词边界误配、名字在值之后这三种都要挡掉。
func TestParseGameruleReply(t *testing.T) {
	known := []string{"keepInventory", "doDaylightCycle", "randomTickSpeed", "maxEntityCramming"}

	cases := []struct {
		name  string
		line  string
		want  string // 命中时的规则名，"" = 不认
		value string
	}{
		{
			name: "英文单查（vanilla 原话）",
			line: "Gamerule keepInventory is currently set to: true",
			want: "keepInventory", value: "true",
		},
		{
			name: "英文设置确认（is now set to）",
			line: "Gamerule keepInventory is now set to: false",
			want: "keepInventory", value: "false",
		},
		{
			name:  "中文单查（Mohist 原话，全角冒号无空格）",
			line:  "游戏规则keepInventory目前为：false",
			want:  "keepInventory",
			value: "false",
		},
		{
			name:  "中文设置确认（已被设为）",
			line:  "游戏规则keepInventory已被设为：true",
			want:  "keepInventory",
			value: "true",
		},
		{
			name: "整数规则",
			line: "Gamerule randomTickSpeed is currently set to: 3",
			want: "randomTickSpeed", value: "3",
		},
		{
			name: "负数（理论上没有，解析器不该漏）",
			line: "Gamerule maxEntityCramming is currently set to: -1",
			want: "maxEntityCramming", value: "-1",
		},
		{
			name: "行尾带 \\r（CRLF 输出）",
			line: "Gamerule keepInventory is currently set to: true\r",
			want: "keepInventory", value: "true",
		},
		{
			// GBK 字节按 UTF-8 解坏后，中文变乱码字节，但 ASCII 的名字和值原样。
			// 这正是「解析不靠本地化文案」要证明的场景。
			name:  "乱码裹挟（GBK 字节拼 UTF-8）",
			line:  string([]byte{0xbf, 0xda, 0xcd, 0xb7}) + "keepInventory" + string([]byte{0xc4, 0xbf, 0xc7, 0xb0, 0xce, 0xaa, 0xa3, 0xba}) + "true",
			want:  "keepInventory",
			value: "true",
		},
		{
			name: "面板回显必须挡掉（行尾同样有名字和值）",
			line: "[EnderBox] > gamerule keepInventory true",
		},
		{
			name: "词边界：更长的名字不能误配",
			line: "Gamerule doDaylightCycleX is currently set to: true",
		},
		{
			name: "词边界：前后缀名字都不能误配",
			line: "Gamerule mykeepInventory is currently set to: true",
		},
		{
			name: "行尾不是值（规则被删/语法错误）",
			line: "Gamerule keepInventory is currently set to: ???",
		},
		{
			name: "不认识的规则名",
			line: "Gamerule notARule is currently set to: true",
		},
		{
			name: "名字出现在值之后（不是规则=值 的行）",
			line: "Gamerule something is currently set to: keepInventory true",
		},
		{
			// 玩家聊天里提到规则名 —— 中间只有空格，不能当成规则值读走。
			name: "聊天行冒名（玩家说了规则名+值）",
			line: "<Steve> keepInventory true",
		},
		{
			name: "聊天行冒名（带尖括号的中文名）",
			line: "<小明> doDaylightCycle false",
		},
		{
			name: "命令报错行",
			line: "Unknown or incomplete command, see below for error",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotVal, ok := parseGameruleReply(c.line, known)
			if c.want == "" {
				if ok {
					t.Fatalf("这行不该被认下来，却解出了 %q=%q：%q", gotName, gotVal, c.line)
				}
				return
			}
			if !ok {
				t.Fatalf("这行应该认下来，却漏了：%q", c.line)
			}
			if gotName != c.want || gotVal != c.value {
				t.Fatalf("解出 %q=%q，想要 %q=%q", gotName, gotVal, c.want, c.value)
			}
		})
	}
}

// 设置确认行和单查行是同一个解析器 —— 值以服务端回报为准（个别规则会 clamp）。
func TestParseGameruleReplySetConfirm(t *testing.T) {
	// 服务端把 1000 clamp 回 100 这类情况：解析器只负责如实取回服务端说的值。
	line := "Gamerule playersSleepingPercentage is now set to: 100"
	n, v, ok := parseGameruleReply(line, []string{"playersSleepingPercentage"})
	if !ok || n != "playersSleepingPercentage" || v != "100" {
		t.Fatalf("解出 %q=%q ok=%v，想要 playersSleepingPercentage=100", n, v, ok)
	}
}

// ─────────────────────────────────────────── 语言文件抽取

func TestExtractGamerules(t *testing.T) {
	raw := map[string]string{
		"gamerule.keepInventory":                     "Keep Inventory",
		"gamerule.keepInventory.description":         "Whether to keep the inventory on death.",
		"gamerule.category.misc":                     "Miscellaneous", // 分组名，不是规则
		"gamerule.category.player":                   "Player",
		"gamerule.":                                  "脏键：空名字", // 必须被名字正则挡掉
		"gamerule.foo.bar":                           "脏键：带点",  // 同上
		"gamerule.Something-with-dash":               "脏键：带横线", // 同上
		"item.minecraft.stick":                       "Stick",  // 非 gamerule 键
		"gamerule.doDaylightCycle":                   "Do Daylight Cycle",
		"gamerule.doDaylightCycle.description.title": "白天与黑夜", // 非标准后缀，当独立名字（点不合法）→ 丢
	}

	cat := extractGamerules(raw)

	if len(cat) != 2 {
		t.Fatalf("应该只抽出 2 条规则，拿到 %d 条：%+v", len(cat), cat)
	}
	ki, ok := cat["keepInventory"]
	if !ok {
		t.Fatal("keepInventory 没抽到")
	}
	if ki.label != "Keep Inventory" {
		t.Errorf("label = %q，想要 %q", ki.label, "Keep Inventory")
	}
	if ki.note != "Whether to keep the inventory on death." {
		t.Errorf("note = %q，描述没抽对", ki.note)
	}
	if d := cat["doDaylightCycle"]; d.label != "Do Daylight Cycle" || d.note != "" {
		t.Errorf("doDaylightCycle = %+v，不对", d)
	}
}

// ─────────────────────────────────────────── jar 读取（含 bundler 内嵌一层）

// writeZip 造一个 zip：entries 是 名字 → 内容。
func writeZip(t *testing.T, path string, entries map[string][]byte) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func langJSON(rules map[string]string) []byte {
	parts := make([]string, 0, len(rules))
	for k, v := range rules {
		parts = append(parts, `"`+k+`":"`+v+`"`)
	}
	return []byte("{" + strings.Join(parts, ",") + "}")
}

// 普通布局（Paper 的 versions/*.jar、Forge 的 libraries/...）：jar 自带 assets。
func TestCatalogFromJarFlat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "server.jar")
	writeZip(t, p, map[string][]byte{
		"assets/minecraft/lang/en_us.json": langJSON(map[string]string{
			"gamerule.keepInventory": "Keep Inventory",
			"gamerule.spawnRadius":   "Respawn Radius",
		}),
		"META-INF/MANIFEST.MF": []byte("Manifest-Version: 1.0\n"),
	})

	cat, ok := catalogFromJar(p)
	if !ok {
		t.Fatal("应该能读出目录")
	}
	if len(cat) != 2 || cat["keepInventory"].label != "Keep Inventory" {
		t.Fatalf("目录不对：%+v", cat)
	}
}

// vanilla bundler 布局：真 jar 藏在 META-INF/versions/<ver>/server-<ver>.jar 里。
func TestCatalogFromJarNestedBundler(t *testing.T) {
	dir := t.TempDir()

	// 内层：真 jar
	inner := filepath.Join(dir, "inner.jar")
	writeZip(t, inner, map[string][]byte{
		"assets/minecraft/lang/en_us.json": langJSON(map[string]string{
			"gamerule.keepInventory": "Keep Inventory",
			"gamerule.doFireTick":    "Fire Spreads",
		}),
	})
	innerBytes, err := os.ReadFile(inner)
	if err != nil {
		t.Fatal(err)
	}

	// 外层：bundler，只有版本清单 + 内嵌 jar
	outer := filepath.Join(dir, "bundler.jar")
	writeZip(t, outer, map[string][]byte{
		"META-INF/versions/1.20.1/server-1.20.1.jar": innerBytes,
		"META-INF/libraries/foo/bar/1.0/bar-1.0.jar": []byte("不是 zip，扫到也不能崩"),
	})

	cat, ok := catalogFromJar(outer)
	if !ok {
		t.Fatal("bundler 内嵌一层也该读得到")
	}
	if len(cat) != 2 || cat["doFireTick"].label != "Fire Spreads" {
		t.Fatalf("目录不对：%+v", cat)
	}
}

// 不是 zip / 是 zip 但没有语言文件 → 老老实实报 false，退基线表。
func TestCatalogFromJarNegative(t *testing.T) {
	dir := t.TempDir()

	notZip := filepath.Join(dir, "a.jar")
	if err := os.WriteFile(notZip, []byte("MZ 假 exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := catalogFromJar(notZip); ok {
		t.Error("不是 zip 不该读出目录")
	}

	noLang := filepath.Join(dir, "b.jar")
	writeZip(t, noLang, map[string][]byte{"META-INF/MANIFEST.MF": []byte("x")})
	if _, ok := catalogFromJar(noLang); ok {
		t.Error("没有语言文件不该读出目录")
	}

	if _, ok := catalogFromJar(filepath.Join(dir, "不存在.jar")); ok {
		t.Error("文件不存在不该读出目录")
	}
}

// scanGameruleCatalog：在实例目录里按约定布局找 jar。
func TestScanGameruleCatalogFindsVersionsJar(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "versions", "1.20.1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeZip(t, filepath.Join(sub, "1.20.1.jar"), map[string][]byte{
		"assets/minecraft/lang/en_us.json": langJSON(map[string]string{
			"gamerule.keepInventory": "Keep Inventory",
		}),
	})

	cat, ok := scanGameruleCatalog(dir, "")
	if !ok || cat["keepInventory"].label != "Keep Inventory" {
		t.Fatalf("versions/*.jar 没扫到：ok=%v cat=%+v", ok, cat)
	}
}

// ─────────────────────────────────────────── 合并 / 行构造 / 排序

func TestMergeGameruleNames(t *testing.T) {
	cat := map[string]jarGamerule{
		"zzzNewRule":    {label: "New"},
		"keepInventory": {label: "Keep"},
	}
	names := mergeGameruleNames(cat)

	// 并集里必须包含目录项与基线项，且有序（命令顺序稳定、输出可复现）
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("zzzNewRule") || !has("keepInventory") || !has("randomTickSpeed") {
		t.Fatalf("并集不全：%v", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("没排序或有重复：%v", names)
		}
	}
}

func TestBuildGameruleRow(t *testing.T) {
	cat := map[string]jarGamerule{
		"keepInventory": {label: "Keep Inventory"},
		"brandNewRule":  {label: "Brand New Rule"},
	}

	// spec 命中：中文名 + 分组 + 说明
	r := buildGameruleRow("keepInventory", "true", cat)
	if r.Label != "死亡保留物品" || r.Group != "玩家" || r.Note == "" || r.Kind != "bool" {
		t.Fatalf("spec 命中时行不对：%+v", r)
	}

	// spec 没命中、jar 有：用 jar 英文名 + 杂项
	r = buildGameruleRow("brandNewRule", "false", cat)
	if r.Label != "Brand New Rule" || r.Group != gameruleGroupFallback || r.Kind != "bool" {
		t.Fatalf("jar 回退时行不对：%+v", r)
	}

	// 两边都没有：驼峰机器名 + 杂项 + int
	r = buildGameruleRow("mysteryRule", "7", nil)
	if r.Label != "mysteryRule" || r.Group != gameruleGroupFallback || r.Kind != "int" {
		t.Fatalf("裸回退时行不对：%+v", r)
	}
}

func TestSortGameruleRows(t *testing.T) {
	rows := []GameruleRow{
		{Name: "b", Group: "杂项"},
		{Name: "z", Group: "玩家"},
		{Name: "a", Group: "玩家"},
		{Name: "m", Group: "伤害"},
		{Name: "未分组", Group: "不存在的组"}, // 未知组落到末尾
	}
	sortGameruleRows(rows)

	want := []string{"a", "z", "m", "b", "未分组"}
	for i, w := range want {
		if rows[i].Name != w {
			t.Fatalf("第 %d 个是 %q，想要 %q；整体=%+v", i, rows[i].Name, w, rows)
		}
	}
	// 组顺序表里的顺序不能乱：玩家 < 伤害 < 杂项
	if gameruleGroupOrder[0] != "玩家" || gameruleGroupOrder[len(gameruleGroupOrder)-1] != "杂项" {
		t.Errorf("组顺序表被动了：%v", gameruleGroupOrder)
	}
}

// spec 表自身的一致性：分组名必须是组顺序表里的，不然规则会静默掉进「末尾」。
func TestGameruleSpecsGroupsAreKnown(t *testing.T) {
	known := map[string]bool{}
	for _, g := range gameruleGroupOrder {
		known[g] = true
	}
	for name, sp := range gameruleSpecs {
		if sp.group == "" {
			t.Errorf("%s: 没写分组", name)
			continue
		}
		if !known[sp.group] {
			t.Errorf("%s: 分组 %q 不在组顺序表里", name, sp.group)
		}
		if sp.label == "" {
			t.Errorf("%s: 没写中文名", name)
		}
		if !gameruleNameRE.MatchString(name) {
			t.Errorf("%s: 名字不符合校验正则（写进 SetGamerule 会被自己挡掉）", name)
		}
	}
	// 基线与 spec 的规则名格式也要合法
	for _, n := range gameruleBaseline {
		if !gameruleNameRE.MatchString(n) {
			t.Errorf("基线里 %q 格式不合法", n)
		}
	}
}

// ─────────────────────────────────────────── Manager 侧的错误路径与防注入

func TestGamerulesInstanceNotFound(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.Gamerules("不存在"); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("想要 ErrInstanceNotFound，拿到 %v", err)
	}
}

func TestGamerulesNoServer(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "停着的服", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Gamerules(inst.ID); !errors.Is(err, ErrGameruleNoServer) {
		t.Fatalf("想要 ErrGameruleNoServer，拿到 %v", err)
	}
	if _, err := m.SetGamerule(inst.ID, "keepInventory", "true"); !errors.Is(err, ErrGameruleNoServer) {
		t.Fatalf("写也该报 ErrGameruleNoServer，拿到 %v", err)
	}
}

// 防注入第一道闸：名字/值里塞换行、空格、分号一律拒。
// 命令走 stdin 行协议 —— 名字里带 \n 等于能再发一条命令。
func TestSetGameruleRejectsInjection(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "服", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}

	badNames := []string{
		"keepInventory\ngamerule doDaylightCycle true",
		"keepInventory true",
		"keepInventory;stop",
		"keepInventory&x",
		"1keepInventory",
		"",
		"keep inventory",
		"keepInventory\r\nstop",
	}
	for _, n := range badNames {
		if _, err := m.SetGamerule(inst.ID, n, "true"); err == nil ||
			!strings.Contains(err.Error(), "规则名不合法") {
			t.Errorf("名字 %q 应该被格式闸拒掉，拿到 %v", n, err)
		}
	}

	badValues := []string{
		"true\ngamerule doDaylightCycle false",
		"true;stop",
		"true ",
		"yes",
		"0x10",
		"1e3",
		"",
	}
	for _, v := range badValues {
		err := func() error { _, e := m.SetGamerule(inst.ID, "keepInventory", v); return e }()
		if err == nil {
			t.Errorf("值 %q 该被拒", v)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "值不合法") && !errors.Is(err, ErrGameruleNoServer) {
			// "true " 会被 TrimSpace 成合法值，然后因为服务器没跑报 NoServer —— 也算拦住了
			t.Errorf("值 %q 的报错不对：%v", v, err)
		}
	}
}

// 防注入第二道闸：格式合法但目录里没有的名字 → ErrGameruleNotFound（哪怕服务器在跑也不放行）。
func TestSetGameruleUnknownName(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "服", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// 全大写下划线看着合法，但不在目录/基线里
	if _, err := m.SetGamerule(inst.ID, "NotARule", "true"); !errors.Is(err, ErrGameruleNotFound) {
		t.Fatalf("想要 ErrGameruleNotFound，拿到 %v", err)
	}
	// 基线里有的名字要能过第二道闸（然后才因为没跑而 NoServer）
	if _, err := m.SetGamerule(inst.ID, "keepInventory", "true"); !errors.Is(err, ErrGameruleNoServer) {
		t.Fatalf("基线名字应该过闸，报 NoServer；拿到 %v", err)
	}
}

// ─────────────────────────────────────────── 全链路（假服务端接在 stdin 上）

// fakeRuleServer 把「假服务端」接在 Process 的 stdin 上：逐行读命令、按真实服务端
// 的口吻回话。没有 java 也能跑通「发命令 → 收行 → 解析 → 出表」这一整条链。
type fakeRuleServer struct {
	mu    sync.Mutex
	got   []string // 收到的原始命令行，用来断言「发出去的是什么」
	reply func(cmd string) []string
}

func (f *fakeRuleServer) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.got...)
}

func attachFakeServer(t *testing.T, p *Process, reply func(cmd string) []string) *fakeRuleServer {
	t.Helper()
	f := &fakeRuleServer{reply: reply}
	pr, pw := io.Pipe()
	p.stdin = pw
	go func() {
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			cmd := strings.TrimSpace(sc.Text())
			f.mu.Lock()
			f.got = append(f.got, cmd)
			f.mu.Unlock()
			for _, line := range f.reply(cmd) {
				p.logs.Append(line)
			}
		}
	}()
	t.Cleanup(func() { _ = pw.Close(); _ = pr.Close() })
	return f
}

// newRunningTestManager 造一个「目录里有 jar 目录、进程槽里挂着假服务端」的实例。
func newRunningTestManager(t *testing.T, reply func(cmd string) []string) (*Manager, *Instance, *fakeRuleServer) {
	t.Helper()
	m := newTestManager(t)
	inst := &Instance{Name: "跑着的服", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// 目录里的 jar 决定「问哪些名字」：三条，其中 randomTickSpeed 会证 int 识别
	jarDir := filepath.Join(inst.Dir, "versions", "1.20.1")
	if err := os.MkdirAll(jarDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeZip(t, filepath.Join(jarDir, "1.20.1.jar"), map[string][]byte{
		"assets/minecraft/lang/en_us.json": langJSON(map[string]string{
			"gamerule.keepInventory":   "Keep Inventory",
			"gamerule.doDaylightCycle": "Do Daylight Cycle",
			"gamerule.randomTickSpeed": "Random Tick Speed",
		}),
	})

	logs := NewLogBuffer(4000)
	p := newProcess(inst, logs)
	fake := attachFakeServer(t, p, reply)
	m.mu.Lock()
	m.procs[inst.ID] = p
	m.mu.Unlock()
	return m, inst, fake
}

// 读全链路：目录里的名字都问一遍 → 只有回得出值的那些成行；报错行不回值、不成行。
func TestGamerulesThroughFakeServer(t *testing.T) {
	known := map[string]string{
		"keepInventory":   "true",
		"doDaylightCycle": "false",
		"randomTickSpeed": "3",
	}
	m, inst, fake := newRunningTestManager(t, func(cmd string) []string {
		parts := strings.Fields(cmd)
		if len(parts) != 2 || parts[0] != "gamerule" {
			t.Errorf("面板发了一条不该发的命令：%q", cmd)
			return nil
		}
		if v, ok := known[parts[1]]; ok {
			return []string{"Gamerule " + parts[1] + " is currently set to: " + v}
		}
		// 服务端不认的名字照真实行为回错误行 —— 这行不含值，解析器必须丢掉
		return []string{"Incorrect argument for command 'gamerule'"}
	})

	rows, err := m.Gamerules(inst.ID)
	if err != nil {
		t.Fatalf("Gamerules: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("应该只出「回得出值」的 3 条，拿到 %d 条：%+v", len(rows), rows)
	}

	byName := map[string]GameruleRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if r := byName["keepInventory"]; r.Value != "true" || r.Kind != "bool" ||
		r.Label != "死亡保留物品" || r.Group != "玩家" {
		t.Errorf("keepInventory 行不对：%+v", r)
	}
	if r := byName["randomTickSpeed"]; r.Value != "3" || r.Kind != "int" || r.Group != "世界" {
		t.Errorf("randomTickSpeed 行不对：%+v", r)
	}
	// 排序：玩家组在前，然后世界组（组内按名字）
	if rows[0].Name != "keepInventory" || rows[1].Name != "doDaylightCycle" || rows[2].Name != "randomTickSpeed" {
		t.Errorf("排序不对：%v %v %v", rows[0].Name, rows[1].Name, rows[2].Name)
	}
	// 发出去的：每条都是两条词的查询命令，且目录里的三条都在
	cmds := fake.commands()
	sawKeep := false
	for _, c := range cmds {
		if len(strings.Fields(c)) != 2 {
			t.Errorf("查询命令不该带参：%q", c)
		}
		if c == "gamerule keepInventory" {
			sawKeep = true
		}
	}
	if !sawKeep {
		t.Error("没问 keepInventory")
	}
}

// 写全链路：值以服务端回报为准（会被夹到范围里），且面板自己的回显行不能被当确认。
func TestSetGameruleThroughFakeServer(t *testing.T) {
	m, inst, fake := newRunningTestManager(t, func(cmd string) []string {
		if cmd != "gamerule playersSleepingPercentage 1000" {
			return nil // 查询命令一律不理（这个用例只测写）
		}
		// 真实服务端会先把命令回显进日志（面板自己的回显也走同一路），
		// 再把这条改动的确认行放出来 —— 面板必须只认后者，且值取服务端说的 100（夹过了）。
		return []string{
			"Gamerule doDaylightCycle is now set to: false", // 噪音：别的规则（并发场景）
			"游戏规则playersSleepingPercentage已被设为：100",
		}
	})

	got, err := m.SetGamerule(inst.ID, "playersSleepingPercentage", "1000")
	if err != nil {
		t.Fatalf("SetGamerule: %v", err)
	}
	if got != "100" {
		t.Fatalf("应该回报服务端夹过的实际值 100，拿到 %q", got)
	}
	cmds := fake.commands()
	if len(cmds) != 1 || cmds[0] != "gamerule playersSleepingPercentage 1000" {
		t.Fatalf("发出去的命令不对：%v", cmds)
	}
}

// 服务端一声不吭（没就绪 / 命令被吞）→ 明确报「没读到」，不许静默返回空表。
func TestGamerulesServerSilent(t *testing.T) {
	m, inst, _ := newRunningTestManager(t, func(string) []string { return nil })
	_, err := m.Gamerules(inst.ID)
	if !errors.Is(err, ErrGameruleNoReply) {
		t.Fatalf("想要 ErrGameruleNoReply，拿到 %v", err)
	}
}

// 「集齐即返回」：目录里的规则全读到值就直接返回，不等安静期。
// 这条同时守住两件事：正常路径够快（不是每次都白等一个安静窗口），
// 以及「目录 = 该版本的完整规则集」这个前提真的成立（三条全回 = 收齐）。
func TestGamerulesEarlyExit(t *testing.T) {
	known := map[string]string{
		"keepInventory":   "true",
		"doDaylightCycle": "false",
		"randomTickSpeed": "3",
	}
	m, inst, _ := newRunningTestManager(t, func(cmd string) []string {
		parts := strings.Fields(cmd)
		if len(parts) == 2 && parts[0] == "gamerule" {
			if v, ok := known[parts[1]]; ok {
				return []string{"Gamerule " + parts[1] + " is currently set to: " + v}
			}
		}
		return nil
	})

	start := time.Now()
	rows, err := m.Gamerules(inst.ID)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Gamerules: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("要 3 条，拿到 %d 条", len(rows))
	}
	if elapsed >= gameruleQuiet {
		t.Fatalf("收齐了该立刻返回，却花了 %s（安静期 %s）", elapsed, gameruleQuiet)
	}
}
