package daemon

import (
	"fmt"
	"path/filepath"
	"strings"
)

// 把「普通模组服」转成「对应的混合端」。
//
// 为什么做成一个显式操作，而不是让用户自己在配置里改类型那一栏：
// 「类型」是好几处的判据 —— 服务端文件怎么下（preflight 一键补齐、配置页重新下载）、
// 插件与模组的目录、能不能用实时数据。手改一个字段，等于让这些判据集体说谎。
// 真正要做的是一整套动作，而且**顺序不能反**：
//
//	① 先下一个混合端的整端 jar（Mohist / Arclight 都是能直接 java -jar 的）
//	② 校验通过后，才把它设成这个实例的启动文件
//	③ 最后才改类型
//
// 反过来先改类型再下载，一旦下载失败（断网、上游 502），就留下一台
// 「自称混合端、启动文件却还是 Forge 的 args.txt」的实例 —— 那种状态界面解释不了，
// 用户也修不回来。所以这里的设计是：**下载成功才算转换成功**。

// ConversionTarget 一个可以转过去的目标。
type ConversionTarget struct {
	Type      Type   `json:"type"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	// NewJar 转换之后这个实例的启动文件叫什么（相对实例目录）。由 api 层填。
	// 放在这里是为了让「名字怎么起」只有一处实现。
	NewJar string `json:"newJar"`
	// Note 一句话说清「转过去之后是什么」、以及代价。上游不写的事这里必须写。
	Note string `json:"note"`
}

// ConversionRefusal 从当前类型出发、我们**故意不做**的转换，以及原因。
// 不静默消失：用户有权知道「有这么条路，只是现在走不通、卡在哪」——
// 这和服务端类型清单里那两条「标 false 并说清原因」是同一条规矩。
type ConversionRefusal struct {
	Label  string `json:"label"`
	Reason string `json:"reason"`
}

// conversionTargets 真相源：从某个类型能转到哪些混合端。前端别另写名单。
//
// 只收「同一个模组系」的组合 —— 这就是「对应」两个字的实际含义：
// Mohist / Arclight 的本体就是 Forge，所以 mods/ 里的模组不用重装、world/ 不用搬，
// 换掉的只是外面那层启动壳。跨系（Forge 转 Cardboard 这种）就不叫「对应」了。
var conversionTargets = map[Type][]Type{
	TypeForge:    {TypeMohist, TypeArclight},
	TypeMohist:   {TypeArclight},
	TypeArclight: {TypeMohist},
}

// conversionNotes 每个目标的一句话说明。用户点之前必须知道自己在换什么。
var conversionNotes = map[Type]string{
	TypeMohist: "Mohist：一个整端 jar，Forge 和 Bukkit 一起实现。" +
		"它自带 Forge 本体但 libraries/ 要首次启动自己装（会再下几十 MB、必须联网）。" +
		"mods/ 与 world/ 原样保留。",
	TypeArclight: "Arclight：同样是 Forge + Bukkit 的整端 jar，装好之后行为接近 Mohist。" +
		"⚠ 它只发布在 GitHub，下载会经第三方加速代理转一道。" +
		"mods/ 与 world/ 原样保留。",
}

// ConversionTargetsOf 从当前类型出发，能转到哪几个混合端。
// 已经是混合端的实例给「另一个混合端」，方便在 Mohist / Arclight 之间来回换。
//
// ⚠ 没有目标时返回**空切片，不是 nil** —— Go 把 nil slice 序列化成 `null`，
// 前端 `d.targets.length` 会直接抛异常、整块面板白掉。这是本项目吃过最多次的坑，
// 判据要放在**序列化之后**才看得出（见 TestConversionListsNeverSerializeNull）。
func ConversionTargetsOf(t Type) []ConversionTarget {
	out := []ConversionTarget{}
	for _, target := range conversionTargets[t] {
		note := conversionNotes[target]
		out = append(out, ConversionTarget{
			Type:      target,
			Label:     TypeDisplay(target),
			Available: true,
			Note:      note,
		})
	}
	return out
}

// ConversionRefusalsOf 当前类型下、我们不做的那几条路，连同原因。
//
// 每一条都必须是**具体的**理由，不许拿「不支持」三个字糊过去 ——
// 用户看到「不支持」只会去别处试，看到「卡在没接下载源」才知道该等什么。
// 同样：没有可得返回空切片，不能是 nil。
func ConversionRefusalsOf(t Type) []ConversionRefusal {
	switch t {
	case TypeFabric:
		return []ConversionRefusal{{
			Label: "Cardboard（Fabric 侧的混合端）",
			Reason: "它不是「换一个启动文件」就成了的东西：Cardboard = Fabric 服务端 + mods/ 里的三件套" +
				"（Cardboard 本体、Fabric API、iCommon）。这个面板现有能力就能配出来（Fabric 会下、Modrinth 会装），" +
				"但那属于「往 mods/ 里装桥接模组」，跟「换服务端」是两件事 —— 所以没做成这个按钮，" +
				"你去「插件与模组」页按上面三个名字搜着装就行。",
		}}
	case TypeNeoForge:
		return []ConversionRefusal{{
			Label: "Youer（NeoForge 侧的混合端）",
			Reason: "下载接口已经探通了（api.mohistmc.com/project/youer），但面板还没接它的下载源，" +
				"所以现在转不过去。这是待办里的一项。",
		}}
	case TypePaper, TypeVanilla:
		return []ConversionRefusal{{
			Label: "换成任何混合端",
			Reason: "你这个本来就是 Bukkit 端（插件已经能装），缺的只是模组。" +
				"要在同一台服上跑 Forge 模组，等于把服务端整个换掉 —— 存档多数能直接读，" +
				"但 Paper 那套配置、以及依赖 Paper API 的插件搬过去不保证能用。" +
				"所以这条路不做成一键转换：它更接近「换一台服」，建议新建一个混合端实例、再把存档搬过去。",
		}}
	case TypeVelocity, TypeBungee:
		return []ConversionRefusal{{
			Label: "换成任何混合端",
			Reason: "代理端没有游戏版本也没有存档，它只是在后面那几台服前面站着 —— " +
				"不存在「把它转成混合端」这回事。要加模组请转它后面的那台服务端。",
		}}
	}
	// 能转的（forge / mohist / arclight）没有要解释的排除项
	return []ConversionRefusal{}
}

// ConversionWarning 转换前必须让用户看到的代价。**不放在点击之后** ——
// 这些事决定了「要不要转」，得在按钮之前就摆出来。
//
// ⚠ 这里是**纯文本**，界面直接整段渲染出来（不走 Markdown）。别写 `**加粗**` 这类记号，
// 那些星号会原样印在屏幕上 —— 这条踩过。
const ConversionWarning = "转换只做两件事：下好混合端的启动文件、把它设成这个实例的启动文件，再把类型改过来。" +
	"mods/、plugins/、world/、各种配置都原样保留。但有三件事你必须先知道：" +
	"① 混合端会复用实例里已有的 libraries/ 并把缺的补上 —— 旧启动文件我们留着不动，" +
	"所以理论上能倒回去，但库目录被动过之后不保证倒得干净；" +
	"② 少数用 Mixin / ASM 改底层类的核心模组，可能跟 Bukkit 实现打架 —— 这是混合端的固有风险，" +
	"上游开发者明确不受理这类问题；" +
	"③ 存档多数能直接读，但同样不保证。转之前先做一次备份。"

// ConversionNewJarName 转换之后该把这个实例的启动文件叫什么（相对实例目录）。
//
// 不叫 server.jar 是有意的：转换前的 Forge 启动文件（args.txt / shim jar）**要留着**，
// 那是唯一的回退路径，名字撞上就把它覆盖了。
// 名字前面带上 mohist / arclight 这两个词也顺带让侧栏、导入识别、混合端判定都认得出来
// （serverLabel 走启动文件名的首段，isHybridLauncherName 也认这两个词）。
func ConversionNewJarName(inst Instance, target Type) string {
	mc := strings.TrimSpace(inst.MCVersion)
	switch target {
	case TypeMohist:
		if mc == "" {
			return "mohist.jar"
		}
		return fmt.Sprintf("mohist-%s.jar", mc)
	case TypeArclight:
		if mc == "" {
			return "arclight-forge.jar"
		}
		return fmt.Sprintf("arclight-forge-%s.jar", mc)
	}
	return ""
}

// ConversionNewJarPath 转换后启动文件的绝对路径。
func ConversionNewJarPath(inst Instance, target Type) string {
	name := ConversionNewJarName(inst, target)
	if name == "" {
		return ""
	}
	return filepath.Join(inst.Dir, name)
}

// ConversionAllowed target 是不是当前类型允许转过去的目标。返回原因（"": 允许）。
//
// ⚠ 这里返回的原因和 ConversionRefusalsOf 里那些**不是一回事**：
// 那些是「我们没做这条路」，这里是「这个具体实例现在不具备条件」。
// 两者给用户的下一步动作完全不同（一个要等我们，一个他自己就能改），
// 所以文案不能复用。
func ConversionAllowed(inst Instance, target Type) string {
	if inst.Dir == "" {
		return "这个实例没记录目录，没法放新文件"
	}
	if strings.TrimSpace(inst.MCVersion) == "" {
		// 混合端是绑游戏版本的（同一版本线才有对应构建），没版本就没法挑。
		return "实例没记录游戏版本，挑不了对应的构建 —— 先去「配置」页把版本填上再来"
	}
	for _, t := range ConversionTargetsOf(inst.Type) {
		if t.Type == target {
			return ""
		}
	}
	if target == inst.Type {
		return "它已经是 " + TypeDisplay(target) + " 了"
	}
	return fmt.Sprintf("从 %s 转不到 %s（跨了模组系：模组要重装、存档也不保证能读，那不叫「对应」）",
		TypeDisplay(inst.Type), TypeDisplay(target))
}

// ConvertedInstance 转换成功后要写回的实例（纯函数，测试盯着它）。
//
// 只动两处：Type 与 JarPath。其余字段（端口、内存、备份策略、端口、自动启动…）
// 一个都不碰 —— 转换服务端类型不该顺手把用户的设置改掉。
func ConvertedInstance(inst Instance, target Type) Instance {
	out := inst
	out.Type = target
	out.JarPath = ConversionNewJarName(inst, target)
	return out
}
