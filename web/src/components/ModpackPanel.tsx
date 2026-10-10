import { useCallback, useEffect, useState, type ReactNode } from 'react'
import {
  api,
  type ModpackFileChange,
  type ModpackInfo,
  type ModpackLoader,
  type ModpackOp,
  type ModpackUpgradePlan,
} from '../api'
import { ConFoot, ConHead, Icon, Panel, SecHead } from './design'
import { ModpackOpView } from './ModpackOpView'
import { ModpackExportPanel } from './ModpackExportPanel'
import { loaderLabel } from './projectMeta'
import { fmtSize, fmtTime, SkeletonList } from './ui'
import type { Instance } from '../types'

type Props = {
  instance: Pick<Instance, 'id' | 'name'>
  onToast: (msg: string) => void
}

/**
 * 整合包升级 / 安装面板。
 *
 * 这块和「转换为混合端」是同一类东西：先把代价摊开，再让你点。区别是升级会**删文件**，
 * 所以「要删哪些」必须在按钮之前、用最显眼的框摆出来 —— 用户得先看见自己会失去什么，
 * 才能决定点不点。输入有两条路：已装包的升级手填 Modrinth 版本号 / id（版本列表接口没做），
 * 全新安装直接指一个本地 .mrpack 文件 —— 预览用的哪条路，开始时就原样带回哪条（planSource）。
 */
export function ModpackPanel({ instance, onToast }: Props) {
  const [info, setInfo] = useState<ModpackInfo | null>(null)
  // 加载「装的是哪个包」时的两态：loading 用骨架，err 用红字 + 重试。
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')

  // 用户填的版本号 / id（版本列表没做，先手填）。
  const [version, setVersion] = useState('')
  // 本地 .mrpack 的绝对路径：装新包 / 不想手填版本号时用。和 version 二选一，
  // 两个都填在前端就挡下（服务端 400 也是同一条规则），用户不用等一次往返才知道错。
  const [packPath, setPackPath] = useState('')
  // 这次预览实际用的输入。开始时只带回这一个字段 —— 把两个都发出去，
  // 服务端按「二选一」会 400，而且重算的计划也未必和用户看过的是同一份。
  const [planSource, setPlanSource] = useState<'version' | 'pack'>('version')

  // 「只算不装」的结果。空 = 还没点预览；非空 = 进入「看代价」阶段。
  const [plan, setPlan] = useState<ModpackUpgradePlan | null>(null)
  const [planLoading, setPlanLoading] = useState(false)
  const [planErr, setPlanErr] = useState('')

  // 真升级的任务 id。非空 = 进入「轮询进度」阶段。
  const [opId, setOpId] = useState('')
  const [op, setOp] = useState<ModpackOp | null>(null)
  const [opErr, setOpErr] = useState('')

  // 服务端说「计划变了」（planHash 对不上，409）。这时不能自动重试 —— 重新预览把新清单摊出来，
  // 等用户看过后**再点一次**才传 confirmStale:true。stale=true 时按钮文案变成「仍然按新计划继续」。
  const [stale, setStale] = useState(false)

  // 只锁自己正在忙的那一格。一个忙全页变灰会被读成「点了没反应」。
  const [busy, setBusy] = useState('')

  // 作业是否还在跑。op 快照可能还没拿到第一拍（opId 刚发下来、轮询未回），这时按在跑算 ——
  // 宁可多显示一拍在跑，也不能把跑着的任务从界面上收掉。resetPlan / preview 收 opId
  // 之前都要过这道闸：跑着的任务不能收，收了进度就没地方显示了。
  const opRunning = !!opId && (!op || op.state === 'running')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const d = await api.modpackInfo(instance.id)
      setInfo(d)
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    void load()
  }, [load])

  // 轮询升级任务。只在 opId 存在时挂上，结束（done/failed/canceled）就停，不再轮。
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
          // 收尾提示：部分失败也要说，不能因为 state=done 就报「全部成功」。
          // 动词跟着 planSource 走：装一个新包叫「安装」，升一版叫「升级」，别混着说。
          const verb = planSource === 'pack' ? '安装' : '升级'
          if (o.state === 'done') {
            onToast(o.result.failed.length ? `${verb}完成，但有部分文件没处理好` : `整合包${verb}完成`)
            // 重读包信息：装完后 have=false → true 的切换靠这一次；升完也把版本号 / 上次升级时间刷成新的。
            void load()
            // 装包的计划描述的是「装之前会动哪些文件」，装完没必要再挂着，收掉让结算自己说话。
            if (planSource === 'pack') setPlan(null)
          } else if (o.state === 'canceled') {
            onToast(`已取消${verb}`)
          } else {
            onToast(`${verb}失败：` + (o.err || '未知原因'))
          }
          return
        }
      } catch (e) {
        if (alive) setOpErr((e as Error).message)
        return
      }
      timer = setTimeout(tick, 700)
    }
    tick()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opId])

  // 只算不装。version 与 pack 二选一，用哪个就把哪个发给后端，并记进 planSource。
  // 把「会动哪些文件」先问出来，再决定点不点开始。
  const preview = async () => {
    const v = version.trim()
    const p = packPath.trim()
    if (v && p) {
      onToast('版本号和本地 .mrpack 文件二选一，请清掉一个再预览')
      return
    }
    if (!v && !p) {
      onToast(
        info?.have
          ? '先填一个 Modrinth 版本号或版本 id，或指一个本地 .mrpack 文件'
          : '先填 .mrpack 文件的完整路径',
      )
      return
    }
    const usePack = p !== ''
    setPlanLoading(true)
    setPlanErr('')
    setPlan(null)
    // 跑着的任务不能收（与 resetPlan 同一条规矩）。正常路径下带着跑动中的 opId 进不来
    // （输入区被 opId 挡着），能进来的只有 start() 的 409 分流这一条路 —— 两处都要守闸。
    if (!opRunning) setOpId('')
    setPlanSource(usePack ? 'pack' : 'version')
    try {
      setPlan(await api.modpackUpgradePlan(instance.id, usePack ? { pack: p } : { version: v }))
    } catch (e) {
      setPlanErr((e as Error).message)
    } finally {
      setPlanLoading(false)
    }
  }

  // 真升级。planHash 原样带回，让服务端校验「要执行的」和「刚预览的」是同一份。
  // confirmStale 默认 false；只有用户**看见「计划变了」之后**再点一次，才传 true。
  const start = async (confirmStale: boolean) => {
    if (!plan || plan.refusal || plan.running) return
    // 作业还在跑就不准再起一笔（两个执行器对着同一批文件必然损坏）。按钮已经禁了，
    // 这里是第二道闸 —— 万一真发出去，后端也是 409 op_running。
    if (opRunning) return
    // 预览之后输入被清空了：带回空字段后端只会 400，这里先说人话。
    const input = planSource === 'pack' ? packPath.trim() : version.trim()
    if (!input) {
      onToast('预览时填的内容被清掉了，请重新预览一次')
      return
    }
    setBusy('start')
    try {
      const r = await api.modpackUpgradeStart(instance.id, {
        // 只带回预览时用的那个输入（planSource 记着）：服务端要拿同一个输入重算计划，
        // 才能对上 planHash —— 带错了或带多了都会白白 409 / 400。
        ...(planSource === 'pack' ? { pack: input } : { version: input }),
        planHash: plan.planHash,
        confirmStale,
      })
      setOpId(r.opId)
      // 计划阶段结束：开始按钮这一格收掉，进度接管。计划内容仍留在 plan 里当上下文展示。
      setStale(false)
    } catch (e) {
      const err = e as Error & { status?: number; reason?: string }
      // 409 有三种语义（服务端给 reason 码），只有「计划变了」走 stale 流程：
      if (err.status === 409 && err.reason === 'stale_plan') {
        // 计划已被重算（预览之后包更新了 / 文件被动了），和眼前这份不是同一份。
        // 不自动重试：重新预览拿最新清单，并标 stale，让用户看过后点「仍然按新计划继续」再确认。
        setStale(true)
        onToast('计划有变化，下面的清单已更新，请确认一遍再继续')
        await preview()
      } else {
        // 其余的（实例在跑 / 已有作业在跑）和别的失败一样原样转述 —— 服务端文案已经把
        // 「下一步该干嘛」说清了。绝不能当成「计划变了」去重预览：那一路会收 opId，
        // 把正在跑的作业从界面上抹掉，用户就只能看着一个来历不明的「计划有变化」。
        onToast(err.message)
      }
    } finally {
      setBusy('')
    }
  }

  const cancel = async () => {
    if (!opId) return
    setBusy('cancel')
    try {
      await api.modpackOpCancel(opId)
      // 不在这里自己改状态 —— 让上面的轮询把 state 拉成 canceled，避免两处状态不一致。
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  // 回到「选输入」阶段：清掉计划，但保留已填的版本号 / 包路径，方便换个再预览。
  // 同时清掉 stale：换了输入就是一次全新的预览，不该带着「计划变了」的标记。
  // 任务已结束（含失败 / 取消）才收掉 opId 让输入区回来 —— 跑着的任务不能收，
  // 收了进度就没地方显示了（判定走上面的 opRunning，preview() 同款）。
  const resetPlan = () => {
    setPlan(null)
    setPlanErr('')
    setStale(false)
    if (!opRunning) setOpId('')
  }

  /* 整页外壳：设计稿 05 的页头。刷新按钮放这里，它刷新的是「这个实例装了什么」。 */
  const head = (
    <SecHead
      idx="06"
      title="整合包"
      sub={
        <>
          升级会删文件，所以先把<b style={{ color: 'var(--ink-1)' }}>代价清单</b>列出来，你点了确认才动
        </>
      }
      style={{ '--rd': '.08s' } as React.CSSProperties}
      right={
        <button
          type="button"
          className="btn sm"
          disabled={busy === 'refresh'}
          onClick={() => {
            setBusy('refresh')
            void load().finally(() => setBusy(''))
          }}
          title="重新读取这个实例的整合包信息"
        >
          <Icon name="refresh" />
          刷新
        </button>
      }
    />
  )

  if (err && !info) {
    return (
      <div className="min-w-0">
        {head}
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>读整合包信息没成功</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn sm" onClick={() => void load()}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      </div>
    )
  }

  return (
    <div className="min-w-0">
      {head}

      {/* 制作整合包 —— 导入的反向操作，所以放在这一页。
          排在「升级」前面：多数人进来是想「把我这台服发出去」，
          而不是先想「升级我装的包」。 */}
      <ModpackExportPanel instance={instance} onToast={onToast} />

      {loading || !info ? (
        <Panel>
          <ConHead icon="cube" title="整合包升级" sub="正在读取…" />
          <div style={{ padding: 14 }}>
            <SkeletonList rows={3} icon={false} />
          </div>
        </Panel>
      ) : !info.have ? (
        /* 大多数实例的默认所见：不是整合包装的。控件往下排，别拿大段字开头。 */
        <Panel style={{ '--rd': '.12s' } as React.CSSProperties}>
          <ConHead
            icon="cube"
            iconColor="var(--ink-3)"
            title="整合包升级"
            sub={info.note || '这个实例不是由整合包安装的'}
          />
          <div style={{ padding: '12px 16px 16px' }}>
            {!opId && (
              <div className="field">
                <div>
                  <div className="lb">从本地 .mrpack 安装</div>
                  <div className="hint">预览只算不装，会先列出要新增哪些文件，确认了再开始</div>
                </div>
                <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
                  <input
                    id="mp-pack"
                    className="inp"
                    style={{ flex: 1, minWidth: 220 }}
                    value={packPath}
                    placeholder="例如 F:\packs\我的整合包.mrpack"
                    onChange={(e) => setPackPath(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !planLoading) void preview()
                    }}
                  />
                  <button
                    type="button"
                    className="btn"
                    disabled={planLoading || busy === 'preview'}
                    onClick={() => void preview()}
                  >
                    <Icon name="ul" />
                    {planLoading ? '计算中…' : '预览安装'}
                  </button>
                </div>
              </div>
            )}
            {planErr && <div className="nbox d" style={{ marginTop: 12 }}>{planErr}</div>}
            {plan && (
              <PlanView
                plan={plan}
                stale={stale}
                busy={busy}
                opRunning={opRunning}
                install={planSource === 'pack'}
                onStart={(cs) => void start(cs)}
                onReset={resetPlan}
              />
            )}
            {opId && (
              <ModpackOpView
                op={op}
                opErr={opErr}
                busy={busy}
                verb={planSource === 'pack' ? '安装' : '升级'}
                onCancel={() => void cancel()}
              />
            )}
          </div>
        </Panel>
      ) : (
        <>
          {/* ① 装的是哪个包。字段全部来自后端，缺的显示 —，不编值 */}
          <Panel style={{ '--rd': '.12s' } as React.CSSProperties}>
            <ConHead
              icon="cube"
              title="整合包升级"
              sub={info.name ? <span className="badge wl">{info.name}</span> : undefined}
            />
            <div style={{ padding: '2px 16px 12px' }}>
              <PackMeta info={info} />
            </div>
          </Panel>

          {/* ② 升级到什么版本：版本列表接口还没做，只能手填版本号 / id；本地 .mrpack 是二选一 */}
          {!opId && (
            <Panel style={{ '--rd': '.16s' } as React.CSSProperties}>
              <ConHead icon="refresh" iconColor="var(--copper)" title="升级到新版本" />
              <div style={{ padding: '4px 16px 16px' }}>
                <div className="field">
                  <div>
                    <div className="lb">要升级到的版本（Modrinth 版本号或版本 id）</div>
                    <div className="hint">
                      版本列表接口还没做，先手填 Modrinth 上的版本号或版本 id。点了「预览升级」只会算、不会真的改任何文件。
                    </div>
                  </div>
                  <input
                    id="mp-version"
                    className="inp"
                    value={version}
                    placeholder="例如 2.4 或 6OMYdNAO"
                    onChange={(e) => setVersion(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !planLoading) void preview()
                    }}
                  />
                </div>
                <div className="field">
                  <div>
                    <div className="lb">或用本地 .mrpack 文件</div>
                    <div className="hint">和上面的版本二选一。两个都填会被拦下并提示（服务端同一条规则）</div>
                  </div>
                  <input
                    id="mp-pack"
                    className="inp"
                    value={packPath}
                    placeholder="填 .mrpack 文件的完整路径"
                    onChange={(e) => setPackPath(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter' && !planLoading) void preview()
                    }}
                  />
                </div>
                <div style={{ display: 'flex', gap: 8, marginTop: 12, flexWrap: 'wrap', alignItems: 'center' }}>
                  <button
                    type="button"
                    className="btn btn-primary"
                    disabled={planLoading || busy === 'preview'}
                    onClick={() => void preview()}
                  >
                    <Icon name="search" />
                    {planLoading
                      ? '计算中…'
                      : packPath.trim() && !version.trim()
                        ? '预览安装'
                        : '预览升级'}
                  </button>
                  <span className="badge wl">只算不装 · 清单先摊开给你看</span>
                </div>
                {planErr && <div className="nbox d" style={{ marginTop: 12 }}>{planErr}</div>}
              </div>
            </Panel>
          )}

          {/* ③ 预览结果 + 进度。顺序就是用户做决定的顺序：拦死 → 版本跳跃 → 要删的 →
                 你改过没动的 → 新增/变更 → 计数 → 按钮。破坏性的一律排在按钮之前。 */}
          {plan && (
            <Panel style={{ '--rd': '.2s' } as React.CSSProperties}>
              <ConHead
                icon="file"
                title="计划清单"
                sub={<span className="badge wl">升级到 {plan.target.versionNumber}</span>}
              />
              <div style={{ padding: '14px 16px' }}>
                <PlanView
                  plan={plan}
                  stale={stale}
                  busy={busy}
                  opRunning={opRunning}
                  install={planSource === 'pack'}
                  onStart={(cs) => void start(cs)}
                  onReset={resetPlan}
                />
              </div>
              <ConFoot icon="ban">
                删除项在你点「开始升级」之前一个都不会动 —— 想反悔就关掉这个面板，什么都没发生
              </ConFoot>
            </Panel>
          )}

          {opId && (
            <Panel>
              <ConHead
                icon="file"
                title={planSource === 'pack' ? '正在安装' : '正在升级'}
                sub="切到别的页面回来还能看到进度"
              />
              <div style={{ padding: '14px 16px' }}>
                <ModpackOpView
                  op={op}
                  opErr={opErr}
                  busy={busy}
                  verb={planSource === 'pack' ? '安装' : '升级'}
                  onCancel={() => void cancel()}
                />
              </div>
            </Panel>
          )}
        </>
      )}
    </div>
  )

}

/** 包信息：照 srow 的「标签 → 值」版式，全部从后端给的字段来，不编任何值。 */
function PackMeta({ info }: { info: ModpackInfo }) {
  const rows: { k: string; v: ReactNode }[] = [
    { k: '当前版本', v: info.versionNumber ?? '—' },
    { k: 'Minecraft', v: info.minecraft ?? '—' },
    { k: '加载器', v: loaderVersLabel(info.loader) },
    { k: '模组数', v: info.fileCount != null ? `${info.fileCount} 个` : '—' },
    { k: '覆盖文件数', v: info.overrideCount != null ? `${info.overrideCount} 个` : '—' },
    { k: '安装时间', v: info.installedAt ? fmtTime(info.installedAt) : '—' },
    // updatedAt 空串 = 从没升过，要说清，别显示成 1970 / 一串 0
    { k: '上次升级', v: info.updatedAt ? fmtTime(info.updatedAt) : '从没升过' },
  ]
  return (
    <div className="px-1 py-1">
      {rows.map((r) => (
        <div key={r.k} className="srow">
          <span className="srow-k">{r.k}</span>
          <span className="srow-v mono-value">{r.v}</span>
        </div>
      ))}
    </div>
  )
}

/**
 * 只算不装的结果展示。
 *
 * 排布顺序就是「用户做决定」的顺序，从上到下重要性递减：
 *   1. 不能升（refusal）—— 直接拦死，连代价都不用看；
 *   2. 版本跳跃警告（levelJump）—— 放在按钮之前，因为它等于「换了一台服」，得先备份；
 *   3. 要删的文件（toRemove）—— 破坏性操作，必须让用户看见自己会失去什么，绝不能藏在按钮之后；
 *   4. 你改过、我们没动的（skipped）—— 这是「你的改动没被吃掉」的证据，也放在按钮之前；
 *   5. 计数摘要 —— 一眼看清规模；
 *   6. 实例在跑（running）—— 拦一下，先停服；
 *   然后才是「开始升级 / 开始安装」按钮（install 决定动词）。
 */
function PlanView({
  plan,
  stale,
  busy,
  opRunning,
  install = false,
  onStart,
  onReset,
}: {
  plan: ModpackUpgradePlan
  stale: boolean
  busy: string
  /** true = 这份计划对应的升级作业还在跑（或第一拍还没回来）。跑着就不准再起一笔。 */
  opRunning: boolean
  /** true = 这次是装新包（全新安装），文案说「安装」不说「升级」。 */
  install?: boolean
  onStart: (confirmStale: boolean) => void
  onReset: () => void
}) {
  const blocked = !!plan.refusal
  const canStart = !blocked && !plan.running && !opRunning
  const verb = install ? '安装' : '升级'

  return (
    <div className="mt-3.5 space-y-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[12.5px] text-ink-3">
          {install ? '将安装' : '将升级到'}{' '}
          <span className="mono-value text-ink-1">{plan.target.versionNumber}</span>
          （MC {plan.target.minecraft} · {loaderVersLabel(plan.target.loader)}）
        </span>
        <button
          type="button"
          className="btn btn-ghost sm ml-auto"
          disabled={busy === 'start'}
          onClick={onReset}
        >
          {install ? '换个包' : '换个版本'}
        </button>
      </div>

      {/* 计划变了（服务端 409）：和眼前这份不是同一份，下面的清单已是最新。强制用户看一遍再点。
          这一格不自动重试，否则用户批准的是 A、系统执行的是 B。 */}
      {stale && (
        <div className="nbox w">
          <div className="hd">
            <Icon name="ban" />
            这份计划被重新算过了
          </div>
          <p style={{ fontSize: 12.5, lineHeight: 1.7 }}>
              你刚才预览之后，这份计划被重新算过了（包可能更新了，或者实例里的文件被谁动了），和眼前这份不是同一份。下面的清单已经是服务端刚算出来的最新版本 —— 确认无误后点「仍然按新计划继续」。
          </p>
        </div>
      )}

      {blocked ? (
        // 不能升：只显示原因，禁用按钮。refusal 已经是后端给的完整说明（例如目标不再是服务端包）。
        <div className="nbox d">
          <div className="hd">
            <Icon name="ban" />
            {install ? '这个包不能这样装' : '这个版本不能这样升'}
          </div>
          <p style={{ fontSize: 12.5, lineHeight: 1.7 }}>{plan.refusal}</p>
          <p style={{ marginTop: 8, fontSize: 12, lineHeight: 1.7, color: 'var(--ink-3)' }}>
            {install ? '换个包再试，或去 Modrinth 上确认这个包是不是服务端包' : '换个版本再试，或去 Modrinth 上确认这个版本是不是服务端包'}
            （server_side 不是 unsupported）。
          </p>
        </div>
      ) : (
        <>
          {/* 版本跳跃警告：必须放在按钮之前。游戏 / 加载器大版本一跳，存档、世界生成、模组全要跟着变，
              等于换了一台服。框里的 levelJumpNote 已经写了「先做一次备份」，原样显示。 */}
          {plan.levelJump && (
            <div className="nbox w">
              <div className="hd">
                <Icon name="ban" />
                版本跳跃：目标 MC {plan.target.minecraft}
              </div>
              <p style={{ fontSize: 12.5, lineHeight: 1.7 }}>{plan.levelJumpNote}</p>
            </div>
          )}

          {/* 要删的文件：破坏性操作的核心，红色框 + 逐条原因。空数组也要给文案 ——
              那是「确认没有会动你的东西」的证据，不能静默不显示。 */}
          <RemoveBlock items={plan.toRemove} />

          {/* 你改过、我们没动的：证明「你的改动没被吃掉」。带 reason 逐条列。 */}
          <SkipBlock items={plan.skipped} />

          {/* 新增 / 变更：信息性，没有就不说。 */}
          <AddReplaceBlock add={plan.toAdd} replace={plan.toReplace} />

          {/* 计数摘要：规模一眼看清。downloadBytes 用 MB/GB 格式化，0 也如实显示成 0（不是 null）。 */}
          <SummaryRow plan={plan} />

          {/* 实例在跑：升级要先停服（Windows 锁文件），否则写不进去。拦一下，提示去概览页停。 */}
          {plan.running && (
            <div className="nbox w">
              <div className="hd">
                <Icon name="ban" />
                实例正在运行，{verb}要先停服
              </div>
              <p style={{ fontSize: 12.5, lineHeight: 1.7 }}>
                  实例正在运行。{verb}要先停服 —— Windows 上运行中的 jar / 文件是锁着的，覆盖不进去；
                  而且改完得重启才生效，跑着做等于做了也不起作用。请先到概览页把服务器停掉，再来点下面的按钮。
              </p>
            </div>
          )}

          {/* 提交按钮：放最后，前面该看的代价都看过了。running（实例在跑）时禁用（理由已在上面的框里说清）；
              opRunning（升级作业已在跑）也禁用 —— 双执行器写同一批文件必然损坏，后端 409 是最后一道闸，
              界面上就不该把按钮亮出来，下面的进度框就是「为什么点不了」的说明。
              stale（计划变了）= 文案写清「这是新计划」，让人没法一路回车点过去；
              levelJump（版本跳跃）= 文案写清「我已备份」，同样强制先清醒。这两档互不绑定。 */}
          <div className="flex flex-wrap items-center gap-2.5 pt-1">
            <button
              type="button"
              className="btn btn-primary"
              disabled={!canStart || busy === 'start'}
              onClick={() => onStart(stale)}
            >
              <Icon name="dl" size={12} />
              {busy === 'start'
                ? '准备中…'
                : opRunning
                  ? `${verb}进行中`
                  : stale
                    ? '仍然按新计划继续'
                    : plan.levelJump
                      ? `我已备份，开始${verb}`
                      : `开始${verb}`}
            </button>
            {canStart && (
              <span className="text-[12px] text-ink-3">
                会先下载再落文件，预计 {fmtSize(plan.downloadBytes)}。
              </span>
            )}
          </div>
        </>
      )}
    </div>
  )
}

/** 要删的文件：破坏性，红色框。逐条带 reason；空 = 明确说「没有要删的」。 */
function RemoveBlock({ items }: { items: ModpackFileChange[] }) {
  return (
    <div className="nbox d">
      <div className="hd">
        <Icon name="ban" />
        升级会删除这些文件（{items.length}）
      </div>
      {items.length === 0 ? (
        <p style={{ fontSize: 12, lineHeight: 1.7, color: 'var(--ink-3)' }}>
          没有要删除的文件 —— 你自己在 mods/ 里加的东西都不会被碰（只删整合包原本装进去的那些）。
        </p>
      ) : (
        items.map((f) => (
          <div key={f.path} className="li">
            <span className="mono">{f.path}</span>
            {f.reason && <span style={{ color: 'var(--ink-3)' }}> —— {f.reason}</span>}
          </div>
        ))
      )}
    </div>
  )
}

/** 你改过、我们没动的文件：逐条带 reason（为什么保留你的那份）。空 = 全部照计划应用。 */
function SkipBlock({ items }: { items: ModpackFileChange[] }) {
  return (
    <div className="nbox">
      <div className="hd" style={{ color: 'var(--ink-2)' }}>你改过、我们没动的文件（{items.length}）</div>
      {items.length === 0 ? (
        <p style={{ fontSize: 12, lineHeight: 1.7, color: 'var(--ink-3)' }}>
          没有跳过的文件 —— 所有变更都会照计划应用，不会保留任何「对不上的旧版本」。
        </p>
      ) : (
        items.map((f) => (
          <div key={f.path} className="li">
            <span className="mono">{f.path}</span>
            {f.reason && <span style={{ color: 'var(--ink-3)' }}> —— {f.reason}</span>}
          </div>
        ))
      )}
    </div>
  )
}

/** 新增 / 变更：信息性。两者都为空才给一句话，否则逐条列（add 绿、replace 黄）。 */
function AddReplaceBlock({
  add,
  replace,
}: {
  add: ModpackFileChange[]
  replace: ModpackFileChange[]
}) {
  if (add.length === 0 && replace.length === 0) {
    return (
      <p className="text-[12px] leading-relaxed text-ink-3">
        没有新增或变更的文件 —— 这一版对你实例里的文件没有要加、也没有要换的。
      </p>
    )
  }
  return (
    <div className="plan">
      {add.map((f) => (
        <Row key={'a' + f.path} tone="add" path={f.path} />
      ))}
      {replace.map((f) => (
        <Row key={'r' + f.path} tone="replace" path={f.path} />
      ))}
    </div>
  )
}

function Row({ tone, path }: { tone: 'add' | 'replace'; path: string }) {
  return (
    <div className="prow2">
      <span className={`k ${tone === 'add' ? 'add' : 'upd'}`}>{tone === 'add' ? '新增' : '变更'}</span>
      <span className="v" title={path}>
        {path}
      </span>
    </div>
  )
}

/** 计数摘要：新增 / 变更 / 删除 / 保留 / 跳过 + 预计下载量。 */
function SummaryRow({ plan }: { plan: ModpackUpgradePlan }) {
  const s = plan.summary
  const cells: { label: string; n: number; cls?: string }[] = [
    { label: '新增', n: s.add, cls: 'g' },
    { label: '变更', n: s.replace, cls: 'w' },
    { label: '删除', n: s.remove, cls: 'd' },
    { label: '保留', n: s.keep },
    { label: '跳过', n: s.skip },
  ]
  return (
    <div className="flex flex-wrap items-center gap-2">
      {cells.map((c) => (
        <span key={c.label} className={`chip s ${c.cls ?? ''}`}>
          {c.label} {c.n}
        </span>
      ))}
      <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
        预计下载 {fmtSize(plan.downloadBytes)}
      </span>
    </div>
  )
}

/**
 * 加载器**版本表** → 人话。{ "forge": "47.4.10" } → "Forge 47.4.10"。
 * ⚠ 与 projectMeta 的 `loaderLabel`（单个 id → 显示名，写死名单）分工不同：
 * 这个是「key + 版本号」的拼接。key 一律过一遍共享的 loaderLabel ——
 * 以前这里是「首字母大写」，`neoforge` 会印成「Neoforge」，而正确写法是「NeoForge」。
 */
function loaderVersLabel(l?: ModpackLoader): string {
  if (!l || Object.keys(l).length === 0) return '—'
  return Object.entries(l)
    .map(([k, v]) => `${loaderLabel(k)} ${v}`)
    .join(' · ')
}
