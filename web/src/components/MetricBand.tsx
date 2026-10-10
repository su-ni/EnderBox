import type { ReactNode } from 'react'
import { IconClock, IconCpu, IconMemory, IconUsers } from './Icons'
import { formatMB, formatUptime, type Metrics } from '../types'

type Tone = 'on' | 'w' | 'd'

function Segments({ pct, count = 10, tone }: { pct: number; count?: number; tone?: Tone }) {
  const filled = Math.max(0, Math.min(count, Math.round((pct / 100) * count)))
  // 默认按水位自动变色；像「运行时长」这种越满越好的指标由调用方强制指定颜色，
  // 否则 100% 会被判成危险色，一条全红的时长条纯属误导。
  const auto: Tone = pct >= 90 ? 'd' : pct >= 70 ? 'w' : 'on'
  const t = tone ?? auto
  return (
    <div className="seg" role="img" aria-label={`占用 ${Math.round(pct)}%`}>
      {Array.from({ length: count }, (_, i) => (
        <i key={i} className={i < filled ? t : undefined} />
      ))}
    </div>
  )
}

/**
 * 火花线：近 N 次采样的走势。
 * ⚠ 少于 2 个点就不画 —— 只有起点的一条线是噪声，还会让人以为有数据。
 * ⚠ 用 polyline 不是 path：像素风要的是折线，贝塞尔平滑会把它画成曲线，风格立刻散。
 */
function Spark({ data, tone }: { data?: number[]; tone?: string }) {
  if (!data || data.length < 2) return null
  const max = Math.max(...data, 1)
  const pts = data
    .map(
      (v, i) =>
        `${((i / (data.length - 1)) * 120).toFixed(1)},${(29 - (v / max) * 26).toFixed(1)}`,
    )
    .join(' ')
  return (
    <svg
      className={`spark${tone ? ' ' + tone : ''}`}
      viewBox="0 0 120 30"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <polyline points={pts} />
    </svg>
  )
}

function Readout({
  icon,
  label,
  value,
  unit,
  pct,
  tone,
  tag,
  note,
  hist,
  histTone,
}: {
  icon: ReactNode
  label: string
  value: string
  unit?: string
  pct: number
  tone?: Tone
  tag?: string
  note: string
  hist?: number[]
  histTone?: string
}) {
  const hasHist = Boolean(hist && hist.length > 1)
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-2 eyebrow">
        <span className="text-ink-3">{icon}</span>
        {label}
        {tag && <span className="ml-auto font-mono text-[11.5px] text-ink-3">{tag}</span>}
      </div>
      <div className="mt-2 mb-2.5 flex items-baseline gap-1.5">
        <b className="font-mono text-[25px] leading-none font-bold tabular-nums text-ink-1">{value}</b>
        {unit && <i className="font-mono text-[11.5px] not-italic text-ink-3">{unit}</i>}
      </div>
      <Segments pct={pct} tone={tone} />
      <Spark data={hist} tone={histTone} />
      {hasHist && (
        <div className="sparkcap">
          <span>近 {hist!.length} 采样</span>
        </div>
      )}
      <p className="mt-2 mono-meta">{note}</p>
    </div>
  )
}

type Props = {
  metrics: Metrics | null
  peakCpu: number
  peakMem: number
  /** CPU / 内存的采样历史（后端每 3s 一点，最多 60）。没有就不画火花线。 */
  histCpu?: number[]
  histMem?: number[]
}

/** 指标带：不是四个卡片，是一条读数带 —— 分段刻度条读数，细分隔线分组 */
export function MetricBand({ metrics, peakCpu, peakMem, histCpu, histMem }: Props) {
  const running = Boolean(metrics?.running)
  const memPct = metrics && metrics.memMax > 0 ? (metrics.memUsed / metrics.memMax) * 100 : 0
  const playerCap = Math.max(20, metrics?.players ?? 0)

  return (
    <section className="stagger px-card mb-5 grid grid-cols-2 gap-x-5 gap-y-6 px-[18px] py-4 lg:grid-cols-4">
      <Readout
        icon={<IconCpu size={12} />}
        label="CPU"
        value={running ? metrics!.cpu.toFixed(0) : '—'}
        unit="%"
        pct={running ? metrics!.cpu : 0}
        tag={running ? `峰值 ${peakCpu.toFixed(0)}%` : undefined}
        hist={running ? histCpu : undefined}
        histTone="cy"
        note={running && metrics!.pid ? `进程 ${metrics!.pid}` : '实例未运行'}
      />
      <Readout
        icon={<IconMemory size={12} />}
        label="MEMORY"
        value={running ? formatMB(metrics!.memUsed) : '—'}
        unit={running ? `/ ${formatMB(metrics!.memMax)}` : undefined}
        pct={running ? memPct : 0}
        tag={running ? `峰值 ${formatMB(peakMem)}` : undefined}
        hist={running ? histMem : undefined}
        histTone="g"
        note={running ? '依据日志实时统计' : ''}
      />
      <Readout
        icon={<IconUsers size={12} />}
        label="PLAYERS"
        value={running ? String(metrics!.players) : '—'}
        unit={running ? `/ ${playerCap}` : '人'}
        pct={running ? (metrics!.players / playerCap) * 100 : 0}
        tag={running ? `上限 ${playerCap}` : undefined}
        note={running ? '依据日志实时统计' : ''}
      />
      <Readout
        icon={<IconClock size={12} />}
        label="UPTIME"
        value={running ? formatUptime(metrics!.uptimeSec) : '—'}
        pct={running ? 100 : 0}
        tone="on"
        note={running && metrics!.pid ? `PID ${metrics!.pid}` : ''}
      />
    </section>
  )
}
