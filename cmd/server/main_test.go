package main

import "testing"

// displayURL 的规格表。
//
// 这条的来历：日志原来写 `"http://localhost" + addr`，默认 addr 是 ":23333"
// 所以一直没人发现问题；一旦像 `-addr 127.0.0.1:23456` 这样显式传 host，
// 就打印出 `http://localhost127.0.0.1:23456` —— 用户照着抄是打不开的，
// 而且这种「看着像地址的乱码」比直接报错更容易让人绕远路。
//
// 前三条是「所有网卡」形式（浏览器打不开，必须换 localhost）；
// 后两条是启动方显式指定的 host，必须原样保留 —— 硬套 localhost 就是 bug 本身。
func TestDisplayURLSpec(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{":23333", "http://localhost:23333"},
		{"0.0.0.0:23333", "http://localhost:23333"},
		{"[::]:23333", "http://localhost:23333"},

		{"127.0.0.1:23456", "http://127.0.0.1:23456"},
		{"192.168.1.5:23333", "http://192.168.1.5:23333"},
	}
	for _, c := range cases {
		if got := displayURL(c.addr); got != c.want {
			t.Errorf("displayURL(%q) = %q，想要 %q", c.addr, got, c.want)
		}
	}
}

// 旧实现的拼接方式单独立一条：确保这个测试真的抓得住回归。
// 把 "http://localhost" 前缀无条件贴上去，这三个就必须是全红的。
func TestDisplayURLRejectsBlindLocalhostPrefix(t *testing.T) {
	blind := func(addr string) string { return "http://localhost" + addr }

	for _, addr := range []string{"127.0.0.1:23456", "192.168.1.5:23333"} {
		if blind(addr) == displayURL(addr) {
			t.Fatalf("addr=%q：displayURL 和「无脑贴 localhost」的旧实现结果一样（%q），说明这个用例挡不住回归",
				addr, blind(addr))
		}
	}
}
