import type { Preflight } from '../api'
import { formatUptime, type Metrics } from '../types'

/**
 * 底部健康条（HUD）—— 结构照设计稿 `.hud`。
 *
 * ⚠ 心形图标走 web/index.html 精灵里的 `#i-heart`，**不要**换成自己画的图标组件 ——
 *    设计稿那颗心是 sprite 里手绘的 16×16 方块心，差一点就不像。
 * ⚠ 每个区段的类名（.hp / .hearts / .xpzone / .lvbox / .f3）都照设计稿，样式在 design.css。
 *
 * 数据取自后端真实字段：
 *   心条 + N/M  ← 启动体检四项（服务端文件、Java 版本、端口、EULA）
 *   经验条      ← CPU 占用（后端没有 TPS，标签就老实写「活跃度 · CPU」）
 *   .f3        ← 世界 / 端口 / 运行时长 / LIVE
 */
const CHECKS = 4

export function HudBar({
  metrics,
  report,
  port,
  name,
  status,
}: {
  metrics: Metrics | null
  report: Preflight | null
  port?: number
  name?: string
  status?: string
}) {
  const running = Boolean(metrics?.running)

  const checks = report
    ? [
        report.jarFound,
        report.javaMajor >= report.needJava,
        !report.issues.some((i) => i.code === 'port_mismatch'),
        !report.issues.some((i) => i.code === 'accept_eula'),
      ]
    : []
  const total = checks.length || CHECKS
  const lit = checks.length ? checks.filter(Boolean).length : CHECKS

  const cpu = running && metrics ? metrics.cpu : 0
  const live = running && status === 'running'

  return (
    <div className="hud tex-dirt">
      <div className="hp">
        <span className="lb">健康</span>
        <div className="hearts">
          {Array.from({ length: total }, (_, i) => (
            <svg
              key={i}
              className="heart"
              viewBox="0 0 16 16"
              style={{
                color: i < lit ? 'var(--bad)' : '#2a333d',
                animationDelay: `${(0.35 + i * 0.07).toFixed(2)}s`,
              }}
              aria-hidden="true"
            >
              <use href="#i-heart" />
            </svg>
          ))}
        </div>
        <span style={{ fontFamily: 'var(--f-term)', fontSize: 15, color: 'var(--ink-2)' }}>
          {lit}/{total}
        </span>
      </div>

      <div className="xpzone">
        <div className="lvbox">
          <span className="lv">{running ? Math.round(cpu) : 20}</span>
        </div>
        <div className="xpbar">
          <div className="fill" style={{ width: `${running ? cpu : 0}%` }} />
        </div>
        <span
          style={{
            fontFamily: 'var(--f-pix)',
            fontSize: 11,
            letterSpacing: '.08em',
            color: 'var(--ink-3)',
          }}
        >
          活跃度 · CPU
        </span>
      </div>

      <div className="f3">
        <span>
          世界 <b>{name || '—'}</b>
        </span>
        <span>
          端口 <b>{port ?? '—'}</b>
        </span>
        <span>
          运行 <b>{running && metrics ? formatUptime(metrics.uptimeSec) : '—'}</b>
        </span>
        <span className={live ? 'blink' : ''}>{live ? '● LIVE' : '● 已停止'}</span>
      </div>
    </div>
  )
}
