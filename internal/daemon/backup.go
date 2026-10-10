package daemon

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BackupInfo 一份备份文件
type BackupInfo struct {
	File string    `json:"file"`
	Kind string    `json:"kind"` // manual | auto | pre-restore
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

// 备份种类。列表接口只拿得到文件名，所以「自动」必须靠一个固定前缀认出来 ——
// 这也是为什么自动备份的备注位不能被用户填的备注占用。
const (
	BackupKindManual     = "manual"
	BackupKindAuto       = "auto"
	BackupKindPreRestore = "pre-restore"
)

// autoBackupPrefix 自动备份的文件名前缀
const autoBackupPrefix = "auto"

// 备份里不收录的目录：日志又大又没价值，收进去只会让备份体积翻好几倍
var skipDirs = map[string]bool{"logs": true}

func (m *Manager) backupsDir(id string) string {
	return filepath.Join(m.baseDir, "backups", id)
}

func (m *Manager) ListBackups(id string) ([]BackupInfo, error) {
	dir := m.backupsDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]BackupInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		kind := BackupKindManual
		switch {
		case strings.HasPrefix(e.Name(), BackupKindPreRestore+"-"):
			kind = BackupKindPreRestore
		case strings.HasPrefix(e.Name(), autoBackupPrefix+"-"):
			kind = BackupKindAuto
		}
		out = append(out, BackupInfo{
			File: e.Name(),
			Kind: kind,
			Size: info.Size(),
			Time: info.ModTime(),
		})
	}
	// 新的排前面：找「最近那份」是最高频操作，保留策略的「删最旧的」也依赖这个顺序
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out, nil
}

// sanitizeLabel 把用户填的备注变成能当文件名的东西
func sanitizeLabel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	repl := strings.NewReplacer(" ", "_", "/", "-", `\`, "-", ":", "-", "*", "-", "?", "-", `"`, "", "<", "", ">", "", "|", "-")
	s = repl.Replace(s)
	r := []rune(s)
	if len(r) > 24 {
		s = string(r[:24])
	}
	return s
}

// zipDir 把 src 目录递归打包进 zw。rel 用来记录相对路径，保证解压后层级不变。
//
// keepTop 只在顶层生效，决定某个顶层条目要不要收（文件和目录都算）——
// 「只存存档」靠它把 plugins / config / server.jar / server.properties 全挡在外面。
// 传 nil 表示全收（logs 永远不收，那是硬规则）。
func zipDir(zw *zip.Writer, src, rel string, keepTop func(name string) bool) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if rel == "" {
			if skipDirs[name] {
				continue
			}
			// 顶层文件同样要过这道闸：不过的话「只存存档」会把 50 MB 的 server.jar
			// 一起打进去，等于范围白设。
			if keepTop != nil && !keepTop(name) {
				continue
			}
		}
		if e.IsDir() {
			if err := zipDir(zw, filepath.Join(src, name), filepath.Join(rel, name), keepTop); err != nil {
				return err
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		p := filepath.Join(rel, name)
		hdr, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(p)
		hdr.Method = zip.Deflate
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		f, err := os.Open(filepath.Join(src, name))
		if err != nil {
			return err
		}
		_, err = io.Copy(w, f)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// backupScopeOf 取实例配的备份范围，兜底成「整个目录」
func backupScopeOf(inst Instance) string {
	if inst.Backup.Scope == BackupScopeWorlds {
		return BackupScopeWorlds
	}
	return BackupScopeAll
}

// levelName 问实例的世界目录叫什么。
// server.properties 里改过 level-name 的话目录名就不是默认的 world ——
// 「只存存档」必须跟着它走，否则会备出一个空的 zip（而界面说备份成功了）。
func (m *Manager) levelName(id string) string {
	rows, err := m.ReadProperties(id)
	if err != nil {
		return "world"
	}
	for _, r := range rows {
		if r.Key == "level-name" {
			if v := strings.TrimSpace(r.Value); v != "" {
				return v
			}
		}
	}
	return "world"
}

// worldScopeFilter 「只存存档」时，顶层收哪些目录。
func (m *Manager) worldScopeFilter(id string) func(string) bool {
	level := m.levelName(id)
	return func(name string) bool {
		if strings.EqualFold(name, level) {
			return true
		}
		// 老版本把下界/末地放在世界的同级目录（world_nether / world_the_end），
		// 新版才挪进 world 里面。两种都得收，否则还原后少了两个维度。
		return strings.HasPrefix(strings.ToLower(name), "world")
	}
}

// hasWorldDir 实例目录里有没有哪怕一个符合范围的世界目录
func (m *Manager) hasWorldDir(inst Instance, keepTop func(string) bool) bool {
	entries, err := os.ReadDir(inst.Dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && keepTop(e.Name()) {
			return true
		}
	}
	return false
}

// CreateBackup 手动备份一份。范围用实例自己配的（页面上可切换）。
// 存完之后按策略清一次旧备份 —— 用户的心智模型是「总量最多 N 份」。
func (m *Manager) CreateBackup(id, label string) (BackupInfo, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return BackupInfo{}, fmt.Errorf("实例不存在: %s", id)
	}
	b, err := m.createBackup(inst, label, BackupKindManual, backupScopeOf(inst))
	if err != nil {
		return BackupInfo{}, err
	}
	_, _ = m.applyRetention(id, inst.Backup.Keep)
	return b, nil
}

// createBackup 真正干活的那个。kind 决定文件名前缀，scope 决定打包范围。
func (m *Manager) createBackup(inst Instance, label, kind, scope string) (BackupInfo, error) {
	if err := os.MkdirAll(m.backupsDir(inst.ID), 0o755); err != nil {
		return BackupInfo{}, err
	}

	prefix := sanitizeLabel(label)
	if prefix == "" {
		prefix = sanitizeLabel(inst.Name)
	}
	if prefix == "" {
		prefix = "backup"
	}
	// 自动备份必须用固定前缀：列表接口就是靠它把这类认出来的
	if kind == BackupKindAuto {
		prefix = autoBackupPrefix
	}

	var keepTop func(string) bool
	if scope == BackupScopeWorlds {
		keepTop = m.worldScopeFilter(inst.ID)
		// 一个世界目录都没有时直接报错，别产出一个空 zip 还说「备份成功」。
		// 这不是理论情况：刚建好、还没启动过的实例就是只有 jar + properties，
		// 用户点「只存存档」会拿到一个 22 字节的包，还原之后什么都没了 ——
		// 而那正是他最怕的事（存档没了）。
		if !m.hasWorldDir(inst, keepTop) {
			return BackupInfo{}, fmt.Errorf(
				"%w（%s*）—— 它可能还没启动过、没生成过世界。"+
					"这种情况请把范围改成「整个实例目录」再备份",
				ErrNoWorldArchive, m.levelName(inst.ID))
		}
	}

	// 运行中：先关自动存档 → 强制刷盘 → 等它落盘 → 再打包 → 恢复自动存档。
	// 不刷盘会拿到半个区块，这是 MC 备份最经典的坑。
	// 用 m.proc（持锁）而不是直接读 m.procs —— 后者和 start/Delete 是数据竞争。
	if p, ok := m.proc(inst.ID); ok && p.Status() == StatusRunning {
		_ = p.Send("save-off")
		_ = p.Send("save-all flush")
		time.Sleep(3 * time.Second)
		defer func() { _ = p.Send("save-on") }()
	}

	name := fmt.Sprintf("%s-%s.zip", prefix, time.Now().Format("20060102-150405"))
	dest := filepath.Join(m.backupsDir(inst.ID), name)

	f, err := os.Create(dest)
	if err != nil {
		return BackupInfo{}, err
	}
	zw := zip.NewWriter(f)
	err = zipDir(zw, inst.Dir, "", keepTop)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(dest)
		return BackupInfo{}, err
	}

	info, err := os.Stat(dest)
	if err != nil {
		return BackupInfo{}, err
	}
	return BackupInfo{File: name, Kind: kind, Size: info.Size(), Time: info.ModTime()}, nil
}

// applyRetention 只留最近 keep 份，超出的删最旧的。keep<=0 = 不限。
//
// 手动、自动、「还原前」都算在内 —— 用户选的就是「总量 N 份」这个模型。
// 因为删的永远是最旧的，所以刚存的那份不会有事。
func (m *Manager) applyRetention(id string, keep int) ([]string, error) {
	if keep <= 0 {
		return nil, nil
	}
	list, err := m.ListBackups(id)
	if err != nil {
		return nil, err
	}
	if len(list) <= keep {
		return nil, nil
	}
	var removed []string
	for _, b := range list[keep:] {
		if err := m.DeleteBackup(id, b.File); err != nil {
			return removed, err
		}
		removed = append(removed, b.File)
	}
	return removed, nil
}

func (m *Manager) DeleteBackup(id, file string) error {
	base := filepath.Base(file)
	if base != file || strings.ContainsAny(base, `/\`) || !strings.HasSuffix(strings.ToLower(base), ".zip") {
		return fmt.Errorf("非法备份文件名: %s", file)
	}
	return os.Remove(filepath.Join(m.backupsDir(id), base))
}

// RestoreBackup 还原：停服 → 先给当前状态存一份 pre-restore → 清空实例目录 → 解压覆盖 → 之前在跑就重新开
func (m *Manager) RestoreBackup(id, file string) (string, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return "", fmt.Errorf("实例不存在: %s", id)
	}
	base := filepath.Base(file)
	if base != file || strings.ContainsAny(base, `/\`) || !strings.HasSuffix(strings.ToLower(base), ".zip") {
		return "", fmt.Errorf("非法备份文件名: %s", file)
	}
	src := filepath.Join(m.backupsDir(id), base)
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("备份文件不存在: %s", base)
	}

	wasRunning := false
	if p, ok := m.procs[id]; ok && p.Status() == StatusRunning {
		wasRunning = true
		_ = m.Stop(id, 60*time.Second)
	}

	// 安全网：把「还原前」的现场单独存一份，后悔了还能回来。
	// ⚠ 这份**固定用整目录范围**，不跟用户的「只存存档」走 ——
	// 它就是用来撤销这次还原的，只存存档的话插件/配置回不去，等于没有后悔药。
	safety, err := m.createBackup(inst, BackupKindPreRestore, BackupKindPreRestore, BackupScopeAll)
	if err != nil {
		return "", fmt.Errorf("还原前备份失败，已中止: %w", err)
	}

	zr, err := zip.OpenReader(src)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	// 打开备份成功之后再动实例目录。顺序很重要：先清后开的话，一个坏 zip
	// 会让世界被清掉却什么都没还原回来。
	//
	// 只清「这份备份确实要覆盖」的那几个顶层条目，而不是把整个实例目录清空 ——
	// 备份有「只存存档」这种范围，清空整目录会把 server.jar / plugins 一起删掉，
	// 还原完服务器直接起不来。整目录备份的顶层集合就是全部，行为与「清空」等价；
	// 唯一的差别是 logs/ 不在任何备份里，所以会被保留（这是好事：排障线索不丢）。
	tops := map[string]bool{}
	for _, f := range zr.File {
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if clean == "." || clean == "" || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		if i := strings.IndexRune(clean, filepath.Separator); i > 0 {
			clean = clean[:i]
		}
		tops[clean] = true
	}
	for name := range tops {
		if err := os.RemoveAll(filepath.Join(inst.Dir, name)); err != nil {
			return "", err
		}
	}

	for _, f := range zr.File {
		// 二次防线：zip 里的路径也可能带 ..，解压前必须校验
		clean := filepath.Clean(filepath.FromSlash(f.Name))
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			continue
		}
		target := filepath.Join(inst.Dir, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		out, err := os.Create(target)
		if err != nil {
			return "", err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return "", err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return "", err
		}
	}

	if wasRunning {
		if err := m.Start(id, inst.JavaPath); err != nil {
			return safety.File, fmt.Errorf("已还原，但重新启动失败: %w", err)
		}
	}
	return safety.File, nil
}

// ── 自动备份：策略、下次时间、调度 ──
//
// 调度规则只有这一份实现（NextBackupRun / backupDue），界面显示的「下次」也是它算的。
// 让前端再算一遍就是第二个口径，早晚对不上 —— 这个项目刚因为「两处各判一次稳定」栽过。

// parseHHMM 解析 "HH:MM"，接受 "4:00" 这种没补零的写法（用户很自然会这么写）。
// 用 Atoi 逐段解析而不是 Sscanf：后者会把 "4:00abc" 也当成功，这种脏输入该被挡掉。
func parseHHMM(s string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 2 {
		return 0, 0, false
	}
	hh, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	mm, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	if hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, 0, false
	}
	return hh, mm, true
}

// NormalizeBackupPolicy 校验并规整策略。
// 非法值直接报错，不悄悄纠正 —— 用户填了 "25:00" 却看到它变成别的时间，比报错更让人不信任。
func NormalizeBackupPolicy(p BackupPolicy) (BackupPolicy, error) {
	if p.EveryHours < 0 || p.EveryHours > 168 {
		return p, fmt.Errorf("间隔要在 0-168 小时之间")
	}
	if p.Keep < 0 || p.Keep > 1000 {
		return p, fmt.Errorf("保留份数要在 0-1000 之间（0 = 不限）")
	}
	if p.Scope == "" {
		p.Scope = BackupScopeAll
	}
	if p.Scope != BackupScopeAll && p.Scope != BackupScopeWorlds {
		return p, fmt.Errorf("不认识的备份范围：%s", p.Scope)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(p.AtTimes))
	for _, t := range p.AtTimes {
		hh, mm, ok := parseHHMM(t)
		if !ok {
			return p, fmt.Errorf("时刻 %q 格式不对，要写成 04:00 这样", t)
		}
		k := fmt.Sprintf("%02d:%02d", hh, mm)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(out) > 6 {
		return p, fmt.Errorf("每天的时刻最多 6 个")
	}
	sort.Strings(out)
	p.AtTimes = out
	// 开了却没设任何触发方式 = 一个永远不跑的任务。与其静默失效，不如当场说清楚。
	if p.Enabled && len(out) == 0 && p.EveryHours == 0 {
		return p, fmt.Errorf("开了自动备份但没设触发方式 —— 至少填一个每天的时刻，或者一个间隔小时数")
	}
	return p, nil
}

func (p BackupPolicy) nextIntervalRun(now time.Time) (time.Time, bool) {
	if p.EveryHours <= 0 {
		return time.Time{}, false
	}
	base := p.LastRun
	if base.IsZero() {
		base = p.Since
	}
	if base.IsZero() {
		base = now
	}
	return base.Add(time.Duration(p.EveryHours) * time.Hour), true
}

// NextBackupRun 下一次该跑的时间。没启用或没有任何触发方式时返回 nil。
func NextBackupRun(p BackupPolicy, now time.Time) *time.Time {
	if !p.Enabled {
		return nil
	}
	var best *time.Time
	keep := func(t time.Time) {
		if best == nil || t.Before(*best) {
			tt := t
			best = &tt
		}
	}
	if t, ok := p.nextIntervalRun(now); ok {
		keep(t)
	}
	for _, s := range p.AtTimes {
		hh, mm, ok := parseHHMM(s)
		if !ok {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if !at.After(now) {
			at = at.AddDate(0, 0, 1) // 今天这个点已经过了，算明天
		}
		keep(at)
	}
	return best
}

// backupDue 这一轮该不该给这个实例跑自动备份。纯函数，便于单测。
//
// 两条硬规则（都是「别在用户刚打开时就备一份」）：
//   - 固定时刻：只认「Since 之后、且今天已经走到的」那些时刻；
//   - 间隔式：从 max(LastRun, Since) 起算，所以刚打开时不会因为
//     LastRun 为零值而被当成「已经过了很久」。
func backupDue(p BackupPolicy, now time.Time) bool {
	if !p.Enabled {
		return false
	}
	if p.EveryHours > 0 {
		base := p.LastRun
		if base.IsZero() {
			base = p.Since
		}
		if !base.IsZero() && now.Sub(base) >= time.Duration(p.EveryHours)*time.Hour {
			return true
		}
	}
	for _, s := range p.AtTimes {
		hh, mm, ok := parseHHMM(s)
		if !ok {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, now.Location())
		if now.Before(at) {
			continue // 今天这个点还没到
		}
		if !p.Since.IsZero() && at.Before(p.Since) {
			continue // 这个点是「启用之前」的，不该补跑
		}
		if !p.LastRun.Before(at) {
			continue // 这个点已经跑过了
		}
		return true
	}
	return false
}

// GetBackupPolicy 取策略 + 算好的下次时间
func (m *Manager) GetBackupPolicy(id string) (BackupPolicyView, bool) {
	inst, ok := m.st.Get(id)
	if !ok {
		return BackupPolicyView{}, false
	}
	return policyView(inst.Backup), true
}

// fillPolicyDefaults 补上策略里「空 = 未设置」的那些字段。
//
// 为什么必须专门做这件事：`AtTimes` 是 slice，Go 的 nil slice 序列化成 JSON **null**
// 而不是 []；`Scope` 的空串在界面上也没有对应的高亮项。两者都会让界面出问题
// （null.length 直接把整页干掉，实测过）。**任何把 Instance 发出去的地方都要先过这里**，
// 而不是指望每个消费者自己写 `?? []` —— 那种约定迟早会在某个新组件里漏掉。
func fillPolicyDefaults(p BackupPolicy) BackupPolicy {
	if p.Scope == "" {
		p.Scope = BackupScopeAll
	}
	if p.AtTimes == nil {
		p.AtTimes = []string{}
	}
	return p
}

// policyView 把策略包成视图
func policyView(p BackupPolicy) BackupPolicyView {
	p = fillPolicyDefaults(p)
	return BackupPolicyView{
		BackupPolicy: p,
		NextRun:      NextBackupRun(p, time.Now()),
	}
}

// SaveBackupPolicy 保存策略。
//
// LastRun 永远以服务端存着的为准（前端提交的是表单，不该由它决定「上次什么时候跑过」）；
// Since 只在「从关到开」这一下重置成现在 —— 新开启的策略不补跑开启之前的固定时刻，
// 间隔式也从这一刻起算。用户只是先把参数设好、想让它按规矩来，不是要它立刻备一份。
func (m *Manager) SaveBackupPolicy(id string, p BackupPolicy) (BackupPolicyView, error) {
	inst, ok := m.st.Get(id)
	if !ok {
		return BackupPolicyView{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, id)
	}
	np, err := NormalizeBackupPolicy(p)
	if err != nil {
		return BackupPolicyView{}, err
	}
	np.LastRun = inst.Backup.LastRun
	np.Since = inst.Backup.Since
	if !inst.Backup.Enabled && np.Enabled {
		np.Since = time.Now()
	}
	inst.Backup = np
	if err := m.st.Put(id, inst); err != nil {
		return BackupPolicyView{}, err
	}
	return policyView(inst.Backup), nil
}

// humanBytes 日志里给人看的体积。备份动辄几十上百 MB，写字节数没人读得出来。
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// RunDueBackups 走一遍所有实例，给到点的跑一次自动备份。
// 抽出来是为了能被测试直接调用，不用等真实的 30 秒循环。
func (m *Manager) RunDueBackups(now time.Time) {
	for _, inst := range m.st.All() {
		if !backupDue(inst.Backup, now) {
			continue
		}
		m.runOneAutoBackup(inst, now)
	}
}

func (m *Manager) runOneAutoBackup(inst Instance, now time.Time) {
	// 同一实例不并发备份：大世界的 zip 要跑几分钟，撞在一起只会互相拖慢、多占磁盘 IO。
	m.mu.Lock()
	if m.backupsRunning[inst.ID] {
		m.mu.Unlock()
		return
	}
	m.backupsRunning[inst.ID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.backupsRunning, inst.ID)
		m.mu.Unlock()
	}()

	logs := m.logsFor(inst.ID)
	logs.Append("[EnderBox] 开始自动备份…")
	b, err := m.createBackup(inst, "", BackupKindAuto, backupScopeOf(inst))
	if err != nil {
		logs.Append("[EnderBox] 自动备份失败：" + err.Error())
		return
	}
	removed, rerr := m.applyRetention(inst.ID, inst.Backup.Keep)
	msg := fmt.Sprintf("[EnderBox] 自动备份完成：%s（%s）", b.File, humanBytes(b.Size))
	if len(removed) > 0 {
		msg += fmt.Sprintf("；按「只留最近 %d 份」清掉了 %d 份旧的", inst.Backup.Keep, len(removed))
	}
	logs.Append(msg)
	if rerr != nil {
		logs.Append("[EnderBox] 清理旧备份时出错：" + rerr.Error())
	}

	// LastRun 落盘放在备份**之后**：写在前面的话，备份失败也会被记成「跑过了」，
	// 那一天就再也不会重试。
	cur, ok := m.st.Get(inst.ID)
	if !ok {
		return
	}
	cur.Backup.LastRun = now
	_ = m.st.Put(cur.ID, cur)
}

// StartScheduler 起一个后台循环，同时驱动自动备份与计划任务。在 main 里调用。
//
// 两者共用这**一个** 30 秒的 tick，而不是各起一个 goroutine ——
// 同一台家用机上开十个实例就是十个循环在空转，纯浪费。
//
// ⚠ 计划任务走异步派发（dispatchDueTasks）：一条任务里可能有 delay（最长 1 小时）
// 或备份一个大世界（几分钟），同步跑会把整个循环卡住，后面的任务全部迟到。
func (m *Manager) StartScheduler() {
	go func() {
		// 启动后先等一会儿：守护进程刚起来时 AutoStartAll 正在拉进程、可能要写文件，
		// 别在这时候抢磁盘。
		time.Sleep(20 * time.Second)
		for {
			now := time.Now()
			m.RunDueBackups(now)
			m.dispatchDueTasks(now)
			time.Sleep(30 * time.Second)
		}
	}()
}
