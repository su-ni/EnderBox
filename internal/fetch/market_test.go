package fetch

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 这几个测试打真源站（-short 时跳过）。
// 为什么要打真的：市场这块唯一的失败模式就是「用户点了没反应」，
// 而 mock 永远能造出一个看起来正确的响应，抓不到「Cloudflare 抽风」这种真实故障。
// 真正要证明的是**兜底链会兜底**，不是「代码看起来对」。
func skipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("需要网络")
	}
}

func TestMarketBasesOrderAndDedup(t *testing.T) {
	// 用户配的地址必须排第一 —— 他既然配了就是认定那个好用，别抢他的决定。
	got := marketBases("https://my.mirror.example/v2")
	if len(got) != 3 {
		t.Fatalf("应为 3 个源站，实际 %d：%v", len(got), got)
	}
	if got[0] != "https://my.mirror.example/v2" {
		t.Errorf("用户配的地址没排第一：%v", got)
	}

	// 配的和默认一样时不能重复出现，否则同一个死掉的地址会被试两次，白白多等一轮超时。
	got = marketBases(DefaultMarketBase)
	if len(got) != 2 || got[0] != DefaultMarketBase || got[1] != MirrorMarketBase {
		t.Errorf("默认地址应去重，实际 %v", got)
	}

	// 空 / 空格也要能收敛到默认，别拼出 " /search" 这种 URL。
	if b := NormalizeMarketBase("  "); b != DefaultMarketBase {
		t.Errorf("空白地址应回落默认，实际 %q", b)
	}
	if b := NormalizeMarketBase(DefaultMarketBase + "/"); b != DefaultMarketBase {
		t.Errorf("尾部斜杠应去掉，实际 %q", b)
	}
}

func TestMarketSourcePinIgnoresConfiguredBase(t *testing.T) {
	// 选了「仅官方 / 仅镜像」就必须只听那条线路 —— 这是用户用来判断
	// 「到底是官方不通还是镜像坏了」的手段。偷偷兜底就永远测不出来。
	dead := "https://127.0.0.1:1/v2"

	official := marketBasesFor(dead, SourceOfficial)
	if len(official) != 1 || official[0] != DefaultMarketBase {
		t.Errorf("仅官方 应只含官方，实际 %v", official)
	}
	mirror := marketBasesFor(dead, SourceMirror)
	if len(mirror) != 1 || mirror[0] != MirrorMarketBase {
		t.Errorf("仅镜像 应只含镜像，实际 %v", mirror)
	}
	// 默认（含空串和未知值）走兜底链，且用户的地址排第一。
	for _, src := range []string{"", SourceAuto, "随便写的"} {
		got := marketBasesFor(dead, src)
		if len(got) != 3 || got[0] != dead {
			t.Errorf("线路 %q 应回落兜底链且用户地址在前，实际 %v", src, got)
		}
	}
}

func TestMarketSearchReturnsBrowsableListWithoutKeyword(t *testing.T) {
	skipIfShort(t)
	// 用户抱怨的核心：「进来全是空的只有搜索框」。
	// 所以不带关键词也必须能列出东西 —— 这条断言就是那句话的判据。
	hits, used, err := MarketSearch(MarketQuery{Sort: SortDownloads, Limit: 10})
	if err != nil {
		t.Fatalf("无关键词列榜单失败：%v", err)
	}
	if len(hits) == 0 {
		t.Fatal("无关键词时列不出任何东西（用户会看到一个空页面）")
	}
	if used == "" {
		t.Error("没回传实际命中的源站，前端就没法告诉用户这次走了哪条路")
	}
	for i, h := range hits {
		if h.ID == "" || h.Title == "" {
			t.Errorf("第 %d 条缺 id 或标题：%+v", i, h)
		}
	}
	// 热门榜必须真的按下载量降序，否则「热门」两个字是骗人的。
	for i := 1; i < len(hits); i++ {
		if hits[i-1].Downloads < hits[i].Downloads {
			t.Fatalf("热门榜没按下载量降序：%d(%d) < %d(%d)",
				i-1, hits[i-1].Downloads, i, hits[i].Downloads)
		}
	}
}

func TestMarketSearchFallsBackWhenFirstSourceDead(t *testing.T) {
	skipIfShort(t)
	// 第一个源站指向一个必然连不上的地址（端口 1，连接会被立刻拒绝，测试不会慢）。
	// 断言仍然拿得到结果 —— 这就是「PCL 能搜、我们不能搜」的修复点：
	// 官方那次超时之后，得有下一条路，而不是直接把错误甩给用户。
	dead := "https://127.0.0.1:1/v2"
	hits, used, err := MarketSearch(MarketQuery{Base: dead, Query: "sodium", Sort: SortRelevance, Limit: 5})
	if err != nil {
		t.Fatalf("首源失效后没兜住：%v", err)
	}
	if len(hits) == 0 {
		t.Fatal("兜底源没返回任何结果")
	}
	if used == dead {
		t.Fatalf("还在用那个死掉的地址，说明兜底根本没生效：%s", used)
	}
}

func TestMarketSearchAllSourcesDeadReportsActionableError(t *testing.T) {
	skipIfShort(t)
	// 把兜底链也换成连不上的地址，制造「全都试过还失败」——
	// 这是唯一能测到那条错误信息的方式（否则总有一个真源站会答上来）。
	old := MarketFallbacks
	dead2 := "https://127.0.0.1:2/v2"
	MarketFallbacks = []string{dead2}
	defer func() { MarketFallbacks = old }()

	_, _, err := MarketSearch(MarketQuery{Base: "https://127.0.0.1:1/v2", Query: "sodium", Limit: 5})
	if err == nil {
		t.Fatal("源站全挂竟然没报错")
	}
	// 错误信息必须说清「试过几个、是哪几个」。只说「连不上市场」等于把用户丢在原地。
	msg := err.Error()
	for _, want := range []string{"已依次试过", "https://127.0.0.1:1/v2", dead2} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息缺少 %q：%s", want, msg)
		}
	}
}

func TestMarketGameVersionsAreRealReleases(t *testing.T) {
	skipIfShort(t)
	// 版本下拉框的候选必须来自源站，而且不能混进快照版 ——
	// 用户选了快照版，绝大多数模组都没有对应构建，等于给他挖坑。
	vs, err := MarketGameVersions("", 12)
	if err != nil {
		t.Fatalf("取版本清单失败：%v", err)
	}
	if len(vs) == 0 {
		t.Fatal("版本清单是空的")
	}
	for _, v := range vs {
		if v == "" {
			t.Error("版本清单里有空串")
		}
		low := strings.ToLower(v)
		for _, snap := range []string{"snapshot", "pre", "rc"} {
			if strings.Contains(low, snap) {
				t.Errorf("版本 %q 看起来是预览版，不该进下拉框", v)
			}
		}
	}
	t.Logf("版本清单前 6 个：%v", vs[:min(6, len(vs))])
}

func TestCuratedCategoriesUsable(t *testing.T) {
	// 名单里的 key 必须真能查到东西 —— 否则就是「能点但永远空」的死按钮。
	// （这个项目踩过一模一样的坑：前端另写一份名单，点进去永远是空的。）
	cats := CuratedCategories()
	if len(cats) == 0 {
		t.Fatal("分类名单是空的")
	}
	seen := map[string]bool{}
	for _, c := range cats {
		if c.Key == "" || c.Label == "" {
			t.Errorf("分类缺 key 或 label：%+v", c)
		}
		if seen[c.Key] {
			t.Errorf("分类 key 重复：%s", c.Key)
		}
		seen[c.Key] = true
	}
	skipIfShort(t)
	for _, c := range cats[:3] {
		hits, _, err := MarketSearch(MarketQuery{Category: c.Key, Sort: SortDownloads, Limit: 3})
		if err != nil {
			t.Fatalf("分类 %s 查询失败：%v", c.Key, err)
		}
		if len(hits) == 0 {
			t.Errorf("分类 %s（%s）查不到任何模组 —— 这是个死按钮", c.Key, c.Label)
		}
	}
}

// ── 中文简介 ──────────────────────────────────────────────
// 用户原话「为什么全是英文」。翻译来自 MCIM 的社区译文服务。
//
// hasCJK 已经挪到 zhname.go（中文关键词改写也要用它，且那里的实现覆盖更全）。

func TestMarketTranslateNeverReturnsNil(t *testing.T) {
	// 返回 nil map 会让前端的 Object.keys 直接炸，而错误边界会把整页换掉。
	// 「没有译文」必须是空 map，不是 nil。
	for _, ids := range [][]string{nil, {}, {""}, {"  "}} {
		got := MarketTranslate(ids)
		if got == nil {
			t.Fatalf("MarketTranslate(%v) 返回了 nil map", ids)
		}
		if len(got) != 0 {
			t.Errorf("MarketTranslate(%v) 不该有内容：%v", ids, got)
		}
	}
}

func TestMarketTranslateGivesChinese(t *testing.T) {
	skipIfShort(t)
	// Fabric API：一个确定有社区译文的项目。
	const id = "P7dR8mSH"
	// MCIM 会间歇性超时（实测约 1/4），超时不算失败 —— 但**只要答上来就必须是中文**。
	// 断言写成「答不上来就跳过」，是为了让这个测试在源站抽风时也不假红。
	got := ""
	for i := 0; i < 3 && got == ""; i++ {
		got = MarketTranslate([]string{id})[id]
	}
	if got == "" {
		t.Skip("MCIM 翻译服务这次没答上来（它间歇性超时），跳过")
	}
	if !hasCJK(got) {
		t.Fatalf("译文不是中文：%q", got)
	}
	// 第二次必须一致（走缓存也不该变样）
	if again := MarketTranslate([]string{id})[id]; again != got {
		t.Errorf("同一项目两次翻译结果不一致：%q vs %q", got, again)
	}
}

// ── 依赖（前置）────────────────────────────────────────────
// 版本里的依赖只有 project_id。以前界面只能写「有前置 2」，
// 用户连前置叫什么名字都不知道，更别说装 —— 这不是提示，是谜题。

func TestMarketProjectsResolvesIDsToNames(t *testing.T) {
	// 纯逻辑：没 ID 就别发请求，且必须返回空切片而不是 nil。
	ps, err := MarketProjects("", nil)
	if err != nil {
		t.Fatalf("空 ID 不该报错：%v", err)
	}
	if ps == nil {
		t.Fatal("空 ID 应返回空切片，不是 nil（nil 会序列化成 null）")
	}

	skipIfShort(t)
	ps, err = MarketProjects("", []string{"P7dR8mSH", "P7dR8mSH", "AANobbMI"})
	if err != nil {
		// 官方 / 镜像都可能抽风；连续三次都失败才当作真的坏了
		for i := 0; i < 2 && err != nil; i++ {
			ps, err = MarketProjects("", []string{"P7dR8mSH"})
		}
		if err != nil {
			t.Skipf("源站这次没答上来：%v", err)
		}
	}
	if len(ps) == 0 {
		t.Fatal("一个项目都没查回来")
	}
	for _, p := range ps {
		if p.ID == "" || p.Title == "" {
			t.Errorf("项目信息缺 ID 或标题：%+v", p)
		}
	}
}

func TestBroadenQuery(t *testing.T) {
	// 这几条都是实测过的：下划线版在 Modrinth 搜不到，空格版才搜得到。
	cases := []struct{ in, want string }{
		{"mezz_config", "mezz config"},
		{"curios_api", "curios api"},
		{"just-enough-items", "just enough items"},
		{"a__b", "a b"}, // 连续分隔符压成一个空格，别发出 "a  b"
		{"sodium", ""},  // 没有别的写法 —— 白跑一趟没意义
		{"   ", ""},     // 空词
		{"", ""},        //
	}
	for _, c := range cases {
		if got := broadenQuery(c.in); got != c.want {
			t.Errorf("broadenQuery(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 已安装模组的依赖声明给的是 modId（"mezz_config"），而 Modrinth 的搜索
// 遇到下划线会直接落空。不补这一刀的话，用户点「选版本装」看到的是空列表 ——
// 也就是他抱怨的「点了没反应」。这个测试用假源站复现源站的真实行为。
func TestMarketSearchBroadensUnderscoreModID(t *testing.T) {
	old := MarketFallbacks
	MarketFallbacks = nil // 只准打我这台假源站，别偷偷上网把测试变成薛定谔
	defer func() { MarketFallbacks = old }()

	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		got = append(got, q)
		w.Header().Set("Content-Type", "application/json")
		if q == "mezz_config" {
			_, _ = io.WriteString(w, `{"hits":[]}`) // 源站的真实行为：下划线版一条都不给
			return
		}
		_, _ = io.WriteString(w, `{"hits":[{"project_id":"7tEfOcA7","slug":"mezzconfig",`+
			`"title":"MezzConfig","description":"Simple configuration library","project_type":"mod"}]}`)
	}))
	defer srv.Close()

	hits, _, err := MarketSearch(MarketQuery{Base: srv.URL, Query: "mezz_config", Loader: "forge", MC: "1.20.1"})
	if err != nil {
		t.Fatalf("搜索失败：%v", err)
	}
	if len(hits) != 1 || hits[0].Title != "MezzConfig" {
		t.Fatalf("下划线版 modId 没能搜到项目：%+v", hits)
	}
	want := []string{"mezz_config", "mezz config"}
	if len(got) != len(want) {
		t.Fatalf("请求了 %d 次，期望 %d 次（原词 + 放宽词）：%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("第 %d 次请求的关键词是 %q，期望 %q", i+1, got[i], want[i])
		}
	}
}

// 原词就有命中时不许再打一次源站 —— 那是白花的往返，也是没必要的扰动。
func TestMarketSearchNoBroadenRetryWhenHitsExist(t *testing.T) {
	old := MarketFallbacks
	MarketFallbacks = nil
	defer func() { MarketFallbacks = old }()

	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hits":[{"project_id":"A","slug":"a","title":"A","project_type":"mod"}]}`)
	}))
	defer srv.Close()

	hits, _, err := MarketSearch(MarketQuery{Base: srv.URL, Query: "mezz_config", Limit: 5})
	if err != nil {
		t.Fatalf("搜索失败：%v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("应该拿到 1 条，实得 %d 条", len(hits))
	}
	if n != 1 {
		t.Errorf("原词已命中却打了 %d 次源站，应该只打 1 次", n)
	}
}

// 描述里的 Markdown 必须洗掉再给界面。
//
// 这条规格的存在理由：这个 bug **断言查不出来**。接口返回的 JSON 完全合法、
// 字段一个不少，只有把界面截下来看，才会发现列表里印着「** 这不是模组体验包」。
// 所以只能把「不许有标记」写成规格，让它以后有人加搜索源时跑一遍。
func TestMarketDescriptionIsPlainText(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"双星号加粗", "** 这不是模组体验包，直接开战。", "这不是模组体验包，直接开战。"},
		{"成对加粗", "A **very** good pack", "A very good pack"},
		{"双下划线加粗", "__重要__ 说明", "重要 说明"},
		{"行内代码", "需要 `fabric-api` 一起装", "需要 fabric-api 一起装"},
		{"Markdown 链接", "见 [官网](https://example.com/a) 说明", "见 官网 说明"},
		{"行首标题号", "## 简介\n这是一个包", "简介\n这是一个包"},
		{"多余空白收拢", "**  两个  空格  **", "两个 空格"},
		// 真实撞上的一条（Modrinth 热门榜，BattleArmory TACZ）。作者在句尾插了一对
		// 不成对的 **，原样印出来就是列表里挂两个星号。取样 100 条热门整合包里
		// 有 1 条是这种情况 —— 不常见，但撞上了就是「这软件做的什么玩意」。
		{
			"真实样本：句尾多余的双星号",
			"厌倦了每次开局都是一样的钻石剑和弓？开局就是 80 帧+丝滑刚枪。** 这不是模组体验卡，这是完全免费的 1.20.1 战术枪战整合包，下载启动，直接开战。",
			"厌倦了每次开局都是一样的钻石剑和弓？开局就是 80 帧+丝滑刚枪。 这不是模组体验卡，这是完全免费的 1.20.1 战术枪战整合包，下载启动，直接开战。",
		},
		// ⚠ 这条是**护栏**，不是随手加的用例：模组 id 里全是单下划线，
		// 把单下划线当斜体吃掉会把名字改坏，而「改坏」不报错，只表现为用户搜不到。
		{"单下划线必须原样保留", "依赖 mezz_config 与 fabric_api", "依赖 mezz_config 与 fabric_api"},
		{"单个星号必须原样保留", "支持 1.20.1* 版本", "支持 1.20.1* 版本"},
		{"干净文本不动", "A simple vanilla+ pack", "A simple vanilla+ pack"},
		{"空串", "", ""},
	}
	for _, c := range cases {
		if got := plainText(c.in); got != c.want {
			t.Errorf("%s：plainText(%q) = %q，期望 %q", c.name, c.in, got, c.want)
		}
	}
}

// 洗标记这一步必须真的接在搜索结果上 —— 光测 plainText 函数不够：
// 函数对了但忘了在构造 MarketHit 时调用，界面照样是脏的。
func TestMarketSearchStripsMarkdownFromHits(t *testing.T) {
	old := MarketFallbacks
	MarketFallbacks = nil
	defer func() { MarketFallbacks = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"hits":[{
			"project_id":"A","slug":"a","title":"**Big** Pack","project_type":"modpack",
			"description":"** 这不是模组体验包，依赖 mezz_config 一起装。"
		}]}`)
	}))
	defer srv.Close()

	hits, _, err := MarketSearch(MarketQuery{Base: srv.URL, Query: "pack", Limit: 5})
	if err != nil {
		t.Fatalf("搜索失败：%v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("应该拿到 1 条，实得 %d 条", len(hits))
	}
	if strings.Contains(hits[0].Description, "**") {
		t.Errorf("描述里的加粗标记原样发给界面了：%q", hits[0].Description)
	}
	if strings.Contains(hits[0].Title, "**") {
		t.Errorf("标题里的加粗标记原样发给界面了：%q", hits[0].Title)
	}
	// 洗标记不能顺手把下划线也吃掉 —— 模组 id 就是这么写的
	if !strings.Contains(hits[0].Description, "mezz_config") {
		t.Errorf("清洗把模组 id 改坏了：%q", hits[0].Description)
	}
}
