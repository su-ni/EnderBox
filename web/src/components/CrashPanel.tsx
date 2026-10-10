import { useCallback, useEffect, useState } from 'react'
import { api, type CrashReport } from '../api'
import { FIX_LABEL } from './PreflightPanel'
import { Icon } from './design'
import type { Instance } from '../types'

type Props = {
  instance: Instance
  /** 正在处理中的修复项 code（和 preflight 共用一份状态，避免两个按钮同时转圈） */
  fixing: string
  onFix: (code: string) => void
  onToast: (msg: string) => void
}

/**
 * 崩溃诊断。
 * 服务端异常退出后出现在首页，把最后 200 行日志判读成「现象 / 原因 / 怎么办」。
 * 只给结论不给证据的判读等于让人盲信，所以每条都带命中的原始行，
 * 底下还给一个「查看原始日志」的出口。
 */
export function CrashPanel({ instance, fixing, onFix, onToast }: Props) {
  const [reports, setReports] = useState<CrashReport[] | null>(null)
  const [idx, setIdx] = useState(0)
  const [raw, setRaw] = useState('')
  const [rawOpen, setRawOpen] = useState(false)
  const [rawBusy, setRawBusy] = useState(false)

  const reload = useCallback(async () => {
    try {
      const d = await api.crashReports(instance.id, 5)
      setReports(d.items ?? [])
    } catch {
      // 读不到就当作没有 —— 崩了已经够糟，不该再叠一层红字
      setReports([])
    }
  }, [instance.id])

  useEffect(() => {
    setReports(null)
    setIdx(0)
    reload()
  }, [reload])

  // 切到别的历史崩溃时收起上一条的原文，否则会出现「标题换了、日志还是旧的」
  useEffect(() => {
    setRawOpen(false)
    setRaw('')
  }, [idx])

  if (!reports || reports.length === 0) return null
  const rep = reports[Math.min(idx, reports.length - 1)]

  const toggleRaw = async () => {
    if (rawOpen) {
      setRawOpen(false)
      return
    }
    setRawBusy(true)
    try {
      const r = await api.crashReportText(instance.id, rep.file)
      setRaw(r.text)
      setRawOpen(true)
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setRawBusy(false)
    }
  }

  const fatal = rep.diagnoses.filter((d) => d.severity === 'fatal').length

  return (
    <section className="nbox d" style={{ marginTop: 16 }}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <p className="flex items-center gap-2 text-[13.5px] font-bold text-[#ffc2c2]">
          <Icon name="ban" size={13} />
          崩溃诊断
        </p>
        <span className="mono-meta">{rep.time}</span>
        {fatal > 0 && <span className="badge ban">{fatal} 项直接原因</span>}
        <button type="button" className="btn btn-ghost sm ml-auto" onClick={reload}>
          <Icon name="refresh" size={12} />
          刷新
        </button>
      </div>

      <p className="mt-1.5 max-w-[80ch] text-[12.5px] leading-[1.7] text-ink-2">
        服务端异常退出。下面这些结论是从最后 {rep.lineCount} 行日志里判读出来的，每条都对应一个能做的动作。
        {rep.exitInfo ? <span className="mono-meta ml-1">（退出信息：{rep.exitInfo}）</span> : null}
      </p>

      {reports.length > 1 && (
        <div className="mt-2.5 flex flex-wrap items-center gap-2">
          <span className="eyebrow">历史崩溃</span>
          {reports.map((r, i) => (
            <button
              key={r.file}
              type="button"
              className={`nav-pill${i === idx ? ' on' : ''}`}
              onClick={() => setIdx(i)}
            >
              {i === 0 ? '最近一次' : r.time.split(' ').pop()}
            </button>
          ))}
        </div>
      )}

      <ul className="mt-3">
        {rep.diagnoses.map((d) => (
          <li key={d.code} className="border-t border-line py-3 first:border-t-0 first:pt-0">
            <div className="flex flex-wrap items-start gap-x-3 gap-y-1.5">
              <span className={`badge ${d.severity === 'fatal' ? 'ban' : 'w'} shrink-0`}>
                {d.severity === 'fatal' ? '直接原因' : '可能有关系'}
              </span>
              <p className="min-w-0 flex-1 text-[13px] font-semibold text-ink-1">{d.title}</p>
              {d.fix && (
                <button
                  type="button"
                  className="btn btn-primary sm shrink-0"
                  disabled={Boolean(fixing)}
                  onClick={() => onFix(d.fix as string)}
                >
                  {fixing === d.fix ? '处理中…' : (FIX_LABEL[d.fix] ?? '一键修复')}
                </button>
              )}
            </div>
            <dl className="mt-1.5 max-w-[80ch] space-y-1 text-[12px] leading-[1.7]">
              <div className="flex gap-2">
                <dt className="shrink-0 text-ink-3">原因</dt>
                <dd className="text-ink-2">{d.cause}</dd>
              </div>
              <div className="flex gap-2">
                <dt className="shrink-0 text-ink-3">怎么办</dt>
                <dd className="text-ink-1">{d.action}</dd>
              </div>
            </dl>
            {d.evidence && (
              <p className="mono-meta mt-1.5 break-all border-l-2 border-line pl-2 text-ink-3">
                {d.evidence}
              </p>
            )}
          </li>
        ))}
      </ul>

      <div className="border-t border-line pt-2.5">
        <button type="button" className="btn btn-ghost sm" onClick={toggleRaw} disabled={rawBusy}>
          <Icon name="file" size={12} />
          {rawBusy ? '读取中…' : rawOpen ? '收起原始日志' : `查看原始日志（最后 ${rep.lineCount} 行）`}
        </button>
        {rawOpen && (
          <pre className="mono-meta mt-2 max-h-[320px] overflow-auto whitespace-pre-wrap break-all bg-panel-2 p-3 text-ink-2">
            {raw}
          </pre>
        )}
      </div>
    </section>
  )
}