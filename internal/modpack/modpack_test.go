package modpack

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── 小工具 ──

func file(path string, client, server Side, sha string) File {
	return File{Path: path, Env: Env{Client: client, Server: server},
		Hashes: map[string]string{"sha512": sha}, Downloads: []string{"https://cdn.example/" + path}}
}

func idx(files ...File) *Index {
	return &Index{Game: "minecraft", FormatVersion: 1, VersionID: "v9",
		Name:         "Test Pack",
		Files:        files,
		Dependencies: map[string]string{"minecraft": "1.20.1", "forge": "47.4.10"}}
}

// 内存里的「磁盘」：只填我们要测的那些路径
func disk(m map[string]string) func(string) (string, bool) {
	return func(p string) (string, bool) { v, ok := m[p]; return v, ok }
}

// ── ① 用户的核心要求：纯客户端整合包必须被拒 ──

func TestServerVerdictRejectsClientOnlyPack(t *testing.T) {
	// 真实形态来自实测：Fabulously Optimized 这类包的所有模组都是 server=unsupported
	ix := idx(
		file("mods/sodium.jar", SideRequired, SideUnsupported, "a"),
		file("mods/iris.jar", SideRequired, SideUnsupported, "b"),
		file("mods/zoomify.jar", SideRequired, SideUnsupported, "c"),
	)
	v := ix.ServerVerdict()
	if v.OK {
		t.Fatal("纯客户端整合包被放行了 —— 这是这次需求的核心，不能错")
	}
	if !v.SkippedAll {
		t.Error("该标出「整包都被跳过」")
	}
	if v.Install != 0 || v.Skipped != 3 {
		t.Errorf("计数不对：install=%d skipped=%d，期望 0/3", v.Install, v.Skipped)
	}
	// 拒绝理由要带**证据**和**下一步**，不能只说「不支持」
	for _, want := range []string{"3 个文件", "server: unsupported", "客户端", "服务端包", "server_side: required"} {
		if !strings.Contains(v.Reason, want) {
			t.Errorf("拒绝理由里应该有 %q：%s", want, v.Reason)
		}
	}
}

func TestServerInstallableKeepsUnknownSide(t *testing.T) {
	// ⚠ 认不出的档位**必须当 required**：当成 unsupported 会把该装的模组悄悄漏掉，
	// 而且界面上完全看不出来（服务端起不来时用户根本想不到是这里）。
	ix := idx(
		file("mods/a.jar", SideRequired, "", "a"),      // 没写 server
		file("mods/b.jar", SideRequired, "weird", "b"), // 上游以后加的新档位
		file("mods/client.jar", SideRequired, SideUnsupported, "c"),
	)
	install, skipped := ix.ServerInstallable()
	if len(install) != 2 {
		t.Errorf("认不出的档位该算「要装」，实际 install=%d", len(install))
	}
	if len(skipped) != 1 || skipped[0].Path != "mods/client.jar" {
		t.Errorf("只该跳过明确标 unsupported 的那个，实际 %+v", skipped)
	}
	if v := ix.ServerVerdict(); !v.OK {
		t.Error("有可装文件却判成不能装")
	}
}

// ── ② 解析层的防护 ──

func TestParseIndexRejectsZipSlip(t *testing.T) {
	for _, bad := range []string{
		"../../windows/system32/evil.jar",
		"mods/../../escape.jar",
		"/abs/path.jar",
		`mods\win\style.jar`, // 反斜杠一律不收
		"C:/drv.jar",
	} {
		ix := idx(file(bad, SideRequired, SideRequired, "x"))
		if err := ix.validate(); err == nil {
			t.Errorf("路径 %q 该被拒（zip-slip 防护）", bad)
		}
	}
	// 正常路径要放行
	ok := idx(file("mods/fine-1.0.jar", SideRequired, SideRequired, "x"),
		file("config/sub/ok.json", SideRequired, SideRequired, "y"))
	if err := ok.validate(); err != nil {
		t.Errorf("正常路径被误拒：%v", err)
	}
}

func TestParseIndexRejectsEmptyFiles(t *testing.T) {
	// 空 files 会让所有统计都变 0，看着像「这包很干净」—— 其实是读错了
	if err := idx().validate(); err == nil {
		t.Error("一个文件都没有的索引该被拒")
	}
}

func TestParseIndexFromSyntheticZip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.mrpack")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create(indexName)
	_, _ = w.Write([]byte(`{"game":"minecraft","formatVersion":1,"versionId":"v1",
		"dependencies":{"minecraft":"1.20.1","fabric-loader":"0.15.0"},
		"files":[{"path":"mods/a.jar","hashes":{"sha1":"aa"},"env":{"client":"required","server":"required"},
		          "downloads":["https://cdn/a.jar"],"fileSize":1234}]}`))
	_ = zw.Close()
	_ = f.Close()

	ix, err := ParseIndex(p)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if ix.Minecraft() != "1.20.1" {
		t.Errorf("MC 版本 = %q", ix.Minecraft())
	}
	if k, v := ix.Loader(); k != "fabric-loader" || v != "0.15.0" {
		t.Errorf("加载器 = (%q,%q)，期望 (fabric-loader,0.15.0)", k, v)
	}
	if s, kind := ix.Files[0].SHA(); s != "aa" || kind != "sha1" {
		t.Errorf("没有 sha512 时该退回 sha1，实际 (%q,%q)", s, kind)
	}
	// 不是 .mrpack 的 zip
	bad := filepath.Join(dir, "y.mrpack")
	bf, _ := os.Create(bad)
	bz := zip.NewWriter(bf)
	bw, _ := bz.Create("readme.txt")
	_, _ = bw.Write([]byte("hi"))
	_ = bz.Close()
	_ = bf.Close()
	if _, err := ParseIndex(bad); err == nil {
		t.Error("没有 modrinth.index.json 的 zip 该被拒，并且说清不是这个格式")
	}
}

// ── ③ 升级比对：三条安全底线 ──

func TestBuildPlanAddReplaceRemoveKeep(t *testing.T) {
	old := &State{
		Format: "mrpack", Minecraft: "1.20.1", Loader: map[string]string{"forge": "47.4.10"},
		Files: []FileRef{
			{Path: "mods/keep.jar", SHA: "k"},
			{Path: "mods/change.jar", SHA: "old"},
			{Path: "mods/gone.jar", SHA: "g"},
		},
		Overrides: []OverrideRef{},
	}
	next := idx(
		file("mods/keep.jar", SideRequired, SideRequired, "k"),
		file("mods/change.jar", SideRequired, SideRequired, "new"),
		file("mods/added.jar", SideRequired, SideRequired, "a"),
	)
	// 磁盘现状与旧状态一致（没人动过）
	p := BuildPlan(old, next, disk(map[string]string{
		"mods/keep.jar": "k", "mods/change.jar": "old", "mods/gone.jar": "g",
	}), nil)
	s := p.Summary()
	if s.Keep != 1 || s.Replace != 1 || s.Add != 1 || s.Remove != 1 || s.Skip != 0 {
		t.Fatalf("计数不对：%+v（期望 keep1 replace1 add1 remove1 skip0）", s)
	}
	if p.Refusal != "" {
		t.Errorf("不该拒绝：%s", p.Refusal)
	}
	if p.ToRemove[0].Path != "mods/gone.jar" {
		t.Errorf("该删的是 gone.jar，实际 %q", p.ToRemove[0].Path)
	}
}

func TestBuildPlanNeverTouchesUserFiles(t *testing.T) {
	// 底线 ①：用户自己丢进 mods/ 的模组，不在任何 packstate 里 —— 一个都不能碰。
	old := &State{Files: []FileRef{{Path: "mods/pack.jar", SHA: "p"}}, Overrides: []OverrideRef{}}
	next := idx(file("mods/pack.jar", SideRequired, SideRequired, "p"))
	// 磁盘上除了包里的那个，还有用户自己加的两个
	p := BuildPlan(old, next, disk(map[string]string{
		"mods/pack.jar": "p", "mods/mine-1.jar": "u1", "mods/mine-2.jar": "u2",
	}), nil)
	if len(p.ToRemove) != 0 {
		t.Errorf("删了用户的文件！%+v", p.ToRemove)
	}
	for _, c := range append(append([]Change{}, p.ToAdd...), p.ToReplace...) {
		if strings.Contains(c.Path, "mine-") {
			t.Errorf("动了用户的文件：%+v", c)
		}
	}
}

func TestBuildPlanSkipsPathsUserAlreadyOccupies(t *testing.T) {
	// 新包要在某路径放文件，但用户已经在那儿放了自己的东西 → 不覆盖，列进跳过
	old := &State{Files: []FileRef{}, Overrides: []OverrideRef{}}
	next := idx(file("mods/foo.jar", SideRequired, SideRequired, "pack"))
	p := BuildPlan(old, next, disk(map[string]string{"mods/foo.jar": "users-own"}), nil)
	if len(p.ToAdd) != 0 || len(p.ToReplace) != 0 {
		t.Errorf("覆盖了用户自己的同名文件：add=%+v replace=%+v", p.ToAdd, p.ToReplace)
	}
	if len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0].Reason, "你自己的文件") {
		t.Errorf("该列进跳过并说明原因，实际 %+v", p.Skipped)
	}
}

func TestBuildPlanSkipsUserModifiedFiles(t *testing.T) {
	// 底线 ②：包里的文件被用户改过（改 config / 换版本）→ 不覆盖、不删除，列出来
	old := &State{Files: []FileRef{
		{Path: "config/x.toml", SHA: "orig"},
		{Path: "mods/edited.jar", SHA: "orig2"},
	}, Overrides: []OverrideRef{}}
	next := idx(
		file("config/x.toml", SideRequired, SideRequired, "newsum"),    // 新包改了这个
		file("mods/edited.jar", SideRequired, SideRequired, "newsum2"), // 新包也改了这个
	)
	p := BuildPlan(old, next, disk(map[string]string{
		"config/x.toml": "users-edit", "mods/edited.jar": "users-edit-2",
	}), nil)
	if len(p.ToReplace) != 0 {
		t.Errorf("覆盖了用户改过的文件：%+v", p.ToReplace)
	}
	if len(p.Skipped) != 2 {
		t.Fatalf("该跳过 2 个，实际 %d：%+v", len(p.Skipped), p.Skipped)
	}
	for _, c := range p.Skipped {
		if !strings.Contains(c.Reason, "没有覆盖") {
			t.Errorf("跳过原因要写清「没有覆盖」，否则用户以为被改了：%q", c.Reason)
		}
	}
}

func TestBuildPlanSkipsRemovalOfModifiedFiles(t *testing.T) {
	// 新版本删掉了某个模组，但用户改过它 → 不删
	old := &State{Files: []FileRef{{Path: "mods/legacy.jar", SHA: "orig"}}, Overrides: []OverrideRef{}}
	next := idx(file("mods/other.jar", SideRequired, SideRequired, "o"))
	p := BuildPlan(old, next, disk(map[string]string{"mods/legacy.jar": "users-edit"}), nil)
	if len(p.ToRemove) != 0 {
		t.Errorf("删了用户改过的文件：%+v", p.ToRemove)
	}
	if len(p.Skipped) != 1 || !strings.Contains(p.Skipped[0].Reason, "没有删") {
		t.Errorf("该跳过删除并说明，实际 %+v", p.Skipped)
	}
}

func TestBuildPlanRemovesFilesThatBecameClientOnly(t *testing.T) {
	// 新版把一个模组改成了纯客户端 → 从「装」变「删」，且理由要说清是版本改的，
	// 不然用户会以为自己误装了什么
	old := &State{Files: []FileRef{{Path: "mods/x.jar", SHA: "s"}}, Overrides: []OverrideRef{}}
	next := idx(
		file("mods/keep.jar", SideRequired, SideRequired, "k"),
		file("mods/x.jar", SideRequired, SideUnsupported, "s2"),
	)
	p := BuildPlan(old, next, disk(map[string]string{"mods/x.jar": "s", "mods/keep.jar": "k"}), nil)
	if len(p.ToRemove) != 1 || p.ToRemove[0].Path != "mods/x.jar" {
		t.Fatalf("该删掉变成客户端专属的那个：%+v", p.ToRemove)
	}
	if !strings.Contains(p.ToRemove[0].Reason, "纯客户端") {
		t.Errorf("理由要说清是「新版本改的」：%q", p.ToRemove[0].Reason)
	}
}

func TestBuildPlanRefusesClientOnlyTarget(t *testing.T) {
	// 目标版本不再是服务端包 → 必须拦住（真升上去会把服务端清成一堆客户端模组）
	old := &State{Files: []FileRef{}, Overrides: []OverrideRef{}}
	next := idx(file("mods/sodium.jar", SideRequired, SideUnsupported, "a"))
	p := BuildPlan(old, next, nil, nil)
	if p.Refusal == "" {
		t.Fatal("目标版本装不到服务端，却没有拒绝")
	}
	if !strings.Contains(p.Refusal, "客户端") {
		t.Errorf("拒绝理由要提到客户端：%q", p.Refusal)
	}
	if p.HasWork() {
		t.Error("拒绝了却还排了活")
	}
}

func TestBuildPlanWarnsOnVersionJump(t *testing.T) {
	old := &State{Minecraft: "1.20.1", Loader: map[string]string{"forge": "47.4.10"},
		Files: []FileRef{}, Overrides: []OverrideRef{}}
	next := idx(file("mods/a.jar", SideRequired, SideRequired, "a"))
	next.Dependencies["minecraft"] = "1.21.1"
	p := BuildPlan(old, next, nil, nil)
	if !p.LevelJump {
		t.Fatal("游戏版本从 1.20.1 换到 1.21.1 却没标成版本跳跃")
	}
	if !strings.Contains(p.LevelJumpNote, "1.20.1") || !strings.Contains(p.LevelJumpNote, "1.21.1") {
		t.Errorf("跳跃提示要写清从哪到哪：%q", p.LevelJumpNote)
	}
	if !strings.Contains(p.LevelJumpNote, "备份") {
		t.Errorf("跳跃提示必须提备份：%q", p.LevelJumpNote)
	}

	// 同版本只是换了加载器也要提示
	old2 := &State{Minecraft: "1.20.1", Loader: map[string]string{"forge": "47.4.10"},
		Files: []FileRef{}, Overrides: []OverrideRef{}}
	next2 := idx(file("mods/a.jar", SideRequired, SideRequired, "a"))
	next2.Dependencies["forge"] = "47.4.30"
	if p := BuildPlan(old2, next2, nil, nil); !p.LevelJump || !strings.Contains(p.LevelJumpNote, "加载器") {
		t.Errorf("加载器换了要提示：jump=%v note=%q", p.LevelJump, p.LevelJumpNote)
	}
}

// ── ④ 状态文件本身 ──

func TestStateRoundTripAndNoNullLists(t *testing.T) {
	dir := t.TempDir()
	st := &State{Format: "mrpack", Minecraft: "1.20.1", Files: []FileRef{}, Overrides: []OverrideRef{}}
	if err := SaveState(dir, st); err != nil {
		t.Fatal(err)
	}
	// ⚠ 判据必须放在**序列化之后**：Go 里 nil slice 和空 slice 用 == / 遍历都分不出，
	// 过一遍 JSON 才看得出一个是 []、一个是 null（前端 .length 会当场炸）
	raw, err := os.ReadFile(StatePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "null") {
		t.Errorf("状态文件里有 null —— 前端会炸：%s", raw)
	}

	got, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Minecraft != "1.20.1" || got.Format != "mrpack" {
		t.Errorf("往返丢了字段：%+v", got)
	}
	if got.Files == nil || got.Overrides == nil {
		t.Error("读回来必须是空切片不是 nil")
	}
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(raw, &probe)
	if string(probe["files"]) != "[]" {
		t.Errorf("files 该是 []，实际 %s", probe["files"])
	}
}

func TestLoadStateMissingIsNoState(t *testing.T) {
	// 「不是整合包装的」和「状态文件坏了」要能分开 —— 界面上说法完全不同
	if _, err := LoadState(t.TempDir()); err != ErrNoState {
		t.Errorf("不存在时该返回 ErrNoState，实际 %v", err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, stateDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StatePath(dir), []byte("{坏掉的"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadState(dir)
	if err == nil || err == ErrNoState {
		t.Errorf("坏文件该报「解析失败」而不是 ErrNoState，实际 %v", err)
	}
	if !strings.Contains(err.Error(), "解析失败") {
		t.Errorf("错误信息要说清是解析失败：%v", err)
	}
}

func TestFromIndexBuildsStateFromServerSideOnly(t *testing.T) {
	ix := idx(
		file("mods/server.jar", SideRequired, SideRequired, "s"),
		file("mods/client.jar", SideRequired, SideUnsupported, "c"),
	)
	st := FromIndex(ix, "slug", "proj", "1.2.3", nil)
	if len(st.Files) != 1 || st.Files[0].Path != "mods/server.jar" {
		t.Fatalf("只该记服务端要装的那个：%+v", st.Files)
	}
	if st.VersionNumber != "1.2.3" || st.Slug != "slug" || st.Minecraft != "1.20.1" {
		t.Errorf("身份字段没填全：%+v", st)
	}
	if st.Loader["forge"] != "47.4.10" {
		t.Errorf("加载器没记下来：%+v", st.Loader)
	}
}
