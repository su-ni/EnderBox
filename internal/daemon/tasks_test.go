package daemon

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 固定基准时刻。用 time.Local 构造，避免测试受运行机器时区影响。
// 2026-01-01 是**周四**（Weekday()=4），下面所有星期相关的断言都依赖这一点。
func baseTime() time.Time {
	return time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)
}

// ── 任务校验与规整 ──

func TestNormalizeTask(t *testing.T) {
	// 规整：「4:00」补零成 04:00；星期去重并排序；动作的 Value 去掉首尾空白
	task, err := NormalizeTask(ScheduledTask{
		Name:    "  每晚重启  ",
		Trigger: TaskTrigger{Kind: TriggerWeekly, At: "4:00", Weekdays: []int{5, 1, 1, 3}},
		Actions: []TaskAction{
			{Kind: ActionCommand, Value: "  say 重启 五分钟后  "},
			{Kind: ActionDelay, Value: " 300 "},
			{Kind: ActionRestart},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Name != "每晚重启" {
		t.Errorf("任务名该去掉首尾空白，实际 %q", task.Name)
	}
	if task.Trigger.At != "04:00" {
		t.Errorf("时刻该补零成 04:00，实际 %q", task.Trigger.At)
	}
	if got := joinInts(task.Trigger.Weekdays); got != "1,3,5" {
		t.Errorf("星期该去重排序成 1,3,5，实际 %s", got)
	}
	if task.Actions[0].Value != "say 重启 五分钟后" {
		t.Errorf("指令该去掉首尾空白，实际 %q", task.Actions[0].Value)
	}
	if task.Actions[1].Value != "300" {
		t.Errorf("等待秒数该归一成 300，实际 %q", task.Actions[1].Value)
	}
}

func TestNormalizeTaskRejectsBadInput(t *testing.T) {
	cmd := []TaskAction{{Kind: ActionCommand, Value: "say hi"}}
	good := TaskTrigger{Kind: TriggerDaily, At: "04:00"}

	bad := []struct {
		why  string
		task ScheduledTask
	}{
		{"任务名为空", ScheduledTask{Name: "  ", Trigger: good, Actions: cmd}},
		{"任务名过长", ScheduledTask{Name: strings.Repeat("字", 41), Trigger: good, Actions: cmd}},
		{"没有动作", ScheduledTask{Name: "x", Trigger: good, Actions: nil}},
		{"触发方式不认识", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: "whenever"}, Actions: cmd}},
		{"间隔为 0", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 0}, Actions: cmd}},
		{"间隔超过 7 天", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 10081}, Actions: cmd}},
		{"时刻非法", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerDaily, At: "25:00"}, Actions: cmd}},
		{"每周没选天", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerWeekly, At: "04:00"}, Actions: cmd}},
		{"星期越界", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerWeekly, At: "04:00", Weekdays: []int{7}}, Actions: cmd}},
		{"CRON 段数不对", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerCron, Cron: "0 4 *"}, Actions: cmd}},
		{"CRON 字段越界", ScheduledTask{Name: "x", Trigger: TaskTrigger{Kind: TriggerCron, Cron: "0 25 * * *"}, Actions: cmd}},
		{"发指令但指令为空", ScheduledTask{Name: "x", Trigger: good, Actions: []TaskAction{{Kind: ActionCommand}}}},
		{"指令带换行", ScheduledTask{Name: "x", Trigger: good, Actions: []TaskAction{{Kind: ActionCommand, Value: "say a\nstop"}}}},
		{"等待秒数为 0", ScheduledTask{Name: "x", Trigger: good, Actions: []TaskAction{{Kind: ActionDelay, Value: "0"}}}},
		{"等待秒数超上限", ScheduledTask{Name: "x", Trigger: good, Actions: []TaskAction{{Kind: ActionDelay, Value: "3601"}}}},
		{"等待秒数不是数字", ScheduledTask{Name: "x", Trigger: good, Actions: []TaskAction{{Kind: ActionDelay, Value: "一会儿"}}}},
		{"动作类型不认识", ScheduledTask{Name: "x", Trigger: good, Actions: []TaskAction{{Kind: "explode"}}}},
	}
	for _, c := range bad {
		if _, err := NormalizeTask(c.task); err == nil {
			t.Errorf("「%s」该被拒，却通过了", c.why)
		}
	}
}

// ── 触发时刻计算 ──

func TestTriggerNextAfter(t *testing.T) {
	base := baseTime() // 2026-01-01 10:00 周四

	cases := []struct {
		why    string
		tr     TaskTrigger
		after  time.Time
		want   time.Time
		wantOK bool
	}{
		{
			"间隔式从起点往后加",
			TaskTrigger{Kind: TriggerInterval, EveryMinutes: 30},
			base,
			base.Add(30 * time.Minute), true,
		},
		{
			"每天：今天的点还没到就用今天",
			TaskTrigger{Kind: TriggerDaily, At: "18:00"},
			base,
			time.Date(2026, 1, 1, 18, 0, 0, 0, time.Local), true,
		},
		{
			"每天：今天的点已过就顺延到明天",
			TaskTrigger{Kind: TriggerDaily, At: "04:00"},
			base,
			time.Date(2026, 1, 2, 4, 0, 0, 0, time.Local), true,
		},
		{
			"每周：周四配周一，顺延到下周一",
			TaskTrigger{Kind: TriggerWeekly, At: "04:00", Weekdays: []int{1}},
			base,
			time.Date(2026, 1, 5, 4, 0, 0, 0, time.Local), true,
		},
		{
			"每周：周四配周四，今天已过就下周四",
			TaskTrigger{Kind: TriggerWeekly, At: "08:00", Weekdays: []int{4}},
			base,
			time.Date(2026, 1, 8, 8, 0, 0, 0, time.Local), true,
		},
		{
			"CRON：每 15 分钟",
			TaskTrigger{Kind: TriggerCron, Cron: "*/15 * * * *"},
			time.Date(2026, 1, 1, 10, 7, 0, 0, time.Local),
			time.Date(2026, 1, 1, 10, 15, 0, 0, time.Local), true,
		},
	}
	for _, c := range cases {
		got, ok := c.tr.nextAfter(c.after)
		if ok != c.wantOK {
			t.Errorf("%s：ok 应为 %v，实际 %v", c.why, c.wantOK, ok)
			continue
		}
		if ok && !got.Equal(c.want) {
			t.Errorf("%s：应为 %s，实际 %s", c.why, c.want.Format(time.RFC3339), got.Format(time.RFC3339))
		}
	}
}

// ── CRON 解析 ──

func TestParseCron(t *testing.T) {
	// 周的 7 也是周日，要归一成 0
	spec, err := parseCron("0 4 * * 7")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.dow[0] || spec.dow[7] {
		t.Error("周字段的 7 该被归一成 0")
	}
	if !spec.domAny {
		t.Error("日段写的是 *，该被识别成「不限」")
	}
	if spec.dowAny {
		t.Error("周段写的是 7，不该被当成「不限」")
	}
	// 真正两段都是 * 的式子
	all, err := parseCron("0 4 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if !all.domAny || !all.dowAny {
		t.Error("日/周两段都是 * 时，都该被识别成「不限」")
	}

	// 列表 + 区间 + 步长混用
	spec, err = parseCron("0,30 9-17/2 * 1,6-8 1-5")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []int{0, 30} {
		if !spec.minute[m] {
			t.Errorf("分钟该包含 %d", m)
		}
	}
	for _, h := range []int{9, 11, 13, 15, 17} {
		if !spec.hour[h] {
			t.Errorf("小时该包含 %d", h)
		}
	}
	if spec.hour[10] {
		t.Error("9-17/2 不该包含 10")
	}
	for _, mo := range []int{1, 6, 7, 8} {
		if !spec.month[mo] {
			t.Errorf("月份该包含 %d", mo)
		}
	}

	bad := []string{"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "a * * * *", "*/0 * * * *", "1- * * * *", "* * * * 1,,2"}
	for _, in := range bad {
		if _, err := parseCron(in); err == nil {
			t.Errorf("CRON %q 该被拒", in)
		}
	}
}

func TestCronNextAfter(t *testing.T) {
	cases := []struct {
		why   string
		expr  string
		after time.Time
		want  time.Time
	}{
		{
			"每天 04:00",
			"0 4 * * *",
			baseTime(),
			time.Date(2026, 1, 2, 4, 0, 0, 0, time.Local),
		},
		{
			"每月 1 号 00:00",
			"0 0 1 * *",
			time.Date(2026, 1, 15, 0, 0, 0, 0, time.Local),
			time.Date(2026, 2, 1, 0, 0, 0, 0, time.Local),
		},
		{
			"工作日 12:00（基准日是周四）",
			"0 12 * * 1-5",
			baseTime(),
			time.Date(2026, 1, 1, 12, 0, 0, 0, time.Local),
		},
		{
			"周日 03:30（下一个周日是 1 月 4 日）",
			"30 3 * * 0",
			baseTime(),
			time.Date(2026, 1, 4, 3, 30, 0, 0, time.Local),
		},
	}
	for _, c := range cases {
		spec, err := parseCron(c.expr)
		if err != nil {
			t.Fatalf("%s：解析 %q 失败：%v", c.why, c.expr, err)
		}
		got, ok := spec.nextAfter(c.after)
		if !ok {
			t.Errorf("%s：应算出下次时间，却放弃了", c.why)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%s：应为 %s，实际 %s", c.why, c.want.Format(time.RFC3339), got.Format(time.RFC3339))
		}
	}
}

// 永不成立的组合不能把调度器拖死：扫满一年要放弃，且不能慢到卡住循环。
func TestCronGivesUpOnImpossibleDate(t *testing.T) {
	spec, err := parseCron("0 0 30 2 *") // 2 月没有 30 号
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, ok := spec.nextAfter(baseTime())
	elapsed := time.Since(start)
	if ok {
		t.Error("2 月 30 日不该算出结果")
	}
	if elapsed > 3*time.Second {
		t.Errorf("放弃得该快（整点跳跃优化失效了？），实际花了 %v", elapsed)
	}
}

// ── taskDue：规格表 ──

func TestTaskDue(t *testing.T) {
	base := baseTime() // 2026-01-01 10:00 周四

	cases := []struct {
		why  string
		task ScheduledTask
		now  time.Time
		want bool
	}{
		{
			"没启用不跑",
			ScheduledTask{Enabled: false, Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 1}, Since: base.Add(-time.Hour)},
			base, false,
		},
		{
			"间隔还没到",
			ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 60}, Since: base.Add(-30 * time.Minute)},
			base, false,
		},
		{
			"间隔已到",
			ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 60}, Since: base.Add(-90 * time.Minute)},
			base, true,
		},
		{
			"间隔式从 LastRun 起算，不再看 Since",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 60},
				Since:   base.Add(-72 * time.Hour), // 很久以前开的
				LastRun: base.Add(-10 * time.Minute),
			},
			base, false, // 上次刚跑过 10 分钟，还没到 60 分钟
		},
		{
			"每日时刻已过且今天没跑过",
			ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"}, Since: base.Add(-48 * time.Hour)},
			base, true,
		},
		{
			"每日时刻已过但今天跑过了",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"},
				Since:   base.Add(-48 * time.Hour),
				LastRun: time.Date(2026, 1, 1, 4, 0, 0, 0, time.Local),
			},
			base, false,
		},
		{
			"每日时刻还没到",
			ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerDaily, At: "23:00"}, Since: base.Add(-48 * time.Hour)},
			base, false,
		},
		{
			"启用之前走过的那个点不补跑",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"},
				Since:   base.Add(-time.Hour), // 今天 09:00 才开的
			},
			base, false, // 今天 04:00 在开启之前
		},
		{
			"每周：今天正是选中的那天",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerWeekly, At: "04:00", Weekdays: []int{4}}, // 基准日是周四
				Since:   base.Add(-72 * time.Hour),
			},
			base, true,
		},
		{
			"每周：今天不是选中的那天，不跑（也不补跑上周那次）",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerWeekly, At: "04:00", Weekdays: []int{1}}, // 只选周一
				Since:   base.Add(-72 * time.Hour),
			},
			base, false,
		},
		{
			// 关机一天再开机：昨天 23:00 那次该不该补？
			// 不该 —— 早上十点突然跑一次「夜间备份」，用户会觉得莫名其妙。
			"错过的每日时刻不补跑",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerDaily, At: "23:00"},
				Since:   time.Date(2025, 12, 25, 10, 0, 0, 0, time.Local),
				LastRun: time.Date(2025, 12, 30, 23, 0, 0, 0, time.Local), // 12/31 那次错过了
			},
			time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local), false,
		},
		{
			// nextAfter 是「严格大于」，而固定时刻的起点今天 0 点 ——
			// 若起点取今天 0 点本身，「00:00」这个触发点会被排除，每天 00:00 的任务永不触发。
			"午夜 00:00 的任务刚过 0 点该触发",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerDaily, At: "00:00"},
				Since:   time.Date(2025, 12, 30, 10, 0, 0, 0, time.Local),
			},
			time.Date(2026, 1, 1, 0, 0, 30, 0, time.Local), true,
		},
		{
			"午夜 00:00 的任务当天已经跑过就不再触发",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerDaily, At: "00:00"},
				Since:   time.Date(2025, 12, 30, 10, 0, 0, 0, time.Local),
				LastRun: time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local),
			},
			time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local), false,
		},
		{
			"CRON 已到点",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerCron, Cron: "*/15 * * * *"},
				Since:   time.Date(2026, 1, 1, 9, 0, 0, 0, time.Local),
			},
			base, true,
		},
		{
			"CRON 还没到点",
			ScheduledTask{
				Enabled: true,
				Trigger: TaskTrigger{Kind: TriggerCron, Cron: "*/15 * * * *"},
				Since:   base.Add(-2 * time.Minute), // 09:58 开的，下一个点是 10:00
				LastRun: time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local),
			},
			base, false,
		},
		{
			"没有起点（LastRun 与 Since 都是零值）不跑",
			ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 1}},
			base, false,
		},
	}
	for _, c := range cases {
		if got := taskDue(c.task, c.now); got != c.want {
			t.Errorf("%s：该 %v，实际 %v", c.why, c.want, got)
		}
	}
}

// 这是整套逻辑里最容易出错、后果最难查的一处。
//
// LastRun 记的正是「刚跑过的那个时刻」。若 nextAfter 允许返回等于起点的时刻，
// 下一轮扫描就会判成「又到点了」—— 30 秒一轮的调度器会把同一条任务
// 每 30 秒跑一次，日志疯狂刷屏、JVM 反复重启，而用户完全看不出为什么。
func TestTaskDueDoesNotReFireImmediately(t *testing.T) {
	at := time.Date(2026, 1, 1, 4, 0, 0, 0, time.Local)
	task := ScheduledTask{
		Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"},
		Since:   at.Add(-24 * time.Hour),
		LastRun: at,
	}
	if taskDue(task, at) {
		t.Error("刚跑完的那一刻不该再判成到点 —— 会退化成每 30 秒跑一次的死循环")
	}
	if taskDue(task, at.Add(time.Minute)) {
		t.Error("同一天内跑过之后，后续每一分钟都不该再判成到点")
	}
	if !taskDue(task, at.Add(24*time.Hour)) {
		t.Error("第二天 04:00 应该到点")
	}

	// 间隔式同理：LastRun 就是起点，必须严格等到下一个间隔
	iv := ScheduledTask{
		Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 30},
		Since:   at.Add(-24 * time.Hour),
		LastRun: at,
	}
	if taskDue(iv, at) {
		t.Error("间隔式在刚跑完的那一刻不该立刻再跑")
	}
	if !taskDue(iv, at.Add(30*time.Minute)) {
		t.Error("满 30 分钟后该跑")
	}
}

func TestNextTaskRun(t *testing.T) {
	base := baseTime()

	// 没启用时不给「下次时间」—— 界面上一行「下次：xxx」出现在已关闭的任务上会让人以为它还会跑
	if got := NextTaskRun(ScheduledTask{Enabled: false, Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"}}, base); got != nil {
		t.Errorf("没启用的任务不该有下次时间，实际 %v", got)
	}

	task := ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerDaily, At: "18:00"}}
	got := NextTaskRun(task, base)
	want := time.Date(2026, 1, 1, 18, 0, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Errorf("下次时间该是 %s，实际 %v", want.Format(time.RFC3339), got)
	}

	// 残缺的触发条件要返回 nil，而不是零值时间（零值序列化出去是 0001-01-01，界面上极难看）
	broken := ScheduledTask{Enabled: true, Trigger: TaskTrigger{Kind: TriggerCron, Cron: "不是表达式"}}
	if got := NextTaskRun(broken, base); got != nil {
		t.Errorf("触发条件坏了该返回 nil，实际 %v", got)
	}
}

// ── 存储与接口 ──

// 和备份策略栽过的同一个坑：nil slice 序列化成 null，前端 .length 直接打崩整页。
func TestTasksNeverSerializeNull(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")

	list, err := m.ListTasks("i1")
	if err != nil {
		t.Fatal(err)
	}
	if list == nil {
		t.Error("ListTasks 返回了 nil slice —— 序列化后会变成 null")
	}

	// 存一次空列表，磁盘上也不许出现 null
	if err := m.saveTasks("i1", nil); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tasksFile(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"tasks": null`) {
		t.Errorf("任务列表序列化成了 null：\n%s", raw)
	}
}

func TestSaveTaskKeepsServerSideState(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")

	v, err := m.SaveTask("i1", "", ScheduledTask{
		Name:    "每晚重启",
		Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"},
		Actions: []TaskAction{{Kind: ActionRestart}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.ID == "" {
		t.Fatal("新建的任务该有 ID")
	}
	if v.Since.IsZero() {
		t.Error("新建并启用的任务该记下 Since")
	}
	if v.NextRun == nil {
		t.Error("启用的任务该带回「下次执行时间」")
	}

	// 模拟「跑过一次」，然后前端拿着一份**旧表单**来保存（里面没有 LastRun/Count）
	ranAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	m.recordTaskRun("i1", v.ID, ranAt, nil)

	updated, err := m.SaveTask("i1", v.ID, ScheduledTask{
		Name:    "每晚重启（改过名）",
		Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerDaily, At: "05:00"},
		Actions: []TaskAction{{Kind: ActionRestart}},
		// 故意带上假的服务端状态，模拟前端瞎传
		LastRun: time.Now(),
		Count:   999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.LastRun.Equal(ranAt) {
		t.Errorf("LastRun 该以服务端存的为准（%s），实际 %s",
			ranAt.Format(time.RFC3339), updated.LastRun.Format(time.RFC3339))
	}
	if updated.Count != 1 {
		t.Errorf("Count 该以服务端存的为准（1），实际 %d", updated.Count)
	}
	if updated.Name != "每晚重启（改过名）" || updated.Trigger.At != "05:00" {
		t.Error("任务名与触发条件该被更新")
	}
}

func TestSetTaskEnabledResetsSince(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")

	// 先建一条**关闭**的任务，Since 留空
	v, err := m.SaveTask("i1", "", ScheduledTask{
		Name:    "备份",
		Enabled: false,
		Trigger: TaskTrigger{Kind: TriggerDaily, At: "04:00"},
		Actions: []TaskAction{{Kind: ActionBackup}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.NextRun != nil {
		t.Error("关闭的任务不该有下次时间")
	}

	// 打开它：Since 要重置成现在，否则「今天 04:00」会被当成还没跑过而立刻触发一次
	on, err := m.SetTaskEnabled("i1", v.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if on.Since.IsZero() || time.Since(on.Since) > time.Minute {
		t.Errorf("从关到开该把 Since 重置成现在，实际 %s", on.Since.Format(time.RFC3339))
	}
	if on.NextRun == nil {
		t.Error("打开后该有下次时间")
	}

	// 再关掉：不该再给下次时间
	off, err := m.SetTaskEnabled("i1", v.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if off.NextRun != nil {
		t.Error("关掉后不该再有下次时间")
	}
}

func TestTaskCRUDAndNotFound(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")

	// 实例不存在 → 该报 ErrInstanceNotFound（上层据此给 404）
	if _, err := m.ListTasks("nope"); err == nil {
		t.Error("不存在的实例该报错")
	}

	a, err := m.SaveTask("i1", "", ScheduledTask{
		Name: "A", Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 10},
		Actions: []TaskAction{{Kind: ActionBackup}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveTask("i1", "", ScheduledTask{
		Name: "B", Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 20},
		Actions: []TaskAction{{Kind: ActionBackup}},
	}); err != nil {
		t.Fatal(err)
	}

	list, err := m.ListTasks("i1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("该有 2 条任务，实际 %d", len(list))
	}
	if list[0].Name != "A" || list[1].Name != "B" {
		t.Error("任务的存储顺序该保持稳定")
	}

	if err := m.DeleteTask("i1", a.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ = m.ListTasks("i1"); len(list) != 1 || list[0].Name != "B" {
		t.Error("删掉 A 之后该只剩 B")
	}

	// 删不存在的任务要报错，不能静默当成功
	if err := m.DeleteTask("i1", "t-nonexistent"); err == nil {
		t.Error("删不存在的任务该报错")
	}
}

// 同一毫秒内连建多条任务，**ID 必须互不相同**。
//
// 触发场景是真的：界面快速连点两次「新建任务」，或用导入/复制批量灌任务。
// 旧代码拿 `t<毫秒时间戳>` 当 ID，两个请求落在同一毫秒就撞成同一个 ——
// 而 DeleteTask / SaveTask 都按 ID 匹配，于是**删一条删掉两条、改一条改掉两条**。
// 平时靠毫秒差蒙混过关，只在偶发时显形（`TestTaskCRUDAndNotFound` 就是因此间歇性红）。
//
// 这里故意用**紧凑循环**造出「同一毫秒」的窗口，而不是靠运气等它自己撞。
func TestTaskIDsDoNotCollideWithinSameMillisecond(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	closeLogSinks(t, m, "i1")

	const n = 40
	seen := make(map[string]int, n)
	for i := 0; i < n; i++ {
		v, err := m.SaveTask("i1", "", ScheduledTask{
			Name:    "任务 " + strconv.Itoa(i),
			Enabled: true,
			Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 10},
			Actions: []TaskAction{{Kind: ActionBackup}},
		})
		if err != nil {
			t.Fatalf("建第 %d 条失败: %v", i, err)
		}
		if prev, dup := seen[v.ID]; dup {
			t.Fatalf("第 %d 条和第 %d 条拿到同一个 ID %q —— 删/改其中一条会连带误伤另一条",
				i, prev, v.ID)
		}
		seen[v.ID] = i
	}

	all, err := m.ListTasks("i1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != n {
		t.Fatalf("该有 %d 条任务，实际 %d", n, len(all))
	}

	// 删中间一条：必须只少一条，其余原样留着
	victim := all[n/2].ID
	survivor := all[0].ID
	if victim == survivor {
		t.Fatal("样本挑重了，测试本身有问题")
	}
	if err := m.DeleteTask("i1", victim); err != nil {
		t.Fatal(err)
	}
	after, err := m.ListTasks("i1")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != n-1 {
		t.Errorf("删掉一条该只剩 %d 条，实际 %d 条 —— 撞车的条目被一起删了", n-1, len(after))
	}
	for _, v := range after {
		if v.ID == victim {
			t.Errorf("被删的 %q 还在列表里", victim)
		}
	}
	kept := false
	for _, v := range after {
		if v.ID == survivor {
			kept = true
		}
	}
	if !kept {
		t.Errorf("没撞车的 %q 被误删了", survivor)
	}
}

// ── 执行与记账 ──

// 失败也要记 LastRun —— 与备份相反。这里用一个「指令发给没在跑的实例」来制造失败：
// 若失败不记账，30 秒后调度器会再撞一次，永远起不来的实例会变成每 30 秒拉一次的死循环。
func TestRunTaskRecordsFailureAndDoesNotRetry(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")

	v, err := m.SaveTask("i1", "", ScheduledTask{
		Name:    "发广播",
		Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 60},
		Actions: []TaskAction{{Kind: ActionCommand, Value: "say hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 把「现在」往后推 2 小时，让间隔 60 分钟的任务真的到点 ——
	// SaveTask 会把 Since 设成此刻，不推的话它永远差一点点才到点。
	now := time.Now().Add(2 * time.Hour)
	m.RunDueTasks(now) // 实例没在跑 → 发指令失败

	list, err := m.ListTasks("i1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("任务该还在，实际 %d 条", len(list))
	}
	got := list[0]
	if got.ID != v.ID {
		t.Errorf("任务 ID 该保持不变，实际 %s", got.ID)
	}
	if got.LastRun.IsZero() {
		t.Error("执行失败也必须记 LastRun，否则 30 秒后会被重复触发")
	}
	if got.Count != 1 {
		t.Errorf("执行次数该是 1，实际 %d", got.Count)
	}
	if got.LastError == "" {
		t.Error("失败原因该记进 LastError（界面靠它告诉用户哪条任务在报错）")
	}

	// 紧接着再跑一轮：因为 LastRun 已经更新，不该再触发
	m.RunDueTasks(time.Now())
	list, _ = m.ListTasks("i1")
	if list[0].Count != 1 {
		t.Errorf("同一间隔内不该重复执行，实际次数 %d", list[0].Count)
	}
}

// 前一步失败就中止，后面的动作不许再执行 —— 尤其「重启失败后还继续强制结束」会很危险。
func TestRunTaskStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")

	// 第一步必然失败（实例没在跑）；第二步是「等待 1 秒」——真跑到了会拖慢测试，
	// 所以顺带用它证明「没继续往下走」。
	v, err := m.SaveTask("i1", "", ScheduledTask{
		Name:    "先广播再等",
		Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 60},
		Actions: []TaskAction{
			{Kind: ActionCommand, Value: "say hi"},
			{Kind: ActionDelay, Value: "1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	m.RunDueTasks(time.Now().Add(2 * time.Hour))
	if elapsed := time.Since(start); elapsed > 900*time.Millisecond {
		t.Errorf("第一步失败后不该继续执行「等待」（说明没中止），实际耗时 %v", elapsed)
	}

	list, _ := m.ListTasks("i1")
	if list[0].LastError == "" {
		t.Error("该记下失败原因")
	}
	if list[0].ID != v.ID {
		t.Error("任务 ID 不该变")
	}
}

func TestTaskRunningFlagReleased(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	inst := &Instance{ID: "i1", Name: "测试服"}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// Windows 上不关日志句柄，t.TempDir 的清理会失败（文件被占用）
	closeLogSinks(t, m, "i1")
	v, err := m.SaveTask("i1", "", ScheduledTask{
		Name: "备份", Enabled: true,
		Trigger: TaskTrigger{Kind: TriggerInterval, EveryMinutes: 60},
		Actions: []TaskAction{{Kind: ActionBackup}},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.RunDueTasks(time.Now().Add(2 * time.Hour))

	list, _ := m.ListTasks("i1")
	if len(list) == 0 || list[0].ID != v.ID {
		t.Fatal("任务该还在")
	}
	if list[0].Running {
		t.Error("执行结束后 Running 必须归位，否则界面上那条任务会一直显示「执行中」")
	}
}

// joinInts 断言用的整数列表拼接
func joinInts(xs []int) string {
	parts := make([]string, 0, len(xs))
	for _, x := range xs {
		parts = append(parts, strconv.Itoa(x))
	}
	return strings.Join(parts, ",")
}
