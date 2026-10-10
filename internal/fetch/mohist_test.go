package fetch

import (
	"strings"
	"testing"
)

// TestMohistBuildsFromSortsNewestFirst 上游给的是新的在前，但**不依赖它** ——
// 换了个接口顺序就会悄悄反过来，用户以为选的是最新的，其实下到最老的。
func TestMohistBuildsFromSortsNewestFirst(t *testing.T) {
	// 故意倒着塞进去
	raw := []mohistBuild{
		{ID: 471, FileSHA: "aaa"},
		{ID: 921, FileSHA: "bbb"},
		{ID: 917, FileSHA: "ccc"},
	}
	bs, err := mohistBuildsFrom(raw, "1.20.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 3 {
		t.Fatalf("拿到 %d 个构建，想要 3 个", len(bs))
	}
	if got := []int{bs[0].ID, bs[1].ID, bs[2].ID}; got[0] != 921 || got[1] != 917 || got[2] != 471 {
		t.Errorf("排序是 %v，想要 [921 917 471]（新的在前）", got)
	}
}

// TestMohistBuildsFromEmptyIsAnError 空列表必须是**错误**，不是空切片。
//
// 这条盯的是实测踩过的坑：老域名那个废弃入口回的是「HTTP 200 + 零字节」。
// 解析层要是把「解出来是空的」当「这个版本没有构建」，界面就只是显示一个空列表 ——
// 用户既不知道自己撞上了废弃地址，也没有任何可操作的下一步。
func TestMohistBuildsFromEmptyIsAnError(t *testing.T) {
	for _, raw := range [][]mohistBuild{
		nil,
		{},         // 数组是空的
		{{ID: 0}},  // 有行但没有构建号 —— 同样没法回源定位
		{{ID: -1}}, // 负数更不行
	} {
		if _, err := mohistBuildsFrom(raw, "1.20.1", 0); err == nil {
			t.Errorf("输入 %v 时没有报错 —— 空列表被当成「这个版本没有构建」了", raw)
		}
	}
}

// TestMohistBuildOfCarriesCheckAndJava 每个构建都要带校验和、下载地址、Java 需求。
// 少一个校验和，用户下到截断的文件时我们连「是不是源的问题」都判断不了。
func TestMohistBuildOfCarriesCheckAndJava(t *testing.T) {
	var b mohistBuild
	b.ID = 921
	b.FileSHA = "ade8962d51108f70a9facb8a7680990e50768cab83a2559d8bb64889f043471c"
	b.Date = "2026-09-28T23:54:37Z"
	b.Loader.Forge = "47.4.23"

	got := mohistBuildOf("1.20.1", b)
	if got.SHA != b.FileSHA || got.SHAKind != "sha256" {
		t.Errorf("校验和没带上：sha=%q kind=%q", got.SHA, got.SHAKind)
	}
	if !strings.HasSuffix(got.URL, "/project/mohist/1.20.1/builds/921/download") {
		t.Errorf("下载地址拼错了：%s", got.URL)
	}
	if !strings.HasPrefix(got.URL, "https://") {
		t.Errorf("下载地址不是 https：%s", got.URL)
	}
	// 1.20.1 要 Java 17 —— 拿一个偏小的数会让用户下到一个起不来的包
	if got.JavaMajor != 17 {
		t.Errorf("Java 需求算成 %d，1.20.1 想要 17", got.JavaMajor)
	}
	if got.JavaFrom == "" {
		t.Error("Java 需求没标来源，界面上没法如实说明它是权威值还是推断值")
	}
	// 文件名要让侧栏能认出端名（serverLabel 取开头的字母）
	if !strings.HasPrefix(got.Name, "mohist-1.20.1-921") {
		t.Errorf("文件名 %q 里认不出 mohist，侧栏会显示成空白类型", got.Name)
	}
	if got.Label == "" || !strings.Contains(got.Label, "47.4.23") {
		t.Errorf("Label 没把绑定的 Forge 版本写出来：%q（那是各构建真正的差异）", got.Label)
	}
}

// TestMohistLimit 只取最近 N 个。
func TestMohistLimit(t *testing.T) {
	raw := []mohistBuild{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
	bs, err := mohistBuildsFrom(raw, "1.21.1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs) != 2 || bs[0].ID != 4 {
		t.Errorf("limit 没生效：拿到 %d 个，第一个是 %d", len(bs), bs[0].ID)
	}
}
