package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/su-ni/EnderBox/internal/api"
	"github.com/su-ni/EnderBox/internal/daemon"
	"github.com/su-ni/EnderBox/internal/fetch"
	"github.com/su-ni/EnderBox/internal/settings"
	"github.com/su-ni/EnderBox/internal/webui"
)

// version 由构建脚本用 -ldflags 注入；直接 go run 时是 dev
var version = "dev"

func main() {
	dir := flag.String("dir", "./data", "数据目录（实例、配置、备份）")
	addr := flag.String("addr", ":23333", "HTTP 监听地址")
	// 默认开：这个工具的目标用户是「双击 exe 就想用」，不自动开浏览器等于让人自己猜地址。
	// 当系统服务跑的时候传 -open=false 关掉。
	open := flag.Bool("open", true, "启动后自动打开浏览器")
	flag.Parse()

	mgr, err := daemon.NewManager(*dir)
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}

	// MC 百科关系星图的磁盘缓存（<数据目录>/mcmod-cache）。
	// 设在数据目录下是为了「重装/换机时跟着数据走」，也是为了让用户能一眼找到、随手清。
	// 有了它，第二次进同一个模组的关系图就是秒开、且不再打百科的服务器。
	fetch.SetMcmodCacheDir(filepath.Join(*dir, "mcmod-cache"))

	// 全局设置（访问安全、下载源、监听地址）。它和实例配置分开存 ——
	// 一个是「整个守护进程」的行为，一个是「某个世界」的行为。
	set, err := settings.NewStore(*dir)
	if err != nil {
		log.Fatalf("读取全局设置失败: %v", err)
	}
	if saved := set.Get(); saved.ListenAddr != "" && saved.ListenAddr != *addr {
		// 配置里存的地址和命令行传的不一致时，以命令行参数为准（那是启动方显式指定的），
		// 但要说出来 —— 否则用户会觉得「我在设置页改了怎么没用」。
		log.Printf("提示：配置里保存的监听地址是 %s，本次命令行传的是 %s，以命令行为准", saved.ListenAddr, *addr)
	}

	mgr.AutoStartAll()

	// 后台调度器：自动备份 + 计划任务共用同一个 30 秒循环。
	// 它内部会先等 20 秒再开始看 —— 别和 AutoStartAll 抢磁盘。
	mgr.StartScheduler()

	log.Printf("EnderBox %s 守护进程已启动，数据目录 %s", version, *dir)

	// 前端产物嵌在二进制里。没打包的话这里的 dist 只有一份占位说明页 ——
	// 与其白屏，不如直接告诉用户「跑一下 build.sh」。
	ui, err := webui.Handler()
	if err != nil {
		log.Fatalf("初始化前端资源失败: %v", err)
	}
	if webui.Built() {
		log.Printf("面板地址 %s（前端已内嵌）", displayURL(*addr))
	} else {
		log.Printf("面板地址 %s", displayURL(*addr))
		log.Printf("注意：这个二进制里没有前端产物，浏览器打开只会看到一份说明页。要出完整版本请跑 build.sh。")
	}

	if *open {
		go openBrowser(displayURL(*addr))
	}

	apiHandler := api.New(mgr, set, *addr).Routes()
	// /api/ 走接口，其余全交给前端。分开是为了让 SPA 回落不会把接口的 404 也吃掉 ——
	// 接口返回 404 时必须还是 404，前端才能正确报错。
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiHandler.ServeHTTP(w, r)
			return
		}
		ui.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:         *addr,
		Handler:      root,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 日志流是长连接，写超时必须关掉
	}
	// ── 退出时优雅停服 ──────────────────────────────────────────
	//
	// 为什么必须做：面板一关，java 会跟着被**硬杀**（Windows 上子进程沿用父进程控制台，
	// 控制台关闭时系统给挂在上面的进程发 CTRL_CLOSE_EVENT）。那是硬杀**不是 stop** ——
	// 存档来不及写盘，有损坏风险。这里拦一道：退出前先好好地停一遍。
	//
	// ⚠ 等待上限必须**短**：Windows 关控制台窗口时，系统只给约 5 秒，
	//   超了就直接切掉 —— 等太久等于没等。所以下面用 4 秒，给日志留点余量。
	// ⚠ 三种退出路径都要覆盖：
	//   ① Ctrl+C             → os.Interrupt
	//   ② kill / 服务停止     → SIGTERM
	//   ③ **关掉那个黑框**     → CTRL_CLOSE_EVENT（Windows 专有，见 consoleCtrlWindows）
	const graceTimeout = 4 * time.Second
	quit := make(chan struct{})
	var quitOnce sync.Once
	shutdown := func(why string) {
		quitOnce.Do(func() {
			if ids := mgr.RunningIDs(); len(ids) == 0 {
				log.Printf("%s，没有在跑的实例，直接退出", why)
			} else {
				log.Printf("%s，正在优雅停服 %d 个实例（最多等 %s）…", why, len(ids), graceTimeout)
				stopped, failed := mgr.StopAllGracefully(graceTimeout)
				if failed > 0 {
					log.Printf("停服完成：成功 %d 个，%d 个没能干净退出", stopped, failed)
				} else {
					log.Printf("停服完成：%d 个全部干净退出", stopped)
				}
			}
			close(quit)
		})
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sig
		shutdown("收到退出信号 " + s.String())
	}()
	// 关控制台窗口走的是另一条路（Windows 不给 Go 投信号），单独挂一个处理器
	watchConsoleClose(func() { shutdown("控制台窗口被关闭") })

	go func() {
		<-quit
		// 让 ListenAndServe 返回；它下面那句 log.Fatalf 会因为 ErrServerClosed 被跳过
		_ = srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		// ⚠ 这里**不能**再用 log.Fatalf：优雅退出时 ListenAndServe 会返回
		//   ErrServerClosed，那是正常路径，不该报「服务退出」也不能吞掉真错误。
		log.Printf("服务退出: %v", err)
	}
	<-quit // 等停服真正走完，别让 main 先返回把 goroutine 掐了
}

// displayURL 把监听地址翻成「人能认、点了能开」的地址。
//
// 为什么需要它：日志里原来直接拼 "http://localhost" + addr，这等于假设
// addr 一定长成 ":23333"（只有端口）。一旦显式传了 host，就拼出
// `http://localhost127.0.0.1:23456` 这种四不像 —— 用户想照抄去浏览器都抄不了。
//
// 规则：
//   - ":23333" / "0.0.0.0:23333" / "[::]:23333" → 都是「所有网卡」，
//     浏览器打不开，换成 localhost（保留端口）
//   - 其余（127.0.0.1:23456、192.168.1.5:23333）原样用 —— 那是启动方
//     明确指定的，硬套 localhost 反而把它改错
func displayURL(addr string) string {
	switch {
	case strings.HasPrefix(addr, ":"):
		return "http://localhost" + addr
	case strings.HasPrefix(addr, "0.0.0.0:"):
		return "http://localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
	case strings.HasPrefix(addr, "[::]:"):
		return "http://localhost:" + strings.TrimPrefix(addr, "[::]:")
	default:
		return "http://" + addr
	}
}

// openBrowser 尽力打开默认浏览器。
// 打不开也不该让守护进程挂掉 —— 它只是个便利功能，用户手动输地址一样能用。
func openBrowser(url string) {
	// 给它一点时间把端口监听上，不然会打开一个「无法访问」
	time.Sleep(500 * time.Millisecond)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// 用 cmd 的 start 而不是 shell 关联程序表：后者在不同机器上可能被改得乱七八糟。
		// 第一个空参数是 start 的「窗口标题」占位，少了它 start 会把 URL 当标题。
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		log.Printf("自动打开浏览器失败（不影响使用，手动访问 %s 即可）：%v", url, err)
		return
	}
	// 不 Wait 会留下僵尸进程；但也不能阻塞，所以丢给一个 goroutine 收尾
	go func() { _ = cmd.Wait() }()
}
