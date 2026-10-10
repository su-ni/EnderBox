package fetch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// StandaloneOpts 借链路下一个文件要的全部输入。
type StandaloneOpts struct {
	URL     string
	Dest    string
	WantSHA string
	SHAKind string
	Size    int64

	// Label 非空 = 这次下载**作为一条可见任务**出现在下载列表里（气泡与记录都看得到进度）。
	// 留空 = 只借链路、不占任务条（现在的默认）。
	//
	// 为什么同一件事要两副面孔：整包本体是「用户点了一下、要干等几十 MB」的一件事，
	// 藏起来他就只能对着一个不动的按钮猜；而一个包里几十上百个模组文件，每个都挂一条
	// 会把真正要找的那条淹掉（几十条一起刷新界面本身也会卡）。
	// 所以**包本体可见、模组文件隐藏**。
	Label string

	// OnDelta 已落盘字节的**增量**（调用方自己累加成整批的进度）。不需要就传 nil。
	OnDelta func(int64)
}

// FetchStandalone 用下载器**同一条链路**（分片 / 续传 / 重试 / 校验 / 原子落盘）下一个文件。
//
// 给整合包用。为什么不干脆绕开整条链路自己写：分片 / 续传 / 重试 / 校验这四件事
// 各有各的坑，链路里 19 处进度上报全挂在任务 id 上。抄一份出来，迟早跟主路分叉 ——
// 到时候「主线能续传、支线不能」这种不一致最难查。所以这里只是**借任务这个壳**
// （Task.hidden，不进 List），链路本身一行不改。
func (m *Manager) FetchStandalone(ctx context.Context, o StandaloneOpts) error {
	if strings.TrimSpace(o.URL) == "" {
		// 整合包里偶尔有文件没给 downloads 地址（作者漏了/平台撤了）。
		// 如实报出来，让上层的 Failed 清单能说清是哪个文件、为什么 —— 别静默跳过。
		return fmt.Errorf("这个文件没有下载地址，没法下")
	}
	if o.Dest == "" {
		return fmt.Errorf("没有指定落盘路径")
	}

	opts := Options{
		Build:    Build{URL: o.URL, SHA: o.WantSHA, SHAKind: o.SHAKind, Size: o.Size},
		DestPath: o.Dest,
	}
	id := fmt.Sprintf("hs%d", time.Now().UnixNano())
	visible := o.Label != ""

	m.mu.Lock()
	m.prune()
	m.tasks[id] = &Task{
		ID: id, File: filepath.Base(o.Dest), Step: "下载中",
		Total: o.Size, SHA: o.WantSHA, State: StateRunning, Phase: "download",
		StartedAt: time.Now(), hidden: !visible, Label: o.Label,
	}
	m.mu.Unlock()

	// 隐藏壳用完就拆：它不是为了被查询而存在的，留着只会让任务表长草
	// （而且每次整合包升级要建上百个，不删就是泄漏）。
	//
	// ⚠ 可见任务**相反，要留着** —— 用户要能回头看到「刚才下完的是哪个」。
	// 清理交给 prune 那条半小时规则，和普通下载任务一致（同一条规则，别造第二套）。
	if !visible {
		defer func() {
			m.mu.Lock()
			delete(m.tasks, id)
			m.mu.Unlock()
		}()
	}

	// 目标目录必须在写第一个字节之前建好 —— 分片是往目标目录写的，
	// 目录不存在时报的是「无法写入分片文件」，而不是「落盘失败」，容易带偏排查方向。
	if err := os.MkdirAll(filepath.Dir(o.Dest), 0o755); err != nil {
		return fmt.Errorf("建目录失败：%w", err)
	}

	// 字节进度：由一个轮询协程统一上报（**只有它调 onDelta**，调用方不必考虑并发）。
	var reported int64
	report := func() {
		cur, ok := m.doneOf(id)
		if !ok || cur <= reported {
			return
		}
		o.OnDelta(cur - reported)
		reported = cur
	}
	stop := make(chan struct{})
	flushed := make(chan struct{})
	go func() {
		defer close(flushed)
		tick := time.NewTicker(200 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				// 收尾必须在这里补报最后一次：轮询是 200ms 一次，小文件（模组通常
				// 几 MB，本地/高速线路下不到 200ms）可能一次都没轮到 ——
				// 不补报的话进度会永远差最后一截，界面停在 99% 不动。
				//
				// ⚠ 而且这一步必须在**任务被删掉之前**做完：隐藏壳会在本函数返回时
				// 被 defer 删掉，删了之后 doneOf 就查不到了。
				// 所以下面要 `<-flushed` 等它，不能 fire-and-forget
				// （初版就是这么错的：close 完直接返回，补报被吞掉，字节数恒为 0）。
				if o.OnDelta != nil {
					report()
				}
				return
			case <-tick.C:
				if o.OnDelta != nil {
					report()
				}
			}
		}
	}()

	// 与普通下载共用同一个并发闸门：整合包升级期间用户仍可能去下服务端 jar，
	// 两条路各放各的会一起把带宽吃满。
	m.sem <- struct{}{}
	part := o.Dest + ".part"
	err := m.fetchToPart(ctx, id, opts, part)
	if err == nil {
		_, err = m.verifyAndPlace(id, opts, part)
	}
	<-m.sem

	close(stop)
	<-flushed // 等最后一次补报落地，再往下走（下面可能就是删任务）
	if visible {
		m.finishStandalone(id, err)
	}
	return err
}

// finishStandalone 给**可见**任务写终态。
//
// FetchStandalone 走的是 fetchToPart + verifyAndPlace，**不经过 run()**，
// 而 running → done / failed 这一步转换本来是 run() 干的。
// 可见任务不自己写，气泡和下载记录就会永远停在「下载中」——
// 用户等一个早就结束的任务，最后只能重启面板来清掉它。
func (m *Manager) finishStandalone(id string, err error) {
	now := time.Now()
	m.set(id, func(t *Task) {
		t.EndedAt = &now
		if err != nil {
			t.State = StateFailed
			t.Err = err.Error()
			return
		}
		t.State = StateDone
		t.Percent = 100
		t.Verified = true
	})
}

// doneOf 读任务已落盘的字节数（给进度轮询用）。
func (m *Manager) doneOf(id string) (int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return 0, false
	}
	return t.Done, true
}
