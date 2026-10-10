//go:build !windows

package main

import "os/exec"

// detach 在非 Windows 上是空操作：那边没有「控制台关闭事件」这回事，
// 子进程本来就不随父进程的控制台一起走。
// 保留这个函数是为了让调用点只有一处、不用到处判断 GOOS。
func detach(_ *exec.Cmd) {}
