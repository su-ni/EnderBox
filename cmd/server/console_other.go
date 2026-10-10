//go:build !windows

package main

// watchConsoleClose 在非 Windows 上是空操作。
//
// Unix 没有「控制台关闭事件」这个概念：关终端时系统给进程发的是 SIGHUP，
// 而 `os/signal` 那条路已经覆盖了（SIGTERM/SIGINT）。
// 想连 SIGHUP 也接上，到时候往 signal.Notify 里加一个就行，不需要单独的平台钩子。
func watchConsoleClose(_ func()) {}
