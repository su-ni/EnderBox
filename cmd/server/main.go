package main

import (
	"flag"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/su-ni/ServerBox/internal/api"
	"github.com/su-ni/ServerBox/internal/daemon"
	"github.com/su-ni/ServerBox/internal/settings"
	"github.com/su-ni/ServerBox/internal/webui"
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

	log.Printf("ServerBox %s 守护进程已启动，数据目录 %s", version, *dir)

	// 前端产物嵌在二进制里。没打包的话这里的 dist 只有一份占位说明页 ——
	// 与其白屏，不如直接告诉用户「跑一下 build.sh」。
	ui, err := webui.Handler()
	if err != nil {
		log.Fatalf("初始化前端资源失败: %v", err)
	}
	if webui.Built() {
		log.Printf("面板地址 http://localhost%s（前端已内嵌）", *addr)
	} else {
		log.Printf("面板地址 http://localhost%s", *addr)
		log.Printf("注意：这个二进制里没有前端产物，浏览器打开只会看到一份说明页。要出完整版本请跑 build.sh。")
	}

	if *open {
		// 给人点的地址不能写 :23333 这种「所有网卡」的形式
		host := *addr
		if strings.HasPrefix(host, ":") {
			host = "localhost" + host
		}
		go openBrowser("http://" + host)
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
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("服务退出: %v", err)
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
