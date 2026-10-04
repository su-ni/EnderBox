package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// 建一个临时闭环的 Manager，测试不去碰任何真实数据目录。
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// 默认行为不能变：删实例只摘配置，磁盘目录原样保留。
func TestDeleteKeepsFilesByDefault(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "留着", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	marker := filepath.Join(inst.Dir, "world", "level.dat")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("save"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(inst.ID, false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := m.st.Get(inst.ID); ok {
		t.Error("配置应该已经摘掉了")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("默认不该删磁盘文件，但 %s 没了: %v", marker, err)
	}
}

// 勾了「同时删除世界文件」才真的清目录。
func TestDeleteWithFilesRemovesInstanceDir(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "删干净", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inst.Dir, "server.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(inst.ID, true); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := m.st.Get(inst.ID); ok {
		t.Error("配置应该已经摘掉了")
	}
	if _, err := os.Stat(inst.Dir); !os.IsNotExist(err) {
		t.Errorf("实例目录应该被删掉，Stat 得到 err=%v", err)
	}
}

// 最要紧的一条：实例配置里的 dir 是可以被手改的（也可能被假 JSON 塞成任意路径）。
// 只允许删 <数据目录>/instances/ 之下的东西，否则一个写错的 dir 就把整个盘交出去了。
func TestDeleteWithFilesRefusesPathOutsideInstances(t *testing.T) {
	m := newTestManager(t)

	// 数据目录之外的「无辜目录」，里面放个文件当证人
	outside := t.TempDir()
	victim := filepath.Join(outside, "重要文件.txt")
	if err := os.WriteFile(victim, []byte("别删我"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := &Instance{Name: "越界", Type: TypePaper, Dir: outside}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	err := m.Delete(inst.ID, true)
	if err == nil {
		t.Fatal("目录在 instances 之外时必须报错，实际却成功了")
	}
	if _, statErr := os.Stat(victim); statErr != nil {
		t.Fatalf("越界目录被删了！%s 已经不在了: %v", victim, statErr)
	}
	// 校验发生在任何副作用之前：配置也得原样留着，用户才有机会改回来
	if _, ok := m.st.Get(inst.ID); !ok {
		t.Error("拒绝删除时不该动配置")
	}
}

// 目标恰好等于 instances 根目录本身也必须拒绝 —— 上面的 ".." 判断拦不住这一种。
func TestDeleteWithFilesRefusesInstancesRootItself(t *testing.T) {
	m := newTestManager(t)
	root := filepath.Join(m.baseDir, "instances")
	keep := filepath.Join(root, "别动我.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	inst := &Instance{Name: "根目录", Type: TypePaper, Dir: root}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := m.Delete(inst.ID, true); err == nil {
		t.Fatal("目标等于 instances 根目录时必须报错")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("instances 根目录下的文件被删了: %v", err)
	}
}

// dir 为空的实例不能带着 removeFiles 把「当前目录」删掉。
func TestDeleteWithFilesRefusesEmptyDir(t *testing.T) {
	m := newTestManager(t)
	inst := &Instance{Name: "空目录", Type: TypePaper}
	if err := m.Create(inst); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 绕过 Create 的兜底，直接模拟一条被改坏的配置
	broken := inst
	broken.Dir = ""
	if err := m.st.Put(broken.ID, *broken); err != nil {
		t.Fatal(err)
	}

	if err := m.Delete(broken.ID, true); err == nil {
		t.Fatal("dir 为空时必须报错，否则 RemoveAll(\"\") 的行为完全看实现脸色")
	}
}
