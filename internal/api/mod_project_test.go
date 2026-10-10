package api

// 模组单项目详情 `/api/mods/project` 的接口级测试。
//
// 只验三件在真实使用里确实会犯的事：
//  ① 正文（body）与作者真的传出来了 —— 详情页的「介绍」页就是它，缺了整页只有抬头；
//  ② 从模组入口点进来一个整合包，要**明确拒绝**而不是静默渲染成「一个模组」；
//  ③ 源站说「没这个项目」要回 404，不能和「我们连不上」混成同一个 502 ——
//     两者的下一步动作完全不同（一个是换一个项目，一个是重试）。
//
// ⚠ 与 modpack_market_test.go 同一条纪律：`fetch.MarketFallbacks` 必须钉成 nil，
// 否则 404 之后的兜底会真打到 api.modrinth.com，测试就变成了「看今天网络好不好」。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/settings"
)

type modProjEnv struct {
	panel  *httptest.Server
	market *httptest.Server
}

func newModProjEnv(t *testing.T, projects map[string]map[string]any) *modProjEnv {
	t.Helper()

	oldFB := fetch.MarketFallbacks
	fetch.MarketFallbacks = nil
	t.Cleanup(func() { fetch.MarketFallbacks = oldFB })

	env := &modProjEnv{}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /project/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, ok := projects[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeTestJSON(t, w, p)
	})
	// 作者名要额外问一次团队接口（尽力而为）。这里给一个，验它真被读出来了。
	mux.HandleFunc("GET /team/{tid}/members", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		writeTestJSON(t, w, []map[string]any{
			{"role": "Owner", "user": map[string]any{"username": "mezz"}},
			{"role": "Member", "user": map[string]any{"username": "someone"}},
		})
	})
	env.market = httptest.NewServer(mux)

	data := t.TempDir()
	set, err := settings.NewStore(filepath.Join(data, "settings"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Update(func(p *settings.Settings) error {
		p.MarketBase = env.market.URL // 市场地址指向假源站 —— 与生产取的是同一处设置
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mgr, err := daemon.NewManager(filepath.Join(data, "daemon"))
	if err != nil {
		t.Fatal(err)
	}
	s := New(mgr, set, "127.0.0.1:0")

	pmux := http.NewServeMux()
	s.marketRoutes(pmux)
	env.panel = httptest.NewServer(pmux)

	t.Cleanup(func() {
		env.panel.Close()
		env.market.Close()
	})
	return env
}

// 一个正常的模组项目。
func modProjectJSON(id, slug, title, ptype string) map[string]any {
	return map[string]any{
		"id": id, "slug": slug, "title": title,
		"description": "一句话简介",
		"body":        "## 用法\n\n把这个模组丢进 mods 目录就行。",
		"team":        "team1", "project_type": ptype,
		"downloads": 1234567, "followers": 4321,
		"categories": []string{"utility", "fabric"}, "loaders": []string{"fabric"},
		"game_versions": []string{"1.20.1", "1.20.4"},
		"client_side":   "required", "server_side": "optional",
		"license":    map[string]any{"id": "MIT", "name": "MIT License"},
		"versions":   []string{"v1", "v2", "v3"},
		"published":  "2026-01-01T00:00:00Z",
		"updated":    "2026-09-01T00:00:00Z",
		"issues_url": "https://example.invalid/issues",
	}
}

func getModProject(t *testing.T, env *modProjEnv, q string) (int, []byte) {
	t.Helper()
	res, err := http.Get(env.panel.URL + "/api/mods/project?" + q)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b
}

func TestModProjectCarriesBodyAuthorAndLinks(t *testing.T) {
	env := newModProjEnv(t, map[string]map[string]any{
		"jei": modProjectJSON("jei", "jei", "Just Enough Items", "mod"),
	})

	code, raw := getModProject(t, env, "project=jei")
	if code != 200 {
		t.Fatalf("状态码 = %d，应为 200；body=%s", code, raw)
	}

	var got struct {
		Item     fetch.MarketProjectDetail `json:"item"`
		Mirrored bool                      `json:"mirrored"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("解析失败: %v；body=%s", err, raw)
	}

	// 正文：详情页「介绍」页唯一的内容来源。洗成纯文本或干脆不传，整页就只剩抬头。
	if !strings.Contains(got.Item.Body, "## 用法") {
		t.Fatalf("正文没传出来（或被洗掉了）：%q", got.Item.Body)
	}
	if got.Item.Author != "mezz" {
		t.Fatalf("作者 = %q，应取到团队里的 Owner（mezz）", got.Item.Author)
	}
	if got.Item.License.ID != "MIT" {
		t.Fatalf("许可 = %q，应为 MIT", got.Item.License.ID)
	}
	if got.Item.VersionCount != 3 {
		t.Fatalf("版本数 = %d，应为 3（源站 versions 有三个）", got.Item.VersionCount)
	}
	if got.Item.ProjectType != "mod" {
		t.Fatalf("项目类型 = %q，应为 mod", got.Item.ProjectType)
	}
	if got.Item.WebURL == "" {
		t.Fatal("webUrl 为空 —— 右栏「在源站上打开原页」就没了着落")
	}
	if got.Mirrored {
		t.Fatal("假源站就是配置里的那个地址，不该报成镜像命中")
	}
}

// 从模组入口点进来一个整合包：必须明说「这是个整合包」，而不是静默渲染。
func TestModProjectRejectsNonMod(t *testing.T) {
	env := newModProjEnv(t, map[string]map[string]any{
		"cobblemon": modProjectJSON("cobblemon", "cobblemon", "Cobblemon Official Modpack", "modpack"),
	})

	code, raw := getModProject(t, env, "project=cobblemon")
	if code != 400 {
		t.Fatalf("状态码 = %d，应为 400；body=%s", code, raw)
	}
	msg := string(raw)
	if !strings.Contains(msg, "整合包") {
		t.Fatalf("错误文案要点名它到底是什么，实际：%s", msg)
	}
	// 报错要可操作：得告诉用户去哪儿装。
	if !strings.Contains(msg, "整合包市场") {
		t.Fatalf("错误文案要给出下一步（去整合包市场），实际：%s", msg)
	}
}

// 源站说「没这个项目」≠ 我们连不上。两者下一步动作不同，不能都是 502。
func TestModProjectNotFoundIs404(t *testing.T) {
	env := newModProjEnv(t, nil)

	code, raw := getModProject(t, env, "project=nope")
	if code != 404 {
		t.Fatalf("状态码 = %d，应为 404（源站明确回 404 时）；body=%s", code, raw)
	}
}

// 少参数是请求方的问题，回 400 并把参数名说清楚。
func TestModProjectMissingParamIs400(t *testing.T) {
	env := newModProjEnv(t, nil)

	code, raw := getModProject(t, env, "")
	if code != 400 {
		t.Fatalf("状态码 = %d，应为 400；body=%s", code, raw)
	}
	if !strings.Contains(string(raw), "project") {
		t.Fatalf("错误文案要点名缺的是哪个参数，实际：%s", raw)
	}
}
