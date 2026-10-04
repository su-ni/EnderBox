package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PropertyRow server.properties 里的一项。
// Kind 决定界面用什么控件，Options 只对 enum 有意义。
type PropertyRow struct {
	Key     string   `json:"key"`
	Value   string   `json:"value"`
	Kind    string   `json:"kind"` // string | int | bool | enum
	Label   string   `json:"label"`
	Group   string   `json:"group"` // 界面上的分组名，顺序按 spec 里首次出现的先后
	Hint    string   `json:"hint,omitempty"`
	Options []string `json:"options,omitempty"`
	Known   bool     `json:"known"`
}

// propSpecs 我们认得、并且值得做成界面的字段，顺序就是界面顺序。
// 只挑「个人服主真的会改」的：把 server.properties 里 80 多个键全铺出来是灾难。
var propSpecs = []PropertyRow{
	{Key: "server-port", Group: "网络与身份", Kind: "int", Label: "监听端口", Hint: "服务端真正监听的端口", Value: "25565"},
	{Key: "motd", Group: "网络与身份", Kind: "string", Label: "服务器标语", Hint: "服务器列表里显示的那句话", Value: "A Minecraft Server"},
	{Key: "online-mode", Group: "网络与身份", Kind: "bool", Label: "正版验证", Hint: "关掉才允许离线客户端进来", Value: "true"},
	{Key: "max-players", Group: "网络与身份", Kind: "int", Label: "最大玩家数", Value: "20"},
	{Key: "difficulty", Group: "玩法", Kind: "enum", Label: "游戏难度", Options: []string{"peaceful", "easy", "normal", "hard"}, Value: "easy"},
	{
		Key: "gamemode", Group: "玩法", Kind: "enum", Label: "默认游戏模式",
		Options: []string{"survival", "creative", "adventure", "spectator"}, Value: "survival",
		Hint: "新玩家进来时的模式",
	},
	{Key: "pvp", Group: "玩法", Kind: "bool", Label: "玩家互相伤害", Value: "true"},
	{Key: "hardcore", Group: "玩法", Kind: "bool", Label: "极限模式", Hint: "死了就变旁观", Value: "false"},
	{Key: "allow-flight", Group: "玩法", Kind: "bool", Label: "允许飞行", Hint: "装了飞行类模组必须开", Value: "false"},
	{Key: "allow-nether", Group: "玩法", Kind: "bool", Label: "允许去下界", Value: "true"},
	{Key: "spawn-protection", Group: "世界生成", Kind: "int", Label: "出生点保护半径", Hint: "半径内非 OP 不能破坏方块", Value: "16"},
	{Key: "level-name", Group: "世界生成", Kind: "string", Label: "世界文件夹名", Hint: "存档文件夹名，改名等于换世界", Value: "world"},
	{Key: "level-seed", Group: "世界生成", Kind: "string", Label: "世界种子", Value: ""},
	{Key: "view-distance", Group: "世界生成", Kind: "int", Label: "视距", Hint: "调小最省性能", Value: "10"},
	{Key: "simulation-distance", Group: "世界生成", Kind: "int", Label: "模拟距离", Value: "10"},
	{Key: "player-idle-timeout", Group: "玩家管理", Kind: "int", Label: "挂机踢出（分钟）", Hint: "0 为不踢", Value: "0"},
	{Key: "white-list", Group: "玩家管理", Kind: "bool", Label: "启用白名单", Value: "false"},
	{Key: "enforce-whitelist", Group: "玩家管理", Kind: "bool", Label: "白名单强制踢出", Hint: "把已在线的非白名单玩家踢掉", Value: "false"},
	{Key: "enable-command-block", Group: "玩法", Kind: "bool", Label: "允许命令方块", Value: "false"},
	{Key: "network-compression-threshold", Group: "网络与身份", Kind: "int", Label: "网络压缩阈值", Hint: "默认就行，不懂别动", Value: "256"},
	{Key: "enable-rcon", Group: "远程控制", Kind: "bool", Label: "开启 RCON", Hint: "给外部工具用的远程控制通道", Value: "false"},
	{Key: "rcon.port", Group: "远程控制", Kind: "int", Label: "RCON 端口", Value: "25575"},
	{Key: "rcon.password", Group: "远程控制", Kind: "string", Label: "RCON 密码", Hint: "开了 RCON 就必须设", Value: ""},
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
	"网络与身份": "别人怎么连进来、以什么身份进来",
	"玩法":    "游戏规则本身 —— 难度、模式、允许什么",
	"世界生成":  "存档长什么样、跑多远、机器扛多少",
	"玩家管理":  "谁能进、进来之后能干什么",
	"远程控制":  "给外部工具用的通道，没需求别开",
}

// GroupDescs 给前端的分组说明表
func GroupDescs() map[string]string {
	out := make(map[string]string, len(propGroupDesc))
	for k, v := range propGroupDesc {
		out[k] = v
	}
	return out
}

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
		if v, ok := present[s.Key]; ok {
			row.Value = v
			seen[s.Key] = true
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
		out = append(out, PropertyRow{Key: k, Value: present[k], Kind: "string", Group: "其它（不认得）", Known: false})
	}
	return out, nil
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

// validateProp 值合法性。宁可在这里挡住，也别写进去让服务端起不来 ——
// 服务端读不懂会整份配置回退成默认值，而且不给任何提示。
func validateProp(key, val string) error {
	i, known := propSpecIndex[key]
	if !known {
		// 未知键仍然允许写（插件可能自己加键），只做最基本的换行防护
		if strings.ContainsAny(val, "\r\n") {
			return fmt.Errorf("%s 的值不能包含换行", key)
		}
		return nil
	}
	spec := propSpecs[i]
	switch spec.Kind {
	case "int":
		if strings.TrimSpace(val) == "" {
			return fmt.Errorf("%s 需要填一个整数", spec.Label)
		}
		if _, err := strconv.Atoi(strings.TrimSpace(val)); err != nil {
			return fmt.Errorf("%s 需要填整数，收到「%s」", spec.Label, val)
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
	if strings.ContainsAny(val, "\r\n") {
		return fmt.Errorf("%s 的值不能包含换行", key)
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
// 文件里没有的键追加到末尾，并注明是 ServerBox 补的。
func (m *Manager) WriteProperties(id string, updates map[string]string) error {
	if len(updates) == 0 {
		return nil
	}
	path, _, err := m.propsPath(id)
	if err != nil {
		return err
	}
	for k, v := range updates {
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
		lines = append(lines, "# 以下由 ServerBox 补充")
		for _, k := range sortedStrings(missing) {
			lines = append(lines, k+"="+updates[k])
		}
	}

	out := strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n"
	return os.WriteFile(path, []byte(out), 0o644)
}
