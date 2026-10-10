package fetch

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"unicode"
)

/*
中文关键词 → 英文名。

为什么需要这一层：在线市场的搜索索引（Modrinth）里**没有中文译名**。
中文能不能搜到，只取决于模组作者有没有自己把中文写进标题/简介 ——
实测「机械动力」只能搜到几个衍生模组、正主 Create 搜不到，
「暮色森林 / 旅行地图 / 物品管理器 / 通用机械」更是 0 条。
所以我们得自己备一层对照表：把中文换成英文名，再去搜。

数据从哪来：MC百科（mcmod.cn）的「模组检索」页，我们自己的生成器抓的，
见 tools/mcmodsnap。**为什么自己抓而不是拿现成的**：那份数据本来就是 MC百科 的公开内容，
直接抓上游既干净（不用背别人启动器的使用条款），又能随时重跑刷新。
*/

//go:embed mcmod_zh.json
var zhRaw []byte

type zhEntry struct {
	cn  string // 原始中文名（含百科的 [缩写] 前缀，如 "[IC2] 工业时代2"）
	en  string // 英文名，可能为空（百科没填）
	key string // 归一化后的中文名，用来比对
	a   string // 归一化后的简称（剥掉 [XX] 前缀后的部分，用于「完全匹配」判定）
}

var (
	zhOnce sync.Once
	zhList []zhEntry
)

func zhLoad() []zhEntry {
	zhOnce.Do(func() {
		var rows [][2]string
		if err := json.Unmarshal(zhRaw, &rows); err != nil {
			// 快照坏了就当它不存在：中文搜索退化成原样搜，不能把整个市场带崩
			return
		}
		zhList = make([]zhEntry, 0, len(rows))
		for _, r := range rows {
			cn, en := strings.TrimSpace(r[0]), strings.TrimSpace(r[1])
			key := zhNorm(cn)
			if key == "" {
				continue
			}
			zhList = append(zhList, zhEntry{cn: cn, en: en, key: key, a: zhNorm(zhStripTag(cn))})
		}
	})
	return zhList
}

// zhStripTag 去掉百科的 "[IC2] " 这类缩写前缀。
func zhStripTag(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[") {
		return s
	}
	if i := strings.Index(s, "]"); i >= 0 {
		return strings.TrimSpace(s[i+1:])
	}
	return s
}

// zhNorm 归一化：小写、只留汉字/字母/数字。
// 把空格和标点都抹掉，「JEI 物品管理器」和「jei物品管理器」才会走到同一条路上。
func zhNorm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hasCJK(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// ResolveZh 把中文关键词换成英文名。
//
// 返回按「把握从大到小」排序的候选，调用方顺着试即可：
// 第一个搜不出东西时再试第二个，比一上来就挑一个然后认死理强。
// 只在关键词含汉字时才值得调用。
func ResolveZh(query string) []ZhCandidate {
	q := strings.TrimSpace(query)
	if q == "" || !hasCJK(q) {
		return nil
	}
	nq := zhNorm(q)
	if nq == "" {
		return nil
	}

	type scored struct {
		e     zhEntry
		tier  int // 3 = 简称完全一致，2 = 互相包含
		extra int // 多出来的字数，越少越像正主
	}
	var hits []scored
	for _, e := range zhLoad() {
		tier, extra := 0, 0
		switch {
		case e.a == nq:
			tier = 3 // 「物品管理器」对上「[JEI] 物品管理器」
		case strings.Contains(e.a, nq), strings.Contains(nq, e.a):
			// ⚠ 「互相包含」这一档必须有长度护栏，否则会把一句普通的中文里的
			// 两三个字当成模组名：「一个根本不存在的模组名字啊啊啊」里含「存在」，
			// 于是整句被换成 Existence —— 用户以为搜的是自己打的那句，实际搜的是
			// 我们编出来的另一个词。乱改比不改糟得多。
			//
			// 护栏：短的那边至少 2 个字，且长的不超过短的两倍。
			// 「暮色森林模组」(6) 对「暮色森林」(4) 照样能过；
			// 「存在」(2) 对 14 个字的整句过不了。
			short, long := len(nq), len(e.a)
			if short > long {
				short, long = long, short
			}
			if short < 2 || long > short*2 {
				continue
			}
			tier = 2
			extra = len(e.a) - len(nq)
			if extra < 0 {
				extra = -extra
			}
		default:
			continue
		}
		hits = append(hits, scored{e: e, tier: tier, extra: extra})
	}
	if len(hits) == 0 {
		return nil
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].tier != hits[j].tier {
			return hits[i].tier > hits[j].tier
		}
		// 同一档里**字数越少越优先**：正主一般叫得短，
		// 附属爱在后面挂后缀 ——「暮色森林」和「斯巴达的武器：暮色森林」同时命中时，
		// 要的是前者。（不这么排就会像常见做法那样锁到附属上。）
		if hits[i].extra != hits[j].extra {
			return hits[i].extra < hits[j].extra
		}
		// 有英文名的优先：没英文名就换不出东西，等于白排
		iEN, jEN := hits[i].e.en != "", hits[j].e.en != ""
		if iEN != jEN {
			return iEN
		}
		return hits[i].e.cn < hits[j].e.cn // 兜底，保证结果稳定可复现
	})

	out := make([]ZhCandidate, 0, 3)
	seen := map[string]bool{}
	for _, h := range hits {
		if h.e.en == "" {
			continue // 换不出英文名，试它没意义
		}
		en := strings.TrimSpace(h.e.en)
		if seen[en] {
			continue
		}
		seen[en] = true
		out = append(out, ZhCandidate{From: q, Matched: h.e.cn, English: en})
		if len(out) == 3 {
			break
		}
	}
	return out
}

// ZhCandidate 一条「中文 → 英文」的解析结果。
// 带上 Matched（百科上的中文名）是有意的：界面可以据它说明
// 「已按 暮色森林 → Twilight Forest 搜索」—— 改错了用户一眼能看出来。
type ZhCandidate struct {
	From    string `json:"from"`
	Matched string `json:"matched"`
	English string `json:"english"`
}

// ZhSnapshotSize 快照里的条目数（给自检/诊断用）。
func ZhSnapshotSize() int { return len(zhLoad()) }

var (
	zhRevOnce sync.Once
	zhRev     map[string]string // 归一化英文名 → 中文名
)

func zhRevLoad() map[string]string {
	zhRevOnce.Do(func() {
		zhRev = make(map[string]string, 1024)
		for _, e := range zhLoad() {
			if e.en == "" {
				continue
			}
			k := zhNorm(e.en)
			if k == "" {
				continue
			}
			if _, dup := zhRev[k]; !dup {
				zhRev[k] = e.cn
			}
		}
	})
	return zhRev
}

// ZhNameFor 反查中文名：拿市场里的英文标题（或 slug）问「它中文叫什么」。
//
// ⚠ 只做**归一化后完全相等**的匹配，不做包含匹配。
// 这是**展示**用的名字：把「Twilight Forest - Dungeons & Villages」的中文名显示成「暮色森林」，
// 比不显示严重得多 —— 用户会拿这个中文名判断该装哪个。
// 查不到就返回空串，界面老实显示英文名，绝不猜。
//
// 覆盖率取决于快照完整度：截至 2026-10-05 只有 4603 条（约 15%），
// 所以热门榜首 300 里只有 16% 能配上中文名。表抓全之后这个比例会明显上去。
func ZhNameFor(title, slug string) string {
	rev := zhRevLoad()
	if v, ok := rev[zhNorm(title)]; ok {
		return v
	}
	if slug != "" {
		if v, ok := rev[zhNorm(slug)]; ok {
			return v
		}
	}
	return ""
}

// DecorateZhNames 给一批搜索结果补中文名（能补的才补，补不了留空）。
func DecorateZhNames(hits []MarketHit) {
	for i := range hits {
		hits[i].ZhName = ZhNameFor(hits[i].Title, hits[i].Slug)
	}
}
