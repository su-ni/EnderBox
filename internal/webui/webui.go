// Package webui 把前端构建产物嵌进二进制。
//
// 为什么要单独一个包 + 一个 dist 目录：`go:embed` **不能跨 `..`**，
// 所以没法直接嵌 `../../web/dist`。做法是构建脚本先把产物复制到
// `internal/webui/dist/`，再编译 —— 这样 `go build` 一条命令就能出一个
// 自带界面的可执行文件，不需要用户先装 Node。
//
// 目录里预置了一份占位 index.html，所以**不跑前端构建也能编译**。
// 那份额外的意义是：真忘了打包时，用户看到的是「怎么补」而不是一片白屏。
package webui

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var files embed.FS

// Built 报告嵌进来的是真前端还是占位页。
// 用「有没有 assets 目录」判断 —— 占位页只有一个 index.html，没有构建产物。
func Built() bool {
	_, err := fs.Stat(files, "dist/assets")
	return err == nil
}

// Handler 提供前端静态文件。
//
// SPA 回落：路径在 dist 里找不到对应文件时，一律回 index.html ——
// 否则用户直接粘一个 `?page=config` 的链接、或者刷新页面，就会拿到 404。
// （前端的页面状态存在 query 里，但路由路径仍可能被浏览器当作文件路径来请求。）
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			serveIndex(w, r, sub)
			return
		}
		// 真文件就直接给；目录也交给 FileServer 处理
		if f, err := sub.Open(p); err == nil {
			_ = f.Close()
			// 前端产物带内容哈希，可以长缓存；index.html 不能缓存，否则更新后刷不掉
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// 找不到 = SPA 路径，回首页。
		// 但「像文件」的请求不能回落 —— 否则一个过期的 assets/xxx.js 会拿到
		// 200 + text/html，浏览器只会报一句「Expected a JavaScript module script
		// but the server responded with a MIME type of text/html」，
		// 把「文件没了」这个真问题藏起来。这类请求必须老老实实回 404。
		if fileLike(p) {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, sub)
	})
	return h, nil
}

// fileLike 判断请求路径是不是「一个文件」而不是前端路由。
// 判据是最后一段带扩展名：前端的页面状态全存在 query 里，路径永远只有 "/"，
// 所以带扩展名的路径必定是在要静态文件。
func fileLike(p string) bool {
	seg := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		seg = p[i+1:]
	}
	return strings.Contains(seg, ".")
}

func serveIndex(w http.ResponseWriter, r *http.Request, sub fs.FS) {
	b, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// 说明页不是错误，是这个二进制的正常形态（没跑 build.sh）。
			// 回 200 而不是 500：用户看到的应该是一页「怎么补」，不是一行报错。
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(w, offlinePage)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html 一定不能缓存：不然前端更新后用户会一直拿到旧的那份壳，
	// 而它引用的 assets 文件名已经变了 —— 表现为「页面打开是白的」。
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}
