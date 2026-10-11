package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// 监控历史落盘。
//
// 为什么需要它：`Metrics()` 采的是**瞬时值**，概览页那张曲线是前端自己攒的 ——
// 刷新页面、切个实例、隔天再看，曲线统统从零开始。「昨天下午那波卡顿」永远看不到。
// 这里在守护进程侧按固定间隔采样并落盘，让曲线跨刷新、跨重启都还在。
//
// ⚠ 刻意**不放进实例目录**：实例目录会被「整个实例目录」范围的备份打包带走，
// 一份 12 小时的历史白白撑大备份包，还原时还会被当成用户数据糊回去。
// 它是面板自己的观测数据，归数据目录。
const (
	// metricEvery 采样间隔。10 秒是「曲线够细」和「磁盘够省」的折中：
	// 12 小时 = 4320 点，一个实例约 200KB。
	metricEvery = 10 * time.Second
	// metricRetain 保留多久以内的历史。更早的点在 append 时裁掉。
	metricRetain = 12 * time.Hour
	// metricFlushEvery 多久落一次盘。**不能每采一点就写** ——
	// 十个实例每 10 秒重写一次文件，磁盘会一直响。
	metricFlushEvery = 60 * time.Second
)

// MetricPoint 一次采样。字段名短，是因为这个结构在磁盘上要存几千条。
type MetricPoint struct {
	T       int64   `json:"t"`       // Unix 秒
	CPU     float64 `json:"cpu"`     // 进程 CPU 百分比
	Mem     int64   `json:"mem"`     // MB，进程实际驻留
	Players int     `json:"players"` // 在线玩家数
}

// metricsFile 历史落盘的位置（数据目录下，**不在**实例目录里）。
func metricsFile(baseDir string) string {
	return filepath.Join(baseDir, "metrics.json")
}

// metricsDisk 是落盘格式。带版本号，方便以后换结构时识别老文件。
type metricsDisk struct {
	V      int                      `json:"v"`
	Series map[string][]MetricPoint `json:"series"`
}

// metricsStore 所有实例的监控历史，一个进程一份。
//
// 内存里是权威副本，落盘是周期性的快照 —— 采样频率比落盘高一个数量级，
// 所以读写都走内存，只有 flush 才碰磁盘。
type metricsStore struct {
	mu     sync.Mutex
	series map[string][]MetricPoint
	dirty  bool
}

func loadMetricsStore(baseDir string) *metricsStore {
	s := &metricsStore{series: map[string][]MetricPoint{}}
	b, err := os.ReadFile(metricsFile(baseDir))
	if err != nil {
		return s // 第一次跑没有这个文件，不是错误
	}
	var d metricsDisk
	if err := json.Unmarshal(b, &d); err != nil {
		// 坏了就从空历史开始 —— 这只是指标曲线，不值得让守护进程起不来。
		// 不挪走坏文件：下一轮 flush 会直接覆盖它（这里没有"抢救"的价值）。
		fmt.Fprintf(os.Stderr, "警告：监控历史读取失败(%v)，本次从空历史开始\n", err)
		return s
	}
	for k, v := range d.Series {
		if len(v) > 0 {
			s.series[k] = v
		}
	}
	return s
}

// append 记一个点，顺手裁掉窗口外的老点。
func (s *metricsStore) append(id string, p MetricPoint) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts := append(s.series[id], p)
	// 时间单调递增，可以二分 —— 别每个点都全扫一遍，长会话下这是每秒都在跑的热路径。
	cut := p.T - int64(metricRetain.Seconds())
	if i := sort.Search(len(pts), func(i int) bool { return pts[i].T >= cut }); i > 0 {
		pts = pts[i:]
	}
	s.series[id] = pts
	s.dirty = true
}

// get 返回某个实例 T >= from 的点。**恒返回非 nil 切片** ——
// Go 的 nil slice 序列化成 JSON 的 null，前端直读 .length 会崩。
func (s *metricsStore) get(id string, from int64) []MetricPoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts := s.series[id]
	i := sort.Search(len(pts), func(i int) bool { return pts[i].T >= from })
	out := make([]MetricPoint, len(pts)-i)
	copy(out, pts[i:])
	return out
}

// flush 落盘。没有新点就不写（省掉无意义的磁盘写）。
//
// 用「临时文件 + rename」而不是直接覆盖：写到一半断电时，老文件还在，
// 不会留下一个半个 JSON 让下次启动读不出来。
func (s *metricsStore) flush(path string) error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	series := make(map[string][]MetricPoint, len(s.series))
	for k, v := range s.series {
		series[k] = v
	}
	s.dirty = false
	s.mu.Unlock()

	b, err := json.Marshal(metricsDisk{V: 1, Series: series})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// StartMetricsHistory 起后台采样循环。在 main 里调用（与 StartScheduler 并列）。
func (m *Manager) StartMetricsHistory() {
	go func() {
		// 先等一会儿：刚起来时 AutoStartAll 正在拉进程、要读配置写文件，
		// 别在这时候再抢一份 CPU。
		time.Sleep(5 * time.Second)
		lastFlush := time.Now()
		for {
			m.sampleMetricsOnce(time.Now())
			if time.Since(lastFlush) >= metricFlushEvery {
				if err := m.metricsHist.flush(metricsFile(m.baseDir)); err != nil {
					fmt.Fprintf(os.Stderr, "警告：监控历史落盘失败：%v\n", err)
				}
				lastFlush = time.Now()
			}
			time.Sleep(metricEvery)
		}
	}()
}

// sampleMetricsOnce 给所有**在跑**的实例各记一个点。
//
// 停着的实例不采：它的 CPU/内存恒为 0，记进去只会把曲线按平，没有信息量，
// 还会把「什么时候真的在跑」这件事淹掉。
func (m *Manager) sampleMetricsOnce(now time.Time) {
	// 先把 id 抄出来再逐个采样 —— Metrics() 自己会持锁，不能在里面嵌套 m.mu。
	m.mu.Lock()
	ids := make([]string, 0, len(m.procs))
	for id := range m.procs {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	for _, id := range ids {
		mt := m.Metrics(id)
		if !mt.IsRunning {
			continue
		}
		m.metricsHist.append(id, MetricPoint{
			T:       now.Unix(),
			CPU:     mt.CPU,
			Mem:     mt.MemUsed,
			Players: mt.Players,
		})
	}
}

// MetricsHistory 返回某实例最近 minutes 分钟的点。minutes 会被夹到 [1, 保留窗口]。
func (m *Manager) MetricsHistory(id string, minutes int) []MetricPoint {
	if minutes <= 0 {
		minutes = 60
	}
	if maxMin := int(metricRetain.Minutes()); minutes > maxMin {
		minutes = maxMin
	}
	from := time.Now().Add(-time.Duration(minutes) * time.Minute).Unix()
	pts := m.metricsHist.get(id, from)
	if pts == nil {
		pts = []MetricPoint{} // 恒返回 []，不是 null
	}
	return pts
}

// MetricIntervalSec 采样间隔的秒数，给前端标注曲线的时间刻度用。
func MetricIntervalSec() int { return int(metricEvery.Seconds()) }
