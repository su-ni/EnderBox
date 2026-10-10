package fetch

import (
	"strings"
	"testing"
)

// TestSourcesAlwaysExplainThemselves 这份清单是「新建世界」那页的真相源：
// 用户看到的就是这里的 Label / Note。两条规矩：
//
//  1. 每个类型都要有一段说明 —— 空着的话用户只知道「有这么个东西」，
//     不知道它能不能用、为什么不能用；
//  2. Available=false 的**必须说清卡在哪**。这是这个项目的下限：
//     没接上不是问题，没接上却不吭声才是问题。
//
// 这条测试的直接起因：BungeeCord 那条曾经写着「源站实测很慢（26 MB 两分钟只跑到 9.9 MB）」。
// 2026-10-06 连测三次，26,175,295 字节稳定在 11 秒左右、与 Content-Length 逐字节一致 ——
// 那句「很慢」是错的。用户看到它会以为「这端接不了」，而真正的原因（滚动构建、
// 没有游戏版本概念，新建向导的表单不成立）反而没写。
//
// 所以除了「非空」，还要挡住「拿一句网络理由当挡箭牌」这种写法。
func TestSourcesAlwaysExplainThemselves(t *testing.T) {
	srcs := Sources()
	if len(srcs) == 0 {
		t.Fatal("清单是空的 —— 新建世界那页会没有类型可选")
	}
	seen := map[Kind]bool{}
	for _, s := range srcs {
		if s.Type == "" {
			t.Error("有类型没写 type")
		}
		if seen[s.Type] {
			t.Errorf("%s 出现了两次 —— 前端按 type 做 key，会漏掉一个", s.Type)
		}
		seen[s.Type] = true
		if strings.TrimSpace(s.Label) == "" {
			t.Errorf("%s 没有 Label，界面上会显示成空白", s.Type)
		}
		if strings.TrimSpace(s.Note) == "" {
			t.Errorf("%s 没有说明 —— 用户不知道它能不能用", s.Type)
		}
		// 洞：某个 %s 没填上会留下连续空格
		if strings.Contains(s.Note, "  ") {
			t.Errorf("%s 的说明里有连续空格，八成是一个没填上的名字：%q", s.Type, s.Note)
		}
		if !s.Available {
			// 「不可用」必须给出可操作的原因，不许只说一句不行就完事。
			if len([]rune(s.Note)) < 25 {
				t.Errorf("%s 的不可用说明只有 %d 个字 —— 说清卡在哪，或者直接说怎么办",
					s.Type, len([]rune(s.Note)))
			}
			// ⚠ 不许拿「很慢」当不可用的理由。
			//
			// 这条是这次踩出来的，写死成规矩：网速结论**会过时**，而它一旦写进界面，
			// 用户就永远失去那个功能。BungeeCord 那条原本是
			// 「源站能连通但实测很慢（26 MB 两分钟只跑到 9.9 MB）」——
			// 回头连测三次：11 秒下完 26 MB，逐字节一致。也就是说整整一段时间里，
			// 界面在用一个假理由拒绝一个完全可以用的端。
			//
			// 真要是通道有问题，写具体现象（「连接被重置」「只解析出 IPv6」），
			// 那种话有据可查、也不会因为某天网络变好就变成谎言。
			// （我自己第一版写的是「提了很慢就得再提点别的」——太客气，旧文案带着
			//  「版本/构建」两个词就绕过去了，等于没测。判据要盯住错法本身。）
			if strings.Contains(s.Note, "很慢") {
				t.Errorf("%s 拿「很慢」当不可用的理由 —— 这类结论会过时，"+
					"而它会让用户白白失去一个能用的端。请写具体现象（连接被重置 / 只解析出 IPv6）"+
					"或者真正卡住的那件事：%q", s.Type, s.Note)
			}
		}
	}
	// 必须认识的类型一个都不能漏。少了哪个，前端那个选项就会凭空消失。
	for _, k := range []Kind{
		KindVanilla, KindPaper, KindFabric, KindForge, KindNeoForge,
		KindVelocity, KindBungeeCord, KindMohist, KindArclight,
	} {
		if !seen[k] {
			t.Errorf("清单里少了 %s —— 「新建世界」那里就选不到它了", k)
		}
	}
}
