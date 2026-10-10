package daemon

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestConversionTargetsStayWithinOneModSystem 「对应」两个字的实际含义：
// 只能转到**同一个模组系**的混合端。跨系（比如 Forge 转 Cardboard）不是转换，
// 那是换一台服 —— 模组要重装、存档也不保证，不该出现在这个按钮下。
func TestConversionTargetsStayWithinOneModSystem(t *testing.T) {
	cases := map[Type][]Type{
		TypeForge:    {TypeMohist, TypeArclight},
		TypeMohist:   {TypeArclight},
		TypeArclight: {TypeMohist},
		// 下面这些一个目标都不该有
		TypePaper:     nil,
		TypeVanilla:   nil,
		TypeFabric:    nil,
		TypeNeoForge:  nil,
		TypeVelocity:  nil,
		TypeBungee:    nil,
		Type(""):      nil,
		Type("weird"): nil,
	}
	for from, want := range cases {
		got := ConversionTargetsOf(from)
		if len(got) != len(want) {
			t.Errorf("%s 的目标数 = %d，期望 %d（%+v）", from, len(got), len(want), got)
			continue
		}
		for i, w := range want {
			if got[i].Type != w {
				t.Errorf("%s 第 %d 个目标是 %s，期望 %s", from, i, got[i].Type, w)
			}
			if !got[i].Available {
				t.Errorf("%s → %s 标成了不可用，但 TypeDisplay 层没条件可降 —— 可用性只该由实例自身决定", from, w)
			}
			if strings.TrimSpace(got[i].Note) == "" {
				t.Errorf("%s → %s 没有说明 —— 用户不知道转过去是什么", from, w)
			}
			if strings.TrimSpace(got[i].Label) == "" {
				t.Errorf("%s → %s 没有显示名", from, w)
			}
		}
	}
}

// TestConversionRefusalsAlwaysExplainThemselves 「不做」必须说清卡在哪。
//
// 这条和服务端类型清单那条（TestSourcesAlwaysExplainThemselves）是同一条规矩，
// 而且这里更要紧：用户在配置页看到的是**一片空白**（没有任何可点的目标），
// 不给原因的话，他只会以为「这个面板不支持转换」，然后去别处折腾。
// 同样不许拿「不支持」三个字糊过去 —— 那不是原因，是拒绝回答。
func TestConversionRefusalsAlwaysExplainThemselves(t *testing.T) {
	// 这几个类型必须各有一条说明，不能空手而归
	mustExplain := []Type{TypeFabric, TypeNeoForge, TypePaper, TypeVanilla, TypeVelocity, TypeBungee}
	for _, ty := range mustExplain {
		rs := ConversionRefusalsOf(ty)
		if len(rs) == 0 {
			t.Errorf("%s 既没有可转的目标、也没有说明 —— 用户会以为面板没这个功能", ty)
			continue
		}
		for _, r := range rs {
			if strings.TrimSpace(r.Reason) == "" {
				t.Errorf("%s 的说明是空的", ty)
				continue
			}
			if n := len([]rune(r.Reason)); n < 30 {
				t.Errorf("%s 的说明只有 %d 个字，说不清卡在哪：%q", ty, n, r.Reason)
			}
			if strings.Contains(r.Reason, "不支持") || strings.Contains(r.Reason, "很慢") {
				t.Errorf("%s 拿「不支持 / 很慢」当理由了 —— 要写具体卡在哪件事实上：%q", ty, r.Reason)
			}
		}
	}
	// 能转的类型不该同时挂一堆「不做」的说明（那只会让面板看着自相矛盾）
	for _, ty := range []Type{TypeForge, TypeMohist, TypeArclight} {
		if rs := ConversionRefusalsOf(ty); len(rs) > 0 {
			t.Errorf("%s 有可转目标，却还挂着 %d 条「不做」说明：%+v", ty, len(rs), rs)
		}
	}
}

// TestConversionNewJarKeepsTheOldLauncher 这条守的是一个**回退能力**：
// 转换之后旧启动文件必须还在原处。所以新 jar 名字不能等于实例现在的 JarPath ——
// 撞上了就是直接覆盖，用户再也回不去 Forge 了。
//
// 同时盯住两件小事：
//   - 名字必须是**裸文件名**（不带路径分隔符）。JarPath 存的是相对实例目录的名字，
//     塞进一个带目录的路径会让 resolveJar 算出别的落点 —— 那就是「下完了却起不来」。
//   - 名字里要带 mohist / arclight 这两个词：侧栏标签（serverLabel 取启动文件名首段）
//     和混合端识别（isHybridLauncherName）都靠它。
func TestConversionNewJarKeepsTheOldLauncher(t *testing.T) {
	forge := Instance{
		ID: "i1", Type: TypeForge, MCVersion: "1.20.1", Dir: `F:\srv\forge`,
		// 真机上普通 Forge 的启动文件就是 args 文件（路径驱动启动），
		// 这条是那个真实形态，别换成 server.jar —— 换成 server.jar 就测不出「撞名」了
		JarPath: `libraries\net\minecraftforge\forge\1.20.1-47.4.23\win_args.txt`,
	}
	for _, target := range []Type{TypeMohist, TypeArclight} {
		name := ConversionNewJarName(forge, target)
		if name == "" {
			t.Fatalf("%s → %s 没算出文件名", forge.Type, target)
		}
		if strings.ContainsAny(name, `/\`) {
			t.Errorf("文件名里带了路径分隔符：%q（必须是相对实例目录的裸名）", name)
		}
		if strings.EqualFold(name, forge.JarPath) {
			t.Errorf("新文件名 %q 撞上了旧启动文件 —— 会把唯一的回退路径覆盖掉", name)
		}
		if !strings.Contains(strings.ToLower(name), string(target)) {
			t.Errorf("%q 里没带 %q 这个词，侧栏标签与混合端识别会认不出来", name, target)
		}
		if !strings.Contains(name, forge.MCVersion) {
			t.Errorf("%q 里没带游戏版本 —— 同一台机器上装两个版本就分不清了", name)
		}
		if p := ConversionNewJarPath(forge, target); !strings.HasPrefix(p, forge.Dir) {
			t.Errorf("落点 %q 不在实例目录 %q 里", p, forge.Dir)
		}
	}
}

// TestConversionListsNeverSerializeNull 空列表必须是 `[]`，不能是 `null`。
//
// ⚠ 这条判据**必须放在序列化之后**：Go 里 nil slice 和空 slice 用 == 比不出来，
// 遍历也遍历不出差别（两个都转 0 次），只有过一遍 JSON 才看得出一个是 `[]`、一个是 `null`。
// 而前端的写法是 `d.targets.length` / `d.refusals.map(...)` —— 拿到 null 会直接抛异常，
// 整块面板白掉。本轮就是这么发现的：单测全绿、接口一打 `refusals` 是 null。
func TestConversionListsNeverSerializeNull(t *testing.T) {
	types := []Type{
		TypeForge, TypeMohist, TypeArclight,
		TypePaper, TypeVanilla, TypeFabric, TypeNeoForge, TypeVelocity, TypeBungee,
		Type(""), Type("whatever"),
	}
	for _, ty := range types {
		// 用匿名 struct 复刻接口的字段名与形状，序列化后再判 —— 直接看变量是看不出来的
		payload, err := json.Marshal(struct {
			Targets  []ConversionTarget  `json:"targets"`
			Refusals []ConversionRefusal `json:"refusals"`
		}{ConversionTargetsOf(ty), ConversionRefusalsOf(ty)})
		if err != nil {
			t.Fatalf("%s 序列化失败：%v", ty, err)
		}
		got := string(payload)
		if strings.Contains(got, "null") {
			t.Errorf("%s 的列表里有 null —— 前端 .length / .map 会当场炸：%s", ty, got)
		}
		if !strings.HasPrefix(got, `{"targets":[`) || !strings.Contains(got, `"refusals":[`) {
			t.Errorf("%s 的字段不是数组：%s", ty, got)
		}
	}
}

// TestConvertedInstanceOnlyTouchesTypeAndJar 转换**只该动两处**。
// 顺手把端口、内存、备份策略、自动重启改掉的话，用户会以为「转个服务端把我设置清了」——
// 那和不静默失败是同一条底线。
func TestConvertedInstanceOnlyTouchesTypeAndJar(t *testing.T) {
	before := Instance{
		ID: "i1", Name: "我的服", Type: TypeForge, MCVersion: "1.20.1",
		Dir: `F:\srv\forge`, JarPath: "server.jar",
		Port: 25599, MinMem: 1024, MaxMem: 8192,
		AutoStart: true, AutoRestart: true,
		JVMArgs: "-XX:+UseG1GC", LoaderVersion: "47.4.23",
	}
	after := ConvertedInstance(before, TypeMohist)
	if after.Type != TypeMohist {
		t.Errorf("类型没改过来：%s", after.Type)
	}
	if after.JarPath != ConversionNewJarName(before, TypeMohist) {
		t.Errorf("启动文件没换成新 jar：%s", after.JarPath)
	}
	// 把这两处还原之后，其余字段必须逐个相等
	after.Type, after.JarPath = before.Type, before.JarPath
	// ⚠ 用 DeepEqual 而不是 ==：Instance 里有 BackupPolicy（含 []string），结构体不可比较
	if !reflect.DeepEqual(after, before) {
		t.Errorf("转换动了不该动的字段：\n before=%+v\n after =%+v", before, after)
	}
}

// TestConversionAllowedExplainsPerInstanceReason 目标本身合法、但这个**实例**现在
// 不具备条件时，要说清是哪一条 —— 因为下一步动作完全不同：
// 「我们没做这条路」用户只能等，「你没填版本」他自己就能改。
func TestConversionAllowedExplainsPerInstanceReason(t *testing.T) {
	ok := Instance{Type: TypeForge, MCVersion: "1.20.1", Dir: `F:\srv\a`, JarPath: "server.jar"}
	if why := ConversionAllowed(ok, TypeMohist); why != "" {
		t.Errorf("条件齐备却不让转：%q", why)
	}

	noVer := ok
	noVer.MCVersion = ""
	why := ConversionAllowed(noVer, TypeMohist)
	if why == "" {
		t.Fatal("没记游戏版本却放行了 —— 会挑不出对应的构建")
	}
	if !strings.Contains(why, "版本") {
		t.Errorf("原因里没提「版本」，用户不知道要改什么：%q", why)
	}

	noDir := ok
	noDir.Dir = ""
	if why := ConversionAllowed(noDir, TypeMohist); why == "" {
		t.Error("没有实例目录却放行了")
	}

	// 跨模组系
	if why := ConversionAllowed(ok, TypeArclight); why != "" {
		t.Errorf("Forge → Arclight 本来就该允许，却被挡了：%q", why)
	}
	paper := Instance{Type: TypePaper, MCVersion: "1.20.1", Dir: `F:\srv\b`, JarPath: "server.jar"}
	if why := ConversionAllowed(paper, TypeMohist); why == "" {
		t.Error("Paper → Mohist 放行了 —— 那是换一台服，不是转换")
	}

	// 转成自己
	if why := ConversionAllowed(ok, TypeForge); why == "" {
		t.Error("转成自己却放行了")
	}
}
