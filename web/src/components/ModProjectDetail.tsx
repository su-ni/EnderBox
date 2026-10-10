import { useCallback, useEffect, useMemo, useState } from 'react'
import { api, type MarketHit, type MarketProjectDetail, type MarketVersion, type McmodRelations } from '../api'
import { renderMarkdown } from '../markdown'
import type { Instance } from '../types'
import { Icon } from './design'
import McmodGraph from './McmodGraph'
import { ExtLink, ProjectHead, ProjectShell, RailRow, RailSection } from './ProjectShell'
import { MOD_LOADER_TAGS, catLabel, licenseLabel, loaderLabel, releaseLabel, sideLabel } from './projectMeta'
import { Empty, SkeletonList, fmtAgo, fmtSize } from './ui'

/*
  百科名 → 市场搜索候选（「快捷安装」用）。
  「钠 (Sodium)」按整串搜是搜不到钠本体的（实测返回的是一堆汉化 / 检测模组）；
  「[RSO] xxx」这类前缀同样干扰。依次给出：原文 → 去掉 [前缀]/(后缀) → 括号里的拉丁名。
*/
function searchQueryVariants(raw: string): string[] {
  const out: string[] = []
  const push = (s: string) => {
    const t = s.trim()
    if (t && !out.includes(t)) out.push(t)
  }
  push(raw)
  push(raw.replace(/^\[[^\]]*\]\s*/, '').replace(/[（(][^）)]*[）)]\s*$/, ''))
  const m = raw.match(/[（(]\s*([A-Za-z][\w'’\- .:&!]*?)\s*[）)]/)
  if (m) push(m[1])
  return out
}

/**
 * 模组详情（市场侧的独立一屏）。
 *
 * 以前这里只有一张「版本列表」：一行英文标题 + 一段简介 + 一堆版本。
 * 用户原话是「详情如此简陋」—— 其实分类、下载量、更新时间、支持的加载器、
 * 作者正文，源站全都给了，是我们没画出来。
 *
 * 现在整页版式和整合包详情**共用同一套外壳**（ProjectShell）：
 * 抬头一行 + 页内页签 介绍/下载 + 左正文右信息栏 + 版心居中。
 * 整合包与模组在 Modrinth 上本来就是同一类对象（project），没道理长成两个样子。
 *
 * 和整合包详情唯一的结构性差别：**模组没有「预览再安装」那一步**。
 * 整合包是一棵要解包、要挑服务端启动文件的树，装之前必须让人看一眼会装进去什么；
 * 模组就是一个 jar，落进 mods/ 就完事 —— 多一道预览只会让每次装模组都多两次点击。
 */
export function ModProjectDetail({
  project,
  instance,
  loader,
  mc,
  onBack,
  backLabel = '返回搜索结果',
  onToast,
  onStarted,
  onOpenDep,
}: {
  /** Modrinth 项目 id 或 slug */
  project: string
  /** 装到哪个实例的 mods/plugins 目录 */
  instance: Instance
  /** 当前实例对应的 Modrinth 加载器分类（原版为空 = 不筛） */
  loader: string
  /** 当前实例的 MC 版本 */
  mc: string
  onBack: () => void
  /** 返回键文案。从「已安装」跳进来时是「返回已安装」。 */
  backLabel?: string
  onToast: (msg: string) => void
  onStarted: (taskId: string, label: string) => void
  /** 打开「装前置」弹层（交给调用方渲染，这样它和列表用的是同一份 loader/mc） */
  onOpenDep: (projectId: string, name: string) => void
}) {
  const [d, setD] = useState<MarketProjectDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [base, setBase] = useState('')
  const [mirrored, setMirrored] = useState(false)

  /** 中文简介。拿不到就显示原文 —— 翻译是加分项，不是能不能装的门槛。 */
  const [zhDesc, setZhDesc] = useState('')

  const [vers, setVers] = useState<MarketVersion[] | null>(null)
  const [vErr, setVErr] = useState('')
  const [verFilter, setVerFilter] = useState('')
  const [busyId, setBusyId] = useState('')

  /** 这个项目一共有多少个构建（含别的加载器 / MC 版本）。 */
  const [totalVers, setTotalVers] = useState(0)
  /** 其他构建：**点了开关才拉**。JEI 那种有三千多个，一进页面就拉不划算。 */
  const [allVers, setAllVers] = useState<MarketVersion[] | null>(null)
  const [loadingAll, setLoadingAll] = useState(false)

  /** 前置的**真名**。只写「有前置 2」不是提示，是谜题。 */
  const [depNames, setDepNames] = useState<Record<string, string>>({})

  /**
   * MC 百科关系星图。
   *
   * **进页面就自动抓**（用户明确要求：「就算是重复进去，也不需要点击就可以自动加载」）。
   * 敢自动抓是因为后端有三级缓存（内存 30 分钟 → 磁盘 7 天 → 联网）：第一次抓完落盘，
   * 之后再进同一个模组是**读本地**，不会再打百科的服务器 —— 这才是「自动加载」不冒犯人家的前提。
   * 名字配不上 / 站方没画图时后端返 404，这里如实显示，不静默留白。
   */
  const [relations, setRelations] = useState<{
    state: 'idle' | 'loading' | 'ok' | 'err'
    data?: McmodRelations
    msg?: string
  }>({ state: 'idle' })

  type Tab = 'desc' | 'dl'
  const [tab, setTab] = useState<Tab>(() =>
    new URLSearchParams(location.search).get('ptab') === 'dl' ? 'dl' : 'desc',
  )

  // 切页同步到 URL：刷新 / 分享要能停在同一页，无头验证也点不了这个按钮 ——
  // 和 ?pk= / ?ptab= 同一个套路，交互态一律可寻址。
  const goto = useCallback((t: Tab) => {
    setTab(t)
    const u = new URL(location.href)
    if (t === 'desc') u.searchParams.delete('ptab')
    else u.searchParams.set('ptab', t)
    history.replaceState(null, '', u)
  }, [])

  const load = useCallback(async () => {
    setLoading(true)
    setErr('')
    setVers(null)
    setVErr('')
    setAllVers(null)
    setVerFilter('')
    try {
      const r = await api.modProject(project)
      setD(r.item ?? null)
      setBase(r.base ?? '')
      setMirrored(!!r.mirrored)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
    // 版本同时拉，不串行等详情 —— 两个请求互不依赖。
    // 详情挂了也仍然拉版本：版本列表是「能不能装」的答案，不该被简介的故障连坐。
    try {
      const items = (await api.modVersions(project, loader, mc)).items ?? []
      setVers(items)
    } catch (e) {
      setVErr((e as Error).message)
      setVers([])
    }
  }, [project, loader, mc])

  useEffect(() => {
    void load()
  }, [load])

  // 中文简介与总构建数：都是锦上添花，失败就空着，不该影响整页能不能出来。
  useEffect(() => {
    let alive = true
    api
      .modTranslate([project])
      .then((r) => {
        if (alive) setZhDesc(r?.items?.[project] ?? '')
      })
      .catch(() => {})
    api
      .modProjects([project])
      .then((r) => {
        if (alive) setTotalVers(r?.items?.[0]?.versionCount ?? 0)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [project])

  // 版本里出现过的前置 id → 真名
  const depIds = useMemo(() => {
    const s = new Set<string>()
    for (const v of vers ?? []) {
      for (const dep of v.requiredDeps ?? []) {
        if (dep.projectId && dep.projectId !== project) s.add(dep.projectId)
      }
    }
    return [...s]
  }, [vers, project])
  const depKey = depIds.join(',')
  useEffect(() => {
    if (!depKey) {
      setDepNames({})
      return
    }
    let alive = true
    api
      .modProjects(depKey.split(','))
      .then((r) => {
        if (!alive) return
        const m: Record<string, string> = {}
        for (const p of r?.items ?? []) m[p.id] = p.title
        setDepNames(m)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [depKey])

  /**
   * 抓 MC 百科的关系星图。名字优先用**中文名** —— 百科是中文站，中文名命中率高得多；
   * 拿不到中文名才退回英文标题。
   * force=true 时跳过后端缓存重抓（给「页面一直开着 → 定时刷新」用）。
   */
  const loadMcmod = useCallback(
    async (force = false) => {
      const name = (d?.zhName || d?.title || '').trim()
      if (!name) {
        setRelations({ state: 'err', msg: '还不知道这个模组叫什么，没能去百科查' })
        return
      }
      setRelations((s) => (s.state === 'ok' ? s : { state: 'loading' })) // 已有图就别闪成 loading
      try {
        const r = await api.mcmodRelations(name, force)
        setRelations({ state: 'ok', data: r })
      } catch (e) {
        setRelations({ state: 'err', msg: (e as Error).message })
      }
    },
    [d?.zhName, d?.title],
  )

  // 详情一到手就抓星图（用户要求「不用点就自动加载」）。名字没好就等下一轮 ——
  // 这个 effect 依赖 name，名字一就绪它自己会再跑
  const mcmodName = (d?.zhName || d?.title || '').trim()
  useEffect(() => {
    if (!mcmodName) return
    if (relations.state !== 'idle') return
    void loadMcmod()
  }, [mcmodName, relations.state, loadMcmod])

  // 页面一直开着 → 每 10 分钟静默重抓一次（走 force，让百科那边的新关系能进来）。
  // 只在「已经成功拿到图」之后才起定时器：一直在报错的模组没必要每 10 分钟骚扰一次。
  useEffect(() => {
    if (relations.state !== 'ok') return
    const t = window.setInterval(() => void loadMcmod(true), 10 * 60 * 1000)
    return () => window.clearInterval(t)
  }, [relations.state, loadMcmod])

  const install = async (v: MarketVersion) => {
    setBusyId(v.id)
    try {
      const t = await api.pluginInstallURL(instance.id, {
        url: v.fileUrl,
        filename: v.fileName,
        sha1: v.sha1,
        size: v.size,
        label: `${d?.title || project} ${v.versionNumber}`,
      })
      const need = (v.requiredDeps ?? []).filter((x) => x.projectId && x.projectId !== project)
      onToast(
        need.length > 0
          ? `已开始下载 ${v.fileName} —— 这个版本还要 ${need.length} 个前置，下面那一栏可以顺手装上`
          : `已开始下载 ${v.fileName}`,
      )
      onStarted(t.id, d?.title || project)
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusyId('')
    }
  }

  /*
    ▍星图面板上的「快捷安装」（2026-10-10 重写：名字候选 + 两段搜索）
    拿百科名去市场搜项目，命中就弹版本层 —— 复用装前置那套弹层（onOpenDep → 上层
    DepQueueDialog），不另造一条安装路径。

    ⚠ 旧版只搜「实例 loader+mc 过滤」一次 —— 用户反馈「点击快捷安装通通提示找不到」：
      Modrinth 的 loader/版本是**硬过滤**（forge/1.20.1 会把 Fabric 系如钠整类砍掉），
      而百科名常带装饰（「钠 (Sodium)」「[RSO] …」）也让关键词命中率骤降，两层叠加几乎必 0。
    现在：① 名字先展开候选（原文 / 去装饰 / 括号里的拉丁名）；
          ② 先带过滤搜（能直接装的优先）；③ 全空则摘掉过滤再搜 —— 项目找到了，
          弹层会如实说「没有匹配你版本的构建」，比笼统的「找不到」准确得多。
    ⚠ 只认**名字精确一致**（title / zhName 归一化后相等），不做「取第一个结果」的瞎猜 ——
      宁可说找不到，也不能弹一个错项目的版本列表让用户装错东西。
  */
  const quickInstall = async (n: { name: string }) => {
    const variants = searchQueryVariants(n.name)
    const norm = (s: string) => s.toLowerCase().replace(/[\s\u3000·・]/g, '')
    const pickExact = (items: MarketHit[]) =>
      items.find((h) => variants.some((v) => norm(h.title) === norm(v) || norm(h.zhName || '') === norm(v)))
    try {
      for (const v of variants) {
        const hit = pickExact((await api.modSearch(v, loader, mc, { limit: 8 })).items ?? [])
        if (hit) {
          onOpenDep(hit.id, n.name)
          return
        }
      }
      for (const v of variants) {
        const hit = pickExact((await api.modSearch(v, '', '', { limit: 8 })).items ?? [])
        if (hit) {
          onOpenDep(hit.id, n.name)
          return
        }
      }
      onToast(`市场里没搜到和「${n.name}」精确对应的项目 —— 它可能不在 Modrinth 上，去它的 MC 百科页面找找`)
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const showAll = () => {
    if (allVers || loadingAll) return
    setLoadingAll(true)
    api
      .modVersions(project, '', '', true)
      .then((r) => setAllVers(r.items ?? []))
      .catch((e) => onToast((e as Error).message))
      .finally(() => setLoadingAll(false))
  }

  /*
    其他构建按「加载器 + MC 版本」分组折叠。
    平铺三千行是堵墙；折叠之后一眼能看到「这个模组还在维护哪些 MC 版本」。
    用原生 <details>：不用写展开状态，键盘和读屏也都认得。
  */
  const otherGroups = useMemo(() => {
    const order: string[] = []
    const byKey: Record<string, { label: string; vers: MarketVersion[] }> = {}
    for (const v of allVers ?? []) {
      for (const l of v.loaders?.length ? v.loaders : ['—']) {
        for (const g of v.gameVersions?.length ? v.gameVersions : ['—']) {
          const k = `${l}|${g}`
          if (!byKey[k]) {
            byKey[k] = { label: `${loaderLabel(l)} ${g}`, vers: [] }
            order.push(k)
          }
          byKey[k].vers.push(v)
        }
      }
    }
    return order.map((k) => ({ key: k, ...byKey[k] }))
  }, [allVers])

  /* 这个项目支持哪些加载器 / 哪些 MC —— 从各版本汇总，不用额外请求 */
  const loaders = useMemo(() => {
    const s: string[] = []
    for (const v of vers ?? []) {
      for (const l of v.loaders ?? []) if (!s.includes(l)) s.push(l)
    }
    return s
  }, [vers])

  /*
    可选 MC 版本。**按首次出现的顺序取**：源站返回的版本列表本来就是新的在前，
    所以第一个出现的就是最新的 —— 不用再写一套版本号比较（那种比较一改格式就错）。
  */
  const mcChoices = useMemo(() => {
    const s: string[] = []
    for (const v of vers ?? []) {
      for (const g of v.gameVersions ?? []) if (!s.includes(g)) s.push(g)
    }
    return s
  }, [vers])

  const shownVers = verFilter
    ? (vers ?? []).filter((v) => (v.gameVersions ?? []).includes(verFilter))
    : (vers ?? [])

  /* 标签只挑用途类的，且最多两个：categories 里混着加载器名，那是筛条件不是分类 */
  const hwCats = (d?.categories ?? []).filter((c) => !MOD_LOADER_TAGS.has(c))
  const otherCount = Math.max(0, totalVers - (vers?.length ?? 0))

  // 正文渲染要记忆化：解析几万字节的值不到每回重渲染都跑一遍。
  // 依赖写已经落成字符串的 `bodySrc` —— 写 `d?.body` 那种表达式，React 编译器认不出它是稳定的。
  const bodySrc = d?.body ?? ''
  const body = useMemo(() => (bodySrc ? renderMarkdown(bodySrc) : null), [bodySrc])

  return (
    <ProjectShell
      onBack={onBack}
      backLabel={backLabel}
      err={err}
      onRetry={() => void load()}
      loading={loading || !d}
      head={
        d ? (
          <ProjectHead
            eyebrow="MOD"
            iconUrl={d.iconUrl}
            title={d.zhName || d.title}
            /* 英文名给人核对 —— 降一级而不是藏起来：去源站查的时候要对得上 */
            origTitle={d.zhName ? d.title : undefined}
            meta={
              [
                d.author ? `作者 ${d.author}` : '',
                d.license?.id ? `许可 ${licenseLabel(d.license.id)}` : '',
              ]
                .filter(Boolean)
                .join(' · ') || undefined
            }
            description={zhDesc || d.description}
            stats={
              <>
                {d.downloads.toLocaleString()} 次下载
                {!!d.follows && ` · ${d.follows.toLocaleString()} 人关注`}
                {d.dateModified && ` · ${fmtAgo(d.dateModified)}更新`}
              </>
            }
            /* 抬头右侧的主行动：滚到「下载」页自己去挑一版。
               不直接装最新版 —— 同一个模组几十个构建，装错版本在这台服务器上不生效。 */
            action={
              <button
                type="button"
                className="btn btn-primary sm mt-0.5 shrink-0"
                title="去下载页挑一个版本再装"
                onClick={() => goto('dl')}
              >
                <Icon name="dl" size={12} />
                安装
              </button>
            }
          />
        ) : null
      }
      tabs={[
        { id: 'desc', label: '介绍' },
        {
          id: 'dl',
          label: '下载',
          badge: vers && vers.length > 0 ? `${vers.length} 个版本` : undefined,
        },
      ]}
      tab={tab}
      onTab={(id) => goto(id as Tab)}
      navLabel="模组详情分页"
      desc={
        /* ⚠ 正文这一层要 `md-wide`：全局 `.md-body` 卡在 72ch，
           而这里左边这一栏本来就比 72ch 宽，不放开的话正文右边又会空出一条。 */
        <div className="md-wide mt-4">
          {body ?? (
            <p className="mono-meta">
              这个模组的作者没有在源站写详细正文，只有抬头那段简介。
              右栏「外链」里有它在源站的原页，那里通常更全。
            </p>
          )}
        </div>
      }
      dl={
        <>
          <div className="sec-head mt-5 mb-3">
            <span className="idx">VERSION</span>
            <h2>挑一个版本</h2>
            <span className="sub">
              只列出这台服务器能用的（加载器与 MC 版本都对得上），装了必定生效
            </span>
          </div>

          {/*
            MC 版本筛选。
            ⚠ **只在它真能筛掉东西时才显示。**
            后端取版本列表时已经带了 loader + mc（只列「你这台服务器能用的」），
            所以经常出现「9 个版本全都声明 1.20 和 1.20.1」—— 这时候点哪个都一样，
            摆出来就是个**假控件**（点了没反应，用户只会以为坏了）。
          */}
          {mcChoices.length > 1 &&
            mcChoices.some(
              (g) =>
                (vers ?? []).filter((v) => (v.gameVersions ?? []).includes(g)).length <
                (vers?.length ?? 0),
            ) && (
              <div className="filters mb-3">
                <span className="eyebrow" style={{ marginRight: 4 }}>MC 版本</span>
                <button
                  type="button"
                  className={`fchip${verFilter === '' ? ' on' : ''}`}
                  onClick={() => setVerFilter('')}
                >
                  全部 <span className="opacity-70">{vers?.length ?? 0}</span>
                </button>
                {mcChoices.map((g) => {
                  const n = (vers ?? []).filter((v) => (v.gameVersions ?? []).includes(g)).length
                  return (
                    <button
                      key={g}
                      type="button"
                      className={`fchip${verFilter === g ? ' on' : ''}`}
                      onClick={() => setVerFilter(g)}
                    >
                      {g} <span className="opacity-70">{n}</span>
                    </button>
                  )
                })}
              </div>
            )}

          {!vers && !vErr && <SkeletonList rows={3} icon={false} />}
          {vErr && (
            <div className="nbox d">
              <p>{vErr}</p>
            </div>
          )}
          {vers && vers.length === 0 && !vErr && (
            <Empty>这个模组没有匹配当前加载器 / MC 版本的构建。换个版本，或者去别的站找找。</Empty>
          )}

          {/* 版本列表：设计稿 mod.html 的 vlist/vrow（132px 版本号 / 标记与文件信息 / 下载数 / 安装） */}
          {vers && vers.length > 0 && (
            <div className="vlist">
              {shownVers.map((v) => {
                const deps = (v.requiredDeps ?? []).filter(
                  (dep) => dep.projectId && dep.projectId !== project,
                )
                return (
                  <div key={v.id}>
                    <div
                      className="vrow"
                      style={{ gridTemplateColumns: '132px minmax(0,1fr) auto auto', cursor: 'default' }}
                    >
                      <span className="ver">{v.versionNumber}</span>
                      <span className="min-w-0">
                        <span className="flex flex-wrap items-center gap-1.5">
                          {v.releaseType !== 'release' && (
                            <span className="chip s w">{releaseLabel(v.releaseType)}</span>
                          )}
                          {deps.length > 0 && (
                            <span className="chip s w" title="这个版本还要求先装别的模组">
                              有前置 {deps.length}
                            </span>
                          )}
                        </span>
                        <span className="mono-meta mt-1 block break-all">
                          {v.fileName} · {fmtSize(v.size)}
                        </span>
                        {/*
                          这一行放「挑版本时真正要比的东西」：
                          哪个加载器、支不支持我的 MC 版本、多少人装过、多久没更新了。
                        */}
                        <span className="mono-meta mt-0.5 block">
                          {v.loaders?.length ? `${v.loaders.map(loaderLabel).join(' / ')} · ` : ''}
                          {v.gameVersions?.length ? `MC ${v.gameVersions.slice(0, 4).join(' / ')}` : ''}
                        </span>
                      </span>
                      <span className="mono-meta hidden text-right sm:block">
                        {v.downloads ? `下载 ${v.downloads.toLocaleString()} 次` : ''}
                        {v.datePublished && <span className="block">{fmtAgo(v.datePublished)}发布</span>}
                      </span>
                      <button
                        type="button"
                        className="btn btn-primary sm shrink-0"
                        disabled={busyId !== ''}
                        onClick={() => void install(v)}
                      >
                        <Icon name="dl" size={12} />
                        {busyId === v.id ? '提交中…' : '安装'}
                      </button>
                    </div>

                    {/*
                      前置要能「顺手装上」，不能只是写着「有前置 2」。
                      ⚠ 但也不能点一下就默默装最新版 —— 用户原话：
                      「我甚至都不知道它装的是哪个版本的前置」。所以点名字开弹层让人自己挑。
                      横条样式照设计稿 mod.html（整宽、黄底、上分隔线）。
                    */}
                    {deps.length > 0 && (
                      <div className="depbar">
                        <div className="flex flex-wrap items-center gap-x-2.5 gap-y-2">
                          <Icon name="info" style={{ color: 'var(--warn)', flex: 'none' }} />
                          <span className="mono-meta shrink-0">这个版本要先装：</span>
                          {deps.map((dep) => (
                            <button
                              key={dep.projectId}
                              type="button"
                              className="badge w"
                              title="打开「装前置」，自己去挑版本"
                              onClick={() =>
                                onOpenDep(dep.projectId as string, depNames[dep.projectId as string] || '')
                              }
                            >
                              {depNames[dep.projectId as string] || dep.projectId}
                            </button>
                          ))}
                        </div>
                        <p className="mono-meta mt-2 leading-relaxed">
                          前置不会顺手静默装 —— 你点了它才进队列。点上面的名字挑版本。
                        </p>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}

          {/*
            只列「这台服务器能用的」是对的（装了必定生效），但要**说清楚还有多少没列**——
            否则用户会以为这个模组就这几个版本。看全量是点开关才拉。
          */}
          {vers && vers.length > 0 && otherCount > 0 && !allVers && (
            <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2">
              <span className="mono-meta">
                这个模组一共有 {totalVers} 个构建，上面列的是这台服务器能用的 {vers.length} 个。
              </span>
              <button type="button" className="btn sm shrink-0" disabled={loadingAll} onClick={showAll}>
                {loadingAll ? '正在取…' : `看其他 ${otherCount} 个`}
              </button>
            </div>
          )}

          {allVers && otherGroups.length > 0 && (
            <div className="mt-5">
              {/*
                ⚠ 这些**只展示、不给安装按钮**：加载器或 MC 版本对不上，装了在这台服务器上不生效。
                给个按钮才是骗人（点了没反应的按钮比没有按钮更糟）。
              */}
              <p className="mono-meta mb-2 max-w-[70ch] leading-relaxed">
                下面这些构建的加载器或 MC 版本跟这台服务器对不上，装了不生效 ——
                列出来只是让你知道这个模组还在维护哪些版本。
              </p>
              {otherGroups.map((g, i) => (
                <details key={g.key} className="mt-1.5" open={i === 0}>
                  <summary className="cursor-pointer py-1.5 text-[13px] text-ink-1 select-none">
                    {g.label}
                    <span className="mono-meta ml-2.5">{g.vers.length} 个构建</span>
                  </summary>
                  <div className="vlist">
                    {g.vers.map((v) => (
                      <div
                        key={v.id}
                        className="vrow"
                        style={{ gridTemplateColumns: '132px auto 1fr auto', cursor: 'default' }}
                      >
                        <span className="ver">{v.versionNumber}</span>
                        <span>
                          {v.releaseType !== 'release' && (
                            <span className="chip s w">{releaseLabel(v.releaseType)}</span>
                          )}
                        </span>
                        <span className="mono-meta">MC {(v.gameVersions ?? []).slice(0, 4).join(' / ')}</span>
                        <span className="mono-meta text-right">
                          {v.datePublished ? `${fmtAgo(v.datePublished)}发布` : ''}
                        </span>
                      </div>
                    ))}
                  </div>
                </details>
              ))}
            </div>
          )}
        </>
      }
      rail={
        d ? (
          <>
            <RailSection title="兼容性">
              {loaders.length > 0 && (
                <>
                  <p className="eyebrow" style={{ fontSize: 10 }}>加载器</p>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {loaders.map((l) => (
                      <span key={l} className="badge wl">
                        {loaderLabel(l)}
                      </span>
                    ))}
                  </div>
                </>
              )}
              {mcChoices.length > 0 && (
                <>
                  <p className="eyebrow mt-3" style={{ fontSize: 10 }}>这台服务器能用的 MC</p>
                  <div className="mt-1.5 flex flex-wrap gap-1.5">
                    {mcChoices.slice(0, 8).map((g) => (
                      <span key={g} className="badge">
                        {g}
                      </span>
                    ))}
                  </div>
                </>
              )}
              {d.serverSide && (
                <>
                  <p className="eyebrow mt-3" style={{ fontSize: 10 }}>服务端支持</p>
                  <p className="mt-1.5 text-[12.5px] text-ink-2">{sideLabel(d.serverSide)}</p>
                </>
              )}
              {loaders.length === 0 && mcChoices.length === 0 && !vErr && (
                <p className="mono-meta">
                  版本还没取回来（或者源站没给这一栏），所以这里暂时没有兼容性信息。
                </p>
              )}
            </RailSection>

            {hwCats.length > 0 && (
              <RailSection title="分类">
                <div className="flex flex-wrap gap-1.5">
                  {hwCats.map((c) => (
                    <span key={c} className="badge">
                      {catLabel(c)}
                    </span>
                  ))}
                </div>
              </RailSection>
            )}

            {/*
              外链：用户原话「你好歹让我能看个介绍什么的，加个跳转链接」。
              源站原页永远排第一 —— 它通常比我们渲染的更全（视频、评论区、更新日志）。
              「去 MC百科 查」是为中文用户留的出口：我们的译名表不全，
              百科那边一定能查到中文名和各平台链接，别让人在找不到东西时卡在这里。
            */}
            <RailSection title="外链">
              <div className="flex flex-col items-start gap-y-1.5">
                {d.webUrl && <ExtLink href={d.webUrl} label="在源站上打开原页" primary />}
                <ExtLink
                  href={`https://search.mcmod.cn/s?key=${encodeURIComponent(d.zhName || d.title)}`}
                  label="去 MC百科 查"
                />
                {d.links?.issues && <ExtLink href={d.links.issues} label="反馈 / 讨论区" />}
                {d.links?.source && <ExtLink href={d.links.source} label="源码" />}
                {d.links?.wiki && <ExtLink href={d.links.wiki} label="Wiki" />}
                {d.links?.discord && <ExtLink href={d.links.discord} label="Discord" />}
                {(d.links?.donation ?? []).map((dn) => (
                  <ExtLink key={dn.url} href={dn.url} label={`赞助作者（${dn.label}）`} />
                ))}
                <button
                  type="button"
                  className="btn btn-ghost xs mt-1"
                  onClick={() => {
                    void navigator.clipboard
                      ?.writeText(d.zhName ? `${d.zhName} (${d.title})` : d.title)
                      .then(() => onToast(`已复制「${d.zhName || d.title}」`))
                      .catch(() => onToast('复制失败 —— 浏览器没给剪贴板权限'))
                  }}
                >
                  复制名称
                </button>
              </div>
            </RailSection>

            <RailSection title="详情">
              <div>
                {d.license?.id && <RailRow k="许可" v={licenseLabel(d.license.id)} />}
                {d.datePublished && <RailRow k="发布" v={fmtAgo(d.datePublished)} />}
                {d.dateModified && <RailRow k="更新" v={fmtAgo(d.dateModified)} />}
                {!!d.versionCount && <RailRow k="版本" v={`${d.versionCount} 个`} />}
                {!!d.follows && <RailRow k="关注" v={d.follows.toLocaleString()} />}
                <RailRow k="下载" v={d.downloads.toLocaleString()} />
              </div>
              {/* 这次是从镜像读的还是从官方读的 —— 源站不通时会走镜像，
                  不写出来用户会在「信息怎么对不上」的时候没线索。 */}
              {base && mirrored && (
                <p className="mono-meta mt-2.5 break-all">这次从镜像读的：{base}</p>
              )}
            </RailSection>
          </>
        ) : null
      }
      /*
        页末通栏 —— MC 百科关系星图。
        用户明确要求「直接放在页末就行，不要放在那（右栏）」：这是一张要占满宽度的力导向图，
        塞进 300px 的右栏只会糊成一团；放页末还有个好处 —— 想深挖关系的人滚到底就有，
        不关心的人不会被它挡在正文前面。
        数据来自百科自己画图用的那份公开 JSON，进页面**自动加载**（后端有缓存兜底，见 loadMcmod）。
      */
      below={
        <section className="panel mcg-sec">
          <div className="sec-head">
            <span className="idx">★</span>
            <h2>关系星图</h2>
            <span className="sub">来自 MC 百科 · 自动加载</span>
          </div>

          {relations.state === 'loading' && (
            <p className="mcg-hint">正在从 MC 百科读取关系星图…（第一次要联网，之后走本地缓存）</p>
          )}
          {relations.state === 'err' && (
            <div className="mcg-idle">
              <p className="mcg-err">{relations.msg}</p>
              <button type="button" className="btn btn-ghost xs" onClick={() => void loadMcmod()}>
                重试
              </button>
            </div>
          )}
          {relations.state === 'ok' && relations.data && (
            <>
              {relations.data.nodes.length === 0 ? (
                <p className="mcg-hint">
                  MC 百科上有这个模组的条目（<b>{relations.data.center?.name}</b>），但没有登记关系图 ——
                  它可能是独立模组，也可能是百科还没补。
                </p>
              ) : (
                <McmodGraph data={relations.data} onQuickInstall={(n) => quickInstall(n)} />
              )}
            </>
          )}
        </section>
      }
    />
  )
}
