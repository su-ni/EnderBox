package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 守护进程自己产生的日志统一放 <baseDir>/logs/，不写进实例目录的 logs/ ——
// 那里是服务端自己的地盘（Paper 会写 latest.log），两边同时写同一个文件会互相穿插成乱码。
const (
	logMaxBytes = 8 << 20 // 单文件 8 MB
	logKeep     = 5       // 每个实例最多留 5 个历史文件
)

// fileSink 把一个实例的日志按行追加到 <dir>/<name>.log，超过上限就滚动
type fileSink struct {
	mu   sync.Mutex
	dir  string
	name string
	f    *os.File
	size int64
	dead bool
}

func newFileSink(dir, name string) *fileSink {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// 建不出来就退化成「只留内存」，不要让整个实例因为日志目录起不了
		return &fileSink{dead: true}
	}
	s := &fileSink{dir: dir, name: name}
	s.open()
	return s
}

func (s *fileSink) path() string { return filepath.Join(s.dir, s.name) }

func (s *fileSink) open() {
	f, err := os.OpenFile(s.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		s.f = nil
		return
	}
	s.f = f
	if st, err := f.Stat(); err == nil {
		s.size = st.Size()
	}
}

func (s *fileSink) Write(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead || s.f == nil {
		return
	}
	n, err := s.f.WriteString(line + "\n")
	if err != nil {
		s.f.Close()
		s.f = nil
		return
	}
	s.size += int64(n)
	if s.size >= logMaxBytes {
		s.rotate()
	}
}

// rotate 滚动：当前文件改名带时间戳，然后重新开一个干净的
func (s *fileSink) rotate() {
	s.f.Close()
	s.f = nil
	base := strings.TrimSuffix(s.name, ".log")
	stamp := time.Now().Format("20060102-150405")
	_ = os.Rename(s.path(), filepath.Join(s.dir, fmt.Sprintf("%s.%s.log", base, stamp)))
	s.size = 0
	s.open()
	s.prune()
}

func (s *fileSink) prune() {
	base := strings.TrimSuffix(s.name, ".log")
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	var olds []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || n == s.name {
			continue
		}
		// 只清自己这个实例的历史，别动别人的
		if strings.HasPrefix(n, base+".") && strings.HasSuffix(n, ".log") {
			olds = append(olds, n)
		}
	}
	if len(olds) <= logKeep {
		return
	}
	sort.Strings(olds)
	for _, n := range olds[:len(olds)-logKeep] {
		_ = os.Remove(filepath.Join(s.dir, n))
	}
}

func (s *fileSink) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.f != nil {
		s.f.Close()
		s.f = nil
	}
}

// sessionBanner 会话分隔线：日志行本身不带额外时间戳（服务端自己已经给了），
// 所以每次起服插一条锚点，否则翻历史时根本分不清哪一段是哪次运行。
func sessionBanner() string {
	return fmt.Sprintf("[ServerBox] ===== 会话开始 %s =====", time.Now().Format("2006-01-02 15:04:05"))
}

// humanDelay 把 1m0s 这种 Go 默认格式换成中文里顺口的说法。
// 「1m0s 后自动重启」读起来像报错，不如「60 秒后」。
func humanDelay(d time.Duration) string {
	sec := int(d.Seconds())
	if sec < 60 {
		return fmt.Sprintf("%d 秒", sec)
	}
	if sec%60 == 0 {
		return fmt.Sprintf("%d 分钟", sec/60)
	}
	return fmt.Sprintf("%d 分 %d 秒", sec/60, sec%60)
}

// WriteCrashSnapshot 崩溃时把最后 N 行单独存一份。
// 向导里承诺过「崩之前最后 200 行会自动存档」—— 承诺了就得有。
func (m *Manager) WriteCrashSnapshot(id string, lines []string, errMsg string) (string, error) {
	dir := filepath.Join(m.baseDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("%s-crash-%s.log", id, time.Now().Format("20060102-150405"))
	p := filepath.Join(dir, name)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# ServerBox 崩溃现场\n")
	fmt.Fprintf(&sb, "# 时间：%s\n", time.Now().Format("2006-01-02 15:04:05"))
	if errMsg != "" {
		fmt.Fprintf(&sb, "# 退出信息：%s\n", errMsg)
	}
	fmt.Fprintf(&sb, "# 下面是从内存缓冲里截到的最后 %d 行\n\n", len(lines))
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(p, []byte(sb.String()), 0o644); err != nil {
		return "", err
	}
	return p, nil
}
