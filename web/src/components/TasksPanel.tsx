import { useCallback, useEffect, useRef, useState } from 'react'
import {
  api,
  type ScheduledTask,
  type TaskAction,
  type TaskActionKind,
  type TaskPayload,
  type TaskTrigger,
  type TaskView,
} from '../api'
import type { Instance } from '../types'
import { ConFoot, ConHead, Icon, Panel, SecHead, Tog } from './design'
import {
  ConfirmDialog,
  Dialog,
  Empty,
  SkeletonList,
  ToggleRow,
  fmtAgo,
  fmtTime,
} from './ui'
import { StepInput } from './StepInput'

/** Go 的零值时间序列化成 0001-01-01T00:00:00Z。它不是「跑过」，别当真实时间显示。 */
const ZERO_TIME = '0001-01-01'

/* 对话框的深链入口：`?page=tasks&taskdialog=new` 打开新建框，
   `?page=tasks&taskdialog=<任务ID>` 直接打开那条任务的编辑框。
   用途有二：无头浏览器点不了按钮，只能从 URL 进（同 App.tsx 的 ?dialog=delete / ?drawer=1）；
   以及「把这条任务的配置发给我看看」时可以直接甩链接。 */
const PREVIEW_DIALOG = new URLSearchParams(location.search).get('taskdialog') ?? ''

const WEEK_LABEL = ['周日', '周一', '周二', '周三', '周四', '周五', '周六']

const ACTION_KINDS: TaskActionKind[] = [
  'command',
  'start',
  'stop',
  'restart',
  'kill',
  'backup',
  'delay',
]

const ACTION_LABEL: Record<TaskActionKind, string> = {
  command: '发指令',
  start: '启动',
  stop: '停止',
  restart: '重启',
  kill: '强制结束',
  backup: '备份',
  delay: '等待',
}

const TRIGGER_KINDS: { k: TaskTrigger['kind']; label: string }[] = [
  { k: 'interval', label: '按间隔' },
  { k: 'daily', label: '每天' },
  { k: 'weekly', label: '每周' },
  { k: 'cron', label: 'CRON' },
]

/** 触发条件说成人话。调度规则本身在后端（「下次执行」也是它算的），这里只是拼字符串。 */
function describeTrigger(tr: TaskTrigger): string {
  switch (tr.kind) {
    case 'interval': {
      const m = tr.everyMinutes ?? 0
      if (m >= 1440 && m % 1440 === 0) return `每 ${m / 1440} 天`
      if (m >= 60 && m % 60 === 0) return `每 ${m / 60} 小时`
      return `每 ${m} 分钟`
    }
    case 'daily':
      return `每天 ${tr.at ?? ''}`
    case 'weekly': {
      const ds = (tr.weekdays ?? []).map((d) => WEEK_LABEL[d] ?? '').filter(Boolean)
      return `${ds.length ? `每${ds.join('、')}` : '每周'} ${tr.at ?? ''}`
    }
    case 'cron':
      return `CRON「${tr.cron ?? ''}」`
  }
  return ''
}

function describeAction(a: TaskAction): string {
  const base = ACTION_LABEL[a.kind] ?? a.kind
  if (a.kind === 'command') return `${base}：${a.value ?? ''}`
  if (a.kind === 'delay') return `等待 ${a.value ?? 0} 秒`
  return base
}

function hasRun(iso?: string): boolean {
  return !!iso && !iso.startsWith(ZERO_TIME)
}

/**
 * 「下次执行」说成人话：今天 22:26 / 明天 04:00 / 10-15 04:00。
 *
 * 后端给的是完整 ISO（2026-10-06T04:00:00+08:00）—— 塞进列表行的标签里又长又难扫，
 * 而这一栏恰恰是用户扫列表时最想先看到的。截成「什么时候」就够了，精确到分。
 */
function fmtNext(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const hm = `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`
  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
  const now = new Date()
  if (sameDay(d, now)) return `今天 ${hm}`
  const tomorrow = new Date(now)
  tomorrow.setDate(now.getDate() + 1)
  if (sameDay(d, tomorrow)) return `明天 ${hm}`
  return `${d.getMonth() + 1}-${d.getDate()} ${hm}`
}

type Props = {
  instance: Pick<Instance, 'id' | 'name'>
  onToast: (msg: string) => void
}

export function TasksPanel({ instance, onToast }: Props) {
  const [items, setItems] = useState<TaskView[]>([])
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  // null = 没开；'new' = 新建；一条任务 = 编辑它
  const [editing, setEditing] = useState<TaskView | 'new' | null>(PREVIEW_DIALOG === 'new' ? 'new' : null)
  const [pendingDelete, setPendingDelete] = useState<TaskView | null>(null)
  // 只锁正在操作的那一行 —— 「一条忙全部禁用」会被读成「点了没反应」
  const [busyId, setBusyId] = useState('')

  const reload = useCallback(async () => {
    try {
      const d = await api.tasks(instance.id)
      setItems(d.items ?? [])
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    // 换实例时先回到「正在读」态，否则会拿上一个实例的任务列表闪一下
    setLoading(true)
    setItems([])
    reload()
  }, [reload])

  // 深链打开某条任务的编辑框（?taskdialog=<id>）。
  // 只在列表首次读回来之后触发一次 —— 之后用户自己关掉对话框，
  // 不该因为 URL 里还挂着参数就被弹回来。
  const deepLinkDone = useRef(false)
  useEffect(() => {
    if (deepLinkDone.current || loading) return
    if (PREVIEW_DIALOG && PREVIEW_DIALOG !== 'new') {
      const t = items.find((x) => x.id === PREVIEW_DIALOG)
      if (t) setEditing(t)
    }
    deepLinkDone.current = true
  }, [loading, items])

  const toggle = async (t: TaskView, enabled: boolean) => {
    setBusyId(t.id)
    try {
      await api.taskToggle(instance.id, t.id, enabled)
      await reload()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusyId('')
    }
  }

  const submit = async (payload: TaskPayload) => {
    const isNew = editing === 'new'
    const saved = isNew
      ? await api.taskCreate(instance.id, payload)
      : await api.taskUpdate(instance.id, (editing as TaskView).id, payload)
    setEditing(null)
    await reload()
    onToast(
      isNew
        ? saved.nextRun
          ? `任务「${saved.name}」建好了，下次执行：${fmtTime(saved.nextRun)}`
          : `任务「${saved.name}」建好了（当前是停用状态）`
        : `任务「${saved.name}」已保存`,
    )
  }

  const doDelete = async () => {
    if (!pendingDelete) return
    const name = pendingDelete.name
    try {
      await api.taskDelete(instance.id, pendingDelete.id)
      setPendingDelete(null)
      await reload()
      onToast(`任务「${name}」已删除`)
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const failed = items.filter((t) => !!t.lastError)

  return (
    <div className="min-w-0">
      <SecHead
        idx="09"
        title="计划任务"
        sub={
          <>
            任务是<b style={{ color: 'var(--ink-1)' }}>有顺序的动作流水线</b> —— 前一步做完才做下一步，
            最多 10 步。和自动备份共用同一个调度循环
          </>
        }
        style={{ '--rd': '.08s' } as React.CSSProperties}
        right={
          <button type="button" className="btn btn-primary sm" onClick={() => setEditing('new')}>
            <Icon name="plus" />
            新建任务
          </button>
        }
      />

      {err && (
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>读任务列表没成功</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn sm" onClick={reload}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      )}

      <Panel style={{ '--rd': '.12s' } as React.CSSProperties}>
        <ConHead icon="clock" title="计划任务" sub={items.length ? `${items.length} 条` : undefined} />

        <div style={{ padding: '12px 16px 0' }}>
          <p style={{ fontSize: 12.5, lineHeight: 1.7, color: 'var(--ink-3)', maxWidth: '70ch' }}>
            到点自动做事：定时备份、到点重启、整点广播。动作按顺序执行，
            <b style={{ color: 'var(--ink-2)' }}>前一步失败就停下来</b>
            ，不会继续往下做 —— 尤其是「重启」失败之后。
          </p>

          {/* 失败的任务必须单独拎出来说。只说「到点了」不说「没做成」，等于让用户以为一切正常。 */}
          {failed.length > 0 && (
            <div className="nbox d" style={{ marginTop: 12 }}>
              <div className="hd">
                <Icon name="ban" />
                有 {failed.length} 条任务上次没做成
              </div>
              {failed.map((t) => (
                <div key={t.id} className="li">
                  「{t.name}」{t.lastError}
                </div>
              ))}
            </div>
          )}
        </div>

        {loading ? (
          <div style={{ padding: 14 }}>
            <SkeletonList rows={3} icon={false} />
          </div>
        ) : items.length === 0 ? (
          <div style={{ padding: 14 }}>
            <Empty>
              还没有计划任务。想「每天凌晨四点自动备份」或者「每周一早上重启一次清理内存」，
              点右上角建一条，30 秒一轮的调度器会按时叫它。
            </Empty>
          </div>
        ) : (
          <div className="rlist" style={{ marginTop: 12 }}>
            {items.map((t) => {
              const last = hasRun(t.lastRun) ? fmtAgo(t.lastRun) : '还没跑过'
              return (
                <div key={t.id} className={`rrow${t.enabled ? '' : ' off'}`}>
                  <div className="ricon">
                    <Icon
                      name="clock"
                      size={18}
                      style={{
                        color: t.running
                          ? 'var(--ok)'
                          : t.lastError
                            ? 'var(--copper)'
                            : 'var(--cyan)',
                      }}
                    />
                  </div>
                  <div className="rmain">
                    <div className="rtitle">
                      {t.name}
                      {/* 「下次」提到标签行：它是扫列表时最想先看到的信息，
                          埋在下面那串 meta 里等于没有。 */}
                      {t.running ? (
                        <span className="chip s g">执行中</span>
                      ) : (
                        t.enabled &&
                        t.nextRun && <span className="chip s">下次 {fmtNext(t.nextRun)}</span>
                      )}
                      {!t.enabled && <span className="chip s">已停用</span>}
                      {t.lastError && <span className="chip s d">上次失败</span>}
                    </div>
                    <div className="rrow-sub">
                      <span>上次 {last}</span>
                    </div>
                  </div>
                  <div className="rmeta">
                    {describeTrigger(t.trigger)} → {t.actions.map(describeAction).join(' → ')}
                  </div>
                  <div className="ract">
                    <Tog
                      on={t.enabled}
                      onChange={(v) => toggle(t, v)}
                      disabled={busyId === t.id}
                      ariaLabel={`启用「${t.name}」`}
                    />
                    <button
                      type="button"
                      className="btn btn-ghost xs"
                      title={`编辑「${t.name}」`}
                      aria-label={`编辑「${t.name}」`}
                      onClick={() => setEditing(t)}
                    >
                      <Icon name="lever" />
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost xs"
                      title={`删除「${t.name}」`}
                      aria-label={`删除「${t.name}」`}
                      onClick={() => setPendingDelete(t)}
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
          任务不写进服务端配置，全由面板自己调度 —— 关掉面板就不再跑，别把它当游戏内的定时器用。
        </ConFoot>
      </Panel>

      {editing !== null && (
        <TaskDialog
          initial={editing === 'new' ? null : editing}
          onClose={() => setEditing(null)}
          onSubmit={submit}
        />
      )}

      {pendingDelete && (
        <ConfirmDialog
          title={`删除「${pendingDelete.name}」`}
          danger
          confirmLabel="删除任务"
          onCancel={() => setPendingDelete(null)}
          onConfirm={doDelete}
          body={
            <>
              这条任务会被移除，之后不再按时执行。已经跑过的那几次留下的备份、
              日志都不会动 —— 只删任务本身。
            </>
          }
        />
      )}
    </div>
  )
}

/** 新建 / 编辑对话框。 */
function TaskDialog({
  initial,
  onClose,
  onSubmit,
}: {
  initial: ScheduledTask | null
  onClose: () => void
  onSubmit: (p: TaskPayload) => Promise<void>
}) {
  const [name, setName] = useState(initial?.name ?? '')
  const [enabled, setEnabled] = useState(initial?.enabled ?? true)
  const [kind, setKind] = useState<TaskTrigger['kind']>(initial?.trigger.kind ?? 'daily')
  const [everyMinutes, setEveryMinutes] = useState(
    String(initial?.trigger.everyMinutes ?? 60),
  )
  const [at, setAt] = useState(initial?.trigger.at ?? '04:00')
  const [weekdays, setWeekdays] = useState<number[]>(initial?.trigger.weekdays ?? [1])
  const [cron, setCron] = useState(initial?.trigger.cron ?? '0 4 * * *')
  const [actions, setActions] = useState<TaskAction[]>(
    initial?.actions?.length ? initial.actions : [{ kind: 'backup' }],
  )
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  const isNew = initial === null

  /** 先在前端把「明显错的」挡住，省一次往返。真正的规则仍在后端 —— 这里只是提前一步。 */
  const validate = (): string => {
    if (!name.trim()) return '任务名不能为空'
    if (kind === 'interval') {
      const n = Number(everyMinutes)
      if (!Number.isFinite(n) || n < 1) return '间隔至少 1 分钟'
      if (n > 10080) return '间隔最多 7 天（10080 分钟）'
    }
    if (kind === 'daily' || kind === 'weekly') {
      if (!/^\d{1,2}:\d{2}$/.test(at.trim())) return '时刻要写成 04:00 这样'
    }
    if (kind === 'weekly' && weekdays.length === 0) return '「每周」至少要选一天'
    if (kind === 'cron' && cron.trim().split(/\s+/).length !== 5)
      return 'CRON 要 5 段：分 时 日 月 周'
    if (actions.length === 0) return '至少要有一个动作'
    for (let i = 0; i < actions.length; i++) {
      const a = actions[i]
      if (a.kind === 'command' && !(a.value ?? '').trim())
        return `第 ${i + 1} 个动作是「发指令」，但指令是空的`
      if (a.kind === 'delay') {
        const n = Number(a.value)
        if (!Number.isFinite(n) || n < 1 || n > 3600)
          return `第 ${i + 1} 个动作的等待时间要是 1-3600 秒`
      }
    }
    return ''
  }

  const save = async () => {
    const msg = validate()
    if (msg) {
      setErr(msg)
      return
    }
    const trigger: TaskTrigger =
      kind === 'interval'
        ? { kind, everyMinutes: Number(everyMinutes) }
        : kind === 'daily'
          ? { kind, at: at.trim() }
          : kind === 'weekly'
            ? { kind, at: at.trim(), weekdays }
            : { kind, cron: cron.trim() }

    setBusy(true)
    setErr('')
    try {
      await onSubmit({ name: name.trim(), enabled, trigger, actions })
    } catch (e) {
      // 后端把「时刻 "25:00" 格式不对」这类话直接写进 error，原地显示比弹 toast 更好找
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const patchAction = (i: number, patch: Partial<TaskAction>) => {
    setActions((as) => as.map((a, j) => (j === i ? { ...a, ...patch } : a)))
  }

  return (
    <Dialog
      title={isNew ? '新建计划任务' : '编辑计划任务'}
      width={620}
      onClose={onClose}
      actions={
        <>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn btn-primary" onClick={save} disabled={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
        </>
      }
    >
      <div className="space-y-4">
        <label className="block min-w-0">
          <span className="field-label">任务名</span>
          <input
            className="px-input w-full"
            value={name}
            placeholder="例如 每晚备份"
            onChange={(e) => setName(e.target.value)}
          />
          <span className="field-hint block">只给你自己看，会出现在控制台日志里。</span>
        </label>

        <div className="border-t border-line pt-1">
          <ToggleRow
            on={enabled}
            onChange={setEnabled}
            title="启用这条任务"
            desc="关掉之后不再按时执行，但配置留着。下次打开时会从打开那一刻重新起算，不补跑关着的时候错过的点。"
          />
        </div>

        <div className="border-t border-line pt-3">
          <span className="field-label">什么时候跑</span>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {TRIGGER_KINDS.map((x) => (
              <button
                key={x.k}
                type="button"
                className={`fchip${kind === x.k ? ' on' : ''}`}
                onClick={() => setKind(x.k)}
              >
                {x.label}
              </button>
            ))}
          </div>

          <div className="mt-3">
            {kind === 'interval' && (
              <div className="flex flex-wrap items-center gap-3">
                <StepInput
                  value={everyMinutes}
                  onChange={setEveryMinutes}
                  min={1}
                  max={10080}
                  step={10}
                  label="间隔分钟数"
                />
                <span className="text-[12.5px] text-ink-2">分钟一次</span>
                <span className="text-[12px] text-ink-3">
                  （60 = 每小时，1440 = 每天）
                </span>
              </div>
            )}

            {kind === 'daily' && (
              <div className="flex flex-wrap items-center gap-3">
                <span className="text-[12.5px] text-ink-2">每天</span>
                <input
                  className="px-input px-input-mono w-[92px]"
                  value={at}
                  placeholder="04:00"
                  onChange={(e) => setAt(e.target.value)}
                />
                <span className="text-[12px] text-ink-3">按这台机器的本地时间</span>
              </div>
            )}

            {kind === 'weekly' && (
              <div className="space-y-2.5">
                <div className="flex flex-wrap gap-1.5">
                  {WEEK_LABEL.map((lab, i) => (
                    <button
                      key={i}
                      type="button"
                      className={`fchip${weekdays.includes(i) ? ' on' : ''}`}
                      onClick={() =>
                        setWeekdays((ws) =>
                          ws.includes(i) ? ws.filter((x) => x !== i) : [...ws, i].sort((a, b) => a - b),
                        )
                      }
                    >
                      {lab}
                    </button>
                  ))}
                </div>
                <div className="flex flex-wrap items-center gap-3">
                  <span className="text-[12.5px] text-ink-2">时刻</span>
                  <input
                    className="px-input px-input-mono w-[92px]"
                    value={at}
                    placeholder="04:00"
                    onChange={(e) => setAt(e.target.value)}
                  />
                </div>
              </div>
            )}

            {kind === 'cron' && (
              <div className="space-y-2">
                <input
                  className="px-input px-input-mono w-full"
                  value={cron}
                  placeholder="0 4 * * *"
                  onChange={(e) => setCron(e.target.value)}
                />
                <p className="text-[12px] leading-relaxed text-ink-3">
                  五段式：分 时 日 月 周。支持 <span className="mono-value">*</span>、
                  <span className="mono-value">*/10</span>、
                  <span className="mono-value">1,15</span>、
                  <span className="mono-value">9-17</span>。
                  日和星期<span className="text-ink-2">同时写</span>时取「同时满足」
                  （标准 cron 取「任一满足」）—— 例如
                  <span className="mono-value"> 0 3 1 * 1 </span>
                  是「每月 1 号且那天正好是周一」。
                </p>
              </div>
            )}
          </div>
        </div>

        <div className="border-t border-line pt-3">
          {/* 动作区限宽。删除按钮要靠右对齐（这样每一行的按钮落在同一条竖线上），
              但若让行铺满整个对话框，「备份」这种没有参数的动作中间就会空出一大片。
              限到 520px，两个问题同时解决。 */}
          <div className="max-w-[520px]">
          <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="field-label mb-0">按顺序做这些事</span>
            <button
              type="button"
              className="btn btn-ghost sm ml-auto"
              disabled={actions.length >= 10}
              onClick={() => setActions((as) => [...as, { kind: 'command', value: '' }])}
            >
              <Icon name="plus" size={12} />
              添加动作
            </button>
          </div>

          <div className="mt-2 border-t border-line">
            {actions.map((a, i) => (
              <div
                key={i}
                className="flex flex-wrap items-center gap-2 border-b border-line py-2.5 last:border-b-0"
              >
                <span className="mono-meta w-5 shrink-0">{i + 1}</span>
                <select
                  className="px-input w-[104px] shrink-0"
                  value={a.kind}
                  aria-label={`第 ${i + 1} 个动作类型`}
                  onChange={(e) =>
                    patchAction(i, { kind: e.target.value as TaskActionKind, value: '' })
                  }
                >
                  {ACTION_KINDS.map((k) => (
                    <option key={k} value={k}>
                      {ACTION_LABEL[k]}
                    </option>
                  ))}
                </select>

                {a.kind === 'command' && (
                  <input
                    className="px-input min-w-0 flex-1"
                    value={a.value ?? ''}
                    placeholder="例如 say 服务器 5 分钟后重启"
                    aria-label={`第 ${i + 1} 个动作的指令`}
                    onChange={(e) => patchAction(i, { value: e.target.value })}
                  />
                )}

                {a.kind === 'delay' && (
                  <div className="flex items-center gap-2">
                    <StepInput
                      value={a.value || '5'}
                      onChange={(v) => patchAction(i, { value: v })}
                      min={1}
                      max={3600}
                      step={5}
                      label={`第 ${i + 1} 个动作的等待秒数`}
                    />
                    <span className="text-[12.5px] text-ink-3">秒</span>
                  </div>
                )}

                <button
                  type="button"
                  className="btn btn-ghost sm ml-auto"
                  aria-label={`删除第 ${i + 1} 个动作`}
                  disabled={actions.length <= 1}
                  onClick={() => setActions((as) => as.filter((_, j) => j !== i))}
                >
                  <Icon name="trash" size={12} />
                </button>
              </div>
            ))}
          </div>

          <p className="mt-3.5 text-[12px] leading-relaxed text-ink-3">
            常用组合：<span className="mono-value">发指令 say …</span> →
            <span className="mono-value"> 等待 300 秒</span> →
            <span className="mono-value"> 重启</span>
            ，就是「先广播再重启」。
          </p>

          {actions.some((a) => a.kind === 'kill') && (
            <div className="nbox d" style={{ marginTop: 12 }}>
              强制结束是直接杀进程，服务端来不及把地图写回磁盘 —— 存档有损坏的风险。
              能用「停止」就别用「强制结束」。
            </div>
          )}
          </div>
        </div>

        {err && <div className="nbox d">{err}</div>}
      </div>
    </Dialog>
  )
}
