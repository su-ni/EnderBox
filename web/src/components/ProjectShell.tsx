import { useEffect, useRef, type ReactNode } from 'react'
import { Icon } from './design'
import { Skeleton } from './ui'

/**
 * 项目详情页的**共用外壳**。
 *
 * 为什么要有这一层：整合包详情（ModpackDetail）那一版版式是用户点名的参考
 * （抬头一行 + 页内页签 + 左正文右信息栏 + 版心居中），而模组详情要做成同一套。
 * 与其把两百行版式抄第二遍 —— 抄完两边就会各自漂移，改一边忘一边，
 * 表现为「同一个产品里两个详情页长得不一样」—— 不如把骨架抽在这里，
 * 两页各自只提供**内容**。
 *
 * 这一层只管版式，不管业务：什么算版本、能不能装、点了装到哪，全在调用方。
 *
 * 材质照设计稿 mod.html / market.html 的详情页：
 *   顶行一条「返回」（btn btn-ghost sm）→ 下面是抬头（sec-head + 图标）→ 两栏。
 *   页内页签用设计稿的筛选标签组（chipf tab）——它和玩家页的档位切换是同一套形状。
 */
export type ShellTab = { id: string; label: string; badge?: string }

export function ProjectShell({
  onBack,
  backLabel = '返回列表',
  err,
  onRetry,
  loading = false,
  head,
  tabs,
  tab,
  onTab,
  navLabel,
  desc,
  dl,
  rail,
  below,
}: {
  onBack: () => void
  backLabel?: string
  err?: string
  /** 有重试入口时才显示「重试」；没有就只留「回列表重挑」 */
  onRetry?: () => void
  loading?: boolean
  /** 抬头一行：图标 + 身份 + 简介 + 统计 + 主行动 */
  head: ReactNode
  /** 页内页签。第一个约定为「介绍」、第二个约定为「下载」 */
  tabs: ShellTab[]
  tab: string
  onTab: (id: string) => void
  navLabel: string
  /** 第一个页签的内容（介绍正文） */
  desc: ReactNode
  /** 第二个页签的内容（版本 / 安装） */
  dl: ReactNode
  /** 右侧信息栏的内容，由调用方用 RailSection / RailRow 拼（它们会落进同一个 panel） */
  rail: ReactNode
  /**
   * 两栏**下面**的通栏内容（页末）。
   *
   * 为什么要有这个位置：MC 百科关系星图是一张要占满宽度的力导向图，
   * 塞进 300px 的右栏会糊成一团（实测被用户否掉过）。它也不属于任何一个页签，
   * 所以放在双栏之下、整页之末 —— 想看图的人滚到底就能看到，不想看的不会被挡路。
   */
  below?: ReactNode
}) {
  const navRef = useRef<HTMLElement | null>(null)

  /*
    切页之后把分页条拉回视口。
    判据是「它已经被滚到视口上方」而不是无脑滚 —— 无脑滚会在用户本来就看着分页条时
    平白跳一下（scrollIntoView 的 block:'start' 即使元素已完全可见也会把它对齐到顶）。
  */
  useEffect(() => {
    const el = navRef.current
    if (el && el.getBoundingClientRect().top < 0) el.scrollIntoView({ block: 'start' })
  }, [tab])

  return (
    /*
      版心包住**整页**（含最上面那条「返回」）：
      只包正文的话，宽屏上版心居中了、返回按钮还贴在屏幕最右，两块对不齐。
      ⚠ 居中的是**版心这一块**，正文文字仍是左对齐
      （本项目禁的是把正文居中排，不是把版心居中）。
      ⚠⚠ `w-full` 不能少：main 是 `display:flex; flex-direction:column`，
      flex 项一旦有 `margin:0 auto`（mx-auto）就**不再被拉伸**，改成按内容收缩（fit-content）
      再被 max-width 截断 —— 于是「介绍」页内容宽顶到上限、「下载」页内容窄就缩水，
      切页签时整页左右横跳（实测 1240 vs 1140，每边跳 50px）。
      ⚠ px-1（4px 侧衬）不是留白偏好，是**给像素转角让位**：
      .panel 的 ::before/::after 转角标故意探出面板外缘 2px（设计稿如此），
      而窗口收窄到版心贴住 main 左右缘时（<约1510px），main 的 overflow 裁切
      会把右侧转角的竖笔画切得只剩 1px —— 左右两个角一完整一残废，很扎眼。
      4px 侧衬让转角有探出的余量，肉眼不可辨，全屏感不变。
    */
    /* max-w 从 1240 提到 1600：宽屏上版心两侧白扔几百 px，用户原话
       「左右这么多的空间不用，干什么吃的」。1600 在 1920 屏上只留 24px 边。 */
    <div className="mx-auto w-full max-w-[1600px] px-1">
      {/* 返回：照设计稿 mod.html，独立一行放左上；身份（PACK/MOD + 名称）在下面的抬头里 */}
      <button type="button" className="btn btn-ghost sm" onClick={onBack}>
        <Icon name="chev-l" size={12} />
        {backLabel}
      </button>

      {err && (
        <div className="nbox d mt-3.5">
          <p>{err}</p>
          <div className="mt-2.5 flex flex-wrap gap-2">
            {onRetry && (
              <button type="button" className="btn sm" onClick={onRetry}>
                <Icon name="refresh" size={12} />
                重试
              </button>
            )}
            <button type="button" className="btn btn-ghost sm" onClick={onBack}>
              回列表重挑
            </button>
          </div>
        </div>
      )}

      {loading && !err && (
        <div className="mt-4 space-y-2">
          <Skeleton w="38%" h={18} />
          <Skeleton w="72%" />
          <Skeleton w="52%" />
        </div>
      )}

      {!loading && !err && (
        <>
          <div className="mt-3.5">{head}</div>

          {/*
            左右两栏。
            窄屏（<lg）时退化成**单栏，正文在前、信息栏在后** —— 和 Modrinth 手机端一致。
            试过把信息栏 order-first 提到前面，结果手机上要先划过整整一屏（约 700px）
            才看得到正文，比「兼容性晚一点看到」更糟。
            xl 上信息栏 340 而不是 300：1440 屏上版心正好填满内容区，右侧不留空。
          */}
          <div className="mt-5 grid grid-cols-1 gap-x-6 gap-y-5 lg:grid-cols-[minmax(0,1fr)_300px] xl:grid-cols-[minmax(0,1fr)_340px]">
            <div className="min-w-0">
              {/*
                页内页签用设计稿的筛选标签组（.fchip.tab）——和玩家页的档位切换同一套形状，
                不另造一种页签。宽度自然撑开（w-fit）：这里只有 2 个，铺满会变成两个大格子。
              */}
              <nav ref={navRef} className="filters" aria-label={navLabel}>
                {tabs.map((t, i) => (
                  <button
                    key={t.id}
                    type="button"
                    aria-current={tab === t.id}
                    className={`fchip tab${tab === t.id ? ' on' : ''}`}
                    onClick={() => onTab(t.id)}
                  >
                    <span className="mr-2 font-mono text-[11px] opacity-70">
                      {String(i + 1).padStart(2, '0')}
                    </span>
                    {t.label}
                    {t.badge && <span className="ml-2 font-mono text-[11px] opacity-70">{t.badge}</span>}
                  </button>
                ))}
              </nav>

              {/* key 换掉就重挂载，动画自然重放；不用额外状态管理（和设置页同一个套路） */}
              <div key={tab} className="anim-in">
                {tab === 'desc' ? desc : dl}
              </div>
            </div>

            {/*
              右侧信息栏（照 Modrinth 的 Compatibility / Links / Tags / Details）。
              桌面端吸顶：正文能读几十屏，这些「读的时候一定会回头找」的东西不该跟着滚走。
              设计稿 mod.html 的 rail 是**一个 panel 内部若干 rail-sec** —— 不是四个各自带边框的小卡片。
            */}
            <aside className="order-1 lg:order-none lg:sticky lg:top-4 lg:self-start">
              <div className="panel" style={{ padding: '14px 16px' }}>
                {rail}
              </div>
            </aside>
          </div>

          {/* 页末通栏：关系星图这类「要占满宽度、又不属于任何页签」的内容放这儿 */}
          {below ? <div className="mt-6">{below}</div> : null}
        </>
      )}
    </div>
  )
}

/**
 * 抬头一行：左边是身份（图标 + MOD/PACK 小标 + 名称 + 作者许可 + 一句话简介 + 统计），
 * 右边是主行动。照设计稿 mod.html —— 名称那行就是 sec-head（像素 idx + h2 + sub），
 * 不是另造一种标题。
 */
export function ProjectHead({
  eyebrow,
  iconUrl,
  title,
  origTitle,
  meta,
  description,
  stats,
  action,
}: {
  /** 左上角那枚小标（PACK / MOD），进 sec-head 的 idx 位 */
  eyebrow?: string
  iconUrl?: string
  /** 主标题：有中文名就给中文名，认东西靠中文 */
  title: string
  /** 英文原名，降一级显示 —— 用户拿它去源站核对时要对得上 */
  origTitle?: string
  /** 「作者 X · 许可 Y」这类，取不到就整条不传（进 sec-head 的 sub 位） */
  meta?: ReactNode
  description?: string
  /** 「N 次下载 · M 人关注 · K 天前更新」这类 */
  stats?: ReactNode
  /** 右侧主行动（安装按钮）。没有就不占位 */
  action?: ReactNode
}) {
  return (
    <div className="flex items-start gap-x-3.5">
      {iconUrl ? (
        <img
          src={iconUrl}
          alt=""
          className="h-16 w-16 shrink-0 border-2 border-[#04060a] object-cover shadow-[0_0_0_1px_#000]"
        />
      ) : (
        <span className="flex h-16 w-16 shrink-0 items-center justify-center border-2 border-[#04060a] bg-[var(--sf-3)] text-ink-3 shadow-[0_0_0_1px_#000]">
          <Icon name="cube" size={26} />
        </span>
      )}
      <div className="min-w-0 flex-1">
        <div className="sec-head" style={{ marginBottom: 0 }}>
          {eyebrow && <span className="idx">{eyebrow}</span>}
          <h2>{title}</h2>
          {meta && <span className="sub">{meta}</span>}
        </div>
        {origTitle && origTitle !== title && <p className="mono-meta mt-1">{origTitle}</p>}
        {/* 作者自己写的一句话简介。它和「介绍」页里的正文不是一回事：
            这句是用来在**还没决定要不要读**的时候做判断的。 */}
        {description && (
          <p className="mt-1.5 max-w-[72ch] text-[12.5px] leading-[1.7] text-ink-2">{description}</p>
        )}
        {stats && <p className="mono-meta mt-1.5">{stats}</p>}
      </div>
      {action}
    </div>
  )
}

/** 信息栏里的一节。设计稿的 rail-sec：像素小标题 + 内容，节与节之间 16px（见覆盖层）。 */
export function RailSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="rail-sec">
      <h4>{title}</h4>
      {children}
    </div>
  )
}

/** 信息栏里的「键 — 值」一行（设计稿的 srow：像素 k + 终端字体的 v）。 */
export function RailRow({ k, v }: { k: string; v: string }) {
  return (
    <div className="srow">
      <span className="k">{k}</span>
      <span className="v">{v}</span>
    </div>
  )
}

/**
 * 信息栏里的外链。照设计稿 mod.html 的「外链」节：一排 btn-ghost xs 竖着摆。
 * primary 给「去源站看原页」那一条 —— 它比其他链接重一档（实底），永远排最前。
 * 真机上它是 <a>（真的跳转），btn 类本来就是 inline-flex，套在 <a> 上形状不变。
 */
export function ExtLink({ href, label, primary }: { href: string; label: string; primary?: boolean }) {
  return (
    <a
      className={primary ? 'btn xs' : 'btn btn-ghost xs'}
      href={href}
      target="_blank"
      rel="noreferrer noopener"
    >
      {label}
    </a>
  )
}

// 三个「源站内部值 → 人话」的映射（licenseLabel / sideLabel / releaseLabel）
// 已挪到 ./projectMeta —— 它们是纯函数，留在这里会让本文件不再「只导出组件」，
// fast refresh 就失效了（oxlint 的 react(only-exports-components) 会报）。
