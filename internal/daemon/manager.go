package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/su-ni/ServerBox/internal/store"
)

// 自动重启的护栏。
// 没有上限的「崩了就重开」在配置错误时是最糟的形态：服务端起不来 → 立刻崩 → 3 秒后重开，
// 每秒拉一次 JVM，机器风扇直接起飞，而用户只会看到日志疯狂滚动。
const (
	maxAutoRestarts    = 3
	crashSnapshotLines = 200
	stableRunSeconds   = 180 // 跑够这么久才算「稳定」，之前的崩溃计数才清零
)

var restartBackoff = []time.Duration{5 * time.Second, 20 * time.Second, 60 * time.Second}

// ErrInstanceRunning 想在实例还在跑的时候做一件必须停着才能做的事。
// 单独定义是为了让上层能给出 409 而不是 500 —— 「你现在不该这么做」和
// 「我们这边炸了」是两回事，混成一个状态码，界面就只能显示一句没用的错误。
var ErrInstanceRunning = errors.New("实例正在运行")

type restartState struct {
	fails int
}

// Manager 实例与进程的持有者
type Manager struct {
	mu       sync.Mutex
	baseDir  string
	st       *store.Store[Instance]
	procs    map[string]*Process
	logs     map[string]*LogBuffer
	restarts map[string]*restartState
}

func NewManager(baseDir string) (*Manager, error) {
	// 转绝对路径：默认值是 ./data，存进实例配置里就成了相对路径 ——
	// 之后以别的 cwd 启动（注册成服务、计划任务）就全对不上了。
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	if err := os.MkdirAll(filepath.Join(baseDir, "instances"), 0o755); err != nil {
		return nil, err
	}
	st, err := store.New[Instance](filepath.Join(baseDir, "instances.json"))
	if err != nil {
		return nil, err
	}
	return &Manager{
		baseDir:  baseDir,
		st:       st,
		procs:    map[string]*Process{},
		logs:     map[string]*LogBuffer{},
		restarts: map[string]*restartState{},
	}, nil
}

// Log 往某个实例的控制台写一行。给「守护进程自己做的事」留痕用 ——
// 换服务端文件、写配置这些动作必须出现在日志里，否则用户排不出时间线。
func (m *Manager) Log(id, line string) {
	m.logsFor(id).Append(line)
}

func (m *Manager) logsFor(id string) *LogBuffer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.logs[id]; ok {
		return b
	}
	b := NewLogBuffer(5000)
	// 只给真实存在的实例落盘。日志接口带着乱造的 id 打进来时不该在磁盘上凭空长文件。
	if _, exists := m.st.Get(id); exists {
		b.SetSink(newFileSink(filepath.Join(m.baseDir, "logs"), id+".log"))
	}
	m.logs[id] = b
	return b
}

// proc 取实例的运行进程。分开写是因为这里加锁的路径太多，散着写迟早漏一个。
func (m *Manager) proc(id string) (*Process, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[id]
	return p, ok
}

// Logs 返回该实例的日志缓冲（不存在则创建空的）
func (m *Manager) Logs(id string) *LogBuffer {
	return m.logsFor(id)
}

func (m *Manager) List() []View {
	insts := m.st.All()
	// store 是 map，遍历顺序随机 —— 不排序的话侧栏每次轮询都会重排，用户看到的列表会自己跳。
	// 按创建时间升序：先建的排前面，位置永远稳定。
	sort.Slice(insts, func(i, j int) bool {
		if insts[i].CreatedAt.Equal(insts[j].CreatedAt) {
			return insts[i].ID < insts[j].ID
		}
		return insts[i].CreatedAt.Before(insts[j].CreatedAt)
	})
	out := make([]View, 0, len(insts))
	for _, inst := range insts {
		v := View{Instance: inst}
		if p, ok := m.procs[inst.ID]; ok {
			v.RuntimeState = RuntimeState{
				Status:    p.Status(),
				PID:       p.PID(),
				StartedAt: p.StartedAt(),
				Error:     p.Error(),
			}
		} else {
			v.RuntimeState = RuntimeState{Status: StatusStopped}
		}
		out = append(out, v)
	}
	return out
}

func (m *Manager) Get(id string) (Instance, bool) {
	return m.st.Get(id)
}

// BaseDir 数据目录，设置页要显示「东西存在哪」
func (m *Manager) BaseDir() string {
	return m.baseDir
}

// Create 新建实例，自动分配目录
func (m *Manager) Create(inst *Instance) error {
	if inst.ID == "" {
		inst.ID = fmt.Sprintf("i%d", time.Now().UnixMilli())
	}
	if inst.Dir == "" {
		inst.Dir = filepath.Join(m.baseDir, "instances", inst.ID)
	}
	if err := os.MkdirAll(inst.Dir, 0o755); err != nil {
		return err
	}
	if inst.MinMem <= 0 {
		inst.MinMem = 1024
	}
	if inst.MaxMem <= 0 {
		inst.MaxMem = 4096
	}
	if inst.Port == 0 {
		inst.Port = 25565
	}
	// jarPath 留空的话兜一个默认名 —— 前端向导会填 "server.jar"，
	// 但 API / 导入来的实例可能没有。启动时 buildArgs 会走 resolveJar 兜底，
	// 这里补上是为了让配置页 / 概览页显示的也是真实名字，而不是一片空白。
	if inst.JarPath == "" {
		inst.JarPath = "server.jar"
	}
	inst.CreatedAt = time.Now()
	return m.st.Put(inst.ID, *inst)
}

func (m *Manager) Update(inst *Instance) error {
	old, ok := m.st.Get(inst.ID)
	if !ok {
		return fmt.Errorf("实例不存在: %s", inst.ID)
	}
	inst.CreatedAt = old.CreatedAt
	inst.Dir = old.Dir
	return m.st.Put(inst.ID, *inst)
}

// Delete 删除实例。removeFiles 为真时，连磁盘上的实例目录一起清掉。
//
// 默认保留文件是刻意的：实例目录里躺着 world/ —— 用户最贵、最不可再生的东西。
// 但「删了世界却留一堆目录在磁盘上」也确实反直觉（用户原话：
// 「为什么删除不能选择删除世界文件」），所以给显式选项，默认仍站在保守那边。
//
// 删文件前有一道硬护栏：目标必须是 <数据目录>/instances/ 之下的子孙。
// 实例配置是可以被手改的（也可能被一段假 JSON 塞进任意路径），
// 少这一道，一个写错的 dir 就能把 C:\ 交给 os.RemoveAll。
func (m *Manager) Delete(id string, removeFiles bool) error {
	inst, ok := m.st.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}

	var abs string
	if removeFiles {
		// 先校验、后动手：路径不合法时要在任何副作用之前就退出。
		if strings.TrimSpace(inst.Dir) == "" {
			return fmt.Errorf("这个实例没记录目录，不能删文件")
		}
		var err error
		abs, err = filepath.Abs(inst.Dir)
		if err != nil {
			return fmt.Errorf("实例目录不合法: %w", err)
		}
		root, err := filepath.Abs(filepath.Join(m.baseDir, "instances"))
		if err != nil {
			return fmt.Errorf("数据目录不合法: %w", err)
		}
		rel, err := filepath.Rel(root, abs)
		// rel == "." 说明目标就是 instances 根目录本身，rel 以 .. 开头说明在上面。
		if err != nil || rel == "." || rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("实例目录 %s 不在数据目录的 instances 下，拒绝删除", abs)
		}
	}

	if p, ok := m.procs[id]; ok {
		// 跑着的时候不动文件：存档正被 JVM 按自己的节奏写，删到一半剩下的是残档；
		// 而且删完进程还活着，会继续往一个已不存在的目录里写。
		if removeFiles && (p.Status() == StatusRunning || p.Status() == StatusStarting) {
			return fmt.Errorf("%w，先停掉再删文件", ErrInstanceRunning)
		}
		_ = p.Kill()
		m.mu.Lock()
		delete(m.procs, id)
		m.mu.Unlock()
	}

	// 先摘配置、再删文件。反过来一旦删完文件这一步失败，就会留下一个
	// 指向不存在目录的实例（体检、启动全都会报怪错）；而先摘配置最坏是留个孤儿目录，
	// 用户自己删掉就行 —— 失败退化的方向要对。
	if err := m.st.Delete(id); err != nil {
		return err
	}

	m.mu.Lock()
	delete(m.logs, id)
	delete(m.restarts, id)
	m.mu.Unlock()

	if removeFiles {
		if err := os.RemoveAll(abs); err != nil {
			return fmt.Errorf("配置已删除，但目录没删干净（%s）：%w", abs, err)
		}
	}
	return nil
}

// Start 手动启动。会先跑一遍启动前检查 —— 任一 blocker 都直接返回带报告的 PreflightError，
// 而不是把进程拉起来再让 Java 吐一段英文报错。
func (m *Manager) Start(id string, javaPath string) error {
	// 用户手动点启动 = 他自认为已经改好了，把自动重启的失败计数清零
	m.mu.Lock()
	if st := m.restarts[id]; st != nil {
		st.fails = 0
	}
	m.mu.Unlock()
	return m.start(id, javaPath)
}

func (m *Manager) start(id string, javaPath string) error {
	inst, ok := m.st.Get(id)
	if !ok {
		return fmt.Errorf("实例不存在: %s", id)
	}

	pf := m.Preflight(id)
	if !pf.OK {
		return &PreflightError{Result: pf}
	}

	// Java 的优先级：调用方指定 > 实例配置 > 按 MC 版本自动挑。
	// 以前是直接 "java"，只有 Java 8 的机器上跑 1.21 会在启动瞬间抛
	// UnsupportedClassVersionError —— 用户看到的是 class file version 61.0 这种天书。
	if javaPath == "" {
		javaPath = inst.JavaPath
	}
	if javaPath == "" {
		javaPath = pf.JavaPath
	}
	if javaPath == "" {
		javaPath = "java"
	}

	// logsFor 内部会加 m.mu，必须在持有 m.mu 之前调用，否则自死锁
	logs := m.logsFor(id)

	m.mu.Lock()
	if p, ok := m.procs[id]; ok && (p.Status() == StatusRunning || p.Status() == StatusStarting) {
		m.mu.Unlock()
		return fmt.Errorf("实例已在运行")
	}
	p := newProcess(&inst, logs)
	m.procs[id] = p
	m.mu.Unlock()

	logs.Append(sessionBanner())
	if err := p.Start(javaPath); err != nil {
		m.mu.Lock()
		delete(m.procs, id)
		m.mu.Unlock()
		logs.Append("[ServerBox] 启动失败：" + err.Error())
		return err
	}
	logs.Append(fmt.Sprintf("[ServerBox] 已启动 · java=%s · PID %d", javaPath, p.PID()))
	go m.supervise(id, javaPath, p)
	return nil
}

// supervise 看住一个进程的收尾：正常停了就清零计数，崩了就存档日志、按退避策略重启。
func (m *Manager) supervise(id, javaPath string, p *Process) {
	<-p.Done()

	// 正常停止（用户点了停 / 发过 stop）：失败序列结束
	if p.Status() != StatusCrashed {
		m.mu.Lock()
		if st := m.restarts[id]; st != nil {
			st.fails = 0
		}
		m.mu.Unlock()
		return
	}

	inst, ok := m.st.Get(id)
	if !ok {
		return
	}
	logs := m.logsFor(id)

	// 崩溃现场存档。跑得够久才崩的，说明不是「一起就崩」的配置问题，
	// 之前那几次失败的计数不该继续算在这条链上。
	uptime := 0.0
	if t := p.StartedAt(); t != nil {
		uptime = time.Since(*t).Seconds()
	}

	lines := logs.Snapshot()
	if len(lines) > crashSnapshotLines {
		lines = lines[len(lines)-crashSnapshotLines:]
	}
	if path, err := m.WriteCrashSnapshot(id, lines, p.Error()); err == nil {
		logs.Append(fmt.Sprintf("[ServerBox] 进程异常退出，最后 %d 行已存档：%s", len(lines), path))
	} else {
		logs.Append("[ServerBox] 进程异常退出（崩溃日志写盘失败：" + err.Error() + "）")
	}

	if !inst.AutoRestart {
		logs.Append("[ServerBox] 该实例没开崩溃自动重启。")
		return
	}

	m.mu.Lock()
	if m.restarts[id] == nil {
		m.restarts[id] = &restartState{}
	}
	if uptime >= stableRunSeconds {
		m.restarts[id].fails = 0
	}
	m.restarts[id].fails++
	fails := m.restarts[id].fails
	m.mu.Unlock()

	if fails > maxAutoRestarts {
		logs.Append(fmt.Sprintf(
			"[ServerBox] 已连续崩溃 %d 次，自动重启 %d 次都没起来，就此停下。"+
				"这种情况十有八九是配置问题（EULA 没同意、内存给多了、端口被占、服务端文件不对）——"+
				"改好之后手动点启动。",
			fails, fails-1))
		return
	}

	delay := restartBackoff[(fails-1)%len(restartBackoff)]
	logs.Append(fmt.Sprintf("[ServerBox] %s后自动重启（第 %d/%d 次）", humanDelay(delay), fails, maxAutoRestarts))
	time.Sleep(delay)

	// 睡着的这段时间里，用户可能自己点了启动、或者把实例删了 —— 那就别再插一脚
	if cur, still := m.proc(id); !still || cur != p {
		return
	}
	if _, exists := m.st.Get(id); !exists {
		return
	}
	if err := m.start(id, javaPath); err != nil {
		logs.Append("[ServerBox] 自动重启失败：" + err.Error())
	}
}

func (m *Manager) Stop(id string, timeout time.Duration) error {
	m.mu.Lock()
	p, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return p.Stop(timeout)
}

func (m *Manager) Kill(id string) error {
	m.mu.Lock()
	p, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return p.Kill()
}

func (m *Manager) Send(id, cmd string) error {
	m.mu.Lock()
	p, ok := m.procs[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("实例未运行")
	}
	return p.Send(cmd)
}

// AutoStartAll 守护进程启动时拉起标记了自动启动的实例
func (m *Manager) AutoStartAll() {
	for _, inst := range m.st.All() {
		if inst.AutoStart {
			_ = m.Start(inst.ID, inst.JavaPath)
		}
	}
}
