import { useEffect, useRef, useState } from 'react'
import { IconTerminal } from './Icons'
import type { Instance } from '../types'

const LEVEL_COLOR: Record<string, string> = {
  INFO: '#4aedd9',
  WARN: '#f2c14e',
  ERROR: '#ff6b6b',
  FATAL: '#ff6b6b',
}

/**
 * 剥掉服务端的 ANSI 控制序列。
 *
 * 后端摄入时已经剥过一遍（见 internal/daemon/process.go 的 stripANSI），这里是兜底：
 * 修好之前落盘的日志文件里已经存着真实的 ESC 字节（实测某实例日志含 1180 个），
 * 老日志翻出来照样得能看。只对含 ESC 的行做正则，正常行零开销。
 */
const ANSI_RE = /\u001b\[[0-9;?]*[ -/]*[@-~]|\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)|\u001b[@-_]/g
function stripANSI(s: string) {
  return s.includes('\u001b') ? s.replace(ANSI_RE, '') : s
}

/**
 * 还原「终端上肉眼看到的那一行」。
 *
 * 服务端输出每条日志前会先写提示符 "> "、再用 "\r  \r" 擦掉，而 \r 不换行 ——
 * 按 \n 切开的一行就带着 "> \r  \r" 前缀（实测 1854 个 CR 字节）。\r 之后的内容
 * 才是真正显示在那一行上的，所以要取最后一段。后端摄入时已经清过一遍，
 * 这里兜底是为了修复之前已经落盘的那些脏行。
 */
function normalizeLine(raw: string) {
  const s = stripANSI(raw)
  const i = s.lastIndexOf('\r')
  return i >= 0 ? s.slice(i + 1) : s
}

/** 从服务端原始 stdout 行里剥出时间 / 级别 / 正文。剥不出来就整行当正文，不猜。 */
function parseLine(raw: string) {
  const line = normalizeLine(raw)

  /*
   * 管道 stdout 的形态和日志文件**不一样**，这是真机对比出来的：
   *   文件：[17:59:07] [Server thread/INFO]: Unknown or incomplete command
   *   管道：[15:26:42 WARN]: YOU ARE RUNNING THIS SERVER AS AN ADMINISTRATIVE
   * 管道那路没有日期、没有线程名，时间和级别挤在同一个方括号里。
   * 只按文件的形态写的话，控制台上每条日志的级别都认不出来，全显示成 LOG。
   */
  const compact = line.match(/^\[(\d{2}:\d{2}:\d{2}) (INFO|WARN|WARNING|ERROR|FATAL)\]:\s*/)
  if (compact) {
    return {
      time: compact[1],
      level: compact[2] === 'WARNING' ? 'WARN' : compact[2],
      msg: line.slice(compact[0].length),
    }
  }

  const t = line.match(/^\[(\d{2}:\d{2}:\d{2})/)
  const lv =
    line.match(/\/(INFO|WARN|WARNING|ERROR|FATAL)\]/) ?? line.match(/\[(INFO|WARN|WARNING|ERROR|FATAL)\]/)
  let msg = line.replace(/^\[\d{2}:\d{2}:\d{2}\]\s*/, '')
  msg = msg.replace(/^\[[^\]]*?\/(INFO|WARN|WARNING|ERROR|FATAL)\]:\s*/, '')
  msg = msg.replace(/^\[(INFO|WARN|WARNING|ERROR|FATAL)\]:\s*/, '')
  const level = lv ? (lv[1] === 'WARNING' ? 'WARN' : lv[1]) : ''
  return { time: t ? t[1] : '', level, msg }
}

const NAME_RE = /^([A-Za-z0-9_]{3,16}) (joined the game|lost connection|left the game)/

function Message({ text }: { text: string }) {
  const m = text.match(NAME_RE)
  if (!m) return <span>{text}</span>
  return (
    <span>
      <em>{m[1]}</em>
      {text.slice(m[1].length)}
    </span>
  )
}

type Props = {
  instance?: Instance
  logs: string[]
  running: boolean
  onSend: (cmd: string) => void
  onClear: () => void
}

export function LogConsole({ instance, logs, running, onSend, onClear }: Props) {
  const [follow, setFollow] = useState(true)
  const [cmd, setCmd] = useState('')
  const boxRef = useRef<HTMLDivElement>(null)

  // 跟随：新日志进来就贴底。关掉后用户可以自由往回翻，不被抢滚动条。
  useEffect(() => {
    if (!follow) return
    const el = boxRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [logs, follow])

  const submit = () => {
    const v = cmd.trim()
    if (!v) return
    onSend(v)
    setCmd('')
    setFollow(true)
  }

  return (
    <section className="pxb flex h-[44vh] shrink-0 flex-col bg-[#070908] md:h-auto md:min-h-0 md:flex-1">
      <div className="flex flex-wrap items-center gap-2.5 border-b-[3px] border-black bg-panel-2 px-3.5 py-2.5">
        <IconTerminal size={14} />
        <h2 className="text-[13.5px] font-semibold text-ink-1">控制台</h2>
        <span className="font-mono text-[11px] tracking-[0.14em]" style={{ color: running ? '#7cc24f' : '#6b7769' }}>
          {running ? 'LIVE' : 'OFFLINE'}
        </span>
        <div className="ml-auto flex gap-1.5">
          <button
            type="button"
            onClick={() => setFollow((v) => !v)}
            aria-pressed={follow}
            className={`px-2.5 py-1 font-mono text-[11px] ${follow ? 'bg-accent-deep font-bold text-[#0c1108]' : 'bg-raised text-ink-2'}`}
          >
            跟随
          </button>
          <button
            type="button"
            onClick={onClear}
            className="bg-raised px-2.5 py-1 font-mono text-[11px] text-ink-2 hover:text-ink-1"
          >
            清空
          </button>
        </div>
      </div>

      <div
        ref={boxRef}
        role="log"
        aria-live="polite"
        className="scroll-thin min-h-[220px] flex-1 overflow-y-auto px-4 py-3 font-mono text-[12.5px] leading-[1.85]"
      >
        {logs.length === 0 && (
          <p className="text-ink-3">
            {instance ? '还没有日志 —— 启动这个世界之后，这里会实时往下滚。' : '左侧选一个世界。'}
          </p>
        )}
        {logs.map((raw, i) => {
          const { time, level, msg } = parseLine(raw)
          return (
            <div key={i} className="log-line">
              <span className="t">{time || '--:--:--'}</span>
              <span className="lv" style={{ color: level ? LEVEL_COLOR[level] : '#6b7769' }}>
                {level || 'LOG'}
              </span>
              <span className="m">
                <Message text={msg} />
              </span>
            </div>
          )
        })}
      </div>

      <div className="flex items-center gap-2.5 border-t-[3px] border-black bg-[#0c0f0c] px-4 py-2.5">
        <span className="shrink-0 font-mono text-[13px] text-accent">
          {instance ? `${instance.name.slice(0, 12)} >` : '>'}
        </span>
        <input
          value={cmd}
          onChange={(e) => setCmd(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') submit()
          }}
          disabled={!running}
          placeholder={running ? '输入指令后回车，例如 list / op Steve / whitelist on' : '实例未运行，启动后才能发指令'}
          aria-label="服务器指令"
          className="min-w-0 flex-1 border-0 bg-transparent font-mono text-[13px] text-ink-1 outline-none placeholder:text-[#414a42] disabled:cursor-not-allowed"
        />
        <span className="cursor-block shrink-0" aria-hidden="true" />
      </div>
    </section>
  )
}
