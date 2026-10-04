import type { Preflight } from '../api'
import { IconCheck, IconRefresh, IconWarn } from './Icons'

/**
 * 启动前体检报告。
 * 出现在两个时机：用户点「启动」被挡住时、以及实例处于停止态时主动检查出问题。
 * 每条问题都尽量给一颗「修复」按钮 —— 能一键修的事不该让用户去翻文件。
 */
export function PreflightPanel({
  report,
  fixing,
  onFix,
  onRecheck,
}: {
  report: Preflight
  fixing: string
  onFix: (code: string) => void
  onRecheck: () => void
}) {
  if (report.issues.length === 0) {
    return (
      <div className="mt-4 flex items-center gap-2.5 border-l-[3px] border-accent-deep bg-[#141a13] px-3.5 py-2.5">
        <span className="text-accent">
          <IconCheck size={12} />
        </span>
        <p className="text-[12.5px] text-ink-1">
          启动前检查都过了，可以开服。
          <span className="ml-2 mono-meta">
            服务端 {report.jarPath.split(/[\\/]/).pop()} · Java {report.javaMajor} · 端口 {report.port}
          </span>
        </p>
      </div>
    )
  }

  const blockers = report.issues.filter((i) => i.level === 'block')
  const warns = report.issues.length - blockers.length
  const hard = blockers.length > 0

  return (
    <div
      className={`mt-4 border-l-[3px] px-4 py-3.5 ${hard ? 'border-danger bg-[#1a1213]' : 'border-warn bg-[#1a1812]'}`}
    >
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
        <p className={`flex items-center gap-2 text-[13.5px] font-bold ${hard ? 'text-[#ffc2c2]' : 'text-[#e7dcb2]'}`}>
          {hard ? <IconWarn size={13} /> : null}
          {hard ? `启动被 ${blockers.length} 项问题挡住` : `可以启动，但有 ${warns} 条提醒`}
        </p>
        <button
          type="button"
          className="btn btn-ghost btn-sm ml-auto"
          onClick={onRecheck}
          disabled={Boolean(fixing)}
        >
          <IconRefresh size={12} />
          重新检查
        </button>
      </div>

      <ul className="mt-2.5 space-y-2.5">
        {report.issues.map((i) => (
          <li key={i.code} className="flex flex-wrap items-start gap-x-3 gap-y-1.5">
            <span className={`chip ${i.level === 'block' ? 'chip-d' : 'chip-w'} shrink-0`}>
              {i.level === 'block' ? '必须解决' : '提醒'}
            </span>
            <div className="min-w-0 flex-1">
              <p className="text-[13px] font-semibold text-ink-1">{i.title}</p>
              <p className="mt-0.5 max-w-[80ch] text-[12px] leading-[1.7] text-ink-2">{i.detail}</p>
            </div>
            {i.fix && (
              <button
                type="button"
                className="btn btn-grass btn-sm ml-auto shrink-0"
                disabled={Boolean(fixing)}
                onClick={() => onFix(i.fix as string)}
              >
                {fixing === i.fix ? '处理中…' : '一键修复'}
              </button>
            )}
          </li>
        ))}
      </ul>
    </div>
  )
}
