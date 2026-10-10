package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 桌面壳唯一有分支的地方是「首屏给什么」，所以这里盯住它。
//
// ⚠ 测试里必须把 autoStart 关掉：它的默认值是 true，而关掉之前任何一次
// 「守护进程不健康」的分支都会去**真的拉起一个 serverbox.exe** ——
// 跑测试顺手在自己机器上开一个服务，是最不该发生的那种副作用。
func TestShellRedirectsWhenDaemonIsUp(t *testing.T) {
	*autoStart = false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()

	sh := &shell{target: srv.URL}
	for _, path := range []string{"/", "/page=players", "/assets/index-abc.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		sh.ServeHTTP(rec, req)

		if rec.Code != http.StatusFound {
			t.Errorf("%s：守护进程健康时该 302，实际 %d", path, rec.Code)
			continue
		}
		// 路径要保留：首屏是 "/"，但别把用户/系统带的路径吃掉
		if want := srv.URL + path; rec.Header().Get("Location") != want {
			t.Errorf("%s：Location = %q，期望 %q", path, rec.Header().Get("Location"), want)
		}
		// 等待页的 meta refresh 绝不能出现在这条路上
		if strings.Contains(rec.Body.String(), "正在启动面板") {
			t.Errorf("%s：健康却返回了等待页", path)
		}
	}
}

// TestShellServesWaitingPageWhenDown 守护进程没起来时：
//   - **不能**把 webview 直接指过去（那样用户看到的是浏览器的「无法访问此网站」）
//   - 要给一个自带重试的等待页，并且把地址写出来（用户得知道它在连谁）
//   - 自动拉起关掉时，文案要说清「我没法自动拉起，你手动启动或者换地址」
func TestShellServesWaitingPageWhenDown(t *testing.T) {
	*autoStart = false
	// 127.0.0.1:1 上不会有人监听 —— 连接立刻被拒，不用等超时
	sh := &shell{target: "http://127.0.0.1:1"}

	rec := httptest.NewRecorder()
	sh.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("连不上时该给等待页（200），实际 %d", rec.Code)
	}
	body := rec.Body.String()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("等待页的 Content-Type 不对：%q", ct)
	}
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Error("等待页没有自带重试 —— 守护进程起来之后用户还得手动刷新")
	}
	if !strings.Contains(body, "http://127.0.0.1:1") {
		t.Error("等待页没写出它在连哪个地址")
	}
	if !strings.Contains(body, "连不上守护进程") {
		t.Error("自动拉起关掉时，文案该说清「连不上」，而不是含糊地说正在启动")
	}
	if strings.Contains(body, "正在启动面板") {
		t.Error("自动拉起已关掉却仍说「正在启动」—— 那是在骗用户等")
	}
}

// TestWaitingPageEscapesAddress 地址是命令行给的，塞进 HTML 前必须转义。
// 这条不是洁癖：`-daemon 'http://x/?a="><script>'` 是能构造出来的，
// 而首屏恰好是**唯一**由我们自己拼 HTML 的地方。
func TestWaitingPageEscapesAddress(t *testing.T) {
	bad := `http://127.0.0.1:23456/?x="><script>alert(1)</script>`
	page := waitingPage(bad, true, "", "")
	if strings.Contains(page, "<script>") {
		t.Error("地址里的 <script> 原样进了 HTML —— 没转义")
	}
	if !strings.Contains(page, "&lt;script&gt;") {
		t.Error("转义后的形态不对，检查 htmlEscape")
	}
}

// TestWaitingPageShowsLaunchError 自动拉起失败时，原因要显示出来，不能只说「正在启动」。
// 这一条和面板那边「不静默失败」是同一条规矩。
func TestWaitingPageShowsLaunchError(t *testing.T) {
	page := waitingPage("http://127.0.0.1:23456", false, "同目录也没找到 serverbox.exe", "")
	if !strings.Contains(page, "同目录也没找到 serverbox.exe") {
		t.Error("拉起失败的原因没显示出来 —— 用户只能干等")
	}
	if !strings.Contains(page, "面板没能起来") {
		t.Error("失败态的标题不对")
	}
	// 失败态不该再转圈：它不是「正在发生」，而是「已经不行了」
	if strings.Contains(page, `class="spin"`) {
		t.Error("已经失败却还在转圈，看着像还在努力")
	}
}

// TestAddrOfPassedToDaemon 自动拉起时**必须**把地址告诉守护进程。
//
// 这是本轮真机踩到的坑：守护进程不带 -addr 就用它自己保存的配置（本机是 :23333），
// 而窗口连的是 -daemon 给的 :23456 —— 结果「两个进程都活着、窗口一直显示启动中」。
// 判据是「拉起的参数里带着 host:port」，这条不测就不长记性。
func TestAddrOfPassedToDaemon(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:23456":     "127.0.0.1:23456",
		"http://localhost:23333/":    "localhost:23333",
		"http://192.168.1.5:23456":   "192.168.1.5:23456",
		"https://panel.example:443/": "panel.example:443",
		"不是个地址":                      "",
	}
	for in, want := range cases {
		if got := addrOf(in); got != want {
			t.Errorf("addrOf(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestDaemonArgsAlwaysCarryAddr 拉起的参数里**必须有 -addr**，且值与 -daemon 一致。
//
// 这是本轮真机踩到的坑：守护进程不带 -addr 就用它自己保存的配置（本机是 :23333），
// 而窗口连的是 -daemon 给的 :23456 —— 结果「两个进程都活着、窗口一直显示正在启动」，
// 现场看像面板坏了。所以这条要盯住参数本身，而不是只测 addrOf 算得对不对。
func TestDaemonArgsAlwaysCarryAddr(t *testing.T) {
	origAddr, origData := *daemonAddr, *dataDir
	defer func() { *daemonAddr, *dataDir = origAddr, origData }()

	*daemonAddr = "http://127.0.0.1:23456"
	*dataDir = ""
	got := daemonArgs()
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "-open=false") {
		t.Errorf("少了 -open=false，守护进程会再开一个浏览器标签：%v", got)
	}
	if !strings.Contains(joined, "-addr 127.0.0.1:23456") {
		t.Errorf("-addr 没传或值不对 —— 会重现「两边端口不一致、窗口永远在等」：%v", got)
	}
	if strings.Contains(joined, "-dir") {
		t.Errorf("没指定数据目录时不该传 -dir（要用守护进程自己的默认值）：%v", got)
	}

	// 指定了数据目录才传 -dir
	*dataDir = "F:/tmp/sbxdata/data"
	got = daemonArgs()
	if !strings.Contains(strings.Join(got, " "), "-dir F:/tmp/sbxdata/data") {
		t.Errorf("指定了数据目录却没传下去：%v", got)
	}

	// 地址解析不出来时，宁可不传 -addr（让守护进程用自己的配置），也不能传个空串
	*daemonAddr = "不是个地址"
	*dataDir = ""
	if strings.Contains(strings.Join(daemonArgs(), " "), "-addr") {
		t.Errorf("地址解析不出来时不该硬塞一个空的 -addr：%v", daemonArgs())
	}
}

// TestIsLocal 只有本机地址才允许自动拉起 —— 在 A 机器上起进程，
// 不会让 B 机器上的地址变得可用。
func TestIsLocal(t *testing.T) {
	local := []string{"http://127.0.0.1:23456", "http://localhost:23456", "http://[::1]:23456", "http://127.0.0.1"}
	remote := []string{"http://192.168.1.5:23456", "http://panel.example:23456", "http://10.0.0.7:23456", "不是个地址"}
	for _, s := range local {
		if !isLocal(s) {
			t.Errorf("%s 该判成本机", s)
		}
	}
	for _, s := range remote {
		if isLocal(s) {
			t.Errorf("%s 不该判成本机 —— 会在一台机器上去拉另一台的进程", s)
		}
	}
}

// TestLaunchDecisionExplainsItself 「不自动拉起」的两种理由要分开说：
// 一个让用户去另一台机器上看，一个让他手动启一下 —— 文案混在一起他只能干等。
func TestLaunchDecisionExplainsItself(t *testing.T) {
	orig := *daemonAddr
	defer func() { *daemonAddr = orig }()

	*autoStart = false
	*daemonAddr = "http://127.0.0.1:23456"
	if can, why := launchDecision(); can || why == "" {
		t.Errorf("关掉自动拉起后不该尝试：can=%v why=%q", can, why)
	}

	*autoStart = true
	*daemonAddr = "http://192.168.1.5:23456"
	can, why := launchDecision()
	if can {
		t.Error("指向别的机器时不该在本机拉起进程")
	}
	if !strings.Contains(why, "另一台机器") {
		t.Errorf("理由没说清是「另一台机器」：%q", why)
	}

	*daemonAddr = "http://127.0.0.1:23456"
	if can, why := launchDecision(); !can || why != "" {
		t.Errorf("本机 + 开着自动拉起，应该去拉：can=%v why=%q", can, why)
	}
}
