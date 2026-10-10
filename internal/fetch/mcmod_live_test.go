package fetch

import (
	"os"
	"testing"
)

// 真机联调（默认跳过）：GOMCMOD_LIVE=1 go test ./internal/fetch/ -run Live -v
// 跑通它才能证明「GBK 转码 + data-id + JSON 端点 + 解析」这条链在真实站点上是对的。
func TestMcmodLiveSodium(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	if os.Getenv("GOMCMOD_LIVE") == "" {
		t.Skip("需要 GOMCMOD_LIVE=1")
	}
	r, err := McmodRefresh("Sodium")
	if err != nil {
		t.Fatalf("抓取失败：%v", err)
	}
	t.Logf("中心=%q classID=%q cat=%q color=%q", r.Center.Name, r.Center.ClassID, r.Center.Cat, r.Center.Color)
	t.Logf("节点=%d 边=%d 分类=%d", len(r.Nodes), len(r.Links), len(r.Cats))
	for _, c := range r.Cats {
		t.Logf("  分类: %q %q", c.Name, c.Color)
	}
	for i, n := range r.Nodes {
		if i >= 6 {
			break
		}
		t.Logf("  node[%d] id=%s name=%q cat=%q color=%q classID=%q content=%q",
			i, n.ID, n.Name, n.Cat, n.Color, n.ClassID, n.Content)
	}
	if len(r.Nodes) < 50 {
		t.Errorf("钠该有几百个节点，只有 %d 个，肯定哪里错了", len(r.Nodes))
	}
	if r.Center.Cat != "中心" && r.Center.Cat == "" {
		t.Error("中心分类为空")
	}
}
