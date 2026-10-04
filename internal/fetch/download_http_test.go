package fetch

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// 这一组用本地假源站把「下载器」真正跑一遍，覆盖三件只靠单测钉不住的事：
//  ① 支持 Range 的源站要走**多线程**，而且拼出来的文件必须逐字节等于原文
//  ② 不支持 Range 的源站要能自动退回单连接，同样不能出错
//  ③ 第一次就失败的源站要能自动重试成功（用户报的就是这个：连接被掐断）
//
// 为什么要用假源站而不是真去下：真源站是别人家的服务器，会抖、会限流，
// 拿它当测试会变成「有时绿有时红」——那种测试比没有更坏。

func parseTestRange(h string) (int64, int64, bool) {
	if !strings.HasPrefix(h, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(h, "bytes=")
	i := strings.Index(spec, "-")
	if i < 0 {
		return 0, 0, false
	}
	a, err := strconv.ParseInt(spec[:i], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	if spec[i+1:] == "" {
		return a, -1, true // 开区间：到结尾
	}
	b, err := strconv.ParseInt(spec[i+1:], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return a, b, true
}

func testData(n int) []byte {
	d := make([]byte, n)
	for i := range d {
		d[i] = byte(i*31 + i/251)
	}
	return d
}

func rangeHandler(data []byte, rangeOK bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, b, ok := parseTestRange(r.Header.Get("Range"))
		if !rangeOK || !ok {
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
			return
		}
		if b < 0 || b >= int64(len(data)) {
			b = int64(len(data)) - 1
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(int(b-a+1)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[a : b+1])
	}
}

func waitTask(t *testing.T, m *Manager, id string) *Task {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := m.Get(id)
		if !ok {
			t.Fatal("任务不见了")
		}
		if got.State != StateRunning {
			return got
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("任务 30 秒还没结束")
	return nil
}

func sha256Of(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ① 支持 Range：5 MB 必须走多线程，且拼出来的文件逐字节等于原文，分片临时文件要清干净。
func TestDownloadSegmentedProducesExactFile(t *testing.T) {
	data := testData(5 << 20)
	srv := httptest.NewServer(rangeHandler(data, true))
	defer srv.Close()

	m := NewManager()
	dest := filepath.Join(t.TempDir(), "server.jar")
	task, err := m.Start(Options{
		InstanceID: "i1",
		Build:      Build{URL: srv.URL, SHA: sha256Of(data), SHAKind: "sha256", Name: "server.jar"},
		DestPath:   dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitTask(t, m, task.ID)
	if got.State != StateDone {
		t.Fatalf("状态 %s，错误：%s", got.State, got.Err)
	}
	if !got.Verified {
		t.Error("sha256 没校验通过")
	}
	if got.Threads < 2 {
		t.Errorf("5 MB 的文件该走多线程，实际线程数 %d（说明分片路径没被走到）", got.Threads)
	}
	if got.Total != int64(len(data)) {
		t.Errorf("总大小 %d，期望 %d", got.Total, len(data))
	}
	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, data) {
		t.Error("拼出来的文件内容和源不一致 —— 分片边界算错了")
	}
	// 分片临时文件不能留在磁盘上
	if idx := findSegIndices(dest + ".part"); len(idx) != 0 {
		t.Errorf("分片临时文件没清干净：%v", idx)
	}
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Error(".part 合并后应该已经被 rename 掉")
	}
}

// ② 不支持 Range 的源站：退回单连接，但内容依然要正确。
func TestDownloadFallsBackWhenNoRangeSupport(t *testing.T) {
	data := testData(3 << 20)
	srv := httptest.NewServer(rangeHandler(data, false))
	defer srv.Close()

	m := NewManager()
	dest := filepath.Join(t.TempDir(), "server.jar")
	task, err := m.Start(Options{
		InstanceID: "i1",
		Build:      Build{URL: srv.URL, SHA: sha256Of(data), SHAKind: "sha256"},
		DestPath:   dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitTask(t, m, task.ID)
	if got.State != StateDone {
		t.Fatalf("状态 %s，错误：%s", got.State, got.Err)
	}
	if got.Threads != 0 {
		t.Errorf("不支持 Range 时应该是单连接，实际线程数 %d", got.Threads)
	}
	out, _ := os.ReadFile(dest)
	if !bytes.Equal(out, data) {
		t.Error("内容不一致")
	}
}

// ③ 头几次请求失败（模拟用户遇到的「连接被掐断」）：必须自动重试到成功。
func TestDownloadRetriesTransientFailure(t *testing.T) {
	data := testData(3 << 20)
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) <= 2 {
			// 前两次直接拒 —— 等价于源站/中间设备把连接掐了
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		rangeHandler(data, true)(w, r)
	}))
	defer srv.Close()

	m := NewManager()
	dest := filepath.Join(t.TempDir(), "server.jar")
	task, err := m.Start(Options{
		InstanceID: "i1",
		Build:      Build{URL: srv.URL, SHA: sha256Of(data), SHAKind: "sha256"},
		DestPath:   dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitTask(t, m, task.ID)
	if got.State != StateDone {
		t.Fatalf("重试后仍失败：%s / %s", got.State, got.Err)
	}
	if atomic.LoadInt32(&n) < 3 {
		t.Errorf("应该请求了至少 3 次，实际 %d 次", n)
	}
	out, _ := os.ReadFile(dest)
	if !bytes.Equal(out, data) {
		t.Error("重试后内容不一致")
	}
}

// 校验和不匹配时必须判失败并把坏文件删掉 —— 留着它比删掉更危险（用户会拿去启动）。
func TestDownloadRejectsWrongChecksum(t *testing.T) {
	data := testData(3 << 20)
	srv := httptest.NewServer(rangeHandler(data, true))
	defer srv.Close()

	m := NewManager()
	dest := filepath.Join(t.TempDir(), "server.jar")
	task, err := m.Start(Options{
		InstanceID: "i1",
		Build:      Build{URL: srv.URL, SHA: sha256Of([]byte("别的文件")), SHAKind: "sha256"},
		DestPath:   dest,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := waitTask(t, m, task.ID)
	if got.State != StateFailed {
		t.Fatalf("校验和不匹配时应该失败，实际 %s", got.State)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Error("校验失败的文件必须删掉，不能留在实例目录里")
	}
}
