package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// 端到端：真的拉起一个服务端进程，真的往它 stdin 敲 stop，再看落到什么状态。
//
// 判据单测再绿也不如真跑一遍 —— 用户的症状（手敲 stop 被判崩溃）是
// 「Send 没置标记」+「收尾只看标记」两条拼出来的，只有真进程能同时证伪它们。
//
// 用 tools/fakeserver/server.jar：它的行为刻意对齐真实服务端
// （打 Done (...)! 认 stop，打完 "Stopping the server" 再 System.exit(0)）。
// 本机没有 java 就跳过 —— 它是联调用的，不该卡住任何人。

func requireTestJava(t *testing.T) string {
	t.Helper()
	cands := []string{os.Getenv("SBX_TEST_JAVA")}
	if p, err := exec.LookPath("java"); err == nil {
		cands = append(cands, p)
	}
	// 本机约定：工具链都在 F 盘
	cands = append(cands,
		filepath.Join(`F:\Java\java 21`, "bin", "java.exe"),
		filepath.Join(`F:\Java\java 21`, "jre", "bin", "java.exe"),
	)
	for _, c := range cands {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	t.Skip("本机没有可用的 java，跳过端到端进程测试")
	return ""
}

func fakeServerJar(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "..", "tools", "fakeserver", "server.jar")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("找不到 fakeserver jar（%s），跳过", p)
	}
	return p
}

// waitFor 等一个条件成立，超时才算失败。
// 别用固定 sleep：JVM 冷启动耗时不稳定，写死要么白等要么 flaky。
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等「%s」超时（%s）", what, timeout)
}

// newE2EProcess 在临时目录里摆一个实例，返回已就绪的 Process（未启动）。
func newE2EProcess(t *testing.T, jarBytes []byte) (*Process, string) {
	t.Helper()
	dir := t.TempDir()
	// 逐字节写一份：别用软链/快捷方式，java 在 Windows 上打不开
	if err := os.WriteFile(filepath.Join(dir, "server.jar"), jarBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	inst := &Instance{
		ID: "e2e", Name: "e2e", Type: TypePaper, Dir: dir,
		JarPath: "server.jar", MinMem: 256, MaxMem: 512, Port: 25565,
	}
	return newProcess(inst, NewLogBuffer(2000)), dir
}

// 用户在控制台敲 stop / /stop（API: POST /command，body {"cmd":"stop"}）之后，
// 状态必须落在「已停止」。这就是报告里的那个场景，字面复现。
func TestE2EConsoleStopIsNotCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真进程测试")
	}
	java := requireTestJava(t)
	b, err := os.ReadFile(fakeServerJar(t))
	if err != nil {
		t.Fatal(err)
	}

	p, _ := newE2EProcess(t, b)
	if err := p.Start(java); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	t.Cleanup(func() { _ = p.Kill() })

	waitFor(t, "服务端就绪（打出 Done）", 60*time.Second, func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.everReady
	})

	// 用户从 wiki 复制来的命令十有八九带斜杠 —— 报告里写的也是 /stop
	if err := p.Send("/stop"); err != nil {
		t.Fatalf("发送 stop 失败：%v", err)
	}

	select {
	case <-p.Done():
	case <-time.After(30 * time.Second):
		t.Fatal("发了 stop 之后服务端迟迟不退出")
	}

	if got := p.Status(); got != StatusStopped {
		lines := p.logs.Snapshot()
		if len(lines) > 8 {
			lines = lines[len(lines)-8:]
		}
		t.Errorf("手敲 /stop 之后状态是 %q，想要 %q —— 这就是用户报的 bug\n最后几行日志：\n%v",
			got, StatusStopped, lines)
	}
}

// 反向验证：光把「正常关服别误报」修好还不够，别顺手把判据放宽成
// 「什么都算正常」。服务端起不来（jar 是坏的）必须仍然报崩溃。
func TestE2EBootFailureIsStillCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过真进程测试")
	}
	java := requireTestJava(t)

	// 不是 zip 的文件：java 会报 "Invalid or corrupt jarfile" 并以非 0 退出，
	// 而且绝不会打出 Done (...) —— 这正是「起不来」的形态
	p, _ := newE2EProcess(t, []byte("this is definitely not a jar"))
	if err := p.Start(java); err != nil {
		t.Fatalf("启动失败：%v", err)
	}
	t.Cleanup(func() { _ = p.Kill() })

	select {
	case <-p.Done():
	case <-time.After(60 * time.Second):
		t.Fatal("坏 jar 应该很快就退出")
	}

	if got := p.Status(); got != StatusCrashed {
		t.Errorf("服务端起不来时状态是 %q，想要 %q（判据被放宽过头了）", got, StatusCrashed)
	}
}
