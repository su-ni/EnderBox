package fetch

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TaskState 一次下载的状态
type TaskState string

const (
	StateRunning TaskState = "running"
	StateDone    TaskState = "done"
	StateFailed  TaskState = "failed"
)

// Task 一次服务端 jar 下载。前端按 id 轮询它。
//
// 用轮询而不是 SSE：下载是「用户点一下、盯着看几秒」的场景，
// 轮询断了重连天然没问题，SSE 断了还得处理重连和事件补偿。
type Task struct {
	ID        string     `json:"id"`
	Instance  string     `json:"instance"`
	File      string     `json:"file"`
	Step      string     `json:"step"` // 连接中 / 下载中 / 校验中 / 就绪 —— 比一个百分比信息量大
	Total     int64      `json:"total"`
	Done      int64      `json:"done"`
	Percent   float64    `json:"percent"`
	Rate      int64      `json:"rate"` // 字节/秒
	Resumed   bool       `json:"resumed"`
	State     TaskState  `json:"state"`
	Err       string     `json:"err,omitempty"`
	SHA       string     `json:"sha"`
	Verified  bool       `json:"verified"`
	StartedAt time.Time  `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt,omitempty"`

	cancel context.CancelFunc
}

// Manager 下载任务表
type Manager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	sem   chan struct{}
	seq   int64
}

func NewManager() *Manager {
	return &Manager{
		tasks: map[string]*Task{},
		// 同时最多两个下载：再多也只是在一条家用宽带上互相抢，
		// 而且同时写两个大文件对磁盘也不友好
		sem: make(chan struct{}, 2),
	}
}

func (m *Manager) Get(id string) (*Task, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return nil, false
	}
	cp := *t
	return &cp, true
}

// List 某个实例的全部任务，新的在前
func (m *Manager) List(instanceID string) []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Task
	for _, t := range m.tasks {
		if instanceID == "" || t.Instance == instanceID {
			cp := *t
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if ok && t.cancel != nil {
		t.cancel()
		return true
	}
	return false
}

// prune 清掉 30 分钟前结束的任务，避免任务表无限长
func (m *Manager) prune() {
	cut := time.Now().Add(-30 * time.Minute)
	for id, t := range m.tasks {
		if t.EndedAt != nil && t.EndedAt.Before(cut) {
			delete(m.tasks, id)
		}
	}
}

func (m *Manager) set(id string, fn func(*Task)) {
	m.mu.Lock()
	if t, ok := m.tasks[id]; ok {
		fn(t)
	}
	m.mu.Unlock()
}

// Options 一次下载的参数
type Options struct {
	InstanceID string
	Build      Build
	DestPath   string // 最终落盘路径（通常是 <实例目录>/server.jar）

	// OnDone 落盘成功后回调。让调用方在这里补后续动作（写 eula、写 server.properties、
	// 往实例控制台写一行日志）——下载器本身不该知道实例和属性的存在。
	OnDone func(opts Options) error
}

// Start 起一个下载任务，立刻返回 task id。
func (m *Manager) Start(opts Options) (*Task, error) {
	if opts.Build.URL == "" {
		return nil, fmt.Errorf("这个构建没有可用的下载地址")
	}
	if opts.DestPath == "" {
		return nil, fmt.Errorf("没有指定落盘路径")
	}

	ctx, cancel := context.WithCancel(context.Background())
	t := &Task{
		ID:        fmt.Sprintf("dl%d", time.Now().UnixNano()),
		Instance:  opts.InstanceID,
		File:      filepath.Base(opts.DestPath),
		Step:      "排队中",
		Total:     opts.Build.Size,
		SHA:       opts.Build.SHA,
		State:     StateRunning,
		StartedAt: time.Now(),
		cancel:    cancel,
	}
	m.mu.Lock()
	m.prune()
	m.tasks[t.ID] = t
	m.mu.Unlock()

	go m.run(ctx, t.ID, opts)

	// 回一份拷贝，别把内部指针交出去 —— 调用方拿到就能改的话，
	// 进度上报和调用方读到的值会打架
	snap, _ := m.Get(t.ID)
	return snap, nil
}

// run 真正的下载流程。分三步走，每步都写进 Step ——
// 「卡在 87%」和「卡在校验」是两种完全不同的故障，界面必须能分辨。
func (m *Manager) run(ctx context.Context, id string, opts Options) {
	defer func() {
		now := time.Now()
		m.set(id, func(t *Task) { t.EndedAt = &now })
	}()

	m.sem <- struct{}{}
	defer func() { <-m.sem }()

	part := opts.DestPath + ".part"

	// ── 断点续传：.part 还在就从它的大小接着下 ──
	// 一个 Paper jar 有 50 MB，家里网抖一下从头再来很伤
	var offset int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 {
		offset = st.Size()
	}

	m.set(id, func(t *Task) {
		if offset > 0 {
			t.Step = "续传中"
			t.Resumed = true
			t.Done = offset
		} else {
			t.Step = "下载中"
		}
	})

	req, err := newRequest(opts.Build.URL)
	if err != nil {
		m.fail(id, err)
		return
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	// 走 dlClient：不设总超时，否则慢网下大文件必失败（见 source.go 里的说明）
	resp, err := dlClient.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			m.fail(id, fmt.Errorf("已取消"))
			return
		}
		m.fail(id, fmt.Errorf("下载失败：%w", err))
		return
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// 服务端不支持 Range（或我们没要），那就从头来
		offset = 0
	case http.StatusPartialContent:
		// 正常续传
	case http.StatusRequestedRangeNotSatisfiable:
		// .part 比远端文件还大，说明它是上一次失败留下的脏数据，删掉重来
		resp.Body.Close()
		_ = os.Remove(part)
		m.fail(id, fmt.Errorf("本地残留的临时文件比远端文件还大，已删除，请重试"))
		return
	default:
		m.fail(id, fmt.Errorf("下载地址返回 HTTP %d（%s）", resp.StatusCode, hostOf(opts.Build.URL)))
		return
	}

	if resp.ContentLength > 0 {
		total := resp.ContentLength + offset
		m.set(id, func(t *Task) { t.Total = total })
	}

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		m.fail(id, fmt.Errorf("无法写入 %s：%w", part, err))
		return
	}

	// 一边写盘一边算校验和。已经续传的那部分要先把老数据喂进 hash，
	// 否则校验值永远是错的 —— 这个坑很隐蔽：文件下完了但校验不通过，看起来像源站的问题。
	var h hash.Hash
	switch strings.ToLower(opts.Build.SHAKind) {
	case "sha1":
		h = sha1.New()
	default:
		h = sha256.New()
	}
	if offset > 0 {
		if old, err := os.Open(part); err == nil {
			_, _ = io.Copy(h, old)
			old.Close()
		}
	}

	written, cpErr := m.copy(ctx, id, f, resp.Body, h, offset)
	_ = f.Close()
	if cpErr != nil {
		m.fail(id, cpErr)
		return
	}

	// ── 校验 ──
	m.set(id, func(t *Task) { t.Step = "校验中" })
	if opts.Build.SHA != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if !strings.EqualFold(got, opts.Build.SHA) {
			_ = os.Remove(part)
			m.fail(id, fmt.Errorf(
				"文件校验不通过，已删除。期望 %s…，实际 %s…（多半是下载过程中被截断，重试一次通常就好）",
				opts.Build.SHA[:12], got[:12]))
			return
		}
		m.set(id, func(t *Task) { t.Verified = true })
	}

	// ── 落盘：rename 是原子的，避免出现「半个 jar 被当成完整的」──
	if err := os.MkdirAll(filepath.Dir(opts.DestPath), 0o755); err != nil {
		m.fail(id, err)
		return
	}
	_ = os.Remove(opts.DestPath) // Windows 上 rename 不能覆盖已存在的文件
	if err := os.Rename(part, opts.DestPath); err != nil {
		m.fail(id, fmt.Errorf("落盘失败：%w", err))
		return
	}

	if opts.OnDone != nil {
		m.set(id, func(t *Task) { t.Step = "收尾中" })
		if err := opts.OnDone(opts); err != nil {
			// 文件已经下好了，收尾失败不算下载失败 —— 但必须说出来，
			// 否则用户会以为「明明好了却起不来」是玄学
			m.set(id, func(t *Task) {
				t.Step = "就绪（收尾有告警）"
				t.State = StateDone
				t.Done = written
				t.Percent = 100
				t.Err = "文件已就绪，但后续配置没写成功：" + err.Error()
			})
			return
		}
	}

	m.set(id, func(t *Task) {
		t.Step = "就绪"
		t.State = StateDone
		t.Done = written
		t.Percent = 100
	})
}

// copy 带进度上报的复制。每 200ms 更新一次进度就够 ——
// 每个 64 KB 块都写一次 task 会让轮询返回一堆没人看得清的中间值。
func (m *Manager) copy(ctx context.Context, id string, dst io.Writer, src io.Reader, h hash.Hash, offset int64) (int64, error) {
	buf := make([]byte, 64*1024)
	var done int64
	lastAt := time.Now()
	lastDone := int64(0)

	for {
		if ctx.Err() != nil {
			return done, fmt.Errorf("已取消")
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return done, fmt.Errorf("写盘失败：%w", werr)
			}
			h.Write(buf[:n])
			done += int64(n)

			if time.Since(lastAt) >= 200*time.Millisecond {
				cur := done + offset
				rate := int64(float64(cur-lastDone) / time.Since(lastAt).Seconds())
				lastAt, lastDone = time.Now(), cur
				m.set(id, func(t *Task) {
					t.Done = cur
					t.Rate = rate
					if t.Total > 0 {
						t.Percent = float64(cur) / float64(t.Total) * 100
					}
				})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return done, fmt.Errorf("下载中断：%w", rerr)
		}
	}
	return done, nil
}

func (m *Manager) fail(id string, err error) {
	msg := err.Error()
	m.set(id, func(t *Task) {
		t.State = StateFailed
		t.Err = msg
		t.Step = "失败"
	})
}
