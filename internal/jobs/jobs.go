// Package jobs 统一的异步作业框架。
//
// 把「一笔要花分钟级的活（备份压包 / 还原解包 / 整合包导出 / 模组升级…）从 HTTP
// handler 里挪出去，起 goroutine 跑，前端靠轮询拿进度」这件事抽成一处通用实现，
// 避免每个慢操作各写一套进度表（项目早期就是这么碎的：modpack 一套、下载一套、
// 备份又是一套，四套互不兼容）。
//
// 设计直接沿用 modpack 那套被验证过的思路：内存表 + 一把锁保护所有字段读写、
// 「同 owner 只准一笔在跑」的闸与入表同锁、终态作业挂在新作业节点上做过期清理、
// 快照恒带全字段（空集合给 [] 不给 null）。差别只在把 modpack 专属的
// kind/result 变成了泛型字段，让所有慢操作共用。
package jobs

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// State 作业状态。
const (
	StateRunning  = "running"
	StateDone     = "done"
	StateFailed   = "failed"
	StateCanceled = "canceled"
)

// ProgressFunc 执行 goroutine 边干边回报的进度回调。
// 各参数含义：已处理文件数 / 总文件数 / 已处理字节 / 总字节 / 当前文件（相对路径）。
// 总数为 0 表示「还没开始数」，前端据此显示「准备中」而不是 NaN 百分比。
type ProgressFunc func(filesDone, filesTotal int, bytesDone, bytesTotal int64, current string)

// Progress 进度快照。前端直接读 .FilesTotal 等做百分比。
type Progress struct {
	FilesDone  int    `json:"filesDone"`
	FilesTotal int    `json:"filesTotal"`
	BytesDone  int64  `json:"bytesDone"`
	BytesTotal int64  `json:"bytesTotal"`
	Current    string `json:"current"`
}

// Job 一笔异步作业。所有字段的读写都在 Store.mu 之内 ——
// 进度回调来自执行 goroutine，快照来自 HTTP handler，两边没有别的同步点。
type Job struct {
	ID     string
	Owner  string // 归属实例 id；全局作业（如 JDK 下载）可空
	Kind   string // 作业种类：modpack-upgrade / modpack-install / backup-create / backup-restore / modpack-export ……
	State  string
	Stage  string // 现在在干嘛："" | prepare | zip | unzip | files | jar …… 只影响文案，不影响进度字段
	Progress
	Result any    // 终态结算，各 kind 自己填（map 或具体 struct）。快照时保证非 nil。
	Err    string // 失败/取消原因（用户能读的句子）
	ctx    context.Context
	cancel context.CancelFunc
	startedAt time.Time
}

// Store 内存作业表。进程重启即清空 —— 做到一半断电，磁盘上是「做到一半」的现实，
// 重新预览/操作会如实重算，所以不持久化。
type Store struct {
	mu  sync.Mutex
	ops map[string]*Job
}

// 终态作业保留多久。留着是为了让用户切走再回来还能看到「刚才那次成什么样了」；
// 过了这个点就只是占内存。不做后台定时器 —— 家用面板频率撑不起专门清理循环，
// 挂在「新建作业」这个必然发生的节点上就够了。
const opLifetime = 24 * time.Hour

// NewStore 建一个空作业表。
func NewStore() *Store {
	return &Store{ops: map[string]*Job{}}
}

// New 建一笔作业并登记入表，返回 nil 表示该 owner 已有一笔 running 作业（同 owner 闸）。
//
// 「建」与「注册」合一：装/升级跑的不是同一个函数，但「同 owner 只准一笔」这条闸
// 必须共用一份实现，各写一遍总有一天两边不一致，症状是俩执行器同时写同一批文件。
// cancel 在入表**之前**就挂上：取消请求可能比 goroutine 跑起来还快，晚挂那一下就落空。
func (s *Store) New(owner, kind string) *Job {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	job := &Job{
		ID:        fmt.Sprintf("job_%d", now.UnixNano()),
		Owner:     owner,
		Kind:      kind,
		State:     StateRunning,
		ctx:       ctx,
		cancel:    cancel,
		startedAt: now,
	}
	if !s.register(job) {
		cancel()
		return nil
	}
	return job
}

// register 把作业登记入表，返回 false 表示该 owner 已有一笔 running 作业。
// 「同 owner 只准一笔在跑」的检查**必须**和插入在同一把锁里完成。
func (s *Store) register(job *Job) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ex := range s.ops {
		if ex.Owner == job.Owner && ex.State == StateRunning {
			return false
		}
	}
	now := time.Now()
	for id, ex := range s.ops {
		if ex.State != StateRunning && now.Sub(ex.startedAt) > opLifetime {
			delete(s.ops, id)
		}
	}
	// UnixNano 一纳秒内撞上是理论可能 —— 撞了就换一个，别把别人悄悄覆盖掉。
	for {
		if _, exists := s.ops[job.ID]; !exists {
			break
		}
		job.ID = fmt.Sprintf("job_%d", time.Now().UnixNano())
	}
	s.ops[job.ID] = job
	return true
}

// PrimeResult 给 running 的作业预置一份结算值。
//
// ⚠ 取消有可能在 goroutine 跑到 Finish 之前就把作业标成终态（Cancel 直接写 state）：
// 那一小段窗口里 Result 还是 nil，快照只能给个空对象 —— 而「结算里的集合字段恒在、
// 空了给 []」是本 IP 的硬规矩，缺字段比 null 还糟（前端 .length 当场炸）。
// 发起方在起作业时把零值结算预置进去，这扇窗就关上了。
func (s *Store) PrimeResult(job *Job, result any) {
	s.mu.Lock()
	job.Result = result
	s.mu.Unlock()
}

// SetStage 更新「现在在干嘛」。与进度同锁 —— 否则前端可能读到
// 「阶段已到 files 但进度还是上一个阶段的数字」这种自相矛盾的快照。
func (s *Store) SetStage(job *Job, stage string) {
	s.mu.Lock()
	job.Stage = stage
	s.mu.Unlock()
}

// UpdateProgress 边干边报进度。
func (s *Store) UpdateProgress(job *Job, filesDone, filesTotal int, bytesDone, bytesTotal int64, current string) {
	s.mu.Lock()
	job.FilesDone = filesDone
	job.FilesTotal = filesTotal
	job.BytesDone = bytesDone
	job.BytesTotal = bytesTotal
	job.Current = current
	s.mu.Unlock()
}

// Finish 标记终态并写入结算。state 必须是 done/failed/canceled 之一。
//
// ⚠ stage 一并清空：它描述的是「现在在干嘛」，作业到终态就不该再显示上一段的
// 阶段文案（报错了还挂着「正在装模组」是自相矛盾的画面）。界面据此判定要不要
// 显示阶段行，所以这条必须落在会场（Store）里，不能指望每个调用点自觉。
func (s *Store) Finish(job *Job, state string, result any, err string) {
	s.mu.Lock()
	job.State = state
	job.Stage = ""
	job.Result = result
	job.Err = err
	s.mu.Unlock()
}

// Snapshot 对外的作业快照。state/progress/result/err 四个字段恒在 ——
// 前端的类型把 result 当非必需，running 时给零值 + 空集合，不给 undefined。
func (s *Store) Snapshot(job *Job) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := job.Result
	if res == nil {
		res = map[string]any{}
	}
	return map[string]any{
		"id":        job.ID,
		"owner":     job.Owner,
		"kind":      job.Kind,
		"state":     job.State,
		"stage":     job.Stage,
		"progress":  job.Progress,
		"result":    res,
		"err":       job.Err,
		"startedAt": job.startedAt.UnixMilli(),
	}
}

// Get 取一笔作业（不分状态）；不存在返回 nil。
func (s *Store) Get(id string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ops[id]
}

// List 列出作业。owner 非空时只返回该 owner 的；否则全部。终态按 startedAt 倒序（新在前）。
func (s *Store) List(owner string) []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Job, 0, len(s.ops))
	for _, j := range s.ops {
		if owner != "" && j.Owner != owner {
			continue
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].startedAt.After(out[j].startedAt) })
	return out
}

// Cancel 取消一笔作业。返回 false = 作业不存在或已终态。
func (s *Store) Cancel(id string) bool {
	s.mu.Lock()
	job := s.ops[id]
	if job == nil || job.State != StateRunning {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	job.cancel()
	s.mu.Lock()
	job.State = StateCanceled
	s.mu.Unlock()
	return true
}

// Dismiss 把终态作业从表里移除（用户看过了「刚才那次结果」就清掉）。
// 返回 false = 不存在或非终态。
func (s *Store) Dismiss(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	job := s.ops[id]
	if job == nil || job.State == StateRunning {
		return false
	}
	delete(s.ops, id)
	return true
}

// Ctx 返回作业自己的取消上下文（执行 goroutine 用来感知取消）。
func (s *Store) Ctx(job *Job) context.Context {
	return job.ctx
}
