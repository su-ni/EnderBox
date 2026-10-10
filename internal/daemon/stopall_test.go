package daemon

import (
	"testing"
	"time"
)

// 空闲时优雅停服必须**立刻返回**，不能等满超时。
//
// 这条看着像废话，但防的是真坑：若实现写成「无脑等到 timeout」，
// 用户按 Ctrl+C 之后面板会**干等 4 秒**才退 —— 而 Windows 关窗口只给约 5 秒，
// 这一等就把余量吃光了，等于白做优雅停服。
func TestStopAllGracefullyReturnsImmediatelyWhenNothingRunning(t *testing.T) {
	m := newTestManager(t)
	if err := m.Create(&Instance{ID: "i1", Name: "停着的服"}); err != nil {
		t.Fatal(err)
	}

	t0 := time.Now()
	stopped, failed := m.StopAllGracefully(4 * time.Second)
	el := time.Since(t0)

	if stopped != 0 || failed != 0 {
		t.Errorf("没有运行中的实例时该返回 (0,0)，实际 (%d,%d)", stopped, failed)
	}
	if el > time.Second {
		t.Errorf("空闲时不该等待，实际耗了 %v（超时值是 4s）", el)
	}
}

// 没在跑的实例不能被算进「要停的名单」——否则退出时会对着空气发 stop。
func TestRunningIDsExcludesStoppedInstances(t *testing.T) {
	m := newTestManager(t)
	for _, id := range []string{"a", "b"} {
		if err := m.Create(&Instance{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if got := m.RunningIDs(); len(got) != 0 {
		t.Errorf("刚建好还没启动的实例不该出现在 RunningIDs 里，实际 %v", got)
	}
	if stopped, failed := m.StopAllGracefully(200 * time.Millisecond); stopped != 0 || failed != 0 {
		t.Errorf("全是停止态时该返回 (0,0)，实际 (%d,%d)", stopped, failed)
	}
}

// 空 store 也不能 panic —— 退出路径上的代码最怕崩：
// 它崩了就等于「停服流程本身失败」，用户看到的是服没停干净。
func TestStopAllGracefullyToleratesEmptyStore(t *testing.T) {
	m := newTestManager(t)
	if ids := m.RunningIDs(); ids != nil {
		t.Errorf("空 store 该返回 nil，实际 %v", ids)
	}
	if stopped, failed := m.StopAllGracefully(50 * time.Millisecond); stopped != 0 || failed != 0 {
		t.Errorf("空 store 该返回 (0,0)，实际 (%d,%d)", stopped, failed)
	}
}
