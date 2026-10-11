import { useCallback, useEffect, useState } from 'react'
import { api, type BackupItem, type JobSnapshot } from '../api'
import { Chipf, ConFoot, ConHead, Icon, Panel, SecHead, Tog } from './design'
import { JobProgress } from './JobProgress'
import { Empty, SkeletonList, fmtSize, fmtTime, useConfirm } from './ui'
import { StepInput } from './StepInput'
import { SavesPanel } from './SavesPanel'
import type { BackupPolicy, BackupPolicyView, Instance } from '../types'

const PREVIEW_RESTORE = new URLSearchParams(location.search).has('restore')

/** 种类的中文名。auto 是后端按文件名前缀认出来的。 */
/** 图标底色：三类各自的情绪 —— 日常的自动、你亲手点的、以及危险动作留下的那份。 */
const KIND_COLOR: Record<string, string> = {
  manual: 'var(--ok)',
  auto: 'var(--cyan)',
  'pre-restore': 'var(--warn)',
}

const KIND_LABEL: Record<string, string> = {
  manual: '手动',
  auto: '自动',
  'pre-restore': '还原前自动存',
}

/**
 * 「上次自动备份」的显示。
 * 后端把「从没跑过」序列化成 0001-01-01（Go 的零值 time.Time），
 * 直接丢给 fmtTime 会显示成 1 年 1 月 —— 那不是一次真实备份，不能那么写。
 */
function lastRunText(iso?: string | null): string | null {
  if (!iso) return null
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return null
  return fmtTime(iso)
}

type Props = {
  instance: Instance
  onToast: (msg: string) => void
}

export function BackupPanel({ instance, onToast }: Props) {
  const [items, setItems] = useState<BackupItem[]>([])
  const [label, setLabel] = useState('')
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [pick, setPick] = useState<BackupItem | null>(null)
  /* 首屏标记。没有它的话，数据回来之前界面会先写「还没有备份」—— 那是句假话，
     而且恰好撒在用户最在意的地方（存档）。 */
  const [loading, setLoading] = useState(true)

  // ── 自动备份策略 ──
  const [pol, setPol] = useState<BackupPolicyView | null>(null)
  const [polErr, setPolErr] = useState('')
  const [savingPol, setSavingPol] = useState(false)
  const [atInput, setAtInput] = useState('')

  const { dialog, confirm } = useConfirm()

  const loadPolicy = useCallback(async () => {
    try {
      const v = await api.backupPolicy(instance.id)
      // atTimes 兜底成 []：后端现在会保证返回数组，但**已经落盘的旧实例**里存的是
      // `null`（Go nil slice 的序列化形态），老数据不会因为后端改了而自愈。
      // 不兜这一下，就是一个 `null.length` 把整页干掉。
      setPol({ ...v, atTimes: v.atTimes ?? [] })
      setPolErr('')
    } catch (e) {
      setPolErr((e as Error).message)
    }
  }, [instance.id])

  const reload = useCallback(async () => {
    try {
      const d = await api.backups(instance.id)
      const list = d.items ?? []
      setItems(list)
      setErr('')
      // 还原确认区是整页风险最高的交互，headless 点不了按钮，
      // 留个 ?restore=1 直接把面板顶出来，方便截图核对
      if (PREVIEW_RESTORE) setPick((prev) => prev ?? list[0] ?? null)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    setItems([])
    setPick(null)
    setLoading(true)
    setPol(null)
    reload()
    loadPolicy()
  }, [reload, loadPolicy])

  // 压包是分钟级操作：后端受理后立刻回 jobId，「成没成」要问作业状态，
  // 不能拿这次请求的返回当判据 —— 202 只代表它被受理了，不代表包打好了。
  const [jobId, setJobId] = useState<string | null>(null)
  const [jobKind, setJobKind] = useState<'create' | 'restore' | null>(null)

  const onJobFinish = (j: JobSnapshot) => {
    const was = jobKind
    setBusy('')
    setJobId(null)
    setJobKind(null)
    if (j.state === 'done') {
      if (was === 'create') {
        const res = j.result as { file?: string; size?: number }
        setLabel('')
        onToast(`备份完成：${res.file ?? ''}（${fmtSize(res.size ?? 0)}）`)
      } else {
        const res = j.result as { safety?: string }
        const tail = res.safety ? `还原前的现场也存了一份：${res.safety}` : ''
        onToast(`已还原到 ${pick?.file ?? ''}。${tail}`)
        setPick(null)
      }
      reload()
    } else if (j.state === 'canceled') {
      onToast(was === 'create' ? '备份已取消' : '还原已取消')
      reload()
    } else {
      onToast(j.err || (was === 'create' ? '备份失败' : '还原失败'))
    }
  }

  const create = async () => {
    setBusy('create')
    setJobKind('create')
    try {
      const r = await api.backupCreate(instance.id, label.trim())
      setJobId(r.jobId)
    } catch (e) {
      setBusy('')
      setJobKind(null)
      onToast((e as Error).message)
    }
  }

  const removeNow = async (b: BackupItem) => {
    try {
      await api.backupDelete(instance.id, b.file)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const remove = (b: BackupItem) =>
    confirm({
      title: '删除这份备份',
      danger: true,
      confirmLabel: '删除备份',
      run: () => removeNow(b),
      body: (
        <>
          删掉的是磁盘上的这个文件：
          <span className="mono-value mt-1.5 block break-all">{b.file}</span>
          之后想还原到它代表的那个时间点就没机会了 —— 除非你在别处还留了一份。
        </>
      ),
    })

  const restore = async () => {
    if (!pick) return
    setBusy('restore')
    setJobKind('restore')
    try {
      const r = await api.backupRestore(instance.id, pick.file)
      setJobId(r.jobId)
    } catch (e) {
      setBusy('')
      setJobKind(null)
      onToast((e as Error).message)
    }
  }

  const total = items.reduce((s, b) => s + b.size, 0)

  // ── 策略编辑的辅助动作 ──

  const patchPol = (p: Partial<BackupPolicyView>) => setPol((cur) => (cur ? { ...cur, ...p } : cur))

  const addTime = () => {
    const t = atInput.trim()
    if (!t || !pol) return
    if (pol.atTimes.length >= 6) {
      setPolErr('每天的时刻最多 6 个')
      return
    }
    patchPol({ atTimes: [...pol.atTimes, t] })
    setAtInput('')
    setPolErr('')
  }

  const savePolicy = async () => {
    if (!pol) return
    setSavingPol(true)
    setPolErr('')
    try {
      // 只发策略字段：nextRun 是后端算的展示值，不该回传
      const body: BackupPolicy = {
        enabled: pol.enabled,
        everyHours: pol.everyHours,
        atTimes: pol.atTimes,
        keep: pol.keep,
        scope: pol.scope,
      }
      const saved = await api.saveBackupPolicy(instance.id, body)
      setPol(saved)
      onToast('自动备份设置已保存')
    } catch (e) {
      // 后端把「25:00 格式不对」这类话直接写进 error，原地显示比弹 toast 更好找
      setPolErr((e as Error).message)
    } finally {
      setSavingPol(false)
    }
  }

  const scopeName = pol?.scope === 'worlds' ? '只存存档' : '整个实例目录'

  return (
    <div className="min-w-0">
      <SecHead
        idx="08"
        title="备份与还原"
        sub={
          <>
            正在管「{instance.name}」。服务器开着的时候备份，会先让服务端把地图刷盘再打包，
            不会拿到半个区块。想把<b style={{ color: 'var(--ink-1)' }}>某一个</b>世界单独搬走
            （导出 / 导入 / 切换），看最下面的「世界存档」。
            {pol && (
              <>
                {' '}
                当前范围：<b style={{ color: 'var(--ink-1)' }}>{scopeName}</b>（在下面「自动备份」里改）
              </>
            )}
          </>
        }
        style={{ '--rd': '.08s' } as React.CSSProperties}
      />

      {err && (
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>读备份列表没成功</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn sm" onClick={reload}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      )}

      {/* 01 现在就来一份 */}
      <Panel style={{ '--rd': '.12s' } as React.CSSProperties}>
        <ConHead num="01" title="现在就来一份" sub="备注可留空" />
        {jobId ? (
          <div style={{ padding: '0 16px 12px' }}>
            <JobProgress
              jobId={jobId}
              label={jobKind === 'restore' ? '正在还原' : '正在备份'}
              onFinish={onJobFinish}
            />
          </div>
        ) : null}
        <div
          style={{ padding: '14px 16px 16px', display: 'flex', gap: 14, alignItems: 'flex-end', flexWrap: 'wrap' }}
        >
          <div style={{ width: 320, maxWidth: '100%' }}>
            <div style={{ fontSize: 12.5, fontWeight: 600 }}>这一份叫什么</div>
            <input
              className="inp"
              style={{ width: '100%', marginTop: 7 }}
              value={label}
              maxLength={24}
              placeholder="例如：装完插件前"
              onChange={(e) => setLabel(e.target.value)}
            />
          </div>
          <button type="button" className="btn btn-primary" disabled={busy === 'create'} onClick={create}>
            <Icon name="chest" />
            {busy === 'create' ? '打包中…' : '立刻备份'}
          </button>
        </div>
      </Panel>

      {/* 02 备份文件 */}
      <Panel style={{ '--rd': '.16s' } as React.CSSProperties}>
        <ConHead
          num="02"
          title="备份文件"
          sub={
            loading
              ? '正在读取…'
              : items.length > 0
                ? `${items.length} 份 · 共 ${fmtSize(total)}`
                : '还没有备份'
          }
        >
          <div className="spacer" />
          <button type="button" className="btn btn-ghost sm" onClick={reload}>
            <Icon name="refresh" />
            刷新
          </button>
        </ConHead>

        {loading ? (
          <div style={{ padding: 14 }}>
            <SkeletonList rows={3} />
          </div>
        ) : items.length === 0 ? (
          <div style={{ padding: 14 }}>
            <Empty>世界越大越舍不得丢。先手动点一次上面那颗按钮试试。</Empty>
          </div>
        ) : (
          <div className="rlist">
            {items.map((b) => (
              <div key={b.file} className="rrow">
                <div className="ricon">
                  <Icon name="chest" size={18} style={{ color: KIND_COLOR[b.kind] ?? 'var(--cyan)' }} />
                </div>
                <div className="rmain">
                  <div className="rtitle">
                    {b.file}
                    <span className={`chip s${b.kind === 'pre-restore' ? ' w' : ''}`}>
                      {KIND_LABEL[b.kind] ?? b.kind}
                    </span>
                  </div>
                  <div className="rsub">
                    {fmtSize(b.size)} · {fmtTime(b.time)}
                  </div>
                </div>
                <div className="ract">
                  <button type="button" className="btn btn-primary xs" onClick={() => setPick(b)}>
                    还原
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost xs"
                    onClick={() => remove(b)}
                    aria-label={`删除备份 ${b.file}`}
                  >
                    <Icon name="trash" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        <ConFoot>
          还原会先把「现在」另存成一份标注「还原前」的备份再动手 —— 选错了也还有后悔药。
        </ConFoot>
      </Panel>

      {/* 03 自动备份 */}
      <Panel style={{ '--rd': '.2s' } as React.CSSProperties}>
        <ConHead
          num="03"
          title="自动备份"
          sub={pol ? (pol.enabled ? '已开启' : '未开启') : '正在读取…'}
        />

        {!pol ? (
          polErr ? (
            <div className="banner-danger" style={{ margin: 14 }}>
              <Icon name="ban" style={{ color: 'var(--bad)' }} />
              <div className="t">
                <h3>读自动备份设置没成功</h3>
                <p>{polErr}</p>
              </div>
              <button type="button" className="btn sm" onClick={loadPolicy}>
                <Icon name="refresh" />
                重试
              </button>
            </div>
          ) : (
            <div style={{ padding: 14 }}>
              <SkeletonList rows={2} />
            </div>
          )
        ) : (
          <div style={{ padding: '14px 16px 18px' }}>
            <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16 }}>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontSize: 13, fontWeight: 600 }}>开启自动备份</div>
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 4 }}>
                  到点自动打包一份。服务器开着时会先让服务端刷盘再打包，不会拿到半个区块。
                </p>
              </div>
              <Tog
                on={pol.enabled}
                ariaLabel="开启自动备份"
                onChange={(v) => {
                  // 打开时如果保留份数还是「不限」，顺手给个推荐值 —— 用户要的就是
                  // 「别把磁盘吃满」，留 0 等于没设。数字就在旁边，不是偷偷改。
                  patchPol({ enabled: v, keep: v && pol.keep === 0 ? 7 : pol.keep })
                }}
              />
            </div>

            <div className="fgrid" style={{ marginTop: 18, borderTop: '3px solid #000', paddingTop: 18 }}>
              <div>
                <div style={{ fontSize: 12.5, fontWeight: 600 }}>每天的时刻</div>
                <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                  <input
                    className="inp"
                    style={{ flex: 1, minWidth: 0 }}
                    value={atInput}
                    placeholder="例如 04:00"
                    maxLength={5}
                    aria-label="添加一个每天的时刻"
                    onChange={(e) => setAtInput(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === 'Enter') {
                        e.preventDefault()
                        addTime()
                      }
                    }}
                  />
                  <button
                    type="button"
                    className="btn sm"
                    onClick={addTime}
                    disabled={pol.atTimes.length >= 6}
                  >
                    <Icon name="plus" />
                    添加
                  </button>
                </div>
                {pol.atTimes.length > 0 && (
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 10 }}>
                    {pol.atTimes.map((t) => (
                      <button
                        key={t}
                        type="button"
                        className="chip s g"
                        title="点一下删掉"
                        onClick={() => patchPol({ atTimes: pol.atTimes.filter((x) => x !== t) })}
                      >
                        {t} ×
                      </button>
                    ))}
                  </div>
                )}
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 8 }}>
                  留空 = 不用固定时刻。最多 6 个，点标签就能删。
                </p>
              </div>

              <div>
                <div style={{ fontSize: 12.5, fontWeight: 600 }}>每隔几小时</div>
                <div style={{ marginTop: 8 }}>
                  <StepInput
                    label="每隔几小时"
                    value={pol.everyHours}
                    min={0}
                    max={168}
                    onChange={(v) => patchPol({ everyHours: Number(v) || 0 })}
                  />
                </div>
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 8 }}>
                  0 = 不用间隔式。两种方式可以一起用，谁先到点按谁跑。
                </p>
              </div>

              <div>
                <div style={{ fontSize: 12.5, fontWeight: 600 }}>保留最近几份</div>
                <div style={{ marginTop: 8 }}>
                  <StepInput
                    label="保留最近几份"
                    value={pol.keep}
                    min={0}
                    max={1000}
                    onChange={(v) => patchPol({ keep: Number(v) || 0 })}
                  />
                </div>
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 8 }}>
                  0 = 不限。超出的自动删最旧的 —— <b style={{ color: 'var(--ink-2)' }}>手动备份也算在内</b>
                  ，重要的一份请额外留个副本。
                </p>
              </div>

              <div>
                <div style={{ fontSize: 12.5, fontWeight: 600 }}>备份范围</div>
                <div className="filters" style={{ marginTop: 8 }}>
                  <Chipf on={pol.scope === 'all'} onClick={() => patchPol({ scope: 'all' })}>
                    整个实例目录
                  </Chipf>
                  <Chipf on={pol.scope === 'worlds'} onClick={() => patchPol({ scope: 'worlds' })}>
                    只存存档
                  </Chipf>
                </div>
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 8 }}>
                  {pol.scope === 'worlds'
                    ? '只打包世界目录，体积小、备得快。还原也只覆盖世界目录，插件和配置保持原样。'
                    : '连插件、模组、配置一起打包，还原后整个环境回到同一时刻的一致状态。体积最大。'}
                  手动备份同样用这个范围。
                </p>
              </div>
            </div>

            <div className="pol-foot" style={{ marginTop: 18 }}>
              <div className="row">
                <span className="k">上次自动备份</span>
                <span className="v">{lastRunText(pol.lastRun) ?? '还没有过'}</span>
              </div>
              <div className="row">
                <span className="k">下次预计</span>
                <span className="v">
                  {pol.enabled ? (pol.nextRun ? fmtTime(pol.nextRun) : '—') : '（未开启）'}
                </span>
              </div>
            </div>

            {polErr && (
              <div className="banner-danger" style={{ marginTop: 12 }}>
                <Icon name="ban" style={{ color: 'var(--bad)' }} />
                <div className="t">
                  <h3>没能保存</h3>
                  <p>{polErr}</p>
                </div>
              </div>
            )}

            <div style={{ display: 'flex', gap: 12, alignItems: 'center', flexWrap: 'wrap', marginTop: 16 }}>
              <button type="button" className="btn btn-primary" disabled={savingPol} onClick={savePolicy}>
                <Icon name="check" />
                {savingPol ? '保存中…' : '保存自动备份设置'}
              </button>
              <span style={{ fontSize: 12, color: 'var(--ink-3)' }}>
                改了要保存才生效。刚打开的策略不会立刻补跑一次。
              </span>
            </div>
          </div>
        )}
      </Panel>

      {/* 04 世界存档：单存档的搬运（导出 / 导入 / 切换当前世界）。
          与上面三块放一起，是因为它们管的是同一件事的另一半 ——
          备份是「回到过去某个时间点」，这里是「把某一个世界搬去别处」。 */}
      <SavesPanel
        instance={instance}
        running={instance.status === 'running'}
        onToast={onToast}
        num="04"
      />

      {/* 还原确认：点列表里的「还原」才出现，是整页风险最高的交互 */}
      {pick && (
        <div className="danger-zone reveal" style={{ '--rd': '.24s' } as React.CSSProperties}>
          <h3>还原会把当前世界换成备份里的样子</h3>
          <p>
            目标：<span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: '#ffd9d9' }}>{pick.file}</span>
            <br />
            流程是：停服 → 先把现在这份状态自动存成「还原前」备份 → 清空世界目录 → 解压覆盖 →
            之前开着就重新拉起来。所以就算选错了也还有一份后悔药，但玩家会掉线一次。
          </p>
          <p style={{ display: 'flex', alignItems: 'center', gap: 8, color: 'var(--warn)' }}>
            <Icon name="ban" />
            {instance.status === 'running' ? '服务器正在运行，还原会先停服。' : '服务器当前未运行。'}
          </p>
          <div style={{ display: 'flex', gap: 10, marginTop: 14, flexWrap: 'wrap' }}>
            <button type="button" className="btn btn-ghost" onClick={() => setPick(null)}>
              取消
            </button>
            <button type="button" className="btn btn-danger" disabled={busy === 'restore'} onClick={restore}>
              {busy === 'restore' ? '还原中…' : '停服并还原到这一份'}
            </button>
          </div>
        </div>
      )}
      {dialog}
    </div>
  )
}
