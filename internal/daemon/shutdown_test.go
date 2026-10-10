package daemon

import (
	"errors"
	"testing"
)

// 本文件盯死一个真实故障：用户在控制台手敲 stop（或 /stop），服务端明明
// 优雅关服了，我们却判成崩溃 —— 存崩溃档、报「已崩溃」、还触发自动重启。
//
// 现场证据（逐字抄自真机原始日志，别按想象手写）：
//
//	F:/sbx-realrun/logs/i1791107861781.log        真实 Forge 1.20.1，用户手敲 stop
//	F:/sbx-realrun/logs/i1791107861781-crash-*.log  同一份日志被判成崩溃存了档
//	F:/sbx-realrun/logs/i1791098663892.log        真实 Paper 1.21.4
//
// 真机里那两行是**连着出现**的，只匹配其中一种就会漏：
//
//	[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping the server
//	[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping server
func TestIsGracefulShutdownLineMatchesRealOutput(t *testing.T) {
	yes := []string{
		"[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping the server",
		"[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping server",
		// 代理端 / fakeserver 的形态（fakeserver 只打这一种，见 tools/fakeserver/FakeServer.java）
		"[04:31:16] [Server thread/INFO]: Stopping the server",
	}
	for _, l := range yes {
		if !isGracefulShutdownLine(l) {
			t.Errorf("该认成关服却没认出来：%q", l)
		}
	}
	no := []string{
		`[18:29:51] [Server thread/INFO] [minecraft/DedicatedServer]: Done (14.198s)! For help, type "help"`,
		"[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Saving worlds",
		"[18:31:28] [Server thread/INFO] [minecraft/MinecraftServer]: ThreadedAnvilChunkStorage: All dimensions are saved",
		// 插件重载不能被误判成关服 —— 所以这里不做大小写 + 子串的模糊匹配
		"[12:00:00] [Server thread/INFO]: Stopping Server Plugin",
		"[12:00:00] [Server thread/INFO]: Reloading server config",
	}
	for _, l := range no {
		if isGracefulShutdownLine(l) {
			t.Errorf("不该认成关服却认了：%q", l)
		}
	}
}

// 收尾判据的完整规格表。这张表定义了「什么情况报崩溃」。
func TestStoppedGracefully(t *testing.T) {
	boom := errors.New("exit status 1")
	cases := []struct {
		name         string
		intentional  bool
		everReady    bool
		waitErr      error
		wantGraceful bool
	}{
		{"用户手敲 stop（Send 置了标记）", true, true, nil, true},
		{"用户点「停止」按钮（Stop 置了标记）", true, true, nil, true},
		{"用户点「强制停止」（Kill 置了标记，退出码非 0）", true, false, boom, true},
		{"服务端自曝关服（track 从日志置的标记）", true, true, nil, true},
		{"被 taskkill /F 打死：非 0 且无人认领", false, true, boom, false},
		{"JVM 自己崩了（非 0）", false, true, boom, false},
		{"服务端起不来就退出（非 0、从没 Ready）", false, false, boom, false},
		{"起来过、退出码 0、无标记 —— 兜底判正常", false, true, nil, true},
		{"压根没起来、退出码 0（eula 被拒那种）", false, false, nil, false},
	}
	for _, c := range cases {
		if got := stoppedGracefully(c.intentional, c.everReady, c.waitErr); got != c.wantGraceful {
			t.Errorf("%s: stoppedGracefully(intentional=%v, everReady=%v, err=%v) = %v，想要 %v",
				c.name, c.intentional, c.everReady, c.waitErr, got, c.wantGraceful)
		}
	}
}

// track 要能从真机日志行里认出这两位。
func TestTrackMarksShutdownAndReadyFromRealLines(t *testing.T) {
	p := newProcess(&Instance{}, NewLogBuffer(16))

	p.track(`[18:29:51] [Server thread/INFO] [minecraft/DedicatedServer]: Done (14.198s)! For help, type "help"`)
	if !p.everReady {
		t.Error("看到 Done ( 之后该记成「起来过」")
	}
	if p.intentional {
		t.Error("光是启动完成不该被当成停机")
	}

	p.track("[18:31:27] [Server thread/INFO] [minecraft/MinecraftServer]: Stopping server")
	if !p.intentional {
		t.Error("服务端自己说 Stopping server 了，该置停机标记")
	}

	// 把两件事合起来 = 本次 bug 的完整现场，必须判成正常停止
	if !stoppedGracefully(p.intentional, p.everReady, nil) {
		t.Error("手敲 stop 的完整现场该判成正常停止，而不是崩溃")
	}
}

// TestReadyLineCoversMohistLocalizedMessage Mohist 把原版那句 `Done (…)` 换成了自己的中文话。
// 下面这行是**真机日志原文**（2026-10-06，Mohist 1.20.1 build 921）。
//
// 只认 "Done (" 的话，Mohist 上的 everReady 永远不成立，
// 「退出码 0 且曾就绪」这条第三层兜底对它就是失效的。
func TestReadyLineCoversMohistLocalizedMessage(t *testing.T) {
	if !isReadyLine("[04:20:03] [Server thread/INFO] [net.minecraft.server.dedicated.DedicatedServer]: " +
		"Mohist 启动成功啦！╰(*°▽°*)╯，总耗时共 11.652s ，您可以使用 /help 命令查看服务器帮助！") {
		t.Error("认不出 Mohist 的就绪行 —— 停服判定的兜底层在 Mohist 上会一直失效")
	}
	// Arclight 打的就是原版那句，别改坏
	if !isReadyLine(`[02:47:48] [Server thread/INFO]: Done (12.249s)! For help, type "help"`) {
		t.Error("原版的 Done ( 认不出来了")
	}
	// ⚠ 反例：光有「启动成功」四个字不算 —— 插件往控制台打一句
	// 「某某插件启动成功」不能把一次崩溃当成正常关服
	// （还要退出码 0 才判正常，但判据本身就不该这么宽）。
	for _, line := range []string{
		"[12:00:00] [Server thread/INFO]: EssentialsX 启动成功",
		"[12:00:00] [Server thread/INFO]: [EnderBoxCompanion] 插件启动成功，开始写快照",
		"[12:00:00] [Server thread/INFO]: 正在启动成功检查",
	} {
		if isReadyLine(line) {
			t.Errorf("插件自己打的「启动成功」被当成就绪行了：%q", line)
		}
	}
}

// 用户手敲的命令走 Send（通用命令通道）。它必须把停机标记置上 ——
// 这条路径原先一个字都没提 intentional，就是本次 bug 的根因。
func TestSendMarksIntentionalForStopCommands(t *testing.T) {
	for _, cmd := range []string{"stop", "/stop", "  /STOP  ", "end", "/end"} {
		p := newProcess(&Instance{}, NewLogBuffer(8))
		// stdin 还没接上，会返回「进程未运行」—— 但标记必须在写命令之前就置上
		_ = p.Send(cmd)
		if !p.intentional {
			t.Errorf("Send(%q) 之后该置停机标记", cmd)
		}
	}
	for _, cmd := range []string{"list", "/help", "say stop", "stopp", "stopall"} {
		p := newProcess(&Instance{}, NewLogBuffer(8))
		_ = p.Send(cmd)
		if p.intentional {
			t.Errorf("Send(%q) 不该置停机标记", cmd)
		}
	}
}

// 停机命令表本身就是规格，钉住它防止后来人「顺手加宽」——
// 每加一个词，就等于把那句话从「普通命令」升级成「关服指令」。
func TestStopCommandsAreExactNotPrefix(t *testing.T) {
	if !stopCommands["stop"] || !stopCommands["end"] {
		t.Fatal("stop / end 必须都在表里")
	}
	if stopCommands["restart"] {
		t.Error("restart 不能当停机：它想的是重启，判成停止会让用户以为服务器没了")
	}
	if len(stopCommands) != 2 {
		t.Errorf("停机命令表被改动了（现在是 %d 条）—— 确认过上游各服务端都认这两个吗？", len(stopCommands))
	}
}
