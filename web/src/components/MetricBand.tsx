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

function Readout({
  icon,
  label,
  value,
  unit,
  pct,
  tone,
  note,
}: {
  icon: ReactNode
  label: string
  value: string
  unit?: string
  pct: number
  tone?: Tone
  note: string
}) {
  return (
    <div className="min-w-0">
      <div className="flex items-center gap-2 eyebrow">
        <span className="text-ink-3">{icon}</span>
        {label}
      </div>
      <div className="mt-2 mb-2.5 flex items-baseline gap-1.5">
        <b className="font-mono text-[25px] leading-none font-bold tabular-nums text-ink-1">{value}</b>
        {unit && <i className="font-mono text-[11.5px] not-italic text-ink-3">{unit}</i>}
      </div>
      <Segments pct={pct} tone={tone} />
      <p className="mt-2 mono-meta">{note}</p>
    </div>
  )
}

type Props = {
  metrics: Metrics | null
  peakCpu: number
  peakMem: number
}

/** 指标带：不是四个卡片，是一条读数带 —— 分段刻度条读数，细分隔线分组 */
export function MetricBand({ metrics, peakCpu, peakMem }: Props) {
  const running = Boolean(metrics?.running)
  const memPct = metrics && metrics.memMax > 0 ? (metrics.memUsed / metrics.memMax) * 100 : 0
  const playerCap = Math.max(20, metrics?.players ?? 0)

  return (
    <section className="stagger px-card mb-5 grid grid-cols-2 gap-x-5 gap-y-6 px-[18px] py-4 lg:grid-cols-4">
      <Readout
        icon={<IconCpu size={12} />}
        label="CPU 占用"
        value={running ? metrics!.cpu.toFixed(0) : '—'}
        unit="%"
        pct={running ? metrics!.cpu : 0}
        note={running ? `峰值 ${peakCpu.toFixed(0)}%` : '实例未运行'}
      />
      <Readout
        icon={<IconMemory size={12} />}
        label="内存"
        value={running ? formatMB(metrics!.memUsed) : '—'}
        unit={running ? `/ ${formatMB(metrics!.memMax)}` : undefined}
        pct={running ? memPct : 0}
        note={running ? `峰值 ${formatMB(peakMem)}` : ''}
      />
      <Readout
        icon={<IconUsers size={12} />}
        label="在线玩家"
        value={running ? String(metrics!.players) : '—'}
        unit={running ? `/ ${playerCap}` : '人'}
        pct={running ? (metrics!.players / playerCap) * 100 : 0}
        note={running ? '依据日志实时统计' : ''}
      />
      <Readout
        icon={<IconClock size={12} />}
        label="运行时长"
        value={running ? formatUptime(metrics!.uptimeSec) : '—'}
        pct={running ? 100 : 0}
        tone="on"
        note={running && metrics!.pid ? `PID ${metrics!.pid}` : ''}
      />
    </section>
  )
}
