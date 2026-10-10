//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detach 让**被拉起的守护进程脱离我们这个进程的控制台**。
//
// ⚠ 这一条不是洁癖，是防「关个窗口把服务器一起停了」：
// 子进程默认沿用父进程的控制台，而控制台一关，系统会给挂在它上面的所有进程
// 发 CTRL_CLOSE_EVENT —— 进程没装处理器就是直接结束。
// 于是「关掉桌面窗口」会连带把守护进程（以及它管着的 MC 服务器）一起带走。
// 现场看就是「我只是关了窗口，服却停了」，而且不好往这里想。
//
// DETACHED_PROCESS(0x08)：子进程**完全不带控制台**，因此收不到我们这个控制台的关闭事件。
// CREATE_NEW_PROCESS_GROUP(0x200)：让它自己成一进程组，Ctrl+C 之类也只影响它自己。
//
// 代价是守护进程没有控制台可写（它本来也只写自己的日志文件与日志缓冲），
// 换来的是「关窗不影响服」。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000008 | 0x00000200,
	}
}
