/**
 * 控制台分页 —— 导航栏一级分页（`?page=console`）。
 *
 * ⚠ 版式**照抄设计稿** `docs/design/pixel-refresh/pages/R1-console-whole-page.html`，
 *   不自己发挥：`.grid-console` 两栏（左控制台 + 右「常用指令 / 最近指令」）、
 *   con-head 里的 缓冲行数 + 跟随 / 清空 / 导出、级别筛选带计数、同类报错自动折叠。
 *   用户原话：「控制台那边设计稿上不是有设计吗」——有就照着做。
 *
 * 设计稿是静态演示页，搬进真机时这几处必须换成真的：
 *   ① 「缓冲 512 行」→ 真的 visible.length；
 *   ② 级别计数 → 真的按 lv 统计；
 *   ③ 报错折叠 → 真的把**连续且完全相同**的 ERROR/FATAL 聚成一行（同类 × N），
 *      展开才列出全部 —— 排错时 12 条一模一样的报错刷屏是最烦的；
 *   ④ 常用指令 → 点一下**只填进输入框**，不直接发（发指令是不可逆动作，不能一键盲发）；
 *   ⑤ 最近指令 → 记本地发过的指令。设计稿写的是「list → 3/20 在线」这种**带结果**的文案，
 *      但后端 send 接口不回结果文本，所以只显示「已发送」——**不编造结果**。
 */
import { useMemo, useRef, useState, type CSSProperties } from 'react'
import { Icon, LogLine, type LogEntry } from '../components/design'
import { applyCompletion, complete, type Completion } from '../mcComplete'
import { useStickyScroll } from '../useStickyScroll'
import type { Instance } from '../types'

const FILTERS: { k: 'all' | 'warn' | 'err'; label: string }[] = [
  { k: 'all', label: '全部' },
  { k: 'warn', label: '只看警示' },
  { k: 'err', label: '只看报错' },
]

/** 常用指令：设计稿给了 6 条，图标照设计稿的 sprite 名。 */
const COMMON: { cmd: string; icon: string }[] = [
  { cmd: 'list', icon: 'users' },
  { cmd: 'save-all', icon: 'check' },
  { cmd: 'op Steve', icon: 'crown' },
  { cmd: 'ban Herobrine', icon: 'ban' },
  { cmd: 'time set day', icon: 'clock' },
  { cmd: 'whitelist on', icon: 'lever' },
]

type Block =
  | { kind: 'line'; e: LogEntry }
  | { kind: 'fold'; key: string; e: LogEntry; n: number; items: LogEntry[] }

/** 把「连续且完全相同」的 ERROR/FATAL 聚成一块；其余原样放行。 */
function groupErrors(entries: LogEntry[]): Block[] {
  const out: Block[] = []
  let i = 0
  while (i < entries.length) {
    const e = entries[i]
    /* FATAL 在解析时已经并进 ERROR（LogLevel 只有 INFO/WARN/ERROR/LOG），别再比一次。 */
    const isErr = e.lv === 'ERROR'
    if (isErr) {
      let j = i
      while (j < entries.length && entries[j].lv === e.lv && entries[j].msg === e.msg) j++
      if (j - i >= 2) {
        out.push({ kind: 'fold', key: String(e.id), e, n: j - i, items: entries.slice(i, j) })
        i = j
        continue
      }
    }
    out.push({ kind: 'line', e })
    i++
  }
  return out
}

export function ConsolePage({
  instance,
  logs,
  running,
  live,
  onSend,
}: {
  instance: Instance
  logs: LogEntry[]
  running: boolean
  /** 日志是否实时。带 ?nosse=1 时是 false —— **必须显示出来**，
   *  否则用户会以为「日志坏了」，而其实是我们自己关掉的。 */
  live: boolean
  onSend: (cmd: string) => void
}) {
  const [filter, setFilter] = useState<'all' | 'warn' | 'err'>('all')
  const [cmd, setCmd] = useState('')
  /** 清空是清当前视图，不是删服务端日志文件 —— 记一个下标即可。 */
  const [clearedAt, setClearedAt] = useState(0)
  const [recent, setRecent] = useState<string[]>([])
  const [openFolds, setOpenFolds] = useState<Record<string, boolean>>({})
  /** 命令补全：候选与当前选中项。null = 面板没开 */
  const [comp, setComp] = useState<Completion | null>(null)
  const [sel, setSel] = useState(0)
  const boxRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)

  const visible = clearedAt ? logs.slice(clearedAt) : logs

  const counts = useMemo(
    () => ({
      all: visible.length,
      warn: visible.filter((e) => e.lv === 'WARN').length,
      err: visible.filter((e) => e.lv === 'ERROR').length,
    }),
    [visible],
  )

  const blocks = useMemo(() => groupErrors(visible), [visible])

  // 粘底滚动：跟随中持续贴底；用户往回翻自动让路、滚回底部自动恢复。
  // 逻辑收在 useStickyScroll（与概览小窗共用一份）——那里记着「点跟随只跟一下下」
  // 那个坑的完整拆解：程序贴底自己触发的 scroll 事件绝不能被当成用户上翻。
  const sticky = useStickyScroll(boxRef, blocks)

  const submit = () => {
    const v = cmd.trim()
    if (!v) return
    onSend(v)
    setRecent((r) => [v, ...r].slice(0, 8))
    setCmd('')
    sticky.stick()
    setComp(null)
    inputRef.current?.focus()
  }

  /**
   * 重算候选。光标位置变化也要算 —— 补的永远是**光标所在那一格**，
   * 不是行尾那一格（把光标挪回中间改参数是常事）。
   */
  const refresh = (value: string, caret: number | null, players?: string[]) => {
    if (!running || !value.startsWith('/')) {
      setComp(null)
      return
    }
    const c = complete(value, caret ?? value.length, { players })
    // 候选为空不弹面板：弹一片空的比不弹更烦
    setComp(c && c.items.length > 0 ? c : null)
    if (c && c.items.length > 0) setSel(0)
  }

  /** 采纳第 i 个候选 */
  const take = (i: number, players?: string[]) => {
    if (!comp) return
    const item = comp.items[i]
    if (!item) return
    const next = applyCompletion(cmd, comp, item.text)
    setCmd(next.line)
    // 光标得等 React 把新 value 写进去之后再设，否则会被打回原位
    requestAnimationFrame(() => {
      const el = inputRef.current
      if (el) {
        el.setSelectionRange(next.caret, next.caret)
        el.focus()
      }
    })
    // 补完立刻重算：Tab 连按要能继续往下补
    const after = complete(next.line, next.caret, { players })
    setComp(after && after.items.length > 0 ? after : null)
    setSel(0)
  }

  /** 在线玩家名 —— 补全 <玩家> 参数用。拿不到就只给 @a/@p/@r/@s/@e，不编名字。 */
  const onlineNames = useMemo(() => {
    const set = new Set<string>()
    for (const line of visible) {
      const m = /:\s*([A-Za-z0-9_]{3,16})\s+(joined|left) the game/.exec(line.raw ?? line.msg ?? '')
      if (m && /joined/.test(m[0])) set.add(m[1])
      if (m && /left/.test(m[0])) set.delete(m[1])
    }
    return [...set]
  }, [visible])

  /** 导出：把当前视图存成 .log。设计稿有「导出」按钮，就得真能导。 */
  const exportLog = () => {
    const text = visible.map((e) => e.raw ?? `${e.t} ${e.lv} ${e.msg}`).join('\n')
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${instance.name}-console.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  const filterCls = filter === 'warn' ? ' only-warn' : filter === 'err' ? ' only-err' : ''
  const liveText = !live ? '非实时' : running ? 'LIVE · 实时' : 'OFFLINE · 已停'

  return (
    <div className="grid-console">
      <div className="panel reveal" style={{ '--rd': '.12s' } as CSSProperties}>
        <i className="corner"></i>
        <div className="con-head tex-stone">
          <Icon name="cmd" style={{ color: 'var(--cyan)' }} />
          <h2>控制台</h2>
          <span className="con-live">
            <span className={running ? 'pulse-dot' : 'pulse-dot d'}></span>
            {liveText}
          </span>
          <div className="spacer"></div>
          <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
            缓冲 {counts.all} 行
          </span>
          {/* 激活态就是「正在跟随」的指示灯：以前按钮不带状态，跟没跟全靠猜 ——
              这也是「点了跟随但不知道它还在不在」那类困惑的一半。 */}
          <button
            type="button"
            className={`btn xs${sticky.following ? ' btn-primary' : ''}`}
            aria-pressed={sticky.following}
            onClick={sticky.stick}
            title={sticky.following ? '正在跟随最新一行（往回翻日志会自动暂停）' : '回到最新一行，并继续跟随'}
          >
            跟随
          </button>
          <button
            type="button"
            className="btn btn-ghost xs"
            onClick={() => setClearedAt(logs.length)}
            title="清空当前视图（服务端的日志文件不动）"
          >
            清空
          </button>
          <button type="button" className="btn btn-ghost xs" onClick={exportLog} title="把当前视图存成 .log">
            <Icon name="dl" size={12} />
            导出
          </button>
        </div>

        {/* 级别筛选：设计稿放在 con-head 下面单独一条 toolbar，计数是真的 */}
        <div className="toolbar" style={{ gap: 8 }}>
          <div className="con-filter">
            {FILTERS.map((f) => (
              <button
                key={f.k}
                type="button"
                className={`chipf${filter === f.k ? ' on' : ''}`}
                aria-pressed={filter === f.k}
                onClick={() => setFilter(f.k)}
              >
                {f.label}
                <span className="n">{f.k === 'all' ? counts.all : f.k === 'warn' ? counts.warn : counts.err}</span>
              </button>
            ))}
          </div>
          <div className="spacer" style={{ marginLeft: 'auto' }}></div>
          <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
            同类报错自动折叠 · 点「展开」看全部
          </span>
        </div>

        <div
          ref={boxRef}
          role="log"
          aria-live="polite"
          className={`con-body${filterCls}`}
          /* overflowAnchor：禁掉浏览器的滚动锚定 —— 它会在内容变化时擅自挪 scrollTop，
             把粘底判定「用户没动过」的前提打破。 */
          style={{ minHeight: 470, maxHeight: 'calc(100vh - 330px)', overflowAnchor: 'none' }}
          {...sticky.scrollProps}
        >
          {blocks.length === 0 && (
            <p style={{ padding: '10px 12px', fontSize: 12.5, color: 'var(--ink-3)' }}>
              这一屏还没有日志。服务器启动后会实时往上滚；没在跑的时候，这里只留上次退出前的输出。
            </p>
          )}
          {blocks.map((b) =>
            b.kind === 'line' ? (
              <LogLine key={b.e.id} entry={b.e} />
            ) : (
              <div key={b.key}>
                <div
                  className="log-line fold"
                  role="button"
                  tabIndex={0}
                  onClick={() => setOpenFolds((o) => ({ ...o, [b.key]: !o[b.key] }))}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' || e.key === ' ') setOpenFolds((o) => ({ ...o, [b.key]: !o[b.key] }))
                  }}
                >
                  <span className="foldh">
                    <Icon name="ban" size={12} />
                    <span>{b.e.msg}</span>
                    <span className="cnt">同类 × {b.n}</span>
                    <span className="exp">{openFolds[b.key] ? '收起 ▴' : '展开 ▾'}</span>
                  </span>
                </div>
                {openFolds[b.key] && b.items.map((e) => <LogLine key={e.id} entry={e} />)}
              </div>
            ),
          )}
        </div>

        {/* ⚠ position:relative 是给补全面板定位用的（面板贴在输入行上方） */}
        <div className="con-input" style={{ position: 'relative' }}>
          <span className="ps1">{instance.name} &gt;</span>
          <input
            ref={inputRef}
            value={cmd}
            placeholder={
              running ? '输入指令后回车 · Tab 补全 · ↑↓ 选择' : '实例未运行，启动后才能发指令'
            }
            aria-label="服务器指令"
            aria-autocomplete="list"
            aria-expanded={Boolean(comp)}
            disabled={!running}
            onChange={(e) => {
              setCmd(e.target.value)
              refresh(e.target.value, e.target.selectionStart, onlineNames)
            }}
            onClick={(e) => refresh(cmd, e.currentTarget.selectionStart, onlineNames)}
            onKeyUp={(e) => {
              // 纯挪光标不触发 onChange，得自己补算一次
              const k = e.key
              if (k === 'ArrowLeft' || k === 'ArrowRight' || k === 'Home' || k === 'End') {
                refresh(cmd, e.currentTarget.selectionStart, onlineNames)
              }
            }}
            onBlur={() => {
              // 延迟收面板：不然候选的 onMouseDown 还没跑，面板就先没了
              window.setTimeout(() => setComp(null), 120)
            }}
            onKeyDown={(e) => {
              if (comp) {
                if (e.key === 'ArrowDown') {
                  e.preventDefault()
                  setSel((i) => (i + 1) % comp.items.length)
                  return
                }
                if (e.key === 'ArrowUp') {
                  e.preventDefault()
                  setSel((i) => (i - 1 + comp.items.length) % comp.items.length)
                  return
                }
                if (e.key === 'Tab' || e.key === 'Enter') {
                  // 面板开着时 Tab/回车都是「采纳」，不是「发送」——
                  // 半截命令直接发出去是事故
                  e.preventDefault()
                  take(sel, onlineNames)
                  return
                }
                if (e.key === 'Escape') {
                  e.preventDefault()
                  setComp(null)
                  return
                }
              } else if (e.key === 'Tab') {
                e.preventDefault()
                refresh(cmd, e.currentTarget.selectionStart, onlineNames)
                return
              }
              if (e.key === 'Enter') submit()
            }}
          />
          <span className="caret"></span>
          <button type="button" className="btn btn-primary sm" disabled={!running} onClick={submit}>
            发送
          </button>

          {/* 候选面板：贴输入行上方弹出。硬边框 + 零圆角，和整体一套语言；
              选中行走钻石青（与页签 .tab.on 同色）。 */}
          {comp && (
            <div
              role="listbox"
              aria-label="命令补全"
              className="scroll-thin"
              style={{
                position: 'absolute',
                left: 0,
                right: 0,
                bottom: '100%',
                marginBottom: 4,
                maxHeight: 252,
                overflowY: 'auto',
                zIndex: 30,
                background: 'var(--sf-1)',
                border: '2px solid var(--bg-0)',
                borderTopColor: 'var(--line-2)',
                borderLeftColor: 'var(--line-2)',
              }}
            >
              {comp.items.slice(0, 60).map((it, i) => {
                const on = i === sel
                return (
                  <button
                    key={`${it.text}-${i}`}
                    type="button"
                    role="option"
                    aria-selected={on}
                    onMouseEnter={() => setSel(i)}
                    onMouseDown={(e) => {
                      e.preventDefault()
                      take(i, onlineNames)
                    }}
                    style={{
                      display: 'flex',
                      alignItems: 'baseline',
                      gap: 10,
                      width: '100%',
                      padding: '5px 10px',
                      textAlign: 'left',
                      border: 0,
                      cursor: 'pointer',
                      background: on ? 'var(--cyan)' : 'transparent',
                      color: on ? '#062825' : 'var(--ink-2)',
                      font: 'inherit',
                    }}
                  >
                    <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, flex: 'none' }}>
                      {it.label ?? it.text}
                    </span>
                    {it.hint && (
                      <span
                        style={{
                          fontSize: 11.5,
                          minWidth: 0,
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                          color: on ? '#0a3330' : 'var(--ink-3)',
                        }}
                      >
                        {it.hint}
                      </span>
                    )}
                    {it.kind === 'plugin' && (
                      <span
                        style={{
                          marginLeft: 'auto',
                          fontSize: 10.5,
                          flex: 'none',
                          color: on ? '#0a3330' : 'var(--ink-3)',
                        }}
                      >
                        插件
                      </span>
                    )}
                  </button>
                )
              })}
            </div>
          )}
        </div>
      </div>

      {/* 右列：常用指令 / 最近指令（设计稿两块，窄屏会堆到控制台下面） */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        <div className="panel reveal" style={{ '--rd': '.16s', marginBottom: 0 } as CSSProperties}>
          <i className="corner"></i>
          <div className="con-head tex-stone">
            <Icon name="cmd" style={{ color: 'var(--cyan)' }} />
            <h2>常用指令</h2>
          </div>
          <div style={{ padding: '10px 12px', display: 'flex', flexDirection: 'column', gap: 6 }}>
            {COMMON.map((c) => (
              <button
                key={c.cmd}
                type="button"
                className="btn btn-ghost sm"
                style={{ justifyContent: 'flex-start' }}
                /* 只填进输入框，不直接发：发指令是不可逆动作，一键盲发太危险。 */
                onClick={() => {
                  setCmd(c.cmd)
                  inputRef.current?.focus()
                }}
              >
                <Icon name={c.icon} size={12} />
                <span className="mono-meta">{c.cmd}</span>
              </button>
            ))}
          </div>
        </div>

        <div className="panel reveal" style={{ '--rd': '.2s', marginBottom: 0 } as CSSProperties}>
          <i className="corner"></i>
          <div className="con-head tex-stone">
            <Icon name="time" style={{ color: 'var(--ok)' }} />
            <h2>最近指令</h2>
          </div>
          <div style={{ padding: '8px 12px 12px', fontFamily: 'var(--f-term)', fontSize: 15, color: 'var(--ink-2)' }}>
            {recent.length === 0 ? (
              <div style={{ padding: '4px 0', fontSize: 13, color: 'var(--ink-3)' }}>还没发过指令</div>
            ) : (
              recent.map((r, i) => (
                <div
                  key={i}
                  style={{
                    padding: '4px 0',
                    borderBottom: i < recent.length - 1 ? '1px solid var(--line-1)' : undefined,
                  }}
                >
                  {r} → 已发送
                </div>
              ))
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
