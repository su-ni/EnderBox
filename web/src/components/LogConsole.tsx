import { useEffect, useRef, useState } from 'react'
import { IconTerminal } from './Icons'
import type { Instance } from '../types'

const LEVEL_COLOR: Record<string, string> = {
  INFO: '#4aedd9',
  WARN: '#f2c14e',
  ERROR: '#ff6b6b',
  FATAL: '#ff6b6b',
}

/** 从服务端原始 stdout 行里剥出时间 / 级别 / 正文。剥不出来就整行当正文，不猜。 */
function parseLine(raw: string) {
  const t = raw.match(/^\[(\d{2}:\d{2}:\d{2})/)
  const lv = raw.match(/\/(INFO|WARN|WARNING|ERROR|FATAL)\]/) ?? raw.match(/\[(INFO|WARN|WARNING|ERROR|FATAL)\]/)
  let msg = raw.replace(/^\[\d{2}:\d{2}:\d{2}\]\s*/, '')
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
