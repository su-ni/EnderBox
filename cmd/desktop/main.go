// EnderBox 桌面端：把面板装进一个原生窗口，双击即用。
//
// 形态（见 docs/DESIGN.md）：**纯客户端**，连守护进程的 HTTP API。
// 前端不在这里再嵌一份 —— 窗口整个重定向到守护进程地址，于是 webview 的「源」
// 就是守护进程本身，会话 Cookie、SSE 日志流、相对路径全部天然成立，
// 跟我们自己在浏览器里打开是同一个环境。
//
// ⚠ 为什么不用反代（把 /api 转发过来）：日志与实时数据都是 SSE。
// 反代要求中间每一层都不缓冲，而 AssetServer 那一层不是我们能控制的 ——
// 一旦被缓冲，控制台就「卡住不动」，而且现场看着像服务端不推数据。
// 重定向把这一整类问题绕开了。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

var (
	daemonAddr = flag.String("daemon", "http://127.0.0.1:23456",
		"守护进程地址（可以是另一台机器，例如 http://192.168.1.5:23456）")
	autoStart = flag.Bool("auto-start", true, "守护进程没在跑时，自动拉起同目录的 enderbox.exe")
	waitSecs  = flag.Int("wait", 20, "自动拉起后最多等多少秒")
	dataDir   = flag.String("data", "",
		"自动拉起守护进程时用它做数据目录；留空则用守护进程自己的默认值（exe 旁边的 data/）")
)

// daemonArgs 自动拉起守护进程时要传的参数（纯函数，规格测试盯着它）。
//
// ⚠ `-addr` 是必需的，不是可选优化：守护进程不带 -addr 时会用它**自己保存的配置**
// （本机那份配的是 :23333），于是「两个进程都活着、窗口却一直显示正在启动」——
// 现场看像面板坏了。本轮真机实测踩到过，所以这条用测试钉住。
//
// `-open=false` 同理：不带它，守护进程会再开一个浏览器标签，
// 用户双击一次会得到「一个原生窗口 + 一个浏览器标签」，看着像出了 bug。
func daemonArgs() []string {
	args := []string{"-open=false"}
	if a := addrOf(*daemonAddr); a != "" {
		args = append(args, "-addr", a)
	}
	if d := strings.TrimSpace(*dataDir); d != "" {
		args = append(args, "-dir", d)
	}
	return args
}

// shell 是 AssetServer 的处理器：**唯一的职责是决定首屏给什么**。
//
// 健康 → 302 到守护进程（之后 webview 就活在守护进程的源上，请求不再经过这里）。
// 不健康 → 自动拉起一次，并返回一个会自己重试的等待页。
//
// 非得这么绕的原因是：直接把窗口指向守护进程，它没起来时用户看到的是**浏览器的错误页**
// （「无法访问此网站」）—— 那既不像我们的产品，也说不出下一步该干嘛。
type shell struct {
	target string

	mu        sync.Mutex
	launching bool
	startErr  string
}

func (s *shell) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if daemonHealthy(s.target) {
		// 保留路径：首屏是 "/"，但也可能是系统/用户带的其它路径
		http.Redirect(w, r, s.target+r.URL.Path, http.StatusFound)
		return
	}

	s.mu.Lock()
	launching := s.launching
	startErr := s.startErr
	s.mu.Unlock()

	canLaunch, why := launchDecision()
	// 只在第一次尝试拉起。失败之后别每来一次请求就再拉一次进程 ——
	// 等待页是每秒重刷的，那样会每秒在后台堆一个 enderbox.exe。
	if canLaunch && !launching && startErr == "" {
		s.mu.Lock()
		s.launching = true
		s.mu.Unlock()
		launching = true
		go func() {
			err := launchDaemon()
			s.mu.Lock()
			s.launching = false
			if err != nil {
				s.startErr = err.Error()
			}
			s.mu.Unlock()
		}()
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, waitingPage(s.target, launching, startErr, why))
}

// daemonHealthy 探一下守护进程在不在。超时给得短 —— 这是首屏，用户在等。
func daemonHealthy(base string) bool {
	c := &http.Client{Timeout: 1200 * time.Millisecond}
	resp, err := c.Get(base + "/api/health")
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// launchDecision 该不该尝试自动拉起；不该的话，原因是什么（要显示给用户）。
//
// 两条不该拉起的理由必须分开说，因为下一步动作完全不同：
//   - 指向别的机器 → 用户该去那台机器上看
//   - 自己关了自动拉起 → 用户该手动启一下
func launchDecision() (bool, string) {
	if !*autoStart {
		return false, "已用 -auto-start=false 关掉了自动拉起。"
	}
	if !isLocal(*daemonAddr) {
		return false, "它指向的是另一台机器 —— 自动拉起只能拉起**这台**机器上的进程，" +
			"所以得去那台机器上确认面板在跑。"
	}
	return true, ""
}

// addrOf 从 -daemon 的地址里取出 host:port，用来告诉守护进程「你监听在哪」。
//
// ⚠ 这一句是必需的，不是可选优化：守护进程不带 -addr 时会用它**自己保存的配置**
// （本机这份配的是 :23333），于是「进程起来了、窗口却一直在等 :23456」——
// 现场看就是「面板卡在启动中」，而两个进程都活着。本轮真机实测踩到过。
func addrOf(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// isLocal 目标是不是本机。不是本机就绝不自动拉起：
// 在 A 机器上起一个进程，不会让 B 机器上的地址变得可用。
func isLocal(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// launchDaemon 拉起同目录的 enderbox.exe。
//
// ⚠ 一定要传 -open=false：守护进程默认会再开一个浏览器标签，
// 那样用户双击一次会得到「一个原生窗口 + 一个浏览器标签」，看着像出了 bug。
func launchDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("定位自身路径失败：%w", err)
	}
	dir := filepath.Dir(exe)
	name := "enderbox.exe"
	if runtime.GOOS != "windows" {
		name = "enderbox"
	}
	bin := filepath.Join(dir, name)
	if _, err := os.Stat(bin); err != nil {
		return fmt.Errorf("守护进程没在跑，同目录也没找到 %s —— 手动启动它，或者用 -daemon 指到别的地址", name)
	}
	args := daemonArgs()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir // 数据目录默认是相对的，工作目录必须钉在 exe 旁边
	// 让守护进程脱离本进程的控制台 —— 否则「关掉桌面窗口」会把守护进程
	// （以及它管着的 MC 服务器）一起带走。理由见 proc_windows.go。
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 %s 失败：%w", name, err)
	}
	go func() { _ = cmd.Wait() }() // 不 Wait 会留僵尸；但也绝不能阻塞首屏

	deadline := time.Now().Add(time.Duration(*waitSecs) * time.Second)
	for time.Now().Before(deadline) {
		if daemonHealthy(*daemonAddr) {
			return nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return fmt.Errorf("已经把 %s 拉起来了，但 %d 秒内没能连上 %s —— 去看一眼它的日志", name, *waitSecs, *daemonAddr)
}

// waitingPage 首屏等待页。自带一次重试（meta refresh 回到本处理器），
// 守护进程起来之后**不需要用户手动刷新**，页面自己就跳过去了。
//
// 配色沿用面板 token（底 #111311 / 面板 #1c201d / 文字 #eef1ec·#b7c2b4 / 主绿 #7cc24f），
// 零圆角零渐变 —— 让人一眼认出「这是同一个产品」，而不是一个报错页。
func waitingPage(target string, launching bool, startErr, refuse string) string {
	title, hint := "正在启动面板…", "守护进程已经拉起来了，就绪之后这个页面会自己跳到面板。"
	if startErr != "" {
		title, hint = "面板没能起来", startErr
	} else if !launching {
		title = "连不上守护进程"
		hint = refuse
		if hint == "" {
			hint = "它没在跑，同目录也没有 enderbox.exe 可以自动拉起 —— 手动启动它，或者用 -daemon 指到别的地址。"
		}
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">`)
	b.WriteString(`<meta http-equiv="refresh" content="1"><title>EnderBox</title><style>
:root{color-scheme:dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;background:#111311;color:#eef1ec;
 font:400 13px/1.7 ui-sans-serif,"Segoe UI",system-ui,sans-serif}
.card{border:3px solid #000;background:#1c201d;padding:26px 30px;max-width:580px}
h1{margin:0 0 10px;font-size:16px;font-weight:700}
p{margin:0 0 12px;color:#b7c2b4}
p:last-child{margin-bottom:0}
code{font-family:ui-monospace,"Cascadia Mono",Consolas,monospace;color:#7cc24f}
.spin{display:inline-block;width:8px;height:8px;background:#7cc24f;margin-right:9px;vertical-align:middle;
 animation:p 1s ease-in-out infinite}
@keyframes p{0%,100%{opacity:.25}50%{opacity:1}}
@media (prefers-reduced-motion:reduce){.spin{animation:none}}
</style></head><body><div class="card">`)
	if startErr != "" {
		b.WriteString(`<h1>` + title + `</h1>`)
	} else {
		b.WriteString(`<h1><span class="spin"></span>` + title + `</h1>`)
	}
	b.WriteString(`<p>` + hint + `</p>`)
	b.WriteString(`<p>守护进程地址：<code>` + htmlEscape(target) + `</code></p>`)
	b.WriteString(`</div></body></html>`)
	return b.String()
}

// htmlEscape 地址是用户/命令行给的，塞进 HTML 前必须转义。
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}

// App 只为了拿到 ctx，用于「第二次启动时把窗口提到前面」。
// 没有 JS 绑定的需求 —— 界面全在守护进程那边。
type App struct {
	mu  sync.Mutex
	ctx context.Context
}

func (a *App) setCtx(c context.Context) {
	a.mu.Lock()
	a.ctx = c
	a.mu.Unlock()
}

func (a *App) getCtx() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctx
}

func main() {
	flag.Parse()

	app := &App{}
	err := wails.Run(&options.App{
		Title:     "EnderBox 控制台",
		Width:     1440,
		Height:    900,
		MinWidth:  1024,
		MinHeight: 640,
		// 与面板同色：窗口出现时不要先闪一下白底
		BackgroundColour: &options.RGBA{R: 17, G: 19, B: 17, A: 1},
		AssetServer: &assetserver.Options{
			// Assets 留空 → 所有 GET 都进 Handler（见 shell 的注释）
			Handler: &shell{target: *daemonAddr},
		},
		OnStartup: func(ctx context.Context) { app.setCtx(ctx) },
		// 单实例：再点一次 exe 不该开出第二个窗口，而应把已有的那个提到前面。
		// ⚠ OnSecondInstanceLaunch 挂在 **SingleInstanceLock 里面**，不是 App 上的字段。
		SingleInstanceLock: &options.SingleInstanceLock{
			// UniqueId 全机唯一即可，它同时也是互斥体的名字
			UniqueId: "enderbox-desktop-7f3a1c",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				ctx := app.getCtx()
				if ctx == nil {
					return
				}
				// 窗口刚建出来就点了第二次的话，这两个调用会 panic ——
				// 不值得为它挂掉整个进程（用户只是多点了两下）
				defer func() { _ = recover() }()
				wailsRuntime.WindowUnminimise(ctx)
				wailsRuntime.WindowShow(ctx)
			},
		},
	})
	if err != nil {
		log.Fatalf("启动窗口失败：%v", err)
	}
}
