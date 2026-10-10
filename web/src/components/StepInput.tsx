import type { ReactNode } from 'react'

/**
 * 数值步进：`[-] 值 [+]`，像素风。
 *
 * 为什么不用 `<input type="number">`：它的 spinner 是浏览器画的一对白色小箭头，
 * 在深色主题里像贴上去的，和整套像素风完全不是一路东西。自己画一对按钮还能顺手把
 * 可点区域做大（系统箭头只有 13px 宽，很难点）。
 */
export function StepInput({
  value,
  onChange,
  step = 1,
  min,
  max,
  label,
  suffix,
  right,
}: {
  value: number | string
  onChange: (v: string) => void
  step?: number
  min?: number
  max?: number
  label: string
  suffix?: string
  right?: ReactNode
}) {
  const num = typeof value === 'number' ? value : Number(value)
  const valid = Number.isFinite(num)

  const bump = (dir: number, big: boolean) => {
    const d = step * dir * (big ? 10 : 1)
    let next = (valid ? num : 0) + d
    if (min !== undefined) next = Math.max(min, next)
    if (max !== undefined) next = Math.min(max, next)
    onChange(String(next))
  }

  return (
    <span className="stepper">
      <button
        type="button"
        aria-label={`${label} 减少`}
        // 按住 Shift 一次走 10 格：端口那种五位数靠一格一格点太累
        onClick={(e) => bump(-1, e.shiftKey)}
      >
        −
      </button>
      <input
        className="inp"
        type="text"
        inputMode="numeric"
        aria-label={label}
        value={String(value)}
        style={{ width: 92 }}
        onChange={(e) => onChange(e.target.value.replace(/[^\d-]/g, ''))}
      />
      <button
        type="button"
        aria-label={`${label} 增加`}
        onClick={(e) => bump(1, e.shiftKey)}
      >
        +
      </button>
      {suffix && (
        <span style={{ alignSelf: 'center', marginLeft: 8, fontSize: 12.5, color: 'var(--ink-3)' }}>{suffix}</span>
      )}
      {right}
    </span>
  )
}
