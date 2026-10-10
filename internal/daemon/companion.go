package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/su-ni/EnderBox/internal/companion"
	"github.com/su-ni/EnderBox/internal/materials"
)

// ── 伴侣插件 ──
//
// 面板想要「玩家现在在哪、背包里有什么、还剩多少血」，而这些东西只存在于服务端内存里，
// 读任何文件都读不到。唯一的办法是在服务端里放一个观察者。
//
// 通信走文件（双向），不走网络：
//
//	<实例>/plugins/EnderBoxCompanion/bridge/snapshot.json        插件 → 面板
//	<实例>/plugins/EnderBoxCompanion/bridge/inbox/cmd-<id>.json  面板 → 插件
//	<实例>/plugins/EnderBoxCompanion/bridge/outbox/res-<id>.json 插件 → 面板
//
// 为什么是文件而不是开一个端口：端口要处理防火墙、要选端口、要鉴权，
// 而插件已经在一个「被服务端管着的目录」里 —— 文件是最不需要协商的通道。

// CompanionSupport 这个实例能不能用伴侣插件，以及不能的话是为什么。
//
// 「不能」必须给出原因：不然用户开着一个 Fabric 端，看到「实时数据」一直空白，
// 只会以为是面板坏了。
type CompanionSupport struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
	// Hybrid 混合端（Forge + Connector 之类）。能用，但没人替我们兜底，
	// 界面上要把这句话原样说给用户听。
	Hybrid bool `json:"hybrid,omitempty"`
	// Folia 单独标出来：它也是「能用 Bukkit 插件」的端，但调度模型不同，
	// 属于「长得像、其实不行」。前端要能把它和普通的「不支持」区分开
	// （普通不支持是换个端就好，Folia 是换端之前用不了实时数据）。
	Folia bool `json:"folia,omitempty"`
}

// jarLooksLike 实例的启动文件名字里有没有这个词。
//
// 为什么看文件名而不是看类型：Folia / Leaves / Purpur 都是 Paper 那一套的 fork，
// 目录结构、启动参数、插件目录完全一致 —— 类型上分不出来，但行为上 Folia 差得远。
// 文件名是这里唯一可靠的信号，而且用户要改名早就出问题了。
func jarLooksLike(inst Instance, word string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(inst.JarPath)), word)
}

// 桥接模组：把它们丢进 mods/，模组端就长出了 Bukkit 的 plugins/ 目录。
// Cardboard 是 Fabric 侧最常用的那个，Sinytra Connector 是 NeoForge 侧的。
// ⚠ 别把这个名单和 isHybridLauncherName 合并 —— 那边的名字是「能直接启动的整端 jar」，
// 这边是「装在 mods/ 里的模组」，两件事。Cardboard 恰好两边都有，其余并不重合。
var bridgeModWords = []string{"cardboard", "sinytra", "connector"}

// hasBukkitBridgeMod 看 mods/ 里有没有桥接模组。
//
// 为什么必须看这个目录：Cardboard 这类**不是**「一个能直接启动的 jar」——
// 启动文件是 `fabric-server.jar`，Cardboard 本人躺在 `mods/` 里。
// 只看启动文件名的话，一台**已经装好桥接、插件正在正常写数据**的服务器会被判成
// 「不支持」—— 于是界面上「不支持实时数据」和「1 人在线」同时挂着，自己打自己的脸。
// （实测：Fabric 1.20.1 + Cardboard + Fabric API + iCommon，插件加载正常。）
//
// 读不到 mods/ 就当没装 —— 和「目录还没建出来」的旧行为一致，不会误判。
func hasBukkitBridgeMod(inst Instance) bool {
	entries, err := os.ReadDir(filepath.Join(inst.Dir, "mods"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		low := strings.ToLower(e.Name())
		if !strings.HasSuffix(low, ".jar") {
			continue
		}
		for _, w := range bridgeModWords {
			if strings.Contains(low, w) {
				return true
			}
		}
	}
	return false
}

// isBukkitHybrid 混合端：本体是模组端，另外挂了一套 Bukkit 实现。
//
// ⚠ 只有「认得出名字」的才算。用户拿面板建的 Forge 服（jarPath 是 forge-...-shim.jar）
// 不带这些词，走的是下面 Forge 那条分支 —— 那是对的，他不一定有 Bukkit。
//
// 两条路：整端 jar 名字里带（Mohist/Arclight/CatServer 这种一个 jar 全包），
// 或者 mods/ 里装了桥接模组（Cardboard 这种）。
//
// ⚠ 还有第三条路，不在这里：**实例类型本身就是混合端**（面板新建出来的，见 isHybridType）。
// 新建那一刻 jarPath 还只是 `server.jar`，名字里什么都认不出来。
func isBukkitHybrid(inst Instance) bool {
	for _, m := range []string{"mohist", "arclight", "catserver", "magma", "cardboard", "banner"} {
		if jarLooksLike(inst, m) {
			return true
		}
	}
	return hasBukkitBridgeMod(inst)
}

// isHybridType 这个**类型**本身是不是混合端（模组端 + Bukkit 实现）。
//
// 和 isBukkitHybrid 的分工：那个看文件，这个看类型。两者都要 ——
// 面板新建的混合端目录里还没有真 jar（jarPath 只是占位的 server.jar），
// 光看文件会把一台刚建好的 Mohist 判成「原版服务端，没有插件系统」；
// 反过来，用户自己丢一个 mohist jar 进去导入、类型还是 forge 的老实例，
// 光看类型也认不出来（那条路由 isBukkitHybrid 兜住）。
//
// ⚠ 这里**只收「独立存在」的两个**：CatServer / Magma / Cardboard / Banner
// 没有可用的官方下载源，面板建不出来，它们只会以「导入」的形式出现 ——
// 那种情况下启动文件名一定认得出来，走 isBukkitHybrid 就够了。
// 等哪天它们也能一键新建了，往这里加一行即可。
func isHybridType(t Type) bool {
	return t == TypeMohist || t == TypeArclight
}

// hybridSupport 混合端的判定结果。**两处共用同一份文案**（isBukkitHybrid 与 isHybridType）——
// 各写一份的话，同一个状态在两台机器上说两套话，用户以为遇到了两种不同的问题。
func hybridSupport() CompanionSupport {
	return CompanionSupport{
		Supported: true,
		Hybrid:    true,
		Reason: "这是一台混合端（模组端 + Bukkit）。插件能装，但混合端的兼容性是" +
			"「尽力而为」：出问题上游不会受理，插件和模组抢同一个类也不是没发生过。" +
			"真出问题就把它关掉，不影响开服。",
	}
}

// CompanionSupportOf 判定一个实例能不能用。
//
// 它只读实例目录里的**一个**位置（mods/，看有没有 Bukkit 桥接模组），其余全靠实例自身
// 的字段。读不到 mods/ 就当没装 —— 和「目录还没建出来」的旧行为一致，
// 不会因此把一台正常的服务器误判成「不支持」。
func CompanionSupportOf(inst Instance) CompanionSupport {
	// 名字能认出来的特例先判。它们和同名类型的东西**行为不一样**，
	// 光看 inst.Type 判不出来 —— Folia 的启动方式、插件目录、EULA 和 Paper 一模一样。
	if jarLooksLike(inst, "folia") {
		// 实测（Folia 1.20.1 build 17）：没有声明 folia-supported 的插件，
		// Folia 会在加载阶段直接拒掉：
		//   Could not load plugin 'EnderBoxCompanion v0.2.0' as it is not marked as supporting Folia!
		// 声明了也没用 —— 它把 Bukkit 那套单线程调度器换成了「按区域分线程」的模型，
		// 我们那个 runTaskTimer 会在 onEnable 里直接抛 UnsupportedOperationException。
		//
		// 那要不要为 Folia 单写一套区域化调度？不值：Folia 是给大型公开服做多线程的，
		// 「家里闲置电脑开小服」这个场景根本用不上它，而改写读取逻辑的代价是
		// 每个玩家都要在自己所属的区域线程里取数据 —— 半吊子支持比不支持更坏。
		return CompanionSupport{
			Supported: false,
			Folia:     true,
			Reason: "Folia 用的是「按区域分线程」的调度模型，和插件的定时器不兼容 ——" +
				"实测它会在加载阶段直接拒绝这个插件。面板不会往 Folia 里塞一个跑不起来的东西：" +
				"要实时数据就用 Paper / Spigot / Purpur / Leaves，这些是一套东西。",
		}
	}
	if isBukkitHybrid(inst) || isHybridType(inst.Type) {
		// 混合端（Mohist / Arclight / CatServer / Cardboard）同时挂了 Bukkit 实现，
		// 所以 plugins/ 是真能用的。但它们是"两套东西缝在一起"，出错没人会替我们查 ——
		// 用户拍板的是「照注入，但把风险说清楚」。
		return hybridSupport()
	}
	// ⚠ 顺序：先按"这是什么端"分流，再谈 Java 版本。反过来判会当场撒谎 ——
	//
	// 代理端（Velocity / BungeeCord）**没有 MC 版本这个概念**，导入进来 inst.MCVersion
	// 是空的；而 JavaForVersion("") 会把空串当成 0.0.0 → 算出 Java 8 → 命中下面那条
	// "需要 Java 8" 的分支。于是一台 Velocity 的界面上写着
	// 「这个插件要求 Java 17 以上，而  需要 Java 8」：端名是空的（%s 没东西可填），
	// 原因也是错的。真实的拒绝原因服务端自己都打出来了：
	//
	//	The plugin file EnderBoxCompanion.jar appears to be a Bukkit or BungeeCord
	//	plugin. Velocity does not support Bukkit or BungeeCord plugins.
	//
	// 实测就是导入 Velocity 3.4.0 时这么显示的。Java 那条规矩只对**真的跑地图的 Bukkit 端**
	// 有意义，所以挪进 TypePaper 分支里判。
	switch inst.Type {
	case TypeVelocity, TypeBungee:
		// ⚠ 引用的原话必须和用户眼前这端对得上。两家代理端的拒绝方式根本不是一回事：
		// Velocity 会**当场识别并拒绝**；BungeeCord 会先把插件收下、再去加载它的主类，
		// 于是死在「找不到 org.bukkit.plugin.java.JavaPlugin」上。
		// 给 BungeeCord 的服务器贴一句 Velocity 的报错，就是在编。
		var evidence string
		if inst.Type == TypeBungee {
			evidence = "实测它会把插件收下、然后在加载主类时崩掉：" +
				"「NoClassDefFoundError: org/bukkit/plugin/java/JavaPlugin」（它没有 Bukkit 那套类）。"
		} else {
			evidence = "实测 Velocity 会当场拒绝：「appears to be a Bukkit or BungeeCord plugin」。"
		}
		return CompanionSupport{
			Supported: false,
			Reason: fmt.Sprintf("%s 是代理端 —— 它自己不开地图、不跑游戏逻辑，也就不认 Bukkit 插件。"+
				"插件要放到它后面那些真正的服务器上。%s", TypeDisplay(inst.Type), evidence),
		}
	case TypeForge, TypeNeoForge, TypeFabric:
		// 这些端本身不认 Bukkit 插件。除非装了桥接模组 —— 那种情况下 plugins/ 目录
		// 是真能用的，但属于混合端，风险自负。
		// ⚠ 能走到这里，说明上面 isBukkitHybrid 已经查过 mods/ 了：确实没装桥接。
		bridge := "Sinytra Connector"
		if inst.Type == TypeFabric {
			bridge = "Cardboard"
		}
		return CompanionSupport{
			Supported: false,
			Hybrid:    true,
			Reason: fmt.Sprintf("%s 是模组端，它不认 Bukkit 插件 —— "+
				"plugins/ 目录对它来说等于不存在。"+
				"装了 %s 这类桥接模组之后插件才真的能跑（实测：Fabric 1.20.1 + Cardboard 加载正常）。"+
				"桥接得你自己装：面板不会替你往别人家的服务端里塞模组，所以这里先按「用不了」算。",
				TypeDisplay(inst.Type), bridge),
		}
	case TypePaper:
		// 唯一的「真能跑」分支，Java 那条规矩也只在这里有意义。
		// ⚠ 版本未知时不下结论：插件确实是按 Java 17 编的，但拿一个"猜出来的 8"
		// 说「你的服务器需要 Java 8」，比不说更坏。让它去启动、让服务端的原话来说
		// （UnsupportedClassVersionError 会被 pluginTrouble 捞到界面上）。
		if inst.MCVersion != "" && JavaForVersion(inst.MCVersion) < 17 {
			return CompanionSupport{
				Supported: false,
				Reason: fmt.Sprintf("这个插件要求 Java 17 以上，而 MC %s 需要 Java %d。"+
					"往 Java %d 的服务端里塞一个 Java 17 的 jar，它连加载都不会加载。",
					inst.MCVersion, JavaForVersion(inst.MCVersion), JavaForVersion(inst.MCVersion)),
			}
		}
		return CompanionSupport{Supported: true}
	default: // vanilla
		return CompanionSupport{
			Supported: false,
			Reason:    "原版服务端没有插件系统 —— 换成 Paper 就能用上实时数据。",
		}
	}
}

// CompanionWanted 用户想不想开。nil（从没设置过）按「开」算 —— 这是用户拍的默认值。
func (i Instance) CompanionWanted() bool {
	return i.Companion == nil || *i.Companion
}

// bridgeDir 插件与面板之间那个目录。
func bridgeDir(inst Instance) string {
	return filepath.Join(append([]string{inst.Dir}, companion.BridgeRel...)...)
}

// CompanionDir 暴露给上层（比如「打开目录」按钮）。
func (m *Manager) CompanionDir(id string) (string, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	return bridgeDir(inst), nil
}

// SyncCompanion 按开关把插件 jar 放进 plugins/ 或从里面拿掉。
//
// 幂等：内容一致就不重写。每次启停都重写一遍会白白改动文件时间，
// 让「这个插件什么时候装的」变成谎言。
func (m *Manager) SyncCompanion(inst Instance) error {
	dir := filepath.Join(inst.Dir, "plugins")
	dst := filepath.Join(dir, companion.FileName)

	want := CompanionSupportOf(inst).Supported && inst.CompanionWanted()
	if !want {
		if _, err := os.Stat(dst); err == nil {
			return os.Remove(dst)
		}
		return nil
	}

	if old, err := os.ReadFile(dst); err == nil && bytes.Equal(old, companion.Jar) {
		return nil // 已经是这一份了
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// 先写临时文件再改名：半截的 jar 会让服务端在启动时报一句看不懂的错，
	// 而不是「插件损坏」—— 原子改名能彻底避免这种中间态。
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, companion.Jar, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ── 快照 ──

// CompanionPlayer 一个玩家的实时状态。
// 字段名与插件写出的 JSON 一一对应 —— 两边不对称就会静默读出零值。
type CompanionPlayer struct {
	UUID        string  `json:"uuid"`
	Name        string  `json:"name"`
	DisplayName string  `json:"displayName,omitempty"`
	Online      bool    `json:"online"`
	World       string  `json:"world,omitempty"`
	Dim         string  `json:"dim,omitempty"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Z           float64 `json:"z"`
	Yaw         float64 `json:"yaw"`
	Pitch       float64 `json:"pitch"`
	Health      float64 `json:"health"`
	MaxHealth   float64 `json:"maxHealth"`
	Food        int     `json:"food"`
	Saturation  float64 `json:"saturation"`
	Level       int     `json:"level"`
	Exp         float64 `json:"exp"`
	Gamemode    string  `json:"gamemode,omitempty"`
	Op          bool    `json:"op"`
	Dead        bool    `json:"dead"`
	Ping        int     `json:"ping"`
	Skin        string  `json:"skin,omitempty"`
	FirstPlayed int64   `json:"firstPlayed,omitempty"`
	LastPlayed  int64   `json:"lastPlayed,omitempty"`
	LastSeenAt  int64   `json:"lastSeenAt,omitempty"`

	Effects    []CompanionEffect         `json:"effects"`
	Inventory  []CompanionItem           `json:"inventory"`
	Equipment  map[string]*CompanionItem `json:"equipment,omitempty"`
	EnderChest []CompanionItem           `json:"enderChest"`

	// ⚠ slot 别加 omitempty：0 号格是背包第一格，是最常出现的那个值，
	// 加了 omitempty 它会被当成「没有」而消失，前端一渲染就错位。

	// ⚠ Effects / Inventory / EnderChest / Ping 这几个**别加 omitempty**：
	// 0 和空数组都是有效值（ping 0ms、背包真的是空的），
	// 带上 omitempty 会被当成「没有这个字段」而整个消失，
	// 前端就得在每个地方写 `?? []`，迟早漏一个。

	// DimLabel 维度的中文名。后端算好，前端不该再认一遍 minecraft:the_nether。
	DimLabel string `json:"dimLabel,omitempty"`
}

// CompanionEffect 一个状态效果
type CompanionEffect struct {
	ID        string `json:"id"`
	Amplifier int    `json:"amplifier"`
	Duration  int    `json:"duration"`
	Ambient   bool   `json:"ambient,omitempty"`
	Label     string `json:"label"`
	AmplLabel string `json:"amplLabel,omitempty"`
}

// CompanionItem 一格物品
type CompanionItem struct {
	Slot        int               `json:"slot"`
	ID          string            `json:"id"`
	Count       int               `json:"count"`
	Name        string            `json:"name,omitempty"` // 物品自带的自定义名（已去色）
	Label       string            `json:"label"`          // 最终显示名：自定义 > 中文 > 英文 > id
	Lore        []string          `json:"lore,omitempty"`
	Damage      int               `json:"damage,omitempty"`
	MaxDamage   int               `json:"maxDamage,omitempty"`
	Unbreakable bool              `json:"unbreakable,omitempty"`
	Potion      string            `json:"potion,omitempty"`
	Enchants    map[string]int    `json:"enchants,omitempty"`
	EnchLabels  map[string]string `json:"enchLabels,omitempty"`
	Icon        string            `json:"icon,omitempty"`
}

// CompanionSnapshot 给前端看的实时数据总览
type CompanionSnapshot struct {
	// Have 有没有读到快照。false 时看 Note，别显示一堆 0。
	Have bool `json:"have"`
	// Note 读不到时解释原因（没开 / 没启动过 / 服务端不支持……）
	Note string `json:"note,omitempty"`
	// Stale 不加 omitempty：false 也要发出去。吞掉它前端就只能靠 undefined 兜，
	// 而「明确说了没事」和「后端没提这回事」是两件不同的事。
	Stale         bool              `json:"stale"`
	GeneratedAt   string            `json:"generatedAt,omitempty"`
	PluginVersion string            `json:"pluginVersion,omitempty"`
	ServerVersion string            `json:"serverVersion,omitempty"`
	OnlineCount   int               `json:"onlineCount"`
	Online        []CompanionPlayer `json:"online"`
	Offline       []CompanionPlayer `json:"offline"`
	Support       CompanionSupport  `json:"support"`
	Installed     bool              `json:"installed"`
	Running       bool              `json:"running"`
}

// CompanionSnapshot 读插件写的快照，并把物品 id 翻成中文名 + 图标地址。
//
// 翻译放在后端而不是前端：语言表是嵌在二进制里的 260KB，
// 前端每开一次页面都拉一遍纯属浪费，而且「显示名」的优先级规则
// （自定义名 > 中文 > 英文 > id）只能有一份实现。
// pluginTrouble 从实例日志的尾部捞出「插件为什么没上来」的原话（捞不到就返回空串）。
//
// 为什么值得单独做：插件的失败模式太多了 —— 平台拒绝加载、依赖缺失、被别的插件顶掉、
// 编译版本比服务端新…… 面板这边能看到的全都只是「没有数据」四个字。
// 而服务端自己在启动时就把原因打在日志里了，只是没人给它翻译。
// 把原话捞出来贴上，比我们猜一百句都准。
func (m *Manager) pluginTrouble(id string) string {
	// 先看服务端自己的 logs/latest.log。为什么不是先看内存里那份：
	// 内存里只有「面板亲自拉起来的进程」的输出 —— 用户手动开的服、
	// 或者面板刚重启过，内存里都是空的，而那恰恰是最需要说清原因的时候。
	// 服务端自己写的这份日志才是永远都在的。
	if inst, ok := m.st.Get(id); ok {
		if why := troubleFromServerLog(filepath.Join(inst.Dir, "logs", "latest.log")); why != "" {
			return why
		}
	}
	buf := m.logsFor(id)
	if buf == nil {
		return ""
	}
	return troubleFromLines(buf.Snapshot())
}

// troubleFromServerLog 读服务端日志尾部若干行再判。只读尾部：
// latest.log 能涨到几十 MB，全读进来只为找一行是浪费。
func troubleFromServerLog(path string) string {
	const tailBytes = 256 << 10
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	off := int64(0)
	if st.Size() > tailBytes {
		off = st.Size() - tailBytes
	}
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && len(buf) > 0 {
		// 读到一部分也能用，继续往下走
		_ = err
	}
	return troubleFromLines(strings.Split(string(buf), "\n"))
}

func troubleFromLines(rawLines []string) string {
	lines := rawLines
	const scan = 800
	if len(lines) > scan {
		lines = lines[len(lines)-scan:]
	}
	// 日志里从头到尾没提过这个插件，那就别猜 —— 日志里成千上万行报错，
	// 挑一条贴上去比不贴更坏。
	if !strings.Contains(strings.Join(lines, "\n"), companion.PluginName) {
		return ""
	}
	// 从后往前找「最近一次到底怎么了」。
	// 关键规则：碰到**成功启用**就立刻收工 —— 说明之前那些报错属于更早的一次尝试
	// （用户自己试过坏 jar、换过版本…），拿它来吓人就是撒谎。
	var generic string
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		low := strings.ToLower(l)
		if strings.Contains(l, companion.PluginName) &&
			(strings.Contains(l, "Enabling ") || strings.Contains(l, "已启用")) {
			return ""
		}
		// 有些平台把插件名和错误分开打（报错那行是异常栈里的 Caused by），
		// 退一步记一条「跟插件有关且明显是错的」，万一找不到点名的就用它。
		if generic == "" && logLineLooksBad(l) &&
			(strings.Contains(low, "plugin") || strings.Contains(l, "插件")) {
			generic = tidyLogLine(l)
		}
		if strings.Contains(l, companion.PluginName) && logLineLooksBad(l) {
			return tidyLogLine(l)
		}
	}
	return generic
}

func logLineLooksBad(l string) bool {
	low := strings.ToLower(l)
	for _, k := range []string{
		"error", "exception", "failed", "could not load", "failed to load",
		"not marked as supporting", "unsupported", "incompatible",
	} {
		if strings.Contains(low, k) {
			return true
		}
	}
	return false
}

// tidyLogLine 把日志行收拾成能贴进界面的一行：去掉 ANSI 颜色和控制字符，
// 去掉时间戳/线程前缀（界面上本来就在讲「这台服务器刚才说的」，前缀只会让这行更长）。
func tidyLogLine(l string) string {
	l = strings.Map(func(r rune) rune {
		if r == 0x1b {
			return -1
		}
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, l)
	l = strings.TrimSpace(l)
	if strings.HasPrefix(l, "[") {
		if i := strings.Index(l, "]: "); i >= 0 && i < 40 {
			l = l[i+3:]
		} else if i := strings.Index(l, "]:"); i >= 0 && i < 40 {
			l = l[i+2:]
		}
	}
	l = strings.TrimSpace(l)
	if len(l) > 300 {
		l = l[:300] + "…"
	}
	return l
}

func (m *Manager) CompanionSnapshot(id string) (CompanionSnapshot, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return CompanionSnapshot{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	sup := CompanionSupportOf(inst)
	// ⚠ 集合字段先补空集合。下面四条「早退」路径（这个端不支持 / 用户关掉了 /
	// 插件还没装 / 从来没启动过）都不会碰 Online / Offline，Go 的 nil slice
	// 会序列化成 `null` 而不是 `[]` —— 前端拿到 null 就得在每个地方写 `?? []`，
	// 迟早漏一个然后在 .map 上崩。（实测：导入 Velocity 后 /live 返回的就是
	// "online": null，"offline": null。）
	out := CompanionSnapshot{
		Support: sup,
		Running: m.isRunning(id),
		Online:  []CompanionPlayer{},
		Offline: []CompanionPlayer{},
	}
	_, statErr := os.Stat(filepath.Join(inst.Dir, "plugins", companion.FileName))
	out.Installed = statErr == nil

	if !sup.Supported {
		out.Note = sup.Reason
		return out, nil
	}
	if !inst.CompanionWanted() {
		out.Note = "你把这个实例的实时数据关掉了 —— 打开开关会在 plugins/ 里放一个只读插件。"
		return out, nil
	}
	if !out.Installed {
		out.Note = "插件还没装进去。启动一次服务器，面板会在启动前自动放好它。"
		return out, nil
	}

	p := filepath.Join(bridgeDir(inst), "snapshot.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			// 有插件、却没写出快照 —— 那就是它没上来。
			// 服务端早就把原因打在自己的日志里了（「failed to load!」这种原话），
			// 把那句捞出来，比我们编一句「插件可能卡住了」有用一百倍。
			//
			// ⚠ 这里**不能**加 `out.Running` 条件：服务端可能是用户自己手动开的
			// （面板重启过、或者他根本没从面板启动），那种情况下 running=false，
			// 而恰恰是最需要把原因说清楚的时候。
			if why := m.pluginTrouble(id); why != "" {
				out.Note = "插件没能起来。服务端是这么说的：" + why
				return out, nil
			}
			out.Note = "插件还没写出过快照 —— 服务器至少成功启动过一次才会有（目录：" +
				filepath.Join(companion.PluginName, companion.Bridge) + "）。"
			return out, nil
		}
		return out, fmt.Errorf("读快照失败：%w", err)
	}
	var doc struct {
		PluginVersion string            `json:"pluginVersion"`
		ServerVersion string            `json:"serverVersion"`
		GeneratedAt   string            `json:"generatedAt"`
		OnlineCount   int               `json:"onlineCount"`
		Online        []CompanionPlayer `json:"online"`
		Offline       []CompanionPlayer `json:"offline"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		// 插件正在写的时候可能读到半截。当「没有」处理是在撒谎，如实报错。
		return out, fmt.Errorf("解析快照失败：%w", err)
	}
	out.Have = true
	out.PluginVersion = doc.PluginVersion
	out.ServerVersion = doc.ServerVersion
	out.GeneratedAt = doc.GeneratedAt
	out.OnlineCount = doc.OnlineCount
	// ⚠ 集合字段必须先补空集合再 append。Go 的 nil slice 会序列化成 null 而不是 []，
	// 前端拿到 null 就得在每个地方写 `?? []`，迟早漏一个然后崩在 .map 上。
	out.Online = make([]CompanionPlayer, 0, len(doc.Online))
	for _, p := range doc.Online {
		out.Online = append(out.Online, enrich(p))
	}
	out.Offline = make([]CompanionPlayer, 0, len(doc.Offline))
	for _, p := range doc.Offline {
		out.Offline = append(out.Offline, enrich(p))
	}
	// 快照是插件每 2 秒写一次的。服务器在跑却超过 30 秒没更新，
	// 那就是插件卡了或者被别的东西干掉了 —— 宁可标旧也别装作是实时的。
	if t, err := time.Parse(time.RFC3339, doc.GeneratedAt); err == nil {
		out.Stale = time.Since(t) > 30*time.Second
	}
	return out, nil
}

var dimLabels = map[string]string{
	"minecraft:overworld":  "主世界",
	"minecraft:the_nether": "下界",
	"minecraft:the_end":    "末地",
	"NORMAL":               "主世界",
	"NETHER":               "下界",
	"THE_END":              "末地",
}

// enrich 把一个玩家身上的机器名翻成人话。
func enrich(p CompanionPlayer) CompanionPlayer {
	p.DimLabel = dimLabels[p.Dim]
	// 同上：集合一律补空集合，别让 JSON 里出现 null。
	if p.Effects == nil {
		p.Effects = []CompanionEffect{}
	}
	if p.Inventory == nil {
		p.Inventory = []CompanionItem{}
	}
	if p.EnderChest == nil {
		p.EnderChest = []CompanionItem{}
	}
	if p.DimLabel == "" {
		p.DimLabel = p.Dim
	}
	for i := range p.Inventory {
		enrichItem(&p.Inventory[i])
	}
	for i := range p.EnderChest {
		enrichItem(&p.EnderChest[i])
	}
	for _, v := range p.Equipment {
		if v != nil {
			enrichItem(v)
		}
	}
	for i := range p.Effects {
		e := &p.Effects[i]
		if ent, ok := materials.Effect(e.ID); ok && ent.ZH != "" {
			e.Label = ent.ZH
		} else {
			e.Label = e.ID
		}
		if e.Amplifier > 0 {
			// 放大器 0 = I 级，1 = II 级。玩家习惯罗马数字，别直接印 0/1。
			roman := []string{"I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"}
			n := e.Amplifier
			if n < len(roman) {
				e.AmplLabel = roman[n]
			} else {
				e.AmplLabel = fmt.Sprintf("%d", n+1)
			}
		}
	}
	return p
}

func enrichItem(it *CompanionItem) {
	it.Name = materials.StripColor(it.Name)
	it.Label = materials.Display(it.ID, it.Name)
	for i := range it.Lore {
		it.Lore[i] = materials.StripColor(it.Lore[i])
	}
	if len(it.Enchants) > 0 {
		it.EnchLabels = map[string]string{}
		for k, v := range it.Enchants {
			if ent, ok := materials.Enchant(k); ok && ent.ZH != "" {
				it.EnchLabels[k] = ent.ZH
			} else {
				it.EnchLabels[k] = k
			}
			_ = v
		}
	}
	if name := materials.IconName(it.ID); name != "" {
		it.Icon = "/api/materials/icon?id=" + it.ID
	}
}

// ── 写操作 ──

// CompanionResult 一条命令的执行结果
type CompanionResult struct {
	ID      string `json:"id"`
	Op      string `json:"op"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	DoneAt  int64  `json:"doneAt"`
}

// ErrCompanionNoBridge 插件还没工作过（桥接目录不存在）。
var ErrCompanionNoBridge = errors.New("插件还没产生过数据 —— 服务器至少成功启动过一次，并且那个插件真的加载了")

// ErrCompanionTimeout 下了命令但插件没回。
var ErrCompanionTimeout = errors.New("插件没有回应")

// CompanionCommand 下发一条写操作并等结果。
//
// 等结果而不是「发完就走」：面板上点「清空背包」，用户必须知道到底清没清成。
// 发完就显示「已执行」是在撒谎 —— 玩家可能根本不在线。
func (m *Manager) CompanionCommand(id, op, target string, args map[string]any) (CompanionResult, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return CompanionResult{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	sup := CompanionSupportOf(inst)
	if !sup.Supported {
		return CompanionResult{}, errors.New(sup.Reason)
	}
	if !inst.CompanionWanted() {
		return CompanionResult{}, errors.New("这个实例的实时数据是关着的，先打开开关再操作")
	}
	bridge := bridgeDir(inst)
	inbox := filepath.Join(bridge, "inbox")
	outbox := filepath.Join(bridge, "outbox")
	if fi, err := os.Stat(inbox); err != nil || !fi.IsDir() {
		return CompanionResult{}, ErrCompanionNoBridge
	}

	// ⚠ 归一化成小写再发出去。白名单是大小写不敏感的（用户手填 GIVE 不该被拒），
	// 但插件那边是精确匹配 —— 不归一化的话会出现「面板放行、插件拒绝」，
	// 用户看到一句莫名其妙的「不支持的操作」。
	op = strings.ToLower(strings.TrimSpace(op))

	// 文件名带时间戳（定宽，字典序 == 时间序），插件按名字排序执行，
	// 面板连点几下也不会乱序。
	cmdID := fmt.Sprintf("%019d", time.Now().UnixNano())
	body, err := json.Marshal(map[string]any{
		"id": cmdID, "op": op, "target": target, "args": args,
	})
	if err != nil {
		return CompanionResult{}, err
	}
	// 原子落盘：插件每 0.5 秒扫一次这个目录，半截文件会被它读成废命令。
	tmp := filepath.Join(inbox, "cmd-"+cmdID+".json.tmp")
	final := filepath.Join(inbox, "cmd-"+cmdID+".json")
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return CompanionResult{}, err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return CompanionResult{}, err
	}

	res := filepath.Join(outbox, "res-"+cmdID+".json")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(res); err == nil {
			var out CompanionResult
			if err := json.Unmarshal(b, &out); err != nil {
				return CompanionResult{}, fmt.Errorf("解析插件返回失败：%w", err)
			}
			return out, nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return CompanionResult{}, fmt.Errorf("%w：%s 已经发出去，但 10 秒内没等到结果（服务器还活着吗？）",
		ErrCompanionTimeout, op)
}

// ── 审计 ──
//
// 为什么单独记：这些操作会真的改玩家的东西。事后有人问「谁把我的钻石清了」，
// 只有一份记着「谁、什么时候、对谁、做了什么」的日志能回答。
// 不记就等于默认「面板干的坏事没人负责」。

// AuditEntry 一条操作记录
type AuditEntry struct {
	At     string `json:"at"`
	Who    string `json:"who"`
	Inst   string `json:"instance"`
	Op     string `json:"op"`
	Target string `json:"target"`
	Args   any    `json:"args,omitempty"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

func (m *Manager) AppendAudit(e AuditEntry) {
	if m.baseDir == "" {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(m.baseDir, "audit.log"),
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// AuditLog 读最近 count 条操作记录（新的在前）。
func (m *Manager) AuditLog(count int) ([]AuditEntry, error) {
	if m.baseDir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(m.baseDir, "audit.log"))
	if err != nil {
		if os.IsNotExist(err) {
			return []AuditEntry{}, nil
		}
		return nil, err
	}
	var all []AuditEntry
	for _, line := range bytes.Split(b, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e AuditEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue // 一行坏了就跳过，别让整页日志打不开
		}
		all = append(all, e)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].At > all[j].At })
	if count > 0 && len(all) > count {
		all = all[:count]
	}
	return all, nil
}

// CompanionOps 支持的操作，报错时能一并告诉用户「你能用哪些」。
var CompanionOps = []string{"give", "clear", "tp", "heal", "feed", "gamemode", "kill"}

// CompanionOpOK 白名单校验。别让调用方把任意 op 塞进插件。
func CompanionOpOK(op string) bool {
	for _, o := range CompanionOps {
		if strings.EqualFold(o, op) {
			return true
		}
	}
	return false
}
