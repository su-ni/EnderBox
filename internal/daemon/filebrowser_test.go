package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 文件管理器的安全边界必须先立起来：面板不该比它守护的东西更危险。
func TestResolvePathBlocksTraversal(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "边界", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	bad := []string{
		"..",
		"../outside.txt",
		"a/../../outside.txt",
		"a/b/../../../outside.txt",
	}
	if filepath.IsAbs(filepath.FromSlash("/etc/passwd")) {
		bad = append(bad, "/etc/passwd")
	}
	for _, rel := range bad {
		if _, _, err := m.resolvePath(inst.ID, rel); err == nil {
			t.Errorf("%q 越界了，应该被拒绝", rel)
		}
	}

	// 正常路径要能过，而且落在实例目录之内
	abs, clean, err := m.resolvePath(inst.ID, "config/server.properties")
	if err != nil {
		t.Fatalf("正常路径被误拒: %v", err)
	}
	if clean != "config/server.properties" {
		t.Errorf("规范化后的相对路径应是 config/server.properties，实际 %q", clean)
	}
	if !strings.HasPrefix(abs, inst.Dir) {
		t.Errorf("解析结果跑到实例目录外了：%s", abs)
	}
}

func TestListFilesAndReadWrite(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "读写", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(inst.Dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(inst.Dir, "config", "server.properties")
	if err := os.WriteFile(cfg, []byte("server-port=25565\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, err := m.ListFiles(inst.ID, "")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(l.Entries) == 0 || !l.Entries[0].IsDir || l.Entries[0].Name != "config" {
		t.Fatalf("根目录里应把 config 目录排在最前，实际 %+v", l.Entries)
	}

	l2, err := m.ListFiles(inst.ID, "config")
	if err != nil {
		t.Fatalf("ListFiles(config): %v", err)
	}
	if l2.Parent != "" {
		t.Errorf("config 的上一级应是根目录（空），实际 %q", l2.Parent)
	}
	if len(l2.Entries) != 1 || l2.Entries[0].Path != "config/server.properties" {
		t.Fatalf("config 里应只有一个文件，实际 %+v", l2.Entries)
	}
	if !l2.Entries[0].Text {
		t.Error(".properties 应当能被当文本打开")
	}

	text, err := m.ReadTextFile(inst.ID, "config/server.properties")
	if err != nil || text != "server-port=25565\n" {
		t.Fatalf("读回的内容不对：%q err=%v", text, err)
	}

	if err := m.WriteTextFile(inst.ID, "config/server.properties", "server-port=25566\n"); err != nil {
		t.Fatalf("WriteTextFile: %v", err)
	}
	if got, _ := m.ReadTextFile(inst.ID, "config/server.properties"); got != "server-port=25566\n" {
		t.Errorf("写入没生效：%q", got)
	}

	if err := m.DeletePath(inst.ID, "config/server.properties"); err != nil {
		t.Fatalf("DeletePath: %v", err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Error("文件应该已经删掉了")
	}
}

func TestDeletePathRefusesRoot(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "根目录", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, rel := range []string{"", ".", "/"} {
		if err := m.DeletePath(inst.ID, rel); err == nil {
			t.Errorf("删根目录（%q）必须被拒绝", rel)
		}
	}
	if _, err := os.Stat(inst.Dir); err != nil {
		t.Fatal("实例目录不该被删掉")
	}
}

func TestReadTextFileRejectsBinary(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "二进制", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 前 4 个字节带 NUL —— 典型的二进制特征
	if err := os.WriteFile(filepath.Join(inst.Dir, "world.dat"), []byte{0x00, 0x01, 0x02, 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadTextFile(inst.ID, "world.dat"); err == nil {
		t.Error("含 NUL 的文件不该当文本读出来")
	}
}

func TestExtractZipStripsSingleRoot(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "整合包", Type: TypeForge}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 网上下来的整合包十有八九长这样：外面套了一层同名文件夹
	zipPath := makeJar(t, inst.Dir, "MyPack.zip", map[string]string{
		"MyPack/mods/a.jar":         "a",
		"MyPack/config/server.toml": "b",
	})

	rel, err := m.ExtractZipAt(inst.ID, "MyPack.zip", "folder")
	if err != nil {
		t.Fatalf("ExtractZipAt: %v", err)
	}
	if rel != "MyPack" {
		t.Errorf("解压目标应是 MyPack，实际 %q", rel)
	}
	// 剥掉了外层，「MyPack」目录里直接就是 mods/，不该再套一层 MyPack
	if _, err := os.Stat(filepath.Join(inst.Dir, "MyPack", "mods", "a.jar")); err != nil {
		t.Errorf("应该剥掉外层文件夹：%v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "MyPack", "MyPack")); !os.IsNotExist(err) {
		t.Error("不该出现 MyPack/MyPack 这种再套一层的结构")
	}
	_ = zipPath
}

func TestExtractZipAtHereMerges(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "就地解压", Type: TypeForge}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	makeJar(t, inst.Dir, "pack.zip", map[string]string{
		"mods/b.jar": "b",
	})

	if _, err := m.ExtractZipAt(inst.ID, "pack.zip", "here"); err != nil {
		t.Fatalf("ExtractZipAt(here): %v", err)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "mods", "b.jar")); err != nil {
		t.Errorf("「直接解压到当前目录」应当把 mods/ 并进实例：%v", err)
	}
}

func TestExtractZipRefusesExistingFolder(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "同名目录", Type: TypeForge}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	makeJar(t, inst.Dir, "pack.zip", map[string]string{"a/b.txt": "b"})
	if err := os.MkdirAll(filepath.Join(inst.Dir, "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ExtractZipAt(inst.ID, "pack.zip", "folder"); err == nil {
		t.Error("已经存在同名文件夹时必须拒绝，而不是覆盖用户的目录")
	}
}

func TestSaveUploadToSanitizesName(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "上传", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	name, err := m.SaveUploadTo(inst.ID, "config", "../../evil.txt", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("SaveUploadTo: %v", err)
	}
	if name != "evil.txt" {
		t.Errorf("上传名里的路径要被打掉，只留文件名，实际 %q", name)
	}
	if _, err := os.Stat(filepath.Join(inst.Dir, "config", "evil.txt")); err != nil {
		t.Errorf("文件应落在 config 目录里：%v", err)
	}
}
