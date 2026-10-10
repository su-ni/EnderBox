package modpack

import (
	"archive/zip"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFetcher 不联网：把「下载」退化成「往 dst 写一个可识别的内容」。
// fail[rel] 命中时返回该错误（模拟某个模组下不来）；cancelOnCall 达到时取消 ctx（模拟中途取消）。
type fakeFetcher struct {
	instanceDir string
	fail        map[string]error
	cancelOn    int
	calls       int
	cancel      context.CancelFunc
}

func (f *fakeFetcher) Fetch(ctx context.Context, url, dst, wantSHA, shaKind string, size int64) error {
	f.calls++
	if f.cancelOn > 0 && f.calls >= f.cancelOn && f.cancel != nil {
		f.cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	rel := filepath.ToSlash(strings.TrimPrefix(dst, filepath.Clean(f.instanceDir)+string(filepath.Separator)))
	if e, ok := f.fail[rel]; ok {
		return e
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, []byte("fake-content:"+filepath.Base(dst)), 0o644)
}

// writeFile 在 instanceDir 下写一个相对路径的文件（测试里造「用户/旧装的文件」用）。
func writeFile(t *testing.T, instanceDir, rel, content string) {
	t.Helper()
	p := filepath.Join(instanceDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readRel 读实例目录下某个相对路径文件的内容。
func readRel(t *testing.T, instanceDir, rel string) string {
	t.Helper()
	p := filepath.Join(instanceDir, filepath.FromSlash(rel))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", rel, err)
	}
	return string(b)
}

// makePack 造一个 .mrpack：里面放 modrinth.index.json + 若干覆盖文件（key 为完整 zip 条目名）。
func makePack(t *testing.T, overrides map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "pack.mrpack")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create(indexName)
	_, _ = w.Write([]byte(`{"game":"minecraft","formatVersion":1,"versionId":"v9",
		"dependencies":{"minecraft":"1.20.1","forge":"47.4.10"},"files":[]}`))
	for name, content := range overrides {
		ew, _ := zw.Create(name)
		_, _ = ew.Write([]byte(content))
	}
	_ = zw.Close()
	_ = f.Close()
	return p
}

// diskSHAFromFile 返回一个 diskSHA：真的读磁盘上的文件算 sha1，模拟「磁盘现状」。
func diskSHAFromFile(instanceDir string) func(string) (string, bool) {
	return func(rel string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(instanceDir, filepath.FromSlash(rel)))
		if err != nil {
			return "", false
		}
		sum := sha1.Sum(b)
		return hex.EncodeToString(sum[:]), true
	}
}

// sha1str 算一段内容的 sha1 十六进制串（测试里造「旧状态记录的哈希」用）。
func sha1str(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// mtime 取某相对路径文件的修改时间。
func mtime(t *testing.T, instanceDir, rel string) time.Time {
	t.Helper()
	p := filepath.Join(instanceDir, filepath.FromSlash(rel))
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

// TestExecuteAddReplaceRemoveKeepAndLeaveUserFilesAlone 守规则①+③：
// 新增/变更/删除/保留各走一遍，且计划外「用户自己丢进 mods/ 的文件」一个字节都不能动。
func TestExecuteAddReplaceRemoveKeepAndLeaveUserFilesAlone(t *testing.T) {
	instance := t.TempDir()
	// 旧状态（升级前装了什么）
	old := &State{Files: []FileRef{
		{Path: "mods/keep.jar", SHA: "k"},
		{Path: "mods/change.jar", SHA: "old"},
		{Path: "mods/gone.jar", SHA: "g"},
	}, Overrides: []OverrideRef{}}
	next := idx(
		file("mods/keep.jar", SideRequired, SideRequired, "k"),
		file("mods/change.jar", SideRequired, SideRequired, "new"),
		file("mods/added.jar", SideRequired, SideRequired, "a"),
	)
	ov, err := ReadOverrides(makePack(t, map[string]string{
		"overrides/config/settings.txt": "SETTINGS",
		"server-overrides/server.txt":   "SERVER",
	}))
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(old, next, disk(map[string]string{
		"mods/keep.jar": "k", "mods/change.jar": "old", "mods/gone.jar": "g",
	}), ov)

	// 预置磁盘现状
	writeFile(t, instance, "mods/keep.jar", "keep-original")
	writeFile(t, instance, "mods/change.jar", "change-old")
	writeFile(t, instance, "mods/gone.jar", "gone-old")
	// 用户自己丢进来的，绝不能被碰
	writeFile(t, instance, "mods/mine-1.jar", "USER-FILE-1")
	writeFile(t, instance, "mods/mine-2.jar", "USER-FILE-2")
	// 计划外但 admin 手动改过名的配置文件
	writeFile(t, instance, "config/admin.toml", "ADMIN-CONF")

	pack := makePack(t, map[string]string{
		"overrides/config/settings.txt": "SETTINGS",
		"server-overrides/server.txt":   "SERVER",
	})
	ff := &fakeFetcher{instanceDir: instance}
	res, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: next, Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "2.0", Fetcher: ff,
	})
	if err != nil {
		t.Fatalf("执行报错：%v", err)
	}

	// 新增/变更落盘
	if got := readRel(t, instance, "mods/added.jar"); !strings.HasPrefix(got, "fake-content:") {
		t.Errorf("新增文件没落盘：%q", got)
	}
	if got := readRel(t, instance, "mods/change.jar"); !strings.HasPrefix(got, "fake-content:") {
		t.Errorf("变更文件没被覆盖：%q", got)
	}
	// 删除生效
	if _, err := os.Stat(filepath.Join(instance, "mods/gone.jar")); !os.IsNotExist(err) {
		t.Error("该删的 mods/gone.jar 还在")
	}
	// 保留不变
	if got := readRel(t, instance, "mods/keep.jar"); got != "keep-original" {
		t.Errorf("保留文件被改动了：%q", got)
	}
	// 计划外用户文件原封不动
	if got := readRel(t, instance, "mods/mine-1.jar"); got != "USER-FILE-1" {
		t.Errorf("用户文件被碰了：%q", got)
	}
	if got := readRel(t, instance, "mods/mine-2.jar"); got != "USER-FILE-2" {
		t.Errorf("用户文件被碰了：%q", got)
	}
	if got := readRel(t, instance, "config/admin.toml"); got != "ADMIN-CONF" {
		t.Errorf("计划外配置被碰了：%q", got)
	}
	// 覆盖文件落地
	if got := readRel(t, instance, "config/settings.txt"); got != "SETTINGS" {
		t.Errorf("overrides 没解出来：%q", got)
	}
	if got := readRel(t, instance, "server.txt"); got != "SERVER" {
		t.Errorf("server-overrides 没解出来：%q", got)
	}

	if res.Added != 3 || res.Replaced != 1 || res.Removed != 1 {
		t.Errorf("计数不对：added=%d replaced=%d removed=%d，期望 3/1/1（1 模组新增 + 2 覆盖新增）", res.Added, res.Replaced, res.Removed)
	}
	// 新状态要包含 added/new-change/keep，且不能有 gone
	paths := map[string]bool{}
	for _, f := range res.State.Files {
		paths[f.Path] = true
	}
	for _, want := range []string{"mods/added.jar", "mods/change.jar", "mods/keep.jar"} {
		if !paths[want] {
			t.Errorf("新状态漏了 %s", want)
		}
	}
	if paths["mods/gone.jar"] {
		t.Error("新状态还记着已删除的 gone.jar")
	}
	// 变更后的 sha 应该是新版本
	for _, f := range res.State.Files {
		if f.Path == "mods/change.jar" && f.SHA != "new" {
			t.Errorf("change.jar 状态 sha 没更新：%s", f.SHA)
		}
	}
	if len(res.State.Overrides) != 2 {
		t.Errorf("新状态该记 2 个覆盖文件，实际 %d", len(res.State.Overrides))
	}
	if len(res.Failed) != 0 || len(res.Skipped) != 0 {
		t.Errorf("不该有失败/跳过：failed=%v skipped=%v", res.Failed, res.Skipped)
	}
}

// TestExecuteNeverTouchesFilesOutsideThePlan 守规则①的硬边界：
// 计划里没有提到的路径，执行器连 open 都不该碰。这里直接造一个「只删一个、其余都别动」的计划，
// 并撒一堆用户文件，断言它们全部原样。
func TestExecuteNeverTouchesFilesOutsideThePlan(t *testing.T) {
	instance := t.TempDir()
	old := &State{Files: []FileRef{{Path: "mods/gone.jar", SHA: "g"}}, Overrides: []OverrideRef{}}
	next := idx(file("mods/keep.jar", SideRequired, SideRequired, "k"))
	// 计划里只有「删 gone.jar」，其它一律不碰
	plan := &Plan{ToRemove: []Change{{Path: "mods/gone.jar", Action: ActionRemove}}}

	writeFile(t, instance, "mods/gone.jar", "bye")
	writeFile(t, instance, "mods/user-a.jar", "A")
	writeFile(t, instance, "mods/sub/user-b.jar", "B")
	writeFile(t, instance, "plugins/x.jar", "P")
	writeFile(t, instance, "readme.txt", "R")

	ff := &fakeFetcher{instanceDir: instance}
	res, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, Plan: plan, Index: next, Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if _, err := os.Stat(filepath.Join(instance, "mods/gone.jar")); !os.IsNotExist(err) {
		t.Error("该删的没删")
	}
	for rel, want := range map[string]string{
		"mods/user-a.jar":     "A",
		"mods/sub/user-b.jar": "B",
		"plugins/x.jar":       "P",
		"readme.txt":          "R",
	} {
		if got := readRel(t, instance, rel); got != want {
			t.Errorf("计划外文件 %s 被改了：%q", rel, got)
		}
	}
	if res.Removed != 1 {
		t.Errorf("Removed=%d，期望 1", res.Removed)
	}
}

// TestExecuteBlocksZipSlip 守规则②（执行层再挡一次 zip-slip）：
// 包里放一个 ../../evil 的覆盖条目，断言报错，且实例目录之外没有产生任何文件。
func TestExecuteBlocksZipSlip(t *testing.T) {
	instance := t.TempDir()
	// 越界的覆盖条目：本该落在 config/ 下，却想逃到实例目录外面
	evilOutside := filepath.Join(filepath.Dir(instance), "evil-escaped.txt")
	_ = evilOutside
	pack := makePack(t, map[string]string{
		"overrides/../../evil-escaped.txt": "PWNED",
		"overrides/ok.txt":                 "OK",
	})
	next := idx()
	plan := &Plan{}
	ff := &fakeFetcher{instanceDir: instance}
	_, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: next,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err == nil {
		t.Fatal("zip-slip 条目没被拦下")
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(instance), "evil-escaped.txt")); !os.IsNotExist(statErr) {
		t.Errorf("实例目录之外产生了文件：%v", statErr)
	}
}

// TestExecuteRemoveMissingFileIsNotFailure 守规则③（删除错误要区分）：
// 计划要删的文件磁盘上本来就不在（用户自己删了），这不算失败，整批照常完成。
func TestExecuteRemoveMissingFileIsNotFailure(t *testing.T) {
	instance := t.TempDir()
	old := &State{Files: []FileRef{{Path: "mods/ghost.jar", SHA: "g"}}, Overrides: []OverrideRef{}}
	next := idx(file("mods/keep.jar", SideRequired, SideRequired, "k"))
	plan := BuildPlan(old, next, disk(map[string]string{"mods/keep.jar": "k"}), nil)
	// 注意：ghost.jar 磁盘上不存在

	ff := &fakeFetcher{instanceDir: instance}
	res, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, Plan: plan, Index: next, Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if len(res.Failed) != 0 {
		t.Errorf("不存在的文件被当成失败了：%v", res.Failed)
	}
	if len(res.State.Files) != 1 || res.State.Files[0].Path != "mods/keep.jar" {
		t.Errorf("状态里还留着已不存在的 ghost.jar：%v", res.State.Files)
	}
}

// TestExecuteOneFetchFailureDoesNotAbortOthers 守「下载失败不中止整批」：
// 某一个模组下不来，它进 Failed，其余照装，且新状态不包含失败的那个。
func TestExecuteOneFetchFailureDoesNotAbortOthers(t *testing.T) {
	instance := t.TempDir()
	next := idx(
		file("mods/a.jar", SideRequired, SideRequired, "a"),
		file("mods/b.jar", SideRequired, SideRequired, "b"),
		file("mods/c.jar", SideRequired, SideRequired, "c"),
	)
	plan := &Plan{
		ToAdd: []Change{
			{Path: "mods/a.jar", Action: ActionAdd, New: &FileRef{Path: "mods/a.jar", SHA: "a"}},
			{Path: "mods/b.jar", Action: ActionAdd, New: &FileRef{Path: "mods/b.jar", SHA: "b"}},
			{Path: "mods/c.jar", Action: ActionAdd, New: &FileRef{Path: "mods/c.jar", SHA: "c"}},
		},
	}
	ff := &fakeFetcher{instanceDir: instance, fail: map[string]error{"mods/b.jar": os.ErrClosed}}
	res, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, Plan: plan, Index: next,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err != nil {
		t.Fatalf("部分失败不该是 error：%v", err)
	}
	if res.Added != 2 {
		t.Errorf("成功装了 2 个，实际 Added=%d", res.Added)
	}
	if len(res.Failed) != 1 || res.Failed[0] != "mods/b.jar" {
		t.Errorf("Failed 该只有 mods/b.jar：%v", res.Failed)
	}
	paths := map[string]bool{}
	for _, f := range res.State.Files {
		paths[f.Path] = true
	}
	if !paths["mods/a.jar"] || !paths["mods/c.jar"] {
		t.Error("成功的 a/c 没进状态")
	}
	if paths["mods/b.jar"] {
		t.Error("失败的 b.jar 不该出现在新状态里")
	}
	// 失败的那个确实没落盘
	if _, statErr := os.Stat(filepath.Join(instance, "mods/b.jar")); !os.IsNotExist(statErr) {
		t.Error("失败的文件不该落盘")
	}
}

// TestExecuteExtractsBothOverridePrefixes 守规则⑤：
// overrides/ 与 server-overrides/ 的条目都该正确解出来（且父目录会自动建）。
func TestExecuteExtractsBothOverridePrefixes(t *testing.T) {
	instance := t.TempDir()
	pack := makePack(t, map[string]string{
		"overrides/config/sub/settings.json":       "CFG",
		"server-overrides/world/server.properties": "PROP",
	})
	next := idx()
	// 把包里的覆盖文件读出来交给 BuildPlan，计划里才会把它们列为 ToAdd
	ov, err := ReadOverrides(pack)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(nil, next, nil, ov)
	ff := &fakeFetcher{instanceDir: instance}
	res, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: next,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if got := readRel(t, instance, "config/sub/settings.json"); got != "CFG" {
		t.Errorf("overrides 深层条目没解出来：%q", got)
	}
	if got := readRel(t, instance, "world/server.properties"); got != "PROP" {
		t.Errorf("server-overrides 条目没解出来：%q", got)
	}
	if len(res.State.Overrides) != 2 {
		t.Errorf("状态里该记 2 个覆盖文件，实际 %d：%v", len(res.State.Overrides), res.State.Overrides)
	}
}

// TestExecuteDoesNotOverwriteUserModifiedOverride 守底线②（覆盖文件版）：
// 用户改过的覆盖文件（磁盘内容对不上我们记的那份）→ 不覆盖、进 Skipped、内容一字未变。
func TestExecuteDoesNotOverwriteUserModifiedOverride(t *testing.T) {
	instance := t.TempDir()
	old := &State{Overrides: []OverrideRef{
		{Path: "overrides/config/x.toml", SHA: "origsha", SHAKind: "sha1"},
	}}
	// 包里这份是新内容；磁盘上是用户改过的
	pack := makePack(t, map[string]string{"overrides/config/x.toml": "NEWPACK"})
	ov, err := ReadOverrides(pack)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, instance, "config/x.toml", "USEREDIT")
	plan := BuildPlan(old, idx(), diskSHAFromFile(instance), ov)
	if len(plan.Skipped) != 1 || plan.Skipped[0].Path != "overrides/config/x.toml" {
		t.Fatalf("用户改过的覆盖文件该进 Skipped：%v", plan.Skipped)
	}
	if !strings.Contains(plan.Skipped[0].Reason, "没有覆盖") {
		t.Errorf("跳过原因要写清「没有覆盖」：%q", plan.Skipped[0].Reason)
	}
	ff := &fakeFetcher{instanceDir: instance}
	if _, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: idx(), Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	}); err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if got := readRel(t, instance, "config/x.toml"); got != "USEREDIT" {
		t.Errorf("用户改过的覆盖文件被覆盖了：%q", got)
	}
}

// TestExecuteDoesNotExtractUnplannedOverride 守规则①（覆盖文件版）：
// 新包有个覆盖条目，但旧状态没记过、磁盘上用户自己已占用了同名文件 → 不解压、内容不变。
func TestExecuteDoesNotExtractUnplannedOverride(t *testing.T) {
	instance := t.TempDir()
	old := &State{Overrides: []OverrideRef{}} // 旧状态完全没记过这个覆盖文件
	pack := makePack(t, map[string]string{"overrides/config/y.toml": "PACK"})
	ov, err := ReadOverrides(pack)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, instance, "config/y.toml", "USEROWN")
	plan := BuildPlan(old, idx(), diskSHAFromFile(instance), ov)
	if len(plan.Skipped) != 1 || plan.Skipped[0].Path != "overrides/config/y.toml" {
		t.Fatalf("计划外覆盖条目该进 Skipped：%v", plan.Skipped)
	}
	if !strings.Contains(plan.Skipped[0].Reason, "你自己的文件") {
		t.Errorf("跳过原因要写清「你自己的文件」：%q", plan.Skipped[0].Reason)
	}
	ff := &fakeFetcher{instanceDir: instance}
	if _, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: idx(), Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	}); err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if got := readRel(t, instance, "config/y.toml"); got != "USEROWN" {
		t.Errorf("计划外覆盖文件被解压了：%q", got)
	}
}

// TestExecuteRemovesDroppedOverride 守规则（覆盖文件删除）：
// 旧有、新版本没有、且磁盘与我们记的一致 → 删掉。
func TestExecuteRemovesDroppedOverride(t *testing.T) {
	instance := t.TempDir()
	content := "server config v1"
	writeFile(t, instance, "config/z.toml", content)
	old := &State{Overrides: []OverrideRef{
		{Path: "overrides/config/z.toml", SHA: sha1str(content), SHAKind: "sha1"},
	}}
	// 新版本包里没有 z.toml（但仍是带服务端模组的合法包）
	pack := makePack(t, map[string]string{})
	ov, err := ReadOverrides(pack)
	if err != nil {
		t.Fatal(err)
	}
	plan := BuildPlan(old, idx(file("mods/_m.jar", SideRequired, SideRequired, "m")), diskSHAFromFile(instance), ov)
	if len(plan.ToRemove) != 1 || plan.ToRemove[0].Path != "overrides/config/z.toml" {
		t.Fatalf("该删掉的覆盖文件没进 ToRemove：%v", plan.ToRemove)
	}
	ff := &fakeFetcher{instanceDir: instance}
	res, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: idx(), Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if res.Removed != 1 {
		t.Errorf("Removed=%d，期望 1", res.Removed)
	}
	if _, statErr := os.Stat(filepath.Join(instance, "config/z.toml")); !os.IsNotExist(statErr) {
		t.Error("该删的覆盖文件还在")
	}
}

// TestExecuteKeepsUnchangedOverride 守「keep 不重复写」：
// 内容相同的覆盖文件 → 进 Kept，不进 ToAdd/ToReplace（所以不会被重写）。
func TestExecuteKeepsUnchangedOverride(t *testing.T) {
	instance := t.TempDir()
	content := "keep me"
	writeFile(t, instance, "config/k.toml", content)
	old := &State{Overrides: []OverrideRef{
		{Path: "overrides/config/k.toml", SHA: sha1str(content), SHAKind: "sha1"},
	}}
	pack := makePack(t, map[string]string{"overrides/config/k.toml": content})
	ov, err := ReadOverrides(pack)
	if err != nil {
		t.Fatal(err)
	}
	before := mtime(t, instance, "config/k.toml")
	plan := BuildPlan(old, idx(), diskSHAFromFile(instance), ov)
	if len(plan.Kept) != 1 || plan.Kept[0].Path != "overrides/config/k.toml" {
		t.Fatalf("该进 Kept：%v", plan.Kept)
	}
	if len(plan.ToAdd) != 0 || len(plan.ToReplace) != 0 {
		t.Errorf("keep 不该进 ToAdd/ToReplace：add=%v replace=%v", plan.ToAdd, plan.ToReplace)
	}
	ff := &fakeFetcher{instanceDir: instance}
	if _, err := Execute(context.Background(), ExecuteOptions{
		InstanceDir: instance, PackPath: pack, Plan: plan, Index: idx(), Old: old,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	}); err != nil {
		t.Fatalf("执行报错：%v", err)
	}
	if got := readRel(t, instance, "config/k.toml"); got != content {
		t.Errorf("keep 文件内容被改了：%q", got)
	}
	after := mtime(t, instance, "config/k.toml")
	if !before.Equal(after) {
		t.Errorf("keep 的文件被重写（mtime 变了）：before=%v after=%v", before, after)
	}
}

// TestReadOverridesEmptyAndFromIndexNotNil 守两个「空集合不是 nil」的契约：
// 没有覆盖文件的包 → ReadOverrides 返回空切片（非 nil）；FromIndex(nil overrides) → st.Overrides 也非 nil。
func TestReadOverridesEmptyAndFromIndexNotNil(t *testing.T) {
	pack := makePack(t, map[string]string{}) // 只有 index，没有覆盖文件
	ov, err := ReadOverrides(pack)
	if err != nil {
		t.Fatal(err)
	}
	if ov == nil {
		t.Error("ReadOverrides 该返回空切片而不是 nil")
	}
	if len(ov) != 0 {
		t.Errorf("不该有覆盖文件，实际 %v", ov)
	}
	st := FromIndex(idx(), "s", "p", "1", nil)
	if st.Overrides == nil {
		t.Error("FromIndex(nil overrides) 的 Overrides 不该是 nil")
	}
}

// TestExecuteRespectsContextCancellation 守规则④：
// ctx 中途被取消，执行器立刻停，不 panic，返回的进度是「实际做到的」。
func TestExecuteRespectsContextCancellation(t *testing.T) {
	instance := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ff := &fakeFetcher{instanceDir: instance, cancelOn: 2, cancel: cancel}
	next := idx(
		file("mods/a.jar", SideRequired, SideRequired, "a"),
		file("mods/b.jar", SideRequired, SideRequired, "b"),
		file("mods/c.jar", SideRequired, SideRequired, "c"),
	)
	plan := &Plan{
		ToAdd: []Change{
			{Path: "mods/a.jar", Action: ActionAdd, New: &FileRef{Path: "mods/a.jar", SHA: "a"}},
			{Path: "mods/b.jar", Action: ActionAdd, New: &FileRef{Path: "mods/b.jar", SHA: "b"}},
			{Path: "mods/c.jar", Action: ActionAdd, New: &FileRef{Path: "mods/c.jar", SHA: "c"}},
		},
	}
	res, err := Execute(ctx, ExecuteOptions{
		InstanceDir: instance, Plan: plan, Index: next,
		Slug: "s", ProjectID: "p", VersionNumber: "1", Fetcher: ff,
	})
	if err == nil {
		t.Fatal("ctx 取消后该返回 error")
	}
	if res == nil {
		t.Fatal("取消后也要带回已做到的结果")
	}
	if res.Added != 1 {
		t.Errorf("取消时只该完成 1 个，实际 Added=%d", res.Added)
	}
	if _, statErr := os.Stat(filepath.Join(instance, "mods/c.jar")); !os.IsNotExist(statErr) {
		t.Error("取消后第三个文件不该被下载")
	}
}
