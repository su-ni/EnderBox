package daemon

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── 时刻解析 ──

func TestParseHHMM(t *testing.T) {
	good := []struct {
		in   string
		h, m int
	}{
		{"04:00", 4, 0},
		{"4:00", 4, 0}, // 用户很自然会这么写
		{"23:59", 23, 59},
		{"00:00", 0, 0},
		{" 9:5 ", 9, 5},
	}
	for _, c := range good {
		h, m, ok := parseHHMM(c.in)
		if !ok || h != c.h || m != c.m {
			t.Errorf("%q 应解析成 %d:%d，实际 ok=%v %d:%d", c.in, c.h, c.m, ok, h, m)
		}
	}
	bad := []string{"", "24:00", "12:60", "12", "abc", "12:34:56", "4:00abc", "-1:00", "12:-1", ":"}
	for _, in := range bad {
		if _, _, ok := parseHHMM(in); ok {
			t.Errorf("%q 不该被接受", in)
		}
	}
}

// ── 策略校验与规整 ──

func TestNormalizeBackupPolicy(t *testing.T) {
	// 规整：去重（"4:00"/"04:00" 是同一时刻）+ 排序 + 补零；范围空值兜底成 all
	p, err := NormalizeBackupPolicy(BackupPolicy{
		Enabled: true,
		AtTimes: []string{"4:00", "04:00", "23:59", "00:30"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(p.AtTimes, ","); got != "00:30,04:00,23:59" {
		t.Errorf("时刻应是 00:30,04:00,23:59，实际 %s", got)
	}
	if p.Scope != BackupScopeAll {
		t.Errorf("范围空值该兜底成 %s，实际 %s", BackupScopeAll, p.Scope)
	}

	// 开了却没设任何触发方式 = 一个永远不跑的任务。必须报错，不能静默失效。
	if _, err := NormalizeBackupPolicy(BackupPolicy{Enabled: true}); err == nil {
		t.Error("开了自动备份但没设触发方式，应该报错")
	}
	// 没开启时允许空策略（用户可能就是来关掉它的）
	if _, err := NormalizeBackupPolicy(BackupPolicy{}); err != nil {
		t.Errorf("没开启时该允许空策略：%v", err)
	}

	bad := []BackupPolicy{
		{EveryHours: -1},
		{EveryHours: 200},
		{Keep: -1},
		{Keep: 1001},
		{Scope: "everything"},
		{AtTimes: []string{"25:00"}},
		{AtTimes: []string{"04:00", "05:00", "06:00", "07:00", "08:00", "09:00", "10:00"}},
	}
	for i, b := range bad {
		if _, err := NormalizeBackupPolicy(b); err == nil {
			t.Errorf("第 %d 个非法策略该被拒：%+v", i, b)
		}
	}
}

// ── 到点判定 ──

func TestBackupDue(t *testing.T) {
	at := func(d, h, mi int) time.Time {
		return time.Date(2026, 10, d, h, mi, 0, 0, time.Local)
	}
	now := at(4, 10, 0)

	cases := []struct {
		name string
		p    BackupPolicy
		want bool
	}{
		{"没开启", BackupPolicy{Enabled: false, AtTimes: []string{"04:00"}}, false},
		{"间隔没到", BackupPolicy{Enabled: true, EveryHours: 6, LastRun: at(4, 8, 0)}, false},
		{"间隔到了", BackupPolicy{Enabled: true, EveryHours: 6, LastRun: at(4, 3, 0)}, true},
		{"从没跑过间隔式：不立刻触发", BackupPolicy{Enabled: true, EveryHours: 6}, false},
		{"今天的时刻还没到", BackupPolicy{Enabled: true, AtTimes: []string{"23:00"}, LastRun: at(3, 23, 0)}, false},
		{"今天这个点过了、从没跑过", BackupPolicy{Enabled: true, AtTimes: []string{"04:00"}}, true},
		{"今天这个点已经跑过", BackupPolicy{Enabled: true, AtTimes: []string{"04:00"}, LastRun: at(4, 4, 0)}, false},
		{"昨天跑过、今天还没跑", BackupPolicy{Enabled: true, AtTimes: []string{"04:00"}, LastRun: at(3, 4, 0)}, true},
		{"间隔没到但固定时刻到了", BackupPolicy{
			Enabled: true, EveryHours: 24, AtTimes: []string{"04:00"}, LastRun: at(4, 3, 0),
		}, true},
		// ── 用户拍板的关键行为：开了之后不该立刻备一份 ──
		{"09:00 开启的策略，不该补跑 04:00 那一刻",
			BackupPolicy{Enabled: true, Since: at(4, 9, 0), AtTimes: []string{"04:00"}}, false},
		{"09:00 开启、间隔 2 小时，10:00 还没到",
			BackupPolicy{Enabled: true, Since: at(4, 9, 0), EveryHours: 2}, false},
		{"09:00 开启、间隔 2 小时，11:00 到了",
			BackupPolicy{Enabled: true, Since: at(4, 9, 0), EveryHours: 2}, false}, // now=10:00，还没到
		{"09:00 开启、间隔 1 小时，10:00 到点",
			BackupPolicy{Enabled: true, Since: at(4, 9, 0), EveryHours: 1}, true},
		{"开了之后跑过一次，下次按 LastRun 算",
			BackupPolicy{Enabled: true, Since: at(4, 9, 0), EveryHours: 1, LastRun: at(4, 9, 30)}, false},
	}
	for _, c := range cases {
		if got := backupDue(c.p, now); got != c.want {
			t.Errorf("%s：期望 %v，实际 %v", c.name, c.want, got)
		}
	}
}

// 11:00 那个用例单独写：now 得跟着挪
func TestBackupDueIntervalAfterEnable(t *testing.T) {
	p := BackupPolicy{Enabled: true, Since: time.Date(2026, 10, 4, 9, 0, 0, 0, time.Local), EveryHours: 2}
	now := time.Date(2026, 10, 4, 11, 0, 0, 0, time.Local)
	if !backupDue(p, now) {
		t.Error("9:00 开启、间隔 2 小时 —— 11:00 该跑")
	}
	earlier := time.Date(2026, 10, 4, 10, 59, 0, 0, time.Local)
	if backupDue(p, earlier) {
		t.Error("10:59 还没到点，不该跑")
	}
}

// ── 下次时间 ──

func TestNextBackupRun(t *testing.T) {
	now := time.Date(2026, 10, 4, 10, 0, 0, 0, time.Local)

	if got := NextBackupRun(BackupPolicy{}, now); got != nil {
		t.Errorf("没启用时应是 nil，实际 %v", got)
	}

	// 只有间隔：从 LastRun 起算
	got := NextBackupRun(BackupPolicy{Enabled: true, EveryHours: 6,
		LastRun: time.Date(2026, 10, 4, 8, 0, 0, 0, time.Local)}, now)
	want := time.Date(2026, 10, 4, 14, 0, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Errorf("间隔式应算出 %v，实际 %v", want, got)
	}

	// 两种方式都有：取更早的那个（间隔 11:00 早于固定时刻 12:00）
	got = NextBackupRun(BackupPolicy{
		Enabled:    true,
		EveryHours: 2,
		AtTimes:    []string{"12:00"},
		LastRun:    time.Date(2026, 10, 4, 9, 0, 0, 0, time.Local),
	}, now)
	want = time.Date(2026, 10, 4, 11, 0, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Errorf("应取更早的 %v，实际 %v", want, got)
	}

	// 只有已过的时刻 → 滚到明天
	got = NextBackupRun(BackupPolicy{Enabled: true, AtTimes: []string{"04:00"}}, now)
	want = time.Date(2026, 10, 5, 4, 0, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Errorf("应滚到明天 04:00，实际 %v", got)
	}
}

// ── 以下走真文件系统（newTestManager 在 delete_test.go 里）──

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func zipEntryNames(t *testing.T, path string) []string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	out := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		out = append(out, strings.TrimSuffix(f.Name, "/"))
	}
	return out
}

func hasEntry(list []string, name string) bool {
	for _, n := range list {
		if n == name || strings.HasPrefix(n, name+"/") {
			return true
		}
	}
	return false
}

// 「只存存档」必须只收世界目录：plugins / server.jar / server.properties 都不该进去；
// logs/ 是任何范围都不收的（硬规则）。文件名改了 level-name 也要跟着走。
func TestBackupScopeWorldsOnly(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// level-name 改成 myworld：目录名不是默认的 world，「只存存档」必须认出来
	writeTestFile(t, filepath.Join(inst.Dir, "server.properties"), "level-name=myworld\n")
	writeTestFile(t, filepath.Join(inst.Dir, "myworld", "level.dat"), "worlddata")
	writeTestFile(t, filepath.Join(inst.Dir, "world_nether", "level.dat"), "netherdata")
	writeTestFile(t, filepath.Join(inst.Dir, "plugins", "EssentialsX.jar"), "plugin")
	writeTestFile(t, filepath.Join(inst.Dir, "server.jar"), "jarbytes")
	writeTestFile(t, filepath.Join(inst.Dir, "eula.txt"), "eula")
	writeTestFile(t, filepath.Join(inst.Dir, "logs", "latest.log"), "logline")

	inst.Backup.Scope = BackupScopeWorlds
	if err := m.st.Put(inst.ID, *inst); err != nil {
		t.Fatal(err)
	}

	b, err := m.CreateBackup(inst.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	names := zipEntryNames(t, filepath.Join(m.backupsDir(inst.ID), b.File))

	if !hasEntry(names, "myworld/level.dat") {
		t.Errorf("level-name 指定的世界目录该在包里：%v", names)
	}
	if !hasEntry(names, "world_nether/level.dat") {
		t.Errorf("老版布局的下界目录也该在（否则还原后少一个维度）：%v", names)
	}
	for _, bad := range []string{"plugins", "server.jar", "eula.txt", "server.properties", "logs"} {
		if hasEntry(names, bad) {
			t.Errorf("「只存存档」不该收 %s：%v", bad, names)
		}
	}
}

// 整目录范围：除了 logs/，其它都要收
func TestBackupScopeAllSkipsLogs(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(inst.Dir, "server.jar"), "jarbytes")
	writeTestFile(t, filepath.Join(inst.Dir, "world", "level.dat"), "worlddata")
	writeTestFile(t, filepath.Join(inst.Dir, "plugins", "A.jar"), "plugin")
	writeTestFile(t, filepath.Join(inst.Dir, "logs", "latest.log"), "logline")

	b, err := m.CreateBackup(inst.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	names := zipEntryNames(t, filepath.Join(m.backupsDir(inst.ID), b.File))
	for _, want := range []string{"server.jar", "world/level.dat", "plugins/A.jar"} {
		if !hasEntry(names, want) {
			t.Errorf("整目录备份该包含 %s：%v", want, names)
		}
	}
	if hasEntry(names, "logs") {
		t.Errorf("logs 不该进备份：%v", names)
	}
}

// 保留策略：只留最近 N 份，删的永远是最旧的
func TestApplyRetentionKeepsNewest(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	dir := m.backupsDir(inst.ID)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	// 显式写 mtime：ListBackups 是按修改时间排的，靠写入顺序会不稳
	for i, n := range []string{"a-1.zip", "b-2.zip", "c-3.zip", "d-4.zip"} {
		p := filepath.Join(dir, n)
		writeTestFile(t, p, "x")
		ts := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := m.applyRetention(inst.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Errorf("该删掉 2 份，实际 %v", removed)
	}
	left, err := m.ListBackups(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("该剩 2 份，实际 %d：%+v", len(left), left)
	}
	for _, bItem := range left {
		if !strings.HasPrefix(bItem.File, "c-") && !strings.HasPrefix(bItem.File, "d-") {
			t.Errorf("留下的必须是最新的两份，却有 %s", bItem.File)
		}
	}
	// keep<=0 = 不限
	if rm, err := m.applyRetention(inst.ID, 0); err != nil || len(rm) != 0 {
		t.Errorf("keep=0 不该删任何东西：removed=%v err=%v", rm, err)
	}
}

// 还原「只存存档」的备份时，绝不能把 server.jar / plugins 一起删掉 ——
// 那是「清空整个实例目录再解压」的老做法造成的真 bug（还原完服务器起不来）。
func TestRestoreWorldsBackupKeepsNonWorldFiles(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(inst.Dir, "server.properties"), "level-name=world\n")
	writeTestFile(t, filepath.Join(inst.Dir, "world", "level.dat"), "v1")
	writeTestFile(t, filepath.Join(inst.Dir, "plugins", "A.jar"), "plugin")
	writeTestFile(t, filepath.Join(inst.Dir, "server.jar"), "jarbytes")

	inst.Backup.Scope = BackupScopeWorlds
	if err := m.st.Put(inst.ID, *inst); err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateBackup(inst.ID, "t")
	if err != nil {
		t.Fatal(err)
	}

	// 备份之后世界变了，而插件和 jar 也「变新」了（不该被还原动到）
	writeTestFile(t, filepath.Join(inst.Dir, "world", "level.dat"), "v2")
	writeTestFile(t, filepath.Join(inst.Dir, "plugins", "NEW.jar"), "newplugin")

	if _, err := m.RestoreBackup(inst.ID, b.File); err != nil {
		t.Fatal(err)
	}

	if got := readTestFile(t, filepath.Join(inst.Dir, "world", "level.dat")); got != "v1" {
		t.Errorf("世界该被还原成 v1，实际 %q", got)
	}
	// server.jar 不在备份里，绝不能被删 —— 这也是这个测试存在的理由
	if got := readTestFile(t, filepath.Join(inst.Dir, "server.jar")); got != "jarbytes" {
		t.Errorf("server.jar 不该被动，实际 %q（空说明被删了）", got)
	}
	if got := readTestFile(t, filepath.Join(inst.Dir, "plugins", "NEW.jar")); got != "newplugin" {
		t.Errorf("插件目录不该被动，实际 %q", got)
	}
}

// 「只存存档」但实例里根本没有世界目录时，必须报错而不是产出空 zip。
// 刚建好还没启动过的实例就是这种状态，给用户一个「备份成功」的空包
// 等于让他以为存档安全了 —— 那正是他最怕的事。
func TestBackupWorldsScopeFailsWhenNoWorld(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// 只有 jar 和配置，没有 world/
	writeTestFile(t, filepath.Join(inst.Dir, "server.jar"), "jarbytes")
	writeTestFile(t, filepath.Join(inst.Dir, "server.properties"), "level-name=world\n")

	inst.Backup.Scope = BackupScopeWorlds
	if err := m.st.Put(inst.ID, *inst); err != nil {
		t.Fatal(err)
	}

	if _, err := m.CreateBackup(inst.ID, "t"); err == nil {
		t.Fatal("没有世界目录时该报错，而不是产出空备份")
	} else if !strings.Contains(err.Error(), "没找到世界存档") {
		t.Errorf("错误信息该说清是没找到世界存档，实际：%v", err)
	}

	// 备份目录里不该留下任何东西
	list, err := m.ListBackups(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("失败时不该留下备份文件，实际 %+v", list)
	}
}

// 整目录范围不受这条限制：没有 world 也能备（jar/配置本身也有价值）
func TestBackupAllScopeWorksWithoutWorld(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(inst.Dir, "server.jar"), "jarbytes")

	if _, err := m.CreateBackup(inst.ID, "t"); err != nil {
		t.Fatalf("整目录范围该能备：%v", err)
	}
}

// closeLogSinks 收尾：关掉测试期间打开的日志文件句柄。
// Windows 上句柄不关，t.TempDir 的清理会直接失败（The process cannot access the file）。
func closeLogSinks(t *testing.T, m *Manager, ids ...string) {
	t.Helper()
	t.Cleanup(func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		for _, id := range ids {
			b, ok := m.logs[id]
			if !ok {
				continue
			}
			if s, ok := b.sink.(*fileSink); ok {
				s.Close()
			}
		}
	})
}

// 调度器真跑一遍：到点 → 产出 auto- 前缀的包 → 写日志 → LastRun 落盘 → 同一时刻不重复跑。
// 这一条盖的是 backupDue 之外的接线（前缀、日志、持久化、防重入），单测纯函数盖不到。
func TestRunDueBackupsCreatesAutoBackup(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "夜班服", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	closeLogSinks(t, m, inst.ID)
	writeTestFile(t, filepath.Join(inst.Dir, "world", "level.dat"), "worlddata")
	writeTestFile(t, filepath.Join(inst.Dir, "server.jar"), "jarbytes")

	// 策略：今天 00:00 这个点，且是昨天开的 → 现在（同一天晚些时候）就该跑
	now := time.Now()
	since := now.AddDate(0, 0, -1)
	inst.Backup = BackupPolicy{
		Enabled: true,
		AtTimes: []string{"00:00"},
		Keep:    0,
		Scope:   BackupScopeAll,
		Since:   since,
	}
	if err := m.st.Put(inst.ID, *inst); err != nil {
		t.Fatal(err)
	}

	m.RunDueBackups(now)

	list, err := m.ListBackups(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("该产出 1 份自动备份，实际 %d：%+v", len(list), list)
	}
	if list[0].Kind != BackupKindAuto {
		t.Errorf("类型该是 %s，实际 %s（文件名 %s）", BackupKindAuto, list[0].Kind, list[0].File)
	}
	if !strings.HasPrefix(list[0].File, autoBackupPrefix+"-") {
		t.Errorf("文件名该以 %s- 开头，实际 %s", autoBackupPrefix, list[0].File)
	}
	if list[0].Size <= 0 {
		t.Error("备份文件是空的")
	}
	// LastRun 必须落盘：不落的话守护进程一重启就会把今天这个点再跑一遍
	saved, _ := m.Get(inst.ID)
	if saved.Backup.LastRun.IsZero() {
		t.Error("LastRun 没落盘")
	}
	// 实例控制台要留痕，否则用户排不出「这份是几点谁备的」
	logs := strings.Join(m.Logs(inst.ID).Snapshot(), "\n")
	if !strings.Contains(logs, "自动备份完成") {
		t.Errorf("控制台里该有一行「自动备份完成」，实际日志：\n%s", logs)
	}

	// 同一时刻再跑一次：不该多出第二份
	m.RunDueBackups(now)
	again, _ := m.ListBackups(inst.ID)
	if len(again) != 1 {
		t.Errorf("同一时刻不该重复备份，实际 %d 份：%+v", len(again), again)
	}
}

// 自动备份也受保留策略约束，且不会把刚产出的那份删掉
func TestRunDueBackupsAppliesRetention(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "t", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	closeLogSinks(t, m, inst.ID)
	writeTestFile(t, filepath.Join(inst.Dir, "world", "level.dat"), "worlddata")
	dir := m.backupsDir(inst.ID)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	for i, n := range []string{"old1-1.zip", "old2-2.zip"} {
		p := filepath.Join(dir, n)
		writeTestFile(t, p, "x")
		ts := base.Add(time.Duration(i) * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now()
	inst.Backup = BackupPolicy{
		Enabled: true,
		AtTimes: []string{"00:00"},
		Keep:    2,
		Scope:   BackupScopeAll,
		Since:   now.AddDate(0, 0, -1),
	}
	if err := m.st.Put(inst.ID, *inst); err != nil {
		t.Fatal(err)
	}
	m.RunDueBackups(now)

	left, err := m.ListBackups(inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 {
		t.Fatalf("keep=2，该剩 2 份，实际 %d：%+v", len(left), left)
	}
	foundAuto := false
	for _, b := range left {
		if b.Kind == BackupKindAuto {
			foundAuto = true
		}
	}
	if !foundAuto {
		t.Error("刚产出的自动备份被误删了 —— 保留策略必须删最旧的")
	}
}

// 策略里的空集合必须是 `[]`，绝不能是 `null`。
//
// 回归的是真机 crash：新建的「10086」实例从没保存过备份策略，Go 的 nil slice
// 序列化成 `"atTimes": null`，前端一个 `.length` 就抛
// `TypeError: Cannot read properties of null (reading 'length')`，
// ErrorBoundary 把整页（连控制台一起）替换成错误界面。
func TestBackupPolicyNeverSerializesNull(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "10086", Type: TypeForge, MCVersion: "1.20.1", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.Create(inst); err != nil {
		t.Fatal(err)
	}
	// 从没保存过策略 —— 这正是崩溃时的状态

	// ① 列表接口（前端拿实例对象的路径）
	raw, err := json.Marshal(m.List())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`"atTimes":null`, `"scope":""`} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("列表接口不该出现 %s，实际：%s", bad, string(raw))
		}
	}

	// ② 策略接口
	v, ok := m.GetBackupPolicy(inst.ID)
	if !ok {
		t.Fatal("取不到策略")
	}
	if v.AtTimes == nil {
		t.Error("AtTimes 是 nil —— 序列化出去就是 null")
	}
	if v.Scope == "" {
		t.Error("Scope 是空串 —— 界面上两个范围标签会一个都不高亮")
	}
	vraw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(vraw), `"atTimes":null`) {
		t.Errorf("策略接口不该出现 atTimes:null，实际：%s", string(vraw))
	}

	// ③ 直接喂零值策略也不许漏
	if got := policyView(BackupPolicy{}); got.AtTimes == nil || got.Scope == "" {
		t.Errorf("零值策略也该被补齐，实际 %+v", got.BackupPolicy)
	}

	// ④ 老数据（磁盘上已经存成 null 的）读出来也要是 []
	stale := &Instance{Name: "old", Type: TypePaper, MCVersion: "1.20.4", JarPath: "server.jar", MaxMem: 1024, Port: 25565}
	if err := m.st.Put(stale.ID, *stale); err == nil {
		if pv, ok := m.GetBackupPolicy(stale.ID); ok && pv.AtTimes == nil {
			t.Error("磁盘上存着 null 的老实例，读出来也该是 []")
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
