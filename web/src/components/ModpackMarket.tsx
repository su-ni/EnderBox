import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type MarketHit, type MarketSort, type MarketSource } from '../api'
import { Icon } from './design'
import { PageFrame } from './PageFrame'
import { MOD_LOADER_TAGS, catLabel } from './projectMeta'
import { Empty, SkeletonList, fmtAgo } from './ui'

const PAGE = 20

/** 加载器。值是 Modrinth 的 categories facet 取值，不是显示名。 */
const LOADERS: { k: string; label: string }[] = [
  { k: 'forge', label: 'Forge' },
  { k: 'fabric', label: 'Fabric' },
  { k: 'neoforge', label: 'NeoForge' },
  { k: 'quilt', label: 'Quilt' },
]

/**
 * 排序。⚠ **故意没有「相关度」这一档**：
 * 后端在 sort 为空时本来就是「有关键词→按相关度，没关键词→按热门榜」。
 * 把「相关度」单列出来，用户空着关键词去点它，拿到的是一份没有排序依据的名单 ——
 * 那不是他要的东西，而他只会觉得「这软件排序坏了」。
 */
const SORTS: { k: MarketSort; label: string }[] = [
  { k: '', label: '自动' },
  { k: 'downloads', label: '下载量' },
  { k: 'updated', label: '最近更新' },
  { k: 'newest', label: '最新发布' },
  { k: 'follows', label: '最多关注' },
]

/** 数据源。仅官方 / 仅镜像**有意不兜底** —— 用户正是拿它判断哪条线不通。 */
const SOURCES: { k: MarketSource; label: string; hint: string }[] = [
  { k: 'auto', label: '自动', hint: '官方不通就自动换镜像，拿不准就留这个' },
  { k: 'official', label: '仅 Modrinth 官方', hint: '只走官方线路 —— 用它判断官方到底是通还是不通' },
  { k: 'mirror', label: '仅国内镜像', hint: '只走镜像线路 —— 用它判断镜像到底是好还是坏' },
]

/** 大数字压缩：1.2k / 2.4M —— 市场卡片上的口径（设计稿 mk-stats 就这样写） */
function fmtCount(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k'
  return String(n)
}

/** 卡片副标题：作者 · 前两个用途类分类（加载器名不是分类，滤掉；分类名翻中文） */
function cardSub(h: MarketHit): string {
  const cats = (h.categories ?? []).filter((c) => !MOD_LOADER_TAGS.has(c)).slice(0, 2)
  return [h.author, ...cats.map(catLabel)].filter(Boolean).join(' · ')
}

/**
 * 一屏列表的完整条件。列表只认它 —— 输入框里打了字还没回车时，列表不该跟着动。
 *
 * 加载器 / 游戏版本 / 排序 / 数据源这四个，后端从一开始就支持，
 * 但界面以前一个入口都没给（`api.modpackSearch` 里连参数拼串都写好了）。
 * 后端有刀子、界面没刀柄，用户只能靠关键词硬凑 —— 这就是补这一块的原因。
 */
type Filters = {
  q: string
  all: boolean
  loader: string
  mc: string
  sort: MarketSort
  src: MarketSource
}

// 深链：?pkq= 关键词 / ?pkall=1 连客户端包一起搜 / ?pkld= 加载器 / ?pkmc= 游戏版本 /
// ?pksort= 排序 / ?pksrc= 数据源。
// 无头浏览器点不了「搜索」，把交互态预置进 URL 是这个项目验证 UI 的惯例。
//
// ⚠ 深链只证明得了「读参数 + 首查」这条链是通的，**证不了「点一下下拉列表当场就换」**。
// 验这四个筛选时要走真交互（原型 value setter + 冒泡 change，见 F:/tmp/p1/verify3.mjs），
// 而且判据得看**结果集合变了没**——只看「已筛」那行文字等于没验（界面自己写死两个字也能显示）。
function readFilters(): Filters {
  const p = new URLSearchParams(location.search)
  return {
    q: p.get('pkq') ?? '',
    all: p.get('pkall') === '1',
    loader: p.get('pkld') ?? '',
    mc: p.get('pkmc') ?? '',
    sort: (p.get('pksort') ?? '') as MarketSort,
    src: (p.get('pksrc') ?? 'auto') as MarketSource,
  }
}

/** 「已筛：加载器 Forge、MC 1.20.1」这种一行摘要。没有筛选就是空串，不渲染。 */
function filterSummary(f: Filters): string {
  const parts: string[] = []
  const ld = LOADERS.find((l) => l.k === f.loader)
  if (ld) parts.push(`加载器 ${ld.label}`)
  if (f.mc) parts.push(`MC ${f.mc}`)
  return parts.join('、')
}

/**
 * 整合包市场：**只到列表为止**。挑中了某个包就进它自己的详情页（ModpackDetail）。
 *
 * 为什么不就地展开：试过，坑不在样式而在「用户不知道发生了什么」——
 * 展开的那一块排在 20 条结果下面一两千像素处，点了跟没点一样；
 * 替他滚上去的话，滚动又把「我刚才在看第几行」抹掉了。详见 ModpackDetail 顶上的说明。
 *
 * 这一屏负责的三件事：搜得到、筛得准、看得清哪一个值得点进去。
 * 「看庄 / 挑版本 / 一键安装」全在详情页。
 */
export function ModpackMarket({
  onOpenPack,
  onCancel,
}: {
  /** 挑中一个包 → 进它的详情页。传的是项目 id（详情/)页认 id 也认 slug） */
  onOpenPack: (projectId: string) => void
  /** 返回世界列表 */
  onCancel: () => void
}) {
  const [name, setName] = useState(() => readFilters().q)
  /** 已生效的条件。列表只认它 —— 输入框里打了字还没回车时，列表不该跟着动。 */
  const [applied, setApplied] = useState<Filters>(readFilters)
  const all = applied.all

  /** MC 版本下拉的选项：真列表来自 `/api/mods/game-versions`，
   *  不是写死几条 —— 写死的话装到新版本时这个筛选就成了摆设。 */
  const [mcList, setMcList] = useState<string[]>([])

  const [hits, setHits] = useState<MarketHit[]>([])
  const [base, setBase] = useState('')
  const [mirrored, setMirrored] = useState(false)
  /** 后端如实回报「这次滤没滤客户端包」。界面要说清，别让用户以为列出来的都能装。 */
  const [onlyServer, setOnlyServer] = useState(true)
  const [zhQuery, setZhQuery] = useState<{ from: string; matched: string; english: string } | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [hasMore, setHasMore] = useState(false)

  // 迟到的响应别覆盖新结果：连点「搜索」「加载更多」会同时飞好几个请求。
  const seq = useRef(0)
  // 「加载更多」的游标。用 ref 不用 state：它只被 fetchList 自己读，
  // 放进 state 会让 fetchList 每次分页都重建一遍（进而把挂载时的那次加载也重跑）。
  const offsetRef = useRef(0)

  const fetchList = useCallback(async (f: Filters, more: boolean) => {
    const my = ++seq.current
    const off = more ? offsetRef.current : 0
    setLoading(true)
    setErr('')
    try {
      const kw = f.q.trim()
      const r = await api.modpackSearch({
        q: kw,
        // 有关键词 → 相关度；没关键词 → 热门榜（下载量）。
        // 不能空着关键词还按相关度排 —— 那等于乱排（后端也是这么兜的，这里显式说清意图），
        // 但用户**显式选了排序**就听他的，别再自作主张。
        sort: f.sort || (kw ? 'relevance' : ''),
        loader: f.loader,
        mc: f.mc,
        src: f.src,
        offset: off,
        limit: PAGE,
        all: f.all,
      })
      if (my !== seq.current) return
      const items = r.items ?? []
      setHits((prev) => (more ? [...prev, ...items] : items))
      setBase(r.base ?? '')
      setMirrored(!!r.mirrored)
      setOnlyServer(r.onlyServer !== false)
      setZhQuery(r.zh ?? null)
      offsetRef.current = off + items.length
      setHasMore(items.length >= PAGE)
      // 条件写回 URL：刷新 / 分享 / 截图核对能回到同一屏
      const u = new URL(location.href)
      const put = (k: string, v: string) => {
        if (v) u.searchParams.set(k, v)
        else u.searchParams.delete(k)
      }
      put('view', 'market')
      put('pkq', kw)
      put('pkall', f.all ? '1' : '')
      put('pkld', f.loader)
      put('pkmc', f.mc)
      // 排序和来源的「默认值」不写进 URL：写了会让每次进页面都像带着一层筛选
      put('pksort', f.sort)
      put('pksrc', f.src === 'auto' ? '' : f.src)
      history.replaceState(null, '', u)
    } catch (e) {
      if (my !== seq.current) return
      setErr((e as Error).message)
      if (!more) setHits([])
    } finally {
      if (my === seq.current) setLoading(false)
    }
  }, [])

  // 进页面就拉热门榜。用户原话（模组市场那边）：「全是空的只有搜索框，你让我怎么用？」
  useEffect(() => {
    void fetchList(applied, false)
    // 只在挂载时跑一次，之后由用户触发
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // MC 版本下拉的选项。拉失败就只留「全部」——**不能编几条假的填进去**：
  // 用户按一个不存在的版本去筛，看到空列表会以为市场里没这个版本的包。
  useEffect(() => {
    let alive = true
    api
      .modGameVersions()
      .then((r) => {
        if (alive) setMcList(r.items ?? [])
      })
      .catch(() => {
        if (alive) setMcList([])
      })
    return () => {
      alive = false
    }
  }, [])

  const submit = (next: Filters) => {
    setApplied(next)
    // 重置要把输入框也清掉；点筛选时 next.q 就是框里那份，等于没动
    setName(next.q)
    // 换条件回到顶部：接着看的是「另一个搜索结果」，还停在半中间会让人以为列表没刷新
    offsetRef.current = 0
    void fetchList(next, false)
  }

  /**
   * 下拉 / 勾选类条件**点一下就生效**，不用再按「搜索」。
   *
   * 为什么不跟模组市场那样攒到「搜索」再提交：这一屏的「含客户端包」本来就是即时的，
   * 同一个页面上两种脾气是最糟的 —— 用户会把「没反应」记在这一屏上，而不是记在某个控件上。
   * 而且筛选不是危险操作，没有需要「确认了再执行」的理由。
   *
   * ⚠ q 取的是输入框里的**当前值**，不是 applied 里那份：用户打了字还没回车就去点加载器，
   * 他想筛的显然是刚打的这个词。（顺序要紧：先铺 applied 再覆盖 q，写反了 q 会被旧值盖掉。）
   */
  const applyPatch = (patch: Partial<Filters>) => submit({ ...applied, q: name, ...patch })

  return (
    // PageFrame 才是「一屏页面」的正主：滚动容器、内边距、错误边界（含 ?crash=1 自检）都在它里面。
    // pad：本页搜索面板是 .panel（转角标探出 2px），内容又全贴 main 左右缘 ——
    // 没这 4px 侧衬，右侧转角的竖笔画会被裁得只剩 1px（1440 和手机档都能看到）。
    <PageFrame title="整合包市场" pad>
      {/* 标题行：设计稿的 sec-head（像素 idx + 名称 + 一句范围声明 + 右侧动作） */}
      <div className="sec-head">
        <span className="idx">MARKET</span>
        <h2>整合包市场</h2>
        <span className="sub">市场只负责「搜得到、筛得准」，挑版本和安装都在详情页</span>
        <div className="spacer" />
        <button type="button" className="btn btn-ghost sm" onClick={onCancel}>
          <Icon name="chev-l" />
          返回世界列表
        </button>
      </div>

      <div className="nbox">
        这里选出来的整合包会<b>新建一个世界</b>
        （不会改动任何已有实例）。<b>点一张卡进它的详情页</b>
        —— 作者写在站上的说明、支持的版本、以及「一键安装」都在那一页。
      </div>

      {/* ── 搜索条件：设计稿的 panel + toolbar ── */}
      <div className="panel mt-3">
        <div className="toolbar">
          {/* 窄屏整行占满，按钮换到下一行 —— 和按钮挤在一行的话，520 宽下输入框只剩不到 200px，
              占位符都被截掉，用户看不清该填什么。 */}
          <div className="flex min-w-[240px] flex-1 items-center gap-2">
            <Icon name="search" style={{ color: 'var(--ink-3)' }} />
            <input
              className="inp"
              value={name}
              placeholder="搜整合包，例如 create / 机械动力"
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit({ ...applied, q: name })
              }}
            />
          </div>
          <button
            type="button"
            className="btn sm"
            disabled={loading}
            onClick={() => submit({ ...applied, q: name })}
          >
            搜索
          </button>
          <button
            type="button"
            className="btn btn-ghost sm"
            disabled={loading}
            onClick={() =>
              // 重置是「回到什么都没筛」：连加载器 / 版本 / 排序 / 来源一起回默认。
              // 只清关键词的话，用户按了重置却发现列表还是被 MC 1.20.1 卡着 —— 那不算重置。
              submit({ q: '', all: false, loader: '', mc: '', sort: '', src: 'auto' })
            }
          >
            <Icon name="refresh" />
            重置
          </button>
          {/* 「含客户端包」：默认关。开了之后列表里会有装了没用的纯客户端包 —— 但偶尔
              你会想找的那个包就是被滤掉了，得留一条能翻出来的路。 */}
          <button
            type="button"
            className={`chipf${all ? ' on' : ''}`}
            aria-pressed={all}
            title="开了之后连纯客户端包一起列出来；那些装到服务端上没用，所以默认滤掉"
            onClick={() => applyPatch({ all: !all })}
          >
            含客户端包
          </button>
        </div>

        {/* 加载器 / 游戏版本：动态长列表走下拉 —— 设计稿只演示了排序与来源（短枚举才用 chipf），
            这两个筛选后端一直支持但界面以前没入口，按同一套语言补在这里。 */}
        <div className="toolbar" style={{ borderTop: 0 }}>
          <span className="eyebrow">LOADER</span>
          <select
            className="px-input"
            value={applied.loader}
            onChange={(e) => applyPatch({ loader: e.target.value })}
          >
            <option value="">全部加载器</option>
            {LOADERS.map((l) => (
              <option key={l.k} value={l.k}>
                {l.label}
              </option>
            ))}
          </select>
          <span className="eyebrow">MC</span>
          <select
            className="px-input"
            value={applied.mc}
            onChange={(e) => applyPatch({ mc: e.target.value })}
          >
            <option value="">全部版本</option>
            {/* ⚠ 当前选中的那个必须**始终可选**：深链带了 ?pkmc=1.18.2 而版本清单还没回来
                （或者刚好没列到这个版本）时，缺了它浏览器会把下拉显示成「全部版本」——
                界面写着没筛、实际筛了，那是最容易被当成「搜索结果不对」的一种状态。 */}
            {applied.mc && !mcList.includes(applied.mc) && (
              <option value={applied.mc}>{applied.mc}（当前）</option>
            )}
            {mcList.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </div>

        {/* 排序 / 来源：短枚举，点一下就重查 —— 设计稿的 SORT / SOURCE 两排标签 */}
        <div className="toolbar" style={{ borderTop: 0 }}>
          <span className="eyebrow">SORT</span>
          <div className="filters">
            {SORTS.map((s) => (
              <button
                key={s.k || 'auto'}
                type="button"
                className={`chipf${applied.sort === s.k ? ' on' : ''}`}
                title={s.k === '' ? '有关键词按相关度，没关键词按热门榜' : undefined}
                onClick={() => applyPatch({ sort: s.k })}
              >
                {s.label}
              </button>
            ))}
          </div>
          <div className="spacer" style={{ marginLeft: 'auto' }} />
          <span className="eyebrow">SOURCE</span>
          <div className="filters">
            {SOURCES.map((s) => (
              <button
                key={s.k}
                type="button"
                className={`chipf${applied.src === s.k ? ' on' : ''}`}
                title={s.hint}
                onClick={() => applyPatch({ src: s.k })}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      {/* 中文关键词被后端换成英文名的那次改写。一定要显示 —— 换错的时候用户得能一眼看出
          「它不是搜不到，是换错了」，而不是当成软件坏了。 */}
      {zhQuery && (
        <p className="mono-meta mt-3">
          中文关键词「{zhQuery.from}」按译名当成 <b className="text-ink-1">{zhQuery.english}</b> 去搜的
          {zhQuery.matched && zhQuery.matched !== zhQuery.english ? `（命中「${zhQuery.matched}」）` : ''}。
          搜不到就别用中文，直接填英文名。
        </p>
      )}

      {/* 过滤状态如实回报。开了「连纯客户端包一起搜」还说「都是能装的」就是骗人 ——
          那 30% 装到服务端上一点用都没有。
          已经设了筛选也要说：不然「只有 3 条」会被当成市场里就这点东西。 */}
      {!loading && !err && hits.length > 0 && (
        <p className="mono-meta mt-3">
          共列出 {hits.length} 个包
          {onlyServer ? '（只含服务端能装的，纯客户端包已被滤掉）' : '（包含纯客户端包，点进去可能装不了）'}
          {filterSummary(applied) && ` · 已筛：${filterSummary(applied)}`}
          {base && mirrored && ` · 结果来自镜像源 ${base}`}
        </p>
      )}

      {/* ── 结果 ── */}
      {err && (
        <div className="nbox d mt-3">
          <p>{err}</p>
          <button
            type="button"
            className="btn sm mt-2.5"
            onClick={() => void fetchList(applied, false)}
          >
            <Icon name="refresh" />
            重试
          </button>
        </div>
      )}

      {loading && hits.length === 0 && <SkeletonList rows={4} />}

      {!loading && !err && hits.length === 0 && (
        // 空态要分得清三种「没有」——「筛窄了」「被服务端滤掉了」「真没有」，
        // 三者的下一步完全不同。只说一句「没数据」，用户会反复改关键词，
        // 而真正该做的是把筛选放宽或者把那个开关打开。
        <Empty>
          {filterSummary(applied) ? (
            <>
              当前带着筛选（{filterSummary(applied)}）在「服务端能装的」范围里没搜到。
              先把加载器 / 游戏版本改成「全部」，再决定是不是真的没有。
            </>
          ) : !onlyServer ? (
            <>这个条件下市场里没有整合包。换个说法，或者清空关键词看热门榜。</>
          ) : (
            <>
              在「服务端能装的」这个范围里没搜到。你想找的包有可能只发了客户端包
              —— 点右上角的「含客户端包」再试一次。
            </>
          )}
        </Empty>
      )}

      {hits.length > 0 && (
        // key 要带上筛选：只带 q 的话，换了加载器列表不重挂载，入场动画不重放，
        // 用户会觉得「点了筛选但列表没变」—— 内容其实换了，只是没有任何反馈。
        <div
          className="mk-grid mt-3 stagger"
          key={`${applied.q}|${applied.all}|${applied.loader}|${applied.mc}|${applied.sort}|${applied.src}`}
        >
          {hits.map((h) => {
            /*
              标签只挑「用途类」的展示，且最多两个。
              Modrinth 的 categories 里混着加载器名（forge/fabric/neoforge）——
              那是筛选项，不是这个包的分类，混进标签里只是噪声。
            */
            const hwCats = (h.categories ?? []).filter((c) => !MOD_LOADER_TAGS.has(c)).slice(0, 3)
            const sub = cardSub(h)
            return (
              /*
                整卡可点：内容用设计稿的 div/h3/p（mk-card 的 CSS 就指着这些标签），
                可点性与键盘可达靠末尾铺满的透明按钮（.mk-hit，见 design-overrides.css）。
              */
              <div className="mk-card" key={h.id}>
                <div className="mk-top">
                  <div className="mk-ico">
                    {h.iconUrl ? (
                      <img src={h.iconUrl} alt="" loading="lazy" />
                    ) : (
                      <Icon name="cube" />
                    )}
                  </div>
                  <div className="tx">
                    {/* 中文名给人认，英文名给人核对 —— 只有译名表精确对上的才有中文名 */}
                    <h3>{h.zhName || h.title}</h3>
                    {sub && <p>{sub}</p>}
                  </div>
                </div>
                <p className="mk-desc">{h.description}</p>
                {hwCats.length > 0 && (
                  <div className="mk-tags">
                    {hwCats.map((c) => (
                      <span key={c} className="badge">
                        {catLabel(c)}
                      </span>
                    ))}
                  </div>
                )}
                <div className="mk-stats">
                  <span>
                    <b>{fmtCount(h.downloads)}</b> 下载
                  </span>
                  {!!h.follows && (
                    <span>
                      <b>{fmtCount(h.follows)}</b> 关注
                    </span>
                  )}
                  {h.dateUpdated && <span>{fmtAgo(h.dateUpdated)}更新</span>}
                </div>
                <button
                  type="button"
                  className="mk-hit"
                  onClick={() => onOpenPack(h.id)}
                  aria-label={`查看 ${h.zhName || h.title} 的详情`}
                  title={h.description}
                />
              </div>
            )
          })}
        </div>
      )}

      {hits.length > 0 && !err && (
        <div className="mt-3 flex items-center gap-3">
          {hasMore ? (
            <button
              type="button"
              className="btn sm"
              disabled={loading}
              onClick={() => void fetchList(applied, true)}
            >
              {loading ? '载入中…' : '加载更多'}
            </button>
          ) : (
            <span className="mono-meta">已经到底了（共 {hits.length} 条）</span>
          )}
        </div>
      )}
    </PageFrame>
  )
}
