/**
 * 全部图标为手写像素画：16×16 网格上用 rect 拼，靠 shape-rendering:crispEdges（在 index.css 全局设）保证边缘不糊。
 * 禁用 emoji 与第三方图标库 —— 像素风必须自己画，否则风格立刻崩。
 */
type IconProps = { size?: number; className?: string }

/** 方块图标：世界列表与 logo 共用。top = 顶层色，body = 底层色 */
export function IconBlock({
  size = 22,
  className,
  top = '#5d9c3a',
  body = '#3b2a1a',
  hi = '#7cc24f',
  lo = '#6f4520',
}: IconProps & { top?: string; body?: string; hi?: string; lo?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect width="16" height="16" fill={body} />
      <rect width="16" height="5" fill={top} />
      <rect x="1" y="2" width="2" height="2" fill={hi} />
      <rect x="6" y="1" width="2" height="2" fill={lo} />
      <rect x="2" y="7" width="3" height="2" fill={lo} />
      <rect x="8" y="9" width="4" height="2" fill={hi} />
      <rect x="10" y="12" width="3" height="2" fill={lo} />
    </svg>
  )
}

export function IconPlay({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="4" y="2" width="2" height="12" fill="currentColor" />
      <rect x="6" y="4" width="2" height="8" fill="currentColor" />
      <rect x="8" y="6" width="2" height="4" fill="currentColor" />
      <rect x="10" y="7" width="2" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconStop({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="3" width="10" height="10" fill="currentColor" />
    </svg>
  )
}

export function IconKill({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="7" y="2" width="2" height="7" fill="currentColor" />
      <rect x="4" y="9" width="8" height="2" fill="currentColor" />
      <rect x="5" y="11" width="6" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconRestart({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="2" width="8" height="2" fill="currentColor" />
      <rect x="11" y="4" width="2" height="6" fill="currentColor" />
      <rect x="9" y="10" width="4" height="2" fill="currentColor" />
      <rect x="3" y="12" width="8" height="2" fill="currentColor" />
      <rect x="1" y="4" width="2" height="8" fill="currentColor" />
      <rect x="5" y="6" width="6" height="4" fill="currentColor" opacity=".45" />
    </svg>
  )
}

export function IconGear({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="6" y="1" width="4" height="3" fill="currentColor" />
      <rect x="6" y="12" width="4" height="3" fill="currentColor" />
      <rect x="1" y="6" width="3" height="4" fill="currentColor" />
      <rect x="12" y="6" width="3" height="4" fill="currentColor" />
      <rect x="3" y="3" width="3" height="3" fill="currentColor" />
      <rect x="10" y="3" width="3" height="3" fill="currentColor" />
      <rect x="3" y="10" width="3" height="3" fill="currentColor" />
      <rect x="10" y="10" width="3" height="3" fill="currentColor" />
      <rect x="5" y="5" width="6" height="6" fill="currentColor" />
    </svg>
  )
}

export function IconPlus({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="6" y="2" width="4" height="12" fill="currentColor" />
      <rect x="2" y="6" width="12" height="4" fill="currentColor" />
    </svg>
  )
}

export function IconTrash({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="3" width="12" height="2" fill="currentColor" />
      <rect x="6" y="1" width="4" height="2" fill="currentColor" />
      <rect x="4" y="5" width="8" height="9" fill="currentColor" />
    </svg>
  )
}

export function IconCpu({ size = 12, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="9" width="12" height="4" fill="currentColor" />
      <rect x="5" y="2" width="6" height="7" fill="currentColor" />
    </svg>
  )
}

export function IconMemory({ size = 12, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="3" width="10" height="10" fill="currentColor" />
      <rect x="5" y="5" width="6" height="6" fill="#000" opacity=".38" />
    </svg>
  )
}

export function IconUsers({ size = 12, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="2" width="10" height="8" fill="currentColor" />
      <rect x="5" y="4" width="2" height="2" fill="#000" opacity=".45" />
      <rect x="9" y="4" width="2" height="2" fill="#000" opacity=".45" />
      <rect x="3" y="11" width="10" height="3" fill="currentColor" />
    </svg>
  )
}

export function IconClock({ size = 12, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="2" width="12" height="12" fill="currentColor" />
      <rect x="7" y="4" width="2" height="5" fill="#000" opacity=".5" />
      <rect x="7" y="9" width="5" height="2" fill="#000" opacity=".5" />
    </svg>
  )
}

export function IconTerminal({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect width="16" height="16" fill="#070908" />
      <rect x="2" y="3" width="5" height="2" fill="#5d9c3a" />
      <rect x="2" y="7" width="9" height="2" fill="#4aedd9" />
      <rect x="2" y="11" width="3" height="2" fill="#5d9c3a" />
    </svg>
  )
}

export function IconSearch({ size = 15, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="2" width="8" height="2" fill="currentColor" />
      <rect x="2" y="2" width="2" height="8" fill="currentColor" />
      <rect x="10" y="4" width="2" height="6" fill="currentColor" />
      <rect x="4" y="10" width="6" height="2" fill="currentColor" />
      <rect x="11" y="11" width="3" height="3" fill="currentColor" />
    </svg>
  )
}

export function IconCheck({ size = 11, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="8" width="3" height="3" fill="currentColor" />
      <rect x="6" y="5" width="3" height="3" fill="currentColor" />
      <rect x="9" y="2" width="3" height="3" fill="currentColor" />
    </svg>
  )
}

export function IconBack({ size = 12, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="6" y="3" width="2" height="10" fill="currentColor" />
      <rect x="4" y="6" width="2" height="4" fill="currentColor" />
      <rect x="2" y="7" width="2" height="2" fill="currentColor" />
    </svg>
  )
}

/** 各服务端类型的方块色板：同一套像素画，换配色即区分类型 */
const TYPE_SKIN: Record<string, { top: string; body: string; hi: string; lo: string }> = {
  paper: { top: '#e6e8e3', body: '#8f978f', hi: '#ffffff', lo: '#6b736b' },
  vanilla: { top: '#5d9c3a', body: '#3b2a1a', hi: '#7cc24f', lo: '#6f4520' },
  fabric: { top: '#9fd8e8', body: '#3f7f92', hi: '#c6ecf7', lo: '#2d6274' },
  forge: { top: '#9c8b78', body: '#4a3f34', hi: '#b8a794', lo: '#332b23' },
  neoforge: { top: '#c8823c', body: '#8a5522', hi: '#e0a05c', lo: '#5f3a17' },
  velocity: { top: '#4fb6d8', body: '#1d6d8c', hi: '#7fd0ea', lo: '#124a61' },
  bungeecord: { top: '#8b5a2b', body: '#5e3a19', hi: '#a87540', lo: '#3f2710' },
}

export function IconTypeBlock({ type, size = 22 }: { type: string; size?: number }) {
  const s = TYPE_SKIN[type] ?? TYPE_SKIN.vanilla
  return <IconBlock size={size} {...s} />
}

/** 汉堡菜单：移动端的世界列表抽屉靠它打开 */
export function IconMenu({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="3" width="12" height="2" fill="currentColor" />
      <rect x="2" y="7" width="12" height="2" fill="currentColor" />
      <rect x="2" y="11" width="12" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconClose({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="3" width="2" height="2" fill="currentColor" />
      <rect x="5" y="5" width="2" height="2" fill="currentColor" />
      <rect x="7" y="7" width="2" height="2" fill="currentColor" />
      <rect x="9" y="9" width="2" height="2" fill="currentColor" />
      <rect x="11" y="11" width="2" height="2" fill="currentColor" />
      <rect x="11" y="3" width="2" height="2" fill="currentColor" />
      <rect x="9" y="5" width="2" height="2" fill="currentColor" />
      <rect x="5" y="9" width="2" height="2" fill="currentColor" />
      <rect x="3" y="11" width="2" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconDownload({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="2" width="10" height="2" fill="currentColor" />
      <rect x="7" y="4" width="2" height="4" fill="currentColor" />
      <rect x="3" y="8" width="10" height="2" fill="currentColor" />
      <rect x="5" y="10" width="6" height="2" fill="currentColor" />
      <rect x="7" y="12" width="2" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconUpload({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="7" y="2" width="2" height="2" fill="currentColor" />
      <rect x="5" y="4" width="6" height="2" fill="currentColor" />
      <rect x="3" y="6" width="10" height="2" fill="currentColor" />
      <rect x="7" y="8" width="2" height="4" fill="currentColor" />
      <rect x="3" y="13" width="10" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconRefresh({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="3" y="3" width="2" height="4" fill="currentColor" />
      <rect x="5" y="3" width="6" height="2" fill="currentColor" />
      <rect x="11" y="3" width="3" height="2" fill="currentColor" />
      <rect x="11" y="5" width="2" height="6" fill="currentColor" />
      <rect x="5" y="11" width="6" height="2" fill="currentColor" />
      <rect x="3" y="7" width="2" height="4" fill="currentColor" />
    </svg>
  )
}

/** Java 咖啡杯：设置页的 Java 运行时一栏用 */
export function IconJava({ size = 13, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="6" y="1" width="2" height="3" fill="currentColor" opacity="0.75" />
      <rect x="9" y="2" width="2" height="3" fill="currentColor" opacity="0.55" />
      <rect x="4" y="6" width="8" height="2" fill="currentColor" />
      <rect x="4" y="8" width="2" height="5" fill="currentColor" />
      <rect x="10" y="8" width="2" height="5" fill="currentColor" />
      <rect x="6" y="13" width="4" height="2" fill="currentColor" />
      <rect x="12" y="8" width="2" height="3" fill="currentColor" />
    </svg>
  )
}

/** 木箱：插件 / 模组的实体感 */
export function IconPackage({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="4" width="12" height="2" fill="currentColor" />
      <rect x="2" y="6" width="2" height="8" fill="currentColor" />
      <rect x="12" y="6" width="2" height="8" fill="currentColor" />
      <rect x="4" y="12" width="8" height="2" fill="currentColor" />
      <rect x="7" y="6" width="2" height="6" fill="currentColor" />
    </svg>
  )
}

/** 软盘：备份就是存档 */
export function IconSave({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="2" y="2" width="12" height="2" fill="currentColor" />
      <rect x="2" y="4" width="2" height="8" fill="currentColor" />
      <rect x="12" y="4" width="2" height="8" fill="currentColor" />
      <rect x="2" y="12" width="12" height="2" fill="currentColor" />
      <rect x="5" y="4" width="6" height="4" fill="currentColor" />
      <rect x="5" y="9" width="6" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconShield({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="7" y="2" width="2" height="2" fill="currentColor" />
      <rect x="4" y="4" width="8" height="2" fill="currentColor" />
      <rect x="3" y="6" width="10" height="5" fill="currentColor" />
      <rect x="4" y="11" width="8" height="2" fill="currentColor" />
      <rect x="6" y="13" width="4" height="2" fill="currentColor" />
    </svg>
  )
}

export function IconWarn({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="7" y="2" width="2" height="2" fill="currentColor" />
      <rect x="5" y="4" width="6" height="2" fill="currentColor" />
      <rect x="3" y="6" width="10" height="2" fill="currentColor" />
      <rect x="2" y="8" width="12" height="3" fill="currentColor" />
    </svg>
  )
}

export function IconInfo({ size = 14, className }: IconProps) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" className={className} aria-hidden="true">
      <rect x="7" y="2" width="2" height="3" fill="currentColor" />
      <rect x="7" y="7" width="2" height="7" fill="currentColor" />
    </svg>
  )
}
