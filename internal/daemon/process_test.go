package daemon

import "testing"

// 样本全部逐字抄自 2026-10-04 真机 stdout（F:/sbx-realrun/logs/i1791098663892.log）。
// 别按想象手写——这项目的上一轮就栽在「样例是编的，单测全绿、真机全错」上。
func TestStripANSIMatchesRealPaperStdout(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "WARN 带 33;1m 加粗黄",
			in:   "\x1b[33;1m[15:26:42 WARN]: YOU ARE RUNNING THIS SERVER AS AN ADMINISTRATIVE OR ROOT USER. THIS IS NOT ADVISED.",
			want: "[15:26:42 WARN]: YOU ARE RUNNING THIS SERVER AS AN ADMINISTRATIVE OR ROOT USER. THIS IS NOT ADVISED.",
		},
		{
			name: "空参数复位 \\x1b[m 跟在行首",
			in:   "\x1b[m[15:26:42 INFO]: Loading properties",
			want: "[15:26:42 INFO]: Loading properties",
		},
		{
			name: "复位+粗体+下划线+亮红，就是用户看到 [91m 那一行",
			in:   "\x1b[m\x1b[37m\x1b[4m\x1b[91m/help\x1b[0m\x1b[3m\x1b[91m<--[HERE]",
			want: "/help<--[HERE]",
		},
		{
			name: "多个码连排且都在行中间",
			in:   "abc\x1b[93mdef\x1b[0m\x1b[97mghi",
			want: "abcdefghi",
		},
		{
			name: "不含 ESC 的行原样返回",
			in:   "[17:58:30] [Server thread/INFO]: Done (13.447s)! For help, type \"help\"",
			want: "[17:58:30] [Server thread/INFO]: Done (13.447s)! For help, type \"help\"",
		},
		{
			name: "OSC（改窗口标题）也要吃掉",
			in:   "\x1b]0;Paper\x07[15:26:42 INFO]: ok",
			want: "[15:26:42 INFO]: ok",
		},
		{
			name: "不吞正文：[] 与数字不能被误伤",
			in:   "[15:26:42 INFO]: Server Ping Player Sample Count: 12",
			want: "[15:26:42 INFO]: Server Ping Player Sample Count: 12",
		},
	}
	for _, c := range cases {
		if got := stripANSI(c.in); got != c.want {
			t.Errorf("%s:\n  输入 %q\n  得到 %q\n  想要 %q", c.name, c.in, got, c.want)
		}
	}
}

// 服务端控制台不认前导斜杠（真机实测：help 成功、/help 报 Unknown or incomplete command）。
func TestNormalizeCommandStripsOneLeadingSlash(t *testing.T) {
	cases := []struct{ in, want string }{
		{"help", "help"},
		{"/help", "help"},
		{"  /list  ", "list"},
		{"say 你好", "say 你好"},
		{"/say 你好", "say 你好"},
		// 只剥一层：剩下的交回给服务端去抱怨，比我们猜用户意图诚实
		{"//help", "/help"},
		{"/", ""},
		{"   ", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizeCommand(c.in); got != c.want {
			t.Errorf("normalizeCommand(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
}

// 控制台提示符与回车符。样本逐字抄自 /api/instances/{id}/logs/history 的真实返回。
//
// 背景：Paper 输出前先写提示符 "> "，再用 "\r  \r" 擦掉。\r 不换行，
// 所以按 \n 切出来的一行带着 "> \r  \r" 前缀 —— 行首不是 [，前端按
// "^[HH:MM:SS LEVEL]" 解析级别的正则就全部落空，整屏日志都变成 LOG 级别。
func TestNormalizeConsoleLineDropsPromptAndHandlesCarriageReturn(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			name: "提示符 + 两次回车（真机原样）",
			in:   "> \r  \r[18:14:45 INFO]: /weather: A Mojang provided command.",
			want: "[18:14:45 INFO]: /weather: A Mojang provided command.",
		},
		{
			name: "只剩提示符的残行要丢掉",
			in:   "> ",
			want: "",
		},
		{
			name: "擦除用的空格行也要丢掉",
			in:   "  ",
			want: "",
		},
		{
			name: "提示符带回车但后面没内容",
			in:   "> \r  \r",
			want: "",
		},
		{
			name: "颜色码与提示符混在一起",
			in:   "\x1b[33;1m> \r\x1b[m\x1b[33;1m[15:26:42 WARN]: YOU ARE RUNNING THIS SERVER AS ROOT",
			want: "[15:26:42 WARN]: YOU ARE RUNNING THIS SERVER AS ROOT",
		},
		{
			name: "干净的行原样通过",
			in:   "[17:58:30] [Server thread/INFO]: Done (13.447s)! For help, type \"help\"",
			want: "[17:58:30] [Server thread/INFO]: Done (13.447s)! For help, type \"help\"",
		},
		{
			name: "我们自己的回显行不受影响",
			in:   "[EnderBox] > help",
			want: "[EnderBox] > help",
		},
		{
			name: "以 > 开头的正文不能被误杀",
			in:   "> 12345 个方块已放置",
			want: "> 12345 个方块已放置",
		},
	}
	for _, c := range cases {
		// 走和 pump 里同样的两步，顺序也要一致
		if got := normalizeConsoleLine(stripANSI(c.in)); got != c.want {
			t.Errorf("%s:\n  输入 %q\n  得到 %q\n  想要 %q", c.name, c.in, got, c.want)
		}
	}
}
