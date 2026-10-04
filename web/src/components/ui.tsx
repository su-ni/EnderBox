import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { IconInfo, IconWarn } from './Icons'

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

// 对话框里可能被聚焦的元素。和浏览器内建的那套 tabbable 规则保持一致即可，
// 不需要把每一种情况都穷举 —— 漏掉的最多是不能用 Tab 到达，不会出错。
const FOCUSABLE =
  'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'

/**
 * 应用内对话框。
 *
 * 为什么不用 window.confirm / alert（用户原话：「为什么弹窗全都是浏览器自带弹窗」）：
 * 一是它长得跟这个像素面板毫无关系，白底圆角、跟主题完全脱节；
 * 二是它只能承载「一段纯文本 + 两个按钮」—— 想加「同时删除世界文件」这种选项根本没地方放；
 * 三是它会把整个 JS 线程卡住，动画和输入全部冻住。
 *
 * 键盘可达是硬要求（这个面板用键盘操作的人不少）：
 *   · 打开时把焦点收进对话框 —— 不收的话 Tab 继续在背后的页面上走，键盘用户直接迷路；
 *   · Esc 关闭；
 *   · Tab / Shift+Tab 在框内循环，不会跑到遮罩背后去；
 *   · 关闭后焦点还给打开它的那个元素。
 */
export function Dialog({
  title,
  children,
  actions,
  onClose,
  danger = false,
  width = 440,
}: {
  title: string
  children: ReactNode
  actions: ReactNode
  onClose: () => void
  /** 不可逆操作（删除、强杀）—— 标题条换成红底，让人在按确认前多看一眼 */
  danger?: boolean
  width?: number
}) {
  const boxRef = useRef<HTMLDivElement>(null)
  const titleId = useId()

  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    const box = boxRef.current
    // data-autofocus 让调用方指定「打开时就该按的那个键」——危险操作应该先落在「取消」上
    const first =
      box?.querySelector<HTMLElement>('[data-autofocus]') ?? box?.querySelector<HTMLElement>(FOCUSABLE)
    first?.focus()

    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
        return
      }
      if (e.key !== 'Tab' || !box) return
      const items = Array.from(box.querySelectorAll<HTMLElement>(FOCUSABLE))
      if (items.length === 0) return
      const firstEl = items[0]
      const lastEl = items[items.length - 1]
      const cur = document.activeElement
      // 只在「到头」和「焦点已经跑到框外」两种情况下接管，其余交给浏览器默认行为
      if (e.shiftKey && (cur === firstEl || !box.contains(cur))) {
        e.preventDefault()
        lastEl.focus()
      } else if (!e.shiftKey && (cur === lastEl || !box.contains(cur))) {
        e.preventDefault()
        firstEl.focus()
      }
    }

    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      prev?.focus?.()
    }
  }, [onClose])

  return (
    <div className="fixed inset-0 z-[70] flex items-start justify-center overflow-y-auto px-4 py-[8vh]">
      {/* 点遮罩关闭：这是所有人的肌肉记忆，不做反而显得坏掉了 */}
      <div className="fixed inset-0 bg-black/70" onClick={onClose} aria-hidden="true" />
      <div
        ref={boxRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="pxb relative w-full bg-panel"
        style={{ maxWidth: width }}
      >
        <div
          className="flex items-center gap-2.5 border-b-[3px] border-black px-4 py-3"
          style={{ background: danger ? '#1c1314' : 'var(--color-panel-2)' }}
        >
          <span className="shrink-0" style={{ color: danger ? 'var(--color-danger)' : 'var(--color-warn)' }}>
            <IconWarn size={14} />
          </span>
          <h2 id={titleId} className="text-[14px] leading-tight font-bold text-ink-1">
            {title}
          </h2>
        </div>
        <div className="px-4 py-3.5">{children}</div>
        <div className="flex flex-wrap justify-end gap-2 border-t-[3px] border-black bg-[#0c0f0c] px-4 py-3">
          {actions}
        </div>
      </div>
    </div>
  )
}

/**
 * 确认框的常用形态：一段正文 + 取消/确认。
 * 需要额外选项（比如「同时删除世界文件」那个勾）时把控件放进 children。
 */
export function ConfirmDialog({
  title,
  body,
  confirmLabel,
  onConfirm,
  onCancel,
  danger = false,
  width,
  children,
}: {
  title: string
  body: ReactNode
  confirmLabel: string
  onConfirm: () => void
  onCancel: () => void
  danger?: boolean
  /** 正文里要塞选项/长路径时给宽一点，默认 440 够放两三行字 */
  width?: number
  children?: ReactNode
}) {
  return (
    <Dialog
      title={title}
      danger={danger}
      width={width}
      onClose={onCancel}
      actions={
        <>
          {/* 焦点默认落在「取消」上：危险操作不该一个回车就执行掉 */}
          <button type="button" className="btn btn-ghost" data-autofocus onClick={onCancel}>
            取消
          </button>
          <button
            type="button"
            className={`btn ${danger ? 'btn-danger' : ''}`}
            onClick={onConfirm}
          >
            {confirmLabel}
          </button>
        </>
      }
    >
      <div className="text-[13px] leading-[1.7] text-ink-2">{body}</div>
      {children}
    </Dialog>
  )
}

type Ask = {
  title: string
  body: ReactNode
  confirmLabel: string
  danger?: boolean
  width?: number
  run: () => void
}

/**
 * 命令式确认框：`const { dialog, confirm } = useConfirm()`，
 * 需要问一句时 `confirm({...})`，然后把 `dialog` 渲染到 JSX 里。
 *
 * 为什么做成 hook 而不是让每个调用点自己 setState：
 * 面板里有五六处「删除/关闭」都要问一句，各写一份 open + payload 的 state
 * 就是同一段样板抄六遍 —— 而抄六遍的东西，早晚会有两处行为不一样。
 */
export function useConfirm() {
  const [ask, setAsk] = useState<Ask | null>(null)

  const dialog = ask ? (
    <ConfirmDialog
      title={ask.title}
      body={ask.body}
      confirmLabel={ask.confirmLabel}
      danger={ask.danger}
      width={ask.width}
      onCancel={() => setAsk(null)}
      onConfirm={() => {
        // 先关框再执行：动作如果失败会自己 toast，框停在上面反而挡住了那条错误
        const cur = ask
        setAsk(null)
        cur.run()
      }}
    />
  ) : null

  return { dialog, confirm: (a: Ask) => setAsk(a) }
}

export function fmtSize(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024 / 1024).toFixed(2)} GB`
  if (bytes >= 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`
  return `${bytes} B`
}

/** 上游有时不给大小（Fabric 的启动器 jar 就没给 Content-Length）。
 *  这时别显示「0 B」—— 那看起来像文件是空的，比说「不知道」还糟。 */
export function fmtSizeMaybe(bytes: number): string {
  return bytes > 0 ? fmtSize(bytes) : '大小未知'
}

export function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/**
 * 骨架屏：数据还没到时的占位。
 *
 * 为什么不用「读取中…」三个字：文字占位会让人以为已经加载完了、只是内容为空 ——
 * 放在「备份列表」这种地方就等于在撒谎。而且换行成文字会让页面高度先塌后跳。
 */
export function Skeleton({ w = '100%', h = 9 }: { w?: string; h?: number }) {
  return <span className="sk" style={{ width: w, height: h }} aria-hidden="true" />
}

/** 列表类页面的骨架。版式刻意对齐 ListRow，数据到位时不会「跳」一下。 */
export function SkeletonList({ rows = 3, icon = true }: { rows?: number; icon?: boolean }) {
  return (
    <div className="px-card" role="status" aria-busy="true" aria-label="正在读取">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="row-b flex items-center gap-3.5 px-4 py-3.5">
          {icon && <Skeleton w="18px" h={18} />}
          <div className="min-w-0 flex-1">
            <Skeleton w={`${54 - i * 9}%`} h={11} />
            <div className="mt-2">
              <Skeleton w="36%" />
            </div>
          </div>
          <Skeleton w="56px" h={22} />
        </div>
      ))}
    </div>
  )
}

/** 表单/配置类页面的骨架：对齐「标签 → 控件」的版式 */
export function SkeletonFields({ rows = 5 }: { rows?: number }) {
  return (
    <div className="px-card px-5 py-4" role="status" aria-busy="true" aria-label="正在读取">
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="py-3">
          <Skeleton w={`${32 - i * 3}%`} h={10} />
          <div className="mt-2.5">
            <Skeleton w="min(300px, 62%)" h={30} />
          </div>
        </div>
      ))}
    </div>
  )
}
