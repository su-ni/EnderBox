//go:build windows

package main

import "syscall"

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// 控制台事件码（Windows）
const (
	ctrlCEvent       = 0
	ctrlBreakEvent   = 1
	ctrlCloseEvent   = 2
	ctrlLogoffEvent  = 5
	ctrlShutdownEvent = 6
)

// watchConsoleClose 挂一个控制台事件处理器，**专门管「关掉那个黑框」这条路**。
//
// 为什么不复用 `os/signal`：Windows 上 Ctrl+C 会被 Go 运行时转成 `os.Interrupt`，
// 但 **CTRL_CLOSE_EVENT / CTRL_SHUTDOWN_EVENT 不会** —— Go 在这两种事件下直接走它自己的
// 退出路径，signal channel 里什么都收不到。于是就出现了最气人的那种情况：
// 按 Ctrl+C 能优雅停服，点右上角那个 ✗ 却把服务器硬杀了。
//
// ⚠ 回调里**不能久留**：系统只给约 5 秒，到点无条件结束进程。
//   所以这里只负责触发，真正的等待逻辑在调用方的 shutdown() 里（它有 4 秒上限）。
//
// ⚠ 用 `Add=1`（追加而不是替换）保留 Go 运行时自己的处理器 ——
//   否则会把 Ctrl+C 那条路也一并接管，反而更糟。
func watchConsoleClose(fn func()) {
	cb := syscall.NewCallback(func(ctrlType uint32) uintptr {
		switch ctrlType {
		case ctrlCloseEvent, ctrlShutdownEvent, ctrlLogoffEvent:
			fn()
			// 返回 1 = 已处理。返回 0 会继续传给下一个处理器，最终仍是被硬杀。
			return 1
		}
		return 0
	})
	// 第一个参数 TRUE = 追加到处理器链（不是替换）
	_, _, _ = procSetConsoleCtrlHandler.Call(cb, 1)
}
