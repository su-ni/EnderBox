package fetch

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 取消只对「还在跑」的任务成立。
//
// 以前 Cancel 只判 cancel 函数还在不在 —— 那个函数在任务结束后也不会被置空，
// 于是对**已经下完**的任务同样返回 true，前端就弹「已取消下载」，
// 可文件其实已经装好了。用户会以为没装成，再装一遍。
// 这两条就是钉这个行为的：拆掉 download.go 里那句 `t.State == StateRunning`
// 这里应该立刻变红。
func TestCancelOnlyRunning(t *testing.T) {
	data := testData(4096)
	srv := httptest.NewServer(rangeHandler(data, true))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "x.jar")
	m := NewManager()

	task, err := m.Start(Options{
		InstanceID: "i1",
		Label:      "取消测试",
		Build: Build{
			Name: "x.jar", Size: int64(len(data)),
			URL: srv.URL + "/x.jar",
		},
		DestPath: dest,
	})
	if err != nil {
		t.Fatalf("起任务失败: %v", err)
	}

	// 等它跑完
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g, _ := m.Get(task.ID); g != nil && g.State != StateRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := m.Get(task.ID)
	if got == nil || got.State != StateDone {
		t.Fatalf("任务没跑完，state=%v", got)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("文件没落盘: %v", err)
	}

	// 已经下完了 —— 取消必须返回 false，否则前端会说一句假话
	if m.Cancel(task.ID) {
		t.Fatal("已结束的任务 Cancel 却返回 true —— 前端会因此谎报「已取消」，但文件其实已经装好了")
	}
}

// 真正在中途取消：**取消即清理** —— 半截文件（.part / 分片）一起删掉，
// 目标路径上更不能出现一个"看起来完整"的 jar。
//
// 口径变更（2026-10-10 用户拍板；原口径是「.part 留着给下次续传」）：
// 拆掉 fetchToPart 里取消分支的 removePart，本测试立刻变红。
func TestCancelMidDownload(t *testing.T) {
	// 分块慢速返回，保证取消时它还在一半
	const total = 3 << 20 // 3 MB
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "3145728")
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 32*1024)
		for written := 0; written < total; {
			for i := range chunk {
				chunk[i] = byte(i)
			}
			n := len(chunk)
			if written+n > total {
				n = total - written
			}
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			written += n
			time.Sleep(2 * time.Millisecond)
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "y.jar")
	m := NewManager()
	task, err := m.Start(Options{
		InstanceID: "i1",
		Label:      "中途取消",
		Build:      Build{Name: "y.jar", Size: total, URL: srv.URL + "/y.jar"},
		DestPath:   dest,
	})
	if err != nil {
		t.Fatalf("起任务失败: %v", err)
	}

	// 等到确实在下（有字节落盘）再取消
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g, _ := m.Get(task.ID); g != nil && g.Done > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if g, _ := m.Get(task.ID); g == nil || g.Done == 0 {
		t.Skip("源站太慢，没等到有字节落盘，跳过（不误导成失败）")
	}

	if !m.Cancel(task.ID) {
		g, _ := m.Get(task.ID)
		t.Fatalf("还在跑的任务却取消不了，state=%v", g.State)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g, _ := m.Get(task.ID); g != nil && g.State != StateRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := m.Get(task.ID)
	t.Logf("取消后 state=%v done=%d/%d err=%q", got.State, got.Done, got.Total, got.Err)

	// 取消之后目标文件不该变成一个"完整的" jar —— 否则服务端会拿半个文件启动
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("取消之后目标路径上出现了成品文件 —— 半个 jar 被当成完整的会更糟")
	}
	// 新口径（10-10 用户拍板）：取消就要**清干净** —— 半截 .part 与分片都不许留在盘上。
	// 以前这里断言的是「.part 应该留着续传」，现在整条反过来：这就是「取消即清理」的钉子。
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Fatal("取消之后 .part 还留在盘上 —— 拍了「取消即删」，不能留神秘残留")
	}
	if idx := findSegIndices(dest + ".part"); len(idx) != 0 {
		t.Fatalf("取消之后还有分片残留：%v", idx)
	}
}

// Dismiss（手动清账本）只对「已结束」的任务成立：
//   - 运行中删除必须被拒 —— 删了之后界面失去唯一引用，用户再也取消不了它，它却还在写盘；
//   - 已结束删除成功后 Get 必须立刻查不到（真删了，不是标记）；
//   - 不存在的 id 返回 false（幂等语义，前端重复点不炸）。
//
// 拆掉 download.go 里 Dismiss 的 `t.State == StateRunning` 判断，第一段断言立刻变红。
func TestDismissRefusesRunning(t *testing.T) {
	const total = 8 << 20 // 8 MB 慢速放，保证断言期间它一直在跑
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", "8388608")
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 32*1024)
		for written := 0; written < total; {
			n := len(chunk)
			if written+n > total {
				n = total - written
			}
			if _, err := w.Write(chunk[:n]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			written += n
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "z.jar")
	m := NewManager()
	task, err := m.Start(Options{
		InstanceID: "i1",
		Label:      "清账本测试",
		Build:      Build{Name: "z.jar", Size: total, URL: srv.URL + "/z.jar"},
		DestPath:   dest,
	})
	if err != nil {
		t.Fatalf("起任务失败: %v", err)
	}

	// 等到确实在下再断言，避免「还没起跑就被判 running」的假阴性
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g, _ := m.Get(task.ID); g != nil && g.Done > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if g, _ := m.Get(task.ID); g == nil || g.Done == 0 {
		t.Skip("源站太慢，没等到有字节落盘，跳过（不误导成失败）")
	}

	// ① 运行中：必须拒绝
	if m.Dismiss(task.ID) {
		t.Fatal("运行中的任务被 Dismiss 删掉了 —— 用户再也取消不了它，它却还在后台写盘")
	}
	if _, ok := m.Get(task.ID); !ok {
		t.Fatal("拒绝之后任务反而不见了 —— Dismiss 拒绝时不能有副作用")
	}

	// ② 取消并等它进终态
	if !m.Cancel(task.ID) {
		t.Fatalf("还在跑的任务却取消不了")
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if g, _ := m.Get(task.ID); g != nil && g.State != StateRunning {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if g, _ := m.Get(task.ID); g == nil || g.State == StateRunning {
		t.Fatalf("取消后没进终态，state=%v", g)
	}

	// ③ 已结束：删除成功，而且真的查不到了
	if !m.Dismiss(task.ID) {
		t.Fatal("已结束的任务 Dismiss 应该成功")
	}
	if _, ok := m.Get(task.ID); ok {
		t.Fatal("Dismiss 之后 Get 还能查到 —— 记录没删干净")
	}

	// ④ 再删一次：不存在就是 false，不 panic
	if m.Dismiss(task.ID) {
		t.Fatal("不存在的任务 Dismiss 却返回 true")
	}
}
