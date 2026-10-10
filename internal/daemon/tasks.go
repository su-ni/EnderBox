package daemon

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrTaskNotFound 目标任务不存在。单独定义是为了让上层能给出 404 而不是 400 ——
// 「你填的触发条件不对」和「这条任务已经没了」是两回事，
// 混成一个码，界面就没法给出有用的提示（同 ErrInstanceNotFound 的由来）。
var ErrTaskNotFound = errors.New("任务不存在")

// ── 计划任务 ──
//
// 和自动备份共用**同一个**调度循环（见 StartScheduler），但存储是分开的。
//
// ⚠ 为什么任务不挂在 Instance 上（备份策略就挂在上面）：
// `PUT /api/instances/{id}` 是**整体覆盖**。备份策略兜底成空策略是无害的，
// 但任务列表兜底成空 = **静默删光用户配好的定时任务**。前端只要有哪怕一处
// 提交实例时没带上 tasks 字段，用户的配置就没了，而且全程没有任何提示。
// 所以任务走独立的 tasks.json + 独立的四条路由。

// 触发方式。
const (
	TriggerInterval = "interval" // 每隔 N 分钟
	TriggerDaily    = "daily"    // 每天 HH:MM
	TriggerWeekly   = "weekly"   // 每周（可多选）HH:MM
	TriggerCron     = "cron"     // 五段式 CRON
)

// 动作类型。Actions 是**有顺序**的流水线，前一步做完才做下一步。
const (
	ActionCommand = "command" // 往控制台发一条指令
	ActionStart   = "start"
	ActionStop    = "stop"
	ActionRestart = "restart"
	ActionKill    = "kill"
	ActionBackup  = "backup"
	ActionDelay   = "delay" // 等 N 秒再做下一个动作
)

// TaskTrigger 触发条件。四个字段按 Kind 各用其一，其余留空。
type TaskTrigger struct {
	Kind string `json:"kind"`
	// EveryMinutes 间隔式的间隔分钟数（interval）
	EveryMinutes int `json:"everyMinutes,omitempty"`
	// At "HH:MM"，每天/每周的时刻（daily / weekly）
	At string `json:"at,omitempty"`
	// Weekdays 周几触发，0=周日 … 6=周六（weekly）
	Weekdays []int `json:"weekdays,omitempty"`
	// Cron 五段式表达式「分 时 日 月 周」（cron）
	Cron string `json:"cron,omitempty"`
}

// TaskAction 一个动作。
type TaskAction struct {
	Kind string `json:"kind"`
	// Value command=要发的指令；delay=等待秒数；其余动作留空
	Value string `json:"value,omitempty"`
}

// ScheduledTask 一条计划任务。
type ScheduledTask struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Enabled bool         `json:"enabled"`
	Trigger TaskTrigger  `json:"trigger"`
	Actions []TaskAction `json:"actions"`
	// Since 这条任务「当前这段启用期」的起点。与备份策略同一套语义：
	// 开启之前的触发点不补跑（否则「每天 04:00」配好一打开就立刻跑一次），
	// 间隔式也从这一刻开始计时。
	Since time.Time `json:"since"`
	// LastRun 上次真的执行过的时刻 —— **不论成败**（理由见 runOneTask）。
	LastRun time.Time `json:"lastRun"`
	// Count 累计执行次数
	Count int `json:"count"`
	// LastError 上次执行的结果，空 = 成功。让用户不翻日志就知道哪条任务在报错。
	LastError string `json:"lastError,omitempty"`
}

// TaskView 给前端的视图 = 任务 + 后端算好的下次执行时间 + 正在跑标记。
// 「下次什么时候跑」不交给前端重算 —— 同 BackupPolicyView，调度规则只能有一份实现。
type TaskView struct {
	ScheduledTask
	NextRun *time.Time `json:"nextRun,omitempty"`
	Running bool       `json:"running,omitempty"`
}

// taskSet 一个实例的全部任务，存 data/tasks.json。
type taskSet struct {
	ID    string          `json:"id"`
	Tasks []ScheduledTask `json:"tasks"`
}

// NormalizeTask 校验并规整一条任务。
//
// 非法值直接报错，不悄悄纠正 —— 同 NormalizeBackupPolicy：用户填了东西却看到
// 它变成别的值，比看到一句报错更让人不信任。
func NormalizeTask(t ScheduledTask) (ScheduledTask, error) {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return t, fmt.Errorf("任务名不能为空")
	}
	if len([]rune(t.Name)) > 40 {
		return t, fmt.Errorf("任务名最多 40 个字")
	}

	switch t.Trigger.Kind {
	case TriggerInterval:
		if t.Trigger.EveryMinutes < 1 || t.Trigger.EveryMinutes > 10080 {
			return t, fmt.Errorf("间隔要在 1 分钟到 7 天（10080 分钟）之间")
		}
	case TriggerDaily, TriggerWeekly:
		hh, mm, ok := parseHHMM(t.Trigger.At)
		if !ok {
			return t, fmt.Errorf("时刻 %q 格式不对，要写成 04:00 这样", t.Trigger.At)
		}
		t.Trigger.At = fmt.Sprintf("%02d:%02d", hh, mm)
		if t.Trigger.Kind == TriggerWeekly {
			if len(t.Trigger.Weekdays) == 0 {
				return t, fmt.Errorf("「每周」至少要选一天")
			}
			seen := map[int]bool{}
			out := make([]int, 0, len(t.Trigger.Weekdays))
			for _, d := range t.Trigger.Weekdays {
				if d < 0 || d > 6 {
					return t, fmt.Errorf("星期取值要在 0-6 之间（0 = 周日），收到 %d", d)
				}
				if seen[d] {
					continue
				}
				seen[d] = true
				out = append(out, d)
			}
			sort.Ints(out)
			t.Trigger.Weekdays = out
		} else {
			t.Trigger.Weekdays = nil
		}
	case TriggerCron:
		if _, err := parseCron(t.Trigger.Cron); err != nil {
			return t, err
		}
	default:
		return t, fmt.Errorf("不认识的触发方式：%q", t.Trigger.Kind)
	}

	if len(t.Actions) == 0 {
		return t, fmt.Errorf("至少要有一个动作")
	}
	if len(t.Actions) > 10 {
		return t, fmt.Errorf("一条任务最多 10 个动作")
	}
	for i := range t.Actions {
		a := &t.Actions[i]
		a.Value = strings.TrimSpace(a.Value)
		switch a.Kind {
		case ActionCommand:
			if a.Value == "" {
				return t, fmt.Errorf("第 %d 个动作是「发指令」，但指令是空的", i+1)
			}
			// 换行会被当成多行命令灌进 stdin，等于替用户多发了几条命令
			if strings.ContainsAny(a.Value, "\r\n") {
				return t, fmt.Errorf("第 %d 个动作的指令里不能带换行", i+1)
			}
		case ActionDelay:
			sec, err := strconv.Atoi(a.Value)
			if err != nil || sec < 1 || sec > 3600 {
				return t, fmt.Errorf("第 %d 个动作的等待时间要是 1-3600 秒", i+1)
			}
			a.Value = strconv.Itoa(sec)
		case ActionStart, ActionStop, ActionRestart, ActionKill, ActionBackup:
			a.Value = ""
		default:
			return t, fmt.Errorf("第 %d 个动作的类型不认识：%q", i+1, a.Kind)
		}
	}
	return t, nil
}

// nextAfter 返回 after **严格之后**的第一个触发时刻。
//
// 「严格大于」是关键：LastRun 记的正是刚跑过的那个时刻。若允许等于返回，
// 下一轮扫描会认为「又到点了」，同一条任务每 30 秒跑一次 —— 一个自己咬自己尾巴的死循环。
func (tr TaskTrigger) nextAfter(after time.Time) (time.Time, bool) {
	switch tr.Kind {
	case TriggerInterval:
		if tr.EveryMinutes < 1 {
			return time.Time{}, false
		}
		return after.Add(time.Duration(tr.EveryMinutes) * time.Minute), true

	case TriggerDaily, TriggerWeekly:
		hh, mm, ok := parseHHMM(tr.At)
		if !ok {
			return time.Time{}, false
		}
		// 往后找 8 天：每周只选中一天时，从任意时刻起 7 天内必命中。
		day := after
		for i := 0; i < 8; i++ {
			at := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, day.Location())
			if at.After(after) && tr.dayMatches(at) {
				return at, true
			}
			day = day.AddDate(0, 0, 1)
		}
		return time.Time{}, false

	case TriggerCron:
		spec, err := parseCron(tr.Cron)
		if err != nil {
			return time.Time{}, false
		}
		return spec.nextAfter(after)
	}
	return time.Time{}, false
}

// dayMatches daily 恒真；weekly 看星期几。
func (tr TaskTrigger) dayMatches(at time.Time) bool {
	if tr.Kind != TriggerWeekly {
		return true
	}
	w := int(at.Weekday()) // 0 = 周日，与 time.Weekday 对齐
	for _, d := range tr.Weekdays {
		if d == w {
			return true
		}
	}
	return false
}

// taskDue 这一轮该不该跑这条任务。纯函数，便于单测（规格表见 tasks_test.go）。
//
// 三条规则，前两条与 backupDue 同源：
//   - 起点取 max(LastRun, Since)，所以刚建好的任务不会因为 LastRun 是零值就被当成「早就该跑了」；
//   - 没有起点就不跑（正常路径下 SaveTask 一定写 Since，这只是兜底）；
//   - **固定时刻类（每天 / 每周 / CRON）只看今天，错过更早的不补跑。**
//
// 最后这条是刻意的，也是最容易写错的一条：晚上关机、第二天早上打开面板时，
// 「每天 23:00 备份」不该补跑一次；「每晚 04:00 重启」更不该在上午突然把服务器重启 ——
// 用户会觉得它自己发疯了。间隔式则相反，它本来就该追赶：每 6 小时的任务
// 错过 8 小时，跑一次是对的，跳过才是错的。
func taskDue(t ScheduledTask, now time.Time) bool {
	if !t.Enabled {
		return false
	}
	base := t.LastRun
	if base.IsZero() {
		base = t.Since
	}
	if base.IsZero() {
		return false
	}

	if t.Trigger.Kind == TriggerInterval {
		return now.Sub(base) >= time.Duration(t.Trigger.EveryMinutes)*time.Minute
	}

	// 固定时刻类：把起点抬到今天 0 点，更早的触发点就不再被算进来。
	// ⚠ 抬到「0 点前一瞬」而不是「0 点」：nextAfter 是**严格大于**，
	// 用 0 点当起点会把「今天 00:00」这个触发点本身排除掉 ——
	// 于是每天 00:00 的任务永远不会触发。差这一纳秒，是个只在午夜现形的 bug。
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	from := base
	if from.Before(today) {
		from = today.Add(-time.Nanosecond)
	}
	next, ok := t.Trigger.nextAfter(from)
	return ok && !next.After(now)
}

// NextTaskRun 界面上显示的「下次执行」。没启用或触发条件残缺时返回 nil。
func NextTaskRun(t ScheduledTask, now time.Time) *time.Time {
	if !t.Enabled {
		return nil
	}
	next, ok := t.Trigger.nextAfter(now)
	if !ok {
		return nil
	}
	return &next
}

// ── 精简 CRON ──
//
// 五段式「分 时 日 月 周」。每段支持 *、*/n、a、a-b、a-b/n，以及逗号列表。
//
// 一处刻意的简化：日与周**同时被限定**时取「与」（标准 cron 取「或」）。
// 「每月 1 号且是周一」这种读法更符合直觉，也更好跟用户解释。
// 只要有一段是 *，行为与标准实现完全一致。
type cronSpec struct {
	minute map[int]bool
	hour   map[int]bool
	dom    map[int]bool
	month  map[int]bool
	dow    map[int]bool
	domAny bool
	dowAny bool
}

func parseCron(expr string) (cronSpec, error) {
	var s cronSpec
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return s, fmt.Errorf("CRON 要 5 段（分 时 日 月 周），现在写了 %d 段", len(fields))
	}
	var err error
	if s.minute, err = cronField(fields[0], 0, 59, "分钟"); err != nil {
		return s, err
	}
	if s.hour, err = cronField(fields[1], 0, 23, "小时"); err != nil {
		return s, err
	}
	if s.dom, err = cronField(fields[2], 1, 31, "日"); err != nil {
		return s, err
	}
	if s.month, err = cronField(fields[3], 1, 12, "月"); err != nil {
		return s, err
	}
	// 周的 7 也是周日（标准 cron 允许），归一成 0
	if s.dow, err = cronField(fields[4], 0, 7, "周"); err != nil {
		return s, err
	}
	if s.dow[7] {
		s.dow[0] = true
		delete(s.dow, 7)
	}
	s.domAny = strings.TrimSpace(fields[2]) == "*"
	s.dowAny = strings.TrimSpace(fields[4]) == "*"
	return s, nil
}

// cronField 解析一段。
func cronField(field string, min, max int, label string) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("%s段里有空的取值", label)
		}
		step := 1
		body := part
		if i := strings.Index(part, "/"); i >= 0 {
			body = strings.TrimSpace(part[:i])
			n, err := strconv.Atoi(strings.TrimSpace(part[i+1:]))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("%s段的步长 %q 不是正整数", label, part[i+1:])
			}
			step = n
		}
		lo, hi := min, max
		if body != "*" {
			if i := strings.Index(body, "-"); i >= 0 {
				a, err1 := strconv.Atoi(strings.TrimSpace(body[:i]))
				b, err2 := strconv.Atoi(strings.TrimSpace(body[i+1:]))
				if err1 != nil || err2 != nil {
					return nil, fmt.Errorf("%s段的区间 %q 看不懂", label, body)
				}
				lo, hi = a, b
			} else {
				a, err := strconv.Atoi(body)
				if err != nil {
					return nil, fmt.Errorf("%s段的取值 %q 看不懂", label, body)
				}
				lo, hi = a, a
				if step > 1 {
					// "5/10" 这种写法：从 5 起、每 10 一个，一直到该段上限
					hi = max
				}
			}
		}
		if lo < min || hi > max || lo > hi {
			return nil, fmt.Errorf("%s段的取值超出范围（%d-%d）：%q", label, min, max, part)
		}
		for v := lo; v <= hi; v += step {
			out[v] = true
		}
	}
	return out, nil
}

func (s cronSpec) match(t time.Time) bool {
	if !s.minute[t.Minute()] || !s.hour[t.Hour()] || !s.month[int(t.Month())] {
		return false
	}
	domOK := s.dom[t.Day()]
	dowOK := s.dow[int(t.Weekday())]
	if s.domAny {
		return dowOK
	}
	if s.dowAny {
		return domOK
	}
	return domOK && dowOK
}

func (s cronSpec) nextAfter(after time.Time) (time.Time, bool) {
	t := after.Truncate(time.Minute).Add(time.Minute)
	// 扫满一年就放弃：像「2 月 31 日」这种组合永远不成立，
	// 不能让界面卡在这儿、也不能让调度器空转。
	limit := t.AddDate(1, 0, 0)
	for t.Before(limit) {
		// 小时或月份不对就整点跳 —— 省掉 59 次注定失败的分钟判断。
		// 「0 0 30 2 *」这类永不成立的式子靠这个把扫描量压到秒级。
		if !s.hour[t.Hour()] || !s.month[int(t.Month())] {
			t = t.Truncate(time.Hour).Add(time.Hour)
			continue
		}
		if s.match(t) {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

// ── 存储 ──

// loadTasks 取某实例的任务。**永远不返回 nil** ——
// nil slice 走到 JSON 里是 null，前端的 length 会直接炸掉整页（这个坑踩过）。
func (m *Manager) loadTasks(instID string) []ScheduledTask {
	set, ok := m.tasks.Get(instID)
	if !ok || set.Tasks == nil {
		return []ScheduledTask{}
	}
	return set.Tasks
}

func (m *Manager) saveTasks(instID string, list []ScheduledTask) error {
	if list == nil {
		list = []ScheduledTask{}
	}
	return m.tasks.Put(instID, taskSet{ID: instID, Tasks: list})
}

func (m *Manager) taskRunning(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasksRunning[taskID]
}

// ListTasks 返回某实例的全部任务（带后端算好的下次执行时间）。
func (m *Manager) ListTasks(instID string) ([]TaskView, error) {
	if _, ok := m.st.Get(instID); !ok {
		return nil, fmt.Errorf("%w: %s", ErrInstanceNotFound, instID)
	}
	list := m.loadTasks(instID)
	now := time.Now()
	out := make([]TaskView, 0, len(list))
	for _, t := range list {
		out = append(out, TaskView{
			ScheduledTask: t,
			NextRun:       NextTaskRun(t, now),
			Running:       m.taskRunning(t.ID),
		})
	}
	return out, nil
}

// newTaskID 生成一个在 list 内**唯一**的任务 ID。
//
// ⚠ 别退回「只用毫秒时间戳」。同一毫秒内连建两条（界面快速连点「新建」、批量导入）
// 会拿到两个一模一样的 ID，而 DeleteTask / SaveTask 都是按 ID 匹配的 ——
// 结果是**删一条删掉两条、改一条改掉两条**。平时靠毫秒差躲过去，
// 所以这种 bug 不会在真机上立刻显形，只会偶发（测试要 `-count=120` 才稳定复现）。
//
// 撞上了就往后补序号（`t<ms>` → `t<ms>-2`）。**不用随机数**：
// 随机后缀会让「同毫秒建的两条」在不同进程里排序不可复现，测试没法写死断言。
func newTaskID(list []ScheduledTask, now time.Time) string {
	base := fmt.Sprintf("t%d", now.UnixMilli())
	id := base
	for n := 2; ; n++ {
		taken := false
		for i := range list {
			if list[i].ID == id {
				taken = true
				break
			}
		}
		if !taken {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, n)
	}
}

// SaveTask 新建（taskID 为空）或更新一条任务。
func (m *Manager) SaveTask(instID, taskID string, in ScheduledTask) (TaskView, error) {
	if _, ok := m.st.Get(instID); !ok {
		return TaskView{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, instID)
	}
	in, err := NormalizeTask(in)
	if err != nil {
		return TaskView{}, err
	}
	list := m.loadTasks(instID)
	now := time.Now()

	if taskID == "" {
		in.ID = newTaskID(list, now)
		in.Since = now
		in.LastRun = time.Time{}
		in.Count = 0
		in.LastError = ""
		list = append(list, in)
	} else {
		idx := -1
		for i := range list {
			if list[i].ID == taskID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return TaskView{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
		}
		old := list[idx]
		// LastRun / Count / LastError / Since 一律以服务端存着的为准 ——
		// 前端提交的是表单，不该由它决定「上次什么时候跑过、跑了几次」。
		in.ID = old.ID
		in.LastRun = old.LastRun
		in.Count = old.Count
		in.LastError = old.LastError
		in.Since = old.Since
		// 「从关到开」时重置起点。用户只是先把参数设好、想让它按规矩来，
		// 不是要它立刻补跑一遍开启之前的时刻。与 SaveBackupPolicy 同一套语义。
		if !old.Enabled && in.Enabled {
			in.Since = now
		}
		list[idx] = in
	}

	if err := m.saveTasks(instID, list); err != nil {
		return TaskView{}, err
	}
	return TaskView{ScheduledTask: in, NextRun: NextTaskRun(in, now), Running: m.taskRunning(in.ID)}, nil
}

// SetTaskEnabled 只改启用状态。
//
// 单独一条而不是让它走「整条任务读-改-写」：开关是最频繁的操作，
// 若它要提交整份表单，用户在一个别处已经改过的旧表单上点一下开关，
// 就会顺手把那条任务回退成旧值 —— 而他以为自己只动了开关。
func (m *Manager) SetTaskEnabled(instID, taskID string, enabled bool) (TaskView, error) {
	list := m.loadTasks(instID)
	idx := -1
	for i := range list {
		if list[i].ID == taskID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return TaskView{}, fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	now := time.Now()
	t := list[idx]
	if !t.Enabled && enabled {
		t.Since = now
	}
	t.Enabled = enabled
	list[idx] = t
	if err := m.saveTasks(instID, list); err != nil {
		return TaskView{}, err
	}
	return TaskView{ScheduledTask: t, NextRun: NextTaskRun(t, now), Running: m.taskRunning(t.ID)}, nil
}

// DeleteTask 删掉一条任务。删不掉时报错，而不是静默当成功 ——
// 界面上点了删除却什么都没发生，比一句「任务不存在」更让人困惑。
func (m *Manager) DeleteTask(instID, taskID string) error {
	list := m.loadTasks(instID)
	out := make([]ScheduledTask, 0, len(list))
	found := false
	for _, t := range list {
		if t.ID == taskID {
			found = true
			continue
		}
		out = append(out, t)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, taskID)
	}
	return m.saveTasks(instID, out)
}

// ── 调度与执行 ──

// forEachDueTask 遍历所有实例中「这一轮到点」的任务，逐个交给 fn。
//
// 实例先按 ID 排序再遍历：store.All() 的顺序是随机的，排一下让行为可复现
// （测试里尤其重要，否则「谁先跑」会随 map 顺序变）。
func (m *Manager) forEachDueTask(now time.Time, fn func(instID string, t ScheduledTask)) {
	insts := m.st.All()
	sort.Slice(insts, func(i, j int) bool { return insts[i].ID < insts[j].ID })
	for _, inst := range insts {
		for _, t := range m.loadTasks(inst.ID) {
			if taskDue(t, now) {
				fn(inst.ID, t)
			}
		}
	}
}

// RunDueTasks 同步跑一遍到点的任务。抽出来给测试直接调用，不用等真实的 30 秒循环。
func (m *Manager) RunDueTasks(now time.Time) {
	m.forEachDueTask(now, func(instID string, t ScheduledTask) {
		m.runOneTask(instID, t, now)
	})
}

// dispatchDueTasks 生产路径：到点就丢进独立 goroutine。
//
// 为什么必须异步：一条任务里可能有 delay（最长 1 小时）或备份一个大世界（几分钟），
// 同步跑会把整个调度循环卡住，后面的任务全部迟到。防重入交给 tasksRunning。
func (m *Manager) dispatchDueTasks(now time.Time) {
	m.forEachDueTask(now, func(instID string, t ScheduledTask) {
		go m.runOneTask(instID, t, now)
	})
}

// runOneTask 执行一条任务的动作流水线，并把结果记下来。
//
// ⚠ LastRun 与备份**相反**：不论成败都记。
// 备份失败不记 LastRun 是为了「今天还来得及重试」；而任务失败若也不记，
// 下一轮 30 秒后就会再撞一次 —— 一个永远起不来的实例配上「每小时重启」，
// 会退化成每 30 秒拉一次 JVM 的死循环。失败原因进 LastError，用户在界面上看得见。
func (m *Manager) runOneTask(instID string, t ScheduledTask, now time.Time) {
	// 同一条任务不并发跑：上一轮还没结束（比如正在清点一个大 world），
	// 下一轮不该再进来一次。
	m.mu.Lock()
	if m.tasksRunning[t.ID] {
		m.mu.Unlock()
		return
	}
	m.tasksRunning[t.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.tasksRunning, t.ID)
		m.mu.Unlock()
	}()

	logs := m.logsFor(instID)
	logs.Append(fmt.Sprintf("[EnderBox] 计划任务「%s」开始执行", t.Name))

	var firstErr error
	for i, a := range t.Actions {
		if err := m.runTaskAction(instID, a); err != nil {
			firstErr = err
			logs.Append(fmt.Sprintf("[EnderBox] 计划任务「%s」第 %d 步（%s）失败：%s",
				t.Name, i+1, actionLabel(a.Kind), err.Error()))
			// 前一步没成，后面的前提就不成立了 —— 尤其「重启」失败后还继续
			// 执行后面的「强制结束」会很危险。停下来，并说清楚剩几步没做。
			if rest := len(t.Actions) - 1 - i; rest > 0 {
				logs.Append(fmt.Sprintf("[EnderBox] 计划任务「%s」已中止，后面 %d 步没有执行",
					t.Name, rest))
			}
			break
		}
	}
	if firstErr == nil {
		logs.Append(fmt.Sprintf("[EnderBox] 计划任务「%s」执行完成", t.Name))
	}
	m.recordTaskRun(instID, t.ID, now, firstErr)
}

// runTaskAction 执行单个动作。
func (m *Manager) runTaskAction(instID string, a TaskAction) error {
	switch a.Kind {
	case ActionCommand:
		return m.Send(instID, a.Value)
	case ActionStart:
		return m.Start(instID, "")
	case ActionStop:
		return m.Stop(instID, 90*time.Second)
	case ActionRestart:
		// Stop 是**阻塞**的（内部等到进程真的退出、或超时后强杀）。
		// 停完再等两秒让端口彻底释放 —— 不等的话新进程会撞
		// 「Address already in use」，而那条报错用户要翻很久才看得懂。
		if err := m.Stop(instID, 90*time.Second); err != nil {
			return fmt.Errorf("停服失败，已取消这次重启：%w", err)
		}
		time.Sleep(2 * time.Second)
		return m.Start(instID, "")
	case ActionKill:
		return m.Kill(instID)
	case ActionBackup:
		_, err := m.CreateBackup(instID, "计划任务")
		return err
	case ActionDelay:
		sec, err := strconv.Atoi(a.Value)
		if err != nil || sec <= 0 {
			return fmt.Errorf("等待时长 %q 不合法", a.Value)
		}
		time.Sleep(time.Duration(sec) * time.Second)
		return nil
	}
	return fmt.Errorf("不认识的动作：%s", a.Kind)
}

// recordTaskRun 落盘执行结果。任务已被删掉时静默跳过（用户删了它，不该再写回去）。
func (m *Manager) recordTaskRun(instID, taskID string, at time.Time, runErr error) {
	list := m.loadTasks(instID)
	for i := range list {
		if list[i].ID != taskID {
			continue
		}
		list[i].LastRun = at
		list[i].Count++
		if runErr != nil {
			list[i].LastError = runErr.Error()
		} else {
			list[i].LastError = ""
		}
		_ = m.saveTasks(instID, list)
		return
	}
}

// actionLabel 日志里给人看的动作名（界面上的中文名由前端自己维护一份）。
func actionLabel(kind string) string {
	switch kind {
	case ActionCommand:
		return "发指令"
	case ActionStart:
		return "启动"
	case ActionStop:
		return "停止"
	case ActionRestart:
		return "重启"
	case ActionKill:
		return "强制结束"
	case ActionBackup:
		return "备份"
	case ActionDelay:
		return "等待"
	}
	return kind
}

// tasksFile 任务库的位置。放在 data/ 根下，和 instances.json 平级。
func tasksFile(baseDir string) string {
	return filepath.Join(baseDir, "tasks.json")
}
