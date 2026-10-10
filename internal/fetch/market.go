package fetch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 在线模组市场（Modrinth）。
//
// 为什么把 base 做成可配置：api.modrinth.com 走 Cloudflare，国内会**间歇性**抽风 ——
// 实测同一台机器上一分钟 200 / 0.9s，下一分钟 15 秒超时。只留一条路的后果是
// 「PCL 能搜，这软件搜不了」，而用户只会归结为我们没写好。其实是路太少。
// 地址仍可在设置里改（排第一优先试），但默认就带一条兜底链。
//
// 顺便说清边界：这个包只负责「问源站要什么」，不负责「下到哪」——
// 下载仍旧走 fetch.Manager（多线程 / 断点续传 / 校验），和别的地方共用一套。

// DefaultMarketBase Modrinth 官方 API。
const DefaultMarketBase = "https://api.modrinth.com/v2"

// MirrorMarketBase 社区镜像（MCIM）。官方抽风时的兜底，接口结构一致可直接替换域名。
// ⚠ 只用它查元数据，别用它下文件：它的 CDN 实测会超时，下载仍走官方 cdn.modrinth.com。
const MirrorMarketBase = "https://mod.mcimirror.top/modrinth/v2"

// 榜单口径。空串 = 相关度（只在有关键词时才有意义；没关键词时相关度等于乱排）。
const (
	SortRelevance = "relevance"
	SortDownloads = "downloads"
	SortNewest    = "newest"
	SortUpdated   = "updated"
	SortFollows   = "follows"
)

// 源站线路。给用户一个「官方不通就走镜像」之外的**显式**选择 ——
// 他要能亲手把线路钉死，而不是只能听我们说「会自动换」。
const (
	SourceAuto     = "auto"     // 默认：用户配置的地址 → 官方 → 镜像，依次试
	SourceOfficial = "official" // 只用 api.modrinth.com
	SourceMirror   = "mirror"   // 只用社区镜像
)

// marketAttemptTimeout 单个源站的尝试上限。
// 三个源站串起来最坏 24 秒 —— 但下面 transport 里的 dial / header 超时会把
// 「连不上」这类挂在握手阶段的失败提前到 4~6 秒，实际很少走到上限。
const marketAttemptTimeout = 8 * time.Second

// marketClient 单独一个 client：市场请求可能被墙，超时要短。
// 用默认 client（无超时）会让「点搜索转圈转到天荒地老」变成一个真实体验。
//
// ⚠ ResponseHeaderTimeout 是这里的救命字段：Client.Timeout 只在「整体」到点才放弃，
// 而 Cloudflare 抽风时最典型的姿势是 TCP 连上了、TLS 握完了、然后 header 永远不来。
// 没有它，每次尝试都要吃满 8 秒；有了它，6 秒就换下一个源。
var marketClient = &http.Client{
	Timeout: marketAttemptTimeout,
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 6 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
		Proxy:                 http.ProxyFromEnvironment, // 用户挂了代理就走代理，别自作主张绕过
	},
}

// MarketHit 搜索结果里的一条。字段名按我们自己的口径来（源站叫 project_id，
// 这里统一成 id），前端只需要认一套名字。
type MarketHit struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Author      string   `json:"author,omitempty"`
	IconURL     string   `json:"iconUrl,omitempty"`
	Downloads   int64    `json:"downloads"`
	Follows     int64    `json:"follows,omitempty"`
	DateCreated string   `json:"dateCreated,omitempty"`
	DateUpdated string   `json:"dateUpdated,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	// ZhName 中文名（译名表里精确对得上才给）。市场本来只有英文名，
	// 列表里能同时看到中文名，找东西快得多。查不到就是空串，界面照旧显示英文名。
	ZhName string `json:"zhName,omitempty"`
	// ProjectType mod / modpack / resourcepack / shader。服务端只关心 mod。
	ProjectType string `json:"projectType,omitempty"`
	// ClientSide / ServerSide 源站给的「这一侧能不能用」：required / optional / unsupported。
	// 列表里直接标出来 —— 用户才不会把一个纯客户端模组装进服务端，等到启动崩了才发现。
	ClientSide string `json:"clientSide,omitempty"`
	ServerSide string `json:"serverSide,omitempty"`
}

// 行内 Markdown 标记：**粗体** / __粗体__ / `代码` / [文字](链接) / 行首的 # 标题号。
var (
	mdLinkRe    = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdHeadRe    = regexp.MustCompile(`(?m)^\s*#{1,6}\s*`)
	mdSpaceRe   = regexp.MustCompile(`[ \t]{2,}`)
	mdMarkerRep = strings.NewReplacer("**", "", "__", "", "`", "")
)

// plainText 把源站描述里的 Markdown 洗成纯文本。
//
// 为什么要在后端做、而不是让前端渲染成富文本：
// 这个项目的界面原则是「给界面看的字符串是纯文本」。描述是原样印在列表里的，
// 带着 ** 的话用户看到的是「** 这不是模组体验包」而不是加粗 —— 实测 Modrinth 上
// 确实有作者这么写（BetterArmy TACZ）。前端各剥一次也行，但那是把同一条规则抄到
// 模组市场、整合包市场两个界面上，总有一边会漏。
//
// ⚠ 只处理**成对出现**的双字符标记，**绝不碰单个 `*` 和 `_`**：
// 模组 id 里全是下划线（mezz_config、fabric_api），把单下划线当斜体吃掉会把名字改坏。
// 宁可留一点脏，也不能改坏内容 —— 而且「改坏」不会报错，只会让用户搜不到东西。
func plainText(s string) string {
	if s == "" {
		return s
	}
	// 没有标记就直接返回：绝大多数描述是干净的，不必为它们跑四条正则。
	if !strings.ContainsAny(s, "*_`[#") {
		return s
	}
	out := mdLinkRe.ReplaceAllString(s, "$1")
	out = mdHeadRe.ReplaceAllString(out, "")
	out = mdMarkerRep.Replace(out)
	out = mdSpaceRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// MarketVersion 一个可下载的版本
type MarketVersion struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	VersionNumber string   `json:"versionNumber"`
	ReleaseType   string   `json:"releaseType"` // release / beta / alpha
	GameVersions  []string `json:"gameVersions,omitempty"`
	Loaders       []string `json:"loaders,omitempty"`
	FileName      string   `json:"fileName"`
	FileURL       string   `json:"fileUrl"`
	Size          int64    `json:"size"`
	SHA1          string   `json:"sha1,omitempty"`
	// Downloads 这一版被下了多少次。列表页要显示它 —— 同一个模组几十个版本，
	// 用户挑的时候「哪个版本装的人多」是最有用的信息之一。
	Downloads int64 `json:"downloads,omitempty"`
	// DatePublished 这一版发布的 ISO 时间。界面上要显示成「1 年 2 个月前」这种相对时间。
	DatePublished string      `json:"datePublished,omitempty"`
	RequiredDeps  []MarketDep `json:"requiredDeps,omitempty"`
	// HasMrpack 这一版挂的文件里有 .mrpack 包本体。整合包市场靠它把「点了装不了」的版本
	// 从列表里剔掉 —— 一个装不上的版本在界面上就是一个死按钮。
	// 模组市场不看它（模组永远没有 .mrpack），所以是 omitempty，别给老响应平添字段。
	HasMrpack bool `json:"hasMrpack,omitempty"`
}

// MarketDep 版本声明的依赖。required 的得先装上，否则服务端起不来。
type MarketDep struct {
	ProjectID string `json:"projectId,omitempty"`
	VersionID string `json:"versionId,omitempty"`
	Kind      string `json:"kind"` // required / optional
}

func NormalizeMarketBase(base string) string {
	b := strings.TrimSpace(base)
	if b == "" {
		b = DefaultMarketBase
	}
	return strings.TrimRight(b, "/")
}

// MarketFallbacks 首选源站之后的兜底顺序。做成变量是为了测试能把它们全换成
// 连不上的地址，去断言「全都试过还失败」时错误信息是否可用 ——
// 写死的话那条路永远测不到（总有一个真源站会答上来）。
// 导出而不只是包内可见，是因为别的包（api 的整合包测试）也要把假源站钉成唯一一路 ——
// 不然 404 之后的兜底会真的打到 api.modrinth.com，测试就变成薛定谔的了。
var MarketFallbacks = []string{DefaultMarketBase, MirrorMarketBase}

// marketBases 返回「按顺序试」的源站列表。
// 用户在设置里配的地址永远排第一 —— 他既然专门配了，就是认定那个更好用，别抢他的决定。
func marketBases(base string) []string {
	out := make([]string, 0, 1+len(MarketFallbacks))
	seen := make(map[string]bool, 1+len(MarketFallbacks))
	add := func(b string) {
		b = NormalizeMarketBase(b)
		if seen[b] {
			return
		}
		seen[b] = true
		out = append(out, b)
	}
	add(base)
	for _, b := range MarketFallbacks {
		add(b)
	}
	return out
}

// marketBasesFor 按用户选的线路决定要试哪些源站。
// 选「仅官方」时不偷偷兜底 —— 那正是用户用来判断「官方到底通不通」的手段，
// 替他兜了底就永远测不出来。
func marketBasesFor(base, source string) []string {
	switch source {
	case SourceOfficial:
		return []string{DefaultMarketBase}
	case SourceMirror:
		return []string{MirrorMarketBase}
	default:
		return marketBases(base)
	}
}

// marketHint 网络出问题时的补充说明。
// 只说「请求失败」等于把用户丢在原地 —— 得告诉他这是常见可解的问题。
func marketHint(base string, err error) error {
	return fmt.Errorf("%w（源站 %s）", err, NormalizeMarketBase(base))
}

// ErrMarketNotFound 源站明确回了 404 的哨兵。它和「网络抽风」必须分得开：
// 前者是「源站活着，且说这个东西不存在」，调用方可以据此走下一步兜底
// （比如整合包升级拿版本号反查 id），或者直接告诉用户「下架了，回搜索页重挑」；
// 后者只能如实报错重试。报错给 502 会把「这个项目没了」说成「我们的故障」，
// 用户会去查网络，方向全错。
var ErrMarketNotFound = errors.New("源站说找不到这个项目（可能已下架）")

func marketGet(base, path string, q url.Values, out any) error {
	return marketGetT(base, path, q, out, marketAttemptTimeout)
}

// marketGetT 与 marketGet 一样，但超时可指定 —— 翻译这类「加分项」请求
// 要单独给一个更短的时限，不能跟主查询一样吃满 8 秒。
func marketGetT(base, path string, q url.Values, out any, timeout time.Duration) error {
	u := NormalizeMarketBase(base) + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	// Modrinth 要求带一个能识别调用方的 UA，否则可能被限流。
	req.Header.Set("User-Agent", "EnderBox/0.2 (Minecraft server panel)")
	req.Header.Set("Accept", "application/json")

	// 挂一个独立的 ctx：Client.Timeout 已经兜了总时长，但请求被取消时
	// 得保证底层连接真的放掉，别把空闲连接留在池里等下一次用（那会拿到半截响应）。
	ctx, cancel := context.WithTimeout(req.Context(), timeout)
	defer cancel()

	resp, err := marketClient.Do(req.WithContext(ctx))
	if err != nil {
		return marketHint(base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrMarketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return marketHint(base, fmt.Errorf("源站返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return marketHint(base, fmt.Errorf("看不懂源站返回的内容：%w", err))
	}
	return nil
}

// marketGetAny 依次试每个源站，谁先答上来用谁。
// 返回真正答上来的那个地址 —— 前端要显示「这次走的是哪个源」，
// 否则用户看到结果突然变了却不知道为什么，又会觉得这软件玄学。
func marketGetAny(bases []string, path string, q url.Values, out any) (string, error) {
	var errs []string
	notFound := 0
	for _, b := range bases {
		if err := marketGet(b, path, q, out); err == nil {
			return b, nil
		} else {
			errs = append(errs, err.Error())
			if errors.Is(err, ErrMarketNotFound) {
				notFound++
			}
		}
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("没有可用的源站")
	}
	// 每个源站都明确说「没这个东西」→ 直接把哨兵透出去，别再包成「市场连不上」。
	// 「已依次试过 N 个源站」对 404 是废话（东西哪儿都没有），而且会把归因带偏。
	if notFound == len(bases) {
		return "", ErrMarketNotFound
	}
	// 全都试过了：把第一个错误给用户看（最具代表性），并说清试过几个、是哪几个。
	// 「连不上市场」这种话没用 —— 用户要的是知道下一步能做什么。
	return "", fmt.Errorf("市场连不上：%s；已依次试过 %d 个源站（%s）都没成",
		errs[0], len(bases), strings.Join(bases, " → "))
}

// marketPost 发一个 JSON POST。目前只有「按文件哈希反查项目」用得上它 ——
// Modrinth 那个接口是 POST 且要 JSON body，GET 那条路走不通。
func marketPost(base, path string, body any, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, NormalizeMarketBase(base)+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "EnderBox/0.2 (Minecraft server panel)")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	ctx, cancel := context.WithTimeout(req.Context(), marketAttemptTimeout)
	defer cancel()

	resp, err := marketClient.Do(req.WithContext(ctx))
	if err != nil {
		return marketHint(base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return ErrMarketNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return marketHint(base, fmt.Errorf("源站返回 %d：%s", resp.StatusCode, strings.TrimSpace(string(msg))))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return marketHint(base, fmt.Errorf("看不懂源站返回的内容：%w", err))
	}
	return nil
}

// marketPostAny 依次试每个源站，谁先答上来用谁。
func marketPostAny(bases []string, path string, body any, out any) (string, error) {
	var errs []string
	for _, b := range bases {
		if err := marketPost(b, path, body, out); err == nil {
			return b, nil
		} else {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) == 0 {
		return "", fmt.Errorf("没有可用的源站")
	}
	return "", fmt.Errorf("市场连不上：%s；已依次试过 %d 个源站", errs[0], len(bases))
}

// FileHashesToProjects 按文件 sha1 反查「这个 jar 属于哪个项目」。
//
// 为什么用哈希而不是文件名 / modId：文件名可以随便改，modId 也不一定等于 Modrinth 的
// slug（有的带后缀）。**哈希是源站自己认的身份**，反查结果是确定的。
// 实测：fabric-api 的 sha1 直接命中 project_id P7dR8mSH。
//
// 返回 sha1 → project_id。**查不到的哈希不出现在结果里**（这不是错误）：
// 用户自己改过的 jar、或压根没发在 Modrinth 上的模组，本来就查不到 ——
// 调用方要按「缺失 = 没核出来」处理，不能当成「确认没问题」。
func FileHashesToProjects(base string, sha1s []string) (map[string]string, error) {
	clean := make([]string, 0, len(sha1s))
	seen := map[string]bool{}
	for _, h := range sha1s {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		clean = append(clean, h)
	}
	if len(clean) == 0 {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	const chunk = 500 // 官方上限 1000，留一倍余量
	for i := 0; i < len(clean); i += chunk {
		end := i + chunk
		if end > len(clean) {
			end = len(clean)
		}
		var res map[string]struct {
			ProjectID string `json:"project_id"`
		}
		body := map[string]any{"hashes": clean[i:end], "algorithm": "sha1"}
		if _, err := marketPostAny(marketBases(base), "/version_files", body, &res); err != nil {
			return nil, err
		}
		for h, v := range res {
			if v.ProjectID != "" {
				out[strings.ToLower(h)] = v.ProjectID
			}
		}
	}
	return out, nil
}

// MarketQuery 一次市场搜索的全部条件。
// 做成结构体而不是一长串位置参数：条件已经有 9 个了，而相邻好几个都是 string ——
// 传错顺序编译器不会拦，排查代价却很大。
type MarketQuery struct {
	Base     string // 用户配置的首选源站（空 → 官方）
	Query    string // 关键词，可空
	Loader   string // 加载器，可空
	MC       string // MC 版本，可空
	Category string // 分类，可空
	Sort     string // 榜单口径，空 = 按有无关键词自动决定
	Source   string // 线路：auto / official / mirror
	Offset   int
	Limit    int
	// ProjectType 项目类型，空 = "mod"（模组市场是绝大多数调用方，别逼它们每处都写一遍）。
	// 合法值 mod / modpack / resourcepack / shader。
	ProjectType string
	// ServerSide 服务端侧过滤，空 = 不筛。整合包市场默认传 required+optional，
	// 把「装到服务端根本没用的客户端包」挡在列表外（实测占整合包的 30%）。
	// ⚠ 同一档内的多个值之间是**或**的关系（Modrinth 的 facet 语义），不是与。
	ServerSide []string
}

// broadenQuery 给出搜索词的「放宽」写法：把下划线 / 连字符换成空格。
//
// 为什么需要它：已安装模组的 Forge / Fabric 依赖声明里写的是 modId，
// 形如 "mezz_config" / "curios_api"；而 Modrinth 的搜索把下划线当成分词断点 ——
// 实测 "mezz_config" 命中 0 条、"mezz config" 才命中 MezzConfig。
// 于是用户点「选版本装」之后，弹层里空空如也，看着就像点了没反应。
// 所以原词命中为空时，用放宽写法再搜一次；仍然为空才算真的没找到。
//
// 返回空串表示「没有另一种写法」，调用方不必重试。
func broadenQuery(q string) string {
	s := strings.TrimSpace(q)
	if s == "" {
		return ""
	}
	spaced := strings.NewReplacer("_", " ", "-", " ").Replace(s)
	spaced = strings.Join(strings.Fields(spaced), " ")
	if spaced == s {
		return ""
	}
	return spaced
}

// MarketSearch 按条件搜索 / 列榜单。
// Query / Loader / MC / Category 为空表示不按它筛（用户还没定下来时也能先看看）。
// Sort 为空时：有关键词走相关度，没关键词走下载量（否则「相关度」在无关键词时等于乱排）。
//
// 返回 (结果, 实际命中的源站地址, 错误)。
func MarketSearch(mq MarketQuery) ([]MarketHit, string, error) {
	limit := mq.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	offset := mq.Offset
	if offset < 0 {
		offset = 0
	}
	facets := make([][]string, 0, 5)
	pt := mq.ProjectType
	if pt == "" {
		pt = "mod" // 老调用方只关心模组，空值回落成 mod，行为与加这个字段之前完全一致
	}
	facets = append(facets, []string{"project_type:" + pt})
	if mq.Loader != "" {
		facets = append(facets, []string{"categories:" + mq.Loader})
	}
	if mq.MC != "" {
		facets = append(facets, []string{"versions:" + mq.MC})
	}
	if mq.Category != "" {
		facets = append(facets, []string{"categories:" + mq.Category})
	}
	if len(mq.ServerSide) > 0 {
		ss := make([]string, 0, len(mq.ServerSide))
		for _, v := range mq.ServerSide {
			if v = strings.TrimSpace(v); v != "" {
				ss = append(ss, "server_side:"+v)
			}
		}
		if len(ss) > 0 {
			facets = append(facets, ss)
		}
	}
	fb, _ := json.Marshal(facets)

	idx := mq.Sort
	if idx == "" {
		if strings.TrimSpace(mq.Query) == "" {
			idx = SortDownloads // 进来就有内容：默认给热门榜，而不是让人对着空搜索框发呆
		} else {
			idx = SortRelevance
		}
	}

	// 一次搜索请求抽成闭包：除了关键词以外其它条件都一样，
	// 原词落空要换写法重试时没必要重算一遍。
	fetch := func(query string) ([]MarketHit, string, error) {
		q := url.Values{}
		q.Set("query", query)
		q.Set("limit", fmt.Sprint(limit))
		q.Set("offset", fmt.Sprint(offset))
		q.Set("index", idx)
		q.Set("facets", string(fb))

		var res struct {
			Hits []struct {
				ProjectID   string   `json:"project_id"`
				Slug        string   `json:"slug"`
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Author      string   `json:"author"`
				IconURL     string   `json:"icon_url"`
				Downloads   int64    `json:"downloads"`
				Follows     int64    `json:"follows"`
				DateCreated string   `json:"date_created"`
				DateUpdated string   `json:"date_modified"`
				Categories  []string `json:"categories"`
				ProjectType string   `json:"project_type"`
				ClientSide  string   `json:"client_side"`
				ServerSide  string   `json:"server_side"`
			} `json:"hits"`
		}
		used, err := marketGetAny(marketBasesFor(mq.Base, mq.Source), "/search", q, &res)
		if err != nil {
			return nil, "", err
		}
		out := make([]MarketHit, 0, len(res.Hits))
		for _, h := range res.Hits {
			out = append(out, MarketHit{
				ID:          h.ProjectID,
				Slug:        h.Slug,
				Title:       plainText(h.Title),
				Description: plainText(h.Description),
				Author:      h.Author,
				IconURL:     h.IconURL,
				Downloads:   h.Downloads,
				Follows:     h.Follows,
				DateCreated: h.DateCreated,
				DateUpdated: h.DateUpdated,
				Categories:  h.Categories,
				ProjectType: h.ProjectType,
				ClientSide:  h.ClientSide,
				ServerSide:  h.ServerSide,
			})
		}
		return out, used, nil
	}

	// 先按原词搜；命中为空、且这词还有放宽写法（下划线版 modId 就是这样）时再试一次。
	// 空关键词（进市场默认的热门榜）不参与放宽 —— 那是「逛」，不是「找」。
	queries := []string{mq.Query}
	if b := broadenQuery(mq.Query); b != "" {
		queries = append(queries, b)
	}
	for i, query := range queries {
		items, used, err := fetch(query)
		if err != nil {
			return nil, "", err
		}
		if len(items) > 0 || i == len(queries)-1 {
			return items, used, nil
		}
	}
	return []MarketHit{}, "", nil
}

// marketFileRaw 源站版本里的文件条目
type marketFileRaw struct {
	URL      string `json:"url"`
	FileName string `json:"filename"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	Hashes   struct {
		SHA1 string `json:"sha1"`
	} `json:"hashes"`
}

// marketVersionRaw 源站 /project/{id}/version 与 /version/{id} 的原始结构
type marketVersionRaw struct {
	ID            string          `json:"id"`
	ProjectID     string          `json:"project_id"`
	Name          string          `json:"name"`
	VersionNumber string          `json:"version_number"`
	VersionType   string          `json:"version_type"`
	GameVersions  []string        `json:"game_versions"`
	Loaders       []string        `json:"loaders"`
	Files         []marketFileRaw `json:"files"`
	Downloads     int64           `json:"downloads"`
	DatePublished string          `json:"date_published"`
	Dependencies  []struct {
		ProjectID      string `json:"project_id"`
		VersionID      string `json:"version_id"`
		DependencyType string `json:"dependency_type"`
	} `json:"dependencies"`
}

// MarketVersions 列出某个项目的可下载版本，按加载器 / MC 版本筛。
func MarketVersions(base, projectID, loader, mc string) ([]MarketVersion, string, error) {
	if strings.TrimSpace(projectID) == "" {
		return nil, "", fmt.Errorf("缺少项目 id")
	}
	q := url.Values{}
	if loader != "" {
		b, _ := json.Marshal([]string{loader})
		q.Set("loaders", string(b))
	}
	if mc != "" {
		b, _ := json.Marshal([]string{mc})
		q.Set("game_versions", string(b))
	}
	var raw []marketVersionRaw
	used, err := marketGetAny(marketBases(base), "/project/"+url.PathEscape(projectID)+"/version", q, &raw)
	if err != nil {
		return nil, "", err
	}

	out := make([]MarketVersion, 0, len(raw))
	for _, v := range raw {
		f := primaryFile(v.Files)
		if f == nil {
			continue // 没有可下载文件的版本对用户没有意义，别塞进列表
		}
		mv := MarketVersion{
			ID:            v.ID,
			Name:          v.Name,
			VersionNumber: v.VersionNumber,
			ReleaseType:   v.VersionType,
			GameVersions:  v.GameVersions,
			Loaders:       v.Loaders,
			FileName:      f.FileName,
			FileURL:       f.URL,
			Size:          f.Size,
			SHA1:          f.SHA1,
			Downloads:     v.Downloads,
			DatePublished: v.DatePublished,
			HasMrpack:     hasMrpackFile(v.Files),
		}
		for _, d := range v.Dependencies {
			if d.DependencyType != "required" {
				continue
			}
			mv.RequiredDeps = append(mv.RequiredDeps, MarketDep{
				ProjectID: d.ProjectID,
				VersionID: d.VersionID,
				Kind:      d.DependencyType,
			})
		}
		out = append(out, mv)
	}
	return out, used, nil
}

// MarketFile 版本里的一个可下载文件。
//
// 单版本接口把整个文件清单带回来而不是只给 primary 那个，是因为整合包升级
// 要从 files[] 里挑 .mrpack 本体 —— 一个版本常同时挂着好几个文件
// （服务端包、客户端包、更新日志），primary 不一定落在包本体上，挑法由调用方定。
type MarketFile struct {
	URL      string `json:"url"`
	FileName string `json:"fileName"`
	Primary  bool   `json:"primary"`
	Size     int64  `json:"size"`
	SHA1     string `json:"sha1,omitempty"`
}

// MarketVersionDetail 单个版本的完整信息：MarketVersion 的口径 + 项目 id + 完整文件清单。
//
// ProjectID 单独带一份：整合包按版本号反查 id 之后就顺着记进 packstate，
// 下次升级才有了「这个实例来自哪个项目」的地基。
type MarketVersionDetail struct {
	MarketVersion
	ProjectID string       `json:"projectId,omitempty"`
	Files     []MarketFile `json:"files"`
}

// ErrVersionNotFound 源站（兜底链里至少一条活着的线路）明确说这个版本 id 不存在。
// 与「市场连不上」是两回事：前者调用方可以走下一步兜底，后者只能重试。
var ErrVersionNotFound = errors.New("市场里没有这个版本")

// MarketVersionByID 按版本 id 取单个版本（GET /version/{id}），
// 与 MarketVersions 走同一条兜底链、同一套类型口径。
//
// 404 的语义在兜底链里要小心：某一条线路 404 不代表版本不存在（镜像可能滞后），
// 所以 404 的线路记下来继续试下一条；全部线路都失败时，
// 只要**有过**一条活着的线路明确说 404，就按「版本不存在」报 ——
// 活着的源站给出了确定答案，比笼统的「连不上」对调用方有用得多。
func MarketVersionByID(base, id string) (*MarketVersionDetail, string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, "", fmt.Errorf("缺少版本 id")
	}
	var (
		errs     []string
		notFound bool
	)
	bases := marketBases(base)
	for _, b := range bases {
		var raw marketVersionRaw
		err := marketGet(b, "/version/"+url.PathEscape(id), nil, &raw)
		if err == nil {
			d := &MarketVersionDetail{
				MarketVersion: MarketVersion{
					ID:            raw.ID,
					Name:          raw.Name,
					VersionNumber: raw.VersionNumber,
					ReleaseType:   raw.VersionType,
					GameVersions:  raw.GameVersions,
					Loaders:       raw.Loaders,
					Downloads:     raw.Downloads,
					DatePublished: raw.DatePublished,
				},
				ProjectID: raw.ProjectID,
				Files:     make([]MarketFile, 0, len(raw.Files)),
			}
			if f := primaryFile(raw.Files); f != nil {
				d.FileName, d.FileURL, d.Size, d.SHA1 = f.FileName, f.URL, f.Size, f.SHA1
			}
			for _, f := range raw.Files {
				d.Files = append(d.Files, MarketFile{
					URL: f.URL, FileName: f.FileName, Primary: f.Primary, Size: f.Size, SHA1: f.Hashes.SHA1,
				})
			}
			return d, b, nil
		}
		if errors.Is(err, ErrMarketNotFound) {
			notFound = true
			continue
		}
		errs = append(errs, err.Error())
	}
	if notFound {
		return nil, "", ErrVersionNotFound
	}
	if len(errs) == 0 {
		return nil, "", fmt.Errorf("没有可用的源站")
	}
	return nil, "", fmt.Errorf("市场连不上：%s；已依次试过 %d 个源站（%s）都没成",
		errs[0], len(bases), strings.Join(bases, " → "))
}

// MarketCategory 分类栏里的一条。
type MarketCategory struct {
	Key   string `json:"key"`   // Modrinth 的 category 值，直接进 facets
	Label string `json:"label"` // 界面显示名
}

// MarketGameVersions 可选 MC 版本清单（只取正式版，新的在前）。
// 从源站取而不是在代码里写死 —— 写死的那份过半年就是错的，
// 而用户看到「最新版本里没有新版本」只会觉得这软件没人维护了。
func MarketGameVersions(base string, limit int) ([]string, error) {
	if limit <= 0 || limit > 200 {
		limit = 40
	}
	var raw []struct {
		Version     string `json:"version"`
		VersionType string `json:"version_type"`
	}
	if _, err := marketGetAny(marketBases(base), "/tag/game_version", nil, &raw); err != nil {
		return nil, err
	}
	// 源站已经按时间倒序给了，这里只过滤 + 截断。
	out := make([]string, 0, limit)
	for _, v := range raw {
		if v.VersionType != "release" {
			continue // 快照 / 预览版不进下拉框：选了也大概率没有模组支持
		}
		out = append(out, v.Version)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// CuratedCategories 分类栏的名单。**这份名单放在后端当唯一真相源** ——
// 之前在前端另写一份名单，结果后端能查的类和前端能点的类对不上，判据成了死代码，
// 用户点进去永远是空列表。这里写死是有意的：Modrinth 的分类值几年不变，
// 真变了也是改这一处。
func CuratedCategories() []MarketCategory {
	return []MarketCategory{
		{Key: "optimization", Label: "优化"},
		{Key: "utility", Label: "实用"},
		{Key: "library", Label: "依赖库"},
		{Key: "storage", Label: "存储"},
		{Key: "technology", Label: "科技"},
		{Key: "magic", Label: "魔法"},
		{Key: "worldgen", Label: "世界生成"},
		{Key: "mobs", Label: "生物"},
		{Key: "adventure", Label: "冒险"},
		{Key: "equipment", Label: "装备"},
		{Key: "food", Label: "食物"},
		{Key: "transportation", Label: "交通"},
		{Key: "economy", Label: "经济"},
		{Key: "management", Label: "管理"},
		{Key: "social", Label: "社交"},
		{Key: "cursed", Label: "奇葩"},
	}
}

// ── 中文简介 ──────────────────────────────────────────────
//
// Modrinth 的简介是英文的，用户原话「为什么全是英文」。MCIM 镜像站自己加了一个
// 翻译服务（不在 /modrinth/v2 前缀下），把社区维护的中文译文按 project_id 给出。
//
// ⚠ 它是**尽力而为**的，不是能不能用的前提：
// 实测 4 次里 3 次 0.3 秒返回、1 次 5 秒超时。拿不到译文就显示原文 ——
// 绝不能因为翻译没回来就让整屏结果出不来。

// TranslateBase 翻译服务的地址（注意：不带 /modrinth/v2 后缀）。
const TranslateBase = "https://mod.mcimirror.top"

// translateTimeout 单个翻译请求的上限。比主查询短：这是加分项，不值得等。
const translateTimeout = 4 * time.Second

// translateConcurrency 同时发出的翻译请求数。
// 一屏 20 条，全发出去既慢又容易被镜像站当成攻击。
const translateConcurrency = 6

type translateResp struct {
	ProjectID  string `json:"project_id"`
	Translated string `json:"translated"`
	Original   string `json:"original"`
}

// 译文档案。进程内缓存：翻页 / 改排序会反复问同一批项目，
// 每次都回源是在给一个已经会抽风的镜像站添堵。
var (
	translateMu    sync.Mutex
	translateCache = map[string]string{}
	translateAt    = map[string]time.Time{}
)

const translateTTL = 30 * time.Minute

func translateCached(id string) (string, bool) {
	translateMu.Lock()
	defer translateMu.Unlock()
	v, ok := translateCache[id]
	if !ok || time.Since(translateAt[id]) > translateTTL {
		return "", false
	}
	return v, true
}

func translatePut(id, v string) {
	translateMu.Lock()
	defer translateMu.Unlock()
	if len(translateCache) > 3000 { // 兜底：真跑很久也别把内存吃光
		translateCache = map[string]string{}
		translateAt = map[string]time.Time{}
	}
	translateCache[id] = v
	translateAt[id] = time.Now()
}

// MarketTranslate 批量取中文简介。
// 返回的 map **只包含真的有译文的项目** —— 没译文的不写 key，前端用原文兜底。
// 它不返回 error：翻译失败是常态里的一部分，报错会让调用方被迫在「显示英文」
// 和「整屏报错」之间二选一，而正确答案永远是前者。
func MarketTranslate(ids []string) map[string]string {
	out := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return out
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, translateConcurrency)

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if v, ok := translateCached(id); ok {
			if v != "" {
				mu.Lock()
				out[id] = v
				mu.Unlock()
			}
			continue
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var r translateResp
			q := url.Values{}
			q.Set("project_id", id)
			err := marketGetT(TranslateBase, "/translate/modrinth", q, &r, translateTimeout)
			// 没译文也记一笔（空串）：下次翻到同一屏就不用再问一遍。
			v := ""
			if err == nil {
				v = strings.TrimSpace(r.Translated)
			}
			translatePut(id, v)
			if v != "" {
				mu.Lock()
				out[id] = v
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	return out
}

// ── 依赖（前置）的项目信息 ────────────────────────────────
// Modrinth 的版本只给依赖的 project_id。以前界面只能写「有前置 2」，
// 用户点不动 —— 连前置叫什么名字都不知道，还谈什么装。

// MarketProject 一个项目的最基本信息，只用来把「一串 ID」变成「人能认的名字」。
type MarketProject struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	IconURL     string `json:"iconUrl,omitempty"`
	Description string `json:"description,omitempty"`
	// VersionCount 这个项目一共有多少个构建（**含**其它 MC 版本 / 加载器的）。
	// 界面上要拿它说清「还有多少个没列出来」—— 只列当前实例能用的那批，
	// 用户会以为这个模组就只有这几个版本。
	VersionCount int `json:"versionCount,omitempty"`
	// ClientSide / ServerSide 源站记的「这一侧能不能用」。
	// 整合包安装前拿它交叉核对 .mrpack 里的 env 标注 —— 作者标错是常态，
	// 而信错一次就是「装完启动崩」（真机案例：ETF 被当 required 装进 Forge 服务端）。
	ClientSide string `json:"clientSide,omitempty"`
	ServerSide string `json:"serverSide,omitempty"`
}

// MarketProjects 按 ID 批量取项目信息。Modrinth 支持一次问多个，
// 一条条问会让「看一眼前置是啥」变成等 10 秒。
func MarketProjects(base string, ids []string) ([]MarketProject, error) {
	clean := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		clean = append(clean, id)
	}
	if len(clean) == 0 {
		return []MarketProject{}, nil
	}
	// ⚠ ids 必须是 JSON 数组的字符串（["a","b"]），不是逗号分隔 ——
	// 写成逗号分隔源站会返回一个空数组，看着像「这些项目都不存在」。
	b, _ := json.Marshal(clean)
	q := url.Values{}
	q.Set("ids", string(b))

	var raw []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		IconURL     string   `json:"icon_url"`
		Description string   `json:"description"`
		Versions    []string `json:"versions"` // 只要个数，见 VersionCount
		ClientSide  string   `json:"client_side"`
		ServerSide  string   `json:"server_side"`
	}
	if _, err := marketGetAny(marketBases(base), "/projects", q, &raw); err != nil {
		return nil, err
	}
	out := make([]MarketProject, 0, len(raw))
	for _, p := range raw {
		out = append(out, MarketProject{
			ID: p.ID, Title: plainText(p.Title), IconURL: p.IconURL, Description: plainText(p.Description),
			VersionCount: len(p.Versions),
			ClientSide:   p.ClientSide,
			ServerSide:   p.ServerSide,
		})
	}
	return out, nil
}

// ── 项目详情 ──────────────────────────────────────────────
//
// 详情页要的东西比搜索结果多：正文（Markdown 原文）、引用链接、图集、许可证、支持的加载器 / MC 版本。
// 这些信息只有 GET /project/{id|slug} 一次性给全 —— 分多次去拼是在给一个已经会抽风的源站添堵。

// MarketLicense 许可证。三个字段都可能为空（作者没填），omitempty 让它退化成一个空对象而不是 null。
type MarketLicense struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

// MarketLink 一条「去别处看看」的链接。
type MarketLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// MarketLinks 项目挂着的外链。
//
// 合并成一个对象而不是散落四个字段：界面上是「相关链接」一栏，
// 前端只要遍历它，不会因为加了第五种链接就要改一次 JSX。
type MarketLinks struct {
	Issues   string       `json:"issues,omitempty"`
	Source   string       `json:"source,omitempty"`
	Wiki     string       `json:"wiki,omitempty"`
	Discord  string       `json:"discord,omitempty"`
	Donation []MarketLink `json:"donation,omitempty"`
}

// MarketGallery 图集里的一张。只留渲染需要的字段 ——
// 源站给的还有 featured / ordering / created / created，详情页用不上。
type MarketGallery struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}

// MarketProjectDetail 一个项目的完整面貌，给详情页用。
type MarketProjectDetail struct {
	ID      string `json:"id"`
	Slug    string `json:"slug,omitempty"`
	Title   string `json:"title"`
	Author  string `json:"author,omitempty"`
	IconURL string `json:"iconUrl,omitempty"`
	Color   int    `json:"color,omitempty"`
	// ZhName 中文名（译名表精确对得上才给），与列表页同一套口径。
	ZhName string `json:"zhName,omitempty"`

	Description string `json:"description"`
	// Body 项目正文的 **Markdown 原文**。
	//
	// ⚠ 这一处**故意不洗成纯文本**，与项目里其它所有字符串相反。
	// 原则「给界面看的字符串是纯文本」针对的是**列表 / 气泡 / 提示**那些直接印出来的地方；
	// 详情页要把作者的排版（标题、列表、代码块、折叠块）还原出来，洗掉等于替作者做决定。
	// 所以这里有代价：渲染端必须自己保证安全 —— 前端的 Markdown 渲染器
	// 全程不碰 innerHTML、只产出 React 元素，外来 HTML 走「先降级成 Markdown 再解析」
	// 的标签白名单（详情见 web/src/markdown.tsx）。
	Body string `json:"body"`

	ProjectType  string   `json:"projectType,omitempty"`
	Categories   []string `json:"categories,omitempty"`
	Loaders      []string `json:"loaders,omitempty"`
	GameVersions []string `json:"gameVersions,omitempty"`
	ClientSide   string   `json:"clientSide,omitempty"`
	ServerSide   string   `json:"serverSide,omitempty"`

	Downloads     int64  `json:"downloads"`
	Follows       int64  `json:"follows,omitempty"`
	DatePublished string `json:"datePublished,omitempty"`
	DateModified  string `json:"dateModified,omitempty"`

	License MarketLicense   `json:"license"`
	Links   MarketLinks     `json:"links"`
	Gallery []MarketGallery `json:"gallery,omitempty"`

	// VersionCount 这个项目一共发过多少个版本（含其它 MC 版本 / 加载器的）。
	// 详情页只列出当前能装的那些，得有一句话说清「还有 N 个没列出来」。
	VersionCount int `json:"versionCount,omitempty"`
	// WebURL 站上的页面地址。用户要「跳转链接」看原介绍 —— 让后端算而不是前端拼：
	// 拼错了没人会发现（链接看起来是对的），而 mod 与 modpack 的路径段不一样。
	WebURL string `json:"webUrl,omitempty"`
}

// projectWebURL 站上的项目页地址。projectType 决定路径段（mod / modpack / resourcepack / shader）。
func projectWebURL(slug, projectType string) string {
	if slug == "" {
		return ""
	}
	seg := strings.TrimSpace(projectType)
	if seg == "" {
		seg = "mod"
	}
	return "https://modrinth.com/" + seg + "/" + url.PathEscape(slug)
}

// FetchProjectDetail 按 id 或 slug 取单个项目的完整信息。
//
// 叫 Fetch* 而不是 Market*：这个包里类型名与取数函数名原本互不冲突（MarketHit / MarketSearch），
// 但项目详情那个类型已经占了 MarketProjectDetail，同名函数会让编译器直接拒编。
//
// 返回 (详情, 实际命中的源站地址, 错误)。id 找不到时返回 ErrMarketNotFound ——
// 详情页拿到它要说「这个包可能下架了」，而不是让用户去查网络。
//
// Author 是尽力而为的额外一步：项目响应里只有 team id，要名字得再问一次 /team/{id}/members。
// 问不到就留空 —— 详情页少了作者名字照样成立，不该因为这一个字段整屏出不来。
func FetchProjectDetail(base, idOrSlug string) (*MarketProjectDetail, string, error) {
	idOrSlug = strings.TrimSpace(idOrSlug)
	if idOrSlug == "" {
		return nil, "", fmt.Errorf("缺少项目 id 或 slug")
	}

	var raw struct {
		ID           string   `json:"id"`
		Slug         string   `json:"slug"`
		Title        string   `json:"title"`
		Description  string   `json:"description"`
		Body         string   `json:"body"`
		Team         string   `json:"team"`
		ProjectType  string   `json:"project_type"`
		IconURL      string   `json:"icon_url"`
		Color        int      `json:"color"`
		Downloads    int64    `json:"downloads"`
		Followers    int64    `json:"followers"`
		Published    string   `json:"published"`
		Updated      string   `json:"updated"`
		Categories   []string `json:"categories"`
		Loaders      []string `json:"loaders"`
		Versions     []string `json:"versions"`
		GameVersions []string `json:"game_versions"`
		ClientSide   string   `json:"client_side"`
		ServerSide   string   `json:"server_side"`
		Gallery      []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
		} `json:"gallery"`
		License struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"license"`
		IssuesURL    string `json:"issues_url"`
		SourceURL    string `json:"source_url"`
		WikiURL      string `json:"wiki_url"`
		DiscordURL   string `json:"discord_url"`
		DonationURLs []struct {
			Platform string `json:"platform"`
			URL      string `json:"url"`
		} `json:"donation_urls"`
	}

	used, err := marketGetAny(marketBases(base), "/project/"+url.PathEscape(idOrSlug), nil, &raw)
	if err != nil {
		return nil, "", err
	}

	d := &MarketProjectDetail{
		ID:            raw.ID,
		Slug:          raw.Slug,
		Title:         plainText(raw.Title),
		Author:        marketTeamOwner(base, raw.Team),
		IconURL:       raw.IconURL,
		Color:         raw.Color,
		Description:   plainText(raw.Description),
		Body:          strings.TrimSpace(raw.Body),
		ProjectType:   raw.ProjectType,
		Categories:    raw.Categories,
		Loaders:       raw.Loaders,
		GameVersions:  raw.GameVersions,
		ClientSide:    raw.ClientSide,
		ServerSide:    raw.ServerSide,
		Downloads:     raw.Downloads,
		Follows:       raw.Followers,
		DatePublished: raw.Published,
		DateModified:  raw.Updated,
		License: MarketLicense{
			ID:   raw.License.ID,
			Name: raw.License.Name,
			URL:  raw.License.URL,
		},
		Links: MarketLinks{
			Issues:  raw.IssuesURL,
			Source:  raw.SourceURL,
			Wiki:    raw.WikiURL,
			Discord: raw.DiscordURL,
		},
		Gallery:      make([]MarketGallery, 0, len(raw.Gallery)),
		VersionCount: len(raw.Versions),
		WebURL:       projectWebURL(raw.Slug, raw.ProjectType),
	}
	d.ZhName = ZhNameFor(d.Title, d.Slug)
	for _, g := range raw.Gallery {
		if g.URL == "" {
			continue
		}
		d.Gallery = append(d.Gallery, MarketGallery{URL: g.URL, Title: g.Title})
	}
	for _, dn := range raw.DonationURLs {
		if dn.URL == "" {
			continue
		}
		label := strings.TrimSpace(dn.Platform)
		if label == "" {
			label = "赞助"
		}
		d.Links.Donation = append(d.Links.Donation, MarketLink{Label: label, URL: dn.URL})
	}
	return d, used, nil
}

// marketTeamOwner 取团队里那个 Owner 的用户名。
//
// 尽力而为：这一步失败返回空串，**不影响详情页本身**。
// 作者名字是「锦上添花」，让它决定整页能不能出来是把事情做反了。
func marketTeamOwner(base, teamID string) string {
	teamID = strings.TrimSpace(teamID)
	if teamID == "" {
		return ""
	}
	var members []struct {
		Role string `json:"role"`
		User struct {
			Username string `json:"username"`
			Name     string `json:"name"`
		} `json:"user"`
	}
	// 用一条短超时单独问：它的结果不值得让用户多等一秒。
	if err := marketGetT(NormalizeMarketBase(base), "/team/"+url.PathEscape(teamID)+"/members", nil,
		&members, 3*time.Second); err != nil {
		return ""
	}
	first := ""
	for _, m := range members {
		who := strings.TrimSpace(m.User.Name)
		if who == "" {
			who = strings.TrimSpace(m.User.Username)
		}
		if who == "" {
			continue
		}
		if strings.EqualFold(m.Role, "Owner") {
			return who
		}
		if first == "" {
			first = who
		}
	}
	return first
}

type marketFile struct {
	URL      string
	FileName string
	Size     int64
	SHA1     string
}

func primaryFile(files []marketFileRaw) *marketFile {
	for _, f := range files {
		if f.Primary && f.URL != "" {
			return &marketFile{URL: f.URL, FileName: f.FileName, Size: f.Size, SHA1: f.Hashes.SHA1}
		}
	}
	// 没有标 primary 的（老项目）就取第一个 —— 总比告诉用户「没有文件」强
	if len(files) > 0 && files[0].URL != "" {
		f := files[0]
		return &marketFile{URL: f.URL, FileName: f.FileName, Size: f.Size, SHA1: f.Hashes.SHA1}
	}
	return nil
}

// hasMrpackFile 这一版里有没有可下载的 .mrpack 本体。
//
// 按**扩展名**判，不按 primary 判：实测整合包的 primary 确实总落在 .mrpack 上（抽查 19/19），
// 但那是上游的习惯不是承诺 —— 哪天作者把更新日志标成 primary，按 primary 判就会把
// 一个本来能装的版本从列表里剔掉，用户看到的是「这个包没版本可装」，查都没法查。
func hasMrpackFile(files []marketFileRaw) bool {
	for _, f := range files {
		if f.URL != "" && strings.EqualFold(filepath.Ext(f.FileName), ".mrpack") {
			return true
		}
	}
	return false
}
