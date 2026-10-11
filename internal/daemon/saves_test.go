package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/su-ni/EnderBox/internal/jobs"
)

// 世界名两道闸：路径分隔符 / 穿越（worldDir 同款），以及 Windows 非法字符。
func TestValidWorldName(t *testing.T) {
	good := []string{"world", "我的世界", "w1", "a.b"}
	for _, n := range good {
		if got, err := validWorldName(n); err != nil || got != n {
			t.Errorf("%q 应该合法，实际 got=%q err=%v", n, got, err)
		}
	}
	// 两侧空格会被裁掉，这是有意的（用户从别处粘过来常带尾空格）
	if got, err := validWorldName("  world  "); err != nil || got != "world" {
		t.Errorf("两侧空格应被裁掉，实际 got=%q err=%v", got, err)
	}

	bad := []string{"", ".", "..", "a/b", `a\b`, "c:d", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b", "x."}
	for _, n := range bad {
		if _, err := validWorldName(n); err == nil {
			t.Errorf("%q 应该被拒", n)
		}
	}
}

// soleTopDir 只按结构判，不看目录叫什么名字。
//
// ⚠ 这条测试能抓住「误用 singleRootPrefix」的回归：singleRootPrefix 会把 world/
// 当成 layoutDirs 而不剥（那是为整合包设计的），到这里就会返回 "" 而不是 "world/"。
func TestSoleTopDir(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{
			name:  "唯一顶层目录（就叫 world，必须剥）",
			files: map[string][]byte{"world/level.dat": []byte("x"), "world/region/r.mca": []byte("y")},
			want:  "world/",
		},
		{
			name:  "顶层是散文件（我们自己导出的形态，不剥）",
			files: map[string][]byte{"level.dat": []byte("x"), "region/r.mca": []byte("y")},
			want:  "",
		},
		{
			name:  "两个顶层目录（不剥）",
			files: map[string][]byte{"a/x": []byte("1"), "b/y": []byte("2")},
			want:  "",
		},
		{
			// 打包工具常顺手塞几个空目录项。把目录项算进「顶层目录」的判断里，
			// 这里就会误判成「有两个顶层目录」而不剥 —— 用户最后拿到 world/world/level.dat。
			name:  "夹着无关空目录项，仍要剥（只看有内容的条目）",
			files: map[string][]byte{"world/level.dat": []byte("x"), "empty/": []byte{}},
			want:  "world/",
		},
		{
			name:  "顶层既有散文件又有目录（不剥）",
			files: map[string][]byte{"level.dat": []byte("1"), "region/r.mca": []byte("2")},
			want:  "",
		},
	}
	for _, c := range cases {
		p := filepath.Join(dir, strings.ReplaceAll(c.name, "/", "_")+".zip")
		writeZip(t, p, c.files)
		if got := soleTopDir(p); got != c.want {
			t.Errorf("%s：soleTopDir 应为 %q，实际 %q", c.name, c.want, got)
		}
	}
}

// 导入的核心路径：解压 → level.dat 校验 → rename 转正。
func TestImportSaveExtractsWorld(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "导入测试", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	z := filepath.Join(t.TempDir(), "w.zip")
	writeZip(t, z, map[string][]byte{
		"level.dat":        []byte("LEVEL"),
		"region/r.0.0.mca": []byte(strings.Repeat("A", 100)),
	})

	var lastDone, lastTotal int
	calls := 0
	cb := func(fd, ft int, bd, bt int64, cur string) {
		calls++
		lastDone, lastTotal = fd, ft
	}
	res, err := m.ImportSave(inst.ID, "imported", z, cb)
	if err != nil {
		t.Fatalf("ImportSave: %v", err)
	}
	if res.World != "imported" {
		t.Errorf("世界名应为 imported，实际 %s", res.World)
	}
	if res.Files != 2 {
		t.Errorf("应导入 2 个文件，实际 %d", res.Files)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "imported", "level.dat")); err != nil {
		t.Errorf("level.dat 应该已经就位：%v", err)
	}
	// 进度：回调确实被调用过，且最后一次是「全解完」
	if calls == 0 {
		t.Error("进度回调一次都没被调用")
	}
	if lastDone != lastTotal || lastTotal != 2 {
		t.Errorf("最后进度应为 2/2，实际 %d/%d", lastDone, lastTotal)
	}
	// 临时目录不能留残骸
	if _, err := os.Stat(filepath.Join(inst.Dir, "imported.enderbox-import.tmp")); !os.IsNotExist(err) {
		t.Error("导入完成后不该留下 .enderbox-import.tmp")
	}
}

// 上传的包套了一层目录时也要能正确就位（常见：用户右键「压缩 world 文件夹」）。
func TestImportSaveStripsSoleTopDir(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "剥层测试", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	z := filepath.Join(t.TempDir(), "nested.zip")
	writeZip(t, z, map[string][]byte{"world/level.dat": []byte("L"), "world/region/r.mca": []byte("R")})

	if _, err := m.ImportSave(inst.ID, "unpacked", z, nil); err != nil {
		t.Fatalf("ImportSave: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "unpacked", "level.dat")); err != nil {
		t.Errorf("剥层后 level.dat 应该在根：%v", err)
	}
	// 不该出现 unpacked/world/level.dat 这种双保险结构
	if _, err := os.Stat(filepath.Join(inst.Dir, "unpacked", "world")); !os.IsNotExist(err) {
		t.Error("不该出现 unpacked/world/ 这种多套一层的结构")
	}
}

// 不是存档的包要当场失败，并且**不留半个目录** —— 否则用户会以为导入成功了。
func TestImportSaveRejectsNoLevelDat(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "坏包测试", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	z := filepath.Join(t.TempDir(), "bad.zip")
	writeZip(t, z, map[string][]byte{"readme.txt": []byte("not a world")})

	if _, err := m.ImportSave(inst.ID, "broken", z, nil); err == nil {
		t.Fatal("没有 level.dat 的包应该被拒")
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "broken")); !os.IsNotExist(err) {
		t.Error("失败的导入不该留下目标目录")
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "broken.enderbox-import.tmp")); !os.IsNotExist(err) {
		t.Error("失败的导入不该留下临时目录")
	}
}

// 同名冲突：不能覆盖已有的世界（宁可让用户换个名字）。
func TestImportSaveRejectsExistingName(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "重名测试", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 先占位一个同名世界
	if err := os.MkdirAll(filepath.Join(inst.Dir, "dup"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.Dir, "dup", "level.dat"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	z := filepath.Join(t.TempDir(), "w.zip")
	writeZip(t, z, map[string][]byte{"level.dat": []byte("new")})

	if _, err := m.ImportSave(inst.ID, "dup", z, nil); err == nil {
		t.Fatal("同名存档应该被拒，不能覆盖")
	}
	// 原来的世界必须原封不动
	b, err := os.ReadFile(filepath.Join(inst.Dir, "dup", "level.dat"))
	if err != nil || string(b) != "old" {
		t.Errorf("原有世界不该被改动，实际内容 %q err=%v", string(b), err)
	}
}

// 切换 world：停服态下改 level-name 并落盘。
func TestActivateSaveWritesLevelName(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "切换测试", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(inst.Dir, "old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst.Dir, "old", "level.dat"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.ActivateSave(inst.ID, "old"); err != nil {
		t.Fatalf("ActivateSave: %v", err)
	}

	rows, err := m.ReadProperties(inst.ID)
	if err != nil {
		t.Fatalf("ReadProperties: %v", err)
	}
	got := ""
	for _, r := range rows {
		if r.Key == "level-name" {
			got = r.Value
		}
	}
	if got != "old" {
		t.Errorf("level-name 应为 old，实际 %q", got)
	}
}

// 切到一个不存在的世界要被拒 —— 否则下次开服会静默生成一个空的新世界。
func TestActivateSaveRejectsMissingWorld(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "缺档测试", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := m.ActivateSave(inst.ID, "nope"); err == nil {
		t.Fatal("切到不存在的世界应该被拒")
	}
}

// 进度回调要能如实报出双口径（字节数不能恒为 0，否则大世界进度条永远卡在 0%）。
func TestExtractZipReportsBytes(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "b.zip")
	writeZip(t, z, map[string][]byte{
		"a.bin": []byte(strings.Repeat("x", 5000)),
		"b.bin": []byte(strings.Repeat("y", 3000)),
	})

	var maxBytes int64
	var totalBytes int64
	cb := func(fd, ft int, bd, bt int64, cur string) {
		if bd > maxBytes {
			maxBytes = bd
		}
		totalBytes = bt
	}
	if err := extractZipToProgress(z, filepath.Join(dir, "out"), "", cb); err != nil {
		t.Fatalf("解压: %v", err)
	}
	if totalBytes != 8000 {
		t.Errorf("总字节应为 8000，实际 %d", totalBytes)
	}
	if maxBytes != 8000 {
		t.Errorf("累计字节应到 8000，实际 %d", maxBytes)
	}
	// 类型契约留个编译期断言点
	var _ jobs.ProgressFunc = cb
}
