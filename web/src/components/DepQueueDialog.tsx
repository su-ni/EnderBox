import { useEffect, useRef, useState } from 'react'
import { api, type MarketHit, type MarketVersion } from '../api'
import { Icon, Panel, ConHead } from './design'
import { loaderLabel } from './projectMeta'
import { Dialog, SkeletonList, fmtSize } from './ui'
import type { Instance } from '../types'

/** 一个待装前置。projectId 是 Modrinth 项目；modId 是模组内部名（如 "jei"），得先搜到项目。 */
export type DepTarget = { projectId?: string; modId?: string; name: string }

const PAGE = 5

type Card = {
  key: string
  dep: DepTarget
  /** find = 还在按内部名搜项目；vers = 列版本；done = 已装；notfound = 搜不到 */
  stage: 'find' | 'vers' | 'done' | 'notfound'
  hit: MarketHit | null
  cands: MarketHit[] | null
  candsErr: string
  vers: MarketVersion[] | null
  versErr: string
  page: number
  /** 当前选中的版本：接口返回新的在前，直接取第一份，不自己写版本号比较 */
  selected: MarketVersion | null
  installing: boolean
  /** 这张卡正在等的下载任务 id。有了它才能问出「到底装成功没有」 */
  taskId: string
  /** 这张卡自己的失败原因（下载失败就写在卡上，不是只弹个 toast 就没了） */
  err: string
  /** 已装后先打标记，等短过渡再把它从队列里摘掉 */
  leaving: boolean
}

function makeCard(dep: DepTarget, i: number): Card {
  return {
    key: `${dep.projectId || dep.modId || dep.name}-${i}`,
    dep,
    stage: dep.projectId ? 'vers' : 'find',
    hit: null,
    cands: null,
    candsErr: '',
    vers: null,
    versErr: '',
    page: 0,
    selected: null,
    installing: false,
    taskId: '',
    err: '',
    leaving: false,
  }
}

/** 用户偏好「减少动态效果」时，滑出动画直接跳过，免得看着卡顿。 */
function prefersReduced(): boolean {
  return typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
}

/**
 * 装前置 —— 一次可能要装好几个，所以是一个**队列**：每个前置一张卡，纵向堆叠。
 *
 * 为什么不再像旧版那样「点一下就装最新的」：用户原话「我甚至都不知道它装的是哪个版本的前置」。
 * 这里每张卡**明确显示它将要装哪个版本**，装错了他一眼能看出来。
 *
 * 也只有「明确显示版本」这一个条件，能把「一键安装」和「静默装最新」区分开：
 * 一键安装装的也是用户**当前选中的**那一版，不是后端随手挑的最新版。
 */
export function DepQueueDialog({
  deps,
  loader,
  mc,
  instance,
  onClose,
  onToast,
  onStarted,
}: {
  deps: DepTarget[]
  loader: string
  mc: string
  instance: Instance
  onClose: () => void
  onToast: (msg: string) => void
  onStarted: (taskId: string, label: string) => void
}) {
  const [cards, setCards] = useState<Card[]>(() => deps.map(makeCard))

  // 正在跑的 fetch 去重：避免 re-render 把同一个请求发两遍（连点、搜索词变化都靠它挡）
  const fetching = useRef<Set<string>>(new Set())

  const remaining = cards.filter((c) => c.stage !== 'done').length
  const installable = cards.filter(
    (c) => c.stage === 'vers' && c.selected && !c.installing,
  ).length
  /** 只有模组内部名、还没确认是哪个项目的卡 —— 一键安装装不了它们，必须说出来 */
  const unresolved = cards.filter((c) => c.stage === 'find').length

  /** 已装：先打 leaving 让卡片滑出，过一小段时间再从数组里摘掉 */
  const markDone = (key: string) => {
    setCards((prev) =>
      prev.map((c) => (c.key === key ? { ...c, stage: 'done', installing: false, leaving: true } : c)),
    )
    setTimeout(() => {
      setCards((prev) => prev.filter((c) => c.key !== key))
    }, prefersReduced() ? 0 : 260)
  }

  // 让轮询能读到**最新**的卡片，而不是创建定时器那一刻的快照。
  // 否则新开始下载的卡不在闭包里、早就装完的卡还在被反复问一遍。
  const cardsRef = useRef<Card[]>([])
  useEffect(() => {
    cardsRef.current = cards
  }, [cards])

  // 某张卡的下载任务结束后把它从队列摘掉。
  // ⚠ **任务状态才是权威信号**，不是「文件名出现在已安装列表里」——
  //   下载失败时那个文件永远不会出现，只靠文件名匹配的话卡片会永远停在
  //   「下载中…」、1 秒一次的轮询也永远停不下来（这就是「不静默失败」在这儿的落法）。
  const anyInstalling = cards.some((c) => c.installing)
  useEffect(() => {
    if (!anyInstalling) return
    let alive = true
    const iv = setInterval(async () => {
      // ⚠ 要摘掉的 key 必须在 **setState 之外**先算好。
      //   在 setCards 的 updater 里往数组 push 是一种错觉：updater 不保证同步执行，
      //   拿到的往往是空数组 —— 卡片被标成隐藏却永远摘不掉，cards.length 到不了 0，
      //   于是「全部装完自动关闭」永远不触发。
      const pending = cardsRef.current.filter((c) => c.installing && c.taskId)
      if (pending.length === 0) return
      const doneKeys: string[] = []
      const failures: { key: string; err: string }[] = []
      for (const c of pending) {
        try {
          const t = await api.fetchTask(c.taskId)
          if (t.state === 'running') continue
          if (t.state === 'done') doneKeys.push(c.key)
          else failures.push({ key: c.key, err: t.err || '下载失败' })
        } catch {
          /* 查不到任务，下一 tick 再试；查不到不等于装失败 */
        }
      }
      if (!alive || (doneKeys.length === 0 && failures.length === 0)) return
      if (failures.length > 0) {
        setCards((prev) =>
          prev.map((c) => {
            const f = failures.find((x) => x.key === c.key)
            return f ? { ...c, installing: false, err: f.err } : c
          }),
        )
      }
      doneKeys.forEach(markDone)
    }, 1000)
    return () => {
      alive = false
      clearInterval(iv)
    }
  }, [anyInstalling, instance.id])

  // 所有卡都装完移出后，留 1.2s 让用户看一眼「装完了」再自动关。
  useEffect(() => {
    if (cards.length === 0) {
      const t = setTimeout(onClose, 1200)
      return () => clearTimeout(t)
    }
  }, [cards.length, onClose])

  // 只有内部名（modId）的前置：先按名字搜项目，让用户确认是哪个
  useEffect(() => {
    cards.forEach((card) => {
      if (card.stage !== 'find' || card.cands !== null || card.candsErr || fetching.current.has('c' + card.key))
        return
      fetching.current.add('c' + card.key)
      const kw = card.dep.name || card.dep.modId || ''
      api
        .modSearch(kw, loader, mc, { limit: 8 })
        .then((d) =>
          setCards((prev) => prev.map((c) => (c.key === card.key ? { ...c, cands: d.items ?? [] } : c))),
        )
        .catch((e) =>
          setCards((prev) =>
            prev.map((c) => (c.key === card.key ? { ...c, candsErr: (e as Error).message } : c)),
          ),
        )
        .finally(() => fetching.current.delete('c' + card.key))
    })
  }, [cards, loader, mc])

  // 已知 projectId 的卡：补一个标题（接口只回 id，名字得单独问一次）
  useEffect(() => {
    cards.forEach((card) => {
      if (!card.dep.projectId || card.hit || fetching.current.has('p' + card.key)) return
      fetching.current.add('p' + card.key)
      api
        .modProjects([card.dep.projectId])
        .then((d) => {
          const p = d.items?.[0]
          if (p)
            setCards((prev) =>
              prev.map((c) =>
                c.key === card.key
                  ? {
                      ...c,
                      hit: {
                        id: card.dep.projectId!,
                        title: p.title,
                        description: p.description ?? '',
                        iconUrl: p.iconUrl,
                        downloads: 0,
                      },
                    }
                  : c,
              ),
            )
        })
        .catch(() => {})
        .finally(() => fetching.current.delete('p' + card.key))
    })
  }, [cards])

  // 进入 vers 阶段：列版本（新的在前，默认选中第一份）
  useEffect(() => {
    cards.forEach((card) => {
      if (card.stage !== 'vers' || card.vers !== null || card.versErr || fetching.current.has('v' + card.key))
        return
      fetching.current.add('v' + card.key)
      const pid = card.hit?.id || card.dep.projectId || ''
      api
        .modVersions(pid, loader, mc)
        .then((d) =>
          setCards((prev) =>
            prev.map((c) =>
              c.key === card.key
                ? { ...c, vers: d.items ?? [], selected: c.selected ?? (d.items?.[0] ?? null) }
                : c,
            ),
          ),
        )
        .catch((e) =>
          setCards((prev) => prev.map((c) => (c.key === card.key ? { ...c, versErr: (e as Error).message } : c))),
        )
        .finally(() => fetching.current.delete('v' + card.key))
    })
  }, [cards, loader, mc])

  const install = async (card: Card, v: MarketVersion) => {
    setCards((prev) =>
      prev.map((c) =>
        c.key === card.key ? { ...c, installing: true, taskId: '', err: '' } : c,
      ),
    )
    try {
      const label = (card.hit?.title || card.dep.name || card.dep.projectId || '前置') + ' ' + v.versionNumber
      const t = await api.pluginInstallURL(instance.id, {
        url: v.fileUrl,
        filename: v.fileName,
        sha1: v.sha1,
        size: v.size,
        label,
      })
      // 记下任务 id：接下来由轮询去问这个任务**装成功了没有**（成功摘卡、失败写在卡上）。
      // 光把任务交给调用方盯着是不够的 —— 调用方只弹 toast，卡片自己不知道结果。
      setCards((prev) => prev.map((c) => (c.key === card.key ? { ...c, taskId: t.id } : c)))
      onStarted(t.id, label)
      onToast('已开始下载 ' + v.fileName)
    } catch (e) {
      setCards((prev) =>
        prev.map((c) =>
          c.key === card.key ? { ...c, installing: false, err: (e as Error).message } : c,
        ),
      )
      onToast((e as Error).message)
    }
  }

  /**
   * 一键安装。
   * ⚠ 只装「已经确认了项目、并且选好了版本」的卡。
   *   还没确认项目的卡（只有模组内部名）根本还没有可选版本 —— 硬装就又变成「静默装最新」。
   *   但也不能让它们**悄悄消失**在计数里，所以下面会把它们的条数明确写出来并提示去确认。
   */
  const installAll = () => {
    let n = 0
    cards.forEach((c) => {
      if (c.stage === 'vers' && c.selected && !c.installing) {
        install(c, c.selected)
        n++
      }
    })
    const left = cards.filter((c) => c.stage === 'find').length
    if (left > 0)
      onToast(`已开始装 ${n} 个；还有 ${left} 个前置要先确认是哪个项目，它们没有可选版本，装不了`)
  }

  const pickProject = (card: Card, c: MarketHit) =>
    setCards((prev) =>
      prev.map((x) => (x.key === card.key ? { ...x, hit: c, stage: 'vers' } : x)),
    )

  const selectVersion = (card: Card, v: MarketVersion) =>
    setCards((prev) => prev.map((x) => (x.key === card.key ? { ...x, selected: v } : x)))

  const setPage = (card: Card, p: number) =>
    setCards((prev) => prev.map((x) => (x.key === card.key ? { ...x, page: p } : x)))

  const renderCard = (card: Card) => {
    const title = card.hit?.title || card.dep.name || card.dep.projectId || '这个前置'
    /** find / notfound = 还认不出是市场里的哪个项目 —— 卡头标「待确认项目」 */
    const pending = card.stage === 'find' || card.stage === 'notfound'
    return (
      <Panel key={card.key} flush className={`dep-card${card.leaving ? ' leaving' : ''}`}>
        {/* 卡头：设计稿两张卡分别是「已确认项目」(绿勾) 与「待确认项目」(黄放大镜) */}
        <ConHead
          icon={pending ? 'search' : 'check'}
          iconColor={pending ? 'var(--warn)' : 'var(--ok)'}
          title={title}
        >
          <div className="spacer" />
          {card.stage === 'done' ? (
            <span className="badge wl">已装</span>
          ) : pending ? (
            <span className="badge w">待确认项目</span>
          ) : (
            <span className="badge wl">已确认项目</span>
          )}
        </ConHead>

        {/* 设计稿的卡体：12px 14px 内边距（.panel 自己没有内边距，见 design-overrides） */}
        <div style={{ padding: '12px 14px' }}>
          {/* 失败要落在卡上：只弹一个 toast 就消失，用户回头看到卡片还写着「装这个」会以为是没点 */}
          {card.err && (
            <div className="nbox d" style={{ marginBottom: 10 }}>
              <div className="hd">
                <Icon name="ban" />
                上一次没装上
              </div>
              {card.err}
            </div>
          )}

          {/* 只有内部名：先搜项目，让用户确认是哪个再挑版本 */}
          {card.stage === 'find' && (
            <>
              <p style={{ fontSize: 12.5, color: 'var(--ink-2)', lineHeight: 1.7 }}>
                这个前置的 ID 是{' '}
                <b className="mono-value" style={{ color: 'var(--ink-1)' }}>
                  {card.dep.modId}
                </b>
                —— 那是模组自己写的内部名，不是市场里的项目。下面按名字在市场里找了一遍，
                <b style={{ color: 'var(--ink-1)' }}>确认是哪个之后</b>再挑版本：
              </p>
              {card.cands === null ? (
                <div style={{ marginTop: 10 }}>
                  <SkeletonList rows={3} />
                </div>
              ) : card.candsErr ? (
                <div className="nbox d" style={{ marginTop: 10 }}>
                  {card.candsErr}
                </div>
              ) : card.cands.length === 0 ? (
                // 搜不到：如实说，并给旧代码里那段替代路径，不编一个装不了的入口
                <div className="nbox w" style={{ marginTop: 10 }}>
                  <div className="hd">
                    <Icon name="info" />
                    市场里没搜到
                  </div>
                  市场里没搜到匹配 {loader || '当前加载器'} / MC {mc} 的「{card.dep.name}」。
                  它可能不在 Modrinth 上，或者这个名字搜不到 —— 可以去市场页手动搜，
                  也可以用「从直链」把 jar 装进来。
                </div>
              ) : (
                <div className="vlist" style={{ marginTop: 10, border: '1px solid var(--line-1)' }}>
                  {card.cands.map((c) => (
                    <button
                      key={c.id}
                      type="button"
                      className="vrow"
                      style={{ gridTemplateColumns: '1fr auto' }}
                      onClick={() => pickProject(card, c)}
                    >
                      <span style={{ minWidth: 0 }}>
                        <span className="ver" style={{ fontFamily: 'var(--f-body)', fontSize: 13 }}>
                          {c.title}
                        </span>
                        {/* 项目描述：名字相近的两个项目得靠它分清楚，选错了没有回头路 */}
                        {c.description && (
                          <span
                            className="line-clamp-2"
                            style={{
                              marginTop: 2,
                              fontSize: 12,
                              color: 'var(--ink-3)',
                              lineHeight: 1.6,
                            }}
                          >
                            {c.description}
                          </span>
                        )}
                      </span>
                      <span className="fmeta" style={{ flex: 'none' }}>
                        {c.downloads.toLocaleString()} 下载
                      </span>
                    </button>
                  ))}
                </div>
              )}
              {/* 还没认项目就没有版本可选 —— 把「将安装」显式写成 —，与右边禁用的按钮对上 */}
              <div className="mt-3 flex flex-wrap items-center gap-2.5">
                <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
                  将安装：
                  <b className="mono-value" style={{ color: 'var(--ink-3)' }}>
                    —
                  </b>
                </span>
                <div className="ml-auto" />
                <button type="button" className="btn sm" disabled>
                  装这个
                </button>
              </div>
            </>
          )}

          {/* 列版本：每页 5 条，默认选中最新的那一版 */}
          {card.stage === 'vers' && (
            <>
              {card.versErr && (
                <div className="nbox d" style={{ marginBottom: 10 }}>
                  {card.versErr}
                </div>
              )}
              {card.vers === null ? (
                <SkeletonList rows={4} />
              ) : card.vers.length === 0 && !card.versErr ? (
                <div className="nbox w">
                  <div className="hd">
                    <Icon name="info" />
                    没有匹配的版本
                  </div>
                  这个项目没有匹配 {mc} / {loader || '当前加载器'} 的版本 —— 它可能是为其它加载器
                  或其它游戏版本发布的。可以回市场页搜它，看看都支持哪些版本。
                </div>
              ) : (
                (() => {
                  const list = card.vers!
                  const total = Math.max(1, Math.ceil(list.length / PAGE))
                  const page = Math.min(card.page, total - 1)
                  const items = list.slice(page * PAGE, page * PAGE + PAGE)
                  return (
                    <>
                      <div className="vlist" style={{ marginTop: 10, border: '1px solid var(--line-1)' }}>
                        {items.map((v) => {
                          const sel = card.selected?.id === v.id
                          return (
                            <div
                              key={v.id}
                              role="button"
                              tabIndex={0}
                              aria-pressed={sel}
                              className={`vrow${sel ? ' on' : ''}`}
                              style={{ gridTemplateColumns: '1fr auto' }}
                              onClick={() => selectVersion(card, v)}
                              onKeyDown={(e) => {
                                if (e.key === 'Enter' || e.key === ' ') {
                                  e.preventDefault()
                                  selectVersion(card, v)
                                }
                              }}
                            >
                              <div style={{ minWidth: 0 }}>
                                <div className="flex flex-wrap items-center gap-2">
                                  <span className="ver">{v.versionNumber}</span>
                                  {v.releaseType !== 'release' && (
                                    <span className="chip s w">
                                      {v.releaseType === 'beta' ? '测试版' : '早期版'}
                                    </span>
                                  )}
                                </div>
                                <p className="mono-meta" style={{ marginTop: 2, wordBreak: 'break-all' }}>
                                  {v.fileName} · {fmtSize(v.size)}
                                  {v.downloads ? ` · 下载 ${v.downloads.toLocaleString()} 次` : ''}
                                </p>
                              </div>
                              <span className="fmeta" style={{ flex: 'none' }}>
                                {v.loaders?.length ? v.loaders.map(loaderLabel).join(' / ') : ''}
                                {v.gameVersions?.length ? ` · MC ${v.gameVersions.slice(0, 3).join('/')}` : ''}
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
                            disabled={page === 0}
                            onClick={() => setPage(card, page - 1)}
                          >
                            上一页
                          </button>
                          <span className="mono-meta">
                            第 {page + 1} / {total} 页
                          </span>
                          <button
                            type="button"
                            className="btn sm"
                            disabled={page >= total - 1}
                            onClick={() => setPage(card, page + 1)}
                          >
                            下一页
                          </button>
                        </div>
                      )}
                      <div className="mt-3 flex flex-wrap items-center gap-2.5">
                        <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
                          将安装：
                          <b className="mono-value" style={{ color: 'var(--ink-1)' }}>
                            {card.selected?.versionNumber ?? '—'}
                          </b>
                        </span>
                        <div className="ml-auto" />
                        {card.installing ? (
                          <span className="badge w">下载中…</span>
                        ) : (
                          <button
                            type="button"
                            className="btn btn-primary sm"
                            disabled={!card.selected}
                            onClick={() => card.selected && install(card, card.selected)}
                          >
                            装这个
                          </button>
                        )}
                      </div>
                    </>
                  )
                })()
              )}
            </>
          )}
        </div>
      </Panel>
    )
  }

  return (
    <Dialog
      title={`装前置 · 共 ${remaining} 项待处理`}
      /* 设计稿的卡头图标：铜色 i-table —— 队列 = 一张待办清单 */
      icon="table"
      iconColor="var(--copper)"
      /*
        920 是为了排双列。Dialog 是 w-full + maxWidth，窄屏会自动收，
        不会出现「窗口比屏还宽」。
      */
      width={920}
      onClose={onClose}
      actions={
        <>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            稍后再说
          </button>
          {/*
            设计稿 footer 的「全部装完」。与卡体上方那枚「一键装选中的 N 项」
            是同一个动作 —— 点完 installable 归 0，两枚一起退场，不会重复触发。
          */}
          {installable > 0 && (
            <button type="button" className="btn btn-start" onClick={installAll}>
              全部装完（{installable}）
            </button>
          )}
        </>
      }
    >
      <p className="mono-meta" style={{ marginBottom: 12 }}>
        环境 {loader || '当前加载器'} / MC {mc || '（未知）'} · 装进 {instance.name}
      </p>

      {(installable > 0 || unresolved > 0) && (
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            gap: 12,
            flexWrap: 'wrap',
            marginBottom: 12,
          }}
        >
          {installable > 0 && <span className="badge wl">可安装 {installable} 项</span>}
          {installable > 0 && (
            <span className="mono-meta" style={{ fontSize: 12.5 }}>
              每张卡装的是它当前选中的那一版，不是后台随手挑的最新版。
            </span>
          )}
          <div className="ml-auto" />
          {/*
            没确认项目的卡不能被一键装（它们还没有可选版本），但也不能让它们
            在计数里**悄悄消失** —— 用户会以为「我点了全部，怎么还剩一个」。
            措辞分两种：旁边真有一键装按钮时才说「另有」，否则那样写不通顺。
            ⚠ 颜色走内联：.mono-meta 是未分层规则，Tailwind 的 text-warn 在
              @layer utilities 里压不过它（旧代码这么写一直没生效过）。
          */}
          {unresolved > 0 && (
            <span className="mono-meta" style={{ fontSize: 12.5, color: 'var(--warn)' }}>
              {installable > 0
                ? `另有 ${unresolved} 个前置还没确认是哪个项目，一键装不了 —— 得先在卡片里挑出来。`
                : `${unresolved} 个前置还没确认是哪个项目 —— 先在卡片里挑出来，才谈得上选版本安装。`}
            </span>
          )}
          {installable > 0 && (
            <button type="button" className="btn btn-start sm" onClick={installAll}>
              一键装选中的 {installable} 项
            </button>
          )}
        </div>
      )}

      {/* 两张以上才排双列：只有一张时铺满更顺眼，也不会让卡片半宽悬着 */}
      <div
        className={cards.length > 1 ? 'grid items-start gap-3.5 md:grid-cols-2' : 'flex flex-col gap-3.5'}
      >
        {cards.map(renderCard)}
      </div>
    </Dialog>
  )
}
