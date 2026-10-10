package api

import (
	"strings"
	"testing"

	"github.com/su-ni/EnderBox/internal/fetch"
)

// TestJarResolversCoverEveryAvailableKind 这条测试守的是一个**具体的、已经发生过的** bug。
//
// 「下服务端文件」这条链路上原本有三个并列的 switch：取版本列表、取构建列表、真正解析下载。
// 前两个加类型时顺手就补了，第三个最容易被忘 —— 忘了**不会编译报错**，
// 表现是：后端 Sources() 说这个类型「可用」，新建向导里能选、能选版本、能选构建，
// 点下「创建」之后回一句「XX 还不能自动下载」。
// 也就是**把「我们忘了写分支」说成「上游不支持」**，用户完全没有办法自己搞清楚。
//
// 2026-10-06 真踩到了：Mohist / Arclight 的版本与构建都能取，只有下载那一段没有分支。
// 现在第三个 switch 换成了 jarResolvers 这张表，于是「有没有漏类型」成了一个
// **能被问出来的问题** —— 就是下面这段。
func TestJarResolversCoverEveryAvailableKind(t *testing.T) {
	for _, s := range fetch.Sources() {
		if !s.Available {
			continue // 标了不可用的，本来就不该有下载实现
		}
		fn, ok := jarResolvers[s.Type]
		if !ok {
			t.Errorf("类型 %s 在 Sources() 里写着可用，jarResolvers 里却没有它 —— "+
				"用户会在向导里选到它，点下载却被告知「还不能自动下载」", s.Type)
			continue
		}
		if fn == nil {
			t.Errorf("类型 %s 在 jarResolvers 里是 nil —— 一个 nil 分支比没有分支更难查", s.Type)
		}
	}
}

// TestJarResolversHaveNoUnknownKind 反向：表里不许有 Sources() 都不认识的类型。
// 拼错的 key 永远不会被命中（dispatch 用的是 Kind 字符串），
// 于是它会一直躺在那儿看着像「已经支持了」，实际等于没有。
func TestJarResolversHaveNoUnknownKind(t *testing.T) {
	if len(jarResolvers) == 0 {
		t.Fatal("jarResolvers 是空的 —— 所有类型都会掉进「还不能自动下载」那条路")
	}
	for k := range jarResolvers {
		if _, ok := fetch.SourceOf(k); !ok {
			t.Errorf("jarResolvers 里的 %q 在 Sources() 里查不到 —— 要么拼错了，要么这个类型已经删了", k)
		}
	}
}

// TestNotAvailableKindsHaveNoResolver 标了不可用的类型**不许**偷偷留一个分支。
// 留着的话，界面上写着「不能下」，代码里却下得动 —— 两边迟早对不上，
// 而且真实原因是设计问题（比如 BungeeCord 没有游戏版本概念，表单根本不成立），
// 拿一个能跑的实现顶上只会掩盖它。
func TestNotAvailableKindsHaveNoResolver(t *testing.T) {
	for _, s := range fetch.Sources() {
		if s.Available {
			continue
		}
		if _, ok := jarResolvers[s.Type]; ok {
			t.Errorf("类型 %s 标着「不可用」，却有一个下载实现 —— "+
				"要么把它翻成可用（并写清为什么现在能用了），要么删掉这个分支", s.Type)
		}
	}
}

// TestResolveByBuildID 公共实现的行为：给了构建号按号取、没给取最新、
// 找不到必须**报错**而不是交回一个零值 Build。
//
// 最后那条是关键：零值 Build 的 URL 是空的，要是被当成成功结果往下走，
// 拿一个空地址去发请求，报出来的错会像「源站挂了」，排查方向从一开始就是错的。
func TestResolveByBuildID(t *testing.T) {
	latest := func(ver string) (fetch.Build, error) {
		return fetch.Build{ID: 999, URL: "https://example.invalid/latest", Label: "latest/" + ver}, nil
	}
	list := func() ([]fetch.Build, error) {
		return []fetch.Build{
			{ID: 10, URL: "https://example.invalid/10"},
			{ID: 20, URL: "https://example.invalid/20"},
		}, nil
	}

	// ① 没给号 → 走「最新」
	b, err := resolveByBuildID("1.20.1", 0, latest, list)
	if err != nil {
		t.Fatalf("没给构建号时不该报错：%v", err)
	}
	if b.ID != 999 || b.Label != "latest/1.20.1" {
		t.Errorf("没给构建号时应该取最新，拿到的是 %+v", b)
	}

	// ② 给了号 → 按号精确取，不许退化成「列表里第几个」
	b, err = resolveByBuildID("1.20.1", 20, latest, list)
	if err != nil {
		t.Fatalf("按号取不该报错：%v", err)
	}
	if b.URL != "https://example.invalid/20" {
		t.Errorf("按号取拿错了：%+v（按序号取会在上游加新构建后悄悄错位）", b)
	}

	// ③ 号不存在 → 报错，且错误里要带上版本和号（否则用户不知道该改哪）
	_, err = resolveByBuildID("1.20.1", 77, latest, list)
	if err == nil {
		t.Fatal("构建号不存在时必须报错 —— 交回零值 Build 会让后面拿空 URL 去下载")
	}
	if !strings.Contains(err.Error(), "1.20.1") || !strings.Contains(err.Error(), "77") {
		t.Errorf("错误信息没带上版本和构建号，用户不知道该改哪一个：%v", err)
	}
}
