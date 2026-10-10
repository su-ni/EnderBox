package fetch

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 反查必须**原样保留 environment** —— 制作整合包靠它把模组分到客户端包 / 服务端包。
// 这条要是丢了，两个包会装进同一批模组，客户端那边直接崩给你看。
func TestVersionFilesByHashKeepsEnvironment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/version_files") {
			t.Errorf("打错了路径：%s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"aaaa1111": map[string]any{
				"id":             "ver1",
				"project_id":     "proj1",
				"version_number": "1.2.3",
				"environment":    "dedicated_server_only",
				"game_versions":  []string{"1.20.1"},
				"loaders":        []string{"fabric"},
				"files": []map[string]any{{
					"url": "https://cdn.example/x.jar", "filename": "x.jar",
					"size": 123, "primary": true,
					"hashes": map[string]string{"sha1": "aaaa1111", "sha512": "zz"},
				}},
			},
		})
	}))
	defer srv.Close()

	got, err := VersionFilesByHash(srv.URL, []string{"aaaa1111"})
	if err != nil {
		t.Fatal(err)
	}
	v, ok := got["aaaa1111"]
	if !ok {
		t.Fatal("没反查到 aaaa1111")
	}
	if v.Environment != "dedicated_server_only" {
		t.Errorf("environment 被改坏了：%q", v.Environment)
	}
	f, ok := v.PrimaryFile()
	if !ok || f.URL != "https://cdn.example/x.jar" || f.Size != 123 {
		t.Errorf("主文件取错了：%+v ok=%v", f, ok)
	}
}

// ⚠⚠ 查不到的哈希**不能出现在结果里**，更不能给个空壳值。
// 调用方是用「请求集 − 返回集」来判定「这是本地自制模组」的 ——
// 如果这里给个零值占位，那个差集就永远为空，所有自制模组都会被当成「已在 Modrinth 上」，
// 于是导出时不会塞 overrides，收包的人装不起来还查不出原因。
func TestVersionFilesByHashOmitsUnknownHashes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 两个 hash 只回一个 —— 复刻实测行为（真实接口就是这么干的）
		_ = json.NewEncoder(w).Encode(map[string]any{
			"known123": map[string]any{"id": "v", "project_id": "p", "environment": "*"},
		})
	}))
	defer srv.Close()

	got, err := VersionFilesByHash(srv.URL, []string{"known123", "unknown456"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["unknown456"]; ok {
		t.Error("查不到的哈希不该出现在结果里")
	}
	if len(got) != 1 {
		t.Errorf("该只回 1 条，实际 %d 条：%v", len(got), got)
	}
}

// 去重 + 小写归一：同一个 jar 在大写路径下算出来的 hash 可能带大写，
// 不归一的话会多打一遍请求，还会让差集算错。
func TestVersionFilesByHashDedupesAndLowercases(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		// 发出去的必须全是小写，且没有重复
		s := string(body)
		if strings.Contains(s, "AAAA") {
			t.Errorf("发给源站的 hash 没转小写：%s", s)
		}
		var req struct{ Hashes []string }
		_ = json.Unmarshal(body, &req)
		if len(req.Hashes) != 1 || req.Hashes[0] != "aaaa1111" {
			t.Errorf("该只剩一个去重后的小写 hash，实际 %v", req.Hashes)
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	_, err := VersionFilesByHash(srv.URL, []string{"AAAA1111", "aaaa1111", "  ", "aaaa1111 "})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("三个等价输入该只打一次请求，实际 %d 次", calls)
	}
}

// 空输入不该发请求（服务端调用方可能传个空目录进来）。
func TestVersionFilesByHashEmptyInputMakesNoRequest(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer srv.Close()

	got, err := VersionFilesByHash(srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 || calls != 0 {
		t.Errorf("空输入该直接返回空 map 且不发请求，实际 len=%d calls=%d", len(got), calls)
	}
}
