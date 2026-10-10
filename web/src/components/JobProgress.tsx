import { useEffect, useRef, useState } from 'react'
import { api, type JobSnapshot } from '../api'

/**
 * 统一的作业进度气泡。
 *
 * 所有「分钟级慢操作」——备份创建/还原、整合包导出、模组升级安装——在后端都只挂
 * `internal/jobs` 这一张表，前端也因此只需要这一份轮询 + 展示：
 * 起操作的地方拿到 jobId 交出去，剩下的等待画面由它负责。
 *
 * 轮询节奏刻意沿用老 moder unity（700ms 一拍）：作业本身是分钟级的，一拍够快；
 * 再密只会让本机面板自己变成热点。
 *
 * ⚠ onFinish 只在**离开 running 的那一拍**触发一次（用 ref 记账）——
 * 少了这道闸，父组件每次重渲染都会被回调一次，用户会看到同一个完成提示连弹好几遍。
 */
const POLL_MS = 700

function fmtBytes(n: number): string {
  if (!n) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  const i = Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)))
  const v = n / Math.pow(1024, i)
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`
}

type Props = {
  jobId: string
  /** 这块气泡要说清自己在等什么：「正在备份」「正在还原」…… */
  label: string
  onFinish?: (j: JobSnapshot) => void
}

export function JobProgress({ jobId, label, onFinish }: Props) {
  const [job, setJob] = useState<JobSnapshot | null>(null)
  const [err, setErr] = useState('')
  const fired = useRef(false)
  const cb = useRef(onFinish)
  cb.current = onFinish

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout>
    fired.current = false
    setJob(null)
    setErr('')

    const tick = async () => {
      let j: JobSnapshot
      try {
        j = await api.jobs.get(jobId)
      } catch (e) {
        if (alive) setErr((e as Error).message)
        return
      }
      if (!alive) return
      setJob(j)
      if (j.state !== 'running') {
        if (!fired.current) {
          fired.current = true
          cb.current?.(j)
        }
        return
      }
      timer = setTimeout(tick, POLL_MS)
    }
    timer = setTimeout(tick, 0)
    return () => {
      alive = false
      clearTimeout(timer)
    }
  }, [jobId])

  const p = job?.progress
  // 没有分母（还没数完 / 总数未知）时老实显示「准备中」，别算成 NaN%
  const ready = p && p.filesTotal > 0
  const pct = ready ? Math.min(100, Math.round((p.filesDone / p.filesTotal) * 100)) : null

  return (
    <div className="mt-2 border-t border-line pt-2 text-[13px]">
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-txt-dim">
          {err ? '状态拿不到' : `${label}…`}
          {job?.state === 'canceled' ? '（已取消）' : ''}
        </span>
        <span className="mono-value text-txt-dim">
          {ready ? `${pct}%` : err ? '' : '准备中'}
        </span>
      </div>

      <div
        className="mt-1.5 h-1 w-full overflow-hidden bg-bg-soft"
        role="progressbar"
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct ?? 0}
      >
        <div
          className="h-full bg-accent transition-[width] duration-200"
          style={{ width: pct !== null ? `${pct}%` : '35%' }}
        />
      </div>

      {p?.current ? (
        <div className="mono-value mt-1.5 truncate text-txt-dim" title={p.current}>
          {p.current}
        </div>
      ) : null}

      {p && p.bytesTotal > 0 ? (
        <div className="mono-value mt-1 text-txt-dim">
          {fmtBytes(p.bytesDone)} / {fmtBytes(p.bytesTotal)}
        </div>
      ) : null}

      {job?.err ? <div className="mt-1.5 text-danger">{job.err}</div> : null}
      {err ? <div className="mt-1.5 text-danger">{err}</div> : null}
    </div>
  )
}
