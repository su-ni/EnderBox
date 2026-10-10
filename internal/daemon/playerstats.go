package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ── 玩家档案（只读）──
//
// 服务端除了那四份名单，还会在**世界目录**里留下每个玩家的痕迹：
//
//	<世界>/stats/<uuid>.json      统计数据（游玩时长、死亡、击杀……）
//	<世界>/playerdata/<uuid>.dat  玩家数据，**mtime 就是最后一次进服的时间**
//
// 这两样是纯文件，读它们不需要服务端插件 —— 正好补上「详情页只有名单状态、
// 没有一句关于这个人玩得怎么样」的空白。
//
// ⚠ 只在**世界存过档**之后才有：刚建的实例、从没进过服的玩家，这两个路径都是空的。
// 那时如实说「还没有数据」，不要显示成 0 小时 0 死亡 —— 那是在编。
//
// 世界目录名由 server.properties 的 level-name 决定（默认 "world"），
// 不能写死：改过名字的实例会读到一个不存在的目录，然后安静地报「没数据」。

// PlayerProfile 一个玩家的只读档案。
type PlayerProfile struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
	// HasStats server 写过 stats 文件。false 时下面几个数字一律无意义，别显示。
	HasStats bool `json:"hasStats"`
	// PlayTicks 总游玩时间，单位是 **tick**（20 tick = 1 秒）。
	// 前端换算成小时。用 tick 传是因为换算是展示层的事，后端不该替它选单位。
	PlayTicks int64 `json:"playTicks"`
	Deaths    int64 `json:"deaths"`
	// PlayerKills 击杀玩家数 / MobKills 击杀生物数。分开给：一个说明他好斗，
	// 另一个说明他肝。
	PlayerKills int64 `json:"playerKills"`
	MobKills    int64 `json:"mobKills"`
	// LastSeen 世界目录里那份 playerdata 的写入时间（RFC3339）。空 = 没有档案。
	LastSeen string `json:"lastSeen"`
	// Note 拿不到数据时说清是为什么。空 = 一切正常，不用解释。
	Note string `json:"note"`
	// WorldName 实际用的世界目录名。界面上偶尔要用（排障时能对上）。
	WorldName string `json:"worldName"`
}

// RosterExtra 名册每一行的补充信息。设计稿的列表行要显示「游戏时长」和「在线 · 27 ms」，
// 这两个数分别来自两个**不同的地方**，所以拆成一张独立的表，而不是塞进 PlayerEntry：
//
//	PlayTicks / LastSeen ← 世界目录下的 stats / playerdata 文件（**离线也读得到**）
//	Ping                ← companion 插件上报（**只有服在跑 + 装了插件**才有）
//
// 三者都可能缺失，用 omitempty 让「没有」和「是 0」区分开 ——
// playTicks 是 0 就表示没档案（不是「他玩了 0 秒」），界面显示「—」。
type RosterExtra struct {
	// PlayTicks 累计游玩刻数（20 tick = 1 秒）。换算是展示层的事，后端不替它选单位。
	PlayTicks int64 `json:"playTicks,omitempty"`
	// LastSeen playerdata 的写入时间（RFC3339）。
	LastSeen string `json:"lastSeen,omitempty"`
	// Ping 延迟毫秒。0 = 不在线（原版服务端不给离线玩家延迟）或没装 companion 插件。
	Ping int `json:"ping,omitempty"`
}

// rosterWant 名册里一个人的「名字 + 已知 UUID」。UUID 可能为空 ——
// 正版服的在线玩家只有名字，面板算不出他的 UUID。
type rosterWant struct {
	Name string
	UUID string
}

// rosterExtraLimit 一次最多读多少人的档案文件。
// 白名单可能有几百人，无上限地逐个读会把一次 /players 请求拖成秒级。
// 宁可少给几行数据，也不能让整个列表卡住 —— 超出的那些行显示「—」。
const rosterExtraLimit = 300

// RosterExtras 批量收集名册每行的补充信息。
//
// 为什么批量而不是逐个调 PlayerProfile：名册一次要渲染几十上百行，
// 逐个调会在一次 HTTP 请求里读上百个文件。这里每人只读一个 stats 文件 + 一次 Stat。
//
// 返回的键是**小写玩家名**（名册合成总表就是按小写名去重的，规则必须一致）。
func (m *Manager) RosterExtras(id string, want []rosterWant) map[string]RosterExtra {
	out := map[string]RosterExtra{}
	inst, ok := m.st.Get(id)
	if !ok {
		return out
	}
	// 正版服里没有 UUID 的人读不出档案（文件以 UUID 命名）—— 那就跳过，不编一个。
	onlineMode, _ := m.onlineModeOf(id)
	world := filepath.Join(inst.Dir, m.worldDirName(id))

	seen := make(map[string]bool, len(want))
	n := 0
	for _, w := range want {
		name := strings.TrimSpace(w.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		if n >= rosterExtraLimit {
			break
		}
		n++

		// 先占位：即使这个人读不出档案也要有条目，否则 companion 上报的延迟没处可填。
		out[key] = RosterExtra{}

		uuid := w.UUID
		if uuid == "" && !onlineMode {
			uuid = offlineUUID(name)
		}
		if uuid == "" {
			continue
		}

		var e RosterExtra
		if st, err := readStatsFile(filepath.Join(world, "stats", uuid+".json")); err == nil && st != nil {
			e.PlayTicks = st.PlayTicks
		}
		if fi, err := os.Stat(filepath.Join(world, "playerdata", uuid+".dat")); err == nil {
			e.LastSeen = fi.ModTime().Format(time.RFC3339)
		}
		out[key] = e
	}
	return out
}

// worldDirName 世界目录名。默认 "world"，可被 level-name 改掉。
func (m *Manager) worldDirName(id string) string {
	if rows, err := m.ReadProperties(id); err == nil {
		for _, row := range rows {
			if row.Key == "level-name" {
				if v := strings.TrimSpace(row.Value); v != "" {
					return v
				}
			}
		}
	}
	return "world"
}

// PlayerProfile 读一个玩家的只读档案。
//
// uuid 可能为空（正版服里在线玩家只有名字）。这时**不编一个** ——
// 拿不到就说拿不到，否则用户会拿一个错 UUID 去别处对，然后发现对不上。
func (m *Manager) PlayerProfile(id, name, uuid string) (PlayerProfile, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return PlayerProfile{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	name = strings.TrimSpace(name)
	p := PlayerProfile{Name: name, UUID: uuid, WorldName: m.worldDirName(id)}

	if p.UUID == "" {
		// 离线服的 UUID 是按名字算的；正版服算不出来。
		if onlineMode, _ := m.onlineModeOf(id); !onlineMode {
			p.UUID = offlineUUID(name)
		}
	}
	if p.UUID == "" {
		p.Note = "这是正版验证服，面板拿不到这个玩家的 UUID —— 详情数据都在以 UUID 命名的文件里，所以读不出来。"
		return p, nil
	}

	world := filepath.Join(inst.Dir, p.WorldName)
	statsPath := filepath.Join(world, "stats", p.UUID+".json")
	dataPath := filepath.Join(world, "playerdata", p.UUID+".dat")

	if st, err := readStatsFile(statsPath); err != nil {
		return p, err
	} else if st != nil {
		p.HasStats = true
		p.PlayTicks = st.PlayTicks
		p.Deaths = st.Deaths
		p.PlayerKills = st.PlayerKills
		p.MobKills = st.MobKills
	}

	if fi, err := os.Stat(dataPath); err == nil {
		p.LastSeen = fi.ModTime().Format(time.RFC3339)
	} else if !os.IsNotExist(err) {
		return p, fmt.Errorf("读取 %s 失败：%w", filepath.Base(dataPath), err)
	}

	if !p.HasStats && p.LastSeen == "" {
		p.Note = fmt.Sprintf("还没有这个玩家的存档 —— %s/ 下既没有 stats 也没有 playerdata。"+
			"他还没进过服，或者世界还没存过档。", p.WorldName)
	}
	return p, nil
}

// mcStats stats 文件里我们要的那几个数。
type mcStats struct {
	PlayTicks   int64
	Deaths      int64
	PlayerKills int64
	MobKills    int64
}

// readStatsFile 读 stats/<uuid>.json。文件不存在返回 (nil, nil) —— 「没有档案」
// 不是错误：新实例、没进过服的玩家都属于这种情况，报错会让详情页整个红掉。
func readStatsFile(path string) (*mcStats, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取 %s 失败：%w", filepath.Base(path), err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil
	}
	// 结构是 {"stats": {"minecraft:custom": {...}, ...}, "DataVersion": n}
	// 只解我们认得的这一层，别的一律忽略 —— 统计项版本间会增删，
	// 定义成强类型结构会在下一个版本直接解析失败。
	var doc struct {
		Stats map[string]map[string]int64 `json:"stats"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		// 服务端正在写这个文件时可能读到半截。这时报错会打断整页，
		// 但把它当「没有档案」又是在撒谎 —— 如实报告。
		return nil, fmt.Errorf("解析 %s 失败：%w", filepath.Base(path), err)
	}
	custom := doc.Stats["minecraft:custom"]
	if custom == nil {
		return nil, nil
	}
	// play_time 是 1.17 之后的名字；之前叫 play_one_minute，**单位同样是 tick**
	// （名字是历史遗留，别照字面当分钟）。
	play := custom["minecraft:play_time"]
	if play == 0 {
		play = custom["minecraft:play_one_minute"]
	}
	return &mcStats{
		PlayTicks:   play,
		Deaths:      custom["minecraft:deaths"],
		PlayerKills: custom["minecraft:player_kills"],
		MobKills:    custom["minecraft:mob_kills"],
	}, nil
}

// onlineModeOf 从 server.properties 读 online-mode。读不到时保守返回 true（正版服），
// 这样不会给在线玩家编 UUID。
func (m *Manager) onlineModeOf(id string) (bool, error) {
	if rows, err := m.ReadProperties(id); err == nil {
		for _, row := range rows {
			if row.Key == "online-mode" {
				return !strings.EqualFold(strings.TrimSpace(row.Value), "false"), nil
			}
		}
	}
	return true, nil
}
