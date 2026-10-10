/**
 * 设计稿共用零件。
 *
 * 全部和 `web/src/design.css`（= docs/design/pixel-refresh/styles.css）的类名一一对应 ——
 * 这里只负责「把设计稿的静态 DOM 变成能接真实数据的组件」，**不新增任何样式**。
 * 视觉上任何一处想改，改的是设计稿，不是这里。
 */
import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'
import { useStickyScroll } from '../useStickyScroll'

// ─────────────────────────────────────────── 基础

export const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v))

/** 精灵图标：`<use href="#i-xxx"/>`。形状与设计稿同一份素材（web/index.html 的 defs），必然一致。 */
export function Icon({
  name,
  size = 16,
  className = 'pic',
  style,
}: {
  name: string
  size?: number
  className?: string
  style?: CSSProperties
}) {
  return (
    <svg className={className} style={{ width: size, height: size, ...style }} aria-hidden="true">
      <use href={`#i-${name}`} />
    </svg>
  )
}

/** 面板：`.panel` + 像素四角 `<i class="corner"/>`。设计稿里一切容器都是它。 */
export function Panel({
  children,
  className = '',
  style,
  flush,
  ...rest
}: {
  children: ReactNode
  className?: string
  style?: CSSProperties
  /** flush = 去掉面板的下外边距（设计稿里「卡片列」的写法用得上） */
  flush?: boolean
} & React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      className={`panel${className ? ' ' + className : ''}`}
      style={flush ? { marginBottom: 0, ...style } : style}
      {...rest}
    >
      <i className="corner" />
      {children}
    </div>
  )
}

/** 眉标（Silkscreen 大写 + 字距）。只放拉丁，汉字会回退。 */
export function Eyebrow({ children, style }: { children: ReactNode; style?: CSSProperties }) {
  return (
    <div className="eyebrow" style={style}>
      {children}
    </div>
  )
}

/** 区块标题：`.sec-head`（左对齐，非 Hero 区一律不居中）。 */
export function SecHead({
  idx,
  title,
  sub,
  right,
  style,
  className = '',
}: {
  idx?: ReactNode
  title: ReactNode
  sub?: ReactNode
  right?: ReactNode
  style?: CSSProperties
  className?: string
}) {
  return (
    <div className={`sec-head reveal${className ? ' ' + className : ''}`} style={style}>
      {idx != null && <span className="idx">{idx}</span>}
      <h2>{title}</h2>
      {sub != null && <span className="sub">{sub}</span>}
      {right != null && (
        <>
          <div className="spacer" />
          {right}
        </>
      )}
    </div>
  )
}

/** 面板头：`.con-head`（编号 / 图标 + 标题 + 统计 + 搜索 + 筛选 + 动作）。 */
export function ConHead({
  icon,
  num,
  iconColor = 'var(--cyan)',
  title,
  sub,
  children,
  stone = true,
  style,
}: {
  icon?: string
  /** 面板编号（像素字体，如 "01"）。设计稿里「备份 / 任务」这类多面板页用它代替图标。 */
  num?: string
  iconColor?: string
  title: ReactNode
  sub?: ReactNode
  children?: ReactNode
  stone?: boolean
  style?: CSSProperties
}) {
  return (
    <div className={`con-head${stone ? ' tex-stone' : ''}`} style={style}>
      {num != null && (
        <span style={{ fontFamily: 'var(--f-pix)', fontSize: 11, letterSpacing: '.06em', color: 'var(--ink-3)' }}>
          {num}
        </span>
      )}
      {icon && <Icon name={icon} style={{ color: iconColor }} />}
      <h2>{title}</h2>
      {sub != null && <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>{sub}</span>}
      {children}
    </div>
  )
}

/** 面板脚注：一句「你该知道、但不必天天看」的话。 */
export function ConFoot({ children, icon = 'info' }: { children: ReactNode; icon?: string }) {
  return (
    <div className="con-foot">
      <Icon name={icon} style={{ color: 'var(--ink-3)' }} />
      {children}
    </div>
  )
}

/**
 * pane 内的区段头（设计稿 09 设置页）：像素字编号 + h3 + 说明 + 右侧动作。
 *
 * ⚠ 和 ConHead / SectionHead 是三个层级，别混用：
 *   SecHead（.sec-head）= 整页的大标题（「09 设置」）；一个页面只有一个；
 *   ConHead（.con-head）= 面板头（「Java 运行时 / 用哪个 Java 跑服务端」），面板级；
 *   PaneHead = **面板内容里**的小节头（「02 面板密码」「02·B 各世界需要哪个 Java」），可以有好几个。
 *   之前设置页把 SectionHead 塞进 pane 里当小节头用 —— 大标题字级 + 零内边距直接贴面板边，全乱。
 *
 * ⚠ 设计稿原文在编号和右侧动作之间放了一个 `<div class="spacer">`，但 .spacer 只在
 *   .topbar / .sec-head / .con-head 下有定义，**在这里是个哑弹** —— 对照设计稿真实渲染，
 *   右侧元素（chip / 按钮）是紧跟在说明文字后面的，不做「修补式」右对齐。
 */
export function PaneHead({
  num,
  title,
  note,
  children,
}: {
  num: string
  title: string
  note?: ReactNode
  children?: ReactNode
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
      <span
        style={{
          fontFamily: 'var(--f-pix)',
          fontSize: 11,
          letterSpacing: '.06em',
          color: 'var(--ink-3)',
        }}
      >
        {num}
      </span>
      <h3 style={{ fontSize: 14, fontWeight: 600 }}>{title}</h3>
      {note != null && <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>{note}</span>}
      {children}
    </div>
  )
}

// ─────────────────────────────────────────── 数据展示

/**
 * 方块刻度条：10 格，逐格点亮（46ms/格，从第 140ms 起）。
 * 复刻设计稿 app.js 的 buildMeters —— 只在挂载时点亮一次，之后跟随数据但不重复动画。
 */
export function Meter({
  pct,
  tone = 'on',
  row = false,
  style,
}: {
  pct: number
  tone?: 'on' | 'w' | 'd' | 'cy' | 'xp'
  row?: boolean
  style?: CSSProperties
}) {
  const filled = Math.round((clamp(pct, 0, 100) / 100) * 10)
  const [lit, setLit] = useState(0)
  const mounted = useRef(false)

  useEffect(() => {
    if (mounted.current) {
      setLit(filled)
      return
    }
    mounted.current = true
    const timers: number[] = []
    for (let i = 0; i < filled; i++) {
      timers.push(
        window.setTimeout(() => setLit((n) => Math.max(n, i + 1)), 140 + i * 46),
      )
    }
    return () => timers.forEach(clearTimeout)
  }, [filled])

  return (
    <div className={`meter${row ? ' row' : ''}`} style={style}>
      {Array.from({ length: 10 }, (_, i) => (
        <i key={i} className={i < lit ? `f ${tone}` : ''} />
      ))}
    </div>
  )
}

/** 火花线：折线 + 峰值虚线 + 峰值方块。复刻 app.js 的 drawSpark。
 *
 *  传 `data`（真实采样）时按它画；没传就退回种子伪随机 —— 版式与几何完全一样，
 *  区别只在于「画的是不是真事」。宁可画真数据：这张图是给人看趋势的，不是装饰。 */
export function Spark({
  seed,
  data,
  cls = '',
  style,
  height,
}: {
  seed: number
  /** 真实采样序列（近 N 次轮询）。长度 < 2 时忽略，退回 seed。 */
  data?: number[]
  cls?: '' | 'g' | 'o' | 'y'
  style?: CSSProperties
  height?: number
}) {
  const usable = !!data && data.filter((n) => Number.isFinite(n)).length >= 2

  const { d, area, peakY, peakX } = useMemo(() => {
    const n = 40
    const W = 120
    const H = 30
    let raw: number[]

    if (usable) {
      // 真实序列：按窗口重采样成 40 点（线性插值），保证几何与设计稿一致。
      const src = data!.filter((n) => Number.isFinite(n))
      const out: number[] = []
      for (let i = 0; i < n; i++) {
        const t = (i / (n - 1)) * (src.length - 1)
        const i0 = Math.floor(t)
        const i1 = Math.min(src.length - 1, i0 + 1)
        out.push(src[i0] + (src[i1] - src[i0]) * (t - i0))
      }
      raw = out
    } else {
      // 种子伪随机：每条线用各自的种子，页面顺序一变曲线不该全变。
      const base = seed || 40
      let s = (20261007 + base) >>> 0
      const rnd = () => {
        s = (s * 1103515245 + 12345) & 0x7fffffff
        return s / 0x7fffffff
      }
      let v = base
      raw = []
      for (let i = 0; i < n; i++) {
        v += (rnd() - 0.46) * 14
        v = clamp(v, base * 0.45 + 4, base * 1.45 + 8)
        raw.push(v)
      }
    }

    const lo = Math.min(...raw)
    const hi = Math.max(...raw)
    const span = hi - lo || 1
    const pts: [number, number][] = raw.map((v, i) => [
      (i / (n - 1)) * W,
      // 上下各留一点边距，免得线贴着框
      H - 3 - ((v - lo) / span) * (H - 8),
    ])
    const dd = pts.map((p, i) => `${i ? 'L' : 'M'}${p[0].toFixed(1)} ${p[1].toFixed(1)}`).join(' ')
    const peak = pts.reduce((a, b) => (b[1] < a[1] ? b : a))
    return { d: dd, area: `${dd} L${W} ${H} L0 ${H} Z`, peakY: peak[1], peakX: peak[0] }
  }, [seed, usable, data])

  return (
    <svg
      className={`spark${cls ? ' ' + cls : ''}`}
      viewBox="0 0 120 30"
      preserveAspectRatio="none"
      style={height ? { height, ...style } : style}
      aria-hidden="true"
    >
      <path className="area" d={area} />
      <line className="peakline" x1={0} y1={peakY} x2={120} y2={peakY} />
      <path className="line" d={d} />
      <rect className="pk" x={peakX - 1.5} y={peakY - 1.5} width={3} height={3} />
    </svg>
  )
}

/** 数字滚动（880ms，ease-out 三次方）。只在挂载时滚一次，之后随数据直接落值。 */
export function Num({
  value,
  className,
  style,
}: {
  value: number
  className?: string
  style?: CSSProperties
}) {
  const [shown, setShown] = useState(0)
  const mounted = useRef(false)
  useEffect(() => {
    if (mounted.current) {
      setShown(value)
      return
    }
    mounted.current = true
    const dur = 880
    const t0 = performance.now()
    let raf = 0
    const step = (t: number) => {
      const k = Math.min(1, (t - t0) / dur)
      setShown(Math.round(value * (1 - Math.pow(1 - k, 3))))
      if (k < 1) raf = requestAnimationFrame(step)
    }
    raf = requestAnimationFrame(step)
    return () => cancelAnimationFrame(raf)
  }, [value])
  return (
    <b className={className} style={style}>
      {shown}
    </b>
  )
}

/** MC 爱心（健康度）。逐颗弹出（.34s + 递进延迟）。 */
export function Hearts({
  score,
  max,
  className = 'hearts',
}: {
  score: number
  max: number
  className?: string
}) {
  return (
    <div className={className}>
      {Array.from({ length: max }, (_, i) => (
        <svg
          key={i}
          className="heart"
          style={{
            color: i < score ? 'var(--bad)' : '#2a333d',
            animationDelay: `${(0.35 + i * 0.07).toFixed(2)}s`,
          }}
          viewBox="0 0 16 16"
        >
          <use href="#i-heart" />
        </svg>
      ))}
    </div>
  )
}

// ─────────────────────────────────────────── 控件

export function Tog({
  on,
  onChange,
  title,
  ariaLabel,
  disabled,
}: {
  on: boolean
  onChange?: (v: boolean) => void
  title?: string
  ariaLabel?: string
  /** 有请求在飞的时候不能改 —— 连点会让「开→关→开」互相覆盖，最后停在哪全看返回顺序。 */
  disabled?: boolean
}) {
  return (
    <span
      className={`tog${on ? ' on' : ''}${disabled ? ' dis' : ''}`}
      role="switch"
      aria-checked={on}
      aria-label={ariaLabel}
      aria-disabled={disabled || undefined}
      title={title}
      tabIndex={disabled ? -1 : 0}
      onClick={() => {
        if (!disabled) onChange?.(!on)
      }}
      onKeyDown={(e) => {
        if (disabled) return
        if (e.key === ' ' || e.key === 'Enter') {
          e.preventDefault()
          onChange?.(!on)
        }
      }}
    >
      <span className="knob" />
    </span>
  )
}

export function Chk({
  on,
  onChange,
  title,
}: {
  on: boolean
  onChange?: (v: boolean) => void
  title?: string
}) {
  return (
    <span
      className={`chk${on ? ' on' : ''}`}
      role="checkbox"
      aria-checked={on}
      title={title}
      tabIndex={0}
      onClick={(e) => {
        e.stopPropagation()
        onChange?.(!on)
      }}
      onKeyDown={(e) => {
        if (e.key === ' ' || e.key === 'Enter') {
          e.preventDefault()
          e.stopPropagation()
          onChange?.(!on)
        }
      }}
    />
  )
}

/** 筛选芯片：`.chipf`（默认单选，容器 data-multi 变多选）。 */
export function Chipf({
  on,
  children,
  onClick,
  title,
}: {
  on?: boolean
  children: ReactNode
  onClick?: () => void
  title?: string
}) {
  return (
    <button type="button" className={`chipf${on ? ' on' : ''}`} onClick={onClick} title={title}>
      {children}
    </button>
  )
}

export function Filters({
  children,
  multi = false,
  style,
}: {
  children: ReactNode
  multi?: boolean
  style?: CSSProperties
}) {
  return (
    <div className="filters" data-multi={multi ? '' : undefined} style={style}>
      {children}
    </div>
  )
}

/** 数字步进器：`− [值] +`（设计稿 .stepper）。**禁 input[type=number]**。 */
export function Stepper({
  value,
  onChange,
  min = 0,
  max = Number.MAX_SAFE_INTEGER,
  step = 1,
  width = 130,
  style,
  suffix,
}: {
  value: number
  onChange?: (v: number) => void
  min?: number
  max?: number
  step?: number
  width?: number
  style?: CSSProperties
  suffix?: string
}) {
  const [text, setText] = useState(String(value))
  const focused = useRef(false)
  useEffect(() => {
    if (!focused.current) setText(String(value))
  }, [value])
  const set = (v: number) => onChange?.(clamp(v, min, max))
  return (
    <div className="stepper" style={style}>
      <button type="button" aria-label="减" onClick={() => set(value - step)}>
        −
      </button>
      <input
        className="inp"
        style={{ width }}
        value={text}
        inputMode="numeric"
        onFocus={() => (focused.current = true)}
        onBlur={() => {
          focused.current = false
          const n = Number(text)
          if (Number.isFinite(n)) set(n)
          else setText(String(value))
        }}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
        }}
      />
      <button type="button" aria-label="加" onClick={() => set(value + step)}>
        +
      </button>
      {suffix && <span style={{ alignSelf: 'center', marginLeft: 8, fontSize: 12.5, color: 'var(--ink-3)' }}>{suffix}</span>}
    </div>
  )
}

// ─────────────────────────────────────────── 对话框

/**
 * 设计稿的弹窗（`.dlg-overlay` + `.dlg`）。
 *
 * ⚠ 必须 portal 到 document.body —— 祖先只要有 transform / filter，
 *   position:fixed 就会以它为包含块，弹窗会被塞进面板里而不是浮在全屏上。
 *   （设计稿是静态页没这问题；搬进 React 就会踩到。）
 */
export function Dlg({
  title,
  icon,
  iconColor,
  onClose,
  children,
  footer,
  width,
}: {
  title: ReactNode
  icon?: string
  iconColor?: string
  onClose?: () => void
  children: ReactNode
  footer?: ReactNode
  width?: number
}) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose?.()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onClose])

  return createPortal(
    <div
      className="dlg-overlay"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose?.()
      }}
    >
      <div className="dlg" style={width ? { maxWidth: width, width: '100%' } : undefined} role="dialog" aria-modal="true">
        <div className="dlg-head">
          {icon && <Icon name={icon} style={{ color: iconColor ?? 'var(--cyan)' }} />}
          <h2>{title}</h2>
          {onClose && (
            <button className="btn btn-ghost xs x" type="button" aria-label="关闭" onClick={onClose}>
              <Icon name="x" />
            </button>
          )}
        </div>
        <div className="dlg-body">{children}</div>
        {footer && <div className="dlg-foot">{footer}</div>}
      </div>
    </div>,
    document.body,
  )
}

// ─────────────────────────────────────────── 日志

export type LogLevel = 'INFO' | 'WARN' | 'ERROR' | 'LOG'

const LEVEL_COLOR: Record<string, string> = {
  INFO: '#4aedd9',
  WARN: '#f2c14e',
  ERROR: '#ff6b6b',
  LOG: '#4aedd9',
}

/**
 * 把服务端原始日志行拆成 时间 / 级别 / 正文。
 * 兼容两种常见形态：
 *   `[03:11:40] [Server thread/INFO]: Done (12.483s)!`   ← Paper/原版
 *   `[03:11:40 INFO]: 消息`                              ← 部分服务端
 * 拆不出来就整行当正文（级别给 LOG，不瞎标）。
 */
export function parseLogLine(raw: string): { t: string; lv: LogLevel; msg: string } {
  const s = raw.replace(/\u00a7./g, '') // 去掉 MC 颜色码
  let t = ''
  let lv: LogLevel = 'LOG'
  let msg = s

  const m1 = /^\[(\d{2}:\d{2}:\d{2})\]\s*\[([^\]]*)\]:\s?([\s\S]*)$/.exec(s)
  if (m1) {
    t = m1[1]
    const tag = m1[2]
    msg = m1[3]
    const mm = /(INFO|WARN|ERROR|FATAL|DEBUG|TRACE)/.exec(tag)
    lv = mm ? (mm[1] === 'FATAL' ? 'ERROR' : (mm[1] as LogLevel)) : 'LOG'
  } else {
    const m2 = /^\[(\d{2}:\d{2}:\d{2})\s+(INFO|WARN|ERROR|LOG)\]:?\s?([\s\S]*)$/.exec(s)
    if (m2) {
      t = m2[1]
      lv = m2[2] as LogLevel
      msg = m2[3]
    }
  }
  if (lv === 'DEBUG' as string || lv === 'TRACE' as string) lv = 'LOG'
  return { t, lv, msg }
}

/**
 * at = 面板**收到这一行**的时刻（毫秒）。
 *
 * 为什么需要它：源日志里有一大批行本来就不带时间戳 —— 面板自己写的
 * `[EnderBox] ...`（命令回显、安装完成通知…），以及 Fabric/Forge 打印的 mod 列表
 * （`- fabric-api 0.92.12+1.20.1`）。实测 1447 行里 675 行（47%）解析不出时间。
 * 这些行不是「时间丢了」，是上游压根没给 —— 所以**不能编一个服务端时间去冒充**。
 * 退而求其次：显示「面板收到它的时刻」，并在样式上弱化、悬停说明出处。
 * 历史快照（打开页面时拉的那批）at=0，不显示 —— 那是过去的行，用现在的时刻是错的。
 */
export type LogEntry = { id: number; raw: string; t: string; lv: LogLevel; msg: string; at?: number }

/** 毫秒时刻 → 本地 HH:MM:SS，与服务端日志的时间戳同宽。 */
export function fmtClock(ms: number): string {
  const d = new Date(ms)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

/** 一行日志：`.log-line`。加入/离开/等宽高亮按正文里的关键词着色。 */
export function LogLine({
  entry,
  fresh,
}: {
  entry: LogEntry
  fresh?: boolean
}) {
  const { lv, t, msg, at } = entry
  // 源日志没带时间戳（t 为空）时退回「面板收到的时刻」；弱化样式 + 悬停说明，不冒充服务端时间。
  const est = t === '' && (at ?? 0) > 0
  const clock = t || (est ? fmtClock(at as number) : '')
  const joined = /joined the game/i.test(msg)
  const left = /(left the game|lost connection|disconnect)/i.test(msg)
  const cls = `log-line${fresh ? ' fresh' : ''}${joined ? ' joined' : ''}${left ? ' left' : ''}`
  return (
    <div className={cls} data-lv={lv}>
      <span
        className={`t${est ? ' est' : ''}`}
        title={est ? '源日志没有时间戳 —— 这是面板收到这一行的时刻' : undefined}
      >
        {clock}
      </span>
      <span className="lv" style={{ color: LEVEL_COLOR[lv] ?? LEVEL_COLOR.LOG }}>
        {lv}
      </span>
      <span className="m">{msg}</span>
    </div>
  )
}

/** 控制台日志体：自动滚到底（跟随中时），带扫描线。 */
export function LogBody({
  entries,
  height,
  maxHeight,
  className = '',
  filter = 'all',
}: {
  entries: LogEntry[]
  height?: number
  maxHeight?: number | string
  className?: string
  filter?: 'all' | 'warn' | 'err'
}) {
  const ref = useRef<HTMLDivElement>(null)
  // 粘底滚动与控制台大页共用一份实现（useStickyScroll）——
  // 那边记着「程序贴底触发的 scroll 事件被误当用户上翻」这个坑的完整拆解。
  const sticky = useStickyScroll(ref, entries)

  // ⚠ 容器样式里那个 overflowAnchor:'none' 是粘底判定的一部分：
  // 浏览器的滚动锚定会在内容变化时擅自挪 scrollTop，把「用户没动过」的前提打破。
  const filterCls =
    filter === 'warn' ? ' only-warn' : filter === 'err' ? ' only-err' : ''

  return (
    <div
      ref={ref}
      className={`con-body${filterCls}${className ? ' ' + className : ''}`}
      style={{
        ...(height ? { minHeight: height } : {}),
        ...(maxHeight != null
          ? { maxHeight: typeof maxHeight === 'number' ? `${maxHeight}px` : maxHeight }
          : {}),
        overflowAnchor: 'none',
      }}
      {...sticky.scrollProps}
    >
      {/* 空态不能是"一整块空背景"：控制台被右侧列拉高后，空着的地方就是
          用户说的「虚高」。没日志时得说清楚为什么空、什么时候会有。 */}
      {entries.length === 0 && (
        <p style={{ padding: '10px 12px', fontSize: 12.5, color: 'var(--ink-3)' }}>
          这一屏还没有日志。服务器启动后会实时往上滚；没在跑的时候，这里只留上次退出前的输出。
        </p>
      )}
      {entries.map((e) => (
        <LogLine key={e.id} entry={e} />
      ))}
      {!sticky.following && (
        <div
          style={{
            position: 'sticky',
            bottom: 0,
            textAlign: 'center',
            paddingTop: 8,
          }}
        >
          <button type="button" className="btn xs" onClick={sticky.stick}>
            回到最新
          </button>
        </div>
      )}
    </div>
  )
}
