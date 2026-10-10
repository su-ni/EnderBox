package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func main() {
	c := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", "https://www.mcmod.cn/static/public/js/class/mc.class.functions.js?v=29", nil)
	req.Header.Set("User-Agent", "EnderBox/0.2 (test)")
	r, _ := c.Do(req)
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	js := string(b)
	i := strings.Index(js, `layout:"force"`)
	if i < 0 {
		fmt.Println("not found")
		return
	}
	lo := i - 1400
	if lo < 0 { lo = 0 }
	hi := i + 600
	if hi > len(js) { hi = len(js) }
	fmt.Println(js[lo:hi])
}
