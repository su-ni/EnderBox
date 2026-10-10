// 从 MC百科 的「模组检索」页生成我们自己的中文译名快照。
//
// 为什么自己做而不是抄现成的：这份数据的上游本来就是 MC百科（公开网页），
// 自己抓一遍既干净（不用背第三方启动器的使用条款），又能按需重跑刷新。
//
// 用法：
//
//	go run ./tools/mcmodsnap -cache ../tmp/mcmod-pages -out internal/fetch/mcmod_zh.json
//
// 设计要点：
//   - **按页落盘**：每页解析结果写成 page-0001.tsv，已经存在的页直接跳过。
//     973 页的抓取中途断了不用从头再来（这次就断过一次，血的教训）。
//   - **限速**：并发 2、每个 worker 每次请求后等 400ms，约 2 req/s。
//     这是别人的公共 wiki，抓快了自己被封、也给别人添麻烦，没必要。
//   - **带自家 UA**：出问题对方能找得到人。
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://www.mcmod.cn/modlist.html?page="

// 一条模组条目：<p class="name"><a href="/class/123.html">[XX] 中文名</a></p>
//
//	<p class="ename"><a ...>English Name</a></p>
var (
	reEntry = regexp.MustCompile(`(?s)<p class="name"><a href="/class/(\d+)\.html"[^>]*>(.*?)</a></p>\s*<p class="ename"><a[^>]*>(.*?)</a></p>`)
	reLast  = regexp.MustCompile(`[?&]page=(\d+)`)
)

type Entry struct {
	ID int    `json:"-"` // 百科 class id，只用于去重，不进产物
	CN string `json:"-"` // 中文名（含 [缩写] 前缀）
	EN string `json:"-"` // 英文名，可能为空
}

func main() {
	cache := flag.String("cache", "mcmod-pages", "每页结果的存放目录")
	out := flag.String("out", "mcmod_zh.json", "合并后的产物路径")
	from := flag.Int("from", 1, "起始页")
	to := flag.Int("to", 0, "结束页（0 = 自动探测最后一页）")
	delay := flag.Duration("delay", 1200*time.Millisecond, "每个 worker 两次请求之间的间隔")
	conc := flag.Int("conc", 1, "并发 worker 数。⚠ 别调大：曾经开到 8，被对方防火墙按「机器人行为」封了 IP")
	retry := flag.Int("retry", 4, "被限流(429)时的最大重试次数")
	backoff := flag.Duration("backoff", 5*time.Second, "被限流后每次重试的等待基数（按次数线性放大）")
	allowPartial := flag.Bool("allow-partial", false, "页数没抓全时也写出产物（默认拒绝：残缺的表会让某些模组静默搜不到）")
	flag.Parse()

	if err := os.MkdirAll(*cache, 0o755); err != nil {
		fmt.Println("建缓存目录失败：", err)
		os.Exit(1)
	}

	last := *to
	if last == 0 {
		var err error
		last, err = detectLastPage()
		if err != nil {
			fmt.Println("探测总页数失败：", err)
			os.Exit(1)
		}
	}
	fmt.Printf("准备抓取第 %d ~ %d 页（并发 %d，间隔 %s）\n", *from, last, *conc, *delay)

	tasks := make(chan int)
	stop := make(chan struct{}) // 被防火墙封了就靠它把整轮叫停
	var wg sync.WaitGroup
	var mu sync.Mutex
	var bannedMsg string
	fetched, skipped, failed, throttled := 0, 0, 0, 0

	abort := func(msg string) {
		mu.Lock()
		if bannedMsg == "" {
			bannedMsg = msg
			close(stop)
		}
		mu.Unlock()
	}

	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for page := range tasks {
				mu.Lock()
				dead := bannedMsg != ""
				mu.Unlock()
				if dead {
					return
				}
				file := filepath.Join(*cache, fmt.Sprintf("page-%04d.tsv", page))
				if _, err := os.Stat(file); err == nil {
					mu.Lock()
					skipped++
					mu.Unlock()
					continue
				}
				var body string
				var err error
				// ⚠ 429 是对方在明说「你太快了」—— 必须退避，而且是大幅退避。
				// 之前吃过一次亏：并发开到 8，一口气被限流 829 页。
				// 这地方贪快只会更慢，还可能把 IP 拉黑。
				for attempt := 1; attempt <= *retry; attempt++ {
					body, err = fetch(baseURL + strconv.Itoa(page))
					if err == nil {
						break
					}
					if !isThrottled(err) {
						break // 不是限流，多半是页面本身的问题，重试没意义
					}
					wait := *backoff * time.Duration(attempt)
					mu.Lock()
					throttled++
					mu.Unlock()
					fmt.Printf("  第 %d 页被限流，等 %s 后重试（第 %d/%d 次）\n", page, wait, attempt, *retry)
					time.Sleep(wait)
				}
				if err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
					fmt.Printf("  第 %d 页失败：%v\n", page, err)
					time.Sleep(*delay)
					continue
				}
				// 被封禁时对方返回的是 **HTTP 200 + 一张说明页**。
				// 不认出来的话会被当成「这页没有模组」静默跳过，
				// 最后产出一份看起来正常、其实缺一半的表 —— 那比直接失败危险得多。
				if isBanned(body) {
					abort("对方防火墙已封禁本机地址（页面原文：" + banSnippet(body) + "）")
					fmt.Println("\n⛔", bannedMsg)
					fmt.Println("   已停止本轮抓取。被封通常是「访问频率过高 / 机器人行为」。")
					fmt.Println("   不要再重试、不要换 UA 硬闯 —— 等几小时再跑，用默认的温和参数（-conc 1 -delay 1200ms）。")
					return
				}
				lines := parsePage(body)
				if len(lines) == 0 {
					// 空页：可能超出范围，或页面结构变了 —— 都要说出来，别静默当成"这页没模组"
					mu.Lock()
					failed++
					mu.Unlock()
					fmt.Printf("  第 %d 页解析出 0 条（结构变了？继续）\n", page)
				} else {
					_ = os.WriteFile(file, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
					mu.Lock()
					fetched++
					mu.Unlock()
				}
				time.Sleep(*delay)
			}
		}()
	}
	for p := *from; p <= last; p++ {
		select {
		case tasks <- p:
		case <-stop:
		}
	}
	close(tasks)
	wg.Wait()

	fmt.Printf("抓取完成：新抓 %d 页，跳过已有 %d 页，失败/空 %d 页（其中遇限流 %d 次）\n",
		fetched, skipped, failed, throttled)

	all := merge(*cache)
	fmt.Printf("缓存里现有 %d 个条目\n", len(all))

	// 被防火墙封了就**不写产物**：留着上一份完整的表，比产出一份看着正常、
	// 实际缺一半的表安全得多。缓存（page-*.tsv）都在，解封后重跑即可续。
	if bannedMsg != "" {
		fmt.Println("⛔ 本轮被中断，未写产物。解封后重跑同一条命令会自动续。")
		os.Exit(3)
	}
	if failed > 0 && !*allowPartial {
		fmt.Printf("⚠ 还有 %d 页没抓到，未写产物 —— 缺页会直接变成「某些模组搜不到」。\n", failed)
		fmt.Println("  确认要用这份残缺表，就加 -allow-partial。重跑会自动续。")
		os.Exit(2)
	}

	fmt.Printf("合并后共 %d 个条目（去重后）\n", len(all))
	if err := writeJSON(*out, all); err != nil {
		fmt.Println("写产物失败：", err)
		os.Exit(1)
	}
	fmt.Println("产物 →", *out)
}

// httpErr 带状态码，好把「被限流」和「页面真出问题」区分开：
// 前者要退避重试，后者重试也是白费时间。
type httpErr struct{ code int }

func (e *httpErr) Error() string { return "HTTP " + strconv.Itoa(e.code) }

// banMark 是对方防火墙封禁页的特征串。
//
// ⚠ 封禁页是 **HTTP 200**，不是 403 —— 只看状态码会把它当成正常页面，
// 结果是「每一页都解析出 0 条」然后产出一份残缺的表。必须按正文认。
const banMark = "你已被系统封禁"

func isBanned(body string) bool { return strings.Contains(body, banMark) }

func banSnippet(body string) string {
	i := strings.Index(body, banMark)
	if i < 0 {
		return ""
	}
	s := strings.Join(strings.Fields(body[i:]), " ")
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

func isThrottled(err error) bool {
	var he *httpErr
	if errors.As(err, &he) {
		return he.code == 429 || he.code == 503
	}
	return false
}

func fetch(u string) (string, error) {
	req, _ := http.NewRequest("GET", u, nil)
	// 带上自己的身份：万一抓出问题，对方能找得到人，而不是当成恶意爬虫
	req.Header.Set("User-Agent", "EnderBox/1.0 (Minecraft server manager; +https://github.com/su-ni/EnderBox)")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	c := &http.Client{Timeout: 25 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", &httpErr{code: resp.StatusCode}
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func detectLastPage() (int, error) {
	body, err := fetch(baseURL + "1")
	if err != nil {
		return 0, err
	}
	max := 0
	for _, m := range reLast.FindAllStringSubmatch(body, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	if max == 0 {
		return 0, fmt.Errorf("页面里找不到页码链接")
	}
	return max, nil
}

// parsePage 抽出一页里的 (id, 中文名, 英文名)。
// 中文名里的 [XX] 是百科给的缩写前缀，保留 —— 有人就是按缩写搜的。
func parsePage(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range reEntry.FindAllStringSubmatch(body, -1) {
		id := m[1]
		if seen[id] {
			continue
		}
		cn := clean(m[2])
		en := clean(m[3])
		if cn == "" {
			continue
		}
		seen[id] = true
		out = append(out, id+"\t"+cn+"\t"+en)
	}
	return out
}

func clean(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.Join(strings.Fields(s), " ")
}

func merge(cache string) []Entry {
	files, _ := filepath.Glob(filepath.Join(cache, "page-*.tsv"))
	byID := map[string]Entry{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				continue
			}
			if _, ok := byID[parts[0]]; ok {
				continue
			}
			byID[parts[0]] = Entry{ID: atoi(parts[0]), CN: parts[1], EN: parts[2]}
		}
	}
	ids := make([]string, 0, len(byID))
	for k := range byID {
		ids = append(ids, k)
	}
	sort.Slice(ids, func(i, j int) bool { return atoi(ids[i]) < atoi(ids[j]) })

	out := make([]Entry, 0, len(ids))
	for _, k := range ids {
		out = append(out, byID[k])
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// writeJSON 写成紧凑的 [[中文名, 英文名], ...]，百科 id 不进去 ——
// 产物只服务于「中文名 → 英文名」这一步，多一个字段就多一份体积。
func writeJSON(path string, all []Entry) error {
	rows := make([][2]string, 0, len(all))
	for _, e := range all {
		rows = append(rows, [2]string{e.CN, e.EN})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
