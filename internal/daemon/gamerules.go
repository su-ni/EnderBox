package daemon

// 游戏规则（gamerule）编辑器 —— 后端。
//
// ▍为什么走「控制台命令」而不是读写 level.dat
//   level.dat 是 NBT 二进制，写它有回写覆盖风险（服务端运行中会周期性保存），
//   还得引入 NBT 读写器。`gamerule` 命令才是官方唯一稳定接口：任何服务端类型都认
//   （原版 / Paper / Forge / Fabric 一致），改完立刻生效，服务端自己负责存档。
//
// ▍规则「全集」从哪来（这是本文件最绕的一处，实测纪要）
//   Java 版 `/gamerule` **没有无参枚举**！1.20.1 实测无参 = "Unknown or incomplete
//   command"（wiki 的语法也一直是 `gamerule <rule> [value]`，rule 必填）。
//   名字先知道、再逐条单查，是唯一的通道。名字的两种来源：
//     ① **服务端 jar 里的 assets/minecraft/lang/en_us.json** —— 键 `gamerule.<name>`
//        就是该版本的完整规则集（还带显示名/描述）。跟着版本走，不硬编码。
//        实测覆盖：vanilla（bundler 内层 jar）/ Paper 系（versions 下的组装产物）/
//        Forge 系（libraries/net/minecraft/server 下的 jar）都能拿到。
//     ② 拿不到（Fabric 等布局）→ 退回内置基线表 gameruleBaseline。
//   然后**每条都单查一次** `gamerule <name>`：服务端不认的名字会报
//   "Incorrect argument"（中文端：错误的命令参数），我们**只显示查得到值的那些**。
//   所以表里多了、旧了都无害 —— 显示出来的一定真实存在。
//   这就是「不硬编码全集、解析到什么显示什么」在命令通道下的落地方式。
//
// ▍解析策略（对本地化和编码都稳，实测过英文/中文两端）
//   一行同时满足三条才认：
//     ① **含已知规则名**（词边界匹配）
//     ② **行尾可取到值**（true/false/整数）
//     ③ 名字与值之间隔着一段说明文字（不能只有空格）—— 见 parseGameruleReply 注释
//   - 英文（vanilla）: "Gamerule keepInventory is currently set to: true"
//   - 中文（Mohist）: "游戏规则keepInventory目前为：false"（全角冒号、无空格）
//   - 就算服务端输出乱码（GBK 字节按 UTF-8 解坏），规则名和值都是 ASCII，照样命中。
//   ⚠ 必须排除面板自己的回显行（"[EnderBox] > gamerule X true" 行尾也有名字和值）。
//
// ▍线程模型
//   收集走 LogBuffer 的订阅通道（每条命令的响应是独立行、按序到达）。
//   两次读操作并发时输出会互相混入 —— 解析按「自己发过的名字」过滤，互不干扰；
//   读与写并发时，写操作的 "is now set to" 行可能被读操作收到 —— 值是真的，无害。

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// 读操作的等待参数。三条一起看：
//   - FirstWait：发出命令后等「第一条输出」的上限。到点没动静就返回空
//     （服务端没在跑 / 命令被吞），比一直挂着强。
//     ⚠ 实测这条几乎用不上：面板自己的回显行（Send 时落日志）就满足「第一条输出」，
//     所以静默路径真正等的是下面的 Quiet。
//   - Quiet：收到输出后，多久没有新行就认为「说完了」。实测 46 条命令原版/Mohist
//     都是连着回完（响应与回显几乎同刻到达），正常路径根本等不到这个窗口 ——
//     有目录时靠「集齐即返回」（见 enough 回调）提前退出；这个窗口只在**读不全**
//     时兜底（服务端 tick 卡顿、并发噪音），所以给宽一点不心疼。
//   - Max：总上限，防止某个服务端慢慢吐字把接口挂死。
const (
	gameruleFirstWait = 1600 * time.Millisecond
	gameruleQuiet     = 800 * time.Millisecond
	gameruleMax       = 8 * time.Second
)

// ErrGameruleNoServer 读/写规则时服务器没在跑。
// 游戏规则是「运行的服务器」的运行时状态：没运行 = 没得读、没得改（改 level.dat 是另一条路，不做）。
var ErrGameruleNoServer = errors.New("服务器未运行 —— 游戏规则要在服务器启动后读写")

// ErrGameruleNotFound 写操作时给的规则名不在「已知目录」里。
var ErrGameruleNotFound = errors.New("不认识的游戏规则名")

// ErrGameruleNoReply 连一条规则都没读到 —— 服务器基本就是「还没就绪」。
// 单独一个错误值（而不是一句 fmt.Errorf）：界面要按它切「稍后重试」的态，
// 靠匹配文案分流太脆。
var ErrGameruleNoReply = errors.New("没有读到任何规则 —— 服务器可能还在启动（就绪前命令不执行），稍后重试")

// GameruleRow 读出来的一行。
type GameruleRow struct {
	Name  string `json:"name"`
	Label string `json:"label"`          // 中文名（spec 命中）；否则用 jar 的英文显示名；再否则就是名字本身
	Note  string `json:"note,omitempty"` // 中文说明（只从 spec 来；英文描述不进中文界面）
	Group string `json:"group"`          // 分组中文名（导航用）
	Value string `json:"value"`          // 当前值。bool 是 "true"/"false"，int 是十进制
	Kind  string `json:"kind"`           // "bool" | "int" —— 前端按它选控件
}

var (
	gameruleNameRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)
	gameruleValueRE = regexp.MustCompile(`^(true|false|-?\d{1,10})$`)
	// 行尾取值：英文 "…to: true" 与中文 "…目前为：false" 的共同点就是「值在行尾」。
	gameruleValueTailRE = regexp.MustCompile(`(true|false|-?\d+)\s*$`)
)

// ────────────────────────────────────────────── 命令通道

// Request 发一批命令并收集它们的输出（安静期判定）。
//
// 语义是「能收多少收多少」：服务端不认的命令不会回值，回显行由解析器过滤。
// 订阅必须建立在 Send 之前 —— 否则先发的命令响应可能落在订阅之前丢掉。
//
// enough 是「够了，别等了」回调，收一批行就判一次，可传 nil。
// 别小看它：安静期是**猜**服务端说完了，猜早了会截断读数、猜宽了拖慢界面；
// 而调用方往往知道「收全了长什么样」（比如规则目录的条数），给个确定判据就能
// 在收齐的瞬间返回 —— 正常路径从几百毫秒降到几十毫秒，安静期只留给收不全的情形。
func (p *Process) Request(cmds []string, quiet, max time.Duration, enough func([]string) bool) []string {
	subID, ch := p.logs.Subscribe()
	defer p.logs.Unsubscribe(subID)

	for _, c := range cmds {
		if err := p.Send(c); err != nil {
			return nil
		}
	}

	var out []string
	firstT := time.NewTimer(gameruleFirstWait)
	quietT := time.NewTimer(1 << 62) // 首条到来前不启用（这样定时器不会提前烧掉）
	maxT := time.NewTimer(max)
	defer firstT.Stop()
	defer quietT.Stop()
	defer maxT.Stop()

	for {
		select {
		case lines := <-ch:
			if len(out) == 0 {
				if !firstT.Stop() {
					<-firstT.C
				}
			}
			out = append(out, lines...)
			if enough != nil && enough(out) {
				return out
			}
			if !quietT.Stop() {
				<-quietT.C
			}
			quietT.Reset(quiet)
		case <-firstT.C:
			// 首条都没等到 —— 服务端没响应（没就绪 / 命令被吞）
			return out
		case <-quietT.C:
			return out
		case <-maxT.C:
			return out
		}
	}
}

// ────────────────────────────────────────────── 解析（纯函数，测试重点）

func gameruleWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// parseGameruleReply 从一行输出里解出「哪条规则 = 什么值」。
//
// 两条同时成立才认（见文件头注释）：
//
//	① 行里出现已知规则名（词边界匹配，防止 doDaylightCycle 误配 doDaylightCycleX）
//	② 行尾能取到值（true/false/整数）
//
// 再加一条顺序约束：值必须出现在名字之后（防跨字段误配）。
func parseGameruleReply(line string, known []string) (name, value string, ok bool) {
	if strings.HasPrefix(line, "[EnderBox] > ") {
		return "", "", false // 面板自己的回显 —— 设置命令的回显行尾同样有值，必须在这里挡掉
	}
	vm := gameruleValueTailRE.FindStringSubmatchIndex(line)
	if vm == nil {
		return "", "", false
	}
	valueStart := vm[2]
	for _, n := range known {
		idx := strings.Index(line, n)
		if idx < 0 {
			continue
		}
		// 词边界：前后不能还是 [A-Za-z0-9_]（否则 "spawnRadius" 会命中 "spawnRadiusXYZ"）
		if idx > 0 && gameruleWordByte(line[idx-1]) {
			continue
		}
		if end := idx + len(n); end < len(line) && gameruleWordByte(line[end]) {
			continue
		}
		if idx >= valueStart {
			continue // 名字在值后面 —— 不是「规则=值」的行
		}
		// ③ 名字与值之间必须隔着一段**本地化说明文字**（英文 " is currently set to: "、
		// 中文 "目前为："），不能只有空格。否则聊天里一句 "keepInventory true"、
		// 或 "<玩家> doDaylightCycle false" 就会被当成规则值读走 —— 单测里踩过。
		if strings.TrimSpace(line[idx+len(n):valueStart]) == "" {
			continue
		}
		return n, line[valueStart:vm[3]], true
	}
	return "", "", false
}

// ────────────────────────────────────────────── 目录（名字来源）

type jarGamerule struct {
	label string
	note  string
}

// scanGameruleCatalog 在实例目录里找服务端 jar 里的 en_us.json。
// 候选顺序 = 「最可能有、且跟实例版本最贴」在前：
//  1. versions/*/*.jar   —— Paper 系的组装产物（paperclip 把原版+补丁合成在这里）
//  2. cache/*.jar        —— Paper 系缓存的原版 jar（mojang_1.20.1.jar 这种）
//  3. libraries/net/minecraft/server/*/*.jar —— Forge/NeoForge 布局
//  4. jar 本身（resolveJar 指到的文件）—— vanilla bundler / Arclight 这类
//
// 每个候选只做「开 zip → 找一条目」，毫秒级，所以不做缓存：每次都重扫，永远新鲜。
// 注意：不做缓存还有个原因 —— 用户换了服务端版本、重下了 jar，缓存会让你看到旧规则集。
func scanGameruleCatalog(dir, jarPath string) (map[string]jarGamerule, bool) {
	var cands []string
	glob := func(pat string) {
		ms, _ := filepath.Glob(filepath.Join(dir, filepath.FromSlash(pat)))
		cands = append(cands, ms...)
	}
	glob("versions/*/*.jar")
	glob("cache/*.jar")
	glob("libraries/net/minecraft/server/*/*.jar")
	if jarPath != "" {
		if abs, err := filepath.Abs(jarPath); err == nil {
			cands = append(cands, abs)
		}
	}

	seen := map[string]bool{}
	n := 0
	for _, c := range cands {
		abs, err := filepath.Abs(c)
		if err != nil || seen[abs] {
			continue
		}
		seen[abs] = true
		n++
		if n > 24 { // 列表上限：正常实例最多几个 jar，24 够用；防止某个奇葩目录把接口拖慢
			break
		}
		if m, ok := catalogFromJar(abs); ok {
			return m, true
		}
	}
	return nil, false
}

// catalogFromJar 从一个 jar 读规则目录：先查 jar 自己，再查内嵌的一层
// （vanilla 的 bundler 把真 jar 放在 META-INF/versions/<ver>/server-<ver>.jar 里）。
func catalogFromJar(path string) (map[string]jarGamerule, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, false
	}
	if m, ok := readLangZip(f, st.Size()); ok {
		return m, true
	}
	// 内嵌一层：只认 META-INF/versions/ 前缀（bundler 约定）。
	// 不扫 META-INF/libraries/** —— 那里是依赖库（几十上百个），既没有 assets 也白费时间。
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, false
	}
	for _, e := range zr.File {
		if !strings.HasPrefix(e.Name, "META-INF/versions/") || !strings.HasSuffix(e.Name, ".jar") {
			continue
		}
		rc, err := e.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		if m, ok := readLangZip(bytes.NewReader(data), int64(len(data))); ok {
			return m, true
		}
	}
	return nil, false
}

// readLangZip 在 zip 里找 assets/minecraft/lang/en_us.json 并提取 gamerule.* 键。
func readLangZip(r io.ReaderAt, size int64) (map[string]jarGamerule, bool) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, false
	}
	for _, e := range zr.File {
		if e.Name != "assets/minecraft/lang/en_us.json" {
			continue
		}
		rc, err := e.Open()
		if err != nil {
			return nil, false
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, false
		}
		var raw map[string]string
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, false // 形态变了（非 flat map）就当读不到，退基线表
		}
		return extractGamerules(raw), true
	}
	return nil, false
}

// extractGamerules 从语言文件里抽出规则。`gamerule.<name>` = 显示名，
// `gamerule.<name>.description` = 描述，`gamerule.category.*` 是分组名（不要）。
func extractGamerules(m map[string]string) map[string]jarGamerule {
	out := map[string]jarGamerule{}
	for k, v := range m {
		if !strings.HasPrefix(k, "gamerule.") {
			continue
		}
		rest := strings.TrimPrefix(k, "gamerule.")
		if strings.HasPrefix(rest, "category.") {
			continue
		}
		name, isDesc := rest, false
		if strings.HasSuffix(rest, ".description") {
			name, isDesc = strings.TrimSuffix(rest, ".description"), true
		}
		if !gameruleNameRE.MatchString(name) {
			continue // 防 `gamerule.` 前缀下的杂键
		}
		e := out[name]
		if isDesc {
			e.note = v
		} else {
			e.label = v
		}
		out[name] = e
	}
	return out
}

// ────────────────────────────────────────────── 读

// Gamerules 读某个实例的全部游戏规则与当前值。
// 服务器必须正在运行 —— 规则值在服务端内存里，命令是唯一通道。
func (m *Manager) Gamerules(id string) ([]GameruleRow, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return nil, ErrInstanceNotFound
	}
	m.mu.Lock()
	p, running := m.procs[id]
	m.mu.Unlock()
	if !running {
		return nil, ErrGameruleNoServer
	}

	cat, _ := scanGameruleCatalog(inst.Dir, resolveJar(inst))
	names := mergeGameruleNames(cat)
	// 发全部单查。名字数 = jar 集的并集（通常 45~55 条），一次性连发，
	// 服务端逐行执行 —— 实测 vanilla/Mohist 都是一口气回完。
	cmds := make([]string, 0, len(names))
	for _, n := range names {
		cmds = append(cmds, "gamerule "+n)
	}
	// 「集齐即返回」：目录 = 这台服务端 jar 里的规则全集，全都读到值就说明读完了，
	// 不必再赌安静期（详见 Request 的 enough 注释）。目录读不到（Fabric 等布局）
	// 时 want=0，这个回调恒 false，自动退回安静期语义。
	want := len(cat)
	lines := p.Request(cmds, gameruleQuiet, gameruleMax, func(got []string) bool {
		if want == 0 {
			return false
		}
		seen := map[string]bool{}
		for _, ln := range got {
			if n, _, ok := parseGameruleReply(ln, names); ok {
				seen[n] = true
			}
		}
		return len(seen) >= want
	})

	// 解析：同一名字可能有多行（我们只发一次查询，理论上一条），后到的覆盖先到的 ——
	// 值的「最后一次出现」最接近当前状态。
	values := map[string]string{}
	for _, ln := range lines {
		if n, v, ok := parseGameruleReply(ln, names); ok {
			values[n] = v
		}
	}

	rows := make([]GameruleRow, 0, len(values))
	for _, n := range names {
		v, ok := values[n]
		if !ok {
			continue // 服务端不认这条（版本没有/名字拼错）—— 不显示，也不报错
		}
		rows = append(rows, buildGameruleRow(n, v, cat))
	}
	if len(rows) == 0 {
		return nil, ErrGameruleNoReply
	}
	sortGameruleRows(rows)
	return rows, nil
}

// mergeGameruleNames 目录 ∪ 基线表，去重 + 排序（排序让命令顺序稳定、输出可复现）。
func mergeGameruleNames(cat map[string]jarGamerule) []string {
	set := map[string]bool{}
	for n := range cat {
		set[n] = true
	}
	for _, n := range gameruleBaseline {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// buildGameruleRow 合并三个来源：值（服务端）→ 组/中文名（spec 表）→ 显示名（jar）。
func buildGameruleRow(name, value string, cat map[string]jarGamerule) GameruleRow {
	row := GameruleRow{Name: name, Label: name, Group: gameruleGroupFallback, Value: value, Kind: "int"}
	if value == "true" || value == "false" {
		row.Kind = "bool"
	}
	if sp, ok := gameruleSpecs[name]; ok {
		if sp.label != "" {
			row.Label = sp.label
		}
		row.Note = sp.note
		if sp.group != "" {
			row.Group = sp.group
		}
	} else if e, ok := cat[name]; ok && e.label != "" {
		// spec 没收录（多半是更高版本新增的规则）—— 用 jar 的英文显示名，别只给驼峰机器名。
		row.Label = e.label
	}
	return row
}

// sortGameruleRows 组按预设顺序，组内按名字；名字分组之间保持稳定可预期。
func sortGameruleRows(rows []GameruleRow) {
	order := map[string]int{}
	for i, g := range gameruleGroupOrder {
		order[g] = i
	}
	rank := func(g string) int {
		if i, ok := order[g]; ok {
			return i
		}
		return len(gameruleGroupOrder) + 1
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := rank(rows[i].Group), rank(rows[j].Group)
		if ri != rj {
			return ri < rj
		}
		return rows[i].Name < rows[j].Name
	})
}

// ────────────────────────────────────────────── 写

// SetGamerule 改一条规则，返回服务端确认后的实际值。
// 校验三层：格式（正则）→ 目录（已知名字）→ 命令回显确认。
func (m *Manager) SetGamerule(id, name, value string) (string, error) {
	name = strings.TrimSpace(name)
	value = strings.TrimSpace(value)
	// ① 格式：名字只允许 [A-Za-z][A-Za-z0-9]* —— 这是防注入的第一道闸
	//（命令走 stdin 行协议，名字里塞 \n 能再发一条命令；value 也一样）
	if !gameruleNameRE.MatchString(name) {
		return "", fmt.Errorf("规则名不合法：%q", name)
	}
	if !gameruleValueRE.MatchString(value) {
		return "", fmt.Errorf("值不合法：%q（只接受 true / false / 整数）", value)
	}
	// ② 目录：必须是这台服务器真实存在的规则。
	// 第二道闸 —— 就算正则被绕过，这里也只放行目录里的名字。
	inst, ok := m.st.Get(id)
	if !ok {
		return "", ErrInstanceNotFound
	}
	cat, _ := scanGameruleCatalog(inst.Dir, resolveJar(inst))
	known := false
	for _, n := range mergeGameruleNames(cat) {
		if n == name {
			known = true
			break
		}
	}
	if !known {
		return "", ErrGameruleNotFound
	}

	m.mu.Lock()
	p, running := m.procs[id]
	m.mu.Unlock()
	if !running {
		return "", ErrGameruleNoServer
	}

	lines := p.Request([]string{"gamerule " + name + " " + value}, gameruleQuiet, gameruleMax, nil)
	// 确认：服务端回 "…is now set to: X"（中文"…已被设为：X"）。
	// 只认「我们刚改的这条」的行 —— 值以服务端实际生效为准（个别规则会 clamp）。
	v, ok := "", false
	for _, ln := range lines {
		if n, got, hit := parseGameruleReply(ln, []string{name}); hit && n == name {
			v, ok = got, true
		}
	}
	if !ok {
		return "", fmt.Errorf("服务端没有确认这次修改 —— 看看控制台（是不是还没启动完）")
	}
	return v, nil
}

// ────────────────────────────────────────────── 中文叠加层（spec 表）

// 为什么要有这张表：jar 里只有英文显示名（中文语言文件在客户端，服务端不带）。
// 表是**叠加层**不是全集 —— 命中就用中文 + 分组；没命中（新版新增的规则）
// 回落到 jar 的英文显示名 + 「杂项」分组，不会因为表旧了就看不到规则。
//
// note 只给「看名字猜不出来」的规则 —— 每行都写解释会把页面变成说明书，
// 用户要的是改开关，不是读文档。
type gameruleSpec struct{ label, note, group string }

// gameruleGroupFallback spec 没命中时的分组（也是未知规则的落点）。
const gameruleGroupFallback = "杂项"

// 分组显示顺序。新的组加在末尾即可。
var gameruleGroupOrder = []string{"玩家", "伤害", "掉落", "生物", "世界", "聊天与指令", "杂项"}

var gameruleSpecs = map[string]gameruleSpec{
	// ── 玩家
	"keepInventory":            {label: "死亡保留物品", note: "开了之后死亡不掉落背包和经验", group: "玩家"},
	"naturalRegeneration":      {label: "自然回血", note: "饱食度满时缓慢回血", group: "玩家"},
	"doImmediateRespawn":       {label: "立即重生", note: "死了跳过重生界面直接复活", group: "玩家"},
	"reducedDebugInfo":         {label: "精简调试信息", note: "F3 不显示坐标等敏感信息", group: "玩家"},
	"spectatorsGenerateChunks": {label: "旁观者也加载区块", group: "玩家"},
	"playersSleepingPercentage": {label: "睡觉跳过夜晚所需比例",
		note: "100 = 全员到齐；0 = 一人上床就天亮", group: "玩家"},
	"showDeathMessages":  {label: "显示死亡消息", group: "玩家"},
	"forgiveDeadPlayers": {label: "原谅死亡玩家", note: "中立生物的目标死掉后，它不再敌对", group: "玩家"},
	"universalAnger":     {label: "无差别激怒", note: "惹一只，附近同类全部敌对", group: "玩家"},
	"doLimitedCrafting":  {label: "限制合成", note: "只能合成已解锁的配方", group: "玩家"},

	// ── 伤害
	"fallDamage":                 {label: "摔落伤害", group: "伤害"},
	"fireDamage":                 {label: "火焰伤害", group: "伤害"},
	"drowningDamage":             {label: "溺水伤害", group: "伤害"},
	"freezeDamage":               {label: "冰冻伤害", group: "伤害"},
	"disableElytraMovementCheck": {label: "关闭鞘翅移动检查", note: "网络卡被误判「飞太快」踢出时开这个", group: "伤害"},
	"disableRaids":               {label: "关闭袭击", group: "伤害"},

	// ── 掉落
	"doTileDrops":             {label: "方块掉落", note: "挖掉的方块掉不掉东西", group: "掉落"},
	"doEntityDrops":           {label: "实体掉落", note: "矿车/船/展示框被破坏时掉不掉本体", group: "掉落"},
	"doMobLoot":               {label: "生物掉落", note: "含经验球", group: "掉落"},
	"blockExplosionDropDecay": {label: "方块爆炸掉落衰减", note: "爆炸炸毁的方块掉落率打折", group: "掉落"},
	"mobExplosionDropDecay":   {label: "生物爆炸掉落衰减", group: "掉落"},
	"tntExplosionDropDecay":   {label: "TNT 爆炸掉落衰减", group: "掉落"},

	// ── 生物
	"doMobSpawning":     {label: "自然刷新生物", group: "生物"},
	"doInsomnia":        {label: "幻翼生成", note: "关掉之后不睡觉也不刷幻翼", group: "生物"},
	"doPatrolSpawning":  {label: "巡逻队生成", group: "生物"},
	"doTraderSpawning":  {label: "流浪商人生成", group: "生物"},
	"doWardenSpawning":  {label: "监守者生成", group: "生物"},
	"mobGriefing":       {label: "生物破坏方块", note: "末影人搬方块、苦力怕炸地形、村民种地", group: "生物"},
	"maxEntityCramming": {label: "实体挤压上限", note: "同一格挤太多实体开始受伤的阈值", group: "生物"},

	// ── 世界
	"doDaylightCycle":        {label: "昼夜更替", group: "世界"},
	"doWeatherCycle":         {label: "天气更替", group: "世界"},
	"doFireTick":             {label: "火焰蔓延", group: "世界"},
	"doVinesSpread":          {label: "藤蔓蔓延", group: "世界"},
	"randomTickSpeed":        {label: "随机刻速度", note: "调高作物长得快，也更吃 CPU", group: "世界"},
	"snowAccumulationHeight": {label: "积雪高度", group: "世界"},
	"lavaSourceConversion":   {label: "岩浆源转换", group: "世界"},
	"waterSourceConversion":  {label: "水源转换", group: "世界"},
	"spawnRadius":            {label: "出生点范围", note: "新手出生点免伤区的半径", group: "世界"},
	"globalSoundEvents":      {label: "全局音效", note: "Boss 刷新这类事件全图都听得见", group: "世界"},

	// ── 聊天与指令
	"announceAdvancements": {label: "广播进度", note: "有人拿到进度就全服播报", group: "聊天与指令"},
	"commandBlockOutput":   {label: "广播命令方块输出", group: "聊天与指令"},
	"logAdminCommands":     {label: "记录管理员命令", group: "聊天与指令"},
	"sendCommandFeedback":  {label: "发送命令反馈", note: "关掉后执行命令不再回显结果", group: "聊天与指令"},

	// ── 杂项
	"commandModificationBlockLimit": {label: "命令修改方块上限", note: "fill / clone 一次能改多少方块", group: "杂项"},
	"maxCommandChainLength":         {label: "命令链长度上限", note: "命令方块链和函数的最大执行长度", group: "杂项"},
}

// gameruleBaseline jar 读不到时的兜底名单（1.20.1 全集 + 后续版本已知新增）。
// 「这台服务器没有」的条目会被单查报错自然滤掉 —— 所以宁多勿缺，
// 版本更新后往这里加新规则名（或者干脆换能读到 jar 的服务端）。
var gameruleBaseline = []string{
	"announceAdvancements", "blockExplosionDropDecay", "commandBlockOutput",
	"commandModificationBlockLimit", "disableElytraMovementCheck", "disableRaids",
	"doDaylightCycle", "doEntityDrops", "doFireTick", "doImmediateRespawn",
	"doInsomnia", "doLimitedCrafting", "doMobLoot", "doMobSpawning",
	"doPatrolSpawning", "doTileDrops", "doTraderSpawning", "doVinesSpread",
	"doWardenSpawning", "doWeatherCycle", "drowningDamage", "fallDamage",
	"fireDamage", "forgiveDeadPlayers", "freezeDamage", "globalSoundEvents",
	"keepInventory", "lavaSourceConversion", "logAdminCommands",
	"maxCommandChainLength", "maxEntityCramming", "mobExplosionDropDecay",
	"mobGriefing", "naturalRegeneration", "playersSleepingPercentage",
	"randomTickSpeed", "reducedDebugInfo", "sendCommandFeedback",
	"showDeathMessages", "snowAccumulationHeight", "spawnRadius",
	"spectatorsGenerateChunks", "tntExplosionDropDecay", "universalAnger",
	"waterSourceConversion",
	// 1.20.2+ / 1.21 新增（存在性由服务端过滤，多问无害）
	"enderPearlsVanishOnDeath", "projectilesCanBreakBlocks", "spawnChunkRadius",
	"playersNetherPortalDefaultDelay", "playersNetherPortalCreativeDelay",
	"maxCommandForkCount",
}
