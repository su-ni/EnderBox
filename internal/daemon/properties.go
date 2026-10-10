package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// PropertyRow server.properties 里的一项。
//
// Kind 决定界面用什么控件；Options 只对 enum 有意义；Min/Max 只对 int 有意义。
// Default 是官方默认值，InFile 说明这个键在文件里**真的存在**吗 ——
// 少了它界面就会把「我们填的默认值」当成「当前配置」展示，那是一种撒谎。
type PropertyRow struct {
	Key     string            `json:"key"`
	Value   string            `json:"value"`
	Kind    string            `json:"kind"` // string | int | bool | enum
	Label   string            `json:"label"`
	Group   string            `json:"group"` // 界面上的分组名，顺序按 spec 里首次出现的先后
	Hint    string            `json:"hint,omitempty"`
	Options []string          `json:"options,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"` // enum 值 → 中文名（只改展示，不改写进去的值）
	Min     *int              `json:"min,omitempty"`
	Max     *int              `json:"max,omitempty"`
	Default string            `json:"default,omitempty"`
	Live    bool              `json:"live,omitempty"` // 运行中能否走控制台立即生效
	Secret  bool              `json:"secret,omitempty"`
	InFile  bool              `json:"inFile"`
	Known   bool              `json:"known"` // false = 我们不认得，只按字符串处理
}

// iv 取指针用的小工具。范围是可选的，用 *int 而不是「0 表示没设」——
// 因为 0 本身就是合法边界（spawn-protection 允许 0）。
func iv(v int) *int { return &v }

// modeLabels 游戏模式的中文名。值本身必须保持英文写进文件，只翻给眼睛看。
var modeLabels = map[string]string{
	"survival":  "生存",
	"creative":  "创造",
	"adventure": "冒险",
	"spectator": "旁观",
}

var diffLabels = map[string]string{
	"peaceful": "和平",
	"easy":     "简单",
	"normal":   "普通",
	"hard":     "困难",
}

var levelTypeLabels = map[string]string{
	"minecraft:normal":               "默认（跟正式版一样的地形）",
	"minecraft:flat":                 "超平坦（一脚平，适合建筑测试）",
	"minecraft:large_biomes":         "大型生物群系",
	"minecraft:amplified":            "放大化（山脉高到离谱，很吃机器）",
	"minecraft:single_biome_surface": "单一生物群系",
}

var compressionLabels = map[string]string{
	"deflate": "deflate（默认，兼容最好）",
	"lz4":     "lz4（更快，占盘更多）",
	"none":    "none（不压缩，最占盘）",
}

// propSpecs 我们认得、并且值得做成界面的字段，顺序就是界面顺序。
//
// 只挑「个人服主真的会改」的：官方 Java 版有 75 个左右的键，全铺出来是灾难；
// 但也不能像以前那样只做 23 个 —— 缺的里面有一批是真会改的。
//
// ⚠ 不要加 spawn-monsters / spawn-animals / spawn-npcs：
// 这三个键在 1.21.2（快照 25w35a）已经从 server.properties 迁到游戏规则了，
// 写进去服务端只会当未知键忽略 —— 加进来就是给用户一个不起作用的开关。
var propSpecs = []PropertyRow{
	// ── 网络与身份 ──────────────────────────────
	{Key: "server-ip", Group: "网络与身份", Kind: "string", Label: "绑定的网卡地址", Hint: "留空 = 监听所有网卡，家用一般都留空", Value: ""},
	{Key: "server-port", Group: "网络与身份", Kind: "int", Label: "监听端口", Hint: "服务端真正监听的端口", Value: "25565", Min: iv(1), Max: iv(65534)},
	{Key: "motd", Group: "网络与身份", Kind: "string", Label: "服务器标语", Hint: "服务器列表里显示的那句话", Value: "A Minecraft Server"},
	{Key: "online-mode", Group: "网络与身份", Kind: "bool", Label: "正版验证", Hint: "关掉才允许离线客户端进来", Value: "true"},
	{Key: "max-players", Group: "网络与身份", Kind: "int", Label: "最大玩家数", Hint: "注意这是「同时在线」上限，不是白名单人数", Value: "20", Min: iv(0)},
	{Key: "network-compression-threshold", Group: "网络与身份", Kind: "int", Label: "网络压缩阈值", Hint: "-1 = 不压缩；填 0 表示全都压。不懂别动", Value: "256", Min: iv(-1)},
	{Key: "prevent-proxy-connections", Group: "网络与身份", Kind: "bool", Label: "拒绝代理连接", Hint: "开了之后同一 IP 的不同账号很难一起进", Value: "false"},
	{Key: "enforce-secure-profile", Group: "网络与身份", Kind: "bool", Label: "强制安全聊天", Hint: "只有正版服有意义，离线服开了反而进不去", Value: "true"},
	{Key: "use-native-transport", Group: "网络与身份", Kind: "bool", Label: "用系统原生网络栈", Hint: "Linux 上能省点 CPU，Windows 上没区别", Value: "true"},

	// ── 玩法 ───────────────────────────────────
	{Key: "difficulty", Group: "玩法", Kind: "enum", Label: "游戏难度", Options: []string{"peaceful", "easy", "normal", "hard"}, Labels: diffLabels, Value: "easy", Live: true},
	{Key: "gamemode", Group: "玩法", Kind: "enum", Label: "默认游戏模式", Options: []string{"survival", "creative", "adventure", "spectator"}, Labels: modeLabels, Value: "survival", Hint: "新玩家进来时的模式", Live: true},
	{Key: "force-gamemode", Group: "玩法", Kind: "bool", Label: "每次登录都重置为默认模式", Hint: "开了之后玩家自己切模式也会被掰回来", Value: "false"},
	{Key: "pvp", Group: "玩法", Kind: "bool", Label: "玩家互相伤害", Value: "true", Live: true},
	{Key: "hardcore", Group: "玩法", Kind: "bool", Label: "极限模式", Hint: "死了就变旁观，且改不回来", Value: "false"},
	{Key: "allow-flight", Group: "玩法", Kind: "bool", Label: "允许飞行", Hint: "装了飞行类模组必须开", Value: "false"},
	{Key: "allow-nether", Group: "玩法", Kind: "bool", Label: "允许去下界", Hint: "关掉之后下界传送门直接不工作", Value: "true"},
	{Key: "enable-command-block", Group: "玩法", Kind: "bool", Label: "允许命令方块", Hint: "给地图作者用的，普通生存服关着更安全", Value: "false"},
	{Key: "generate-structures", Group: "玩法", Kind: "bool", Label: "生成结构", Hint: "村庄、要塞、废弃矿井这些。关掉只影响新生成的地图", Value: "true"},

	// ── 世界生成 ────────────────────────────────
	{Key: "level-name", Group: "世界生成", Kind: "string", Label: "世界文件夹名", Hint: "存档文件夹名，改名等于换世界", Value: "world"},
	{Key: "level-seed", Group: "世界生成", Kind: "string", Label: "世界种子", Hint: "留空 = 随机。改它只对还没生成的世界有效", Value: ""},
	{
		Key: "level-type", Group: "世界生成", Kind: "enum", Label: "世界类型",
		Options: []string{"minecraft:normal", "minecraft:flat", "minecraft:large_biomes", "minecraft:amplified", "minecraft:single_biome_surface"},
		Labels:  levelTypeLabels, Value: "minecraft:normal",
		Hint: "⚠ 只对「还没生成的世界」有效 —— 存档已经在了，改这个等于换一个新世界",
	},
	{Key: "generator-settings", Group: "世界生成", Kind: "string", Label: "生成器参数", Hint: "超平坦调的 JSON，一般不手写。留空即可", Value: "{}"},
	{Key: "spawn-protection", Group: "世界生成", Kind: "int", Label: "出生点保护半径", Hint: "半径内非 OP 不能破坏方块，0 = 关闭保护", Value: "16", Min: iv(0)},
	{Key: "view-distance", Group: "世界生成", Kind: "int", Label: "视距", Hint: "服务器发给客户端的区块半径。调小最省性能，8 以下会明显糊", Value: "10", Min: iv(3), Max: iv(32)},
	{Key: "simulation-distance", Group: "世界生成", Kind: "int", Label: "模拟距离", Hint: "怪物、掉落物在这个半径内才真的在动。比视距更吃 CPU", Value: "10", Min: iv(3), Max: iv(32)},
	{Key: "max-world-size", Group: "世界生成", Kind: "int", Label: "世界边界半径", Hint: "以出生点为圆心的最大半径，防止存档无限膨胀", Value: "29999984", Min: iv(1), Max: iv(29999984)},

	// ── 玩家管理 ────────────────────────────────
	{Key: "white-list", Group: "玩家管理", Kind: "bool", Label: "启用白名单", Value: "false", Live: true},
	{Key: "enforce-whitelist", Group: "玩家管理", Kind: "bool", Label: "白名单强制踢出", Hint: "把已在线的非白名单玩家踢掉", Value: "false"},
	{Key: "player-idle-timeout", Group: "玩家管理", Kind: "int", Label: "挂机踢出（分钟）", Hint: "0 = 不踢", Value: "0", Min: iv(0)},
	{Key: "op-permission-level", Group: "玩家管理", Kind: "int", Label: "OP 默认权限等级", Hint: "1 = 只改出生点；4 = 全部指令（给 /op 就是给这个等级）", Value: "4", Min: iv(0), Max: iv(4)},
	{Key: "function-permission-level", Group: "玩家管理", Kind: "int", Label: "函数/命令方块权限等级", Hint: "数据包和命令方块执行指令时的等级，默认 2", Value: "2", Min: iv(1), Max: iv(4)},
	{Key: "hide-online-players", Group: "玩家管理", Kind: "bool", Label: "隐藏在线玩家列表", Hint: "开着的话第三方工具也查不到谁在线", Value: "false"},

	// ── 性能与稳定 ──────────────────────────────
	{
		Key: "max-tick-time", Group: "性能与稳定", Kind: "int", Label: "单帧超时自动重启（毫秒）",
		Hint: "一个 tick 卡超过这个时间服务端就自杀退出，-1 = 永不自杀。家里机器卡顿时最讨厌的一个键", Value: "60000", Min: iv(-1),
	},
	{Key: "entity-broadcast-range-percentage", Group: "性能与稳定", Kind: "int", Label: "实体同步范围（%）", Hint: "调小能显著省带宽和 CPU，代价是远处实体看着卡顿", Value: "100", Min: iv(10), Max: iv(1000)},
	{Key: "rate-limit", Group: "性能与稳定", Kind: "int", Label: "同 IP 每秒连接上限", Hint: "0 = 不限。被刷连接时调成 5 左右", Value: "0", Min: iv(0)},
	{Key: "pause-when-empty-seconds", Group: "性能与稳定", Kind: "int", Label: "空服暂停（秒）", Hint: "没人时暂停世界以省 CPU；0 = 不暂停。注意会让红石/刷怪机停摆", Value: "60", Min: iv(0)},
	{Key: "sync-chunk-writes", Group: "性能与稳定", Kind: "bool", Label: "区块写入同步落盘", Hint: "关掉能快一些，但断电时更容易丢存档", Value: "true"},
	{Key: "region-file-compression", Group: "性能与稳定", Kind: "enum", Label: "区块文件压缩方式", Options: []string{"deflate", "lz4", "none"}, Labels: compressionLabels, Value: "deflate", Hint: "改了只影响之后新写的区块"},
	{Key: "enable-jmx-monitoring", Group: "性能与稳定", Kind: "bool", Label: "开启 JMX 监控", Hint: "给外部监控工具读性能数据用的，没需求别开", Value: "false"},

	// ── 可见性与查询 ────────────────────────────
	{Key: "enable-status", Group: "可见性与查询", Kind: "bool", Label: "允许查询服务器状态", Hint: "关掉后服务器列表里会显示「无法连接」，但游戏内其实能进", Value: "true"},
	{Key: "enable-query", Group: "可见性与查询", Kind: "bool", Label: "开启 Query 协议", Hint: "给 dynmap、外部网站这类工具读在线人数用的", Value: "false"},
	{Key: "query.port", Group: "可见性与查询", Kind: "int", Label: "Query 端口", Value: "25565", Min: iv(1), Max: iv(65534)},
	{Key: "log-ips", Group: "可见性与查询", Kind: "bool", Label: "日志里记录玩家 IP", Hint: "关掉之后日志里只留名字，出事不好查", Value: "true"},

	// ── 资源包 ──────────────────────────────────
	{Key: "resource-pack", Group: "资源包", Kind: "string", Label: "资源包下载地址", Hint: "必须是直链（http/https 或 file），填网页地址客户端下不了", Value: ""},
	{Key: "resource-pack-sha1", Group: "资源包", Kind: "string", Label: "资源包 SHA-1", Hint: "填上能防止下载到半截的包，不填也能跑", Value: ""},
	{Key: "resource-pack-prompt", Group: "资源包", Kind: "string", Label: "资源包提示文案", Hint: "客户端弹「是否下载」时显示的那句话", Value: ""},
	{Key: "require-resource-pack", Group: "资源包", Kind: "bool", Label: "强制使用资源包", Hint: "拒绝或下载失败就不让进服。开了就没法裸客户端进", Value: "false"},

	// ── 远程控制 ────────────────────────────────
	{Key: "enable-rcon", Group: "远程控制", Kind: "bool", Label: "开启 RCON", Hint: "给外部工具用的远程控制通道，开了必须设密码", Value: "false"},
	{Key: "rcon.port", Group: "远程控制", Kind: "int", Label: "RCON 端口", Value: "25575", Min: iv(1), Max: iv(65534)},
	{Key: "rcon.password", Group: "远程控制", Kind: "string", Label: "RCON 密码", Hint: "开了 RCON 必须设，且别用弱密码", Value: "", Secret: true},
	{Key: "broadcast-console-to-ops", Group: "远程控制", Kind: "bool", Label: "控制台输出也发给 OP", Value: "true"},
	{Key: "broadcast-rcon-to-ops", Group: "远程控制", Kind: "bool", Label: "RCON 输出也发给 OP", Value: "true"},
}

var propSpecIndex = func() map[string]int {
	m := make(map[string]int, len(propSpecs))
	for i, s := range propSpecs {
		m[s.Key] = i
	}
	return m
}()

// propGroupDesc 分组的说明。放在后端是为了让「字段名 / 说明 / 分组说明」都只有一个来源 ——
// 前端自己编一套文案，改一次就要改两处。
var propGroupDesc = map[string]string{
	"网络与身份":  "别人怎么连进来、以什么身份进来",
	"玩法":     "游戏规则本身 —— 难度、模式、允许什么",
	"世界生成":   "存档长什么样、跑多远、机器扛多少",
	"玩家管理":   "谁能进、进来之后能干什么",
	"性能与稳定":  "卡不卡、崩不崩、扛不扛得住",
	"可见性与查询": "服务器列表和第三方工具能查到什么",
	"资源包":    "进服时带给客户端的材质包",
	"远程控制":   "给外部工具用的通道，没需求别开",
}

// GroupDescs 给前端的分组说明表
func GroupDescs() map[string]string {
	out := make(map[string]string, len(propGroupDesc))
	for k, v := range propGroupDesc {
		out[k] = v
	}
	return out
}

// liveCommand 运行中能直接生效的键 → 控制台指令。
//
// 只有这四个：它们在原版指令里真的存在，而且语义与 properties 里的同名键一致。
// max-players、view-distance、motd 这些**没有**对应指令 —— 硬给一个「立即生效」
// 按钮就是骗人（点了没反应，或者更糟：只改了文件，重启后又被服务端内存里的旧值盖回去）。
// 所以宁可没有。
func liveCommand(key, value string) (string, bool) {
	switch key {
	case "difficulty":
		return "difficulty " + value, true
	case "gamemode":
		return "defaultgamemode " + value, true
	case "pvp":
		return "gamerule pvp " + value, true
	case "white-list":
		switch value {
		case "true":
			return "whitelist on", true
		case "false":
			return "whitelist off", true
		}
	}
	return "", false
}

// LiveCommand 给 api 层用的包装。返回指令原文和「这个键能不能立即生效」。
func LiveCommand(key, value string) (string, bool) { return liveCommand(key, value) }

func (m *Manager) propsPath(id string) (string, Instance, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", Instance{}, fmt.Errorf("实例不存在: %s", id)
	}
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		return "", inst, err
	}
	return filepath.Join(inst.Dir, "server.properties"), inst, nil
}

// readPropFile 按行保留原始内容：服务端自己写的注释和顺序都要留着，
// 我们只做「改某几行」这件事，不做重新生成 —— 重新生成会丢掉别的插件加进去的键。
func readPropFile(path string) (lines []string, present map[string]string) {
	present = map[string]string{}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, present
	}
	lines = strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	for _, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
			continue
		}
		i := strings.Index(t, "=")
		if i <= 0 {
			continue
		}
		present[strings.TrimSpace(t[:i])] = strings.TrimSpace(t[i+1:])
	}
	return lines, present
}

// normalizeLevelType 把老写法补上命名空间。
//
// 1.19 之前 level-type 写的是 `normal` / `flat` / `large_biomes` 这些裸值，
// 现在是 `minecraft:normal`。两种服务端都认，但如果我们只把带命名空间的写法放进
// Options，读到一个老档就会「值不在选项里」——<select> 会静默显示第一项，
// 用户看到的配置和文件里的根本不是一回事。所以读到裸值先归一化。
func normalizeLevelType(v string) string {
	t := strings.TrimSpace(v)
	if t == "" {
		return t
	}
	if strings.Contains(t, ":") {
		return t
	}
	switch strings.ToLower(t) {
	case "normal", "flat", "large_biomes", "amplified", "single_biome_surface":
		return "minecraft:" + strings.ToLower(t)
	}
	return t
}

// ReadProperties 返回可编辑字段列表。
// 文件还不存在时也返回完整默认值 —— 这样用户能在第一次开服前就把配置定下来。
func (m *Manager) ReadProperties(id string) ([]PropertyRow, error) {
	path, _, err := m.propsPath(id)
	if err != nil {
		return nil, err
	}
	_, present := readPropFile(path)

	out := make([]PropertyRow, 0, len(propSpecs))
	seen := map[string]bool{}
	for _, s := range propSpecs {
		row := s
		row.Known = true
		row.Default = s.Value
		if v, ok := present[s.Key]; ok {
			if s.Key == "level-type" {
				v = normalizeLevelType(v)
			}
			row.Value = v
			row.InFile = true
			seen[s.Key] = true
		}
		// 枚举兜底：文件里是个我们没预料的取值时，把它补进选项里。
		// 不补的话 <select> 找不到匹配项会显示成第一项 —— 界面撒谎，用户按保存就把
		// 一个他从没选过的值写了进去。这是最隐蔽的一类配置事故。
		if row.Kind == "enum" && row.InFile {
			found := false
			for _, o := range row.Options {
				if strings.EqualFold(o, row.Value) {
					found = true
					break
				}
			}
			if !found {
				row.Options = append(append([]string{}, row.Options...), row.Value)
				row.Labels = withLabel(row.Labels, row.Value, "文件里的值（不在预设内，原样保留）")
			}
		}
		out = append(out, row)
	}

	// 文件里存在但我们不认得的键，附在末尾只读展示 ——
	// 悄悄藏起来会让用户以为「这文件里就这么点东西」。
	var extra []string
	for k := range present {
		if _, known := propSpecIndex[k]; !known {
			extra = append(extra, k)
		}
	}
	for _, k := range sortedStrings(extra) {
		out = append(out, PropertyRow{
			Key: k, Value: present[k], Kind: "string", Group: "其它（不认得）",
			Known: false, InFile: true,
		})
	}
	return out, nil
}

// withLabel 复制一份 Labels 再加一项 —— 直接改传进来的 map 会污染共享的包级变量。
func withLabel(m map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(m)+1)
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func sortedStrings(in []string) []string {
	out := make([]string, len(in))
	copy(out, in)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// rangeText 把范围说成人话，错误信息里要用。
func rangeText(spec PropertyRow) string {
	switch {
	case spec.Min != nil && spec.Max != nil:
		return fmt.Sprintf("%d ~ %d", *spec.Min, *spec.Max)
	case spec.Min != nil:
		return fmt.Sprintf("不能小于 %d", *spec.Min)
	case spec.Max != nil:
		return fmt.Sprintf("不能大于 %d", *spec.Max)
	}
	return ""
}

// validateProp 值合法性。宁可在这里挡住，也别写进去让服务端起不来 ——
// 服务端读不懂会整份配置回退成默认值，而且不给任何提示。
func validateProp(key, val string) error {
	if strings.ContainsAny(val, "\r\n") {
		return fmt.Errorf("%s 的值不能包含换行", key)
	}

	i, known := propSpecIndex[key]
	if !known {
		// 未知键仍然允许写（插件可能自己加键），上面那道换行防护就够了
		return nil
	}
	spec := propSpecs[i]

	switch spec.Kind {
	case "int":
		t := strings.TrimSpace(val)
		if t == "" {
			return fmt.Errorf("%s 需要填一个整数", spec.Label)
		}
		n, err := strconv.Atoi(t)
		if err != nil {
			return fmt.Errorf("%s 需要填整数，收到「%s」", spec.Label, val)
		}
		// 范围校验：以前只验「是不是整数」，视距填 999 也照收，
		// 结果服务端起不来 —— 这类错误必须在写盘前挡下来。
		if spec.Min != nil && n < *spec.Min {
			return fmt.Errorf("%s 应该是 %s，收到 %d", spec.Label, rangeText(spec), n)
		}
		if spec.Max != nil && n > *spec.Max {
			return fmt.Errorf("%s 应该是 %s，收到 %d", spec.Label, rangeText(spec), n)
		}
	case "bool":
		v := strings.ToLower(strings.TrimSpace(val))
		if v != "true" && v != "false" {
			return fmt.Errorf("%s 只能是 true 或 false，收到「%s」", spec.Label, val)
		}
	case "enum":
		ok := false
		for _, o := range spec.Options {
			if strings.EqualFold(o, strings.TrimSpace(val)) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%s 只能是 %s", spec.Label, strings.Join(spec.Options, " / "))
		}
	}
	return nil
}

// WritePropertiesIfAbsent 只写「文件里还没有」的键。
// 用在「刚下载完服务端、要按实例配置生成一份最小配置」这个场景 ——
// 已经存在的键一个字都不能动，那是用户自己调过的。
func (m *Manager) WritePropertiesIfAbsent(id string, kv map[string]string) error {
	path, _, err := m.propsPath(id)
	if err != nil {
		return err
	}
	_, present := readPropFile(path)
	upd := map[string]string{}
	for k, v := range kv {
		if _, ok := present[k]; !ok {
			upd[k] = v
		}
	}
	if len(upd) == 0 {
		return nil
	}
	return m.WriteProperties(id, upd)
}

// WriteProperties 只改传进来的这些键，其余行原样保留。
// 文件里没有的键追加到末尾，并注明是 EnderBox 补的。
func (m *Manager) WriteProperties(id string, updates map[string]string) error {
	if len(updates) == 0 {
		return nil
	}
	path, _, err := m.propsPath(id)
	if err != nil {
		return err
	}
	// 键也要校验：未知键的 key 里带 = 或换行会把文件写坏（例如把 "a=b" 当 key）。
	for k, v := range updates {
		if err := validatePropKey(k); err != nil {
			return err
		}
		if err := validateProp(k, v); err != nil {
			return err
		}
	}

	lines, _ := readPropFile(path)
	done := map[string]bool{}
	for i, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "!") {
			continue
		}
		j := strings.Index(t, "=")
		if j <= 0 {
			continue
		}
		key := strings.TrimSpace(t[:j])
		v, want := updates[key]
		if !want {
			continue
		}
		lines[i] = key + "=" + v
		done[key] = true
	}

	var missing []string
	for k := range updates {
		if !done[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		// 文件本来就不存在时别在开头留一个空行 —— 这是服务端会读的配置文件，不是草稿
		if len(strings.TrimSpace(strings.Join(lines, ""))) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, "# 以下由 EnderBox 补充")
		for _, k := range sortedStrings(missing) {
			lines = append(lines, k+"="+updates[k])
		}
	}

	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	return os.WriteFile(path, []byte(out), 0o644)
}

// validatePropKey 键名防护。允许自定义键，但不允许能把文件写坏的字符。
//
// ⚠ 空白字符也要挡，不只是首尾：Java 的 Properties 解析把 `=`、`:`、
// 以及**任意空白**都当键值分隔符，所以 "a b=x" 会被读成 key=a、value=b=x。
// 我们写得出来，服务端读出来却是另一个键 —— 这种错谁都不好查。
func validatePropKey(k string) error {
	if strings.TrimSpace(k) == "" {
		return fmt.Errorf("配置键不能为空")
	}
	if strings.ContainsAny(k, "=:\r\n") {
		return fmt.Errorf("配置键「%s」不能包含 = 、: 或换行", k)
	}
	if strings.IndexFunc(k, unicode.IsSpace) >= 0 {
		return fmt.Errorf("配置键「%s」不能包含空格或制表符（服务端会把分隔符读歪）", k)
	}
	return nil
}
