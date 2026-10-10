import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Icon } from './design'

/** 区块标题：设计稿 .sec-head（idx + h2 + sub + 右侧动作） */
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
    <div className="sec-head" style={{ flexWrap: 'wrap' }}>
      <span className="idx">{idx}</span>
      <h2>{title}</h2>
      {note && <span className="sub">{note}</span>}
      {right && <span className="spacer" />}
      {right}
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

/** 开关。设计稿的 .tog + .knob 原样用；外面套一层无样式 button 壳负责键盘可达。
    （设计稿里 .tog 是个静态 span，真实现必须可聚焦可回车，所以壳不能省） */
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
      className="tog-btn"
    >
      <span className={`tog${on ? ' on' : ''}`}>
        <span className="knob" />
      </span>
    </button>
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
    <div className="nbox w" style={{ display: 'flex', alignItems: 'flex-start', gap: 10 }}>
      <span style={{ marginTop: 3, flexShrink: 0, color: 'var(--warn)' }}>
        <Icon name="info" size={13} />
      </span>
      <p style={{ minWidth: 0 }}>{children}</p>
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
  icon,
  iconColor,
}: {
  title: string
  children: ReactNode
  actions: ReactNode
  onClose: () => void
  /** 不可逆操作（删除、强杀）—— 标题条换成红底，让人在按确认前多看一眼 */
  danger?: boolean
  width?: number
  /** 标题左侧的精灵图标（设计稿 .dlg-head 的 .pic）—— 只给少数「有性格」的弹窗用 */
  icon?: string
  iconColor?: string
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

  /*
    ⚠ 覆盖层一律 portal 到 document.body，不能就地渲染。

    祖先链上只要有一个元素挂着 transform / filter / backdrop-filter / will-change，
    position:fixed 就改认它当包含块 —— 于是「撑满视口」变成「撑满那个祖先」。
    本站的入场动画（.stagger / .anim-in / .anim-in-x）结束时就留下一个
    identity transform，抽屉压到 200 多像素高就是这么来的。
    portal 之后祖先只剩 body，这类坑一次免疫，以后谁再给页面加动画也踩不到。
  */
  return createPortal(
    /*
      ⚠ 单层 .dlg-overlay（设计稿原生的弹窗外壳）：遮罩、布局、滚动一个元素全包。

      为什么不能回到「滚动容器 + 独立遮罩 + 静态内容容器」那套两层结构：
      那样写的结果是**所有弹窗整体蒙灰、点哪儿都直接关掉**（真机上翻车过）。
      根因是 CSS 层叠规则：同一层叠上下文里，「z-index:auto 的定位元素」
      画在「无定位的静态元素」之上 —— fixed 遮罩把静态的 .dlg 整个盖住了，
      于是看是黑的、点击全落在遮罩的 onClose 上。单层后 .dlg 是 overlay 的子元素，
      两者是父子不是同层兄弟，不存在竞逐；点击只要 target === overlay 就说明点到了框外。

      ⚠ 垂直居中靠 design-overrides.css 的 `.dlg-overlay > .dlg { margin: auto }`，
      不用 align-items —— 设计稿本身是 flex-start 顶对齐（用户抱怨过「弹窗上浮」），
      margin:auto 在内容超高时自动退化为 0、能完整滚动，比 center 安全（后者会裁掉顶部）。
    */
    <div
      className="dlg-overlay"
      onClick={(e) => {
        // 只有点在 overlay 自己的空白区才算「点框外」；点在 .dlg 内部（含其 padding）都不关
        if (e.target === e.currentTarget) onClose()
      }}
    >
      <div
        ref={boxRef}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="dlg"
        style={{ maxWidth: width }}
      >
        {/*
          标题带用设计稿的 .dlg-head（sf-2 底 + 2px 下描边）。
          危险操作加 .danger 变体（红底 + 红字）—— 设计稿没有这个变体，
          见 design-overrides.css 里的说明；标题条原本是 3px 黑粗边，那是上一版的材质。
        */}
        <div className={`dlg-head${danger ? ' danger' : ''}`}>
          {/*
            警告三角**只在危险操作时出现**。
            原来不分场合地挂一个黄色三角，于是「新建任务」「编辑配置」「模组详情」
            这些中性对话框也顶着个警告图标 —— 它既不警告也不危险，是纯噪音，
            还会让人以为「我是不是点到什么危险的地方了」。
            真正的危险操作（删除实例、强杀、还原备份）才配得上这个记号。
          */}
          {/* 设计稿 .dlg-head 的图标位：图标在标题左侧，不是每只弹窗都配一个 */}
          {icon && <Icon name={icon} style={{ color: iconColor ?? 'var(--cyan)' }} />}
          {danger && (
            <span className="shrink-0" style={{ color: 'var(--bad)' }}>
              <Icon name="ban" size={14} />
            </span>
          )}
          <h2 id={titleId}>{title}</h2>
        </div>
        {/* .dlg-body 自带 padding 与 max-height:60vh 滚动 —— 内容再长也顶不破屏幕 */}
        <div className="dlg-body">{children}</div>
        <div className="dlg-foot">
          <div className="ml-auto flex flex-wrap items-center gap-2">{actions}</div>
        </div>
      </div>
    </div>,
    document.body,
  )
}

/**
 * 右侧滑出的抽屉。
 *
 * 为什么用抽屉而不是对话框：详情要和身后的名单**同屏**参照。
 * 「这个人是谁」的答案常常就在他旁边那几个人身上；对话框一开把名单整个盖住，
 * 关掉还得重新定位回原来那一行。点两次之后人就不想再点第三次了。
 *
 * 宽度给 `min(width, 100vw)`：窄屏时铺满，不学桌面端那样留一条缝 ——
 * 那点缝既显示不出后面有什么，又白白吃掉几十像素。
 */
export function Sheet({
  title,
  subtitle,
  subtitleProse,
  onClose,
  children,
  footer,
  width = 420,
}: {
  title: ReactNode
  subtitle?: ReactNode
  /**
   * 副标题默认等宽（UUID / 坐标那类机器数据正好）。
   * 中文散文传 true —— 等宽字体没有汉字，回退字形发飘；散文副标题走正文栈且允许折行。
   */
  subtitleProse?: boolean
  onClose: () => void
  children: ReactNode
  footer?: ReactNode
  width?: number
}) {
  const ref = useRef<HTMLDivElement>(null)
  const titleId = useId()

  useEffect(() => {
    const prev = document.activeElement as HTMLElement | null
    // 焦点落到关闭键上：抽屉没有「确认」这个终点，进去第一件事不该是误触某个危险按钮
    ref.current?.querySelector<HTMLElement>('[data-autofocus]')?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    document.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('keydown', onKey, true)
      prev?.focus?.()
    }
  }, [onClose])

  // 同 Dialog：portal 到 body，避开祖先 transform 造出来的假包含块
  return createPortal(
    <div className="fixed inset-0 z-[70]">
      {/*
        遮罩**刻意很淡**（对话框那边是 /70）。
        抽屉的价值就在于「详情和身后的名单同屏参照」—— 压到看不清名单，
        这个形态就只剩下「比对话框窄一点」这一个区别了，还白白丢掉唯一的好处。
        淡到「能看出多了一层、点得到、关得掉」就够了。
      */}
      <div className="fixed inset-0 bg-black/15" onClick={onClose} aria-hidden="true" />
      <div
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="sheet"
        style={{ ['--sheet-w' as string]: `${width}px` } as CSSProperties}
      >
        <div className="sheet-head">
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="sheet-title">
              {title}
            </h2>
            {subtitle && (
              <div className={`sheet-sub${subtitleProse ? ' sheet-sub-prose' : ''}`}>{subtitle}</div>
            )}
          </div>
          <button
            type="button"
            className="ibtn"
            data-autofocus
            onClick={onClose}
            title="关闭（Esc）"
            aria-label="关闭"
          >
            <Icon name="x" size={13} />
          </button>
        </div>
        <div className="sheet-body">{children}</div>
        {footer && <div className="sheet-foot">{footer}</div>}
      </div>
    </div>,
    document.body,
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
 * 相对时间：「3 个月前」这种。
 *
 * 为什么不用绝对日期：挑模组版本时，人真正想知道的是「这版还新不新」。
 * `2023-08-14` 得在脑子里减一次才知道是三年前，而「1 年 10 个月前」一眼就够。
 * 太长的时候退回绝对日期 —— 「237 天前」不如直接给日期。
 */
export function fmtAgo(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  const days = Math.floor((Date.now() - d.getTime()) / 86400000)
  if (days < 0) return '刚刚'
  if (days === 0) return '今天'
  if (days === 1) return '昨天'
  if (days < 30) return `${days} 天前`
  const months = Math.floor(days / 30)
  if (months < 12) return `${months} 个月前`
  const years = Math.floor(days / 365)
  const rest = Math.floor((days - years * 365) / 30)
  return rest > 0 ? `${years} 年 ${rest} 个月前` : `${years} 年前`
}

/**
 * 相对时间（分钟级）：「刚刚 / 6 分钟前 / 3 小时前」。
 *
 * 和 fmtAgo 的区别只在**粒度**：fmtAgo 回答「这版模组还新不新」（天级就够），
 * 这个回答「刚才那件事过去多久了」—— 下载跑完两三分钟，天级看不出差别。
 * 超过一天退回绝对日期：台账里「2 天前」还要再做一次减法，不如直接给日期。
 * 解析不出来返回空串（不摆占位符 —— 调用方自己决定那格要不要留）。
 */
export function fmtAgoMin(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ''
  const s = Math.max(0, Math.floor((Date.now() - t) / 1000))
  if (s < 90) return '刚刚'
  if (s < 3600) return `${Math.floor(s / 60)} 分钟前`
  if (s < 86400) return `${Math.floor(s / 3600)} 小时前`
  const d = new Date(t)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
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

/** 列表类页面的骨架。版式刻意对齐 .rlist / .rrow，数据到位时不会「跳」一下。 */
export function SkeletonList({ rows = 3, icon = true }: { rows?: number; icon?: boolean }) {
  return (
    <div className="panel" role="status" aria-busy="true" aria-label="正在读取">
      <i className="corner" />
      {Array.from({ length: rows }, (_, i) => (
        <div
          key={i}
          style={{
            display: 'grid',
            gridTemplateColumns: '32px minmax(0, 1fr) auto',
            gap: 12,
            alignItems: 'center',
            padding: '12px 14px',
            borderTop: i > 0 ? '1px solid var(--line-1)' : undefined,
          }}
        >
          {icon && <Skeleton w="32px" h={32} />}
          <div className="min-w-0">
            <Skeleton w={`${54 - i * 9}%`} h={11} />
            <div style={{ marginTop: 8 }}>
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
    <div
      className="panel"
      style={{ padding: '16px 20px' }}
      role="status"
      aria-busy="true"
      aria-label="正在读取"
    >
      <i className="corner" />
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
