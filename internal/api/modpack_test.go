package api

// 整合包升级链路的接口级测试。
//
// 造假方式：httptest 起一台假 Modrinth（/version/{id}、/project/{pid}/version）
// 和一台假 CDN（/cdn/…），.mrpack 照 modpack 包测试的同款口径现造 ——
// 索引里的 sha512 按**真实内容**算出来，这样 FetchStandalone 的哈希校验
// 在测试里也是真的在拦，而不是摆设。
//
// ⚠ fetch.MarketFallbacks 必须钉成 nil：不钉的话，版本 404 之后的兜底会
// 真的打到 api.modrinth.com，测试结果取决于当时网络通不通 —— 薛定谔的测试。

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/jobs"
	"github.com/su-ni/EnderBox/internal/modpack"
	"github.com/su-ni/EnderBox/internal/settings"
)

// ── 假数据 ────────────────────────────────────────────────

const (
	mpVerID     = "abcd1234" // 8 位版本 id（形态对齐 Modrinth）
	mpVerBadID  = "badbad11" // 纯客户端整合包的版本 id
	mpVerNumber = "2.4"
	mpProjectID = "projZZZ9"

	mpKeepV1     = "keep-content-v1"   // 新旧一致 → keep
	mpChangeV1   = "change-content-v1" // 旧的一份
	mpChangeV2   = "change-content-v2" // 新包换的内容 → replace
	mpGoneV1     = "gone-content-v1"   // 新包里没有 → remove
	mpAddedV1    = "added-content-v1"  // 新包里才有 → add
	mpOverrideV2 = "SETTINGS-V2"       // 覆盖文件的新内容
)

type fakePackFile struct {
	path    string
	content string
	server  modpack.Side
}

type fakePack struct {
	versionID string
	name      string
	files     []fakePackFile
	overrides map[string]string // zip 条目名（overrides/...）→ 内容
	// deps 覆盖索引里的 dependencies。为空时用默认那套（1.20.1 + forge 47.4.10）——
	// P1 的市场测试要造 fabric / neoforge / quilt 的包，靠它把加载器换掉。
	deps map[string]string
}

// buildFakePack 造一份 .mrpack。api 包用不了 modpack 包测试里的 makePack，
// 这里照同一套口径造：哈希按内容实算，downloads 指向假 CDN。
func buildFakePack(t *testing.T, marketURL string, p fakePack) []byte {
	t.Helper()
	idxFiles := make([]map[string]any, 0, len(p.files))
	for _, f := range p.files {
		idxFiles = append(idxFiles, map[string]any{
			"path":      f.path,
			"hashes":    map[string]string{"sha512": sha512hex(f.content)},
			"env":       map[string]string{"client": string(modpack.SideRequired), "server": string(f.server)},
			"downloads": []string{marketURL + "/cdn/" + f.path},
			"fileSize":  len(f.content),
		})
	}
	deps := p.deps
	if deps == nil {
		deps = map[string]string{"minecraft": "1.20.1", "forge": "47.4.10"}
	}
	index := map[string]any{
		"game":          "minecraft",
		"formatVersion": 1,
		"versionId":     p.versionID,
		"name":          p.name,
		"dependencies":  deps,
		"files":         idxFiles,
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("modrinth.index.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(w).Encode(index); err != nil {
		t.Fatal(err)
	}
	for name, content := range p.overrides {
		ow, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(ow, content); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ── 假环境 ────────────────────────────────────────────────

type modpackEnv struct {
	panel    *httptest.Server
	market   *httptest.Server
	s        *Server
	inst     daemon.Instance // 装过 2.3 的主实例（有 packstate，ProjectID=projZZZ9）
	fresh    daemon.Instance // 没装过包的实例
	packPath string          // 主场景那份 .mrpack 落成磁盘文件（?pack= 用）
}

// newModpackEnv 起一台接假 Modrinth / 假 CDN 的面板（只挂整合包那组路由，不过鉴权）。
// cdnDelay 给假 CDN 每个响应加的延迟 —— 取消测试靠它把「下载做到一半」的窗口撑大，
// 让取消必能赶在 Execute 收尾之前。
func newModpackEnv(t *testing.T, cdnDelay time.Duration) *modpackEnv {
	t.Helper()
	oldFB := fetch.MarketFallbacks
	fetch.MarketFallbacks = nil
	t.Cleanup(func() { fetch.MarketFallbacks = oldFB })

	// 三个 map 都是先建好、起服务、再灌数据 —— handler 闭包引用它们，
	// httptest 服务在灌数据之后被请求时才读得到。
	content := map[string][]byte{}         // 假 CDN：路径 → 内容
	versions := map[string][]byte{}        // 假 Modrinth：版本 id → 单版本 JSON
	projectVersions := map[string][]byte{} // 项目 id → 版本列表 JSON

	mux := http.NewServeMux()
	mux.HandleFunc("GET /version/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, ok := versions[r.PathValue("id")]
		if !ok {
			http.NotFound(w, r) // 与真源站同款 404
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
		if cdnDelay > 0 {
			time.Sleep(cdnDelay)
		}
		b, ok := content[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	})
	market := httptest.NewServer(mux)

	// 主场景：keep 不动 / change 换 / added 新增 / gone 该删 / clientmod 是客户端专属 /
	// 一份覆盖文件新增。装完这些数字就是下面所有断言的基准。
	main := fakePack{
		versionID: "v24idx",
		name:      "Zombie Invade 100 Days",
		files: []fakePackFile{
			{path: "mods/keep.jar", content: mpKeepV1, server: modpack.SideRequired},
			{path: "mods/change.jar", content: mpChangeV2, server: modpack.SideRequired},
			{path: "mods/added.jar", content: mpAddedV1, server: modpack.SideRequired},
			{path: "mods/clientmod.jar", content: "client-only-stuff", server: modpack.SideUnsupported},
		},
		overrides: map[string]string{"overrides/config/settings.toml": mpOverrideV2},
	}
	packZip := buildFakePack(t, market.URL, main)
	for _, f := range main.files {
		content["/cdn/"+f.path] = []byte(f.content)
	}
	content["/cdn/pack-2.4.mrpack"] = packZip

	// 纯客户端整合包：一个文件都是 server=unsupported、没有覆盖 → 必须被拒
	badZip := buildFakePack(t, market.URL, fakePack{
		versionID: "vBadIdx",
		name:      "Fabulously Optimized",
		files:     []fakePackFile{{path: "mods/sodium.jar", content: "client", server: modpack.SideUnsupported}},
	})
	content["/cdn/client-pack.mrpack"] = badZip

	versionJSON := func(id, mrpackName string) map[string]any {
		return map[string]any{
			"id": id, "project_id": mpProjectID,
			"name": "2.4", "version_number": mpVerNumber, "version_type": "release",
			"files": []map[string]any{{
				"url":      market.URL + "/cdn/" + mrpackName,
				"filename": mrpackName,
				"primary":  true,
				"size":     len(content["/cdn/"+mrpackName]),
				"hashes":   map[string]string{"sha1": sha1hex(content["/cdn/"+mrpackName])},
			}},
		}
	}
	versions[mpVerID] = toJSON(t, versionJSON(mpVerID, "pack-2.4.mrpack"))
	versions[mpVerBadID] = toJSON(t, versionJSON(mpVerBadID, "client-pack.mrpack"))
	projectVersions[mpProjectID] = toJSON(t, []any{versionJSON(mpVerID, "pack-2.4.mrpack")})

	// 面板本体
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
	env := &modpackEnv{market: market}
	env.inst = createInstance(t, mgr, "zombie")
	env.fresh = createInstance(t, mgr, "vanilla-fresh")

	// 主实例：磁盘上是 2.3 那一代的文件，packstate 记着真实 sha512
	writeInstanceFile(t, env.inst.Dir, "mods/keep.jar", mpKeepV1)
	writeInstanceFile(t, env.inst.Dir, "mods/change.jar", mpChangeV1)
	writeInstanceFile(t, env.inst.Dir, "mods/gone.jar", mpGoneV1)
	if err := modpack.SaveState(env.inst.Dir, &modpack.State{
		Format:        "mrpack",
		ProjectID:     mpProjectID,
		Slug:          "zombie-invade-100-days",
		VersionID:     "oldver001",
		VersionNumber: "2.3",
		Name:          "Zombie Invade 100 Days",
		Minecraft:     "1.20.1",
		Loader:        map[string]string{"forge": "47.4.10"},
		Files: []modpack.FileRef{
			{Path: "mods/keep.jar", SHA: sha512hex(mpKeepV1), SHAKind: "sha512", Side: modpack.SideRequired},
			{Path: "mods/change.jar", SHA: sha512hex(mpChangeV1), SHAKind: "sha512", Side: modpack.SideRequired},
			{Path: "mods/gone.jar", SHA: sha512hex(mpGoneV1), SHAKind: "sha512", Side: modpack.SideRequired},
		},
		Overrides: []modpack.OverrideRef{},
	}); err != nil {
		t.Fatal(err)
	}

	// 本地包场景用：同一份 .mrpack 落成磁盘文件
	env.packPath = filepath.Join(data, "pack-local.mrpack")
	if err := os.WriteFile(env.packPath, packZip, 0o644); err != nil {
		t.Fatal(err)
	}

	env.s = New(mgr, set, "127.0.0.1:0")
	pmux := http.NewServeMux()
	env.s.instanceModpackRoutes(pmux)
	// 作业端点单独一组：把这几条漏掉的话，轮询 /api/jobs 只会拿到 mux 原生的 404，
	// 报「作业不存在」那样的人话却拿不到 —— 测试会误判成产品代码的问题。
	env.s.jobRoutes(pmux)
	env.panel = httptest.NewServer(pmux)
	t.Cleanup(env.panel.Close)
	t.Cleanup(market.Close)
	return env
}

func createInstance(t *testing.T, mgr *daemon.Manager, name string) daemon.Instance {
	t.Helper()
	inst := daemon.Instance{Name: name, Type: daemon.TypePaper, MCVersion: "1.20.1"}
	if err := mgr.Create(&inst); err != nil {
		t.Fatal(err)
	}
	return inst
}

// ── 小工具 ────────────────────────────────────────────────

func sha512hex(s string) string {
	sum := sha512.Sum512([]byte(s))
	return hex.EncodeToString(sum[:])
}

func sha1hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

func toJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeInstanceFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mpReadFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("读 %s 失败：%v", rel, err)
	}
	return string(b)
}

func mpGet(t *testing.T, rawURL string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func mpPost(t *testing.T, rawURL string, body any) (int, []byte) {
	t.Helper()
	resp, err := http.Post(rawURL, "application/json", bytes.NewReader(toJSON(t, body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func mpDecode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("响应不是 JSON 对象：%v\n%s", err, raw)
	}
	return m
}

func summaryOf(t *testing.T, m map[string]any) map[string]int {
	t.Helper()
	raw, ok := m["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary 缺失或形态不对：%v", m["summary"])
	}
	out := map[string]int{}
	for k, v := range raw {
		f, isNum := v.(float64)
		if !isNum {
			t.Fatalf("summary.%s 不是数字：%v", k, v)
		}
		out[k] = int(f)
	}
	return out
}

// assertArrayField 集合字段必须是数组（[]），不是 null —— null.length 前端会当场炸。
// 判据放在反序列化之后：Go 里 nil slice 和空 slice 分不出，过一遍 JSON 才见真章。
func assertArrayField(t *testing.T, m map[string]any, keys ...string) {
	t.Helper()
	for _, key := range keys {
		v, ok := m[key]
		if !ok {
			t.Errorf("响应里没有 %s 字段（整字段缺失比 null 还糟）", key)
			continue
		}
		if _, isArr := v.([]any); !isArr {
			t.Errorf("%s 该是数组（[]），实际是 %T：%v", key, v, v)
		}
	}
}

// assertNoNullInRaw 整份响应里不许出现 null —— 这些接口的每个集合字段都保证给 []。
func assertNoNullInRaw(t *testing.T, raw []byte) {
	t.Helper()
	if strings.Contains(string(raw), "null") {
		t.Errorf("响应里出现了 null —— 前端 .length 会当场炸：%s", raw)
	}
}

// waitForOp 轮询作业直到离开 running（或超时）。返回最后一拍的原文与解码。
func waitForOp(t *testing.T, panelURL, opID string) (string, map[string]any) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last []byte
	for time.Now().Before(deadline) {
		code, raw := mpGet(t, panelURL+"/api/jobs/"+opID)
		if code != 200 {
			t.Fatalf("轮询作业返回 %d：%s", code, raw)
		}
		last = raw
		m := mpDecode(t, raw)
		if m["state"] != opRunning {
			return string(raw), m
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("作业 30 秒还没到终态，最后一拍：%s", last)
	return "", nil
}

// ── 只算不装（GET 预览）──────────────────────────────────

// ?version= 全链路：假源站 → 下载 .mrpack → 解析 → 计划 → 指纹。
// 顺带验哈希的两条性质：同输入两次逐字节一致（POST 能对上的前提）、
// 内容变了哈希必须变（不然拦不住「批准 A 执行 B」）。
func TestModpackPreviewByVersionFullChain(t *testing.T) {
	env := newModpackEnv(t, 0)
	previewURL := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade?version=" + mpVerID

	code, raw := mpGet(t, previewURL)
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	assertNoNullInRaw(t, raw)
	m := mpDecode(t, raw)
	assertArrayField(t, m, "toAdd", "toReplace", "toRemove", "skipped")

	sum := summaryOf(t, m)
	want := map[string]int{"add": 2, "replace": 1, "remove": 1, "keep": 1, "skip": 0}
	// add=2：mods/added.jar + 覆盖文件 settings.toml；clientmod 是客户端专属，不进任何清单
	for k, v := range want {
		if sum[k] != v {
			t.Errorf("summary.%s = %d，期望 %d（全表：%v）", k, sum[k], v, sum)
		}
	}
	if m["ok"] != true || m["refusal"] != "" {
		t.Errorf("不该被拒：ok=%v refusal=%v", m["ok"], m["refusal"])
	}
	hash, _ := m["planHash"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hash) {
		t.Fatalf("planHash 该是 64 位小写十六进制，实际 %q", hash)
	}
	target, _ := m["target"].(map[string]any)
	if target["versionNumber"] != mpVerNumber {
		t.Errorf("target.versionNumber = %v，期望 %s（市场版本的 version_number）", target["versionNumber"], mpVerNumber)
	}
	if target["versionId"] != "v24idx" {
		t.Errorf("target.versionId = %v，期望包索引里的 v24idx", target["versionId"])
	}
	if target["minecraft"] != "1.20.1" {
		t.Errorf("target.minecraft = %v", target["minecraft"])
	}

	// 稳定：同输入两次，哈希必须逐字节一致 —— 这是 POST 能对上指纹的前提
	code2, raw2 := mpGet(t, previewURL)
	if code2 != 200 {
		t.Fatalf("第二次预览返回 %d", code2)
	}
	m2 := mpDecode(t, raw2)
	if m2["planHash"] != m["planHash"] {
		t.Errorf("同输入两次哈希不一致：\n%s\n%s", m["planHash"], m2["planHash"])
	}

	// 敏感：磁盘上少一个该删的文件 → 计划少一条 remove → 哈希必须变
	if err := os.Remove(filepath.Join(env.inst.Dir, "mods", "gone.jar")); err != nil {
		t.Fatal(err)
	}
	code3, raw3 := mpGet(t, previewURL)
	if code3 != 200 {
		t.Fatalf("删文件后预览返回 %d", code3)
	}
	assertNoNullInRaw(t, raw3)
	m3 := mpDecode(t, raw3)
	assertArrayField(t, m3, "toRemove")
	if m3["planHash"] == m["planHash"] {
		t.Error("计划内容变了（少一条 remove）哈希却没变 —— 这个指纹拦不住「批准 A 执行 B」")
	}
	if s := summaryOf(t, m3); s["remove"] != 0 {
		t.Errorf("文件已不在磁盘，不该再列删除：remove=%d", s["remove"])
	}
}

// 用户抄下来的是版本号（2.4）而不是 8 位 id：/version/2.4 → 404，
// 然后靠 packstate 里的 ProjectID 在版本列表里按 version_number 反查。
func TestModpackPreviewByVersionNumberFallback(t *testing.T) {
	env := newModpackEnv(t, 0)
	code, raw := mpGet(t, env.panel.URL+"/api/instances/"+env.inst.ID+"/modpack/upgrade?version="+mpVerNumber)
	if code != 200 {
		t.Fatalf("按版本号反查该走通，返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["ok"] != true || m["refusal"] != "" {
		t.Errorf("反查成功却带了拒绝信息：ok=%v refusal=%v", m["ok"], m["refusal"])
	}
	if m["planHash"] == "" {
		t.Error("反查路径也必须给 planHash")
	}
}

// fresh 实例没装过市场里的包：版本号反查没有地基，400 要把三条路说全。
func TestModpackPreviewVersionUnknownOnFreshInstance(t *testing.T) {
	env := newModpackEnv(t, 0)
	code, raw := mpGet(t, env.panel.URL+"/api/instances/"+env.fresh.ID+"/modpack/upgrade?version=9.9")
	if code != 400 {
		t.Fatalf("返回 %d，期望 400：%s", code, raw)
	}
	for _, want := range []string{"9.9", "版本 id", "pack"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("错误文案该提到 %q（三条路要说全）：%s", want, raw)
		}
	}
}

// ?pack= 本地包预览：不打市场、不下包，直接解析。
func TestModpackPreviewLocalPack(t *testing.T) {
	env := newModpackEnv(t, 0)
	code, raw := mpGet(t, env.panel.URL+"/api/instances/"+env.inst.ID+
		"/modpack/upgrade?pack="+url.QueryEscape(env.packPath))
	if code != 200 {
		t.Fatalf("本地包预览返回 %d：%s", code, raw)
	}
	assertNoNullInRaw(t, raw)
	m := mpDecode(t, raw)
	assertArrayField(t, m, "toAdd", "toReplace", "toRemove", "skipped")
	sum := summaryOf(t, m)
	if sum["add"] != 2 || sum["replace"] != 1 || sum["remove"] != 1 {
		t.Errorf("计数不对：%v（期望 add2 replace1 remove1）", sum)
	}
	// 本地包没有市场信息：版本号退回索引里的 name —— 有什么说什么
	target, _ := m["target"].(map[string]any)
	if target["versionNumber"] != "Zombie Invade 100 Days" {
		t.Errorf("本地包的 versionNumber 该是索引 name，实际 %v", target["versionNumber"])
	}
	if m["planHash"] == "" {
		t.Error("本地包预览也必须给 planHash（POST 全靠它）")
	}
}

// ── 真升级（POST 开始）──────────────────────────────────

func TestModpackStartValidations(t *testing.T) {
	env := newModpackEnv(t, 0)
	post := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade"

	// ① version / pack 都不带
	code, raw := mpPost(t, post, map[string]any{"planHash": "x"})
	if code != 400 || !strings.Contains(string(raw), "二选一") {
		t.Errorf("两个都不带该 400 且说清二选一：%d：%s", code, raw)
	}
	// ② 两个都带
	code, raw = mpPost(t, post, map[string]any{"version": mpVerID, "pack": env.packPath, "planHash": "x"})
	if code != 400 {
		t.Errorf("两个都带该 400：%d：%s", code, raw)
	}
	// ③ planHash 为空 —— 先预览再开始，没有预览就没有指纹可比
	code, raw = mpPost(t, post, map[string]any{"version": mpVerID})
	if code != 400 || !strings.Contains(string(raw), "预览") {
		t.Errorf("缺 planHash 该 400 且提示先预览：%d：%s", code, raw)
	}
	// ④ 版本不存在（这个实例装过市场包，但 9.9 反查不到）
	code, raw = mpPost(t, post, map[string]any{"version": "9.9", "planHash": "x"})
	if code != 400 || !strings.Contains(string(raw), "9.9") {
		t.Errorf("版本不存在该 400 并带上下文：%d：%s", code, raw)
	}
}

// 目标版本是纯客户端整合包：预览照常 200（把原因摊开），POST 必须拦死。
func TestModpackStartRefusesClientOnlyTarget(t *testing.T) {
	env := newModpackEnv(t, 0)
	base := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade"
	code, raw := mpGet(t, base+"?version="+mpVerBadID)
	if code != 200 {
		t.Fatalf("预览该把拒绝原因摊出来（200），实际 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	if m["ok"] != false || !strings.Contains(m["refusal"].(string), "客户端") {
		t.Fatalf("ok=%v refusal=%v —— 拒绝理由要提到客户端", m["ok"], m["refusal"])
	}
	code, raw = mpPost(t, base, map[string]any{"version": mpVerBadID, "planHash": m["planHash"]})
	if code != 400 || !strings.Contains(string(raw), "客户端") {
		t.Errorf("客户端包该被 400 拦下：%d：%s", code, raw)
	}
}

// 409 → confirmStale 全流程：预览之后磁盘被动过 → POST 带旧指纹必须 409；
// 用户看过新计划后再点一次（confirmStale=true）→ 按契约本意执行**重算出来的新计划**。
func TestModpackStartStaleThenConfirmStale(t *testing.T) {
	env := newModpackEnv(t, 0)
	base := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade"

	code, raw := mpGet(t, base+"?version="+mpVerID)
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	hash1 := mpDecode(t, raw)["planHash"]

	// 预览之后磁盘被动过（该删的 gone.jar 被用户自己删了）
	if err := os.Remove(filepath.Join(env.inst.Dir, "mods", "gone.jar")); err != nil {
		t.Fatal(err)
	}
	code, raw = mpPost(t, base, map[string]any{"version": mpVerID, "planHash": hash1})
	if code != 409 {
		t.Fatalf("计划变了却没拦（指纹校验被摘了？）：%d：%s", code, raw)
	}
	if !strings.Contains(string(raw), "重新预览") {
		t.Errorf("409 文案要告诉用户先重新预览：%s", raw)
	}
	// 前端靠 reason 码分流「下一步该干嘛」，不匹配文案 —— 码必须钉在契约里
	if m := mpDecode(t, raw); m["reason"] != "stale_plan" {
		t.Errorf("stale 409 的 reason 码不对：%v（原文：%s）", m["reason"], raw)
	}

	// 前端流程：409 之后重新预览，把新清单摊给用户看
	code, raw = mpGet(t, base+"?version="+mpVerID)
	if code != 200 {
		t.Fatalf("重预览返回 %d", code)
	}
	if m := mpDecode(t, raw); m["planHash"] == hash1 {
		t.Fatal("重预览的哈希和旧的一样 —— 后面无法验证「执行的是新计划」")
	}
	// 用户看过后点确认：confirmStale=true。故意还带旧指纹 ——
	// 证明放行靠的是 confirmStale，执行的是刚算出来的这份，不是指纹对上的那份。
	code, raw = mpPost(t, base, map[string]any{"version": mpVerID, "planHash": hash1, "confirmStale": true})
	if code != 202 {
		t.Fatalf("confirmStale 后该放行：%d：%s", code, raw)
	}
	opID, _ := mpDecode(t, raw)["opId"].(string)
	if !strings.HasPrefix(opID, "job_") {
		t.Fatalf("opId 该是统一作业表的 id（job_ 前缀），实际 %q", opID)
	}

	opRaw, op := waitForOp(t, env.panel.URL, opID)
	if op["state"] != opDone {
		t.Fatalf("state=%v err=%v 原文：%s", op["state"], op["err"], opRaw)
	}
	assertNoNullInRaw(t, []byte(opRaw))
	res, _ := op["result"].(map[string]any)
	assertArrayField(t, res, "skippedPaths", "failed")
	// 新计划里没有 remove（文件早被删了），所以 Removed 必须 0 ——
	// 这一条同时证明了「执行的是重算的新计划」
	if res["added"] != float64(2) || res["replaced"] != float64(1) || res["removed"] != float64(0) {
		t.Errorf("计数不对：added=%v replaced=%v removed=%v（期望 2/1/0：1 模组新增 + 1 覆盖新增）",
			res["added"], res["replaced"], res["removed"])
	}
	// 序列化之后验：空集合必须是 []，不是 null（Go 值层面分不出来）
	if !strings.Contains(opRaw, `"skippedPaths":[]`) || !strings.Contains(opRaw, `"failed":[]`) {
		t.Errorf("done 的 result 集合字段该序列化成 []：%s", opRaw)
	}

	// 磁盘现实：新增/变更/覆盖落了，该删的没了，保留的没动
	if got := mpReadFile(t, env.inst.Dir, "mods/added.jar"); got != mpAddedV1 {
		t.Errorf("added.jar 内容不对：%q", got)
	}
	if got := mpReadFile(t, env.inst.Dir, "mods/change.jar"); got != mpChangeV2 {
		t.Errorf("change.jar 没被覆盖：%q", got)
	}
	if got := mpReadFile(t, env.inst.Dir, "config/settings.toml"); got != mpOverrideV2 {
		t.Errorf("覆盖文件没解出来：%q", got)
	}
	if _, err := os.Stat(filepath.Join(env.inst.Dir, "mods", "gone.jar")); !os.IsNotExist(err) {
		t.Error("gone.jar 不该回来")
	}
	if got := mpReadFile(t, env.inst.Dir, "mods/keep.jar"); got != mpKeepV1 {
		t.Errorf("keep.jar 被动过了：%q", got)
	}

	// packstate 写回：版本号换成新版本，UpdatedAt 是这次升级的时刻
	st, err := modpack.LoadState(env.inst.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.VersionNumber != mpVerNumber {
		t.Errorf("packstate.VersionNumber = %q，期望 %s", st.VersionNumber, mpVerNumber)
	}
	if st.UpdatedAt == "" {
		t.Error("升过级之后 UpdatedAt 不能还是空")
	}
}

// 实例在跑 → 409。造一个「真的在跑」得拉真进程再过 preflight 的 java 探测，
// 把测试绑死在机器环境上；这里换掉探测函数本身 ——
// 闸的真实实现（instanceRunning）由多条路由共用、别处已验，这里只验 POST 必须过这道闸。
// 故意用一个不存在的版本：409 若发生在重算之前，回的就是 409 而不是 400，
// 同时锁住了契约要求的校验顺序。
func TestModpackStartBlockedWhileInstanceRunning(t *testing.T) {
	env := newModpackEnv(t, 0)
	orig := modpackRunning
	modpackRunning = func(*Server, string) bool { return true }
	defer func() { modpackRunning = orig }()

	post := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade"
	code, raw := mpPost(t, post, map[string]any{"version": "zzzzzzz", "planHash": "x"})
	if code != 409 {
		t.Fatalf("实例在跑时该 409：%d：%s", code, raw)
	}
	if !strings.Contains(string(raw), "停服") {
		t.Errorf("文案要告诉用户先停服：%s", raw)
	}
	if m := mpDecode(t, raw); m["reason"] != "instance_running" {
		t.Errorf("实例在跑 409 的 reason 码不对：%v（原文：%s）", m["reason"], raw)
	}
}

// 已经有一笔作业在跑时再点开始 → 409 op_running。这条 409 和「计划变了」是两回事：
// 前端绝不能把它当成 stale 去重预览 —— 那一路会收 opId，把跑着的作业从界面上抹掉。
// 所以 reason 码必须分开：本测试锁 op_running，TestModpackStartStaleThenConfirmStale 锁 stale_plan。
// 等第一笔作业到终态后再 POST 该放行（202）—— 证明闸是「跑着才挡」，不是一次性的。
func TestModpackStartBlockedWhileOpRunning(t *testing.T) {
	// CDN 每个响应睡 300ms：第一笔作业的第一份文件落盘之前，第二笔 POST 的重算
	//（走本地假源站，毫秒级）早就打完了 —— 磁盘没动、指纹对得上，409 才稳稳落在
	// op_running 这道闸上，不会抖成 stale_plan。
	env := newModpackEnv(t, 300*time.Millisecond)
	base := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade"

	code, raw := mpGet(t, base+"?version="+mpVerID)
	if code != 200 {
		t.Fatalf("预览返回 %d：%s", code, raw)
	}
	hash := mpDecode(t, raw)["planHash"]

	code, raw = mpPost(t, base, map[string]any{"version": mpVerID, "planHash": hash})
	if code != 202 {
		t.Fatalf("第一笔该 202：%d：%s", code, raw)
	}
	opID := mpDecode(t, raw)["opId"].(string)
	t.Cleanup(func() { _, _ = mpPost(t, env.panel.URL+"/api/jobs/"+opID+"/cancel", nil) })

	// 同指纹立刻再点一次开始：拦它的必须是「已有作业在跑」，不是指纹
	code, raw = mpPost(t, base, map[string]any{"version": mpVerID, "planHash": hash})
	if code != 409 {
		t.Fatalf("作业在跑时再开始该 409：%d：%s", code, raw)
	}
	if m := mpDecode(t, raw); m["reason"] != "op_running" {
		t.Errorf("作业在跑 409 的 reason 码不对：%v（原文：%s）", m["reason"], raw)
	}

	// 第一笔到终态（state=done 落在 packstate 写回**之后**，waitForOp 出来时磁盘已定型）
	// 后闸该松开：磁盘已变，旧指纹必然对不上，重预览拿新指纹再 POST —— 放行 202。
	opRaw, op := waitForOp(t, env.panel.URL, opID)
	if op["state"] != opDone {
		t.Fatalf("第一笔该跑成 done，实际 %v（err=%v）原文：%s", op["state"], op["err"], opRaw)
	}
	code, raw = mpGet(t, base+"?version="+mpVerID)
	if code != 200 {
		t.Fatalf("作业结束后重预览返回 %d：%s", code, raw)
	}
	hash2 := mpDecode(t, raw)["planHash"]
	code, raw = mpPost(t, base, map[string]any{"version": mpVerID, "planHash": hash2})
	if code != 202 {
		t.Fatalf("作业结束后再开始该 202：%d：%s", code, raw)
	}
	// 第二笔也要等到终态再走 —— 作业 goroutine 收尾时还要写 packstate，
	// 测试先退了的话 t.TempDir 的 RemoveAll 会跟它赛跑（Windows 上必炸）。
	opID2 := mpDecode(t, raw)["opId"].(string)
	waitForOp(t, env.panel.URL, opID2)
}

// ── 作业表 ────────────────────────────────────────────────

func TestModpackOpsLifecycle(t *testing.T) {
	// 100ms 的 CDN 延迟把「下载做到一半」的窗口撑大，取消必能赶上
	env := newModpackEnv(t, 100*time.Millisecond)

	// 未知作业：查 / 取消都 404
	code, _ := mpGet(t, env.panel.URL+"/api/jobs/mp_nope")
	if code != 404 {
		t.Errorf("未知作业该 404，实际 %d", code)
	}
	code, _ = mpPost(t, env.panel.URL+"/api/jobs/mp_nope/cancel", nil)
	if code != 409 {
		t.Errorf("取消未知作业该 409（统一端点里「不存在」与「已结束」同码 —— 取消键本就该消失，界面不用分流），实际 %d", code)
	}

	// 起一笔（curl 型流程：预览拿指纹 → POST）
	base := env.panel.URL + "/api/instances/" + env.inst.ID + "/modpack/upgrade"
	_, raw := mpGet(t, base+"?version="+mpVerID)
	hash := mpDecode(t, raw)["planHash"]
	code, raw = mpPost(t, base, map[string]any{"version": mpVerID, "planHash": hash})
	if code != 202 {
		t.Fatalf("开始升级该 202：%d：%s", code, raw)
	}
	opID, _ := mpDecode(t, raw)["opId"].(string)

	// 立刻取消。cancel 在入表前就挂好了，这一下必须生效
	code, raw = mpPost(t, env.panel.URL+"/api/jobs/"+opID+"/cancel", nil)
	if code != 200 {
		t.Fatalf("取消该 200：%d：%s", code, raw)
	}
	if m := mpDecode(t, raw); m["ok"] != true {
		t.Errorf("取消成功该回 {\"ok\":true}，实际 %s", raw)
	}

	opRaw, op := waitForOp(t, env.panel.URL, opID)
	if op["state"] != opCanceled {
		t.Fatalf("取消后该是 canceled，实际 %v（err=%v）原文：%s", op["state"], op["err"], opRaw)
	}
	assertNoNullInRaw(t, []byte(opRaw))
	res, _ := op["result"].(map[string]any)
	assertArrayField(t, res, "skippedPaths", "failed")
	prog, _ := op["progress"].(map[string]any)
	for _, k := range []string{"filesDone", "filesTotal", "bytesDone", "bytesTotal", "current"} {
		if _, ok := prog[k]; !ok {
			t.Errorf("progress 里缺 %s：%v", k, prog)
		}
	}

	// 对已结束的作业再取消：统一端点给 409 —— 取消键在作业跑完后本就该消失，
	// 让「还能点到」变成报错比静默忽略更诚实。
	code, raw = mpPost(t, env.panel.URL+"/api/jobs/"+opID+"/cancel", nil)
	if code != 409 {
		t.Errorf("取消已结束的作业该 409：%d：%s", code, raw)
	}
}

// fresh 实例从本地 .mrpack 全新安装：预览全是新增（「要删除的文件」为空是诚实行为），
// POST 装完之后 packstate 立起来 —— 从此这个实例的升级功能有了地基。
func TestModpackInstallFreshFromLocalPack(t *testing.T) {
	env := newModpackEnv(t, 0)
	base := env.panel.URL + "/api/instances/" + env.fresh.ID + "/modpack/upgrade"

	code, raw := mpGet(t, base+"?pack="+url.QueryEscape(env.packPath))
	if code != 200 {
		t.Fatalf("fresh 实例本地包预览返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	sum := summaryOf(t, m)
	// 3 个服务端模组 + 1 份覆盖文件全是新增；clientmod 不算
	if sum["add"] != 4 || sum["replace"] != 0 || sum["remove"] != 0 {
		t.Errorf("fresh 安装的计数不对：%v（期望 add4 其余 0）", sum)
	}
	if m["ok"] != true {
		t.Errorf("不该被拒：%v", m["refusal"])
	}

	code, raw = mpPost(t, base, map[string]any{"pack": env.packPath, "planHash": m["planHash"]})
	if code != 202 {
		t.Fatalf("开始安装该 202：%d：%s", code, raw)
	}
	opID := mpDecode(t, raw)["opId"].(string)
	opRaw, op := waitForOp(t, env.panel.URL, opID)
	if op["state"] != opDone {
		t.Fatalf("state=%v err=%v 原文：%s", op["state"], op["err"], opRaw)
	}
	res, _ := op["result"].(map[string]any)
	if res["added"] != float64(4) {
		t.Errorf("added=%v，期望 4", res["added"])
	}

	// 安装完成后身份立起来了（have=true），modpackInfo 该说得出来
	code, raw = mpGet(t, env.panel.URL+"/api/instances/"+env.fresh.ID+"/modpack")
	if code != 200 {
		t.Fatalf("安装后查身份返回 %d", code)
	}
	info := mpDecode(t, raw)
	if info["have"] != true {
		t.Errorf("装完之后 have 该是 true：%s", raw)
	}
	if info["versionNumber"] != "Zombie Invade 100 Days" {
		t.Errorf("versionNumber = %v", info["versionNumber"])
	}
	st, err := modpack.LoadState(env.fresh.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Files) != 3 {
		t.Errorf("packstate 该记 3 个服务端模组，实际 %d", len(st.Files))
	}
}

// 「同实例只准一笔 running 作业」的闸必须与入表在同一把锁里。
// 并发的过闸竞态没法确定性地复现 —— 这正是要把 check 和 insert 塞进同一把锁的原因；
// 锁结构由这条契约测试看守：running 挡同 owner、放别 owner、放终态后的重开。
//
// ⚠ 闸的实现已经从 modpack 专属的 registerOp 上移到通用作业表 internal/jobs，
// 所以这条测试直接对着 jobs.Store 验 —— 整合包升级/安装、备份、导出共用同一把闸，
// 任何一类作业从这个表上绕开，再写一版私有的闸，这条测试就守不住它了。
func TestModpackOpsOneRunningPerInstance(t *testing.T) {
	store := jobs.NewStore()

	first := store.New("A", "upgrade")
	if first == nil {
		t.Fatal("空表上第一笔作业应当能登记")
	}
	if store.New("A", "upgrade") != nil {
		t.Fatal("同实例已有一笔 running 作业，第二笔必须被闸住")
	}
	if store.New("B", "upgrade") == nil {
		t.Fatal("别的实例不该被同实例的闸挡住")
	}

	// 终态作业不挡新作业：A 那笔结束后，A 上要能再起一笔。
	store.Finish(first, jobs.StateCanceled, nil, "")
	if store.New("A", "upgrade") == nil {
		t.Fatal("上一笔已是终态，同实例的新作业应当放行")
	}
}

// 回归（真机验收抓到的 bug）：执行器给覆盖文件记账用的是落盘时自算的 sha1
// （包里对覆盖不给哈希），比对层 diskProbe 收集「该按哪种哈希问磁盘」时必须把
// old.Overrides / 新包覆盖也收进去。漏了会按默认 sha512 问磁盘，与 sha1 记录
// 永远对不上 —— 上一版就有的、一字未动的覆盖全被误判成「用户改过」跳过，
// 升级该覆盖的覆盖不了，而界面上看起来还挺「安全」。
func TestUpgradeDoesNotSkipPristineOverrides(t *testing.T) {
	env := newModpackEnv(t, 0)

	// v1：一份模组 + 两份覆盖（same 两版一致、changed 两版不同）
	v1 := fakePack{
		versionID: "ovr00001",
		name:      "覆盖回归",
		// 模组用假 CDN 已登记的 keep.jar（内容/哈希一致，两版相同 → 预期 keep）
		files: []fakePackFile{{path: "mods/keep.jar", content: mpKeepV1, server: modpack.SideRequired}},
		overrides: map[string]string{
			"overrides/config/same.toml":    "same-content",
			"overrides/config/changed.toml": "changed-content-v1",
		},
	}
	v2 := fakePack{
		versionID: "ovr00002",
		name:      "覆盖回归",
		// 模组用假 CDN 已登记的 keep.jar（内容/哈希一致，两版相同 → 预期 keep）
		files: []fakePackFile{{path: "mods/keep.jar", content: mpKeepV1, server: modpack.SideRequired}},
		overrides: map[string]string{
			"overrides/config/same.toml":    "same-content",
			"overrides/config/changed.toml": "changed-content-v2",
		},
	}
	pack1 := filepath.Join(t.TempDir(), "v1.mrpack")
	pack2 := filepath.Join(t.TempDir(), "v2.mrpack")
	if err := os.WriteFile(pack1, buildFakePack(t, env.market.URL, v1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pack2, buildFakePack(t, env.market.URL, v2), 0o644); err != nil {
		t.Fatal(err)
	}

	base := env.panel.URL + "/api/instances/" + env.fresh.ID + "/modpack/upgrade"

	// 真执行器装 v1 —— packstate 里覆盖记的是 sha1，不是测试手灌的
	code, raw := mpGet(t, base+"?pack="+url.QueryEscape(pack1))
	if code != 200 {
		t.Fatalf("v1 预览返回 %d：%s", code, raw)
	}
	m := mpDecode(t, raw)
	code, raw = mpPost(t, base, map[string]any{"pack": pack1, "planHash": m["planHash"]})
	if code != 202 {
		t.Fatalf("v1 安装该 202：%d：%s", code, raw)
	}
	if _, op := waitForOp(t, env.panel.URL, mpDecode(t, raw)["opId"].(string)); op["state"] != opDone {
		t.Fatalf("v1 安装没跑完：%v", op["state"])
	}

	// 预览升到 v2：same.toml 一字未动 → 不许进跳过清单；changed.toml → 必须进替换清单。
	// 期望摘要：replace 1 / keep 1（模组原样），add/remove/skip 全 0。
	code, raw = mpGet(t, base+"?pack="+url.QueryEscape(pack2))
	if code != 200 {
		t.Fatalf("v2 预览返回 %d：%s", code, raw)
	}
	m = mpDecode(t, raw)
	sum := summaryOf(t, m)
	if sum["replace"] != 1 || sum["skip"] != 0 || sum["keep"] != 2 || sum["add"] != 0 || sum["remove"] != 0 {
		t.Errorf("升级摘要不对：%v（期望 replace1 keep2[模组+same.toml] 其余 0）", sum)
	}
	if n := len(m["skipped"].([]any)); n != 0 {
		t.Errorf("没动过的覆盖不该被跳过，实际 %d 条：%s", n, raw)
	}
	found := false
	for _, c := range m["toReplace"].([]any) {
		if c.(map[string]any)["path"] == "overrides/config/changed.toml" {
			found = true
		}
	}
	if !found {
		t.Errorf("changed.toml 该进替换清单：%s", raw)
	}
}
