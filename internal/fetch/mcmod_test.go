package fetch

import (
	"strings"
	"testing"
)

// 真实结构（2026-10-09 从 https://www.mcmod.cn/static/public/json/relation/class/yCuvlBxnGhqkdtET.json
// 实测抄下来的，钠/Sodium 的星图）。样本里刻意保留了四处「凭印象一定会写错」的地方：
//
//	① 分类色在 itemStyle.normal.color，**不是**顶层 color；
//	② **有一个分类的 name 是 null**（就是「中心」那一类）；
//	③ id / category / source / target **都是数字**；
//	④ content 是 **HTML 片段**，带 <span class="common-text-title">概述</span>。
const mcmodSampleGraph = `{
  "categories": [
    {"name":"前置模组","itemStyle":{"normal":{"color":"#ce4844"}}},
    {"name":"拓展模组","itemStyle":{"normal":{"color":"#1b809e"}}},
    {"name":"联动模组","itemStyle":{"normal":{"color":"#666666"}}},
    {"name":null,"itemStyle":{"normal":{"color":"#13406C"}}}
  ],
  "nodes": [
    {"id":0,"name":"钠 (Sodium)","symbolSize":40,"category":3},
    {"id":1,"classID":"3413","name":"铟 (Indium)","content":"<span class=\"common-text-title common-text-title-1\">概述</span>铟（Indium）是渲染优化模组 钠 (Sodium) 的附属模组。","symbolSize":20,"category":1,"deep":1},
    {"id":2,"classID":"3551","name":"[BSVSB]更好的钠视频设置按钮 (Better Sodium Video Settings Button)","symbolSize":20,"category":1,"deep":1},
    {"id":3,"classID":"1001","name":"Fabric API","symbolSize":18,"category":0,"deep":1},
    {"id":4,"classID":"1002","name":"Iris Shaders","symbolSize":22,"category":2,"deep":1}
  ],
  "links": [
    {"source":0,"target":1},
    {"source":0,"target":2},
    {"source":0,"target":3},
    {"source":0,"target":4},
    {"source":1,"target":0}
  ]
}`

func TestParseMcmodGraphBasic(t *testing.T) {
	r, err := parseMcmodGraph(mcmodSampleGraph, "2785")
	if err != nil {
		t.Fatalf("该解出来，报错：%v", err)
	}
	if len(r.Nodes) != 5 {
		t.Fatalf("该有 5 个节点，实际 %d：%+v", len(r.Nodes), r.Nodes)
	}
	if r.Center == nil {
		t.Fatal("center 不该为空")
	}
	if r.Center.Name != "钠 (Sodium)" {
		t.Errorf("中心应是「钠 (Sodium)」，实际 %q", r.Center.Name)
	}
	// 中心节点 JSON 里没有 classID，必须用搜索拿到的 classID 回填 —— 否则中心点点不进百科。
	if r.Center.ClassID != "2785" {
		t.Errorf("中心节点该回填搜索拿到的 classID=2785，实际 %q", r.Center.ClassID)
	}
	// 中心排在最前（前端按数组顺序画）
	if r.Nodes[0].ID != "0" {
		t.Errorf("中心节点该沉到数组最前，第一个是 %q", r.Nodes[0].ID)
	}
	// 中心的分类名是 null → 我们补成「中心」，绝不留空
	if r.Center.Cat != "中心" {
		t.Errorf("中心分类该补成「中心」（站方给的是 null），实际 %q", r.Center.Cat)
	}
	// 分类色必须从 itemStyle.normal.color 抠出来
	if r.Center.Color != "#13406C" {
		t.Errorf("中心该拿到分类色 #13406C（来自 itemStyle.normal.color），实际 %q", r.Center.Color)
	}
}

func TestParseMcmodGraphCategoryAndColor(t *testing.T) {
	r, _ := parseMcmodGraph(mcmodSampleGraph, "2785")
	byID := map[string]McmodNode{}
	for _, n := range r.Nodes {
		byID[n.ID] = n
	}
	// category:1 → 拓展模组（#1b809e）
	if byID["1"].Cat != "拓展模组" {
		t.Errorf("节点 1 该是「拓展模组」，实际 %q", byID["1"].Cat)
	}
	if byID["1"].Color != "#1b809e" {
		t.Errorf("节点 1 该拿到拓展色 #1b809e，实际 %q", byID["1"].Color)
	}
	// category:0 → 前置模组
	if byID["3"].Cat != "前置模组" {
		t.Errorf("节点 3 该是「前置模组」，实际 %q", byID["3"].Cat)
	}
	// category:2 → 联动模组
	if byID["4"].Cat != "联动模组" {
		t.Errorf("节点 4 该是「联动模组」，实际 %q", byID["4"].Cat)
	}
	// classID 要留着（能点进百科）
	if byID["1"].ClassID != "3413" {
		t.Errorf("节点 1 的 classID 该是 3413，实际 %q", byID["1"].ClassID)
	}
}

// content 是 HTML，必须剥成纯文本，且不能把「概述」这种小标题留在里面。
func TestParseMcmodGraphStripsHTMLContent(t *testing.T) {
	r, _ := parseMcmodGraph(mcmodSampleGraph, "2785")
	var indium *McmodNode
	for i := range r.Nodes {
		if r.Nodes[i].ID == "1" {
			indium = &r.Nodes[i]
		}
	}
	if indium == nil {
		t.Fatal("找不到节点 1")
	}
	if strings.Contains(indium.Content, "<") || strings.Contains(indium.Content, ">") {
		t.Errorf("content 该剥掉 HTML 标签，实际 %q", indium.Content)
	}
	if !strings.Contains(indium.Content, "铟（Indium）") {
		t.Errorf("content 该保留正文文字「铟（Indium）」，实际 %q", indium.Content)
	}
}

func TestParseMcmodGraphLinks(t *testing.T) {
	r, _ := parseMcmodGraph(mcmodSampleGraph, "2785")
	// 5 条原始边里 {"source":1,"target":0} 是 0→1 的反向重复，去重后应剩 4 条。
	if len(r.Links) != 4 {
		t.Fatalf("该去重成 4 条边（正反算一条），实际 %d：%+v", len(r.Links), r.Links)
	}
	for _, l := range r.Links {
		if l.Source == l.Target {
			t.Errorf("不该有自环：%+v", l)
		}
	}
	// 数字下标要解析成节点 id 串（"0" 而不是 0）
	found := false
	for _, l := range r.Links {
		if l.Source == "0" && l.Target == "1" {
			found = true
		}
	}
	if !found {
		t.Errorf("该有 0→1 这条边（数字下标应解析成 id 串），实际 %+v", r.Links)
	}
}

// 中心选择：deep==0 优先；都没有就用 symbolSize 最大的。
func TestParseMcmodGraphCenterFallback(t *testing.T) {
	// 没有任何 deep 字段 → 退回 symbolSize 最大者
	g := `{
	  "categories":[{"name":"a","itemStyle":{"normal":{"color":"#111111"}}}],
	  "nodes":[
	    {"id":0,"name":"小","symbolSize":10,"category":0},
	    {"id":1,"name":"大","symbolSize":99,"category":0}
	  ],
	  "links":[]
	}`
	r, err := parseMcmodGraph(g, "1")
	if err != nil {
		t.Fatalf("该解出来：%v", err)
	}
	if r.Center.Name != "大" {
		t.Errorf("没有 deep 时该选 symbolSize 最大的当中心，实际 %q", r.Center.Name)
	}
}

// 站方改版 / 空数据时：报错，**不 panic、不画空图**。
func TestParseMcmodGraphRejectsGarbage(t *testing.T) {
	for _, raw := range []string{
		"",
		"不是 JSON",
		`{"categories":[],"nodes":[],"links":[]}`, // 没有节点 = 没有图
	} {
		if _, err := parseMcmodGraph(raw, "1"); err == nil {
			t.Errorf("喂 %q 该报错，实际没报", raw)
		}
	}
}

// data-id 的两种属性顺序都要认得（实测页面上是 id 在前，但别赌它永远如此）。
func TestMcmodDataID(t *testing.T) {
	cases := map[string]string{
		`<button id="class-relation-chart-btn" data-id="yCuvlBxnGhqkdtET">`                                            : "yCuvlBxnGhqkdtET",
		`<button data-id="Abc_12-x" class="btn" id="class-relation-chart-btn">`                                       : "Abc_12-x",
		`<div>没有按钮</div>`                                                                                          : "",
	}
	for page, want := range cases {
		if got := mcmodDataID(page); got != want {
			t.Errorf("mcmodDataID(%q) = %q，想要 %q", page, got, want)
		}
	}
}

// GBK → UTF-8：站方返回 GBK，直接当 UTF-8 会得到「閽� (Sodium)」这种乱码。
func TestDecodeMcmodBytesGBK(t *testing.T) {
	// "钠 (Sodium)" 的 GBK 编码
	gbk := []byte{0xC4, 0xC6, 0x20, 0x28, 0x53, 0x6F, 0x64, 0x69, 0x75, 0x6D, 0x29}
	got := decodeMcmodBytes(gbk)
	if got != "钠 (Sodium)" {
		t.Errorf("GBK 该被正确转成 UTF-8，实际 %q", got)
	}
	// 合法 UTF-8 不该被误转
	utf8 := []byte("钠 (Sodium)")
	if got := decodeMcmodBytes(utf8); got != "钠 (Sodium)" {
		t.Errorf("合法 UTF-8 该原样返回，实际 %q", got)
	}
}

// 磁盘缓存：写进去能读出来，键名安全（中文/空格不炸文件名）。
func TestMcmodDiskCache(t *testing.T) {
	dir := t.TempDir()
	SetMcmodCacheDir(dir)
	defer SetMcmodCacheDir("")

	key := mcmodKey("机械 动力 Create")
	r := &McmodRelations{
		Center: &McmodNode{ID: "0", Name: "机械动力"},
		Nodes:  []McmodNode{{ID: "0", Name: "机械动力"}},
		Links:  []McmodLink{},
		Cats:   []McmodCat{{Name: "中心", Color: "#13406C"}},
	}
	mcmodWriteDisk(key, r)

	got := mcmodReadDisk(key)
	if got == nil {
		t.Fatal("写进去的缓存该能读出来")
	}
	if got.Center == nil || got.Center.Name != "机械动力" {
		t.Errorf("读出来的中心和写进去的不一致：%+v", got.Center)
	}
	if !got.Cached {
		t.Error("从磁盘读出的结果 Cached 该为 true")
	}

	// 不存在的键 → nil，不 panic
	if mcmodReadDisk(mcmodKey("不存在的模组")) != nil {
		t.Error("不存在的键该返回 nil")
	}
}

// 「📖简介」前缀：站方把详情页小节标题连着正文塞进 content 的另一种形态，剥标签后
// 就成了正文开头的 "📖简介xxx"。面板自带「简介」小标题，不剥就是「简介 / 📖简介xxx」
// 顶着重复上屏，还违反项目禁 emoji 的规矩。
// 2026-10-10 实测：[FC]Forge Config API Port 节点 content 原文就是 `📖简介Forge Config…`。
func TestMcmodTrimLeadEmojiPrefix(t *testing.T) {
	cases := []struct{ in, want string }{
		{"📖简介Forge Config API Port 在 Fabric 构建了", "Forge Config API Port 在 Fabric 构建了"},
		{"📖 简介：这是正文", "这是正文"},
		{"📖官方说明分页", "官方说明分页"},                      // 有 emoji 没「简介」：emoji 一样不留
		{"概述在 附魔书 的工具提示中显示", "概述在 附魔书 的工具提示中显示"}, // 正常标题原样保留
		{"没有前缀的正文", "没有前缀的正文"},
		{"  📖简介  前导空格也认  ", "前导空格也认"},
	}
	for _, c := range cases {
		if got := mcmodTrimLead(c.in); got != c.want {
			t.Errorf("mcmodTrimLead(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
	// 幂等：剥过的再剥一遍不变（读盘补刀就靠这条性质）
	once := mcmodTrimLead("📖简介正文开始")
	if twice := mcmodTrimLead(once); twice != once {
		t.Errorf("重复清洗该幂等：%q → %q", once, twice)
	}
}

// 走完整管线：HTML 形态的「📖简介」小标题，剥标签后也必须被清掉（修复前它会原样上屏）。
func TestMcmodPlainTextStripsEmojiLead(t *testing.T) {
	got := mcmodPlainText(`<span class="common-text-title">📖简介</span>Forge Config API Port 在 Fabric 构建了一整个 Forge 配置 API。`)
	if strings.Contains(got, "📖") || strings.HasPrefix(got, "简介") {
		t.Errorf("「📖简介」前缀该被剥掉，实际 %q", got)
	}
	if !strings.HasPrefix(got, "Forge Config API Port") {
		t.Errorf("正文该原样保留，实际 %q", got)
	}
}
