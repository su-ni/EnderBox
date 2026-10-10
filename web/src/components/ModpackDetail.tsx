import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  api,
  type MarketProjectDetail,
  type MarketVersion,
  type ModpackInspect,
  type ModpackOp,
} from '../api'
import { renderMarkdown } from '../markdown'
import { Icon } from './design'
import { ModpackOpView } from './ModpackOpView'
import { PageFrame } from './PageFrame'
import {
  ExtLink,
  ProjectHead,
  ProjectShell,
  RailRow,
  RailSection,
} from './ProjectShell'
import { catLabel, licenseLabel, loaderLabel, releaseLabel, sideLabel } from './projectMeta'
import { Dialog, Empty, Skeleton, SkeletonList, fmtAgo, fmtSize } from './ui'

/**
 * 整合包详情页。
 *
 * 为什么是**独立一页**而不是在搜索结果下面就地展开一块：
 * 就地展开那一版做过，坑有两个，都不是样式问题而是"用户不知道发生了什么"：
 *   ① 变化的那一块在列表下面一两千像素处，点了跟没点一样 —— 得靠 scrollIntoView 替他滚上去，
 *      而这一滚，上下文（我刚才在看第几行）也一起丢了；
 *   ② 列表往上拉还能继续加载更多，一旦展开的版本区夹在中间，翻页的落点会随已展开内容变化，
 *      看着就像列表会自己跳。
 * 于是这里做成有自己 URL 的一页（?view=market&pk=<slug|id>）：既能返回、也能把链接发给别人。
 *
 * 页内再分两页（`?ptab=`）：**介绍** 与 **下载**。
 * 为什么分：作者正文实测能到 53KB（几十屏），一屏里「几十屏正文 + 二十行版本」上下叠着，
 * 想装包的人得先划过整个帖子才够得着版本列表 —— 而想读帖子的人又被版本列表挡在下面。
 * 这两件事的下一步完全不同（一个是"再读点"、一个是"挑一版装"），挤在一屏里就是互相挡路。
 *
 * 版面照 Modrinth 官方项目页（用户点名要的那个）：**抬头一行 + 下面左右两栏**。
 * 抬头左侧是身份与简介、右侧是主行动；两栏左边是页签与正文、右边是固定信息栏。
 * 为什么非要做成两栏：这一页原先只有一栏 72ch 的正文，在 1440 的屏上右边整整空掉六百多像素 ——
 * 用户的原话是「右边太空」。信息栏不是为了好看，是把「能不能装在我这版上 / 缺不缺东西 / 去哪提问」
 * 这些**看正文时一定会回头找**的东西，放到不用滚回去就能看见的位置。
 */
/**
 * 「最新版」= 第一个正式版；一个正式版都没发就退第一个（源站按发布时间倒序给）。
 * 详情页的默认选中、头部「安装」的确认框都认它 —— 一处口径，别两处各写一份。
 */
function latestVerOf(items: MarketVersion[]): MarketVersion | undefined {
  return items.find((v) => v.releaseType === 'release') ?? items[0]
}

/**
 * 安装作业的「取回凭据」。作业跑在守护进程里，跟这个组件活不活毫无关系 ——
 * 组件切走就 unmount，opId 这个 state 一丢，进度卡就瞎了（用户实报：
 * 装到一半去别的页面转了一圈，回来就找不到进度了）。
 * 所以把 opId/instId 按「包」存进 sessionStorage：切页回来（或刷新）能接上；
 * 关掉标签页即失效 —— 一个标签页里发起的安装，本来也只该在这个标签页里接着看。
 */
type OpStash = { opId: string; instId: string }
function readOpStash(project: string): OpStash | null {
  try {
    const raw = sessionStorage.getItem('sbx-pack-op:' + project)
    if (!raw) return null
    const v = JSON.parse(raw) as Partial<OpStash>
    if (typeof v?.opId !== 'string' || !v.opId) return null
    return { opId: v.opId, instId: typeof v.instId === 'string' ? v.instId : '' }
  } catch {
    return null
  }
}
function saveOpStash(project: string, s: OpStash) {
  try {
    sessionStorage.setItem('sbx-pack-op:' + project, JSON.stringify(s))
  } catch {
    /* 隐私模式这类存不了：不影响安装本身，只是切页回来接不上进度 */
  }
}

export function ModpackDetail({
  project,
  onBack,
  onOpenInstance,
  onInstanceCreated,
  onToast,
}: {
  /** 项目 id 或 slug */
  project: string
  /** 回列表 */
  onBack: () => void
  /** 装完之后去看看那个实例 */
  onOpenInstance: (id: string) => void
  onInstanceCreated: () => void
  onToast: (msg: string) => void
}) {
  const [d, setD] = useState<MarketProjectDetail | null>(null)
  const [base, setBase] = useState('')
  const [mirrored, setMirrored] = useState(false)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  // 版本
  const [versions, setVersions] = useState<MarketVersion[]>([])
  const [vLoading, setVLoading] = useState(true)
  const [vErr, setVErr] = useState('')
  const [verId, setVerId] = useState('')

  // 预览
  const [insp, setInsp] = useState<ModpackInspect | null>(null)
  const [insLoading, setInsLoading] = useState(false)
  const [insErr, setInsErr] = useState('')
  const [instName, setInstName] = useState('')

  // 安装作业。opId / instId 从 sessionStorage 里接回来（见文件头的 OpStash 说明）——
  // 切页回来、刷新回来，进度卡都能重新挂上同一笔作业。
  const [opId, setOpId] = useState(() => readOpStash(project)?.opId ?? '')
  const [op, setOp] = useState<ModpackOp | null>(null)
  const [opErr, setOpErr] = useState('')
  const [instId, setInstId] = useState(() => readOpStash(project)?.instId ?? '')
  const [busy, setBusy] = useState('')
  /** 「一键装最新版」的确认框开没开 */
  const [confirmOpen, setConfirmOpen] = useState(false)

  /** 页内分页：'desc' 介绍（默认） / 'dl' 下载与安装 */
  type Tab = 'desc' | 'dl'
  const [tab, setTab] = useState<Tab>(() =>
    new URLSearchParams(location.search).get('ptab') === 'dl' ? 'dl' : 'desc',
  )

  // 切页要同步到 URL：刷新 / 分享要能停在同一页，无头验证也点不了这个按钮 ——
  // 和 ?view= / ?pk= 同一个套路，交互态一律可寻址。
  // （「切页后把分页条拉回视口」那一手在 ProjectShell 里，两页共用，不必各写一份。）
  const goto = useCallback((t: Tab) => {
    setTab(t)
    const u = new URL(location.href)
    if (t === 'desc') u.searchParams.delete('ptab')
    else u.searchParams.set('ptab', t)
    history.replaceState(null, '', u)
  }, [])

  // 换 project 意味着换了一个包，整屏状态（版本、预览、作业）都得是新的。
  // 靠 App 那边的 key={packId} 重挂载来做，而不是在这里写一堆 setState 去清 ——
  // 后者既啰嗦又容易漏掉某个字段（漏一个就表现为「换了包但还留着上一个包的预览结果」）。
  const loadDetail = useCallback(async () => {
    setLoading(true)
    setErr('')
    try {
      const r = await api.modpackProject({ project })
      setD(r.item ?? null)
      setBase(r.base ?? '')
      setMirrored(!!r.mirrored)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
    // 版本同时拉，不串行等详情 —— 两个请求互不依赖，串行等于让用户多等一个来回。
    setVLoading(true)
    setVErr('')
    try {
      const items = (await api.modpackVersions({ project })).items ?? []
      setVersions(items)
      // 默认挑最新版（口径在 latestVerOf：优先第一个正式版）。这只是省一次点击，
      // 后面还有预览和安装两道闸，选错了能退回来。
      setVerId(latestVerOf(items)?.id ?? '')
    } catch (e) {
      setVErr((e as Error).message)
    } finally {
      setVLoading(false)
    }
  }, [project])

  useEffect(() => {
    void loadDetail()
  }, [loadDetail])

  // 安装作业轮询
  useEffect(() => {
    if (!opId) return
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const tick = async () => {
      try {
        const o = await api.modpackOp(opId)
        if (!alive) return
        setOp(o)
        if (o.state !== 'running') {
          if (o.state === 'done') {
            onToast('整合包安装完成 —— 打开实例，启动前先在体检里同意 EULA')
          } else if (o.state === 'canceled') {
            onToast('已取消安装。实例和已经下好的文件都留着，可以自己去那个实例接着弄')
          } else {
            onToast('安装失败，那个半成品实例已经被清掉了：' + (o.err || '未知原因'))
          }
          return
        }
      } catch (e) {
        if (alive) setOpErr((e as Error).message)
        return
      }
      timer = setTimeout(tick, 800)
    }
    tick()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [opId, onToast])

  const pickedVer = versions.find((v) => v.id === verId) ?? null
  const locked = !!opId && (!op || op.state === 'running')
  /** 头部「安装」确认框里的主角：最新版（口径同默认选中） */
  const latest = latestVerOf(versions)

  const doInspect = async (vidArg?: string, autoInstall = false) => {
    const vid = vidArg ?? verId
    if (!vid) {
      onToast('先在下面挑一个版本')
      return
    }
    setInsLoading(true)
    setInsErr('')
    setInsp(null)
    try {
      const r = await api.modpackInspect({ version: vid })
      setInsp(r)
      setInstName((n) => n || r.name || d?.title || '')
      // 快捷路径（确认框点了「确认安装」）：预览过了就接着装，别让用户再点一次。
      // ⚠ 用刚拿到的 r，不能读 state 里的 insp —— setState 还没提交，读到的是旧值。
      if (autoInstall && r.ok && r.token) await doInstall(r)
    } catch (e) {
      setInsErr((e as Error).message)
    } finally {
      setInsLoading(false)
    }
  }

  const doInstall = async (useInsp?: ModpackInspect) => {
    const ins = useInsp ?? insp
    if (!ins || !ins.ok || !ins.token) return
    setBusy('install')
    try {
      const r = await api.modpackInstall({
        token: ins.token,
        // 名字优先用户填的；没填就用包自己的推荐名（快捷路径下用户没机会填）。
        name: instName.trim() || ins.name || d?.title || undefined,
      })
      setInstId(r.instanceId)
      setOpId(r.opId)
      // 立刻落凭证：从这里开始，切页 / 刷新回来都能把作业接回来。
      saveOpStash(project, { opId: r.opId, instId: r.instanceId })
      onInstanceCreated()
      setInsp(null)
    } catch (e) {
      const er = e as Error & { status?: number }
      onToast(er.message)
      if (er.status === 400) setInsp(null)
    } finally {
      setBusy('')
    }
  }

  const cancelOp = async () => {
    if (!opId) return
    setBusy('cancel')
    try {
      await api.modpackOpCancel(opId)
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  // 正文渲染要记忆化：中位 4KB、最大 53KB 的解析不值得到每回重渲染都跑一遍。
  // 依赖写 `bodySrc` 这个已经落成字符串的值 —— 写 `d?.body` 的话是个每次求值都可能变的表达式，
  // React 编译器认不出它是稳定依赖，会直接放弃这个 memo（表现为每次渲染都重解析一遍）。
  const bodySrc = d?.body ?? ''
  const body = useMemo(() => (bodySrc ? renderMarkdown(bodySrc) : null), [bodySrc])

  const title = d?.title || '整合包'

  /*
    版式骨架在 ProjectShell 里（抬头一行 + 页内页签 + 左正文右信息栏 + 版心居中），
    整合包与模组两页共用同一套 —— 同样的版式抄第二遍，两边就会各自漂移。
    这里只负责「内容」：抬头写什么、左侧两页各放什么、右栏拼哪几节。
  */
  return (
    <PageFrame title={title}>
      {/* 「一键装最新版」的确认框。点头部「安装」弹它；主按钮一路走到底
          （自动预览 + 自动安装），「我自己挑版本」退回下载页的手动动线。 */}
      {confirmOpen && (
        <Dialog
          title="安装整合包"
          icon="dl"
          width={480}
          onClose={() => setConfirmOpen(false)}
          actions={
            <>
              <button
                type="button"
                className="btn btn-ghost"
                onClick={() => {
                  setConfirmOpen(false)
                  goto('dl')
                }}
              >
                我自己挑版本
              </button>
              <button
                type="button"
                className="btn btn-primary"
                disabled={!latest}
                onClick={() => {
                  if (!latest) return
                  setConfirmOpen(false)
                  // 显式把选中钉到最新版：用户之前可能在下载页手动选过别的版本，
                  // 而这个按钮的语义就是「装最新版」，别让旧选择把它截胡。
                  setVerId(latest.id)
                  goto('dl')
                  void doInspect(latest.id, true)
                }}
              >
                确认安装
              </button>
            </>
          }
        >
          {vLoading ? (
            <p>正在取版本列表…</p>
          ) : vErr ? (
            <p>版本列表没取到（{vErr}）—— 稍后重试，或者从「我自己挑版本」进下载页看看。</p>
          ) : !latest ? (
            <p>
              这个包没有已发布的可安装版本（作者没传 .mrpack 包本体）。
              从「我自己挑版本」进去能看到它的文件情况。
            </p>
          ) : (
            <>
              <p>
                将安装 <b className="text-ink-1">{d?.zhName || d?.title || '这个整合包'}</b> 的
                <b className="text-ink-1">最新版本 {latest.versionNumber}</b>
                {latest.releaseType !== 'release' && `（${releaseLabel(latest.releaseType)}）`}，
                并<b>新建一个世界</b> —— 不会改动任何已有实例。
              </p>
              <p className="mono-meta mt-2">
                {latest.gameVersions?.length
                  ? `MC ${latest.gameVersions.slice(0, 3).join(' / ')} · `
                  : ''}
                {fmtSize(latest.size)}
                {latest.datePublished ? ` · ${fmtAgo(latest.datePublished)}发布` : ''}
              </p>
              <p className="mt-2 text-ink-3">
                确认后会先把这个包下下来算一遍安装计划（几十 MB，慢的时候要等几分钟），
                然后自动开始安装；进度一直挂在右下角，切页面也不耽误。
              </p>
            </>
          )}
        </Dialog>
      )}
      <ProjectShell
        onBack={onBack}
        err={err}
        onRetry={() => void loadDetail()}
        loading={loading || !d}
        head={
          d ? (
            <ProjectHead
            eyebrow="PACK"
              iconUrl={d.iconUrl}
              title={d.zhName || d.title}
              /* 英文名给人核对 —— 降一级而不是藏起来：用户拿它去 Modrinth 查的时候要对得上 */
              origTitle={d.zhName ? d.title : undefined}
              /* 作者名是**尽力而为**的（后端要额外问一次团队接口），取不到就不写这一句 ——
                 写「作者未公开」是在替作者下结论：他可能公开了，只是这次没取到。 */
              meta={
                [
                  d.author ? `作者 ${d.author}` : '',
                  d.license?.id ? `许可 ${licenseLabel(d.license.id)}` : '',
                ]
                  .filter(Boolean)
                  .join(' · ') || undefined
              }
              description={d.description}
              stats={
                <>
                  {d.downloads.toLocaleString()} 次下载
                  {!!d.follows && ` · ${d.follows.toLocaleString()} 人关注`}
                  {d.dateModified && ` · ${fmtAgo(d.dateModified)}更新`}
                </>
              }
              /* 简介右侧的主行动入口：一键装最新版。
                 改版前它只把人领到下载页，用户还得「挑版本 → 预览 → 开始安装」自己走三步
                 —— 用户点名要「点击安装直接提示默认安装最新版本并提示用户确认」。
                 现在的形状：点开 → 确认框把「装哪版、多大、新建一个世界」说清 → 确认后
                 自动预览 + 自动开始安装。想看细节 / 换版本的人，从框里那扇
                 「我自己挑版本」的门进旧动线。 */
              action={
                <button
                  type="button"
                  className="btn btn-primary sm mt-0.5 shrink-0"
                  disabled={locked}
                  title={locked ? '安装任务还在跑 —— 装完再开新的' : '一键装最新版（点开先给你确认）'}
                  onClick={() => setConfirmOpen(true)}
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
            /* 版本数直接写在页签上：用户是为了"挑一版装"才点过来的，
               进去发现一版都没有，不如在点之前就告诉他。 */
            badge: versions.length > 0 ? `${versions.length} 个版本` : undefined,
          },
        ]}
        tab={tab}
        onTab={(id) => goto(id as Tab)}
        navLabel="整合包详情分页"
        desc={
                            /* ⚠ 正文这一层要 `md-wide`：全局 `.md-body` 卡在 72ch，
                     而这里左边这一栏本来就比 72ch 宽，不放开的话正文右边又会空出一条。 */
                  <div className="md-wide mt-4">
                    {body ?? (
                      <p className="mono-meta">这个包的作者没有写详细正文，只有抬头那段简介。</p>
                    )}
                  </div>
        }
        dl={
          <>
                    {/* ── 版本 ── */}
                    <div className="sec-head mt-5 mb-3">
                      <span className="idx">VERSION</span>
                      <h2>挑一个版本</h2>
                      <span className="sub">
                        只列出带 .mrpack 的版本 —— 点下去装不了的版本不该出现在这里
                      </span>
                    </div>

                    {vLoading && <SkeletonList rows={3} icon={false} />}
                    {vErr && (
                      <div className="nbox d">
                        <p>{vErr}</p>
                      </div>
                    )}
                    {!vLoading && !vErr && versions.length === 0 && (
                      <Empty>
                        这个包没有发过可安装的版本（文件里没有 .mrpack 包本体）。它可能只发了客户端包，
                        或者作者还没打服务端包。
                      </Empty>
                    )}

                    {versions.length > 0 && (
                      <div className="vlist">
                        {versions.map((v) => {
                          const on = v.id === verId
                          return (
                            <button
                              key={v.id}
                              type="button"
                              disabled={locked}
                              onClick={() => {
                                setVerId(v.id)
                                // 换版本就把上一个版本的预览清掉：预览结果是绑在那个版本上的，
                                // 留着会让人以为「我已经预览过这个新版本了」。
                                setInsp(null)
                                setInsErr('')
                              }}
                              className={`vrow${on ? ' on' : ''}`}
                              style={{ gridTemplateColumns: '110px minmax(0,1fr) auto' }}
                              aria-pressed={on}
                            >
                              <span className="ver">{v.versionNumber}</span>
                              <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                                {v.releaseType !== 'release' && (
                                  <span className="chip s w">{releaseLabel(v.releaseType)}</span>
                                )}
                                {(v.loaders ?? []).map((l) => (
                                  <span key={l} className="chip s">
                                    {loaderLabel(l)}
                                  </span>
                                ))}
                                <span className="mono-meta">
                                  {(v.gameVersions ?? []).slice(0, 3).join(' / ')}
                                </span>
                                <span className="mono-meta">{fmtSize(v.size)}</span>
                                {v.datePublished && <span className="mono-meta">{fmtAgo(v.datePublished)}</span>}
                              </span>
                              {/* 选中态除了底色，再给一枚看得见的小标 —— 键盘用户没有 hover */}
                              {on && <span className="chip s g">已选</span>}
                              {!on && <span />}
                            </button>
                          )
                        })}
                      </div>
                    )}

                    {versions.length > 0 && (
                      <div className="mt-3 flex flex-wrap items-center gap-2.5">
                        <button
                          type="button"
                          className="btn sm"
                          disabled={!verId || insLoading || locked}
                          onClick={() => void doInspect()}
                        >
                          <Icon name="dl" size={12} />
                          {insLoading ? '正在下载并计算…' : '预览安装'}
                        </button>
                        <span className="text-[12px] text-ink-3">
                          预览会先把包下下来算一遍（几十 MB，慢的时候要等几分钟），
                          这样点安装时就不用重下第二遍。只算，不会动任何文件。
                        </span>
                      </div>
                    )}

                    {insLoading && (
                      <div className="mt-3 space-y-2" role="status" aria-busy="true" aria-label="正在下载整合包">
                        <Skeleton w="46%" />
                        <Skeleton w="72%" />
                        <Skeleton w="35%" />
                        <p className="text-[12px] leading-relaxed text-ink-3">
                          正在从源站下这个包。别关面板 —— 下载是面板自己发起的，关掉就断了。
                        </p>
                      </div>
                    )}

                    {insErr && (
                      <div className="nbox d mt-3">
                        <p>{insErr}</p>
                      </div>
                    )}

                    {insp && <InspectView insp={insp} instName={instName} onName={setInstName} />}

                    {insp?.ok && (
                      <div className="mt-3.5 flex flex-wrap items-center gap-2.5">
                        <button
                          type="button"
                          className="btn btn-primary sm"
                          disabled={busy === 'install' || locked}
                          onClick={() => void doInstall()}
                        >
                          <Icon name="dl" size={12} />
                          {busy === 'install' ? '正在创建实例…' : '开始安装'}
                        </button>
                        {insp.canAutoJar ? (
                          <span className="text-[12px] text-ink-3">会顺带把服务端启动文件也准备好。</span>
                        ) : (
                          <span className="text-[12px] text-ink-3">启动文件要你自己放（原因见上面的提示）。</span>
                        )}
                      </div>
                    )}

                    {pickedVer && !insp && !insLoading && (
                      <p className="mt-3 text-[12px] leading-relaxed text-ink-3">
                        已选 {pickedVer.versionNumber}
                        {pickedVer.gameVersions?.length ? ` · MC ${pickedVer.gameVersions.join(' / ')}` : ''}。
                        点「预览安装」看这一版会装进去什么。
                      </p>
                    )}

                    {/* ── 安装作业 ── */}
                    {opId && (
                      <div className="mt-6">
                        <div className="sec-head mb-3">
                          <span className="idx">INSTALL</span>
                          <h2>正在装成新世界</h2>
                          <span className="sub">
                            在后台跑，可以切页面；别退出面板程序（退出就把它掐了）
                          </span>
                        </div>

                        <ModpackOpView
                          op={op}
                          opErr={opErr}
                          busy={busy}
                          verb="安装"
                          onCancel={() => void cancelOp()}
                          onOpenInstance={instId ? () => onOpenInstance(instId) : undefined}
                        />

                        {op?.state === 'done' && (
                          <p className="mt-2.5 text-[12px] leading-[1.7] text-ink-3">
                            启动之前还要同意一次 Minecraft 的 EULA（这是 Mojang 的条款，得你自己点，面板不替你按）。
                            打开这个实例、点「启动」，体检会把那一步提示出来。
                          </p>
                        )}
                        {op?.state === 'canceled' && (
                          <p className="mt-2.5 text-[12px] leading-[1.7] text-ink-3">
                            取消了，但世界和已经下好的文件都还在左边的列表里。想接着弄就打开它，
                            在「整合包」那一块重新预览、再装一次。
                          </p>
                        )}
                      </div>
                    )}
          </>
        }
        rail={
          /* 右栏读的是详情对象，得自己判空 —— 外层虽然保证了「加载完才渲染」，
             但它是个 prop，TS 的窄化传不进来。 */
          d ? (
            <>
                <RailSection title="兼容性">
                  {(d.gameVersions?.length ?? 0) > 0 && (
                    <>
                      <p className="mono-meta">Minecraft</p>
                      <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {(d.gameVersions ?? []).slice(0, 8).map((g) => (
                          <span key={g} className="badge">
                            {g}
                          </span>
                        ))}
                        {(d.gameVersions?.length ?? 0) > 8 && (
                          <span className="mono-meta">等 {(d.gameVersions?.length ?? 0) - 8} 个</span>
                        )}
                      </div>
                    </>
                  )}
                  {(d.loaders?.length ?? 0) > 0 && (
                    <>
                      <p className="mono-meta mt-3">加载器</p>
                      <div className="mt-1.5 flex flex-wrap gap-1.5">
                        {(d.loaders ?? []).map((l) => (
                          <span key={l} className="badge wl">
                            {loaderLabel(l)}
                          </span>
                        ))}
                      </div>
                    </>
                  )}
                  {d.serverSide && (
                    <>
                      <p className="mono-meta mt-3">服务端支持</p>
                      <p className="mt-1.5 text-[12.5px] text-ink-2">{sideLabel(d.serverSide)}</p>
                    </>
                  )}
                </RailSection>

                {(d.categories?.length ?? 0) > 0 && (
                  <RailSection title="分类">
                    <div className="flex flex-wrap gap-1.5">
                      {(d.categories ?? []).map((c) => (
                        <span key={c} className="badge">
                          {catLabel(c)}
                        </span>
                      ))}
                    </div>
                  </RailSection>
                )}

                {/* 外链：用户原话「你好歹让我能看个介绍什么的，加个跳转链接」。
                    站上的原页往往比我们能渲染的更全（视频、评论区、更新日志），
                    所以 Modrinth 页面永远排第一个 —— 而且是"真的另外去看"，不是替只想装包的人凑数。 */}
                {(d.webUrl || d.links) && (
                  <RailSection title="外链">
                    <div className="flex flex-col items-start gap-y-1.5">
                      {d.webUrl && <ExtLink href={d.webUrl} label="在 Modrinth 上打开原页" primary />}
                      {d.links?.issues && <ExtLink href={d.links.issues} label="反馈 / 讨论区" />}
                      {d.links?.source && <ExtLink href={d.links.source} label="源码" />}
                      {d.links?.wiki && <ExtLink href={d.links.wiki} label="Wiki" />}
                      {d.links?.discord && <ExtLink href={d.links.discord} label="Discord" />}
                      {(d.links?.donation ?? []).map((dn) => (
                        <ExtLink key={dn.url} href={dn.url} label={`赞助作者（${dn.label}）`} />
                      ))}
                    </div>
                  </RailSection>
                )}

                <RailSection title="详情">
                  <div className="space-y-1.5">
                    {d.license?.id && <RailRow k="许可" v={licenseLabel(d.license.id)} />}
                    {d.datePublished && <RailRow k="发布" v={fmtAgo(d.datePublished)} />}
                    {d.dateModified && <RailRow k="更新" v={fmtAgo(d.dateModified)} />}
                    {!!d.versionCount && <RailRow k="版本" v={`${d.versionCount} 个`} />}
                    {!!d.follows && <RailRow k="关注" v={d.follows.toLocaleString()} />}
                    <RailRow k="下载" v={d.downloads.toLocaleString()} />
                  </div>
                  {/* 这次是从镜像读的还是从官方读的 —— 源站不通时会走镜像，
                      不写出来用户会在"信息怎么对不上"的时候没线索。 */}
                  {base && mirrored && (
                    <p className="mono-meta mt-2.5 break-all">这次从镜像读的：{base}</p>
                  )}
                </RailSection>
            </>
          ) : null
        }
      />
    </PageFrame>
  )
}

/** 预览结果：把「会装什么、跳过什么、要下多少、缺什么」摊开来，再让用户决定。 */
export function InspectView({
  insp,
  instName,
  onName,
}: {
  insp: ModpackInspect
  instName: string
  onName: (v: string) => void
}) {
  if (!insp.ok) {
    return (
      <div className="nbox d mt-3.5">
        <p>{insp.refusal}</p>
      </div>
    )
  }

  const totalBytes = insp.packBytes + insp.downloadBytes
  const rows: { k: string; v: string }[] = [
    { k: 'Minecraft', v: insp.minecraft || '包内未声明' },
    {
      k: '加载器',
      v: insp.loaderKind
        ? `${insp.loaderKind}${insp.loaderVersion ? ' ' + insp.loaderVersion : ''} → 面板按 ${insp.serverType} 建实例`
        : '包内未声明',
    },
    { k: '会装进去', v: `${insp.install} 个文件` },
    {
      k: '会跳过',
      v: insp.skipped > 0 ? `${insp.skipped} 个（客户端专用的，装到服务端没用）` : '0 个',
    },
    { k: '覆盖文件', v: `${insp.overrideCount} 个（配置、脚本这类，不在文件索引里）` },
    {
      k: '要下载',
      v: `${fmtSize(insp.packBytes)} 包本体 + ${fmtSize(insp.downloadBytes)} 模组 = ${fmtSize(totalBytes)}`,
    },
  ]

  return (
    <div className="mt-3.5">
      <div>
        {rows.map((r) => (
          <div key={r.k} className="srow">
            <span className="k">{r.k}</span>
            <span className="v">{r.v}</span>
          </div>
        ))}
      </div>

      {/*
        装前核对的结果：.mrpack 说这些文件该装，Modrinth 说它们服务端用不了。
        作者标错是常态 —— 信错一次的代价是服务端直接起不来，所以必须摆在「开始安装」之上。
        ⚠ 措辞不能反过来说「其余已确认没问题」：核对不上（离线 / 非 Modrinth 源）时这栏是空的，
        空只代表「这次没核出来」。
      */}
      {insp.suspectClient && insp.suspectClient.length > 0 && (
        <div className="nbox w mt-3.5">
          <div className="hd">
            <Icon name="ban" size={14} />
            这个包里有 {insp.suspectClient.length} 个「仅客户端」模组
          </div>
          <p>
            它们在 Modrinth 上标着服务端用不了，但包里的清单却说要装 —— 装上去会混进 mods 目录，
            服务端可能起不来。装完到「插件与模组」里把这几个删掉就行：
          </p>
          <p className="mono-meta">
            {insp.suspectClient
              .slice(0, 5)
              .map((s) => s.title || (s.file.split('/').pop() ?? s.file))
              .join('、')}
            {insp.suspectClient.length > 5 ? ` 等 ${insp.suspectClient.length} 个` : ''}
          </p>
        </div>
      )}

      {!insp.canAutoJar && insp.jarNote && (
        <div className="nbox w mt-3.5">
          <div className="hd">
            <Icon name="ban" size={14} />
            启动文件要你自己放
          </div>
          <p>{insp.jarNote}</p>
        </div>
      )}

      <label className="mt-3.5 block">
        <span className="field-label">新世界的名字</span>
        <input
          className="px-input w-full sm:max-w-[320px]"
          value={instName}
          placeholder={insp.name || '整合包实例'}
          onChange={(e) => onName(e.target.value)}
        />
        <span className="field-hint">
          重名会自动加后缀。端口由面板自动挑一个空闲的，之后能在实例设置里改。
        </span>
      </label>
    </div>
  )
}
