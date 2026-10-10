import { useEffect, useRef, useState } from 'react'
import { api, type DownloadTask } from '../api'
import { Icon } from './design'
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
  onToast,
}: {
  taskId: string
  onDone?: (t: DownloadTask) => void
  onSettled?: (t: DownloadTask) => void
  /** 有就用来反馈取消的结果；没有也照样能取消 */
  onToast?: (msg: string) => void
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
  /** 取消。已经下完的任务后端会拒绝，这时把原因说出来而不是假装成功 */
  const cancel = async () => {
    setTask((cur) => (cur ? { ...cur, step: '正在取消' } : cur))
    try {
      await api.cancelTask(taskId)
      const t = await api.fetchTask(taskId)
      setTask(t)
      onSettled?.(t)
      onToast?.('已取消下载')
    } catch (e) {
      setTask((cur) => cur)
      onToast?.((e as Error).message)
    }
  }

  if (!task) return null

  const failed = task.state === 'failed'
  const done = task.state === 'done'
  // 安装阶段（目前只有 Forge 会走到）：没有「下了多少字节」这回事，
  // 进度条只能表示「在动」。继续画字节数会让人以为卡死在一个数字上。
  const installing = task.phase === 'install'
  // 上游有时不给 Content-Length，这时进度条按「已下多少」显示，不假装知道百分比
  const pct = task.total > 0 ? Math.min(100, task.percent) : 0
  const unknownTotal = task.total <= 0 || installing

  return (
    <div className={`nbox ${failed ? 'd' : done ? 'ok' : 'live'}`}>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="text-[13px] font-semibold text-ink-1">
          {failed ? (installing ? '安装失败' : '下载失败') : done ? '服务端文件已就绪' : task.step}
        </span>
        {task.resumed && !done && <span className="chip">续传中</span>}
        {!done && !failed && !installing && (task.threads ?? 0) > 1 && (
          <span className="chip">{task.threads} 线程</span>
        )}
        {done && task.sha && (
          <span className={`chip ${task.verified ? 'chip-g' : 'chip-w'}`}>
            {task.verified ? '校验通过' : '未校验'}
          </span>
        )}
        {!done && !failed && !installing && task.rate > 0 && (
          <span className="mono-meta">{fmtSize(task.rate)}/s</span>
        )}
        {!installing && (
          <span className="mono-meta ml-auto">
            {unknownTotal
              ? `已下载 ${fmtSize(task.done)}`
              : `${fmtSize(task.done)} / ${fmtSize(task.total)}`}
          </span>
        )}
        {!done && !failed && (
          <button
            type="button"
            className="btn btn-ghost sm shrink-0"
            onClick={cancel}
            title="取消这次下载"
          >
            <Icon name="x" size={12} />
            取消
          </button>
        )}
      </div>

      {!failed && (
        <div className={`pbar mt-2.5${unknownTotal ? ' unknown' : ''}`}>
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
            ? task.phase === 'install'
              ? `装好了${task.sha ? '，安装器的校验和与源站一致' : ''}，服务端文件已经自动指向该启动的那个文件（可能是 jar，也可能是启动参数文件）。记得到概览页点启动。`
              : `文件已写入实例目录${task.sha ? `，SHA 与源站一致` : ''}。记得到概览页点启动。`
            : installing
              ? '正在安装：这一步要下库文件、还要给服务端打补丁，所以比下载本身慢。安装器的输出会写进这个世界的日志（[forge] 开头那些行）。'
              : '下载在后台进行，可以切到别的页面，回来还能看到进度。'}
      </p>
    </div>
  )
}
