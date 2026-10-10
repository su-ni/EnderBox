import { useEffect, useState } from 'react'
import { Icon } from './design'
import { Sheet, fmtSize, fmtTime, fmtAgoMin } from './ui'
import { type FeedItem, type Toast } from '../useDownloadFeed'

// 「正在下 / 正在装」的两个界面形态，共用 useDownloadFeed 那份数据。
//
// 为什么要一份：下载任务（服务端文件、模组、JDK）和整合包作业（安装、升级）是**两套后端数据源**，
// 但对用户来说是同一件事 ——「后台有东西在跑」。分别轮询的话两个界面会各自拿到不同时刻的快照，
// 出现「气泡说 42%、台账里写 38%」这种对不上的画面。
//
// 两个形态的分工（用户明确要的排法）：
//   · **气泡**只管「进行中」——右下角，一个任务一个圈，转着的就是活的；
//   · **台账**（侧栏那个入口点开）才是记录：跑完的、失败的也都在，可以回头查。

// ── 气泡 ──────────────────────────────────────────────────

/** 一条纯文字通知（原来的 toast）。 */
/**
 * 右下角的气泡堆。两种内容共用一列 —— 分成两列的话它们会重叠。
 *
 * 位置选右下：左下是移动端抽屉的地盘，正中下方会盖住主内容区的操作按钮，
 * 右上被导航条占着。右下是这个面板里唯一长期空闲的角落。
 */
export function Bubbles({
  items,
  toasts,
  onDismiss,
  onOpenInstance,
  onCancel,
  aboveHud = false,
}: {
  items: FeedItem[]
  toasts: Toast[]
  onDismiss: (id: number) => void
  onOpenInstance: (id: string) => void
  onCancel: (item: FeedItem) => void
  /** 底部健康条（HUD）在不在。在的话气泡要抬到它上面 —— 直接落 bottom:12px 会钻进 HUD 里。 */
  aboveHud?: boolean
}) {
  const running = items.filter((i) => i.running)
  if (running.length === 0 && toasts.length === 0) return null

  return (
    <div
      // z-60：压过 HUD（z-50）—— 用户原话「气泡信息的优先级应该比底部条高」；
      // 但仍低于弹窗（.dlg-overlay z-80 / .sheet z-70），模态打开时气泡让位。
      className="pointer-events-none fixed right-3 z-[60] flex w-[302px] max-w-[88vw] flex-col gap-2"
      style={{ bottom: aboveHud ? 'calc(var(--hud-h) + 12px)' : 12 }}
      role="status"
      aria-live="polite"
    >
      {/* 文字通知在前（上面），进度气泡在下面。新通知从上面进来，不挤走正在看的进度。 */}
      {toasts.map((t) => (
        <div key={t.id} className="pxb-2 pointer-events-auto flex items-start gap-2 bg-panel px-3 py-2.5">
          <p className="min-w-0 flex-1 text-[12.5px] leading-[1.6] text-ink-1">{t.text}</p>
          <button
            type="button"
            className="ibtn shrink-0"
            onClick={() => onDismiss(t.id)}
            aria-label="关闭通知"
            title="关闭"
          >
            <Icon name="x" size={11} />
          </button>
        </div>
      ))}

      {running.map((i) => (
        <div key={i.key} className="pxb-2 pointer-events-auto flex items-center gap-2.5 bg-panel px-3 py-2.5">
          <ProgressRing pct={i.pct} />
          <div className="min-w-0 flex-1">
            <div className="flex items-baseline gap-1.5">
              <span className="shrink-0 text-[11px] font-bold tracking-[0.06em] text-cyan">
                {i.kind === 'download' ? '下载中' : i.kind === 'install' ? '安装中' : '升级中'}
              </span>
              {/* 百分比要**一直**在：用户原话就是「你好歹做个百分比」。未知总量时给「—」，
                  不给 0% —— 那会被读成卡住。 */}
              <span className="mono-value ml-auto shrink-0 text-[12px] text-ink-1">
                {i.pct === null ? '—' : `${Math.floor(i.pct)}%`}
              </span>
            </div>
            {i.instanceId ? (
              <button
                type="button"
                className="mt-0.5 block max-w-full truncate text-left text-[12.5px] leading-tight font-semibold text-ink-1 hover:underline"
                title={i.label}
                onClick={() => onOpenInstance(i.instanceId)}
              >
                {i.label}
              </button>
            ) : (
              <span className="mt-0.5 block truncate text-[12.5px] leading-tight font-semibold text-ink-1" title={i.label}>
                {i.label}
              </span>
            )}
            {/* step 是「中文 + 数字」的人话（「正在下载 · 1.2 MB / 5.6 MB」），
                不能用 mono-meta —— VT323 没有汉字，中文靠回退，一行两套字体很割裂。
                .step-line = 正文栈 + 等宽数字（见 design-overrides.css） */}
            <span className="step-line mt-0.5 block truncate">
              {i.step}
              {i.bytesTotal > 0 && ` · ${fmtSize(i.bytesDone)}/${fmtSize(i.bytesTotal)}`}
              {i.threads > 1 && ` · ${i.threads} 线程`}
              {i.resumed && ' · 续传'}
            </span>
          </div>
          <button
            type="button"
            className="ibtn shrink-0"
            onClick={() => onCancel(i)}
            aria-label={`取消 ${i.label}`}
            title="取消（已经下好的部分会留着，下次续传）"
          >
            <Icon name="x" size={11} />
          </button>
        </div>
      ))}
    </div>
  )
}

/**
 * 环形进度。
 *
 * 为什么是环不是条：气泡只有 300px 宽，横条放进去只剩一百多像素 ——
 * 那个宽度下「30%」和「33%」在视觉上没差别。圆环占的宽度固定（36px）、
 * 位置也固定，一眼看的是「转没转」，数字则由旁边的百分比负责。
 *
 * pct=null（总量未知）时**不画半圈假装知道**：给一个转着的弧，
 * 它表达的是「在动，但不知道还有多久」——这正是真实情况。
 */
function ProgressRing({ pct }: { pct: number | null }) {
  const R = 14
  const C = 2 * Math.PI * R
  const known = pct !== null
  const off = known ? C * (1 - Math.min(100, Math.max(0, pct)) / 100) : C * 0.75
  return (
    <svg
      width="36"
      height="36"
      viewBox="0 0 36 36"
      className={`shrink-0${known ? '' : ' ring-indet'}`}
      aria-hidden="true"
    >
      {/* 轨道：给足对比度，否则深色底上环会消失成一个色块 */}
      <circle cx="18" cy="18" r={R} fill="none" stroke="#3d453d" strokeWidth="4" />
      <circle
        cx="18"
        cy="18"
        r={R}
        fill="none"
        stroke="var(--color-cyan)"
        strokeWidth="4"
        strokeDasharray={C}
        strokeDashoffset={off}
        strokeLinecap="butt"
        // 从 12 点方向开始转 —— 默认从 3 点开始，读起来像「差了一格」
        transform="rotate(-90 18 18)"
        style={{ transition: 'stroke-dashoffset 220ms linear' }}
      />
    </svg>
  )
}

// ── 记录面板（台账） ──────────────────────────────────────
//
// 这一版是**推倒重来**的（旧版是「两段一模一样的行列表」）。打开台账的第一眼：
//   · 先看总量 —— 段落头右侧给「合计 3.4 MB/s」，不用自己一条条加；
//   · 「进行中」是**作业票**（卡片）：名字 / 步骤 / 速度 / 进度条 / 字节，一层层往下铺 ——
//     正在动的东西配得上这个面积，也与下边的记录行拉开层级；
//   · 「最近结束」是**紧凑行**：一行说完「谁、成没成、多久前、多大」，完了就把位置让出来；
//   · 底部「清空已结束」—— 账本会越堆越长，一条条删不现实（两段式点击防误触）。

export function DownloadPanel({
  open,
  items,
  onClose,
  onOpenInstance,
  onCancel,
  onDismiss,
  onClearFinished,
}: {
  open: boolean
  items: FeedItem[]
  onClose: () => void
  onOpenInstance: (id: string) => void
  onCancel: (item: FeedItem) => void
  /** 删掉一条已结束的记录。运行中的行不出现这个按钮 —— 后端也会拒（409）。 */
  onDismiss: (item: FeedItem) => void
  /** 一次清掉全部已结束的记录。逐条删的实现细节（含部分失败）由调用方收口。 */
  onClearFinished: (finished: FeedItem[]) => void
}) {
  // 「清空已结束」的两段式确认：第一下把按钮切成警示态，3.5 秒没有第二下就自动复位。
  // 不弹模态 —— 删的是**记录**不是文件，配不上一个对话框；但一笔清光又值得一次迟疑。
  const [confirmClear, setConfirmClear] = useState(false)
  useEffect(() => {
    if (!confirmClear) return
    const t = setTimeout(() => setConfirmClear(false), 3500)
    return () => clearTimeout(t)
  }, [confirmClear])

  if (!open) return null
  const running = items.filter((i) => i.running)
  const past = items.filter((i) => !i.running)
  // 合计速度：只加给得出的（整合包作业没有速度概念，恒 0，不参与）。
  const totalRate = running.reduce((s, i) => s + (i.rate > 0 ? i.rate : 0), 0)

  return (
    <Sheet
      title="下载与安装"
      subtitle="任务跑在守护进程里：关掉这个页面没关系，守护进程一退（桌面窗口关闭 / 服务停止）它们才会断"
      subtitleProse
      onClose={onClose}
      width={600}
      footer={
        items.length > 0 ? (
          <div className="flex w-full items-center gap-2">
            <span className="step-line">共 {items.length} 条记录</span>
            <span className="flex-1" />
            {past.length > 0 && (
              <button
                type="button"
                className={`btn btn-ghost xs${confirmClear ? ' btn-danger' : ''}`}
                onClick={() => {
                  if (!confirmClear) {
                    setConfirmClear(true)
                    return
                  }
                  setConfirmClear(false)
                  onClearFinished(past)
                }}
              >
                <Icon name="trash" size={12} />
                {confirmClear ? `再点一次，清掉这 ${past.length} 条` : '清空已结束'}
              </button>
            )}
          </div>
        ) : undefined
      }
    >
      {items.length === 0 ? (
        // 空态：左对齐，图标内联在标题左侧（「大图标 + 居中标题」那个模板是设计规范点名的反例）。
        // 文案要回答「这儿是干嘛的、我怎么让它有东西」，不能只说「暂无数据」。
        <div className="flex items-start gap-3 px-0.5 py-6">
          <Icon name="dl" size={20} style={{ color: 'var(--ink-3)', flex: 'none', marginTop: 2 }} />
          <div className="min-w-0">
            <p className="text-[13.5px] leading-[1.6] font-semibold text-ink-2">
              现在没有正在下、也没有正在装的东西
            </p>
            <p className="mt-1.5 text-[12.5px] leading-[1.7] text-ink-3">
              从「整合包市场」装一个包、给世界补服务端文件、下载模组 ——
              后台任务都会出现在这里。跑完的记录会留一阵子，装到哪一步、成没成，随时回来查。
            </p>
          </div>
        </div>
      ) : (
        <>
          {running.length > 0 && (
            <div>
              <SectionHead
                label="进行中"
                n={running.length}
                extra={totalRate > 0 ? `合计 ${fmtSize(totalRate)}/s` : undefined}
              />
              <div className="flex flex-col gap-2.5">
                {running.map((i) => (
                  <RunCard key={i.key} item={i} onOpenInstance={onOpenInstance} onCancel={onCancel} />
                ))}
              </div>
            </div>
          )}

          {past.length > 0 && (
            <div className={running.length > 0 ? 'mt-6' : ''}>
              <SectionHead label="最近结束" n={past.length} />
              <div className="dl-done-list">
                {past.map((i) => (
                  <DoneRow key={i.key} item={i} onOpenInstance={onOpenInstance} onDismiss={onDismiss} />
                ))}
              </div>
            </div>
          )}
        </>
      )}
    </Sheet>
  )
}

/** 段落头：eyebrow（拉丁像素字）+ 计数 + 右侧补充说明（如合计速度）。两段版式必须一致。 */
function SectionHead({ label, n, extra }: { label: string; n: number; extra?: string }) {
  return (
    <div className="mb-2.5 flex items-baseline gap-2">
      <span className="eyebrow">{label}</span>
      <span className="chip s">{n}</span>
      {extra && <span className="step-line ml-auto">{extra}</span>}
    </div>
  )
}

/** 后台作业的图标与名字（进行中卡片 / 结束行共用一份）。 */
const KIND_META = {
  download: { icon: 'dl', label: '下载', color: 'var(--cyan)' },
  install: { icon: 'cube', label: '安装', color: 'var(--copper)' },
  upgrade: { icon: 'refresh', label: '升级', color: 'var(--warn)' },
} as const

/** 终态说明的语气：完成=绿、失败=红、取消=中性。 */
function noteTone(note: string): 'ok' | 'bad' | 'mute' {
  if (!note) return 'mute'
  if (note.endsWith('完成')) return 'ok'
  if (note === '已取消') return 'mute'
  return 'bad'
}

/**
 * 进行中的**作业票**：一件活一张。
 *
 * 四层信息从上往下：谁（名字）+ 什么类型 → 在干嘛（步骤）+ 多快（实时速度）→ 还有多少（进度条 + 字节）。
 * 进度条属于**整张票的底部**，不占右列 —— 这一版不再是四列网格，所以它自然通栏。
 */
function RunCard({
  item,
  onOpenInstance,
  onCancel,
}: {
  item: FeedItem
  onOpenInstance: (id: string) => void
  onCancel: (item: FeedItem) => void
}) {
  const meta = KIND_META[item.kind]
  return (
    <div className="dl-card">
      <div className="flex items-center gap-2.5">
        <span className="ricon">
          <Icon name={meta.icon} size={16} style={{ color: meta.color }} />
        </span>
        {item.instanceId ? (
          <button
            type="button"
            className="min-w-0 truncate text-left text-[13.5px] font-semibold text-ink-1 hover:underline"
            title={item.label}
            onClick={() => onOpenInstance(item.instanceId)}
          >
            {item.label}
          </button>
        ) : (
          <span className="min-w-0 truncate text-[13.5px] font-semibold text-ink-1" title={item.label}>
            {item.label}
          </span>
        )}
        <span className="chip s shrink-0">{meta.label}</span>
        <span className="flex-1" />
        <button type="button" className="btn btn-ghost xs shrink-0" onClick={() => onCancel(item)}>
          <Icon name="x" size={12} />
          取消
        </button>
      </div>

      <div className="mt-2 flex items-baseline gap-3">
        <span className="step-line min-w-0 truncate">
          {item.step}
          {/* 后端在「多线程下载（4 线程）」这类步骤文案里已经点过线程数了，别再说第二遍 */}
          {item.threads > 1 && !item.step.includes('线程') && ` · ${item.threads} 线程`}
          {item.resumed && ' · 续传'}
        </span>
        <span className="flex-1" />
        {/* 速度是「正在动」最硬的证据：青色 + 大一号的终端字，扫一眼就知道还活着 */}
        {item.rate > 0 && <span className="dl-rate">{fmtSize(item.rate)}/s</span>}
      </div>

      <div className="mt-2.5 flex items-center gap-3">
        {/* 总量未知时不假装知道：条子呼吸、不给百分比（满格不动的条子会被读成「卡在 100%」）。 */}
        <div className={`prog min-w-0 flex-1${item.pct === null ? ' indet' : ''}`}>
          <div
            className="fill"
            style={{ width: item.pct === null ? '100%' : `${Math.max(0, Math.min(100, item.pct))}%` }}
          />
          {item.pct !== null && <div className="lb2">{Math.floor(item.pct)}%</div>}
        </div>
        <span className="dl-bytes">
          {item.bytesTotal > 0 ? `${fmtSize(item.bytesDone)} / ${fmtSize(item.bytesTotal)}` : ''}
        </span>
      </div>
    </div>
  )
}

/**
 * 结束行（完成 / 失败 / 取消都在这里）。
 *
 * 紧凑是刻意的：结束的东西不再需要面积，一行说清「谁、成没成、多久前、多大」就够。
 * 长的失败原因（磁盘满了那种）chip 装不下，单独一行红字说完整 —— 塞进 chip 会撑成一坨。
 */
function DoneRow({
  item,
  onOpenInstance,
  onDismiss,
}: {
  item: FeedItem
  onOpenInstance: (id: string) => void
  onDismiss: (item: FeedItem) => void
}) {
  const meta = KIND_META[item.kind]
  const tone = noteTone(item.note)
  const longErr = tone === 'bad' && item.note.length > 8
  const chipText = !item.note ? '' : longErr ? '失败' : item.note
  const chipCls = tone === 'ok' ? ' g' : tone === 'bad' ? ' d' : ''
  const iconColor = tone === 'ok' ? 'var(--ok)' : tone === 'bad' ? 'var(--bad)' : 'var(--ink-3)'
  // 「多久前」：优先用**结束时刻**（下载任务有）；整合包作业后端不给，退回开始时刻。
  const when = item.endedAt || item.startedAt
  const whenTip = item.endedAt ? `结束于 ${fmtTime(item.endedAt)}` : `开始于 ${fmtTime(item.startedAt)}`

  return (
    <div className="dl-done">
      <div className="flex items-center gap-2.5">
        <span className="dl-dicon">
          <Icon name={meta.icon} size={13} style={{ color: iconColor }} />
        </span>
        {item.instanceId ? (
          <button
            type="button"
            className="min-w-0 truncate text-left text-[13px] font-semibold text-ink-1 hover:underline"
            title={item.label}
            onClick={() => onOpenInstance(item.instanceId)}
          >
            {item.label}
          </button>
        ) : (
          <span className="min-w-0 truncate text-[13px] font-semibold text-ink-1" title={item.label}>
            {item.label}
          </span>
        )}
        {chipText && <span className={`chip s shrink-0${chipCls}`}>{chipText}</span>}
        <span className="flex-1" />
        <span className="dl-done-time" title={whenTip}>
          {fmtAgoMin(when)}
        </span>
        <span className="dl-done-size">{item.bytesTotal > 0 ? fmtSize(item.bytesTotal) : ''}</span>
        <button
          type="button"
          className="btn btn-ghost xs shrink-0"
          onClick={() => onDismiss(item)}
          title="从记录里删掉这一条"
          aria-label={`删除记录：${item.label}`}
        >
          <Icon name="trash" size={12} />
        </button>
      </div>
      {longErr && <div className="dl-done-err">{item.note}</div>}
    </div>
  )
}
