package fetch

import (
	"strings"
	"testing"
)

// 快照没嵌进来（或者 JSON 坏了）是最要命的失败：功能会静默退化成「中文搜不到」，
// 而界面上看不出任何异常。先把它钉住。
func TestZhSnapshotLoaded(t *testing.T) {
	n := ZhSnapshotSize()
	if n < 1000 {
		t.Fatalf("中文译名快照只加载到 %d 条，基本可以断定 embed 或 JSON 解析坏了", n)
	}
}

// 中文关键词要真的能换出英文名 —— 这是「中文搜得到模组」的全部依据。
//
// 这几条都是实测过的：不换的话它们在 Modrinth 上分别是 0 条或只能搜到附属包。
func TestResolveZhKnownMods(t *testing.T) {
	cases := []struct {
		query string
		want  string // 期望换出的英文名
		exact bool   // true = 完全相等；false = 只要包含（同义变体都算对）
	}{
		// 这三个是「唯一正主」，必须精确命中
		{"暮色森林", "The Twilight Forest", true},
		{"通用机械", "Mekanism", true},
		{"机械动力", "Create", true},
		// 「物品管理器」在百科上有一串同名变体（JEI / NEI / REI / LEI…），
		// 挑中哪一个都合理，但绝不能挑到不相干的东西上
		{"物品管理器", "Enough Items", false},
	}
	for _, c := range cases {
		got := ResolveZh(c.query)
		if len(got) == 0 {
			t.Errorf("「%s」没有换出任何英文名", c.query)
			continue
		}
		en := got[0].English
		ok := en == c.want
		if !c.exact {
			ok = strings.Contains(en, c.want)
		}
		if !ok {
			t.Errorf("「%s」换成了 %q，期望%s %q（百科条目：%s）",
				c.query, en, map[bool]string{true: "", false: "包含"}[c.exact], c.want, got[0].Matched)
		}
		// 候选必须带着「据谁换的」，界面要拿它告诉用户换成了什么
		if got[0].Matched == "" {
			t.Errorf("「%s」的候选没带百科条目名，界面就没法解释这次改写", c.query)
		}
	}
}

// 附属包不能盖过正主。
//
// 这一条是被真实坑出来的：按「相似度 + 热度」挑（常见做法）会把「暮色森林」
// 锁到某个附属包上，用户看到的是一串看着对、其实不是他要的模组。
// 我们的判据是「同一档里字数少的优先」—— 正主一般叫得短，附属才挂后缀。
func TestResolveZhPrefersTheMainModOverAddons(t *testing.T) {
	got := ResolveZh("暮色森林")
	if len(got) == 0 {
		t.Fatal("「暮色森林」没有换出任何英文名")
	}
	if got[0].English != "The Twilight Forest" {
		t.Errorf("「暮色森林」锁到了附属上：%q（百科条目：%s），应当是 The Twilight Forest",
			got[0].English, got[0].Matched)
	}
}

// 非中文、或者表里根本没有的词，都不该乱改关键词 ——
// 乱改比不改更糟：用户以为自己搜的是这个词，实际搜的是我们编的另一个词。
func TestResolveZhLeavesNonChineseAndUnknownAlone(t *testing.T) {
	for _, q := range []string{
		"jei",
		"JourneyMap",
		"sodium",
		"",
		"   ",
		"一个根本不存在的模组名字啊啊啊",
	} {
		if got := ResolveZh(q); len(got) > 0 {
			t.Errorf("「%s」本不该被改写，却换成了 %v", q, got)
		}
	}
}

// 归一化要能吃掉空格和标点，否则「jei 物品管理器」这种带空格的输入会对不上。
func TestZhNorm(t *testing.T) {
	same := [][2]string{
		{"[JEI] 物品管理器", "jei物品管理器"},
		{"JEI物品管理器", "jei物品管理器"},
		{"  暮色森林  ", "暮色森林"},
		{"Tinkers' Construct", "tinkersconstruct"},
	}
	for _, c := range same {
		if got := zhNorm(c[0]); got != c[1] {
			t.Errorf("zhNorm(%q) = %q，期望 %q", c[0], got, c[1])
		}
	}
	if got := zhStripTag("[IC2] 工业时代2"); got != "工业时代2" {
		t.Errorf("zhStripTag 没剥掉缩写前缀：%q", got)
	}
	if got := zhStripTag("机械动力"); got != "机械动力" {
		t.Errorf("zhStripTag 不该动没有前缀的名字：%q", got)
	}
}

// 反查中文名只许精确命中 —— 这条是**展示**用的名字，猜错比不显示严重得多。
// 用户会拿列表里那个中文名判断该装哪个模组，张冠李戴等于骗人。
func TestZhNameForIsExactOnly(t *testing.T) {
	// 正例：表里的英文名精确对上，就该给出中文名
	if got := ZhNameFor("AppleSkin", "appleskin"); got == "" {
		t.Error("AppleSkin 反查不到中文名（表里明明有）")
	}
	// 反例：标题里**包含**正主的英文名，但表里并没有这个精确名字 —— 绝不能硬凑一个。
	// ⚠ 别拿真实存在的附属包当反例：表里真有「Twilight Forest - Dungeons & Villages」
	// 这种条目，那属于精确命中、本来就该给中文名（我第一版测试就写错在这）。
	for _, title := range []string{
		"Sodium Extra Extended",         // 含 Sodium
		"Just Enough Items (JEI) Extra", // 含 Just Enough Items
		"完全不存在的东西 ABCDEF",
	} {
		if got := ZhNameFor(title, ""); got != "" {
			t.Errorf("「%s」不该反查出中文名 %q —— 反查只做精确匹配，包含匹配会张冠李戴", title, got)
		}
	}
}

// 给一批结果补中文名时，补不上的要留空（而不是塞个错的）。
func TestDecorateZhNamesLeavesUnknownEmpty(t *testing.T) {
	hits := []MarketHit{
		{ID: "a", Title: "AppleSkin", Slug: "appleskin"},
		{ID: "b", Title: "Sodium Extra Extended"},
	}
	DecorateZhNames(hits)
	if hits[0].ZhName == "" {
		t.Error("AppleSkin 应该补上中文名")
	}
	if hits[1].ZhName != "" {
		t.Errorf("表里没有的标题不该被硬凑中文名，实得 %q", hits[1].ZhName)
	}
}
