import type { ReactNode } from 'react'
import { IconInfo } from './Icons'

/**
 * 区块标题。整页只有它能用 3px 黑线 —— 黑线是「新的一块开始了」的信号，
 * 用多了就变成噪音（面板外框、按钮、chip 各有各的黑边，页面就糊了）。
 */
export function SectionHead({
  idx,
  title,
  note,
  right,
}: {
  idx: string
  title: string
  note?: string
  right?: ReactNode
}) {
  return (
    <div className="mb-3 flex flex-wrap items-baseline gap-x-3 gap-y-2 border-b-[3px] border-black pb-2.5">
      <span className="eyebrow">{idx}</span>
      <h2 className="text-[16px] leading-tight font-bold text-ink-1">{title}</h2>
      {note && <span className="text-[12px] text-ink-3">{note}</span>}
      {right && <span className="ml-auto flex items-center gap-2">{right}</span>}
    </div>
  )
}

/**
 * 列表行：列表类页面（插件、备份…）统一用它。
 * 版式固定成「图标 · 标题+标签 / 元信息 · 右侧动作」，
 * 页面就不用各自拼字号字号组合 —— 那正是「每页看起来都不一样」的来源。
 */
export function ListRow({
  icon,
  title,
  tags,
  meta,
  actions,
}: {
  icon?: ReactNode
  title: string
  tags?: ReactNode
  meta?: ReactNode
  actions?: ReactNode
}) {
  return (
    <div className="row-b flex flex-wrap items-center gap-x-3.5 gap-y-1.5 px-4 py-3">
      {icon && <span className="shrink-0">{icon}</span>}
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
          <span className="truncate text-[14px] leading-tight font-semibold text-ink-1">{title}</span>
          {tags}
        </div>
        {meta && <div className="mono-meta mt-0.5 truncate" title={typeof meta === 'string' ? meta : undefined}>{meta}</div>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  )
}

/** 表单字段：标签 + 控件 + 说明。标签不再带 key 名 —— key 名交给控件的 title，界面上不必占一行。 */
export function Field({
  label,
  hint,
  children,
}: {
  label: string
  hint?: string
  children: ReactNode
}) {
  return (
    <label className="block min-w-0">
      <span className="field-label">{label}</span>
      {children}
      {hint && <span className="field-hint block">{hint}</span>}
    </label>
  )
}

/** 开关。span 上带 role=switch，键盘可聚焦 */
export function Toggle({
  on,
  onChange,
  label,
  disabled,
}: {
  on: boolean
  onChange: (v: boolean) => void
  label: string
  disabled?: boolean
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!on)}
      className={`tg-sw${on ? ' on' : ''}`}
      style={disabled ? { opacity: 0.4, cursor: 'default' } : undefined}
    />
  )
}

/** 「开关 + 标题 + 说明」的一行。字号层级靠字重和颜色拉开，不额外加图标。 */
export function ToggleRow({
  on,
  onChange,
  title,
  desc,
  disabled,
}: {
  on: boolean
  onChange: (v: boolean) => void
  title: string
  desc: string
  disabled?: boolean
}) {
  return (
    <div className="flex items-start gap-3 py-2.5">
      <Toggle on={on} onChange={onChange} label={title} disabled={disabled} />
      <div className="min-w-0">
        <div className="text-[13.5px] leading-tight font-semibold text-ink-1">{title}</div>
        <p className="mt-1 text-[12px] leading-relaxed text-ink-3">{desc}</p>
      </div>
    </div>
  )
}

/** 「这块还没接后端」的统一提示条。不假装功能存在，也不留一片死灰的控件。 */
export function NotReady({ children }: { children: ReactNode }) {
  return (
    <div className="flex items-start gap-2.5 border-l-[3px] border-warn bg-[#1a1812] px-4 py-3">
      <span className="mt-[3px] shrink-0 text-warn">
        <IconInfo size={13} />
      </span>
      <p className="min-w-0 text-[12.5px] leading-[1.7] text-[#e7dcb2]">{children}</p>
    </div>
  )
}

/** 危险区：还原、删除这类不可逆操作都放这里，左侧红条 + 深红底 */
export function DangerZone({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="border-l-[3px] border-danger bg-[#1c1314] px-4 py-3.5">
      <p className="text-[13.5px] font-bold text-[#ffc2c2]">{title}</p>
      <div className="mt-2.5">{children}</div>
    </div>
  )
}

/** 空态：列表没数据时用它，别画一堆占位卡片 */
export function Empty({ children }: { children: ReactNode }) {
  return <p className="px-1 py-5 text-[13px] leading-relaxed text-ink-2">{children}</p>
}

export function fmtSize(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
}

export function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}
