package daemon

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Process 一个正在运行（或曾经运行）的服务端进程
type Process struct {
	inst *Instance

	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	status    Status
	startedAt *time.Time
	errMsg    string
	done      chan struct{}
	players   map[string]struct{}

	logs *LogBuffer
	job  jobHandle
}

func newProcess(inst *Instance, logs *LogBuffer) *Process {
	return &Process{
		inst:   inst,
		status: StatusStopped,
		logs:   logs,
		done:   make(chan struct{}),
	}
}

// buildArgs 构造 java 启动参数
// 注意：Windows 中文系统下 java 的 stdout 默认跟随系统 locale（GBK），
// 强制指定 UTF-8 能避免大量乱码。latest.log 文件通道在 M2 接入。
func buildArgs(inst *Instance) []string {
	minMem, maxMem := inst.MinMem, inst.MaxMem
	if minMem <= 0 {
		minMem = 1024
	}
	if maxMem <= 0 {
		maxMem = 4096
	}
	args := []string{
		fmt.Sprintf("-Xms%dM", minMem),
		fmt.Sprintf("-Xmx%dM", maxMem),
		"-Dfile.encoding=UTF-8",
		"-Dstdout.encoding=UTF-8",
		"-Dstderr.encoding=UTF-8",
	}
	if inst.JVMArgs != "" {
		args = append(args, strings.Fields(inst.JVMArgs)...)
	}
	args = append(args, "-jar", inst.JarPath, "nogui")
	return args
}

// Start 拉起进程
func (p *Process) Start(javaPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.status == StatusRunning || p.status == StatusStarting {
		return fmt.Errorf("实例已在运行")
	}

	cmd := exec.Command(javaPath, buildArgs(p.inst)...)
	cmd.Dir = p.inst.Dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	p.cmd = cmd
	p.stdin = stdin
	p.status = StatusRunning
	now := time.Now()
	p.startedAt = &now
	p.errMsg = ""
	p.done = make(chan struct{})

	// 挂到 Job Object，保证崩了也能把整棵进程树带走
	p.job, _ = assignToJob(cmd.Process)

	go p.pump(stdout)
	go p.pump(stderr)

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if p.status != StatusStopping {
			p.status = StatusCrashed
			if err != nil {
				p.errMsg = err.Error()
			}
		} else {
			p.status = StatusStopped
		}
		p.mu.Unlock()
		closeJob(p.job)
		p.job = 0
		close(p.done)
	}()

	return nil
}

func (p *Process) pump(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		p.logs.Append(line)
		p.track(line)
	}
}

// Send 向控制台写入一条命令，并把这条命令回显进日志。
// 不回显的话，用户敲完 list 会盯着屏幕怀疑自己到底按没按回车 ——
// 服务端对很多命令的回应要等好几秒。
func (p *Process) Send(cmd string) error {
	p.mu.Lock()
	w := p.stdin
	p.mu.Unlock()
	if w == nil {
		return fmt.Errorf("进程未运行")
	}
	p.logs.Append("[ServerBox] > " + cmd)
	_, err := io.WriteString(w, cmd+"\n")
	return err
}

// Stop 优雅停止：先发 stop 命令，超时再强杀
func (p *Process) Stop(timeout time.Duration) error {
	p.mu.Lock()
	if p.status != StatusRunning {
		p.mu.Unlock()
		return nil
	}
	p.status = StatusStopping
	p.mu.Unlock()

	_ = p.Send("stop")

	select {
	case <-p.done:
		return nil
	case <-time.After(timeout):
		return p.Kill()
	}
}

// Kill 强杀整棵进程树
func (p *Process) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	terminateJob(p.job)
	_ = p.cmd.Process.Kill()
	p.status = StatusStopped
	return nil
}

func (p *Process) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status
}

func (p *Process) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *Process) StartedAt() *time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.startedAt
}

func (p *Process) Error() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.errMsg
}

func (p *Process) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.done
}
