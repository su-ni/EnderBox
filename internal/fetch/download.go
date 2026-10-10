package fetch

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

	// Label 没有归属实例时（下载 JDK 就是这种）界面上显示的名字。
	// 留空时前端退回显示文件名 —— 但一个 OpenJDK21U-jdk_x64_…zip
	// 摆在那里，用户看不出这是在给 Java 21 做准备。
	Label string `json:"label,omitempty"`

	// Phase 当前处于哪一段：download（在下文件）还是 install（在下完之后的安装步骤）。
	// 前端要分开显示 —— 安装那段没有「已下载多少字节」这回事，
	// 继续画字节数只会让人以为卡住了。只有 Forge 会走到 install。
	Phase string `json:"phase"`

	// Threads 这次用了几个连接并行下载（0/1 表示单连接）。纯展示用：
	// 用户提过「想要多线程」，那就把「真的开了几条」如实说给他。
	Threads int `json:"threads"`

	cancel context.CancelFunc

	// hidden 这条任务只是「走一遍下载链路」的载体，**不出现在任务条里**。
	//
	// 用途只有一个：整合包（`FetchStandalone`）。一个包动辄几十上百个模组，
	// 每个都往任务条上挂一条，用户真正要找的那条就被淹了 —— 而任务条的设计前提
	// 本来就是「一条任务 ＝ 一个服务端 jar」。
	//
	// 之所以要借任务这个壳、而不是绕开整条链路，是因为分片 / 续传 / 重试 / 校验
	// 这四件事全都挂在任务 id 上（链路里 19 处 m.set(id,…)）。绕开就等于抄一份出来，
	// 迟早跟主路分叉。所以这里只是「让已有的链路不占任务条」，链路本身一行不改。
	hidden bool
}

// Manager 下载任务表
type Manager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	sem   chan struct{}
	seq   int64
}

// 下载重试与分片的调参。
//
// 重试：家用宽带连国外源站时，连接被中途掐断是**常态**（对方边缘节点换回源、
// 或者中间设备超时清理），不是「源站挂了」。实测用户那边报的就是
// `wsarecv: An existing connection was forcibly closed by the remote host` ——
// 这类错误重连一次多半就好，所以必须自动重试，而不是把一句英文报错丢给用户。
//
// 分片：门槛定在 2 MB。再小的文件切了不划算 —— 多开连接本身有开销
// （TLS 握手、每个分片各走一次镜像的 302 跳转），省下的时间还不够抵消。
//
// 上限 8 条（2026-10-06 实测从 4 提到 8）：瓶颈**不是**出口带宽，是源站对单连接的限速。
// 同一个 30MB 的 .mrpack，同一台机器，只改连接数：
//
//	1 条连接  96 KB/s      4 条  345 KB/s（3.6×）      8 条  695 KB/s（7.2×）
//
// 几乎线性 —— 所以「几十 MB 要拉半小时」不是链路差，是连接太少了。
// ⚠ 别再加了：BMCLAPI 有限流（实测 60 秒内只准 10 次请求），分片越碎越容易自己把自己限死；
// 而且这是公益镜像，多开连接等于替所有人抢带宽。8 是「明显提速」和「别当攻击」之间的位置。
const (
	dlAttempts  = 4
	segMinSize  = 2 << 20
	segMaxParts = 8
	segMinChunk = 1 << 20 // 每片至少 1 MB，避免切出一堆碎片
)

// permError 标记「重试也没用」的失败（4xx 这种）。重试只对连接类故障有意义。
type permError struct{ msg string }

func (e *permError) Error() string { return e.msg }

// errNoRange 服务端不支持 Range。分片和续传都靠它，遇到就得退回单连接从头下。
var errNoRange = errors.New("服务端不支持分段下载")

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

// List 某个实例的全部任务，新的在前。
// 隐藏任务（整合包那种「借链路下载」的）不在这里返回 —— 见 Task.hidden。
func (m *Manager) List(instanceID string) []*Task {
	m.mu.Lock()
	defer m.mu.Unlock()
	// ⚠ 必须是**空切片**不是 nil：它会被直接塞进 JSON 响应的 items 字段，
	// nil 切片序列化成 `null`，前端读 `.items.length` 当场炸。
	// 这里修而不是在 handler 里补 `?? []` —— 补在调用方意味着每个调用方都要记得补。
	out := []*Task{}
	for _, t := range m.tasks {
		if t.hidden {
			continue
		}
		if instanceID == "" || t.Instance == instanceID {
			cp := *t
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Cancel 取消一个下载。**只对还在跑的任务返回 true**。
//
// ⚠ 以前这里只判「cancel 函数还在不在」，而那个函数在任务结束后也不会被置空 ——
// 于是对**已经下完**的任务同样回 true，前端就弹「已取消下载」，
// 可文件其实已经装好了。这句话比不提示更糟：用户以为没装，再去装一遍。
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	t, ok := m.tasks[id]
	running := ok && t.State == StateRunning
	m.mu.Unlock()
	if !ok || !running {
		return false
	}
	if t.cancel != nil {
		t.cancel()
	}
	return true
}

// Dismiss 从任务表里删掉一条**已结束**的记录（用户手动清账本）。
//
// ⚠ 还在跑的一律拒绝：把运行中的任务从表里删掉，界面就失去了对它的唯一引用 ——
// 用户再也没法取消它，它却还在后台往磁盘写。想清掉先 Cancel，等它进终态再来删。
func (m *Manager) Dismiss(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok || t.State == StateRunning {
		return false
	}
	delete(m.tasks, id)
	return true
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
	// Label 没有归属实例时界面上显示的名字（下载 JDK 用）。有实例时前端优先显示实例名。
	Label    string
	Build    Build
	DestPath string // 最终落盘路径（通常是 <实例目录>/server.jar）

	// OnDone 落盘成功后回调。让调用方在这里补后续动作（写 eula、写 server.properties、
	// 往实例控制台写一行日志）——下载器本身不该知道实例和属性的存在。
	//
	// written 是这次真正落盘的字节数。必须传给它：构建列表里通常没有大小
	// （Forge 那 500 多个构建不可能一个个去问 Content-Length），
	// 用 Build.Size 记日志会把「5.5 MB 的安装器」写成「0 字节」。
	OnDone func(opts Options, written int64) error

	// PostInstall 落盘之后的第二阶段。只有 Forge 用得上：它给的是 installer，
	// 必须再跑一遍才产出能启动的服务端。
	//
	// 它跑在**同一个任务**里而不是另开一个任务 —— 对用户来说「下 Forge」就是一件事，
	// 拆成两个任务会让他点两次、还得自己知道要点第二次。
	// 它失败时整个任务判失败（而不是像 OnDone 那样只给个告警）：文件虽然落盘了，
	// 但目录里没有可启动的东西，「就绪」两个字会是彻头彻尾的谎。
	//
	// report 用来把阶段文案写回任务，让界面显示得到过程。
	PostInstall func(ctx context.Context, report func(step string)) error
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
		Label:     opts.Label,
		File:      filepath.Base(opts.DestPath),
		Step:      "排队中",
		Total:     opts.Build.Size,
		SHA:       opts.Build.SHA,
		State:     StateRunning,
		Phase:     "download",
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

// run 真正的下载流程。分四步走，每步都写进 Step ——
// 「卡在 87%」和「卡在校验」是两种完全不同的故障，界面必须能分辨。
func (m *Manager) run(ctx context.Context, id string, opts Options) {
	defer func() {
		now := time.Now()
		m.set(id, func(t *Task) { t.EndedAt = &now })
	}()

	m.sem <- struct{}{}
	defer func() { <-m.sem }()

	part := opts.DestPath + ".part"

	// 目标目录必须在下第一个字节之前建好。
	// 多线程下载会往同一目录写 .part.N 分片，目录不存在时报的是「无法写入分片文件」；
	// 而落盘阶段的 MkdirAll 太晚 —— 那时分片早就写失败了。
	// 服务端 jar 因为实例目录本来就存在，一直没暴露这个问题；JDK 下到 runtimes/.download
	// 这种全新目录时才炸出来（实测）。
	if err := os.MkdirAll(filepath.Dir(opts.DestPath), 0o755); err != nil {
		m.fail(id, err)
		return
	}

	// ── 下载（带自动重试）──
	if err := m.fetchToPart(ctx, id, opts, part); err != nil {
		m.fail(id, err)
		return
	}

	// ── 校验 + 落盘 ──
	written, err := m.verifyAndPlace(id, opts, part)
	if err != nil {
		m.fail(id, err)
		return
	}

	if opts.OnDone != nil {
		m.set(id, func(t *Task) { t.Step = "收尾中" })
		if err := opts.OnDone(opts, written); err != nil {
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

	// ── 第二阶段：Forge 的 installer 要真跑一遍才算装完 ──
	if opts.PostInstall != nil {
		m.set(id, func(t *Task) {
			t.Phase = "install"
			t.Step = "开始安装"
			// 安装阶段的进度不是字节数，把下载留下的数字清掉 ——
			// 否则界面会停在「100% / 18.9 MB」不动，看起来像卡死
			t.Total, t.Done, t.Percent, t.Rate, t.Threads = 0, 0, 0, 0, 0
		})
		err := opts.PostInstall(ctx, func(step string) {
			m.set(id, func(t *Task) { t.Step = step })
		})
		if err != nil {
			if ctx.Err() != nil {
				m.fail(id, fmt.Errorf("已取消"))
				return
			}
			// 下载是成功的，但装不出来 —— 对用户来说这就是失败，不能报「就绪」
			m.fail(id, err)
			return
		}
		written = 0
	}

	m.set(id, func(t *Task) {
		t.Step = "就绪"
		t.State = StateDone
		t.Done = written
		t.Percent = 100
	})
}

// verifyAndPlace 下载完之后的两件事：校验、原子落盘。返回真正落盘的字节数。
//
// 抽成独立方法是为了让「借链路下载但不占任务条」（FetchStandalone）用**同一份**逻辑。
// 校验这件事只能有一份实现 —— 抄一份出来，一边改了另一边没改，就会出现
// 「主线校验/支线不校验」这种最难查的不一致。
//
// 校验和是「下完之后通读一遍算」而不是「边下边算」：因为会重试、会分片 ——
// 分片是乱序落盘的，边下边算的 hash 顺序必然是错的；重试则会让「已经算进去的那一段」
// 和「文件里实际还在的那一段」对不上（hash 状态没法回滚）。
// 通读一遍多花的是本地磁盘时间（50 MB 也就零点几秒），换来的是逻辑上不可能算错。
func (m *Manager) verifyAndPlace(id string, opts Options, part string) (int64, error) {
	m.set(id, func(t *Task) { t.Step = "校验中" })
	if opts.Build.SHA != "" {
		got, err := hashFile(part, opts.Build.SHAKind)
		if err != nil {
			return 0, fmt.Errorf("读取下载文件失败：%w", err)
		}
		if !strings.EqualFold(got, opts.Build.SHA) {
			removePart(part)
			return 0, fmt.Errorf(
				"文件校验不通过，已删除。期望 %s…，实际 %s…（多半是下载过程中被截断，重试一次通常就好）",
				clip(opts.Build.SHA, 12), clip(got, 12))
		}
		m.set(id, func(t *Task) { t.Verified = true })
	}

	var written int64
	if st, err := os.Stat(part); err == nil {
		written = st.Size()
	}

	// ── 落盘：rename 是原子的，避免出现「半个 jar 被当成完整的」──
	// 目标目录已在下载开始前 MkdirAll 过，这里不再重复。
	_ = os.Remove(opts.DestPath) // Windows 上 rename 不能覆盖已存在的文件
	if err := os.Rename(part, opts.DestPath); err != nil {
		return 0, fmt.Errorf("落盘失败：%w", err)
	}
	return written, nil
}

// fetchToPart 反复尝试把文件下到 part，直到成功或用完重试次数。
//
// 只重试**连接类**故障：4xx（地址错了、版本被撤了）重试一百次也没用，
// 只会让用户多等几十秒才看到同一句错。
//
// ⚠ 取消 = **连半截文件一起清掉**（2026-10-10 用户拍板「取消即删」；原口径是留着续传）。
// 清理挂在这里而不是调用方的错误分支：取消有两个出口（下载中取消 / 重试等待中取消），
// 一处 defer 全兜住，将来加第三个出口也不会漏。
// ⚠⚠ 只清「失败 + 已取消」这一种；**普通网络失败照旧保留 .part** —— 那种场景续传是主诉求。
// 为什么不在 Cancel() 里直接删：那一刻下载协程还在往文件里写，Windows 上会撞「文件被占用」；
// 走到这里时写协程已退出、句柄全关，删除才是稳的。
func (m *Manager) fetchToPart(ctx context.Context, id string, opts Options, part string) (err error) {
	defer func() {
		if err != nil && ctx.Err() != nil {
			removePart(part)
		}
	}()
	var lastErr error
	for attempt := 1; attempt <= dlAttempts; attempt++ {
		if attempt > 1 {
			d := backoffDelay(attempt)
			m.set(id, func(t *Task) {
				t.Step = fmt.Sprintf("连接被中断，%d 秒后重试（第 %d 次）", int(d.Seconds()), attempt)
			})
			if !sleepCtx(ctx, d) {
				return fmt.Errorf("已取消")
			}
		}
		derr := m.downloadOnce(ctx, id, opts, part)
		if derr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("已取消")
		}
		var pe *permError
		if errors.As(derr, &pe) {
			return derr
		}
		lastErr = derr
	}
	return fmt.Errorf("%w（已自动重试 %d 次仍失败；可以先重试，或者在「下载源」里换一个镜像）",
		lastErr, dlAttempts-1)
}

// downloadOnce 一次尝试。已下到的部分走续传，够大且支持 Range 就走分片。
func (m *Manager) downloadOnce(ctx context.Context, id string, opts Options, part string) error {
	// 上一轮留下的分片临时文件还在 → 接着分片下（每个分片自己记着自己的进度）
	if len(findSegIndices(part)) > 0 {
		return m.attemptSegmented(ctx, id, opts, part, nil)
	}

	var offset int64
	if st, err := os.Stat(part); err == nil && st.Size() > 0 {
		offset = st.Size()
	}
	if offset > 0 {
		m.set(id, func(t *Task) {
			t.Resumed = true
			t.Done = offset
			t.Step = "续传中"
		})
	}

	req, err := newRequest(opts.Build.URL)
	if err != nil {
		return &permError{err.Error()}
	}
	// 即使从零开始也带 Range —— 带上服务端才回 206，我们才能从 Content-Range 里
	// 读到文件总大小，进而决定要不要分片。不带的话永远只拿到 200，
	// 于是「多线程下载」这条路径一次都不会走到（踩过：装了个看似正常的单线程）。
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	// 走 dlClient：不设总超时，否则慢网下大文件必失败（见 source.go 里的说明）
	resp, err := dlClient.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("已取消")
		}
		return fmt.Errorf("下载失败：%w", err)
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		total := totalFromContentRange(resp)
		// 从零开始、文件够大、而且服务端确实支持 Range → 分片并行下。
		// 这里必须从零：.part 里已有的那一段是顺序写下的，和分片的位置对不上。
		if offset == 0 && total >= segMinSize {
			resp.Body.Close()
			return m.attemptSegmented(ctx, id, opts, part, &segPlan{total: total})
		}
		if total > 0 {
			m.set(id, func(t *Task) { t.Total = total })
		}
		return m.downloadStream(ctx, id, part, resp, offset, total)

	case http.StatusOK:
		// 服务端不认 Range，把整个文件从头给了回来。那就老老实实从头写。
		if offset > 0 {
			offset = 0
		}
		m.set(id, func(t *Task) {
			t.Step = "下载中"
			if resp.ContentLength > 0 {
				t.Total = resp.ContentLength
			}
		})
		return m.downloadStream(ctx, id, part, resp, 0, resp.ContentLength)

	case http.StatusRequestedRangeNotSatisfiable:
		// .part 比远端文件还大，说明它是上一次失败留下的脏数据，删掉重来
		resp.Body.Close()
		removePart(part)
		return fmt.Errorf("本地残留的临时文件比远端文件还大，已删除，正在重新下载")

	default:
		resp.Body.Close()
		// 一律当可重试：BMCLAPI 这类镜像背后是一堆节点轮着给（实测每次落到不同域名），
		// 某个节点抽风回 404 是常事，换一次就换到好的了。真正的「版本不存在」在上一步
		// 取 .sha1 时就已经被拦下了，不会走到这里。
		return fmt.Errorf("下载地址返回 HTTP %d（%s）", resp.StatusCode, hostOfResp(resp))
	}
}

// downloadStream 单连接写入。offset 是已经躺在 part 里的字节数（0 表示从头写）。
func (m *Manager) downloadStream(ctx context.Context, id, part string, resp *http.Response, offset, total int64) error {
	defer resp.Body.Close()

	m.set(id, func(t *Task) {
		t.Threads = 0
		if offset == 0 {
			t.Step = "下载中"
		}
	})

	flags := os.O_CREATE | os.O_WRONLY
	if offset > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return fmt.Errorf("无法写入 %s：%w", part, err)
	}
	defer f.Close()

	done, err := m.copyProgress(ctx, id, f, resp.Body, offset)
	if err != nil {
		return err
	}
	m.set(id, func(t *Task) {
		t.Done = done
		if total > 0 {
			t.Percent = float64(done) / float64(total) * 100
		}
	})
	return nil
}

// attemptSegmented 跑分片下载；万一服务端其实不支持 Range（某些镜像会把
// 范围请求当普通请求、回一整份），就清掉分片退回单连接。
func (m *Manager) attemptSegmented(ctx context.Context, id string, opts Options, part string, plan *segPlan) error {
	err := m.downloadSegmented(ctx, id, opts, part, plan)
	if err == nil {
		return nil
	}
	if errors.Is(err, errNoRange) {
		removeSegFiles(part)
		return m.singleStream(ctx, id, opts, part)
	}
	return err
}

// singleStream 重新发一个完整请求，单连接下到底。
// 只在「本来想分片、但服务端不支持」时兜底用。
func (m *Manager) singleStream(ctx context.Context, id string, opts Options, part string) error {
	req, err := newRequest(opts.Build.URL)
	if err != nil {
		return &permError{err.Error()}
	}
	resp, err := dlClient.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("已取消")
		}
		return fmt.Errorf("下载失败：%w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return &permError{fmt.Sprintf("下载地址返回 HTTP %d（%s）", resp.StatusCode, hostOf(opts.Build.URL))}
	}
	total := resp.ContentLength
	if total <= 0 {
		total = totalFromContentRange(resp)
	}
	return m.downloadStream(ctx, id, part, resp, 0, total)
}

// segRange 一个分片在最终文件里的位置（闭区间）
type segRange struct{ start, end int64 }

func (r segRange) len() int64 { return r.end - r.start + 1 }

// segPlan 分片方案。total 是远端文件总大小。
type segPlan struct{ total int64 }

// segPath 第 i 个分片的临时文件。用**独立文件**而不是往同一个 part 里
// WriteAt，是因为独立文件天然带续传状态（文件多大就是下了多少），
// 不会出现「中间挖了个洞但文件尺寸看着像下完了」这种最难查的坏法。
func segPath(part string, i int) string { return fmt.Sprintf("%s.%d", part, i) }

// findSegIndices 找出 part 旁边还没合并的分片号（升序）。
func findSegIndices(part string) []int {
	entries, err := os.ReadDir(filepath.Dir(part))
	if err != nil {
		return nil
	}
	prefix := filepath.Base(part) + "."
	var idx []int
	for _, e := range entries {
		n := e.Name()
		if !strings.HasPrefix(n, prefix) {
			continue
		}
		if i, err := strconv.Atoi(n[len(prefix):]); err == nil && i >= 0 {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	return idx
}

// planSegments 把 [0,total) 均分成若干片，余数摊给前面几片。
func planSegments(total int64) []segRange {
	n := segMaxParts
	if max := int(total / segMinChunk); max < n {
		n = max
	}
	if n < 2 {
		n = 2
	}
	base := total / int64(n)
	rem := total % int64(n)
	var out []segRange
	var at int64
	for i := 0; i < n; i++ {
		size := base
		if int64(i) < rem {
			size++
		}
		if size <= 0 {
			continue
		}
		out = append(out, segRange{start: at, end: at + size - 1})
		at += size
	}
	return out
}

// downloadSegmented 多连接并行下载。
//
// plan 为 nil 表示「从已有的分片文件续传」—— 这时总量得重新问一次源站
// （分片文件只记进度，不记总量）。
func (m *Manager) downloadSegmented(ctx context.Context, id string, opts Options, part string, plan *segPlan) error {
	if plan == nil {
		total, err := m.probeTotal(ctx, opts.Build.URL)
		if err != nil {
			return err
		}
		plan = &segPlan{total: total}
		// 残留的分片和这次的总量对不上（上游换过文件，或者上次是别的分片方案），
		// 整组丢掉重下 —— 硬拼出来的必然是坏文件，而且要到校验那一步才暴露。
		if segFilesMismatch(part, plan.total) {
			removeSegFiles(part)
		}
	}
	if plan.total < segMinSize {
		removeSegFiles(part)
		return m.singleStream(ctx, id, opts, part)
	}

	parts := planSegments(plan.total)
	if len(parts) < 2 {
		removeSegFiles(part)
		return m.singleStream(ctx, id, opts, part)
	}

	m.set(id, func(t *Task) {
		t.Total = plan.total
		t.Threads = len(parts)
		t.Step = fmt.Sprintf("多线程下载（%d 线程）", len(parts))
	})

	// 每片的当前进度先按本地文件大小读出来，之后由各自的 worker 递增
	sizes := make([]int64, len(parts))
	for i, p := range parts {
		if st, err := os.Stat(segPath(part, i)); err == nil {
			s := st.Size()
			if s > p.len() {
				s = p.len()
			}
			sizes[i] = s
		}
	}

	var wmu sync.Mutex
	// 速率按两次 report 之间的增量算 —— 和单连接那条路（downloadStream）同一个口径。
	// 以前这里只报 Done/Percent：8 个连接在猛跑，界面上速度恒为 0，
	// 「多线程」的意义反而看不见了（用户当初抱怨慢，速度正是他最想看的东西）。
	var lastAt time.Time
	var lastDone int64
	var lastRate int64
	var idle int // 连续几个采样窗口一个字节都没到
	report := func() {
		wmu.Lock()
		var sum int64
		for _, s := range sizes {
			sum += s
		}
		wmu.Unlock()
		now := time.Now()
		var rate int64
		if !lastAt.IsZero() {
			if dt := now.Sub(lastAt).Seconds(); dt > 0 {
				rate = int64(float64(sum-lastDone) / dt)
			}
		}
		// ⚠ 200ms 窗口里一个字节都没到（源站卡顿 / 网络空窗）时**别立刻刷成 0**：
		// 那是采样窗口的假象，不是真的停了，直接归零会让界面上的速度忽有忽无。
		// 连续 5 个窗口（约 1 秒）都没动静才认输显示 0。
		if sum == lastDone {
			idle++
			if idle <= 5 {
				rate = lastRate
			}
		} else {
			idle = 0
		}
		lastAt, lastDone, lastRate = now, sum, rate
		m.set(id, func(t *Task) {
			t.Done = sum
			t.Rate = rate
			if t.Total > 0 {
				t.Percent = float64(sum) / float64(t.Total) * 100
			}
		})
	}
	report()

	stopTicker := make(chan struct{})
	go func() {
		tk := time.NewTicker(200 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopTicker:
				return
			case <-tk.C:
				report()
			}
		}
	}()
	defer close(stopTicker)

	ctx2, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		emu      sync.Mutex
		firstErr error
	)
	for i, p := range parts {
		have := sizes[i]
		if have >= p.len() {
			continue // 这片已经下完，只等合并
		}
		i, p, have := i, p, have
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := m.segWorker(ctx2, opts.Build.URL, segPath(part, i), p, have, func(n int64) {
				wmu.Lock()
				sizes[i] += n
				wmu.Unlock()
			})
			if err != nil {
				emu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				emu.Unlock()
				cancel() // 一片挂了，别的也别白跑
			}
		}()
	}
	wg.Wait()
	report()

	if firstErr != nil {
		return firstErr
	}

	m.set(id, func(t *Task) { t.Step = "合并分片" })
	return concatSegments(part, len(parts))
}

// segWorker 下第 i 片。have 是这片已经有的字节数（接着它往后要）。
func (m *Manager) segWorker(ctx context.Context, url, spath string, p segRange, have int64, onDelta func(int64)) error {
	req, err := newRequest(url)
	if err != nil {
		return &permError{err.Error()}
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", p.start+have, p.end))
	resp, err := dlClient.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("已取消")
		}
		return fmt.Errorf("分片下载失败：%w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		// 我们要的是某一段，它却回了整份 —— 说明它根本不认 Range
		return errNoRange
	}
	if resp.StatusCode != http.StatusPartialContent {
		return &permError{fmt.Sprintf("分片请求返回 HTTP %d（%s）", resp.StatusCode, hostOf(url))}
	}

	f, err := os.OpenFile(spath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("无法写入分片文件 %s：%w", spath, err)
	}
	defer f.Close()

	_, err = m.copyProgress(ctx, "", f, resp.Body, 0, func(n int64) { onDelta(n) })
	return err
}

// concatSegments 把所有分片按顺序拼成 part，拼完删掉分片文件。
func concatSegments(part string, n int) error {
	out, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("无法写入 %s：%w", part, err)
	}
	defer out.Close()

	for i := 0; i < n; i++ {
		sp := segPath(part, i)
		in, err := os.Open(sp)
		if err != nil {
			return fmt.Errorf("缺少分片 %s，无法合并（重新下载一次）", filepath.Base(sp))
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			return fmt.Errorf("合并分片失败：%w", err)
		}
		in.Close()
		_ = os.Remove(sp)
	}
	return nil
}

// probeTotal 问一次源站文件有多大（Range: bytes=0-0 只会拿回 1 个字节）。
func (m *Manager) probeTotal(ctx context.Context, url string) (int64, error) {
	req, err := newRequest(url)
	if err != nil {
		return 0, &permError{err.Error()}
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := dlClient.Do(req.WithContext(ctx))
	if err != nil {
		if ctx.Err() != nil {
			return 0, fmt.Errorf("已取消")
		}
		return 0, fmt.Errorf("下载失败：%w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return 0, errNoRange
	}
	total := totalFromContentRange(resp)
	if total <= 0 {
		return 0, errNoRange
	}
	return total, nil
}

// copyProgress 带进度上报的复制。每 200ms 更新一次进度就够 ——
// 每个 64 KB 块都写一次 task 会让轮询返回一堆没人看得清的中间值。
// id 为空表示这是个分片 worker（进度汇总由调用方自己算）。
func (m *Manager) copyProgress(ctx context.Context, id string, dst io.Writer, src io.Reader, base int64, onDelta ...func(int64)) (int64, error) {
	buf := make([]byte, 64*1024)
	done := base
	lastAt := time.Now()
	lastDone := done
	var delta func(int64)
	if len(onDelta) > 0 {
		delta = onDelta[0]
	}

	for {
		if ctx.Err() != nil {
			return done, fmt.Errorf("已取消")
		}
		n, rerr := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return done, fmt.Errorf("写盘失败：%w", werr)
			}
			done += int64(n)
			if delta != nil {
				delta(int64(n))
			}

			if id != "" && time.Since(lastAt) >= 200*time.Millisecond {
				rate := int64(float64(done-lastDone) / time.Since(lastAt).Seconds())
				lastAt, lastDone = time.Now(), done
				m.set(id, func(t *Task) {
					t.Done = done
					t.Rate = rate
					if t.Total > 0 {
						t.Percent = float64(done) / float64(t.Total) * 100
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

// ── 小工具 ──

// totalFromContentRange 从 `Content-Range: bytes 0-0/8837089` 里取总大小。
// 取不到返回 0（调用方按「大小未知」处理，不假装知道）。
func totalFromContentRange(resp *http.Response) int64 {
	cr := resp.Header.Get("Content-Range")
	i := strings.LastIndex(cr, "/")
	if i < 0 || i == len(cr)-1 {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(cr[i+1:]), 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// hostOfResp 报错时用**最终落地的主机名**（跟完跳转之后的），
// 而不是我们请求的那个。镜像是一条跳转链，写源站名会让人查错地方。
func hostOfResp(resp *http.Response) string {
	if resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.Host
	}
	return ""
}

// hashFile 通读文件算校验和。kind 是 sha1 / sha256 / sha512（空与不认识的按 sha256 算）。
//
// ⚠ sha512 不是锦上添花：Modrinth 索引里的模组哈希清一色是 sha512（可能带个 sha1 兜底），
// 整合包升级把种类原样传下来，这里不认的话校验全按 sha256 算 ——
// 结果是**每一个模组都过不了校验**，用户看到的是 129 个「下载失败」。
func hashFile(path, kind string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var h hash.Hash
	switch {
	case strings.EqualFold(kind, "sha1"):
		h = sha1.New()
	case strings.EqualFold(kind, "sha512"):
		h = sha512.New()
	default:
		h = sha256.New()
	}
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// segFilesMismatch 判断残留的分片文件能不能配上这次的分片方案。
// 任何一处对不上（片数不同、编号断档、某片比它该有的长度还大）都算脏数据。
func segFilesMismatch(part string, total int64) bool {
	ranges := planSegments(total)
	idx := findSegIndices(part)
	if len(idx) == 0 {
		return false
	}
	if len(idx) != len(ranges) {
		return true
	}
	for n, i := range idx {
		if i != n {
			return true
		}
		st, err := os.Stat(segPath(part, i))
		if err != nil || st.Size() > ranges[n].len() {
			return true
		}
	}
	return false
}

// removeSegFiles 只清分片临时文件。退回单连接时要清 ——
// 不然下一轮 findSegIndices 还会看见它们，又拐回分片那条路。
func removeSegFiles(part string) {
	for _, i := range findSegIndices(part) {
		_ = os.Remove(segPath(part, i))
	}
}

// removePart 把 part 和分片临时文件一起清掉，换源重下时用。
func removePart(part string) {
	removeSegFiles(part)
	_ = os.Remove(part)
}

// backoffDelay 第 attempt 次尝试前等多久：第 2 次等 1 秒，之后翻倍。
// 别等太久 —— 用户就坐在屏幕前面看着进度条。
func backoffDelay(attempt int) time.Duration {
	if attempt < 2 {
		return 0
	}
	d := time.Second << uint(attempt-2)
	if d > 8*time.Second {
		d = 8 * time.Second
	}
	return d
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// clip 截断长串用于报错信息，短串原样返回。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
