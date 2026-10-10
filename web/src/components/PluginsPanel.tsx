import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import {
  api,
  type MarketCategory,
  type MarketHit,
  type MarketSource,
  type MarketVersion,
  type ModIssue,
  type PluginItem,
} from '../api'
import { MOD_LOADER_TAGS, catLabel, loaderLabel } from './projectMeta'
import { ModProjectDetail } from './ModProjectDetail'
import { DepQueueDialog, type DepTarget } from './DepQueueDialog'
import { Chipf, ConFoot, ConHead, Filters, Icon, Panel, SecHead, Tog } from './design'
import {
  Dialog,
  Empty,
  SkeletonList,
  fmtAgo,
  fmtSize,
  fmtTime,
  useConfirm,
} from './ui'
import { STATUS_LABEL, serverLabel, type Instance } from '../types'

/** 模组端放 mods，插件端放 plugins —— 告诉用户往哪丢文件，别让他猜 */
function contentDirLabel(type: string): string {
  return type === 'fabric' || type === 'forge' || type === 'neoforge' ? 'mods' : 'plugins'
}

/** 实例类型 → Modrinth 的加载器分类。原版没有加载器，不筛。 */
function marketLoader(type: string): string {
  if (type === 'vanilla') return ''
  if (type === 'bungee') return 'bungeecord'
  return type
}


/** 五档筛选。抄 PCL 的思路：先按「状态」分堆，比让人在几十个条目里翻要快得多。 */
type FilterKey = 'all' | 'on' | 'off' | 'issue' | 'unknown'

const FILTERS: { k: FilterKey; label: string }[] = [
  { k: 'all', label: '全部' },
  { k: 'on', label: '已启用' },
  { k: 'off', label: '已禁用' },
  { k: 'issue', label: '缺前置' },
  { k: 'unknown', label: '读不出信息' },
]

function matchFilter(p: PluginItem, k: FilterKey): boolean {
  switch (k) {
    case 'on':
      return p.enabled
    case 'off':
      return !p.enabled
    case 'issue':
      return reqIssues(p).length > 0
    case 'unknown':
      return !p.meta
    default:
      return true
  }
}

/** 只有「必需」的依赖问题才算会挡住启动，文案也据此分开写 */
function issueText(is: ModIssue): string {
  if (is.problem === 'disabled') {
    return `前置 ${is.depName || is.depId} 被禁用了`
  }
  return `缺少前置 ${is.depId}`
}

/** 依赖问题里只报「必需」的 —— 可选前置是建议，不是「会起不来」 */
function reqIssues(p: PluginItem): ModIssue[] {
  return (p.issues ?? []).filter((i) => i.kind === 'required')
}

/** 模组图标。读不到就退回项目里那个像素木箱 —— 不留白，也不假装有图标。 */
function ModIcon({ id, p }: { id: string; p: PluginItem }) {
  const [broken, setBroken] = useState(false)
  if (!p.hasIcon || broken) return <Icon name="cube" />
  return (
    <img
      src={api.pluginIconUrl(id, p.file, p.modTime)}
      alt=""
      loading="lazy"
      onError={() => setBroken(true)}
    />
  )
}

type Props = {
  instance: Instance
  onToast: (msg: string) => void
  /** 打开「文件」页并定位到某个目录（模组目录） */
  onOpenFiles?: (path: string) => void
}

export function PluginsPanel({ instance, onToast, onOpenFiles }: Props) {
  const [items, setItems] = useState<PluginItem[]>([])
  // 「核对客户端模组」的结果与过程态。
  // 结果留在页内而不是弹窗：用户要一边看着清单一边决定禁不禁，弹窗挡住列表反而碍事。
  const [audit, setAudit] = useState<{
    scanned: number
    identified: number
    clientOnly: { file: string; projectId?: string; title?: string }[]
  } | null>(null)
  const [auditing, setAuditing] = useState(false)
  const [busyAudit, setBusyAudit] = useState(false)
  // 多选：单击行选中，Ctrl/Cmd 加选，Shift 范围选。存文件名（目录里唯一）。
  const [sel, setSel] = useState<Set<string>>(new Set())
  const [busyBulk, setBusyBulk] = useState(false)
  const [dir, setDir] = useState('')
  const [filter, setFilter] = useState('')
  const [fk, setFk] = useState<FilterKey>('all')
  const [err, setErr] = useState('')
  /* 首屏标记。不区分「还没读到」和「读到了但目录是空的」，
     会在数据回来之前先把「目录是空的」写出来 —— 那是句假话。 */
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<PluginItem | null>(null)

  /**
   * 「从已安装详情 → 市场详情页」的整屏覆盖态。
   * 存的是 Modrinth 项目 id。它独立于 `detail`（已安装的弹窗）：
   * 打开后把整屏换成 ModProjectDetail，但 `detail` 仍保留 ——
   * 点返回（onBack）清空本态，已安装的详情弹窗自动重现，闭环不丢人。
   */
  const [marketDetail, setMarketDetail] = useState<string | null>(null)

  /** installed = 管理已装的；market = 获取模组（页面主体，不是弹窗）。
      初始值从 URL 读：?page=plugins&mod=market 直接落到市场视图。 */
  const [view, setView] = useState<'installed' | 'market'>(() =>
    new URLSearchParams(location.search).get('mod') === 'market' ? 'market' : 'installed',
  )

  /** 视图切换时把状态写回 URL —— 深链可分享，无头浏览器也能直达（向导 ?view=create 同一思路） */
  const switchView = useCallback((v: 'installed' | 'market') => {
    setView(v)
    const u = new URL(location.href)
    if (v === 'market') u.searchParams.set('mod', 'market')
    else u.searchParams.delete('mod')
    history.replaceState(null, '', u)
  }, [])

  const { dialog, confirm } = useConfirm()

  /**
   * 正在装的前置队列。
   * 两种来源：市场里点「装前置」→ 带 projectId；
   * 已安装模组的依赖 → 只有 modId（比如 "jei"），弹层会先去市场里找项目。
   */
  const [deps, setDeps] = useState<DepTarget[]>([])

  /**
   * 「去装前置」的落点。
   * 以前缺前置只在详情里写一行「还没装」，用户得自己退出去、切到市场、
   * 把那个 modId 再敲一遍 —— 一步能做完的事被拆成四步。
   */
  const [seed, setSeed] = useState('')
  const [seedN, setSeedN] = useState(0)
  const findDep = useCallback((kw: string, where: 'market' | 'installed') => {
    if (!kw) return
    if (where === 'installed') {
      // 它已经在目录里了，只是被禁用 —— 缺的不是下载，是把它从一堆文件里挑出来
      switchView('installed')
      setFk('all')
      setFilter(kw)
      return
    }
    switchView('market')
    setSeed(kw)
    setSeedN((n) => n + 1)
  }, [switchView])

  /**
   * 已安装详情弹窗里的「详情页」按钮 → 打开这个模组的市场详情（介绍 / 版本 / 关系星图）。
   * 优先用 jar 里读到的 Modrinth 项目 id 直接进详情；读不到 id 就退一步切到市场按名字搜。
   * ⚠ 不切 `view`：整屏交给 ModProjectDetail（marketDetail 态），`detail` 弹窗保留，
   *    返回即重现 —— 这就是用户要的「页面返回」闭环。
   */
  const goMarketDetail = useCallback(
    (item: PluginItem) => {
      const id = item.meta?.id
      if (id) {
        setMarketDetail(id)
        return
      }
      onToast(`没读到「${item.name}」的市场项目 id，已帮你打开市场按名字搜`)
      switchView('market')
      setSeed(item.name)
      setSeedN((n) => n + 1)
    },
    [switchView, onToast],
  )

  const reload = useCallback(async () => {
    try {
      const d = await api.plugins(instance.id)
      setItems(d.items ?? [])
      setDir(d.dir)
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  /** 勾 / 取消勾一行。 */
  const toggleSel = (file: string) => {
    setSel((prev) => {
      const next = new Set(prev)
      if (next.has(file)) next.delete(file)
      else next.add(file)
      return next
    })
  }

  /**
   * 全选 / 取消全选 —— 对**当前过滤出来的**那些，不是整个目录（搜完只勾命中的那批）。
   * ⚠「是否已全选」必须在 updater 里现算：`shown` 定义在下面，写在函数外面会被 TS 拦
   * （block-scoped variable used before its declaration）。
   */
  const toggleAll = () => {
    setSel((prev) => {
      const all = shown.length > 0 && shown.every((p) => prev.has(p.file))
      return all ? new Set() : new Set(shown.map((p) => p.file))
    })
  }

  /** 批量启用 / 禁用选中的。只动状态还没对上的那些 —— 不然白跑一堆请求。 */
  const bulkToggle = async (on: boolean) => {
    const files = shown.filter((p) => sel.has(p.file) && p.enabled !== on).map((p) => p.file)
    if (files.length === 0) {
      onToast(`选中的本来就都是「${on ? '已启用' : '已禁用'}」`)
      return
    }
    setBusyBulk(true)
    let ok = 0
    const failed: string[] = []
    for (const f of files) {
      try {
        await api.pluginToggle(instance.id, f, on)
        ok++
      } catch {
        failed.push(f)
      }
    }
    setBusyBulk(false)
    setSel(new Set())
    await reload()
    onToast(
      failed.length === 0
        ? `已${on ? '启用' : '禁用'} ${ok} 个`
        : `${on ? '启用' : '禁用'}了 ${ok} 个，${failed.length} 个失败：${failed.slice(0, 3).join('、')}`,
    )
  }

  /** 核对已装模组的客户端 / 服务端归属（联网，几秒）。 */
  const runAudit = async () => {
    setAuditing(true)
    try {
      setAudit(await api.pluginSideAudit(instance.id))
    } catch (e) {
      onToast('核对失败：' + (e as Error).message)
    } finally {
      setAuditing(false)
    }
  }

  /**
   * 一键禁用核对出来的「仅客户端」模组。
   *
   * 用「禁用」而不是删：改后缀成 `.jar.disabled`，加载器就不认它了，
   * 而用户随时能点回来 —— 核对的依据是源站的标注，标注也可能错，必须留后路。
   * 逐个调（不新开批量接口）：失败一个不连累其它，还能如实报出是哪几个没成功。
   */
  const disableClientOnly = async () => {
    if (!audit) return
    const files = audit.clientOnly.map((s) => s.file)
    setBusyAudit(true)
    let ok = 0
    const failed: string[] = []
    for (const f of files) {
      try {
        await api.pluginToggle(instance.id, f, false)
        ok++
      } catch {
        failed.push(f)
      }
    }
    setBusyAudit(false)
    setAudit(null)
    await reload()
    onToast(
      failed.length === 0
        ? `已禁用 ${ok} 个仅客户端模组（想恢复就在列表里重新启用）`
        : `禁用了 ${ok} 个，${failed.length} 个失败：${failed.slice(0, 3).join('、')}`,
    )
  }

  // 从崩溃诊断跳过来时地址栏带 ?audit=1 —— 自动跑一次核对，用户不用再去找按钮。
  // 跑完立刻把参数清掉：留着的话每次刷新都会重跑一遍（那是几秒的网络请求）。
  useEffect(() => {
    if (items.length === 0) return
    const u = new URL(location.href)
    if (u.searchParams.get('audit') !== '1') return
    u.searchParams.delete('audit')
    history.replaceState(null, '', u)
    void runAudit()
  }, [items.length])

  useEffect(() => {
    setItems([])
    setLoading(true)
    reload()
  }, [reload])

  /** 起完下载任务后盯着它，落地了自动刷新列表 —— 否则用户得自己想到点刷新。 */
  const watchTask = useCallback(
    async (taskId: string, label: string) => {
      for (let i = 0; i < 300; i++) {
        await new Promise((r) => setTimeout(r, 700))
        let t
        try {
          t = await api.fetchTask(taskId)
        } catch {
          reload()
          return
        }
        if (t.state === 'running') continue
        if (t.state === 'done') onToast(`${label} 装好了 —— 重启服务器后生效`)
        else onToast(`${label} 安装失败：${t.err || '未知原因'}`)
        reload()
        return
      }
      reload()
    },
    [onToast, reload],
  )

  const toggle = async (p: PluginItem, on: boolean) => {
    try {
      await api.pluginToggle(instance.id, p.file, on)
      // 依赖问题是「谁装了、谁禁用」算出来的 —— 改了启停就必须重算，
      // 否则行上会挂着一条已经不成立的警告。
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const removeNow = async (p: PluginItem) => {
    try {
      await api.pluginDelete(instance.id, p.file)
      setDetail(null)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const remove = (p: PluginItem) =>
    confirm({
      title: `删除「${p.name}」`,
      danger: true,
      confirmLabel: '删除文件',
      run: () => removeNow(p),
      body: (
        <>
          文件会<b className="text-ink-1">从磁盘上删掉</b>，不是只从这个列表里藏起来。
          <span className="mono-value mt-1.5 block break-all">{p.file}</span>
          想要回来就得重新去下载一份；如果是自己改过的版本，先拷出去再删。
        </>
      ),
    })

  const onCount = items.filter((i) => i.enabled).length
  const issueCount = items.filter((i) => matchFilter(i, 'issue')).length
  const dirName = contentDirLabel(instance.type)

  // 装了二三十个模组之后，靠眼睛在列表里找某一个是不现实的
  const fq = filter.trim().toLowerCase()
  const shown = useMemo(() => {
    const byFilter = items.filter((p) => matchFilter(p, fk))
    if (!fq) return byFilter
    return byFilter.filter((p) =>
      (p.name + ' ' + p.file + ' ' + (p.version ?? '') + ' ' + (p.meta?.id ?? ''))
        .toLowerCase()
        .includes(fq),
    )
  }, [items, fk, fq])

  /*
    ▍「从已安装详情 → 市场详情页」整屏覆盖。
    优先级高于市场视图：进到这一态时整屏交给 ModProjectDetail，
    已安装的 `detail` 弹窗态仍在内存里，返回即重现（用户要的「页面返回」闭环）。
    ⚠ 这条提前返回必须在**所有 hook 之后**，否则 hook 调用次数会随分支变化。
  */
  if (marketDetail) {
    return (
      <>
        <ModProjectDetail
          key={marketDetail}
          project={marketDetail}
          instance={instance}
          loader={marketLoader(instance.type)}
          mc={instance.mcVersion || ''}
          backLabel="返回已安装"
          onBack={() => setMarketDetail(null)}
          onToast={onToast}
          onStarted={watchTask}
          onOpenDep={(id, name) => setDeps([{ projectId: id, name }])}
        />
        {deps.length > 0 && (
          <DepQueueDialog
            deps={deps}
            loader={marketLoader(instance.type)}
            mc={instance.mcVersion || ''}
            instance={instance}
            onClose={() => setDeps([])}
            onToast={onToast}
            onStarted={watchTask}
          />
        )}
      </>
    )
  }

  /* ── 获取模组视图：PCL 的做法 —— 装模组就是一整个页面，不是弹窗。
        弹窗里塞搜索 + 20 条结果 + 版本列表，要么滚不动、要么把动作按钮挤出屏幕。 ── */
  if (view === 'market') {
    return (
      <MarketView
        instance={instance}
        onToast={onToast}
        onStarted={watchTask}
        onBack={() => switchView('installed')}
        seed={seed}
        seedN={seedN}
      />
    )
  }

  /* 缺「必需」前置的条目 —— 设计稿那面横幅就是给它们留的位置。
     只报 missing：被禁用的前置不是「要再下一份」，是「去把它打开」。 */
  const missingDeps: DepTarget[] = []
  for (const p of items) {
    for (const is of reqIssues(p)) {
      if (is.problem === 'missing') missingDeps.push({ modId: is.depId, name: is.depName || is.depId })
    }
  }

  return (
    <div className="min-w-0">
      <SecHead
        idx="05"
        title="插件与模组"
        sub={<>正在管 {instance.name} 的 {dirName} 目录 —— 改动要重启服务器才会生效</>}
        style={{ '--rd': '.08s' } as React.CSSProperties}
        right={
          <>
            {onOpenFiles && (
              <button type="button" className="btn btn-ghost sm" onClick={() => onOpenFiles(dirName)}>
                <Icon name="cube" />
                打开文件夹
              </button>
            )}
            <button type="button" className="btn btn-primary sm" onClick={() => switchView('market')}>
              <Icon name="dl" />
              获取模组
            </button>
            <button type="button" className="btn btn-ghost sm" onClick={reload} aria-label="刷新列表">
              <Icon name="refresh" />
            </button>
          </>
        }
      />

      {err && (
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>读目录没成功</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn btn-ghost sm" onClick={reload}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      )}

      {missingDeps.length > 0 && (
        <div className="banner-danger">
          <Icon name="info" size={20} style={{ color: 'var(--warn)' }} />
          <div className="t">
            <h3>有 {missingDeps.length} 个模组缺少必需前置</h3>
            <p>
              缺必需前置的模组在服务端加载阶段就会被丢掉，压根不会进游戏。点「装前置」会先按你的 MC
              版本挑好对应版本 —— 不会替你默认装最新版；如果那个前置其实已经在目录里、只是被禁用了，它会告诉你去把它启用。
            </p>
          </div>
          <button type="button" className="btn btn-primary sm" onClick={() => setDeps(missingDeps)}>
            <Icon name="dl" />
            装前置（{missingDeps.length}）
          </button>
        </div>
      )}

      <Panel>
        <ConHead
          icon="table"
          title="已安装"
          sub={
            loading
              ? '正在读取…'
              : fq || fk !== 'all'
                ? `${shown.length} / ${items.length}`
                : items.length > 0
                  ? `${items.length} 个 · 启用 ${onCount} 个`
                  : '目录是空的'
          }
        >
          <input
            className="inp"
            style={{ maxWidth: 240, marginLeft: 8 }}
            placeholder="名字 / 文件名 / modId"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
          <div className="spacer" />
          <Filters>
            {FILTERS.map((f) => {
              const n =
                f.k === 'all'
                  ? items.length
                  : f.k === 'issue'
                    ? issueCount
                    : items.filter((p) => matchFilter(p, f.k)).length
              return (
                <Chipf
                  key={f.k}
                  on={fk === f.k}
                  onClick={() => setFk(f.k)}
                  title={f.k === 'issue' && n === 0 ? '当前没有缺前置的条目' : undefined}
                >
                  {f.label}
                  {n > 0 && <span className="n">{n}</span>}
                </Chipf>
              )
            })}
          </Filters>
          <button
            type="button"
            className="btn btn-ghost sm"
            style={{ marginLeft: 8 }}
            disabled={auditing || items.length === 0}
            onClick={runAudit}
            title="按每个 jar 的哈希去 Modrinth 反查它的客户端 / 服务端归属（需要联网，几秒钟）"
          >
            <Icon name="check" />
            {auditing ? '核对中…' : '核对客户端模组'}
          </button>
        </ConHead>

        {/* 批量操作条：勾了才出现。只做启用/禁用 —— 删除不可逆，留在行内逐个确认。 */}
        {sel.size > 0 && (
          <div className="selbar">
            <span>
              已选 <b className="text-ink-1">{sel.size}</b> 个
            </span>
            <button type="button" className="btn btn-ghost xs" disabled={busyBulk} onClick={toggleAll}>
              {shown.length > 0 && shown.every((p) => sel.has(p.file))
                ? '取消全选'
                : `全选（${shown.length}）`}
            </button>
            <div className="spacer" style={{ flex: 1 }} />
            <button
              type="button"
              className="btn sm"
              disabled={busyBulk}
              onClick={() => void bulkToggle(true)}
            >
              启用
            </button>
            <button
              type="button"
              className="btn sm"
              disabled={busyBulk}
              onClick={() => void bulkToggle(false)}
            >
              {busyBulk ? '处理中…' : '禁用'}
            </button>
            <button
              type="button"
              className="btn btn-ghost sm"
              disabled={busyBulk}
              onClick={() => setSel(new Set())}
            >
              清空选择
            </button>
          </div>
        )}

        {/*
          核对结果。⚠ 措辞上有一条硬规矩：`identified < scanned` 说明有模组**没核出来**
          （本地改过的 jar、或没发布在 Modrinth 上）—— 那不等于「其余都安全」，
          所以必须把这个差值说出来，不能只说「没发现问题」。
        */}
        {audit && (
          <div className="nbox w" style={{ margin: '0 14px 12px' }}>
            <div className="hd">
              <Icon name={audit.clientOnly.length > 0 ? 'ban' : 'check'} size={14} />
              {audit.clientOnly.length > 0
                ? `核对完成：${audit.clientOnly.length} 个模组在服务端装不了`
                : '核对完成：没发现仅客户端的模组'}
            </div>
            <p>
              扫了 {audit.scanned} 个、认出 {audit.identified} 个。
              {audit.identified < audit.scanned &&
                ` 另有 ${audit.scanned - audit.identified} 个没核出来（本地改过、或没发布在 Modrinth 上）—— 没核出来不代表没问题。`}
            </p>
            {audit.clientOnly.length > 0 && (
              <>
                <p>它们只能装在客户端，留在服务端的 mods 目录里会让服务器起不来：</p>
                <p className="mono-meta">
                  {audit.clientOnly
                    .slice(0, 8)
                    .map((s) => s.title || s.file)
                    .join('、')}
                  {audit.clientOnly.length > 8 ? ` 等 ${audit.clientOnly.length} 个` : ''}
                </p>
                <div style={{ marginTop: 10, display: 'flex', gap: 8 }}>
                  <button
                    type="button"
                    className="btn btn-primary sm"
                    disabled={busyAudit}
                    onClick={disableClientOnly}
                  >
                    <Icon name="ban" />
                    {busyAudit ? '正在禁用…' : `禁用这 ${audit.clientOnly.length} 个`}
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost sm"
                    disabled={busyAudit}
                    onClick={() => setAudit(null)}
                  >
                    先不动
                  </button>
                </div>
              </>
            )}
          </div>
        )}

        {loading ? (
          <div style={{ padding: 14 }}>
            <SkeletonList rows={4} />
          </div>
        ) : items.length === 0 ? (
          <div style={{ padding: 14 }}>
            <Empty>
              这个 {dirName} 目录还是空的。点右上角<b className="text-ink-1">「获取模组」</b>
              —— 可以从在线市场搜、传本地 jar，或者粘一个下载地址。
            </Empty>
          </div>
        ) : shown.length === 0 ? (
          <div style={{ padding: 14 }}>
            <Empty>{fq ? `没有匹配「${filter.trim()}」的条目。` : '这一档里没有条目。'}</Empty>
          </div>
        ) : (
          <div className="rlist">
            {shown.map((p) => {
              const issues = reqIssues(p)
              /* 缺「要下载」的前置给黄条（.dep）；「只是被禁用」不给 —— 那是开关能解决的事，
                 整行泛黄会让人以为文件坏了。 */
              const needDownload = issues.some((i) => i.problem === 'missing')
              /* 副行内容：问题优先，其次描述，最后退回文件名。
                 ⚠ 只显示一行（.one-line）—— Modrinth 的 description 可能是整段版权文本，
                 不截断会把行高撑歪（Konkrete 那行曾撑到 4 行）；全文挂在 title 上。 */
              const sub =
                issues.length > 0 ? issues.map(issueText).join('；') : p.meta?.description || p.file
              return (
                <div
                  key={p.file}
                  className={`rrow has-ck${needDownload ? ' dep' : ''}${p.enabled ? '' : ' off'}${sel.has(p.file) ? ' sel' : ''}`}
                >
                  {/* 勾选位。⚠ 要挡住冒泡：点它不该顺带把这一行打开详情 */}
                  <label className="rsel" onClick={(e) => e.stopPropagation()}>
                    <input
                      type="checkbox"
                      checked={sel.has(p.file)}
                      onChange={() => toggleSel(p.file)}
                      aria-label={`选择 ${p.name}`}
                    />
                  </label>
                  <div className="ricon">
                    <ModIcon id={instance.id} p={p} />
                  </div>
                  <button type="button" className="rmain" onClick={() => setDetail(p)}>
                    <div className="rtitle">
                      {p.name}
                      {/*
                        「仅客户端」排在最前 —— 它是这一行里唯一会让服务器起不来的信息
                        （Fabric 的 environment=client 就是加载器拒绝加载它的依据）。
                        ⚠ Forge 读不出 side，这里就不会出现徽标，但那不等于「两端都行」。
                      */}
                      {p.meta?.side === 'client' && (
                        <span
                          className="badge ban"
                          title="这个模组只能在客户端跑 —— 留在服务端的 mods/ 里会让服务器起不来"
                        >
                          仅客户端
                        </span>
                      )}
                      {p.meta?.side === 'server' && (
                        <span className="badge" title="这个模组只装在服务端，客户端不用装">
                          仅服务端
                        </span>
                      )}
                      {p.meta?.loader && <span className="badge">{p.meta.loader}</span>}
                      {p.version && <span className="badge">{p.version}</span>}
                      {issues.length > 0 && (
                        <span className="badge ban">
                          {issues.every((i) => i.problem === 'disabled') ? '前置被禁用' : '缺前置'}
                        </span>
                      )}
                      {!p.meta && <span className="badge">读不出信息</span>}
                    </div>
                    <div className="rsub one-line" title={sub}>
                      {sub}
                    </div>
                  </button>
                  <div className="rmeta">
                    {p.file} · {fmtSize(p.size)}
                  </div>
                  <div className="ract">
                    <Tog on={p.enabled} onChange={(v) => toggle(p, v)} ariaLabel={`${p.name} 启用状态`} />
                    <button
                      type="button"
                      className="btn btn-ghost xs"
                      onClick={() => remove(p)}
                      aria-label={`删除 ${p.name}`}
                    >
                      <Icon name="trash" />
                    </button>
                  </div>
                </div>
              )
            })}
          </div>
        )}

        <ConFoot>
          开关改的是文件后缀（<code>.jar</code> ↔ <code>.jar.disabled</code>），下次启动才生效；删除是不可逆的，唯一的后悔药在备份页。
          {dir && <> 目录：<code>{dir}</code></>}
        </ConFoot>
      </Panel>

      {detail && (
        <ModDetail
          item={detail}
          instanceId={instance.id}
          onClose={() => setDetail(null)}
          onToggle={(v) => toggle(detail, v)}
          onDelete={() => remove(detail)}
          onFindDep={findDep}
          onOpenDep={(id, name) => setDeps([{ modId: id, name }])}
          onOpenMarket={goMarketDetail}
        />
      )}

      {deps.length > 0 && (
        <DepQueueDialog
          deps={deps}
          loader={marketLoader(instance.type)}
          mc={instance.mcVersion || ''}
          instance={instance}
          onClose={() => setDeps([])}
          onToast={onToast}
          onStarted={watchTask}
        />
      )}

      {dialog}
    </div>
  )
}

/**
 * 退出吸顶要比进入多滚这么多像素。
 *
 * ⚠ 为什么给到这么大（120）而不是二三十：不是为了防手抖，是为了盖住
 * **补偿那一帧的滞后**。`useStableScrollRange` 是下一帧才读得到新高度，所以塌陷动画期间
 * 文档还会短暂矮几十像素（1440 宽下实测最多 56px；帧率越低每帧掉得越多，所以取 120）。
 * 退敏阈值盖过这个赤字，浏览器那点夹取就永远够不到「退出」的门槛 —— 成不了环。
 * 代价是往回滚约 120px 才会重新展开，好处是再也不会翻来翻去。
 */
const STUCK_HYST = 120

/**
 * 量吸顶块「本来该在哪」时用的锚点：**它的父元素，不是它自己**。
 *
 * ⚠⚠ 这一条踩了两次坑，都记在这：
 *  ① 量吸顶块自己的 `getBoundingClientRect()` —— 不行。`position: sticky`
 *     是**浏览器自动**生效的，跟我们的 `.stuck` class 毫无关系。滚过 top 那条线，
 *     rect.top 立刻被钉在吸顶线上，反推出来的是「它现在被钉在哪」而不是「它本来在哪」。
 *  ② 改用 `offsetTop` 链绕开绘制位置 —— **也不行**。实测 Chrome 里 sticky 元素的
 *     `offsetTop` 同样会跟着滚动走（吸顶块 offsetTop 直接等于 scrollTop）。
 *  ③ 最后落到这里：用**父元素**。父元素是普通流，sticky 影响不到它，
 *     而吸顶块是父元素的第一个子元素（没有 margin），两者顶部重合。
 *     实测幂等：滚到哪量都是同一个值。
 */
function anchorOf(el: HTMLElement): HTMLElement {
  return el.parentElement ?? el
}

/**
 * 吸顶判定：滚过某条线就吸住。
 *
 * ⚠ 判据必须是**不受吸顶块自身高度影响**的量，否则它会自我触发成环：
 *   ① 判定吸住 → ② 面板收起、吸顶块变矮 → ③ 文档总高度变短
 *   → ④ 若已滚到底，浏览器把 scrollTop 夹小 → ⑤ 吸顶块相对视口上移
 *   → ⑥ 判定「没吸住」→ ① 变高 → 再来一轮，停不下来。
 *   （用户原话：「滚动之后会进行缩放，当缩放的过渡动画到一定程度时，
 *    它就会一直抽搐」。）
 *
 * 所以判据改成 **scrollTop 与一条恒定阈值线** 比较：
 *   · 线的位置 = 吸顶块「未吸顶时的内容坐标」- sticky 的 top 偏移。
 *     它只由吸顶块**前面**的内容决定，吸顶块自己怎么变都影响不到它。
 *   · 再加 STUCK_HYST 的滞回，把小抖动挡掉。
 *
 * 但**光靠判据不够**：scrollTop 是被浏览器夹小的这一步发生在判据之外，
 * 夹完如果正好落到阈值线下面，照样会翻回去 —— 实测就是这么翻的（见 useStableScrollRange）。
 * 真正断环要靠 `useStableScrollRange` 让文档高度全程不变，两头都得有。
 *
 * 顺带：不用再往 DOM 里插哨兵元素了，少一个「必须记得删」的东西。
 *
 * 阈值线怎么算：吸顶块那个位置的**内容坐标** - sticky 的 top 偏移
 *   （内容坐标靠量父元素拿到，见 anchorOf 的注释 —— 量吸顶块自己会踩坑）。
 *   阈值线只由吸顶块**前面**的内容决定：吸顶块自己怎么变高变矮都影响不到它。
 */
function useStuck(head: React.RefObject<HTMLDivElement | null>): boolean {
  const [stuck, setStuck] = useState(false)
  const stuckRef = useRef(false)
  /** 阈值线（内容坐标）。取不到时是 +∞，等于「先不吸顶」 */
  const line = useRef(Number.POSITIVE_INFINITY)

  useEffect(() => {
    const el = head.current
    // 滚动容器是 PageFrame 的 <main>；sticky 正是相对它生效的。
    const main = el?.closest('main') as HTMLElement | null
    if (!el || !main) return

    const measure = () => {
      const stickTop = Number.parseFloat(getComputedStyle(el).top) || 0
      const mr = main.getBoundingClientRect()
      // 内容坐标 = 距容器顶部的偏移 + 已滚距离
      const natural = anchorOf(el).getBoundingClientRect().top - mr.top + main.scrollTop
      line.current = natural - stickTop
    }

    const onScroll = () => {
      const st = main.scrollTop
      if (stuckRef.current) {
        if (st < line.current - STUCK_HYST) {
          stuckRef.current = false
          setStuck(false)
        }
        return
      }
      measure()
      if (st >= line.current) {
        stuckRef.current = true
        setStuck(true)
      }
    }

    measure()
    main.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll)
    return () => {
      main.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
    }
  }, [head])

  return stuck
}

const STABLE_FRAMES = 40 // ≈660ms，够盖住最长那条过渡，多给一点余量

/**
 * 让滚动区的**总高度**不随吸顶块塌陷而变 —— 这是止住抽搐的关键，`useStuck` 那环补不了。
 *
 * 背景：`position: sticky` 的元素仍然占着文档流。面板收起时它从 618px 塌到 369px，
 * 等于整份文档矮了 249px。滚到底部附近时浏览器只能把 scrollTop 往回夹
 * （实测抓到过 scrollTop 从 238 掉到 57 的那一帧），判定随即翻成「没吸住」，
 * 又展开，再被夹 ——
 * **判定写得再稳也没用：它是被「文档高度变了」拖下水的，不是判据的问题。**
 *
 * 所以守住源头：塌陷掉多少，就在滚动区**末尾**补回多少。
 * 为什么不写死 249px：塌陷量取决于说明文字几行、字段有没有溢出换行、断点是多少 ——
 * 写死必然在某个机型上错。改成「量出来的差值」就不用担心这些。
 *
 * ⚠ 补在**末尾**，不是补在吸顶块底下。这一条是截图看出来的：
 *   补在吸顶块底下（给它 margin-bottom）等于把结果列表按在原地不动，
 *   而横条已经缩到 369 —— 两者之间就裂出一条空带（实测 200px 上下），
 *   要往下再滚三百多像素才合上，比抽搐还难看（`?drawer` 那张对照图就是）。
 *   补在末尾则是：横条照旧只占压缩后的高度、内容紧贴它，
 *   多出来那截藏在列表最底下，滚到最底才看得见，代价小得多。
 *
 * 顺带的收益：内容不再「往上跳」一截 —— 列表原地不动，只是横条让出了视野。
 */
function useStableScrollRange(
  head: React.RefObject<HTMLDivElement | null>,
  spacer: React.RefObject<HTMLDivElement | null>,
  stuck: boolean,
): void {
  /** 展开态的高度。塌陷过程中的每一帧都小于它 */
  const target = useRef(0)

  const apply = useCallback(() => {
    const h = head.current?.getBoundingClientRect().height
    const sp = spacer.current
    if (h == null || !sp) return
    // 只有「已经撑到最满」时才更新目标值，否则塌陷中的矮帧会把目标越刷越小。
    if (h >= target.current - 0.5) target.current = h
    sp.style.height = target.current > h + 0.5 ? `${target.current - h}px` : '0px'
  }, [head, spacer])

  useEffect(() => {
    target.current = 0 // 换了视图/内容，旧目标不该留着
    apply()
    const el = head.current
    const sp = spacer.current
    if (!el || !sp) return
    // RO 兜的是「非过渡引起的变化」（比如筛选项增减改变了高度）
    const ro = new ResizeObserver(() => apply())
    ro.observe(el)
    // 换了断点，展开高度本身就变了 —— 丢掉旧目标重新量，别抱着上一档的数字。
    const onResize = () => {
      target.current = 0
      apply()
    }
    window.addEventListener('resize', onResize)
    return () => {
      ro.disconnect()
      window.removeEventListener('resize', onResize)
      sp.style.height = '0px'
    }
  }, [head, spacer, apply])

  /*
    ⚠ ResizeObserver 跟不上过渡动画：它的回调比盒子变化晚一帧，
    实测塌陷中途能差到 56px（高度已到 599，margin 还是 0）——文档照样会抖。
    所以过渡那段时间改成 rAF 逐帧紧跟，RO 只当兜底。
    `stuck` 一变就等于「要开始动画了」，重跑一小段 rAF 即可。
  */
  useEffect(() => {
    let raf = 0
    let n = 0
    const tick = () => {
      apply()
      if (++n < STABLE_FRAMES) raf = requestAnimationFrame(tick)
    }
    raf = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(raf)
  }, [apply, stuck])
}

/**
 * 获取模组视图（页面主体，PCL 的做法 —— 不是弹窗）。
 * 三种来源收在页签里；「在线市场」内部还有一层：搜索结果 → 点进版本列表。
 * 版本层是同一视图内的第二层级（带「← 返回」），不是第二层弹窗 ——
 * 装模组经常要连装好几个前置，留在页面里才方便继续装下一个。
 */
function MarketView({
  instance,
  onToast,
  onStarted,
  onBack,
  seed,
  seedN,
}: {
  instance: Instance
  onToast: (msg: string) => void
  onStarted: (taskId: string, label: string) => void
  onBack: () => void
  /** 外部塞进来的搜索词（比如「去装前置」）—— 变了就按它查一次 */
  seed: string
  /** 同一个词再点一次也要重新查，所以带一个递增号 */
  seedN: number
}) {
  const dirName = contentDirLabel(instance.type)
  const loader = marketLoader(instance.type)
  const mc = instance.mcVersion || ''
  const [tab, setTab] = useState<'market' | 'url' | 'local'>(() => {
    const t = new URLSearchParams(location.search).get('modtab')
    return t === 'url' || t === 'local' ? t : 'market'
  })
  /**
   * 选中了哪个项目在看详情。
   *
   * 存的是 **project id 字符串**，不是搜索结果对象：详情页要能靠 `?pm=` 从 URL 复原，
   * 而 URL 里只放得下一个 id —— 那个 MarketHit 是「刚才那次搜索」的产物，刷新就没了。
   * 详情页自己去拉它需要的全部东西，不依赖列表里那份快照。
   */
  const [pick, setPick] = useState<string>(() => new URLSearchParams(location.search).get('pm') || '')
  /** 正在装的前置队列（市场入口：手里已经有 projectId，弹层直接列版本） */
  const [deps, setDeps] = useState<DepTarget[]>([])

  /*
    进详情 / 回列表都要同步到 URL。
    交互态一律可寻址：刷新停在原处、链接能发给别人；无头验证点不了这个按钮，也只能靠它。
  */
  const openPick = (h: MarketHit) => {
    setPick(h.id)
    const u = new URL(location.href)
    u.searchParams.set('pm', h.id)
    u.searchParams.delete('ptab') // 换个项目就从「介绍」开始看
    history.replaceState(null, '', u)
  }
  const closePick = () => {
    setPick('')
    const u = new URL(location.href)
    u.searchParams.delete('pm')
    u.searchParams.delete('ptab')
    history.replaceState(null, '', u)
  }

  const switchTab = (t: 'market' | 'url' | 'local') => {
    setTab(t)
    setPick('') // 换来源时别把上一层的选中带过去
    const u = new URL(location.href)
    if (t === 'market') u.searchParams.delete('modtab')
    else u.searchParams.set('modtab', t)
    u.searchParams.delete('pm')
    u.searchParams.delete('ptab')
    history.replaceState(null, '', u)
  }

  const TABS = [
    { k: 'market' as const, label: '在线市场' },
    { k: 'url' as const, label: '从直链' },
    { k: 'local' as const, label: '本地上传' },
  ]

  // 市场查询的状态提在这里：条件面板要吸顶、结果列表要滚，
  // 两块分开渲染，但它们必须共用同一份状态。
  const mq = useMarketQuery(instance, seed, seedN)
  const showControls = tab === 'market' && !pick

  // 吸住之后要瘦身：说明文字、次要条件都收起来，只留「改关键词 + 换排序」。
  // 用户原话「吸顶太大了」—— 544px 的吸顶条吃掉半屏，比不吸还难受。
  const headRef = useRef<HTMLDivElement>(null)
  /** 滚动区末尾的补位块：吸顶块塌陷掉多少高度，它长多高（见 useStableScrollRange） */
  const spacerRef = useRef<HTMLDivElement>(null)
  const stuck = useStuck(headRef)
  useStableScrollRange(headRef, spacerRef, stuck)

  /** 展开完整条件：滚回顶部，吸顶状态自然解除，面板也就摊开了 */
  const expandFilters = () => {
    const main = headRef.current?.closest('main') as HTMLElement | null
    main?.scrollTo({ top: 0, behavior: 'smooth' })
  }

  /** 装前置的弹层。详情页那一支也要渲染它，所以提出来一份，别抄两遍。 */
  const depNode = deps.length > 0 ? (
    <DepQueueDialog
      key={deps.map((d) => d.projectId || d.modId || d.name).join('|')}
      deps={deps}
      loader={marketLoader(instance.type)}
      mc={instance.mcVersion || ''}
      instance={instance}
      onClose={() => setDeps([])}
      onToast={onToast}
      onStarted={onStarted}
    />
  ) : null

  /** 市场结果里点「装前置」→ 开队列（把那几个 deps 包成数组）。 */
  const openDeps = (d: DepTarget[]) => setDeps(d)

  /*
    进了详情就**整屏交给它**，连上面那条吸顶头都不留。
    留着的话屏幕上会有两条「返回」和两组页签，用户得先想清楚哪条返回到哪儿 ——
    那不是他要解决的问题。模块自己带抬头条，和整合包详情一致。
    ⚠ 这个提前返回必须在**所有 hook 之后**，否则 hook 调用次数会随分支变化。
  */
  if (pick) {
    return (
      <>
        <ModProjectDetail
          key={pick}
          project={pick}
          instance={instance}
          loader={marketLoader(instance.type)}
          mc={instance.mcVersion || ''}
          onBack={closePick}
          onToast={onToast}
          onStarted={onStarted}
          onOpenDep={(id, name) => setDeps([{ projectId: id, name }])}
        />
        {depNode}
      </>
    )
  }

  return (
    <div className="min-w-0">
      {/*
        吸顶区 = 首行 + 说明 + 来源页签 + 整块搜索条件面板。
        用户要的是「这一整块都吸住」，不是只吸一个标题 ——
        条件面板滚走了，改个筛选条件就得往回滚到顶。
      */}
      <div ref={headRef} className={`sticky-head${stuck ? ' stuck' : ''}`}>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <button type="button" className="btn btn-ghost sm shrink-0" onClick={onBack}>
            <Icon name="chev-l" />
            返回已安装
          </button>
          {/* 页面标题用设计稿的 sec-head（像素 idx + h2），不再另造一种页头 */}
          <div className="sec-head" style={{ marginBottom: 0 }}>
            <span className="idx">MOD</span>
            <h2>获取模组</h2>
          </div>
          {/*
            吸顶后导航栏那条身份信息已经滚出去了 —— 让它从右侧滑进来补位，
            而不是凭空出现：transform + opacity 一起过渡，看着就是「移过来」。
          */}
          <span
            className="mident mono-meta ml-auto hidden min-w-0 truncate md:block"
            title={instance.dir}
          >
            {instance.name} · {STATUS_LABEL[instance.status]} ·{' '}
            {serverLabel(instance).toUpperCase()} {instance.mcVersion} · 端口{' '}
            {instance.port}
          </span>
        </div>

        {/* 吸住时收起：这句话是给第一次进来的人看的，不是每次滚动都要读一遍 */}
        <p className="mdesc mt-1 max-w-[62ch] text-[13px] leading-relaxed text-ink-2">
          装进 <b className="text-ink-1">{instance.name}</b> 的 {dirName} 目录，
          下载完会自动出现在「已安装」列表里，重启服务器后生效。
        </p>

        {/* 模组来源：三个入口都是「换一屏内容」——用设计稿的筛选标签组（chipf）承载 */}
        <div className="filters mt-3" role="group" aria-label="模组来源">
          {TABS.map((t) => (
            <button
              key={t.k}
              type="button"
              aria-pressed={tab === t.k}
              className={`fchip tab${tab === t.k ? ' on' : ''}`}
              onClick={() => switchTab(t.k)}
            >
              {t.label}
            </button>
          ))}
        </div>

        {showControls && <MarketControls q={mq} stuck={stuck} onExpand={expandFilters} />}
      </div>

      <div className="mt-3">
        {tab === 'local' && <LocalTab instance={instance} onToast={onToast} onDone={onBack} />}
        {tab === 'url' && <UrlTab instance={instance} onToast={onToast} onStarted={onStarted} />}
        {tab === 'market' && (
          <MarketResults
            q={mq}
            onPick={openPick}
            loader={loader}
            mc={mc}
            instance={instance}
            onStarted={onStarted}
            onToast={onToast}
            onInstallDeps={openDeps}
          />
        )}
      </div>

      {depNode}

      {/*
        补位块：吸顶块塌陷掉的高度补在这里。
        ⚠ 必须放**滚动区末尾**，不能改成给吸顶块加 margin-bottom ——
        那样等于把结果列表按在原处，横条和第一行之间会裂出一条空带
        （截图实证：差 214px，要再往下滚三百多像素才合上），比抽搐还难看。
        放这儿则是内容紧贴横条，多出的高度藏在列表最底下。
      */}
      <div ref={spacerRef} aria-hidden className="h-0" />
    </div>
  )
}

/** 本地上传：一个拖放条，不做成整块卡片 */
function LocalTab({
  instance,
  onToast,
  onDone,
}: {
  instance: Instance
  onToast: (msg: string) => void
  onDone: () => void
}) {
  const [busy, setBusy] = useState(false)
  const [drag, setDrag] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  const upload = async (f: File) => {
    setBusy(true)
    try {
      const r = await api.pluginUpload(instance.id, f)
      onToast(`已放入 ${contentDirLabel(instance.type)}/${r.file} —— 重启服务器后生效`)
      onDone()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <div
        onDragOver={(e) => {
          e.preventDefault()
          setDrag(true)
        }}
        onDragLeave={() => setDrag(false)}
        onDrop={(e) => {
          e.preventDefault()
          setDrag(false)
          const f = e.dataTransfer.files?.[0]
          if (f) upload(f)
        }}
        className="panel flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-4"
        style={drag ? { borderTopColor: 'var(--cyan)', borderLeftColor: 'var(--cyan)' } : undefined}
      >
        <span className="shrink-0 text-accent">
          <Icon name="ul" size={18} />
        </span>
        <p className="min-w-0 flex-1 text-[13px] text-ink-2">
          把 jar 拖到这里，或者从磁盘挑一个 —— 一次一个
        </p>
        <button
          type="button"
          className="btn btn-primary shrink-0"
          disabled={busy}
          onClick={() => fileRef.current?.click()}
        >
          {busy ? '上传中…' : '选择文件'}
        </button>
        <input
          ref={fileRef}
          type="file"
          accept=".jar"
          className="hidden"
          onChange={(e) => {
            const f = e.target.files?.[0]
            if (f) upload(f)
            e.target.value = ''
          }}
        />
      </div>
      <p className="field-hint">
        只认 .jar；同名文件会直接覆盖。装完记得重启服务器才会加载。
      </p>
    </div>
  )
}

/** 从直链装。地址里的文件名通常就是 jar 名，后端会兜底解析。 */
function UrlTab({
  instance,
  onToast,
  onStarted,
}: {
  instance: Instance
  onToast: (msg: string) => void
  onStarted: (taskId: string, label: string) => void
}) {
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)

  const go = async () => {
    const u = url.trim()
    if (!u) return
    setBusy(true)
    try {
      const t = await api.pluginInstallURL(instance.id, { url: u })
      onToast('已开始下载，下完会自动出现在列表里')
      onStarted(t.id, '模组')
      setUrl('')
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="max-w-[520px]">
      <div className="search flex items-center gap-2.5 px-3 py-2">
        <span className="shrink-0 text-ink-3">
          <Icon name="dl" size={13} />
        </span>
        <input
          type="url"
          className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink-1 outline-0 placeholder:text-ink-3"
          placeholder="https://example.com/xxx.jar"
          value={url}
          onChange={(e) => setUrl(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') go()
          }}
        />
      </div>
      <button
        type="button"
        className="btn btn-primary mt-3"
        disabled={busy || !url.trim()}
        onClick={go}
      >
        <Icon name="dl" size={13} />
        {busy ? '提交中…' : '下载并放入'}
      </button>
      <p className="field-hint">
        支持 http/https 直链 —— GitHub Release、镜像站、网盘直链都行。
        地址最后一段会被当成文件名；下载和多线程、断点续传走的是同一套下载器。
      </p>
    </div>
  )
}

/** 在线市场：搜索 → 点一个结果 → 选版本 → 装。页面主体自然滚动，不再塞进 42vh 的小滚区。 */
/** 榜单口径（非空）。相关度只在有关键词时才有意义，所以不进这排 tab。 */
type SortKey = 'downloads' | 'newest' | 'updated' | 'follows'

// PCL 的模组页一进去就有内容，不是让人对着一个空搜索框发呆 —— 这是明确要求。
// 所以下面这排 tab 是这页的**主内容入口**，不是装饰：点哪个都立刻换一屏列表。
const SORTS: { k: SortKey; label: string }[] = [
  { k: 'downloads', label: '热门下载' },
  { k: 'newest', label: '最新收录' },
  { k: 'updated', label: '最近更新' },
  { k: 'follows', label: '最多关注' },
]

/** 「来源」下拉的选项。仅官方 / 仅镜像是有意不做兜底的 —— 用户拿它判断哪条线不通。 */
const SOURCES: { k: MarketSource; label: string; hint: string }[] = [
  { k: 'auto', label: '自动', hint: '官方不通就自动换镜像，拿不准就留这个' },
  { k: 'official', label: '仅 Modrinth 官方', hint: '只走官方线路 —— 用它判断官方到底是通还是不通' },
  { k: 'mirror', label: '仅国内镜像', hint: '只走镜像线路 —— 用它判断镜像到底是好还是坏' },
]

const PAGE = 20

/** 真正拿去查的那份条件。表单里改了但没点「搜索」时，列表不跟着动。 */
type Applied = { name: string; ver: string; cat: string; src: MarketSource; sort: SortKey }

/**
 * 市场查询的全部状态与动作。
 *
 * 抽成 hook 而不是一个组件，是因为**条件面板要吸顶、结果列表要滚** ——
 * 它们不能待在同一个 DOM 块里（sticky 只能整块吸）。状态提到外面，两块各渲染一半。
 */
function useMarketQuery(instance: Instance, seed: string, seedN: number) {
  const loader = marketLoader(instance.type)
  const qs = () => new URLSearchParams(location.search)
  const defVer = instance.mcVersion || ''

  // 深链：?mq= 名称 / ?mver= 版本 / ?mcat= 类型 / ?msrc= 线路 / ?msort= 排序。
  // 无头浏览器点不了「搜索」，交互态预置进 URL 是这个项目验证 UI 的惯例。
  // 前缀一律带 m（mq/msort/mcat）—— 别跟新建向导的 ?type=&mc= 抢参数名。
  const [name, setName] = useState(() => qs().get('mq') ?? '')
  const [ver, setVer] = useState(() => qs().get('mver') ?? defVer)
  const [cat, setCat] = useState(() => qs().get('mcat') ?? '')
  const [src, setSrc] = useState<MarketSource>(() => {
    const s = qs().get('msrc')
    return s === 'official' || s === 'mirror' ? s : 'auto'
  })
  const [sort, setSort] = useState<SortKey>(() => {
    const s = qs().get('msort')
    return s === 'newest' || s === 'updated' || s === 'follows' ? s : 'downloads'
  })

  // 已生效的条件。列表只认它。
  const [applied, setApplied] = useState<Applied>(() => ({ name, ver, cat, src, sort }))

  const [cats, setCats] = useState<MarketCategory[]>([])
  const [vers, setVers] = useState<string[]>([])
  const [hits, setHits] = useState<MarketHit[]>([])
  const [base, setBase] = useState('')
  const [mirrored, setMirrored] = useState(false)
  /**
   * 中文关键词被后端换成英文名时的那次改写。
   * 一定要显示出来 —— 换错的时候用户得能一眼看出「它不是搜不到，是换错了」，
   * 否则就会被当成软件坏了。
   */
  const [zhQuery, setZhQuery] = useState<{ from: string; matched: string; english: string } | null>(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  // 中文简介：projectId → 译文。拿不到就不写 key，界面显示原文。
  const [zh, setZh] = useState<Record<string, string>>({})
  // 迟到的响应别覆盖新结果：连点排序 tab 会同时发出好几个请求，谁后发谁算数。
  const seq = useRef(0)
  const zhSeq = useRef(0)

  /**
   * 取这一屏的中文简介。**不 await**：列表先用原文显示出来，译文到了再换。
   * 翻译服务会间歇性超时（实测四次里有一次），把它放进主流程等于
   * 让「看一眼有什么模组」这件事随机多等四秒 —— 那比显示英文糟糕得多。
   */
  const loadZh = useCallback((rawIds: string[]) => {
    const ids = rawIds.filter(Boolean)
    if (ids.length === 0) return
    const my = ++zhSeq.current
    api
      .modTranslate(ids)
      .then((r) => {
        if (my !== zhSeq.current) return
        const m = r?.items ?? {}
        if (Object.keys(m).length > 0) setZh((prev) => ({ ...prev, ...m }))
      })
      .catch(() => {
        /* 翻译拿不到就显示原文 —— 这不是错误，别弹红字 */
      })
  }, [])

  const fetchList = useCallback(
    async (q: Applied, more: boolean) => {
      const key = q.name.trim()
      const my = ++seq.current
      const off = more ? offset : 0
      setLoading(true)
      setErr('')
      try {
        const r = await api.modSearch(key, loader, q.ver, {
          // 有关键词 → 相关度；没关键词 → 用户选的榜单口径。
          // （避免「没搜任何东西却按相关度排」—— 那种排序等于乱排。）
          sort: key ? 'relevance' : q.sort,
          cat: q.cat || undefined,
          src: q.src,
          offset: off,
          limit: PAGE,
        })
        if (my !== seq.current) return
        const items = r.items ?? []
        setHits((prev) => (more ? [...prev, ...items] : items))
        setBase(r.base ?? '')
        setMirrored(!!r.mirrored)
        setZhQuery(r.zh ?? null)
        setOffset(off + items.length)
        setHasMore(items.length >= PAGE)
        loadZh(items.map((i) => i.id))
        // 生效条件写回 URL：刷新 / 分享能回到同一屏
        const u = new URL(location.href)
        const put = (k: string, v: string) => {
          if (v) u.searchParams.set(k, v)
          else u.searchParams.delete(k)
        }
        put('mq', key)
        put('mver', q.ver === defVer ? '' : q.ver)
        put('mcat', q.cat)
        put('msrc', q.src === 'auto' ? '' : q.src)
        put('msort', q.sort)
        history.replaceState(null, '', u)
      } catch (e) {
        if (my !== seq.current) return
        setErr((e as Error).message)
        if (!more) setHits([])
      } finally {
        if (my === seq.current) setLoading(false)
      }
    },
    [loader, offset, defVer, loadZh],
  )

  const submit = useCallback(
    (next: Applied) => {
      setApplied(next)
      void fetchList(next, false)
    },
    [fetchList],
  )

  // 进页面就拉热门榜。用户原话：「全是空的只有搜索框，你让我怎么用？」
  // 分类与版本清单也在这趟取 —— 名单的唯一真相源是后端，前端不另写一份。
  useEffect(() => {
    void fetchList(applied, false)
    api.modCategories().then((r) => setCats(r.items ?? [])).catch(() => setCats([]))
    api.modGameVersions().then((r) => setVers(r.items ?? [])).catch(() => setVers([]))
    // 只在挂载时跑一次，之后的所有刷新都由用户触发
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 外部指名要找某个东西（比如「去装前置」）：换成那个词查一次。
  // 放在 effect 里而不是在点击处直接改 —— 点的时候市场视图可能还没挂载。
  useEffect(() => {
    if (!seed) return
    setName(seed)
    setVer(defVer)
    setCat('')
    submit({ name: seed, ver: defVer, cat: '', src, sort })
    // 跟着 seedN 走：同一个词被点第二次也要重新查一遍
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seedN])

  // 实例自己的版本排第一 —— 大多数时候用户要的就是它，别让他在 100 项里翻。
  const verOptions = useMemo(
    () => [defVer, ...vers.filter((v) => v !== defVer)].filter(Boolean),
    [defVer, vers],
  )

  const reset = () => {
    const d: Applied = { name: '', ver: defVer, cat: '', src: 'auto', sort }
    setName(d.name)
    setVer(d.ver)
    setCat(d.cat)
    setSrc(d.src)
    submit(d)
  }

  return {
    loader, defVer, verOptions,
    name, setName, ver, setVer, cat, setCat, src, setSrc, sort, setSort,
    applied, cats, hits, base, mirrored, loading, err, hasMore, zh, loadZh, zhQuery,
    submit, fetchList, reset,
  }
}

type MarketQuery = ReturnType<typeof useMarketQuery>

/**
 * 吸顶的那半：搜索条件面板 + 排序 + 状态行。
 * 它是「控件」，滚走了用户就得往回滚才能改条件 —— 所以整块跟着吸。
 */
function MarketControls({
  q,
  stuck,
  onExpand,
}: {
  q: MarketQuery
  /** 吸顶了 —— 只留最常用的那一个字段，其余收起来 */
  stuck: boolean
  /** 收起之后想改完整条件：滚回顶部，面板自己摊开 */
  onExpand: () => void
}) {
  const { name, setName, ver, setVer, cat, setCat, src, setSrc, sort, setSort, applied, cats, verOptions, defVer, submit, reset, loading } = q

  return (
    <div>
      {/*
        搜索条件面板 —— 设计稿 market.html 的 toolbar 形态：
        行一「搜索框 + 动作」，行二「版本 / 类型下拉 + 来源线路」，行三「排序标签」。
        搜索是显式提交（回车 / 点搜索才查），筛选标签点一下立即生效 ——
        「改一个字就重查」看着聪明，实际是在用户选的过程中不停打断他。
        ⚠ .mf-name / .mf-opt 是吸顶收起的钩子（index.css 里 .stuck 的联动），别动那两个类名。
      */}
      <div className="panel mfilter mt-3">
        <div className="toolbar">
          {/* mf-name：恒定独占整行 —— 收起后它是唯一留下的字段 */}
          <div className="mf-name flex min-w-[220px] flex-1 items-center gap-2">
            <Icon name="search" style={{ color: 'var(--ink-3)' }} />
            <input
              className="inp"
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit({ name, ver, cat, src, sort })
              }}
              placeholder="比如 sodium / lithium"
            />
          </div>
          <button
            type="button"
            className="btn sm"
            disabled={loading}
            onClick={() => submit({ name, ver, cat, src, sort })}
          >
            搜索
          </button>
          {/* 吸顶时收起：重置不是每次滚动都要干的事（真要重置就点「更多条件」滚回顶部） */}
          <span className="mf-opt">
            <button type="button" className="btn btn-ghost sm" onClick={reset}>
              重置条件
            </button>
          </span>
          {stuck && (
            <button
              type="button"
              className="btn btn-ghost sm"
              onClick={onExpand}
              title="滚回顶部，展开完整条件"
            >
              更多条件 ↑
            </button>
          )}
        </div>

        {/* 吸顶时收起：来源 / 版本 / 类型不是每次滚动都要改的东西 */}
        <div className="toolbar mf-opt" style={{ borderTop: 0 }}>
          <span className="eyebrow">VERSION</span>
          <select className="px-input" value={ver} onChange={(e) => setVer(e.target.value)}>
            <option value="">全部版本</option>
            {verOptions.map((v) => (
              <option key={v} value={v}>
                {v}
                {v === defVer ? '（本实例）' : ''}
              </option>
            ))}
          </select>
          <span className="eyebrow">TYPE</span>
          <select className="px-input" value={cat} onChange={(e) => setCat(e.target.value)}>
            <option value="">全部类型</option>
            {cats.map((c) => (
              <option key={c.key} value={c.key}>
                {c.label}
              </option>
            ))}
          </select>
          <div className="spacer" style={{ marginLeft: 'auto' }} />
          <span className="eyebrow">SOURCE</span>
          <div className="filters">
            {SOURCES.map((s) => (
              <button
                key={s.k}
                type="button"
                className={`chipf${src === s.k ? ' on' : ''}`}
                onClick={() => setSrc(s.k)}
                title={s.hint}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>

        {/* 排序不塞进「更多条件」：它改的是「怎么看这一屏」，吸顶时也要能换 */}
        <div className="toolbar" style={{ borderTop: 0 }}>
          <span className="eyebrow">SORT</span>
          <div className="filters">
            {SORTS.map((s) => (
              <button
                key={s.k}
                type="button"
                className={`chipf${applied.sort === s.k ? ' on' : ''}`}
                onClick={() => {
                  setSort(s.k)
                  submit({ ...applied, sort: s.k })
                }}
              >
                {s.label}
              </button>
            ))}
          </div>
        </div>
      </div>

      <p className="mono-meta mt-2.5">
        {applied.name.trim() ? `关键词「${applied.name.trim()}」· 相关度` : `榜单：${SORTS.find((s) => s.k === applied.sort)?.label ?? '热门下载'}`}
        {applied.cat && ` · 类型 ${cats.find((c) => c.key === applied.cat)?.label ?? applied.cat}`}
        {applied.ver && ` · 版本 ${applied.ver}`}
        {q.loader && ` · 加载器 ${q.loader}`}
        {!q.loader && '（原版没有模组加载器，只能装插件）'}
      </p>

      {/*
        来源说明放在结果上方，而不是只在报错时出现：
        后端会自动换源（官方不通 → 备用源），用户看得见才知道「不是玄学」。
        吸顶时收起 —— 它是解释性文字，不是每次滚动都要读。
      */}
      {q.base && !q.err && (
        <p className="mf-note mono-meta mt-1">
          数据来自 Modrinth{q.mirrored && '（首选源站没通，已自动改用备用源）'}
        </p>
      )}

      {q.err && (
        <div className="nbox d mt-3 max-w-[70ch]">
          <p className="leading-relaxed">{q.err}</p>
          <p className="mt-2 leading-relaxed">
            已经把这条线路上的源站都试过了。还可以：① 等一会儿再试（源站会间歇性抽风）；
            ② 把上面的「来源」换成另一条线路试试，看是官方不通还是镜像坏了；
            ③ 先去别的站下好 jar，用「本地上传」或「从直链」—— 效果一样。
          </p>
        </div>
      )}
    </div>
  )
}

/**
 * 百科条目名和用户输入的词其实是同一个（「机械动力」vs「机械动力」）时，
 * 说明里再念一遍条目名就是废话。这种只报「换成了什么英文」。
 * ⚠ 归一化要跟后端的 zhNorm 同一套口径：剥掉 [XX] 前缀、抹掉空格标点。
 */
function sameAsQuery(matched: string, from: string): boolean {
  const norm = (s: string) =>
    s
      .replace(/^\[[^\]]*\]\s*/, '')
      .replace(/[\s:：·、,，.。()（）\-_/+'"’]/g, '')
      .toLowerCase()
  return norm(matched) === norm(from)
}

/** 会滚走的那半：加载态 / 结果列表 / 加载更多 */
const VER_PAGE = 5

/**
 * 市场结果里的一行 + 它就地展开出来的版本列表。
 * 展开后**惰性加载**版本：第一次展开（vers 还是 null）才请求，之后收起再展开不重复请求（按行缓存）。
 */
function MarketRow({
  h,
  loader,
  mc,
  instance,
  onStarted,
  onToast,
  onInstallDeps,
}: {
  h: MarketHit
  loader: string
  mc: string
  instance: Instance
  onStarted: (taskId: string, label: string) => void
  onToast: (msg: string) => void
  onInstallDeps: (deps: DepTarget[]) => void
}) {
  const [vers, setVers] = useState<MarketVersion[] | null>(null)
  const [err, setErr] = useState('')
  const [page, setPage] = useState(0)
  const [busyId, setBusyId] = useState('')

  // 惰性加载：第一次展开才请求，之后复用这份缓存，不重复打接口
  useEffect(() => {
    if (vers !== null) return
    let alive = true
    api
      .modVersions(h.id, loader, mc)
      .then((d) => alive && setVers(d.items ?? []))
      .catch((e) => alive && setErr((e as Error).message))
    return () => {
      alive = false
    }
  }, [h.id, loader, mc, vers])

  const install = async (v: MarketVersion) => {
    setBusyId(v.id)
    try {
      const label = (h.zhName || h.title) + ' ' + v.versionNumber
      const t = await api.pluginInstallURL(instance.id, {
        url: v.fileUrl,
        filename: v.fileName,
        sha1: v.sha1,
        size: v.size,
        label,
      })
      onToast('已开始下载 ' + v.fileName)
      // 只装本体；前置绝不顺手静默装 —— 用户得自己点「装前置」才进队列
      onStarted(t.id, label)
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusyId('')
    }
  }

  // 这一版依赖的、且不是本项目自己的前置：过滤掉 projectId 为空或等于本模组 id 的
  const depOf = (v: MarketVersion) =>
    (v.requiredDeps ?? [])
      .filter((d) => d.projectId && d.projectId !== h.id)
      .map((d) => ({ projectId: d.projectId as string, name: '' }))

  const rows = vers ?? []
  const total = Math.max(1, Math.ceil(rows.length / VER_PAGE))
  const pg = Math.min(page, total - 1)
  const pageRows = rows.slice(pg * VER_PAGE, pg * VER_PAGE + VER_PAGE)

  return (
    <div className="rexp">
      {err && (
        <div className="nbox d">
          <div className="hd">
            <Icon name="ban" />
            版本列表没拿到
          </div>
          {err}
        </div>
      )}
      {vers === null && !err && <SkeletonList rows={4} />}
      {vers && rows.length === 0 && !err && (
        <Empty>这个项目没有匹配 {mc} / {loader || '当前加载器'} 的版本。</Empty>
      )}
      {vers && rows.length > 0 && (
        <>
          {/* 版本列表：设计稿 mod.html 的 vlist/vrow —— 版本号 / 标记与文件信息 / 下载数 / 操作 */}
          <div className="vlist">
            {pageRows.map((v) => {
              const deps = depOf(v)
              return (
                <div
                  key={v.id}
                  className="vrow"
                  style={{ gridTemplateColumns: '132px minmax(0,1fr) auto auto' }}
                >
                  <span className="ver">{v.versionNumber}</span>
                  <span className="min-w-0">
                    <span className="flex flex-wrap items-center gap-1.5">
                      {v.releaseType !== 'release' && (
                        <span className="chip s w">
                          {v.releaseType === 'beta' ? '测试版' : '早期版'}
                        </span>
                      )}
                      {deps.length > 0 && (
                        <span className="chip s w" title="这个版本还要求先装别的模组">
                          有前置 {deps.length}
                        </span>
                      )}
                    </span>
                    <span className="mono-meta mt-1 block break-all">
                      {v.fileName} · {fmtSize(v.size)}
                      {v.gameVersions?.length ? ` · MC ${v.gameVersions.slice(0, 3).join('/')}` : ''}
                    </span>
                    <span className="mono-meta mt-0.5 block">
                      {v.loaders?.length ? `${v.loaders.map(loaderLabel).join(' / ')} · ` : ''}
                      {v.downloads ? `下载 ${v.downloads.toLocaleString()} 次` : ''}
                    </span>
                  </span>
                  <span className="ract">
                    {deps.length > 0 && (
                      <button type="button" className="btn sm" onClick={() => onInstallDeps(deps)}>
                        <Icon name="zip" size={12} />
                        装前置（{deps.length}）
                      </button>
                    )}
                    <button
                      type="button"
                      className="btn btn-primary sm"
                      disabled={busyId !== ''}
                      onClick={() => install(v)}
                    >
                      <Icon name="dl" size={12} />
                      {busyId === v.id ? '提交中…' : '安装'}
                    </button>
                  </span>
                </div>
              )
            })}
          </div>
          {total > 1 && (
            <div className="mt-2 flex items-center gap-3">
              <button
                type="button"
                className="btn sm"
                disabled={pg === 0}
                onClick={() => setPage(pg - 1)}
              >
                上一页
              </button>
              <span className="mono-meta">第 {pg + 1} / {total} 页</span>
              <button
                type="button"
                className="btn sm"
                disabled={pg >= total - 1}
                onClick={() => setPage(pg + 1)}
              >
                下一页
              </button>
            </div>
          )}
        </>
      )}
    </div>
  )
}

function MarketResults({
  q,
  onPick,
  loader,
  mc,
  instance,
  onStarted,
  onToast,
  onInstallDeps,
}: {
  q: MarketQuery
  onPick: (h: MarketHit) => void
  loader: string
  mc: string
  instance: Instance
  onStarted: (taskId: string, label: string) => void
  onToast: (msg: string) => void
  onInstallDeps: (deps: DepTarget[]) => void
}) {
  const { hits, loading, err, hasMore, applied, fetchList, zh, zhQuery } = q
  const cats = q.cats

  /* 就地展开：点行的主体 → 在该行正下方展开版本列表。
     手风琴：同一时刻只展开一行，用 URL 的 ?expand= 预置，无头验证点不了也得能直达。 */
  const [expanded, setExpanded] = useState<string>(
    () => new URLSearchParams(location.search).get('expand') ?? '',
  )
  const toggleExpand = (id: string) => {
    setExpanded((prev) => {
      const next = prev === id ? '' : id
      const u = new URL(location.href)
      if (next) u.searchParams.set('expand', next)
      else u.searchParams.delete('expand')
      // 只 replaceState：展开/收起不该在浏览器历史里留下一跳，刷新也不会跳走
      history.replaceState(null, '', u)
      return next
    })
  }

  return (
    <div className="min-w-0">
      {/*
        中文关键词被换成了英文名 —— 换了就明说。
        市场里本来没有中文译名，不换就搜不到正主；但换错的时候（比如「暮色森林」
        换成了某个附属包）用户必须能一眼看出来，而不是以为「就这几个结果」。
        放在结果区而不是吸顶区：滚着看结果时它得一直在。
      */}
      {zhQuery && (
        <p className="mono-meta mt-3">
          {sameAsQuery(zhQuery.matched, zhQuery.from) ? (
            <>
              中文关键词「{zhQuery.from}」不在市场索引里，已换成英文名
              <b className="text-ink-1"> {zhQuery.english} </b>再搜。
            </>
          ) : (
            <>
              中文关键词「{zhQuery.from}」不在市场索引里，已按百科条目
              <b className="text-ink-1"> {zhQuery.matched} </b>译成
              <b className="text-ink-1"> {zhQuery.english} </b>再搜。
            </>
          )}
        </p>
      )}

      {loading && hits.length === 0 && (
        <SkeletonList rows={5} />
      )}

      {!loading && hits.length === 0 && !err && (
        <Empty>
          {zhQuery ? (
            <>
              中文是换对了（→ <b className="text-ink-1">{zhQuery.english}</b>），但市场里没有这个模组。
              有些模组只在 CurseForge 上架、没上 Modrinth —— 这种只能去别处下好 jar，
              再用「本地上传」或「从直链」装进来。
            </>
          ) : (
            <>这个条件下没列出东西。把「版本」「类型」放宽一点，或者确认这个实例的加载器是不是你要找的那种。</>
          )}
        </Empty>
      )}

      {hits.length > 0 && (
        // PCL 式大列表：一行一个通栏条目。点主体就地展开版本，不跳整屏。
        <div className="rlist mt-3 stagger" key={applied.sort + applied.name + applied.cat + applied.ver}>
          {hits.map((h) => {
            /*
              标签只挑「用途类」的展示，且最多两个。
              Modrinth 的 categories 里混着加载器名（forge/fabric/neoforge）——
              那是筛选项，不是这个模组的分类，混进标签里只是噪声。
            */
            const hwCats = (h.categories ?? [])
              .filter((c) => !MOD_LOADER_TAGS.has(c))
              .slice(0, 2)
            const isOpen = expanded === h.id
            return (
              <Fragment key={h.id}>
                {/*
                  rrow 是设计稿的通用行：32px 图标 + 主体 + 元信息 + 动作。
                  主体是可点击区（就地展开版本），「详情」是独立按钮 ——
                  展开的版本列表由 MarketRow 挂在下面，视觉上和这一行连成一体。
                */}
                <div className="rrow">
                  <span className="ricon">
                    {h.iconUrl ? <img src={h.iconUrl} alt="" loading="lazy" /> : <Icon name="cube" size={16} />}
                  </span>
                  <button
                    type="button"
                    className="rmain"
                    onClick={() => toggleExpand(h.id)}
                    aria-expanded={isOpen}
                    title={h.description}
                  >
                    <span className="rtitle">
                      {/*
                        中文名 + 英文名并排。中文是给人认的（「钠」比「Sodium」好认太多），
                        英文是给人核对的 —— 弱化但不省略：认出是哪个模组比省几个像素重要。
                        只有译名表里精确对上的才显示中文名，配不上就老老实实只有英文。
                      */}
                      {h.zhName && <span>{h.zhName}</span>}
                      <span style={h.zhName ? { fontWeight: 500, color: 'var(--ink-3)' } : undefined}>
                        {h.title}
                      </span>
                    </span>
                    {/* 有社区译文就用中文，没有就老老实实显示原文 —— 空着比英文更糟 */}
                    <span className="rsub one-line" title={zh[h.id] || h.description}>
                      {zh[h.id] || h.description}
                    </span>
                    {(hwCats.length > 0 || h.serverSide === 'unsupported' || h.clientSide === 'unsupported') && (
                      <span className="flex flex-wrap gap-1.5 pt-1.5">
                        {/*
                          「装不装得上服务端」放在最前 —— 这是列表里唯一会让人把服搞崩的信息。
                          ⚠ 只标需要提醒的两种，两端都行的不加：满屏徽标等于没有徽标。
                        */}
                        {h.serverSide === 'unsupported' && (
                          <span className="chip s side-warn" title="这个项目在服务端用不了（纯客户端模组）">
                            仅客户端
                          </span>
                        )}
                        {h.clientSide === 'unsupported' && h.serverSide !== 'unsupported' && (
                          <span className="chip s side-ok" title="只装在服务端，客户端不用装">
                            仅服务端
                          </span>
                        )}
                        {hwCats.map((c) => (
                          <span key={c} className="chip s">
                            {/* 后端筛选栏名单优先；它没收录的（如 decoration）用共享表兜底 —— 别把内部值漏上屏 */}
                            {cats.find((x) => x.key === c)?.label ?? catLabel(c)}
                          </span>
                        ))}
                      </span>
                    )}
                  </button>
                  <span className="mono-meta hidden shrink-0 text-right sm:block">
                    {h.downloads.toLocaleString()} 次下载
                    {h.dateUpdated && <span className="block">{fmtAgo(h.dateUpdated)}更新</span>}
                  </span>
                  <button
                    type="button"
                    className="btn btn-ghost xs"
                    onClick={() => onPick(h)}
                    aria-label={`查看 ${h.zhName || h.title} 的详情`}
                  >
                    详情
                  </button>
                </div>
                {/* 就地展开的版本列表：挂在行下面，rexp 让它和上面的行连成一体 */}
                {isOpen && (
                  <MarketRow
                    h={h}
                    loader={loader}
                    mc={mc}
                    instance={instance}
                    onStarted={onStarted}
                    onToast={onToast}
                    onInstallDeps={onInstallDeps}
                  />
                )}
              </Fragment>
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
    </div>
  )
}


/** 单个模组的详情。列表里只放得下名字和版本，简介 / 作者 / 完整依赖得在这里看。 */
function ModDetail({
  item,
  instanceId,
  onClose,
  onToggle,
  onDelete,
  onFindDep,
  onOpenDep,
  onOpenMarket,
}: {
  item: PluginItem
  /** 取模组图标要用（图标是按 实例 + 文件名 从磁盘读的） */
  instanceId: string
  onClose: () => void
  onToggle: (on: boolean) => void
  onDelete: () => void
  /**
   * 缺前置时给它一条路，别让用户自己去敲一遍名字。
   * installed = 它已经在目录里，只是被禁用了 —— 缺的不是下载，是把它找出来启用。
   */
  onFindDep: (keyword: string, where: 'market' | 'installed') => void
  /** 打开「装前置」弹层：直接挑版本装，不是丢一个搜索链接给用户 */
  onOpenDep: (depId: string, depName: string) => void
  /** 跳到这个模组的市场详情页（介绍 / 版本列表 / 关系星图都在那边） */
  onOpenMarket: (item: PluginItem) => void
}) {
  const m = item.meta
  return (
    <Dialog
      title={item.name}
      icon="cube"
      onClose={onClose}
      width={620}
      actions={
        <>
          <button
            type="button"
            className="btn"
            title="去市场里看它的完整详情：介绍、版本列表、关系星图"
            onClick={() => onOpenMarket(item)}
          >
            详情页
          </button>
          <button type="button" className="btn btn-danger" onClick={onDelete}>
            <Icon name="trash" size={12} />
            删除
          </button>
          <button type="button" className="btn" onClick={() => onToggle(!item.enabled)}>
            {item.enabled ? '禁用' : '启用'}
          </button>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            关闭
          </button>
        </>
      }
    >
      {/*
        抬头一行：左边是图标 + 机器标识 / 作者，右边不摆东西。
        名字在弹窗标题条上已经有了，这里**不再重复** —— 同一个名字印两遍，
        看起来像两个不同的东西。

        为什么把机器标识和作者从下面的表格提到抬头：这两栏回答的是「这到底是啥」，
        在读简介之前就该看到；文件路径那种是「要核对了才看」的，留在下面的行里。
      */}
      <div className="flex items-start gap-x-3">
        <span className="mk-ico" style={{ width: 46, height: 46 }}>
          <ModIcon id={instanceId} p={item} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className={item.enabled ? 'badge wl' : 'badge'}>{item.enabled ? '已启用' : '已禁用'}</span>
            {item.version && <span className="badge">{item.version}</span>}
            {m?.loader && <span className="badge">{loaderLabel(m.loader)}</span>}
          </div>
          {m?.id && <p className="mono-meta mt-1.5 break-all">{m.id}</p>}
          {m?.authors?.length ? (
            <p className="mt-1 text-[12.5px] text-ink-2">作者 {m.authors.join('、')}</p>
          ) : null}
        </div>
      </div>

      {m?.description && (
        <p className="mt-3 max-w-[68ch] text-[13px] leading-relaxed whitespace-pre-line text-ink-2">
          {m.description}
        </p>
      )}

      {/*
        键 — 值行。
        原来这里是两列 <dl>：在 620px 里长文件路径和 URL 会被挤成三四行，
        而且左右两列的值互相对不齐，看着像没排过。改成一列「左标签 / 右值」，
        长值自己换行，标签永远在同一个位置。用的是列表行那套 .srow，不再另造一种。
      */}
      <div className="mt-4 border-y border-line">
        <div className="srow">
          <span className="srow-k">文件</span>
          <span className="srow-v mono-value break-all">{item.file}</span>
        </div>
        <div className="srow">
          <span className="srow-k">大小</span>
          <span className="srow-v mono-value">{fmtSize(item.size)}</span>
        </div>
        <div className="srow">
          <span className="srow-k">修改时间</span>
          <span className="srow-v mono-value">{fmtTime(item.modTime)}</span>
        </div>
        {m?.source && (
          <div className="srow">
            <span className="srow-k">元数据读自</span>
            <span className="srow-v mono-value break-all">{m.source}</span>
          </div>
        )}
        {m?.homepage && (
          <div className="srow">
            <span className="srow-k">主页</span>
            <span className="srow-v min-w-0">
              <a
                className="text-[13px] break-all text-accent underline"
                href={m.homepage}
                target="_blank"
                rel="noreferrer"
              >
                {m.homepage}
              </a>
            </span>
          </div>
        )}
      </div>

      {!m && (
        <div className="nbox w" style={{ marginTop: 16 }}>
          <div className="hd">
            <Icon name="info" />
            这个 jar 里读不出元数据
          </div>
          原因：{item.metaErr || '不是标准模组包，或者包损坏'}。上面显示的名字和版本是
          <b>按文件名猜的</b>，可能不准。依赖检查也没法做 —— 它在列表里会被归到「读不出信息」那一档。
        </div>
      )}

      {(m?.requires?.length ?? 0) > 0 || (m?.optional?.length ?? 0) > 0 ? (
        <div className="mt-5">
          <p className="eyebrow mb-2">依赖</p>
          <div className="vlist" style={{ border: '1px solid var(--line-1)' }}>
            {(m?.requires ?? []).map((d) => {
              const iss = (item.issues ?? []).find((i) => i.depId === d.id && i.kind === 'required')
              return (
                <div
                  key={'r' + d.id}
                  className="vrow"
                  style={{ gridTemplateColumns: 'auto 1fr auto auto', cursor: 'default' }}
                >
                  <span className="badge ban">必需</span>
                  <span className="mono-value" style={{ minWidth: 0, wordBreak: 'break-all' }}>
                    {d.id}
                  </span>
                  {d.builtin ? (
                    <span className="mono-meta">运行环境自带</span>
                  ) : iss ? (
                    <>
                      <span className="badge w">
                        {iss.problem === 'disabled' ? `已装但被禁用：${iss.depName || d.id}` : '还没装'}
                      </span>
                      {iss.problem === 'disabled' ? (
                        // 已经躺在目录里了，缺的不是下载，是把它启用 —— 说清楚是哪一步
                        <button
                          type="button"
                          className="btn sm"
                          onClick={() => {
                            onClose()
                            onFindDep(iss.depName || d.id, 'installed')
                          }}
                        >
                          在列表里找到它
                        </button>
                      ) : (
                        <button
                          type="button"
                          className="btn btn-primary sm"
                          onClick={() => {
                            onClose()
                            // 直接开弹层挑版本，不再把人丢去市场自己搜一遍 ——
                            // 用户原话：「我不是让你给我一个链接去搜索」
                            onOpenDep(d.id, iss.depName || d.id)
                          }}
                        >
                          <Icon name="dl" size={12} />
                          选版本装
                        </button>
                      )}
                    </>
                  ) : (
                    <span className="mono-meta">已满足</span>
                  )}
                </div>
              )
            })}
            {(m?.optional ?? []).map((d) => (
              <div
                key={'o' + d.id}
                className="vrow"
                style={{ gridTemplateColumns: 'auto 1fr auto', cursor: 'default' }}
              >
                <span className="badge">可选</span>
                <span className="mono-value" style={{ minWidth: 0, wordBreak: 'break-all' }}>
                  {d.id}
                </span>
                {d.builtin && <span className="mono-meta">运行环境自带</span>}
              </div>
            ))}
          </div>
          <p className="mt-2 max-w-[68ch] text-[12px] leading-relaxed text-ink-3">
            「必需」的前置没装（或装了但被禁用），服务端启动时就会报错 —— 列表里那一行红字说的就是这件事。
          </p>
        </div>
      ) : null}
    </Dialog>
  )
}
