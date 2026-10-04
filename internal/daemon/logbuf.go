package daemon

import (
	"sync"
)

// LogSink 让日志在进环形缓冲的同时落一份到磁盘。
// 内存 ring 一重启就没了，落盘之后崩了还能倒回去查 —— 这是排查的唯一线索。
type LogSink interface {
	Write(line string)
}

// LogBuffer 环形缓冲 + 订阅广播
type LogBuffer struct {
	mu      sync.RWMutex
	lines   []string
	cap     int
	head    int
	subs    map[uint64]chan []string
	nextSub uint64
	sink    LogSink
}

func NewLogBuffer(capacity int) *LogBuffer {
	return &LogBuffer{
		lines: make([]string, 0, capacity),
		cap:   capacity,
		subs:  make(map[uint64]chan []string),
	}
}

// SetSink 挂上落盘通道。传 nil 表示只留内存。
func (b *LogBuffer) SetSink(s LogSink) {
	b.mu.Lock()
	b.sink = s
	b.mu.Unlock()
}

func (b *LogBuffer) Append(line string) {
	b.mu.Lock()
	if len(b.lines) < b.cap {
		b.lines = append(b.lines, line)
	} else {
		b.lines[b.head] = line
		b.head = (b.head + 1) % b.cap
	}
	snapshot := []string{line}
	for _, ch := range b.subs {
		select {
		case ch <- snapshot:
		default:
		}
	}
	sink := b.sink
	b.mu.Unlock()

	// 落盘放在解锁之后：写文件可能阻塞，不能占着锁 ——
	// 否则一次慢 IO 会把控制台的日志推送全部堵死。
	if sink != nil {
		sink.Write(line)
	}
}

// Snapshot 返回按时间顺序的全部历史行
func (b *LogBuffer) Snapshot() []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]string, 0, len(b.lines))
	if len(b.lines) < b.cap {
		out = append(out, b.lines...)
		return out
	}
	out = append(out, b.lines[b.head:]...)
	out = append(out, b.lines[:b.head]...)
	return out
}

func (b *LogBuffer) Subscribe() (uint64, chan []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.nextSub
	b.nextSub++
	ch := make(chan []string, 256)
	b.subs[id] = ch
	return id, ch
}

func (b *LogBuffer) Unsubscribe(id uint64) {
	b.mu.Lock()
	delete(b.subs, id)
	b.mu.Unlock()
}
