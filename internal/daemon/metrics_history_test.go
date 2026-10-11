package daemon

import (
	"os"
	"testing"
	"time"
)

// 超出保留窗口的点要被裁掉，否则文件会无限长。
func TestMetricsStoreRetention(t *testing.T) {
	s := &metricsStore{series: map[string][]MetricPoint{}}
	now := time.Now().Unix()

	s.append("a", MetricPoint{T: now - int64(metricRetain.Seconds()) - 3600, CPU: 1}) // 13 小时前
	s.append("a", MetricPoint{T: now, CPU: 2})

	got := s.get("a", 0)
	if len(got) != 1 {
		t.Fatalf("超出保留窗口的老点该被裁掉，实际剩 %d 个", len(got))
	}
	if got[0].CPU != 2 {
		t.Errorf("留下的应该是新点，实际 cpu=%v", got[0].CPU)
	}
}

// ⚠ 这条守的是项目硬规矩「集合字段恒 []，不是 null」：
// nil 切片序列化成 JSON 的 null，前端直读 .length 会白屏。
func TestMetricsStoreGetNeverNil(t *testing.T) {
	s := &metricsStore{series: map[string][]MetricPoint{}}
	if got := s.get("从来没有过的实例", 0); got == nil {
		t.Fatal("get 必须返回非 nil 切片（nil → JSON null → 前端 .length 崩）")
	}
	if got := s.get("从来没有过的实例", 0); len(got) != 0 {
		t.Errorf("没有历史时该是空切片，实际 %d 个", len(got))
	}
}

// 落盘 → 读回：数据要能穿过重启。
func TestMetricsStoreFlushRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := metricsFile(dir)

	s := &metricsStore{series: map[string][]MetricPoint{}}
	s.append("inst-1", MetricPoint{T: 1000, CPU: 1.5, Mem: 123, Players: 2})
	if err := s.flush(path); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// 临时文件不能留在磁盘上（rename 之后它就该没了）
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("flush 之后不该留下 .tmp")
	}

	back := loadMetricsStore(dir)
	got := back.get("inst-1", 0)
	if len(got) != 1 {
		t.Fatalf("读回来应该有 1 个点，实际 %d 个", len(got))
	}
	if got[0].CPU != 1.5 || got[0].Mem != 123 || got[0].Players != 2 {
		t.Errorf("读回的点不对：%+v", got[0])
	}
}

// 没有新点时不该碰磁盘 —— 十个实例每 10 秒写一次会把磁盘一直弄响。
func TestMetricsStoreFlushSkipsWhenClean(t *testing.T) {
	dir := t.TempDir()
	path := metricsFile(dir)
	s := &metricsStore{series: map[string][]MetricPoint{}}
	if err := s.flush(path); err != nil {
		t.Fatalf("flush: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("没有新点时不该写文件")
	}
}

// 坏文件不能把守护进程带走：从空历史开始，而不是报错。
func TestLoadMetricsStoreCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(metricsFile(dir), []byte("{ 这不是 json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := loadMetricsStore(dir)
	if s == nil {
		t.Fatal("坏文件也该返回一个可用的空 store")
	}
	if got := s.get("x", 0); len(got) != 0 {
		t.Errorf("坏文件后应该是空历史，实际 %d 个点", len(got))
	}
}

// 停着的实例不采点：CPU/内存恒为 0，记进去只会把曲线按平。
func TestSampleMetricsOnceSkipsStopped(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "停着的", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	m.sampleMetricsOnce(time.Now())
	if got := m.MetricsHistory(inst.ID, 60); len(got) != 0 {
		t.Errorf("没在跑的实例不该被采样，实际 %d 个点", len(got))
	}
}

// minutes 会被夹进保留窗口，且返回值恒非 nil。
func TestMetricsHistoryClampsAndNeverNil(t *testing.T) {
	m := newTestManager(t)
	pts := m.MetricsHistory("不存在的实例", 999999)
	if pts == nil {
		t.Fatal("必须返回 [] 而不是 nil")
	}
	if len(pts) != 0 {
		t.Errorf("不存在的实例应该没有历史，实际 %d 个点", len(pts))
	}
}

// 窗口查询按时间下界过滤，别把老点也捞出来。
func TestMetricsStoreGetSince(t *testing.T) {
	s := &metricsStore{series: map[string][]MetricPoint{}}
	now := time.Now().Unix()
	for i := int64(0); i < 10; i++ {
		s.append("a", MetricPoint{T: now - 100 + i*10, CPU: float64(i)})
	}
	// 点是 now-100, now-90, … now-10；下界含 now-50，所以命中 5 个（-50,-40,-30,-20,-10）
	got := s.get("a", now-50)
	if len(got) != 5 {
		t.Fatalf("应取到下界之后的 5 个点，实际 %d 个", len(got))
	}
	if got[0].T < now-50 {
		t.Errorf("第一个点的时刻早于下界：%d < %d", got[0].T, now-50)
	}
}
