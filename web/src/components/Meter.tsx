/**
 * 方块刻度条 —— 照设计稿 app.js 的 buildMeters 原样实现：
 * 固定 10 格，按 pct 算亮几格，亮的加 `f` + tone 类。
 *
 * tone：`cy` 青（CPU）· `on` 绿（内存/玩家）· `xp` 经验绿（运行时长）· `w` 黄 · `d` 红
 * 样式在 design.css 的 `.meter` / `.meter i.f.*` 里，这里不写任何内联样式。
 */
export function Meter({
  pct,
  tone = 'on',
  className = 'meter',
}: {
  pct: number
  tone?: string
  className?: string
}) {
  const filled = Math.round((Math.max(0, Math.min(100, pct)) / 100) * 10)
  return (
    <div className={className} data-built="1">
      {Array.from({ length: 10 }, (_, i) => (
        <i key={i} className={i < filled ? `f ${tone}` : undefined} />
      ))}
    </div>
  )
}
