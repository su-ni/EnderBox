import { useEffect, useRef, useState } from 'react'
import { api, type DownloadTask } from '../api'
import { fmtSize } from './ui'

/**
 * 下载进度条。自己按 taskId 轮询。
 *
 * 用轮询不用 SSE：下载是「点一下盯着看几秒」的场景，轮询断了重连天然没问题；
 * SSE 断了还得处理重连和事件补偿，对一个几十秒的任务来说不划算。
 */
export function DownloadBar({
  taskId,
  onDone,
  onSettled,
}: {
  taskId: string
  onDone?: (t: DownloadTask) => void
  onSettled?: (t: DownloadTask) => void
}) {
  const [task, setTask] = useState<DownloadTask | null>(null)
  const [gone, setGone] = useState(false)
  const doneRef = useRef(false)

  useEffect(() => {
    if (!taskId) return
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined

    const tick = async () => {
      try {
        const t = await api.fetchTask(taskId)
        if (!alive) return
        setTask(t)
        if (t.state !== 'running') {
          onSettled?.(t)
          if (t.state === 'done' && !doneRef.current) {
            doneRef.current = true
            onDone?.(t)
          }
          return // 结束了就不再轮
        }
      } catch {
        if (alive) setGone(true)
        return
      }
      timer = setTimeout(tick, 600)
    }
    tick()

    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId])

  if (gone || (!task && !gone)) {
    if (gone) {
      return <p className="mono-meta">任务已经不在了（可能已完成超过半小时被清理）。回概览页看一眼服务端文件在不在。</p>
    }
    return <p className="text-[12.5px] text-ink-3">正在起下载任务…</p>
  }
  if (!task) return null

  const failed = task.state === 'failed'
  const done = task.state === 'done'
  // 上游有时不给 Content-Length，这时进度条按「已下多少」显示，不假装知道百分比
  const pct = task.total > 0 ? Math.min(100, task.percent) : 0
  const unknownTotal = task.total <= 0

  return (
    <div
      className="border-l-[3px] px-4 py-3"
      style={{
        borderColor: failed ? 'var(--color-danger)' : done ? 'var(--color-accent-deep)' : 'var(--color-cyan)',
        background: failed ? '#1c1314' : done ? '#141a13' : '#131b1a',
      }}
    >
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="text-[13px] font-semibold text-ink-1">
          {failed ? '下载失败' : done ? '服务端文件已就绪' : task.step}
        </span>
        {task.resumed && !done && <span className="chip">续传中</span>}
        {done && task.sha && (
          <span className={`chip ${task.verified ? 'chip-g' : 'chip-w'}`}>
            {task.verified ? '校验通过' : '未校验'}
          </span>
        )}
        {!done && !failed && task.rate > 0 && <span className="mono-meta">{fmtSize(task.rate)}/s</span>}
        <span className="mono-meta ml-auto">
          {unknownTotal
            ? `已下载 ${fmtSize(task.done)}`
            : `${fmtSize(task.done)} / ${fmtSize(task.total)}`}
        </span>
      </div>

      {!failed && (
        <div className="pbar mt-2.5">
          <i
            style={{
              width: unknownTotal ? '100%' : `${pct}%`,
              background: done ? 'var(--color-accent)' : undefined,
            }}
          />
        </div>
      )}

      <p className="mt-2 text-[12px] leading-relaxed text-ink-2">
        {failed
          ? task.err
          : done
            ? `文件已写入实例目录${task.sha ? `，SHA 与源站一致` : ''}。记得到概览页点启动。`
            : '下载在后台进行，可以切到别的页面，回来还能看到进度。'}
      </p>
    </div>
  )
}
