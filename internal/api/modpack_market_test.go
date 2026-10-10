package api

// 整合包市场（P1）的接口级测试。
//
// 造假方式与 modpack_test.go 同一套：httptest 起一台假 Modrinth（多了 /search）与一台假 CDN，
// .mrpack 现造、哈希按内容实算，所以下载链路的哈希校验在测试里也是真的在拦。
//
// ⚠ 两条纪律，破了测试就会变成薛定谔的：
//   1. fetch.MarketFallbacks 必须钉成 nil —— 不钉的话 404 之后的兜底会真打到 api.modrinth.com；
//   2. modpackStartJar 必须打桩 —— 不打的话每装一次 forge 包都会真去连 maven.minecraftforge.net。

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/modpack"
	"github.com/su-ni/EnderBox/internal/settings"
)

const (
	mkForgeVer    = "frg11111" // forge 包（服务端文件能自动准备）
	mkNeoVer      = "neo22222" // neoforge 包（类型认识，但服务端文件不能自动准备）
	mkQuiltVer    = "qlt33333" // quilt 包（面板压根没有这个服务端类型 → 必须拒）
	mkClientVer   = "cli44444" // 纯客户端包 → 必须拒
	mkProject     = "mpProj01"
	mkNoPackProj  = "mpNoPack1"  // 有 .mrpack 也有 .zip → 验「只留装得了的版本」
	mkZipOnlyProj = "mpZipOnly1" // 全是 .zip → 验「空数组而不是报错」
	mkPackName    = "Forge Pack"
)

type marketEnv struct {
	panel  *httptest.Server
	market *httptest.Server
	s      *Server
	mgr    *daemon.Manager

	mu         sync.Mutex
	lastSearch url.Values
	lastJarReq serverJarReq
	jarCalls   int
}

func newMarketEnv(t *testing.T) *marketEnv {
	t.Helper()
	oldFB := fetch.MarketFallbacks
	fetch.MarketFallbacks = nil
	t.Cleanup(func() { fetch.MarketFallbacks = oldFB })

	env := &marketEnv{}
	content := map[string][]byte{}         // 假 CDN：路径 → 内容
	versions := map[string][]byte{}        // 版本 id → 单版本 JSON
	projectVersions := map[string][]byte{} // 项目 id → 版本列表 JSON
	var searchHits []map[string]any        // 先声明、后灌：handler 闭包认的是这个变量

	mux := http.NewServeMux()
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		env.mu.Lock()
		env.lastSearch = r.URL.Query()
		env.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		writeTestJSON(t, w, map[string]any{"hits": searchHits, "total_hits": len(searchHits)})
	})
	mux.HandleFunc("GET /version/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := versions[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /project/{pid}/version", func(w http.ResponseWriter, r *http.Request) {
		b, ok := projectVersions[r.PathValue("pid")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("GET /cdn/", func(w http.ResponseWriter, r *http.Request) {
		b, ok := content[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	market := httptest.NewServer(mux)
	env.market = market

	// 版本 JSON：一个 .mrpack 文件。withPack=false 时挂一个 .zip（用来验版本列表会把
	// 「点了装不了」的版本剔掉）。
	versionJSON := func(id, projectID, fileName string, withPack bool) map[string]any {
		return map[string]any{
			"id": id, "project_id": projectID,
			"name": "2.4", "version_number": "2.4", "version_type": "release",
			"game_versions": []string{"1.20.1"}, "loaders": []string{"forge"},
			"files": []map[string]any{{
				"url":      market.URL + "/cdn/" + fileName,
				"filename": fileName,
				"primary":  true,
				"size":     len(content["/cdn/"+fileName]),
				"hashes":   map[string]string{"sha1": sha1hex(content["/cdn/"+fileName])},
			}},
		}
	}

	// addPack 造一份包、把它的文件挂上假 CDN、登记版本 JSON。
	addPack := func(verID, projectID, packName, mrpackName string, p fakePack) {
		p.name = packName
		p.versionID = verID
		zip := buildFakePack(t, market.URL, p)
		content["/cdn/"+mrpackName] = zip
		for _, f := range p.files {
			content["/cdn/"+f.path] = []byte(f.content)
		}
		versions[verID] = toJSON(t, versionJSON(verID, projectID, mrpackName, true))
	}

	addPack(mkForgeVer, mkProject, mkPackName, "forge-pack.mrpack", fakePack{
		files: []fakePackFile{
			{path: "mods/a.jar", content: "forge-mod-a", server: modpack.SideRequired},
			{path: "mods/clientonly.jar", content: "client-stuff", server: modpack.SideUnsupported},
		},
		overrides: map[string]string{"overrides/config/settings.toml": "SETTINGS-FORGE"},
	})
	addPack(mkNeoVer, mkProject, "Neo Pack", "neo-pack.mrpack", fakePack{
		deps:  map[string]string{"minecraft": "1.20.1", "neoforge": "21.1.0"},
		files: []fakePackFile{{path: "mods/n.jar", content: "neo-mod", server: modpack.SideRequired}},
	})
	addPack(mkQuiltVer, mkProject, "Quilt Pack", "quilt-pack.mrpack", fakePack{
		deps:  map[string]string{"minecraft": "1.20.1", "quilt-loader": "0.23.1"},
		files: []fakePackFile{{path: "mods/q.jar", content: "quilt-mod", server: modpack.SideRequired}},
	})
	addPack(mkClientVer, mkProject, "Client Only", "client-pack.mrpack", fakePack{
		files: []fakePackFile{{path: "mods/sodium.jar", content: "client", server: modpack.SideUnsupported}},
	})

	// 版本列表用：mkNoPackProj 一个有 .mrpack、一个只有 .zip（验「剔掉装不了的版本」）；
	// mkZipOnlyProj 一个可安装版本都没有（验「空数组而不是报错」）。
	content["/cdn/only-zip.zip"] = []byte("not-a-pack")
	projectVersions[mkNoPackProj] = toJSON(t, []any{
		versionJSON("vwithpack", mkNoPackProj, "forge-pack.mrpack", true),
		versionJSON("vnozpack1", mkNoPackProj, "only-zip.zip", false),
	})
	projectVersions[mkZipOnlyProj] = toJSON(t, []any{
		versionJSON("vzip1", mkZipOnlyProj, "only-zip.zip", false),
		versionJSON("vzip2", mkZipOnlyProj, "only-zip.zip", false),
	})

	searchHits = append(searchHits, map[string]any{
		"project_id": mkProject, "slug": "forge-pack", "title": mkPackName,
		"description": "d", "downloads": 14909057,
		"categories": []string{"forge", "adventure"}, "project_type": "modpack",
	})

	data := t.TempDir()
	set, err := settings.NewStore(filepath.Join(data, "settings"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.Update(func(p *settings.Settings) error {
		p.MarketBase = market.URL // 市场地址指向假源站 —— 与生产取的是同一处设置
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	mgr, err := daemon.NewManager(filepath.Join(data, "daemon"))
	if err != nil {
		t.Fatal(err)
	}
	env.mgr = mgr
	env.s = New(mgr, set, "127.0.0.1:0")

	pmux := http.NewServeMux()
	env.s.instanceModpackRoutes(pmux)
	env.s.modpackMarketRoutes(pmux)
	env.s.jobRoutes(pmux)
	env.panel = httptest.NewServer(pmux)

	// 服务端文件那一步打桩。真实现留着不动，由其它包的路由共用，不在这里重复验。
	oldJar := modpackStartJar
	modpackStartJar = func(s *Server, inst daemon.Instance, body serverJarReq) (*fetch.Task, error) {
		env.mu.Lock()
		env.jarCalls++
		env.lastJarReq = body
		env.mu.Unlock()
		return &fetch.Task{ID: "t-stub", State: fetch.StateDone, Step: "就绪"}, nil
	}

	t.Cleanup(func() {
		modpackStartJar = oldJar
		env.panel.Close()
		market.Close()
	})
	return env
}

// writeTestJSON 写假源站的 JSON。测试夹具里出错只能让请求失败，没有 *testing.T 可传。
func writeTestJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	_, _ = w.Write(toJSON(t, v))
}

func (e *marketEnv) facets(t *testing.T) string {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastSearch == nil {
		t.Fatal("假源站没收到过 /search —— 搜索这条路根本没走通")
	}
	return e.lastSearch.Get("facets")
}

func (e *marketEnv) jarSnapshot() (int, serverJarReq) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.jarCalls, e.lastJarReq
}

// ── 搜索 ──────────────────────────────────────────────────

// 默认必须把 server_side 的两个可用档位拼进 facets ——
// 少了它，实测会有 30% 的客户端包混进列表，用户点进去才在下一步被拒。
func TestModpackSearchFiltersServerSide(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpGet(t, env.panel.URL+"/api/modpacks/search?q=zombie")
	if code != 200 {
		t.Fatalf("搜索返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["onlyServer"] != true {
		t.Errorf("默认该只列服务端可用的，onlyServer=%v", m["onlyServer"])
	}
	if m["mirrored"] != false {
		t.Errorf("用的就是配置里的源站，mirrored 该是 false，实际 %v", m["mirrored"])
	}
	assertArrayField(t, m, "items")
	assertNoNullInRaw(t, raw)

	f := env.facets(t)
	for _, want := range []string{`"project_type:modpack"`, `"server_side:required"`, `"server_side:optional"`} {
		if !strings.Contains(f, want) {
			t.Errorf("facets 里少了 %s：%s", want, f)
		}
	}
}

// all=1 关掉服务端过滤，且响应要如实回报 —— 界面得能对用户解释「这次为什么有客户端包」。
func TestModpackSearchAllLiftsServerFilter(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpGet(t, env.panel.URL+"/api/modpacks/search?all=1&q=")
	if code != 200 {
		t.Fatalf("搜索返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["onlyServer"] != false {
		t.Errorf("带了 all=1，onlyServer 该是 false，实际 %v", m["onlyServer"])
	}
	if f := env.facets(t); strings.Contains(f, "server_side") {
		t.Errorf("all=1 时不该再拼 server_side：%s", f)
	}
}

// ── 版本列表 ──────────────────────────────────────────────

// 只有 .zip、没有 .mrpack 的版本必须被剔掉 —— 留在列表里就是个点了装不了的按钮。
func TestModpackVersionsDropsPacksWithoutMrpack(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpGet(t, env.panel.URL+"/api/modpacks/versions?project="+mkNoPackProj)
	if code != 200 {
		t.Fatalf("版本列表返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	items, ok := m["items"].([]any)
	if !ok {
		t.Fatalf("items 不是数组：%v", m["items"])
	}
	if len(items) != 1 {
		t.Fatalf("该只剩 1 个可安装版本，实际 %d 个：%s", len(items), raw)
	}
	first := items[0].(map[string]any)
	if first["id"] != "vwithpack" {
		t.Errorf("留下的该是那个带 .mrpack 的版本，实际 %v", first["id"])
	}
	assertNoNullInRaw(t, raw)
}

// 一个可安装版本都没有时，必须是空数组而不是 404 / 500 ——
// 界面要说「这个包没发过可安装的版本」，不是「加载失败」。
func TestModpackVersionsUnknownProjectIsEmpty(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpGet(t, env.panel.URL+"/api/modpacks/versions?project=no-such-project")
	if code == 200 {
		m := mpDecode(t, raw)
		if items, ok := m["items"].([]any); !ok || len(items) != 0 {
			t.Errorf("未知项目的项目该回 200 + 空数组，实际 %s", raw)
		}
		return
	}
	// 404 也可以接受，但**绝不能**是 500：那是我们自己的锅，却让用户以为他没填对
	if code >= 500 {
		t.Errorf("未知项目回了 %d，把「没这个项目」报成了我们的故障：%s", code, raw)
	}
}

// ── 预览（只算不装）────────────────────────────────────────

func TestModpackInspectForgePack(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkForgeVer})
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	assertNoNullInRaw(t, raw)
	if m["ok"] != true {
		t.Fatalf("forge 包该能装，ok=%v refusal=%v", m["ok"], m["refusal"])
	}
	if m["refusal"] != "" {
		t.Errorf("能装就别给 refusal：%v", m["refusal"])
	}
	if m["serverType"] != "forge" || m["loaderKind"] != "forge" || m["loaderVersion"] != "47.4.10" {
		t.Errorf("加载器识别不对：type=%v kind=%v ver=%v", m["serverType"], m["loaderKind"], m["loaderVersion"])
	}
	if m["minecraft"] != "1.20.1" {
		t.Errorf("游戏版本该是 1.20.1，实际 %v", m["minecraft"])
	}
	if m["canAutoJar"] != true {
		t.Errorf("forge 的服务端文件能自动准备，canAutoJar 该是 true")
	}
	if note, _ := m["jarNote"].(string); note != "" {
		t.Errorf("能自动准备就不该给 jarNote：%s", note)
	}
	if token, _ := m["token"].(string); token == "" {
		t.Error("能装却没给取件码 —— 那样点安装时那几十 MB 得重下一遍")
	}
	// 1 个 required 装、1 个 unsupported 跳过、1 份覆盖
	if m["install"] != float64(1) || m["skipped"] != float64(1) || m["overrideCount"] != float64(1) {
		t.Errorf("计数不对：install=%v skipped=%v override=%v", m["install"], m["skipped"], m["overrideCount"])
	}
	if m["name"] != mkPackName {
		t.Errorf("实例名该取包内索引的 name（%s），实际 %v", mkPackName, m["name"])
	}
}

// 纯客户端包：拒绝，且**不能留取件码** —— 留着的话用户改天还能拿它装，
// 而装上去是个「起得来但模组全不生效」的静默失败。
func TestModpackInspectRefusesClientOnlyPack(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkClientVer})
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["ok"] != false {
		t.Fatalf("纯客户端包该被拒，ok=%v", m["ok"])
	}
	reason, _ := m["refusal"].(string)
	if !strings.Contains(reason, "unsupported") {
		t.Errorf("拒绝理由要给证据（几个文件、什么标记），实际：%s", reason)
	}
	if token, _ := m["token"].(string); token != "" {
		t.Errorf("被拒的包不该留取件码，实际 %s", token)
	}
}

// 认不出的加载器（Quilt）：必须拒。用 vanilla 兜着装完再「成功」是最坏的一种失败 ——
// 界面报成功、服务器起得来、模组一个都没加载。
func TestModpackInspectRefusesUnknownLoader(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkQuiltVer})
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["ok"] != false {
		t.Fatalf("Quilt 包该被拒，ok=%v", m["ok"])
	}
	if m["serverType"] != "" {
		t.Errorf("Quilt 没有对应的服务端类型，serverType 该是空串，实际 %v", m["serverType"])
	}
	reason, _ := m["refusal"].(string)
	if !strings.Contains(reason, "Quilt") {
		t.Errorf("拒绝理由要点名是哪个加载器，实际：%s", reason)
	}
	if !strings.Contains(reason, "导入实例") {
		t.Errorf("拒绝理由要给下一步（导入实例这条路），实际：%s", reason)
	}
	if token, _ := m["token"].(string); token != "" {
		t.Errorf("被拒的包不该留取件码，实际 %s", token)
	}
}

// NeoForge：类型认识、模组装得上，但**服务端文件不能自动准备**。
// 这时 ok 仍为 true，但必须给 jarNote 说清现象 —— 界面要把它放在按钮上方。
func TestModpackInspectNeoForgeWarnsAboutJar(t *testing.T) {
	env := newMarketEnv(t)
	code, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkNeoVer})
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["ok"] != true {
		t.Fatalf("neoforge 包该能装（模组有价值），ok=%v refusal=%v", m["ok"], m["refusal"])
	}
	if m["serverType"] != "neoforge" {
		t.Errorf("serverType 该是 neoforge，实际 %v", m["serverType"])
	}
	if m["canAutoJar"] != false {
		t.Errorf("neoforge 的服务端文件不能自动准备，canAutoJar 该是 false")
	}
	note, _ := m["jarNote"].(string)
	if note == "" {
		t.Fatal("不能自动准备服务端文件时必须给 jarNote")
	}
	if !strings.Contains(note, "还不能自动下载") {
		t.Errorf("jarNote 要说清是我们没接、不是包坏了：%s", note)
	}
	if !strings.Contains(note, "自己") {
		t.Errorf("jarNote 要给下一步（自己放服务端文件）：%s", note)
	}
}

// 参数校验：两头都不给、或者都给，都必须 400 并说清是二选一。
func TestModpackInspectRejectsAmbiguousInput(t *testing.T) {
	env := newMarketEnv(t)
	for _, body := range []map[string]any{{}, {"version": mkForgeVer, "pack": "C:/x.mrpack"}} {
		code, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", body)
		if code != 400 {
			t.Errorf("输入 %v 该回 400，实际 %d：%s", body, code, raw)
		}
		if !strings.Contains(string(raw), "二选一") {
			t.Errorf("400 的文案要说清是二选一：%s", raw)
		}
	}
}

// ── 安装 ──────────────────────────────────────────────────

// 取件码必须是一次性的，且过期/没带时不能含糊。
func TestModpackInstallRequiresFreshToken(t *testing.T) {
	env := newMarketEnv(t)
	for _, tok := range []string{"", "pk_不存在", "mp_old"} {
		code, raw := mpPost(t, env.panel.URL+"/api/modpacks/install", map[string]any{"token": tok})
		if code != 400 {
			t.Errorf("取件码 %q 该回 400，实际 %d：%s", tok, code, raw)
		}
		if !strings.Contains(string(raw), "重新预览") {
			t.Errorf("400 的文案要指向下一步（重新预览）：%s", raw)
		}
	}
	if n := len(env.mgr.List()); n != 0 {
		t.Errorf("取件码不对时一个实例都不该建，实际建了 %d 个", n)
	}
}

// 全链路：预览 → 安装 → 起作业 → 装完。断言实例参数、落盘内容、packstate、作业快照。
func TestModpackInstallEndToEnd(t *testing.T) {
	env := newMarketEnv(t)

	_, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkForgeVer})
	token, _ := mpDecode(t, raw)["token"].(string)
	if token == "" {
		t.Fatal("预览没给取件码")
	}

	code, raw := mpPost(t, env.panel.URL+"/api/modpacks/install", map[string]any{"token": token})
	if code != 202 {
		t.Fatalf("安装该回 202，实际 %d：%s", code, raw)
	}
	res := mpDecode(t, raw)
	instID, _ := res["instanceId"].(string)
	opID, _ := res["opId"].(string)
	if instID == "" || opID == "" {
		t.Fatalf("202 必须同时给 instanceId 与 opId：%s", raw)
	}

	// 作业跑起来就得能看出这是「安装」而不是「升级」——
	// 老作业不带 kind，缺省按 upgrade 报，所以这里必须显式是 install。
	midCode, midRaw := mpGet(t, env.panel.URL+"/api/jobs/"+opID)
	if midCode != 200 {
		t.Fatalf("查作业返回 %d：%s", midCode, midRaw)
	}
	if k := mpDecode(t, midRaw)["kind"]; k != "install" {
		t.Errorf("作业 kind 该是 install，实际 %v", k)
	}

	_, snap := waitForOp(t, env.panel.URL, opID)
	// 终态的**线上原始字节**不能出现 null —— Go 的 nil slice 会序列化成 null，
	// 前端读 `result.failed.length` 会当场崩。这条只能查真响应，查重新编码的 map 是自欺。
	if code, rawSnap := mpGet(t, env.panel.URL+"/api/jobs/"+opID); code == 200 {
		assertNoNullInRaw(t, rawSnap)
	} else {
		t.Errorf("终态快照该取得到，实际 %d", code)
	}

	if snap["state"] != opDone {
		t.Fatalf("安装没跑完：state=%v err=%v", snap["state"], snap["err"])
	}
	if snap["stage"] != "" {
		t.Errorf("终态的 stage 该是空串，实际 %v", snap["stage"])
	}
	if snap["owner"] != instID {
		t.Errorf("作业快照要能指回实例：%v vs %v", snap["owner"], instID)
	}
	result, _ := snap["result"].(map[string]any)
	if result == nil {
		t.Fatalf("result 缺失：%v", snap)
	}
	assertArrayField(t, result, "skippedPaths", "failed", "warnings")
	if f, _ := result["failed"].([]any); len(f) != 0 {
		t.Errorf("不该有失败项：%v", f)
	}
	if w, _ := result["warnings"].([]any); len(w) != 0 {
		t.Errorf("forge 包的服务端文件已打好桩，不该有警告：%v", w)
	}
	// ⚠ Execute 的 Added/Replaced 把**模组和覆盖文件算在一起**（前端只按 path 展示，
	// 分两个计数反而要拼回去，见 executor.go）。这个包 = 1 个 required 模组 + 1 份 overrides
	// → added=2。别按「只数模组」的直觉写 1。
	//
	// 客户端专用的那个（mods/clientonly.jar）压根不进计划，所以它**既不是 added 也不是 skipped** ——
	// 它只是不存在。这条保证只能靠磁盘断言，计数看不出来。
	if result["added"] != float64(2) || result["replaced"] != float64(0) ||
		result["removed"] != float64(0) || result["skipped"] != float64(0) {
		t.Errorf("安装计数不对（该 2 新增 / 0 替换 / 0 删除 / 0 跳过）：%v", result)
	}

	inst, ok := env.mgr.Get(instID)
	if !ok {
		t.Fatal("安装完了实例却不在")
	}
	if inst.Type != daemon.TypeForge || inst.MCVersion != "1.20.1" || inst.LoaderVersion != "47.4.10" {
		t.Errorf("实例参数不对：type=%v mc=%v loader=%v", inst.Type, inst.MCVersion, inst.LoaderVersion)
	}
	if inst.Port != 25565 {
		t.Errorf("第一个实例该落在 25565（自动挑端口），实际 %d", inst.Port)
	}
	if inst.Name != mkPackName {
		t.Errorf("实例名该取包名，实际 %q", inst.Name)
	}
	if got := mpReadFile(t, inst.Dir, "mods/a.jar"); got != "forge-mod-a" {
		t.Errorf("模组没落盘：%q", got)
	}
	if got := mpReadFile(t, inst.Dir, "config/settings.toml"); got != "SETTINGS-FORGE" {
		t.Errorf("覆盖文件没落盘（server-overrides 前缀没剥掉）：%q", got)
	}
	// 客户端专用模组一个字节都不该落到服务端目录 —— 这是整条链路最该保住的东西。
	if _, err := os.Stat(filepath.Join(inst.Dir, "mods", "clientonly.jar")); !os.IsNotExist(err) {
		t.Errorf("客户端专用模组被装进服务端了（err=%v）—— 静默失败又回来了", err)
	}

	st, err := modpack.LoadState(inst.Dir)
	if err != nil {
		t.Fatalf("packstate 没写：%v", err)
	}
	if st.ProjectID != mkProject || st.VersionNumber != "2.4" {
		t.Errorf("packstate 身份不对：project=%v ver=%v", st.ProjectID, st.VersionNumber)
	}
	if st.InstalledAt == "" {
		t.Error("首次安装必须写 InstalledAt")
	}
	if st.UpdatedAt != "" {
		t.Errorf("首次安装不该写 UpdatedAt（那是「上次升级」，两者混了界面上会显示假记录）：%q", st.UpdatedAt)
	}

	// 服务端文件那一步：真的被调了，而且带上了 Forge 的 tag
	// （不带 tag 会退回「这个版本的推荐构建」，可能装成另一个版本）。
	n, req := env.jarSnapshot()
	if n != 1 {
		t.Fatalf("服务端文件该起 1 次，实际 %d 次", n)
	}
	if req.Type != "forge" || req.Version != "1.20.1" || req.Tag != "47.4.10" {
		t.Errorf("服务端文件请求不对：%+v", req)
	}

	// 取件码用过就作废
	code2, raw2 := mpPost(t, env.panel.URL+"/api/modpacks/install", map[string]any{"token": token})
	if code2 != 400 {
		t.Errorf("同一个取件码第二次用该回 400，实际 %d：%s", code2, raw2)
	}
	if n := len(env.mgr.List()); n != 1 {
		t.Errorf("第二次安装不该再建实例，实际共 %d 个", n)
	}
}

// 第二个实例要拿到下一个端口、且名字不撞 —— 一键安装必须自己把这两件事办了，
// 否则两个实例都落在 25565，而用户既没选过端口也不知道去哪改。
func TestModpackInstallSecondGetsFreePortAndUniqueName(t *testing.T) {
	env := newMarketEnv(t)
	installOnce := func() daemon.Instance {
		t.Helper()
		_, raw := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkForgeVer})
		token, _ := mpDecode(t, raw)["token"].(string)
		code, raw := mpPost(t, env.panel.URL+"/api/modpacks/install", map[string]any{"token": token})
		if code != 202 {
			t.Fatalf("安装返回 %d：%s", code, raw)
		}
		instID, _ := mpDecode(t, raw)["instanceId"].(string)
		opID, _ := mpDecode(t, raw)["opId"].(string)
		if _, snap := waitForOp(t, env.panel.URL, opID); snap["state"] != opDone {
			t.Fatalf("第二次安装没跑完：%v", snap["err"])
		}
		inst, ok := env.mgr.Get(instID)
		if !ok {
			t.Fatal("实例不在")
		}
		return inst
	}
	first := installOnce()
	second := installOnce()

	if first.Port != 25565 || second.Port != 25566 {
		t.Errorf("端口该自动顺延：%d / %d", first.Port, second.Port)
	}
	if first.Name != mkPackName {
		t.Errorf("第一个该用包名，实际 %q", first.Name)
	}
	if second.Name != mkPackName+" 2" {
		t.Errorf("重名该自动加序号，实际 %q", second.Name)
	}
}

// 「不能自动准备服务端文件」时那句话，是我们自己拼的：模板 + 源站的 Note。
// ⚠ Note 本身是整句、自带句号，模板又补一个 —— 结果就是「…… 参照。。模组会照样装好」。
// 断言查不出这种东西（没有测试会去数句号），只有把响应正文打出来看才发现。
// 这条测试把「任何源、任何类型」都过一遍，拼出来的话里不许出现重复标点。
func TestJarNoteNeverDoublesPunctuation(t *testing.T) {
	for _, src := range fetch.Sources() {
		note := string(src.Type)
		if got := jarNoteFor(note); got != "" {
			for _, bad := range []string{"。。", "..", "，，", "  "} {
				if strings.Contains(got, bad) {
					t.Errorf("%s 的 jarNote 里有重复标点 %q：%s", src.Type, bad, got)
				}
			}
		}
	}
	// 直接钉住 NeoForge 这条（它走的就是 Note 那条分支，最容易再犯）
	if got := jarNoteFor("neoforge"); got != "" {
		if strings.Contains(got, "。。") {
			t.Errorf("neoforge 的 jarNote 双句号又回来了：%s", got)
		}
		if !strings.HasSuffix(got, "就能启动。") {
			t.Errorf("neoforge 的 jarNote 结尾不对：%s", got)
		}
	}
	// 认不出的类型走的是另一条兜底文案，也不能出现没填上的 %s（连续两个空格是它的指纹）
	if got := jarNoteFor("something-weird"); strings.Contains(got, "  ") {
		t.Errorf("兜底 jarNote 有没填上的占位符：%s", got)
	}
}

// ── 不依赖网络的纯函数 ─────────────────────────────────────

// 「能不能自动准备服务端文件」只有一张判据表（jarResolvers）。
// 另立一份支持列表的话，两份迟早不一致 —— 本项目在这上面栽过。
func TestCanAutoPrepareJarTracksResolverTable(t *testing.T) {
	cases := map[string]bool{
		"forge":    true,
		"fabric":   true,
		"vanilla":  true,
		"paper":    true,
		"neoforge": false, // 表里没有 → 不能自动准备（官方 maven 本机只解析出 IPv6）
		"":         false, // 认不出的加载器
		"quilt":    false,
	}
	for typ, want := range cases {
		if got := canAutoPrepareJar(typ); got != want {
			t.Errorf("canAutoPrepareJar(%q) = %v，期望 %v", typ, got, want)
		}
	}
	// 反过来核对：表里每个能自动准备的类型，都不该给出 jarNote
	for kind := range jarResolvers {
		if note := jarNoteFor(string(kind)); note != "" {
			t.Errorf("%s 能自动准备，不该有 jarNote：%s", kind, note)
		}
	}
}

// 拒绝文案必须点名加载器、且给下一步 —— 一句「不支持」等于没说。
func TestModpackRefusalNamesLoaderAndNextSteps(t *testing.T) {
	msg := modpackRefusal("quilt-loader")
	if !strings.Contains(msg, "quilt-loader") {
		t.Errorf("要点名是哪个加载器：%s", msg)
	}
	if !strings.Contains(msg, "导入实例") || !strings.Contains(msg, "pack 参数") {
		t.Errorf("要给能走通的路：%s", msg)
	}
	// 空加载器也得说得出话，且不能出现没填上的 %s 指纹（连续两个空格）
	if msg2 := modpackRefusal(""); strings.Contains(msg2, "  ") {
		t.Errorf("空加载器的文案里有没填上的占位符：%s", msg2)
	}
}

// 取件码表：过期与用过都必须取不到，且**收掉临时目录**（那几十 MB 不能一直躺在 %TEMP%）。
func TestPackCacheClaimOnceAndExpiring(t *testing.T) {
	c := newPackCache()
	tmp := t.TempDir()
	fresh := &cachedPack{token: "pk_a", tmpDir: tmp, cachedAt: time.Now()}
	c.put(fresh)
	if got := c.claim("pk_a"); got == nil {
		t.Fatal("刚放进去的取件码该能取到")
	}
	if got := c.claim("pk_a"); got != nil {
		t.Error("取件码只能取一次")
	}

	old := &cachedPack{token: "pk_b", tmpDir: t.TempDir(), cachedAt: time.Now().Add(-2 * packTokenTTL)}
	c.put(old) // put 顺手清过期的
	if got := c.claim("pk_b"); got != nil {
		t.Error("过期的取件码不该能取到")
	}
}

// 本地包不许被删：用户指着自己的文件，release 一个字节都不能动。
func TestCachedPackReleaseKeepsUserFile(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "user.mrpack")
	if err := os.WriteFile(local, []byte("user's own pack"), 0o644); err != nil {
		t.Fatal(err)
	}
	// tmpDir 为空 = 用户本地文件（不是我们下到 %TEMP% 的那份）
	cp := &cachedPack{packPath: local, tmpDir: ""}
	cp.release()

	if _, err := os.Stat(local); err != nil {
		t.Errorf("release 把用户自己的文件动了：%v", err)
	}
	if cp.tmpDir != "" {
		t.Error("本地包没有临时目录，release 不该凭空造一个")
	}

	// 反过来：真的是我们下的临时包，release 必须把目录收干净
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "sbx-pack-1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	down := &cachedPack{packPath: filepath.Join(sub, "pack.mrpack"), tmpDir: sub}
	down.release()
	if _, err := os.Stat(sub); !os.IsNotExist(err) {
		t.Errorf("临时包 release 后目录该没了，实际 err=%v", err)
	}
}

// ── 作业列表 ──────────────────────────────────────────────

// 作业列表接口存在的理由：整合包的安装/升级是**后端在跑**的长任务，和「用户此刻停在哪一页」无关。
// 只有单条 /ops/{id} 的话，刷新一下页面那笔作业就变成不可见的了 —— 它还在装，
// 界面却是「什么都没发生」，用户会再点一次安装。
func TestModpackOpListAlwaysArrayAndCarriesIdentity(t *testing.T) {
	env := newMarketEnv(t)

	// 空表必须是 `[]` 而不是 null：前端直接读 .length，null 会让它当场崩。
	// 这类错在 Go 这边只是 nil slice，序列化出来却是 null —— 只能查线上原始字节。
	code, raw := mpGet(t, env.panel.URL+"/api/jobs")
	if code != 200 {
		t.Fatalf("作业列表返回 %d：%s", code, raw)
	}
	items, ok := mpDecode(t, raw)["items"].([]any)
	if !ok {
		t.Fatalf("items 该是数组，实际 %s", raw)
	}
	if len(items) != 0 {
		t.Fatalf("还没起过作业，列表该是空的，实际 %d 条", len(items))
	}

	// 起一笔安装
	_, rawInsp := mpPost(t, env.panel.URL+"/api/modpacks/inspect", map[string]any{"version": mkForgeVer})
	token, _ := mpDecode(t, rawInsp)["token"].(string)
	if token == "" {
		t.Fatal("预览没给取件码")
	}
	code, rawInst := mpPost(t, env.panel.URL+"/api/modpacks/install", map[string]any{"token": token})
	if code != 202 {
		t.Fatalf("安装该回 202，实际 %d：%s", code, rawInst)
	}
	res := mpDecode(t, rawInst)
	opID, _ := res["opId"].(string)
	instID, _ := res["instanceId"].(string)

	code, raw = mpGet(t, env.panel.URL+"/api/jobs")
	if code != 200 {
		t.Fatalf("作业列表返回 %d：%s", code, raw)
	}
	// 列表里每一笔都要带 id 与 startedAt：少了 id，界面点不动它；少了 startedAt，
	// 「跑了多久」只能显示成空白 —— 而用户正是拿这个判断"是不是卡住了"。
	var found map[string]any
	for _, it := range mpDecode(t, raw)["items"].([]any) {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		if m["id"] == "" || m["id"] == nil {
			t.Errorf("列表里的作业缺 id：%v", m)
		}
		if m["startedAt"] == "" || m["startedAt"] == nil {
			t.Errorf("列表里的作业缺 startedAt：%v", m)
		}
		if m["id"] == opID {
			found = m
		}
	}
	if found == nil {
		t.Fatalf("刚起的作业 %s 没出现在列表里：%s", opID, raw)
	}
	if found["kind"] != "install" {
		t.Errorf("列表里也该带 kind=install，实际 %v", found["kind"])
	}
	if found["owner"] != instID {
		t.Errorf("列表项要能指回实例（作业快照里叫 owner）：%v vs %v", found["owner"], instID)
	}

	// 列表同样是给前端直接吃的：一条 null 就能让那一栏崩掉。
	assertNoNullInRaw(t, raw)

	// 等这笔跑完，列表里还应该看得到它（终态作业保留 24 小时）——
	// 「记录」这一栏的用处就在这儿，跑完就消失的话它只是另一份进度条。
	_, _ = waitForOp(t, env.panel.URL, opID)
	code, raw = mpGet(t, env.panel.URL+"/api/jobs")
	if code != 200 {
		t.Fatalf("终态后列表返回 %d：%s", code, raw)
	}
	after := mpDecode(t, raw)["items"].([]any)
	hitTerminal := false
	for _, it := range after {
		m, _ := it.(map[string]any)
		if m != nil && m["id"] == opID && m["state"] == opDone {
			hitTerminal = true
		}
	}
	if !hitTerminal {
		t.Errorf("跑完的作业该留在列表里当记录，实际：%s", raw)
	}
	assertNoNullInRaw(t, raw)
}

// 从下载直链里抠 project id —— 「装前核对客户端模组」全靠它把文件对回项目。
// ⚠ 抠错了会去问一个不存在的项目，结果是「核对不出来」而不是报错，很难发现。
func TestModrinthProjectID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://cdn.modrinth.com/data/AANobbMI/versions/abc123/sodium.jar", "AANobbMI"},
		{"https://cdn.modrinth.com/data/P7dR8mSH/versions/x/fabric-api-0.92.jar", "P7dR8mSH"},
		// 非 Modrinth 源（作者自建 / GitHub）→ 认不出就空串，调用方跳过，不当错误
		{"https://github.com/foo/bar/releases/download/v1/mod.jar", ""},
		{"https://example.com/mods/a.jar", ""},
		{"", ""},
		// 只有前缀、后面没斜杠 → 认不出
		{"https://cdn.modrinth.com/data/", ""},
	}
	for _, c := range cases {
		if got := modrinthProjectID(c.in); got != c.want {
			t.Errorf("modrinthProjectID(%q) = %q，想要 %q", c.in, got, c.want)
		}
	}
}

// 核对不上（没有 Modrinth 直链可问、或离线）时必须**安静地返回空**，不能报错 ——
// inspect 是主线功能，不能因为「核不出客户端模组」就整个失败；也不能把已经在
// 安装时会跳过的 server:unsupported 文件重复算进疑名单（那是重复告警）。
func TestSuspectClientFilesSkipsNonModrinthAndSkipped(t *testing.T) {
	ix := &modpack.Index{Files: []modpack.File{
		{Path: "mods/a.jar", Downloads: []string{"https://example.com/a.jar"}},
		{Path: "mods/b.jar", Downloads: []string{"https://github.com/x/y/releases/b.jar"}},
		// 标了 server:unsupported → 安装时会跳过，不该再进疑名单
		{Path: "mods/c.jar", Env: modpack.Env{Server: modpack.SideUnsupported},
			Downloads: []string{"https://cdn.modrinth.com/data/AAAAAAAA/versions/v/c.jar"}},
	}}
	if got := suspectClientFiles("https://api.modrinth.com/v2", ix); len(got) != 0 {
		t.Errorf("没有可核对的 Modrinth 直链时该返回空，实际 %+v", got)
	}
}
