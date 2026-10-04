import { useCallback, useEffect, useState } from 'react'
import { api, type BackupItem } from '../api'
import { IconSave, IconTrash, IconWarn } from './Icons'
import { DangerZone, Empty, Field, ListRow, NotReady, SectionHead, fmtSize, fmtTime } from './ui'
import type { Instance } from '../types'

const PREVIEW_RESTORE = new URLSearchParams(location.search).has('restore')

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
    }
  }, [instance.id])

  useEffect(() => {
    setItems([])
    setPick(null)
    reload()
  }, [reload])

  const create = async () => {
    setBusy('create')
    try {
      const b = await api.backupCreate(instance.id, label.trim())
      setLabel('')
      onToast(`备份完成：${b.file}（${fmtSize(b.size)}）`)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const remove = async (b: BackupItem) => {
    if (!window.confirm(`删除这份备份？\n\n${b.file}\n删了就找不回来了。`)) return
    try {
      await api.backupDelete(instance.id, b.file)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const restore = async () => {
    if (!pick) return
    setBusy('restore')
    try {
      const r = await api.backupRestore(instance.id, pick.file)
      const tail = r.safety ? `还原前的现场也存了一份：${r.safety}` : ''
      onToast(`已还原到 ${pick.file}。${tail}`)
      setPick(null)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const total = items.reduce((s, b) => s + b.size, 0)

  return (
    <div className="min-w-0">
      <h1 className="text-[22px] leading-tight font-bold text-ink-1">备份与还原</h1>
      <p className="mt-2 max-w-[68ch] text-[13.5px] leading-relaxed text-ink-2">
        正在管 <b className="text-ink-1">{instance.name}</b> 的存档。
        服务器开着的时候备份，会先让服务端把地图刷盘再打包，不会拿到半个区块。
      </p>

      {err && (
        <p className="mt-4 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
          {err}
        </p>
      )}

      {/* ── 手动备份 ── */}
      <div className="mt-7">
        <SectionHead idx="01" title="现在就来一份" note="备注可留空" />
        <div className="flex flex-wrap items-end gap-3">
          <div className="w-full max-w-[320px]">
            <Field label="这一份叫什么">
              <input
                className="px-input w-full"
                value={label}
                maxLength={24}
                placeholder="例如：装完插件前"
                onChange={(e) => setLabel(e.target.value)}
              />
            </Field>
          </div>
          <button type="button" className="btn btn-grass" disabled={busy === 'create'} onClick={create}>
            <IconSave size={13} />
            {busy === 'create' ? '打包中…' : '立刻备份'}
          </button>
        </div>
      </div>

      {/* ── 历史 ── */}
      <div className="mt-8">
        <SectionHead
          idx="02"
          title="备份文件"
          note={items.length > 0 ? `${items.length} 份 · 共 ${fmtSize(total)}` : "还没有备份"}
          right={
            <button type="button" className="btn btn-ghost btn-sm" onClick={reload}>
              刷新
            </button>
          }
        />

        {items.length === 0 ? (
          <Empty>世界越大越舍不得丢。先手动点一次上面那颗按钮试试。</Empty>
        ) : (
          <div className="stagger px-card">
            {items.map((b) => (
              <ListRow
                key={b.file}
                icon={<IconSave size={16} />}
                title={b.file}
                tags={
                  <span className={`chip ${b.kind === 'pre-restore' ? 'chip-w' : ''}`}>
                    {b.kind === 'pre-restore' ? '还原前自动存' : '手动'}
                  </span>
                }
                meta={`${fmtSize(b.size)} · ${fmtTime(b.time)}`}
                actions={
                  <>
                    <button type="button" className="btn btn-sm" onClick={() => setPick(b)}>
                      还原
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      onClick={() => remove(b)}
                      aria-label={`删除备份 ${b.file}`}
                    >
                      <IconTrash size={12} />
                    </button>
                  </>
                }
              />
            ))}
          </div>
        )}
      </div>

      {/* ── 自动备份：只说清现状，不画一排用不了的控件 ── */}
      <div className="mt-8">
        <SectionHead idx="03" title="自动备份" note="还没接后端" />
        <NotReady>
          定时任务（每天几点、保留几份、压缩等级）要等调度器落地 —— 现在只能手动点一次备一份。
          这里刻意没有画一排禁用的输入框：摆着不能用的控件，比直接说「还没做」更让人困惑。
        </NotReady>
      </div>

      {/* ── 还原确认 ── */}
      {pick && (
        <div className="mt-5">
          <DangerZone title="还原会把当前世界换成备份里的样子">
            <p className="max-w-[68ch] text-[13px] leading-[1.7] text-[#e8cfcf]">
              目标：<b className="mono-value text-[#ffd9d9]">{pick.file}</b>
              <br />
              流程是：停服 → 先把现在这份状态自动存成「还原前」备份 → 清空世界目录 → 解压覆盖 →
              之前开着就重新拉起来。所以就算选错了也还有一份后悔药，但玩家会掉线一次。
            </p>
            <p className="mt-2.5 flex items-center gap-2 text-[12.5px] text-warn">
              <IconWarn size={12} />
              {instance.status === 'running' ? '服务器正在运行，还原会先停服。' : '服务器当前未运行。'}
            </p>
            <div className="mt-3.5 flex flex-wrap gap-2.5">
              <button type="button" className="btn btn-ghost" onClick={() => setPick(null)}>
                取消
              </button>
              <button
                type="button"
                className="btn btn-danger"
                disabled={busy === 'restore'}
                onClick={restore}
              >
                {busy === 'restore' ? '还原中…' : '停服并还原到这一份'}
              </button>
            </div>
          </DangerZone>
        </div>
      )}
    </div>
  )
}
