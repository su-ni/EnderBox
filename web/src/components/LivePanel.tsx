import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { api } from '../api'
import type { LiveAudit, LiveItem, LivePlayer, LiveSnapshot } from '../types'
import { Dialog, Empty, SectionHead, Sheet, SkeletonList, fmtTime } from './ui'
import { StepInput } from './StepInput'
import {
  IconBlock,
  IconCheck,
  IconClock,
  IconInfo,
  IconKill,
  IconPackage,
  IconRefresh,
  IconShield,
  IconUsers,
  IconWarn,
} from './Icons'

// 需要二次确认的操作。
// 「给物品」不算破坏性（东西多了不会丢东西），但「清空背包」和「击杀」会真的让
// 玩家丢东西 —— 这两种分开对待，否则用户会对所有确认框脱敏，等于没确认。
const DESTRUCTIVE = new Set(['clear', 'kill'])

const GAMEMODES: { v: string; label: string }[] = [
  { v: 'survival', label: '生存' },
  { v: 'creative', label: '创造' },
  { v: 'adventure', label: '冒险' },
  { v: 'spectator', label: '旁观' },
]

/**
 * 写命令的中文名。
 *
 * 审计日志里只存 op 的英文标识 —— 那是接口契约，也是以后唯一稳定的东西；
 * 翻译只发生在这张表里。后端为了「好看」把机器字段改成人话，
 * 迟早会让某个前端分支匹配不上。
 */
const OP_LABEL: Record<string, string> = {
  give: '给物品',
  clear: '清空背包',
  tp: '传送',
  heal: '治疗',
  feed: '喂饱',
  gamemode: '切换模式',
  kill: '击杀',
}

/** 日志里的参数压成一行。参数是给「事后复盘」用的，不是给人逐字段读的。
 *
 *  模式要翻译：日志里「切换模式 UIBot survival」是半中半英的，
 *  而上面那张操作表里同一个东西叫「生存」—— 同一个概念在一个抽屉里两个名字，
 *  看的人会以为它们不是一回事。 */
function fmtArgs(a?: Record<string, unknown>): string {
  if (!a) return ''
  const out: string[] = []
  if (a.item) out.push(String(a.item))
  if (a.count != null) out.push(`×${a.count}`)
  if (a.mode) {
    const m = String(a.mode).toLowerCase()
    out.push(GAMEMODES.find((g) => g.v === m)?.label ?? String(a.mode))
  }
  if (a.x != null) out.push(`${a.x}, ${a.y}, ${a.z}`)
  if (a.armor) out.push('含装备')
  return out.join(' ')
}

/**
 * 「最后一份数据是什么时候的」。
 *
 * 粒度按分算，不用 fmtAgo：那边最细的一档是「今天」，而这个场景里
 * 「今天」跟没说一样 —— 用户要判断的是「刚才那一秒还在不在」。
 */
function fmtSince(iso?: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const s = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000))
  if (s < 60) return `${s} 秒前`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m} 分钟前`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h} 小时前`
  return `${Math.floor(h / 24)} 天前`
}

/** tick → 「还剩 1 分 20 秒」。状态效果的时长在 MC 里一律是 tick。 */
function fmtTicks(t: number): string {
  const s = Math.max(0, Math.round(t / 20))
  if (s < 60) return `${s} 秒`
  const m = Math.floor(s / 60)
  const rest = s % 60
  return rest ? `${m} 分 ${rest} 秒` : `${m} 分`
}

/** 血量条。用实心色块而不是进度组件 —— 圆角进度条在这个界面里是外来物。 */
function Bar({ v, max, tone }: { v: number; max: number; tone: 'hp' | 'food' | 'xp' }) {
  const pct = max > 0 ? Math.max(0, Math.min(100, (v / max) * 100)) : 0
  return (
    <span className="live-bar" title={`${v} / ${max}`}>
      <span className={`live-bar-fill live-bar-${tone}`} style={{ width: `${pct}%` }} />
    </span>
  )
}

/** 一格物品。有图标就画图标，没有就画一个占位方块 ——
 *  占位方块刻意是空的（不是问号），「这个物品没图」和「图裂了」不能长一样。 */
function ItemCell({ it }: { it: LiveItem }) {
  const ench = it.enchLabels ? Object.values(it.enchLabels) : []
  const title = [
    it.label,
    it.count > 1 ? `×${it.count}` : '',
    it.damage != null && it.maxDamage ? `耐久 ${it.maxDamage - it.damage}/${it.maxDamage}` : '',
    ...(it.lore ?? []),
    ...ench,
  ]
    .filter(Boolean)
    .join('\n')
  return (
    <div className="live-slot" title={title}>
      {it.icon ? (
        <img src={it.icon} alt="" width={22} height={22} className="live-icon" loading="lazy" />
      ) : (
        <span className="live-icon-none" />
      )}
      {it.count > 1 && <span className="live-count">{it.count > 64 ? '64+' : it.count}</span>}
      <span className="live-slot-name">{it.label}</span>
    </div>
  )
}

function ItemGrid({ items, empty }: { items?: LiveItem[]; empty: string }) {
  const list = (items ?? []).filter(Boolean)
  if (list.length === 0) return <p className="live-empty">{empty}</p>
  return (
    <div className="live-grid">
      {list.map((it, i) => (
        <ItemCell key={`${it.id}-${it.slot}-${i}`} it={it} />
      ))}
    </div>
  )
}

/** 只吃 id：实时数据这一块不需要实例的其他字段，
 *  把入参放宽能让它在任何拿到实例 id 的地方直接挂上去。 */
export default function LivePanel({ instance }: { instance: { id: string } }) {
  const [snap, setSnap] = useState<LiveSnapshot | null>(null)
  const [err, setErr] = useState('')
  const [loading, setLoading] = useState(true)
  const [detail, setDetail] = useState<LivePlayer | null>(null)
  const [busy, setBusy] = useState('')
  const [toast, setToast] = useState('')
  // 操作日志。抽屉里那句「每一步都会记进审计日志」要是没地方看，就是一句空话 ——
  // 承诺了就得给入口。
  const [auditOpen, setAuditOpen] = useState(false)
  const [audit, setAudit] = useState<LiveAudit[]>([])
  const [auditErr, setAuditErr] = useState('')
  // 「用户想不想开」后端没有单独回传（只能看到 jar 在不在），所以本地记一份。
  const [wantOn, setWantOn] = useState(true)
  const [confirm, setConfirm] = useState<{
    title: string
    body: ReactNode
    danger: boolean
    run: () => Promise<void>
  } | null>(null)

  // 表单：给物品 / 传送。各自独立的小状态，不做一个大表单 ——
  // 「给物品」和「传送」是两件不相关的事，塞进一张表单会让人以为要一起提交。
  const [giveId, setGiveId] = useState('minecraft:diamond')
  const [giveN, setGiveN] = useState(1)
  const [tpX, setTpX] = useState('')
  const [tpY, setTpY] = useState('')
  const [tpZ, setTpZ] = useState('')

  const alive = useRef(true)
  useEffect(() => {
    alive.current = true
    return () => {
      alive.current = false
    }
  }, [])

  const reload = useCallback(async () => {
    try {
      const s = await api.live(instance.id)
      if (!alive.current) return
      setSnap(s)
      setErr('')
    } catch (e) {
      if (!alive.current) return
      setErr(e instanceof Error ? e.message : String(e))
    } finally {
      if (alive.current) setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    void reload()
  }, [reload])

  // 服务器在跑才轮询。停着的时候每 5 秒打一次接口，只会让日志里堆满无用请求。
  const running = snap?.running ?? false
  useEffect(() => {
    if (!running) return
    const t = setInterval(() => void reload(), 5000)
    return () => clearInterval(t)
  }, [running, reload])

  /** 真的去执行。所有写操作都只从这里出去 —— 审计在后端，但入口只有这一个。 */
  const run = useCallback(
    async (op: string, target: string, args: Record<string, unknown>, label: string) => {
      setConfirm(null)
      setBusy(op)
      try {
        const r = await api.liveCommand(instance.id, op, target, args)
        setToast(r.message || `${label}完成`)
        void reload()
      } catch (e) {
        setToast(e instanceof Error ? e.message : String(e))
      } finally {
        setBusy('')
      }
    },
    [instance.id, reload],
  )

  /** 二次确认。破坏性操作（清空背包 / 击杀）走红标题，其余走普通确认。 */
  const ask = useCallback(
    (
      op: string,
      target: string,
      args: Record<string, unknown>,
      label: string,
      body: ReactNode,
    ) => {
      setConfirm({
        title: label,
        body,
        danger: DESTRUCTIVE.has(op),
        run: () => run(op, target, args, label),
      })
    },
    [run],
  )

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(''), 4000)
    return () => clearTimeout(t)
  }, [toast])

  const openAudit = useCallback(async () => {
    setAuditOpen(true)
    setAuditErr('')
    try {
      setAudit(await api.liveAudit(instance.id))
    } catch (e) {
      setAuditErr(e instanceof Error ? e.message : String(e))
    }
  }, [instance.id])

  const id = instance.id
  const players = snap?.online ?? []
  const offline = snap?.offline ?? []
  const sup = snap?.support

  /** 审计里存的是 UUID（改名也不影响追溯），但给人看的得是名字。
   *  查不到就退成短 UUID —— 不要显示一个空字符串，那看着像日志坏了。 */
  const nameOf = (uuid: string): string =>
    [...players, ...offline].find((p) => p.uuid === uuid)?.name ?? `${uuid.slice(0, 8)}…`

  return (
    <div>
      <SectionHead
        idx="01"
        title="实时状态"
        note={
          snap?.have
            ? `${snap.onlineCount} 人在线 · 插件 v${snap.pluginVersion ?? '?'}`
            : loading
              ? undefined
              : '没有数据'
        }
        right={
          <>
            {/* 不支持这个端的时候不摆这个开关：点了也没有东西可装可卸，
                摆着只会让人以为「点了就能有实时数据」。 */}
            {sup?.supported !== false && (
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                onClick={() => {
                  const next = !wantOn
                  setWantOn(next)
                  void api.companionToggle(id, next).then(reload)
                }}
                title="关掉之后，下次启动时面板会把插件从 plugins 目录里拿掉"
              >
                <IconShield size={12} />
                {wantOn ? '插件：开' : '插件：关'}
              </button>
            )}
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={() => void openAudit()}
              title="谁、什么时候、对谁、做了什么 —— 全在服务端本地的审计文件里"
            >
              <IconClock size={12} />
              操作日志
            </button>
            <button type="button" className="btn btn-sm" onClick={() => void reload()}>
              <IconRefresh size={12} />
              刷新
            </button>
          </>
        }
      />

      {/* 一句话就够。这段原本是三行，讲插件怎么实现的、不监听什么 ——
          那是**我们自己的事**，用户只需要知道「数据哪来的」和「它不会动服务端」。
          满屏解释性文字正是这个页面「乱」的主要来源，细节留在 docs 里。 */}
      <p className="text-[12.5px] leading-[1.7] text-ink-3">
        这些读文件读不到，是面板自带的<b className="text-ink-2">只读插件</b>定时写出来的 ——
        它不监听事件、不注册命令，玩家在 /help 里看不到它。
      </p>

      {err && (
        <div className="mt-3 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
          {err}
        </div>
      )}

      {loading || !snap ? (
        <div className="mt-4">
          <SkeletonList rows={2} />
        </div>
      ) : !snap.have ? (
        <div className="mt-4 border-l-[3px] border-warn bg-[#1d1a12] px-4 py-3">
          <div className="flex items-start gap-2 text-[13px] text-[#f0d79a]">
            <IconWarn size={14} />
            {/* Folia 不是「这个端没有插件系统」，是「它有，但它不让我们这么用」——
                这两种实话必须分开说，不然用户会去翻 plugins 目录找问题。 */}
            <span>
              {sup?.folia && <b className="mr-1">这台服务器是 Folia，用不了实时数据。</b>}
              {snap.note || '暂时没有实时数据。'}
            </span>
          </div>
          {sup?.hybrid && !sup.supported && (
            <div className="mt-2 flex items-start gap-2 text-[12.5px] text-ink-3">
              <IconInfo size={13} />
              <span>
                混合端（模组端 + 桥接模组）理论上能用，但出问题没人会替我们查 ——
                面板不会替你自动往里面塞插件。
              </span>
            </div>
          )}
        </div>
      ) : (
        <>
          {/* 旧数据要报，但**原因不能瞎猜**。
              面板自己托管着进程（running=true）时，"超过 30 秒没更新"只能解释成插件卡了；
              而面板没托管进程时，进程可能是被强杀的（它在的时候连收尾的机会都没有），
              这时还说"服务器还在跑"就是一句假话 —— 用户刚在顶上看到"已停止"。
              同一句话，两种场景，必须分开说。 */}
          {snap.stale && (
            <div className="mt-3 border-l-[3px] border-warn bg-[#1d1a12] px-4 py-2.5 text-[12.5px] text-[#f0d79a]">
              {snap.running ? (
                <>服务器还在跑，但快照已经超过 30 秒没更新了 —— 插件可能卡住了，下面是最后一次的数据。</>
              ) : (
                <>
                  这不是实时数据
                  {snap.generatedAt ? `：最后一份快照是 ${fmtSince(snap.generatedAt)}的` : ''} ——
                  服务器已经不在面板手上了，所以它现在到底在不在跑，面板说不上来。
                </>
              )}
            </div>
          )}

          {/* 混合端能跑，但风险要在「能用」的时候就说 —— 等出事了再说就晚了。
              用户拍板的原话是「照注入，但把风险说清楚」。 */}
          {sup?.hybrid && sup.supported && (
            <div className="mt-3 border-l-[3px] border-line bg-[#151814] px-4 py-2.5 text-[12.5px] text-ink-3">
              {sup.reason}
            </div>
          )}

          {/* 面板重启过（或者服务端不是面板拉起来的）就会这样：
              插件还在稳定写文件，说明服务器确实在跑，但面板手里没有那个进程。
              不说清楚的话，上面的「已停止」和这里的「1 人在线」会互相打架，
              用户只会得出「这软件坏了」这一个结论。 */}
          {!snap.running && !snap.stale && (
            <div className="mt-3 border-l-[3px] border-line bg-[#151814] px-4 py-2.5 text-[12.5px] text-ink-3">
              插件还在稳定写数据，说明服务器确实在跑 —— 但这个进程不是面板拉起来的
              （面板重启过，或者服务端是你手动开的）。所以实例状态那边会显示「已停止」，
              停服、重启、看控制台这些要面板托管才做的事暂时用不了。
            </div>
          )}

          {/* 在线的人。一人一行 —— 这条规矩是上一轮玩家管理定下的，
              实时数据更不能例外：同样的名字印三遍，看的人会以为有三个他。 */}
          <div className="mt-4 border-y border-line">
            {players.length === 0 ? (
              <div className="px-3 py-5">
                <Empty>现在没人在线。</Empty>
              </div>
            ) : (
              players.map((p) => (
                <div key={p.uuid} className="prow">
                  <div className="prow-main">
                    <div className="prow-id">
                      <IconUsers size={14} />
                      <div className="prow-name">
                        <button
                          type="button"
                          className="hover:underline"
                          onClick={() => setDetail(p)}
                          title="看背包、装备和操作"
                        >
                          {p.name}
                        </button>
                        {p.op && <span className="chip chip-w">OP</span>}
                        {p.dead && <span className="chip chip-d">已死亡</span>}
                        {p.gamemode && (
                          <span className="chip">
                            {GAMEMODES.find(
                              (g) => g.v === (p.gamemode ?? '').toLowerCase(),
                            )?.label ?? p.gamemode}
                          </span>
                        )}
                        {p.dimLabel && <span className="chip">{p.dimLabel}</span>}
                      </div>
                      <div className="mono-meta">
                        {p.world} · {Math.round(p.x)}, {Math.round(p.y)}, {Math.round(p.z)}
                        {p.ping != null ? ` · ${p.ping} ms` : ''}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-3">
                    <span className="live-stat">
                      <span className="text-[11px] text-ink-3">血</span>
                      <Bar v={p.health} max={p.maxHealth || 20} tone="hp" />
                      <span className="mono-value">{Math.round(p.health)}</span>
                    </span>
                    <span className="live-stat">
                      <span className="text-[11px] text-ink-3">饱</span>
                      <Bar v={p.food} max={20} tone="food" />
                      <span className="mono-value">{p.food}</span>
                    </span>
                    <button
                      type="button"
                      className="ibtn"
                      title="操作这个人"
                      onClick={() => setDetail(p)}
                    >
                      <IconPackage size={14} />
                    </button>
                  </div>
                </div>
              ))
            )}
          </div>

          {/* 离线的人：插件缓存了他们最后一次的状态。
              有就显示，没有也不空着 —— 「没缓存」和「没有」要分开说。 */}
          {offline.length > 0 && (
            <div className="mt-5">
              <div className="mb-2 text-[12px] text-ink-3">
                最近见过（下线前的最后一次状态）
              </div>
              <div className="border-y border-line">
                {offline.slice(0, 8).map((p) => (
                  <div key={p.uuid} className="prow">
                    <div className="prow-main">
                      <div className="prow-id">
                        <div className="prow-name">
                          <span>{p.name}</span>
                          <span className="chip chip-d">离线</span>
                        </div>
                        <div className="mono-meta">
                          {p.world} · {Math.round(p.x)}, {Math.round(p.y)}, {Math.round(p.z)}
                        </div>
                      </div>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      )}

      {/* ── 详情抽屉 ── */}
      {detail && (
        <Sheet
          title={detail.name}
          subtitle={`${detail.world ?? '?'} · ${detail.dimLabel ?? detail.dim ?? ''} · ${Math.round(detail.x)}, ${Math.round(detail.y)}, ${Math.round(detail.z)}`}
          onClose={() => setDetail(null)}
          width={560}
        >
          <div className="jlist">
            <div className="text-[12px] text-ink-3">状态</div>
            <div className="flex flex-wrap gap-x-5 gap-y-2 text-[12.5px]">
              <span>
                血量 <b className="mono-value">{Math.round(detail.health)}</b> /{' '}
                {Math.round(detail.maxHealth || 20)}
              </span>
              <span>
                饱食度 <b className="mono-value">{detail.food}</b> / 20
              </span>
              <span>
                等级 <b className="mono-value">{detail.level}</b>
              </span>
              {detail.ping != null && (
                <span>
                  延迟 <b className="mono-value">{detail.ping}</b> ms
                </span>
              )}
            </div>
          </div>

          {(detail.effects?.length ?? 0) > 0 && (
            <div className="jlist mt-4">
              <div className="text-[12px] text-ink-3">状态效果</div>
              <div className="flex flex-wrap gap-2">
                {detail.effects!.map((e) => (
                  <span key={e.id} className="chip chip-w">
                    {e.label}
                    {e.amplLabel ? ` ${e.amplLabel}` : ''} · {fmtTicks(e.duration)}
                  </span>
                ))}
              </div>
            </div>
          )}

          <div className="jlist mt-4">
            <div className="text-[12px] text-ink-3">装备</div>
            <ItemGrid
              items={Object.values(detail.equipment ?? {}).filter(Boolean) as LiveItem[]}
              empty="没穿任何装备"
            />
          </div>

          <div className="jlist mt-4">
            <div className="text-[12px] text-ink-3">背包</div>
            <ItemGrid items={detail.inventory} empty="背包是空的" />
          </div>

          <div className="jlist mt-4">
            <div className="text-[12px] text-ink-3">末影箱</div>
            <ItemGrid items={detail.enderChest} empty="末影箱是空的" />
          </div>

          {/* ── 操作 ──
              每一个都要二次确认：这些按钮会真的改玩家身上的东西。
              确认框里写清「对谁、做什么」，不写就没人敢点。 */}
          <div className="mt-6 border-t border-line pt-4">
            <div className="mb-2.5 text-[12px] text-ink-3">
              操作（会真的改 {detail.name} 身上的东西，每一步都会记进审计日志）
            </div>

            <div className="jlist">
              <div className="text-[12px] text-ink-3">给物品</div>
              <div className="flex flex-wrap items-center gap-2">
                <input
                  className="px-input px-input-mono"
                  style={{ width: 220 }}
                  value={giveId}
                  onChange={(e) => setGiveId(e.target.value)}
                  placeholder="minecraft:diamond"
                  title="物品的 id，比如 minecraft:diamond_sword"
                />
                <StepInput
                  value={giveN}
                  onChange={(v) => setGiveN(Number(v) || 1)}
                  min={1}
                  max={640}
                  label="数量"
                />
                <button
                  type="button"
                  className="btn btn-sm btn-grass"
                  disabled={busy !== '' || !giveId.trim()}
                  onClick={() =>
                    ask(
                      'give',
                      detail.uuid,
                      { item: giveId.trim(), count: Math.max(1, giveN) },
                      `给 ${detail.name} 物品`,
                      <>
                        会把 <b className="mono-value">{giveId.trim()}</b> ×{' '}
                        <b className="mono-value">{Math.max(1, giveN)}</b> 放进 {detail.name} 的背包。
                        背包满了的话，多出来的会掉在他脚下（不会凭空消失）。
                      </>,
                    )
                  }
                >
                  <IconCheck size={12} />
                  给物品
                </button>
              </div>
            </div>

            <div className="jlist mt-4">
              <div className="text-[12px] text-ink-3">传送到坐标</div>
              <div className="flex flex-wrap items-center gap-2">
                <input
                  className="px-input px-input-mono"
                  style={{ width: 90 }}
                  placeholder="X"
                  value={tpX}
                  onChange={(e) => setTpX(e.target.value)}
                />
                <input
                  className="px-input px-input-mono"
                  style={{ width: 90 }}
                  placeholder="Y"
                  value={tpY}
                  onChange={(e) => setTpY(e.target.value)}
                />
                <input
                  className="px-input px-input-mono"
                  style={{ width: 90 }}
                  placeholder="Z"
                  value={tpZ}
                  onChange={(e) => setTpZ(e.target.value)}
                />
                <button
                  type="button"
                  className="btn btn-sm"
                  disabled={busy !== '' || !tpX || !tpY || !tpZ}
                  onClick={() =>
                    ask(
                      'tp',
                      detail.uuid,
                      { x: Number(tpX), y: Number(tpY), z: Number(tpZ) },
                      `把 ${detail.name} 传送走`,
                      <>
                        会把 {detail.name} 传送到{' '}
                        <b className="mono-value">
                          {tpX}, {tpY}, {tpZ}
                        </b>
                        。坐标填错可能把他送进墙里或者高空摔死 —— 这是他自己的号，别乱填。
                      </>,
                    )
                  }
                >
                  传送
                </button>
              </div>
            </div>

            <div className="mt-4 flex flex-wrap gap-2">
              <button
                type="button"
                className="btn btn-sm"
                disabled={busy !== ''}
                onClick={() =>
                  ask('heal', detail.uuid, {}, `治疗 ${detail.name}`, <>回满血量、饱食度，并灭火。</>)
                }
              >
                治疗
              </button>
              <button
                type="button"
                className="btn btn-sm"
                disabled={busy !== ''}
                onClick={() =>
                  ask('feed', detail.uuid, {}, `喂饱 ${detail.name}`, <>把饱食度补满。</>)
                }
              >
                喂饱
              </button>
              {GAMEMODES.map((g) => (
                <button
                  key={g.v}
                  type="button"
                  className="btn btn-sm"
                  disabled={busy !== ''}
                  onClick={() =>
                    ask('gamemode', detail.uuid, { mode: g.v }, `把 ${detail.name} 切成${g.label}`, <>
                      会把 {detail.name} 的游戏模式切成 <b>{g.label}</b>。
                    </>)
                  }
                >
                  {g.label}
                </button>
              ))}
              <button
                type="button"
                className="btn btn-sm btn-danger"
                disabled={busy !== ''}
                onClick={() =>
                  ask(
                    'clear',
                    detail.uuid,
                    { armor: true },
                    `清空 ${detail.name} 的背包`,
                    <>
                      会清空 {detail.name} 的<b>全部背包与身上装备</b>，
                      并且<b className="text-danger">无法撤销</b>。他正在玩的话会当场发现东西没了。
                    </>,
                  )
                }
              >
                <IconBlock size={12} />
                清空背包
              </button>
              <button
                type="button"
                className="btn btn-sm btn-danger"
                disabled={busy !== ''}
                onClick={() =>
                  ask(
                    'kill',
                    detail.uuid,
                    {},
                    `击杀 ${detail.name}`,
                    <>
                      会把 {detail.name} 的血量直接归零。他背包里的东西会掉在地上，
                      死了之后按死亡规则处理。
                    </>,
                  )
                }
              >
                <IconKill size={12} />
                击杀
              </button>
            </div>
          </div>
        </Sheet>
      )}

      {/* ── 操作日志 ──
          只读。列「时间 / 结果 / 做了什么 / 对谁 / 参数」，
          失败的那条把原因挂在下面 —— 一条只写「失败」的日志等于没记。 */}
      {auditOpen && (
        <Sheet
          title="操作日志"
          subtitle={
            auditErr
              ? '读取失败'
              : `${audit.length} 条 · 这份服务器上的所有写操作，最新的在最上面`
          }
          onClose={() => setAuditOpen(false)}
          width={620}
        >
          {auditErr && (
            <div className="border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
              {auditErr}
            </div>
          )}
          {!auditErr && audit.length === 0 && <Empty>还没有任何操作记录。</Empty>}
          {!auditErr && audit.length > 0 && (
            <div className="border-y border-line">
              {audit.map((a, i) => (
                <div
                  key={`${a.at}-${a.op}-${i}`}
                  className="border-b border-line py-2.5 last:border-b-0"
                >
                  <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                    <span className={`chip ${a.ok ? 'chip-g' : 'chip-d'}`}>
                      {a.ok ? '成功' : '失败'}
                    </span>
                    <span className="text-[13px] font-semibold text-ink-1">
                      {OP_LABEL[a.op] ?? a.op}
                    </span>
                    <span className="text-[12.5px] text-ink-2">{nameOf(a.target)}</span>
                    <span className="mono-value text-[12px] text-ink-3">{fmtArgs(a.args)}</span>
                    <span className="ml-auto shrink-0 text-[11.5px] text-ink-3">
                      {fmtTime(a.at)}
                    </span>
                  </div>
                  {!a.ok && a.error && (
                    <div className="mt-1 text-[12px] text-[#ffc2c2]">{a.error}</div>
                  )}
                </div>
              ))}
            </div>
          )}
          <p className="mt-3 text-[12px] leading-[1.7] text-ink-3">
            这份日志存在面板数据目录下的 audit.log 里，面板只是把它读出来 ——
            换面板、重装都带不走它，也不会被实例自己的日志滚掉。
          </p>
        </Sheet>
      )}

      {confirm && (
        <Dialog
          title={confirm.title}
          danger={confirm.danger}
          onClose={() => setConfirm(null)}
          actions={
            <>
              <button
                type="button"
                className="btn btn-sm"
                data-autofocus
                onClick={() => setConfirm(null)}
              >
                取消
              </button>
              <button
                type="button"
                className={confirm.danger ? 'btn btn-sm btn-danger' : 'btn btn-sm btn-grass'}
                disabled={busy !== ''}
                onClick={() => void confirm.run()}
              >
                {busy ? '执行中…' : '确认'}
              </button>
            </>
          }
        >
          <div className="text-[13px] leading-[1.7] text-ink-2">{confirm.body}</div>
          <div className="mt-3 border-l-[3px] border-line px-3 py-2 text-[12px] text-ink-3">
            这一步会记进审计日志（谁、什么时候、对谁、做了什么）。
          </div>
        </Dialog>
      )}

      {toast && (
        <div className="mt-3 border-l-[3px] border-accent bg-[#141a13] px-4 py-2.5 text-[12.5px] text-[#cfe6bd]">
          {toast}
        </div>
      )}
    </div>
  )
}
