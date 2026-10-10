package daemon

import (
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ErrInstanceNotRunning 想做一件必须服务器在跑才能做的事（目前只有「踢人」）。
// 单独定义是为了让上层能给出 409 而不是 400 ——
// 「你现在不该这么做」和「你填错了」是两回事，混成一个码界面就说不清。
var ErrInstanceNotRunning = errors.New("服务器没在运行")

// ErrBanNeedsStop 想限时封禁，但服务器在跑 —— 服务端的 ban 命令不支持时长。
// 定义成独立错误，上层才能给出「先停服」这种可操作的提示，
// 而不是一句「操作失败」让用户去猜。
var ErrBanNeedsStop = errors.New("限时封禁需要先停服")

// ── 玩家管理 ──
//
// 数据全部来自服务端自己写的四个原生文件：
//
//	whitelist.json / ops.json / banned-players.json / banned-ips.json
//
// **我们不做服务端插件**，也不去猜服务端的内存状态 —— 读文件就够。
//
// 写操作分两条轨，按服务端在不在跑来选：
//   · 在跑 → 发控制台命令（`whitelist add` / `op` / `ban` / `kick`）。
//     服务端会把内存和文件一起更新，比我们直接改文件权威 ——
//     改文件的话，它内存里还是旧的，名单当场不生效。
//   · 没在跑 → 直接改文件。这时候没有内存状态要同步，文件就是唯一真相，
//     服务端下次启动会读它。
//
// 两条路各自都是正确的，不需要第三套逻辑去「对齐」它们。

// PlayerEntry 白名单里的一条。字段名照抄服务端写的 whitelist.json ——
// 两边对称才能原样读写，字段名不同会把服务端的数据读坏。
type PlayerEntry struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// OpEntry ops.json 里的一条
type OpEntry struct {
	UUID                string `json:"uuid"`
	Name                string `json:"name"`
	Level               int    `json:"level"`
	BypassesPlayerLimit bool   `json:"bypassesPlayerLimit"`
}

// BanEntry banned-players.json 里的一条
type BanEntry struct {
	UUID    string `json:"uuid"`
	Name    string `json:"name"`
	Created string `json:"created"`
	Source  string `json:"source"`
	// Expires "forever" 或者一个时间串。界面上要能把「永久」和「到点解封」分开说。
	Expires string `json:"expires"`
	Reason  string `json:"reason"`
}

// IPBanEntry banned-ips.json 里的一条
type IPBanEntry struct {
	IP      string `json:"ip"`
	Created string `json:"created"`
	Source  string `json:"source"`
	Expires string `json:"expires"`
	Reason  string `json:"reason"`
}

// 名单文件名。服务端认死了这几个名字，不能改。
const (
	fileWhitelist = "whitelist.json"
	fileOps       = "ops.json"
	fileBanned    = "banned-players.json"
	fileBannedIPs = "banned-ips.json"
)

// PlayerRoster 一次取全的四份名单 + 在线名单。
type PlayerRoster struct {
	// Online 也是 PlayerEntry，不只是名字 ——
	// 界面要把在线/白名单/OP/封禁合成**一张总表**，在线的人也得有 UUID 才能和名单里
	// 的同一行对上。离线服（关掉正版验证）算得出，正版服算不出，那时 UUID 留空，
	// 界面按「没记下 UUID」显示，不编一个。
	Online    []PlayerEntry `json:"online"`
	Whitelist []PlayerEntry `json:"whitelist"`
	Ops       []OpEntry     `json:"ops"`
	Banned    []BanEntry    `json:"banned"`
	BannedIPs []IPBanEntry  `json:"bannedIps"`
	// WhitelistEnabled 来自 server.properties 的 white-list。
	// 单独给出来是因为：白名单没开的时候往里加人，加了也不生效 ——
	// 界面得能提前把这句话说出来，而不是让用户加完人还在门口站着。
	WhitelistEnabled bool `json:"whitelistEnabled"`
	// OnlineMode 来自 server.properties 的 online-mode。false = 离线服，
	// 这时玩家 UUID 是按名字算出来的（见 offlineUUID），界面才敢给在线的人补 UUID。
	OnlineMode bool `json:"onlineMode"`
	// Running 决定写操作走哪条轨，也决定「踢人」能不能点。
	Running bool `json:"running"`
	// Extra 每行的补充信息（累计时长 / 最后在线 / 延迟），键是**小写玩家名**。
	// 单独一张表而不是塞进 PlayerEntry：PlayerEntry 是名单文件的**原样映射**，
	// 而时长来自另一个文件、延迟来自插件，混进去会让「名单文件里到底有什么」变得含糊。
	// 保证非 nil（nil map 会变成 JSON null）。
	Extra map[string]RosterExtra `json:"extra"`
}

// offlineUUID 算离线模式下的玩家 UUID。
//
// 服务端的算法是 `UUID.nameUUIDFromBytes(("OfflinePlayer:" + 名字).getBytes(UTF_8))`，
// 它内部是 **MD5 版本 3 UUID**，不是「md5 之后直接插连字符」：
//
//	① 取 "OfflinePlayer:<名字>" 的 MD5（16 字节）
//	② 第 6 字节高 4 位清零、置成 3 —— 标明这是「基于名字」的 v3 UUID
//	③ 第 8 字节高 2 位清零、置成 10 —— 标明 IETF 变体
//
// 少做 ②③ 两步，算出来的 UUID 和服务端写的对不上：
// 白名单里明明有这个人，他还是进不来，而且报错只有一句「你不在白名单里」，
// 完全看不出是 UUID 算错了。基准值由本机 JDK 的 UUID.nameUUIDFromBytes 实际跑出来，
// 见 players_test.go。
func offlineUUID(name string) string {
	sum := md5.Sum([]byte("OfflinePlayer:" + name))
	b := sum[:]
	b[6] = (b[6] & 0x0f) | 0x30
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// validPlayerName Java 版玩家名只认 ASCII 字母、数字、下划线，1-16 位。
//
// 按**字节**逐个检查，所以中文名会被挡下 —— 「玩家一号」是 12 字节，
// 长度这一关过得去，但每个字节都 ≥ 0x80，逐个检查才会露馅。
func validPlayerName(name string) bool {
	if len(name) < 1 || len(name) > 16 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
		if !ok {
			return false
		}
	}
	return true
}

// mcTime 名单文件里的时间格式：Java 的 "yyyy-MM-dd HH:mm:ss Z"。
// Go 的 -0700 正好对应 Java 的 Z（形如 +0800）。
func mcTime(t time.Time) string {
	return t.Format("2006-01-02 15:04:05 -0700")
}

// readJSONList 读一个 json 数组文件。
//
// 文件不存在（服务器没跑过、还没加过人）**不是错误**，返回空列表 ——
// 把它当错误报出去的话，一个干干净净的新实例会让整个玩家页报红。
//
// 服务端可能在同时写这个文件（有人正在游戏里 /op）。读到半截 json 就等一下重试一次：
// 面板轮询和玩家操作撞在一起的概率不低，而半截 json 会让整页失败。
func readJSONList[T any](path string) ([]T, error) {
	out := []T{}
	read := func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			return nil
		}
		out = []T{}
		if err := json.Unmarshal(b, &out); err != nil {
			return err
		}
		if out == nil {
			out = []T{}
		}
		return nil
	}
	if err := read(); err != nil {
		time.Sleep(120 * time.Millisecond)
		if err2 := read(); err2 != nil {
			return []T{}, fmt.Errorf("读取 %s 失败：%w", filepath.Base(path), err2)
		}
	}
	return out, nil
}

// writeJSONList 原子写回：先写 .tmp 再 rename。
// 服务端随时可能读这些文件，不能让它读到半个（半截 json 会让它启动直接报错）。
func writeJSONList[T any](path string, list []T) error {
	if list == nil {
		list = []T{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// sortByName 按名字排序（不分大小写）。
//
// 服务端自己写这些文件时顺序是随机的（它从 HashMap 里遍历出来），所以排序不会和它打架：
// 它下次写会重排，我们再读再排。排序是为了**我们这边**的顺序稳定 ——
// 界面每 3 秒轮询一次，不排的话名单每次刷新都自己跳一下。
func sortByName[T any](list []T, nameOf func(T) string) {
	sort.Slice(list, func(i, j int) bool {
		return strings.ToLower(nameOf(list[i])) < strings.ToLower(nameOf(list[j]))
	})
}

// isRunning 服务端在不在跑。写操作走哪条轨、踢人按钮能不能点，都看它。
func (m *Manager) isRunning(id string) bool {
	p, ok := m.proc(id)
	return ok && p.Status() == StatusRunning
}

func (m *Manager) playerFilePath(id, file string) (string, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	return filepath.Join(inst.Dir, file), nil
}

// PlayerLists 取四份名单 + 在线名单。所有集合保证非 nil（nil slice 会变成 JSON null）。
func (m *Manager) PlayerLists(id string) (PlayerRoster, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return PlayerRoster{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	r := PlayerRoster{
		Whitelist: []PlayerEntry{},
		Ops:       []OpEntry{},
		Banned:    []BanEntry{},
		BannedIPs: []IPBanEntry{},
	}
	var err error
	if r.Whitelist, err = readJSONList[PlayerEntry](filepath.Join(inst.Dir, fileWhitelist)); err != nil {
		return r, err
	}
	if r.Ops, err = readJSONList[OpEntry](filepath.Join(inst.Dir, fileOps)); err != nil {
		return r, err
	}
	if r.Banned, err = readJSONList[BanEntry](filepath.Join(inst.Dir, fileBanned)); err != nil {
		return r, err
	}
	if r.BannedIPs, err = readJSONList[IPBanEntry](filepath.Join(inst.Dir, fileBannedIPs)); err != nil {
		return r, err
	}

	// white-list 与 online-mode 都从 server.properties 读。读不到就取保守值：
	// 白名单当「没开」（让用户自己去看一眼，好过真没开时说已开启）；
	// online-mode 当「正版服」（这样不给在线的人编 UUID）。
	onlineMode := true
	if rows, err := m.ReadProperties(id); err == nil {
		for _, row := range rows {
			switch row.Key {
			case "white-list":
				r.WhitelistEnabled = strings.EqualFold(strings.TrimSpace(row.Value), "true")
			case "online-mode":
				onlineMode = !strings.EqualFold(strings.TrimSpace(row.Value), "false")
			}
		}
	}
	r.OnlineMode = onlineMode
	// 在线名单只有名字，UUID 得补。离线服算得出，正版服算不出 —— 那时留空，不编。
	r.Online = m.OnlineEntries(id, !onlineMode)
	r.Running = m.isRunning(id)

	// 每行的补充信息（累计时长 / 最后在线）。
	// 收集时**带上名单里已有的 UUID**：正版服的玩家只有名字、面板算不出 UUID，
	// 但 whitelist.json 里记着 —— 不把它带上，正版服就一个人都读不出时长。
	want := make([]rosterWant, 0, len(r.Whitelist)+len(r.Ops)+len(r.Banned)+len(r.Online))
	for _, e := range r.Whitelist {
		want = append(want, rosterWant{Name: e.Name, UUID: e.UUID})
	}
	for _, e := range r.Ops {
		want = append(want, rosterWant{Name: e.Name, UUID: e.UUID})
	}
	for _, e := range r.Banned {
		want = append(want, rosterWant{Name: e.Name, UUID: e.UUID})
	}
	for _, e := range r.Online {
		want = append(want, rosterWant{Name: e.Name, UUID: e.UUID})
	}
	r.Extra = m.RosterExtras(id, want)

	// 延迟只有 companion 插件给得了 —— 原版服务端不暴露玩家级 ping。
	// 拿不到（没装插件 / 服没跑）就整段跳过，不假装有，更不编一个数。
	if snap, err := m.CompanionSnapshot(id); err == nil && snap.Have {
		for _, p := range snap.Online {
			k := strings.ToLower(strings.TrimSpace(p.Name))
			if e, ok := r.Extra[k]; ok {
				e.Ping = p.Ping
				r.Extra[k] = e
			}
		}
	}

	// 按名字排序：store 的 map 顺序随机，而名单是**文件里的数组顺序**，
	// 服务端写文件时顺序也会变。不排一下，界面上每次刷新都可能重排。
	sortByName(r.Whitelist, func(e PlayerEntry) string { return e.Name })
	sortByName(r.Ops, func(e OpEntry) string { return e.Name })
	sortByName(r.Banned, func(e BanEntry) string { return e.Name })
	sortByName(r.BannedIPs, func(e IPBanEntry) string { return e.IP })
	return r, nil
}

// OnlinePlayers 当前在线玩家名（排序）。服务端没跑就是空。
func (m *Manager) OnlinePlayers(id string) []string {
	p, ok := m.proc(id)
	if !ok {
		return []string{}
	}
	if s := p.Status(); s != StatusRunning && s != StatusStopping {
		return []string{}
	}
	return p.PlayerNames()
}

// OnlineEntries 在线玩家，带上能算出来的 UUID。
//
// 在线名单是从日志的「xxx joined the game」里解析的，只有名字、没有 UUID。
// 离线服（online-mode=false）的 UUID 是按名字算的，所以这里补上；
// 正版服算不出来，留空 —— 界面要能区分「没记下」和「算错了」。
func (m *Manager) OnlineEntries(id string, offline bool) []PlayerEntry {
	names := m.OnlinePlayers(id)
	out := make([]PlayerEntry, 0, len(names))
	for _, n := range names {
		uuid := ""
		if offline {
			uuid = offlineUUID(n)
		}
		out = append(out, PlayerEntry{UUID: uuid, Name: n})
	}
	return out
}

// WhitelistSet 把玩家加进白名单 / 移出白名单。
func (m *Manager) WhitelistSet(id, name string, on bool) error {
	name = strings.TrimSpace(name)
	if !validPlayerName(name) {
		return fmt.Errorf("玩家名只能是 1-16 位的字母、数字或下划线（Java 版不支持中文名）")
	}
	if m.isRunning(id) {
		verb := "add"
		if !on {
			verb = "remove"
		}
		return m.Send(id, "whitelist "+verb+" "+name)
	}

	path, err := m.playerFilePath(id, fileWhitelist)
	if err != nil {
		return err
	}
	list, err := readJSONList[PlayerEntry](path)
	if err != nil {
		return err
	}
	uuid := offlineUUID(name)
	hit := false
	out := make([]PlayerEntry, 0, len(list)+1)
	for _, e := range list {
		if !strings.EqualFold(e.Name, name) {
			out = append(out, e)
			continue
		}
		hit = true
		if on {
			// 已经在名单里：把 UUID 纠正过来（可能是别处手工塞进来的，值不对）
			out = append(out, PlayerEntry{UUID: uuid, Name: e.Name})
		}
	}
	if on && !hit {
		out = append(out, PlayerEntry{UUID: uuid, Name: name})
	}
	if !on && !hit {
		return fmt.Errorf("%s 本来就不在白名单里", name)
	}
	sortByName(out, func(e PlayerEntry) string { return e.Name })
	return writeJSONList(path, out)
}

// OpSet 给玩家 OP 权限 / 撤销。level 1-4（4 = 完全权限）。
func (m *Manager) OpSet(id, name string, level int, on bool) error {
	name = strings.TrimSpace(name)
	if !validPlayerName(name) {
		return fmt.Errorf("玩家名只能是 1-16 位的字母、数字或下划线（Java 版不支持中文名）")
	}
	if !on {
		if m.isRunning(id) {
			return m.Send(id, "deop "+name)
		}
		path, err := m.playerFilePath(id, fileOps)
		if err != nil {
			return err
		}
		list, err := readJSONList[OpEntry](path)
		if err != nil {
			return err
		}
		hit := false
		out := make([]OpEntry, 0, len(list))
		for _, e := range list {
			if strings.EqualFold(e.Name, name) {
				hit = true
				continue
			}
			out = append(out, e)
		}
		if !hit {
			return fmt.Errorf("%s 本来就不是管理员", name)
		}
		sortByName(out, func(e OpEntry) string { return e.Name })
		return writeJSONList(path, out)
	}

	if level < 1 || level > 4 {
		return fmt.Errorf("管理员等级要在 1-4 之间（4 = 全部权限）")
	}
	// 命令轨：`op` 恒给 4 级，给不了指定等级。
	// 所以在跑着的时候，非 4 级要改文件；但服务端内存里仍是旧等级，
	// 得再发一条 `op` 让它重新读 —— 这是唯一一处「两条轨要配合」的地方。
	if m.isRunning(id) {
		if level == 4 {
			return m.Send(id, "op "+name)
		}
		return fmt.Errorf("服务器运行中只能用「完全权限」（4 级）。要设 %d 级，请先停服再改", level)
	}

	path, err := m.playerFilePath(id, fileOps)
	if err != nil {
		return err
	}
	list, err := readJSONList[OpEntry](path)
	if err != nil {
		return err
	}
	uuid := offlineUUID(name)
	hit := false
	out := make([]OpEntry, 0, len(list)+1)
	for _, e := range list {
		if !strings.EqualFold(e.Name, name) {
			out = append(out, e)
			continue
		}
		hit = true
		e.UUID = uuid
		e.Level = level
		out = append(out, e)
	}
	if !hit {
		out = append(out, OpEntry{UUID: uuid, Name: name, Level: level, BypassesPlayerLimit: false})
	}
	sortByName(out, func(e OpEntry) string { return e.Name })
	return writeJSONList(path, out)
}

// BanSet 封禁 / 解封。kind 是 "player" 或 "ip"；on=false 表示解封；
// expires 零值 = 永久，非零 = 到点自动解封。
//
// ⚠ 限时封禁**只能在停服时做**：服务端的 `ban` / `ban-ip` 命令只收「名字 + 原因」，
// 没有时长参数（`tempban` 是插件提供的，原版没有）。跑着的时候写文件也不作数 ——
// 服务端的内存名单不会重读，而且它下次自己写盘会把我们的 expires 覆盖掉。
// 所以运行中遇到限时就明确拒绝并说明，而不是假装成功（那会让用户以为封了 7 天，
// 实际是永久）。
func (m *Manager) BanSet(id, target, kind, reason string, expires time.Time, on bool) error {
	target = strings.TrimSpace(target)
	reason = strings.TrimSpace(reason)
	isIP := kind == "ip"
	if isIP {
		if net.ParseIP(target) == nil {
			return fmt.Errorf("%q 不是一个合法的 IP 地址", target)
		}
	} else {
		kind = "player"
		if !validPlayerName(target) {
			return fmt.Errorf("玩家名只能是 1-16 位的字母、数字或下划线（Java 版不支持中文名）")
		}
	}
	if on && !expires.IsZero() && !expires.After(time.Now()) {
		return fmt.Errorf("解封时间要晚于现在")
	}
	if err := banRejectReason(m.isRunning(id), on, expires); err != nil {
		return err
	}

	// 跑着 → 命令轨。服务端会自己算 UUID、自己写两个文件。
	if m.isRunning(id) {
		var cmd string
		switch {
		case isIP && on:
			cmd = "ban-ip " + target
		case isIP:
			cmd = "pardon-ip " + target
		case on:
			cmd = "ban " + target
		default:
			cmd = "pardon " + target
		}
		if on && reason != "" {
			cmd += " " + reason
		}
		return m.Send(id, cmd)
	}

	// 没在跑 → 改文件轨。
	file, err := m.banFilePath(id, isIP)
	if err != nil {
		return err
	}
	if isIP {
		list, err := readJSONList[IPBanEntry](file)
		if err != nil {
			return err
		}
		return m.applyIPBan(file, list, target, reason, expires, on)
	}
	list, err := readJSONList[BanEntry](file)
	if err != nil {
		return err
	}
	return m.applyBan(file, list, target, reason, expires, on)
}

// banFilePath 封禁名单文件的位置。玩家和 IP 是两份**不同**的文件，写串了会两边都不对。
func (m *Manager) banFilePath(id string, isIP bool) (string, error) {
	file := fileBanned
	if isIP {
		file = fileBannedIPs
	}
	return m.playerFilePath(id, file)
}

// banRejectReason 判断这次封禁请求在「服务端在不在跑」这个前提下能不能满足。
//
// **只认「运行中 + 限时」这一种组合是做不到的**：服务端的 `ban` / `ban-ip` 命令
// 没有时长参数（`tempban` 是插件给的，原版没有）。跑着的时候写文件也不作数 ——
// 服务端内存里的名单不会重读，它下次自己写盘还会覆盖我们的 expires。
//
// 抽成纯函数是为了能被规格表直接覆盖：**运行态在单元测试里造不出来**（要真起进程），
// 所以判据必须和进程状态解耦，否则这一整条分支永远没有测试。
func banRejectReason(running, on bool, expires time.Time) error {
	if running && on && !expires.IsZero() {
		return fmt.Errorf("%w：服务端的 ban 命令不支持时长，只能永久封禁。要限时请先停服再封",
			ErrBanNeedsStop)
	}
	return nil
}

// applyBan 往 banned-players.json 里加 / 删一条。
func (m *Manager) applyBan(path string, list []BanEntry, name, reason string, expires time.Time, on bool) error {
	hit := false
	out := make([]BanEntry, 0, len(list)+1)
	for _, e := range list {
		if strings.EqualFold(e.Name, name) {
			hit = true
			if !on {
				continue
			}
		}
		out = append(out, e)
	}
	if !on {
		if !hit {
			return fmt.Errorf("%s 本来就没被封禁", name)
		}
		sortByName(out, func(e BanEntry) string { return e.Name })
		return writeJSONList(path, out)
	}
	if hit {
		return fmt.Errorf("%s 已经在封禁名单里了", name)
	}
	if reason == "" {
		reason = "Banned by an operator."
	}
	out = append(out, BanEntry{
		UUID:    offlineUUID(name),
		Name:    name,
		Created: mcTime(time.Now()),
		Source:  "EnderBox",
		Expires: banExpires(expires),
		Reason:  reason,
	})
	sortByName(out, func(e BanEntry) string { return e.Name })
	return writeJSONList(path, out)
}

// banExpires 封禁条目里的到期时间。服务端认的「永久」就是字面量 "forever"
// （不是空串）—— 写空串它解析不了。
func banExpires(expires time.Time) string {
	if expires.IsZero() {
		return "forever"
	}
	return mcTime(expires)
}

func (m *Manager) applyIPBan(path string, list []IPBanEntry, ip, reason string, expires time.Time, on bool) error {
	hit := false
	out := make([]IPBanEntry, 0, len(list)+1)
	for _, e := range list {
		if e.IP == ip {
			hit = true
			if !on {
				continue
			}
		}
		out = append(out, e)
	}
	if !on {
		if !hit {
			return fmt.Errorf("%s 本来就没被封禁", ip)
		}
		sortByName(out, func(e IPBanEntry) string { return e.IP })
		return writeJSONList(path, out)
	}
	if hit {
		return fmt.Errorf("%s 已经在封禁名单里了", ip)
	}
	if reason == "" {
		reason = "Banned by an operator."
	}
	out = append(out, IPBanEntry{
		IP:      ip,
		Created: mcTime(time.Now()),
		Source:  "EnderBox",
		Expires: banExpires(expires),
		Reason:  reason,
	})
	sortByName(out, func(e IPBanEntry) string { return e.IP })
	return writeJSONList(path, out)
}

// KickPlayer 把在线玩家踢出去。只能走命令轨 —— 服务端没跑就没有「在线」可言。
func (m *Manager) KickPlayer(id, name, reason string) error {
	name = strings.TrimSpace(name)
	if !validPlayerName(name) {
		return fmt.Errorf("玩家名只能是 1-16 位的字母、数字或下划线（Java 版不支持中文名）")
	}
	if !m.isRunning(id) {
		return fmt.Errorf("%w，没有可以踢的人", ErrInstanceNotRunning)
	}
	cmd := "kick " + name
	if r := strings.TrimSpace(reason); r != "" {
		cmd += " " + r
	}
	return m.Send(id, cmd)
}
