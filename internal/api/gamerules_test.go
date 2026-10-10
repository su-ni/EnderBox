package api

// 游戏规则接口的错误分流测试。
//
// 成功路径（真的读到几十条、改一条再读回来）由 daemon 的全链路单测与真机验收覆盖；
// 这里只钉 HTTP 层的事：路由挂没挂上、409/404/400 分流对不对、reason 码给没给。
// reason 码是给机器看的 —— 前端靠它切「停服说明」态，靠文案匹配会一改就断。
//
// 注：mpGet / mpPost / mpDecode 是本包共享的 HTTP 测试辅助（见 modpack_test.go）。

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/settings"
)

// newRuleEnv 起一台只挂游戏规则路由的测试面板 + 一个「存在但没启动」的实例。
func newRuleEnv(t *testing.T) (*httptest.Server, *daemon.Instance) {
	t.Helper()
	data := t.TempDir()
	set, err := settings.NewStore(filepath.Join(data, "settings"))
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := daemon.NewManager(filepath.Join(data, "daemon"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(mgr, set, "127.0.0.1:0")

	mux := http.NewServeMux()
	s.gameruleRoutes(mux)
	panel := httptest.NewServer(mux)
	t.Cleanup(panel.Close)

	inst := &daemon.Instance{Name: "停着的服", Type: daemon.TypePaper}
	if err := mgr.Create(inst); err != nil {
		t.Fatal(err)
	}
	return panel, inst
}

func TestGameruleListNoServer(t *testing.T) {
	panel, inst := newRuleEnv(t)
	code, raw := mpGet(t, panel.URL+"/api/instances/"+inst.ID+"/gamerules")
	if code != 409 {
		t.Fatalf("停服的实例该是 409，拿到 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["reason"] != "no-server" {
		t.Errorf("reason 该是 no-server，拿到 %v", m["reason"])
	}
	if s, _ := m["error"].(string); s == "" {
		t.Error("error 消息不能空 —— 界面要把它当停服说明显示")
	}
}

func TestGameruleListUnknownInstance(t *testing.T) {
	panel, _ := newRuleEnv(t)
	code, _ := mpGet(t, panel.URL+"/api/instances/没有这个实例/gamerules")
	if code != 404 {
		t.Fatalf("未知实例该是 404，拿到 %d", code)
	}
}

func TestGameruleSetErrorSplit(t *testing.T) {
	panel, inst := newRuleEnv(t)
	url := panel.URL + "/api/instances/" + inst.ID + "/gamerules"

	cases := []struct {
		what   string
		body   map[string]any
		code   int
		reason string
	}{
		{"未知规则名", map[string]any{"name": "NotARule", "value": "true"}, 404, "unknown-rule"},
		{"名字带换行（注入）", map[string]any{"name": "keepInventory\ngamerule doDaylightCycle true", "value": "true"}, 400, ""},
		{"名字带空格", map[string]any{"name": "keep Inventory", "value": "true"}, 400, ""},
		{"值不是布尔/整数", map[string]any{"name": "keepInventory", "value": "yes"}, 400, ""},
		{"值带换行（注入）", map[string]any{"name": "keepInventory", "value": "true\nstop"}, 400, ""},
		{"合法但服务器没跑", map[string]any{"name": "keepInventory", "value": "true"}, 409, "no-server"},
	}
	for _, c := range cases {
		code, raw := mpPost(t, url, c.body)
		if code != c.code {
			t.Errorf("%s：想要 %d，拿到 %d（%s）", c.what, c.code, code, raw)
			continue
		}
		if c.reason != "" {
			if m := mpDecode(t, raw); m["reason"] != c.reason {
				t.Errorf("%s：reason 想要 %q，拿到 %v", c.what, c.reason, m["reason"])
			}
		}
		// 任何分流都要给可读的消息，不能只有状态码
		if m := mpDecode(t, raw); m["error"] == nil {
			t.Errorf("%s：没有 error 消息", c.what)
		}
	}
}
