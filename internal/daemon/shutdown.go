package daemon

import (
	"sync"
	"time"
)

// StopAllGracefully 给**所有正在运行**的实例发 stop，等它们写盘退出。
//
// 为什么需要它：面板一关，java 就跟着被 Windows 硬杀（`CTRL_CLOSE_EVENT`，进程没装处理器
// 就是直接结束）。那是**硬杀不是 stop** —— 存档来不及写盘，有损坏风险。
// 这里拦一道：退出前先好好地停一遍。
//
// ⚠ 调用方必须给一个**够短**的 timeout：Windows 关控制台窗口时，
//   系统给进程的处理时间只有大约 5 秒，超了就直接切掉 —— 等太久等于没等。
//
// ⚠ 并发停而不是串行：串行的话 3 个实例就是 3 × timeout，而窗口关闭给的总时间只有 5 秒。
//
// 返回 (停成功的个数, 非零退出的个数)。两者之和 = 当时在跑的实例数。
func (m *Manager) StopAllGracefully(timeout time.Duration) (stopped, failed int) {
	var running []string
	for _, in := range m.st.All() {
		if m.isRunning(in.ID) {
			running = append(running, in.ID)
		}
	}
	if len(running) == 0 {
		return 0, 0
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, id := range running {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			err := m.Stop(id, timeout)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
			} else {
				stopped++
			}
		}(id)
	}
	wg.Wait()
	return stopped, failed
}

// RunningIDs 当前正在跑的实例 id（排序后返回，调用方拿去打日志用）。
// 单独抽出来是为了让「退出时到底有没有东西要停」这件事可以被测试直接问。
func (m *Manager) RunningIDs() []string {
	var out []string
	for _, in := range m.st.All() {
		if m.isRunning(in.ID) {
			out = append(out, in.ID)
		}
	}
	return out
}
