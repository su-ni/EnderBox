package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ── 批量删除 ────────────────────────────────────────────
//
// 判据：一条失败不该连累其它条（用户勾了 20 个，第 3 个被占用
// 不该让剩下 17 个白勾一遍），而且回了 ok 的必须真在磁盘上消失了。
func TestDeletePathsPartial(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "批量", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	mk := func(rel, content string) {
		p := filepath.Join(inst.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a.txt", "a")
	mk("sub/b.txt", "b")
	mk("keep.txt", "keep")

	got := m.DeletePaths(inst.ID, []string{"a.txt", "sub/b.txt", "nope.txt"})
	if len(got.OK) != 2 {
		t.Errorf("应删掉 2 条，实际 %d（%v）", len(got.OK), got.OK)
	}
	if len(got.Failed) != 1 || got.Failed[0].Path != "nope.txt" {
		t.Errorf("应有 1 条失败且是 nope.txt，实际 %+v", got.Failed)
	}
	for _, rel := range []string{"a.txt", "sub/b.txt"} {
		if _, err := os.Stat(filepath.Join(inst.Dir, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s 没被真正删掉", rel)
		}
	}
	// 没点名的一律不动
	if _, err := os.Stat(filepath.Join(inst.Dir, "keep.txt")); err != nil {
		t.Errorf("keep.txt 不该被删: %v", err)
	}
	// 实例根目录不能删
	r := m.DeletePaths(inst.ID, []string{""})
	if len(r.Failed) != 1 {
		t.Errorf("删实例根应被记为失败，实际 %+v", r)
	}
}

// 越界的路径要被当成「这条失败」而不是整个请求崩掉。
func TestDeletePathsRejectsTraversal(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "越界", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got := m.DeletePaths(inst.ID, []string{"../outside.txt"})
	if len(got.Failed) != 1 || len(got.OK) != 0 {
		t.Errorf("越界路径应失败且不删任何东西，实际 %+v", got)
	}
}

// ── 新建文件夹 ──────────────────────────────────────────

// 判据：父目录不存在要一起建出来；**已存在必须报错** —— 静默覆盖是毁数据。
func TestMkdirAt(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "建目录", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := m.MkdirAt(inst.ID, "a/b/c"); err != nil {
		t.Fatalf("多级目录没建出来: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(inst.Dir, "a", "b", "c")); err != nil || !fi.IsDir() {
		t.Errorf("a/b/c 不是目录: %v", err)
	}
	if _, err := m.MkdirAt(inst.ID, "a/b/c"); !errors.Is(err, ErrPathExists) {
		t.Errorf("已存在应回 ErrPathExists，实际 %v", err)
	}
	if _, err := m.MkdirAt(inst.ID, ""); err == nil {
		t.Error("空路径（实例根）应被拒绝")
	}
}

// ── 移动 / 重命名 ───────────────────────────────────────

// 判据：撞上同名目标时**原文件必须还在**（先删后失败是最坏的结果）。
func TestMovePath(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "移动", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	write := func(rel, s string) {
		p := filepath.Join(inst.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old.txt", "hello")
	write("taken.txt", "occupied")

	if _, err := m.MovePath(inst.ID, "old.txt", "new.txt"); err != nil {
		t.Fatalf("改名失败: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(inst.Dir, "new.txt")); err != nil || string(b) != "hello" {
		t.Errorf("改名后内容不对: %q / %v", string(b), err)
	}
	if _, err := m.MovePath(inst.ID, "new.txt", "taken.txt"); !errors.Is(err, ErrPathExists) {
		t.Errorf("目标已存在应回 ErrPathExists，实际 %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "new.txt")); err != nil {
		t.Errorf("失败后原文件丢了: %v", err)
	}
	if _, err := m.MovePath(inst.ID, "ghost.txt", "x.txt"); !errors.Is(err, ErrPathNotFound) {
		t.Errorf("不存在的源应回 ErrPathNotFound，实际 %v", err)
	}
	// 跨目录移动：目标目录不存在时要建出来
	if _, err := m.MovePath(inst.ID, "new.txt", "deep/er/moved.txt"); err != nil {
		t.Fatalf("跨目录移动失败: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "deep", "er", "moved.txt")); err != nil {
		t.Errorf("移动后的文件不在: %v", err)
	}
}

// ── 复制 ────────────────────────────────────────────────

// 判据：目录要递归复制；**把自己复制进自己是无限递归**，必须在递归前挡住
//（否则每拷一层里面又多一层，直到写满磁盘）。
func TestCopyPath(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "复制", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	write := func(rel, s string) {
		p := filepath.Join(inst.Dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("src/one.txt", "1")
	write("src/deep/two.txt", "2")

	if _, err := m.CopyPath(inst.ID, "src", "src-copy"); err != nil {
		t.Fatalf("复制目录失败: %v", err)
	}
	for _, rel := range []string{"src-copy/one.txt", "src-copy/deep/two.txt"} {
		if _, err := os.Stat(filepath.Join(inst.Dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("递归复制漏了 %s: %v", rel, err)
		}
	}
	if _, err := m.CopyPath(inst.ID, "src", "src/inner"); err == nil {
		t.Error("把目录复制进它自己里面应被拒绝（否则无限递归）")
	}
	if _, err := m.CopyPath(inst.ID, "src", "src-copy"); !errors.Is(err, ErrPathExists) {
		t.Errorf("目标已存在应回 ErrPathExists，实际 %v", err)
	}
	// 单文件复制：内容要一致
	if _, err := m.CopyPath(inst.ID, "src/one.txt", "one-copy.txt"); err != nil {
		t.Fatalf("复制文件失败: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(inst.Dir, "one-copy.txt"))
	if err != nil || string(b) != "1" {
		t.Errorf("复制出来的内容不对: %q / %v", string(b), err)
	}
}
