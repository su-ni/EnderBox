/**
 * 概览页 —— 结构照设计稿 docs/design/pixel-refresh/pages/01-overview.html，
 * **原样搬过来**（class 名、层级、图标 sprite 都不动），把假数据换成真接口数据。
 *
 * 三块，从上到下：
 *   ① 启动前体检（.preflight）—— 六项检查，通过几项画几颗红心
 *   ② 指标带（.metrics）—— CPU / 内存 / 玩家 / 运行时长，各带方块刻度条 + 60 点历史曲线
 *   ③ 控制台预览 + 右侧「服务器档案 / 快捷操作」
 *
 * ⚠ 图标走 index.html 里的精灵：<use href="#i-xxx"/>，不改回自己画的图标组件。
 */
import { useMemo, useRef, useState, type CSSProperties } from 'react'
import type { Preflight, PreflightIssue } from '../api'
import { Hearts, Icon, LogBody, Meter, Num, Spark, type LogEntry } from '../components/design'
import { formatMB, formatUptime, type Instance, type Metrics } from '../types'

/** --rd 是入场延迟（错开入场）。设计稿是错开出现的，一起入场就不像了。 */
const rd = (v: string) => ({ '--rd': v }) as CSSProperties

/** 心跳小圆点，概览里出现好几次，抽出来免得漏写宽高。 */
const DOT = { width: '6px', height: '6px' } as CSSProperties

/** 体检的六项 —— 与后端 preflight 的 issue code 一一对应，不多不少。 */
const CHECKS: { key: string; label: string; codes: string[] }[] = [
  { key: 'jar', label: '服务端文件', codes: ['jar_missing'] },
  { key: 'eula', label: 'EULA', codes: ['eula_pending'] },
  { key: 'java', label: 'Java', codes: ['java_too_old', 'java_broken', 'java_missing'] },
  { key: 'port', label: '端口', codes: ['port_busy'] },
  { key: 'portsync', label: '端口一致性', codes: ['port_mismatch'] },
  { key: 'mem', label: '内存', codes: ['mem_over'] },
]

const basename = (p: string) => (p ? (p.split(/[\\/]/).pop() ?? p) : '—')

/** 秒 → H:MM:SS（设计稿里 UPTIME 那颗大数字就是这个形态）。 */
function clock(sec: number): string {
  if (!sec || sec <= 0) return '0:00:00'
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  const s = Math.floor(sec % 60)
  return `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`
}

type Props = {
  instance: Instance
  metrics: Metrics | null
  histCpu: number[]
  histMem: number[]
  histPlayers: number[]
  peakCpu: number
  peakMem: number
  preflight: Preflight | null
  fixing: string
  onFix: (code: string) => void
  onRerunPreflight: () => void
  logs: LogEntry[]
  onSend: (cmd: string) => void
  /** 快捷操作里的跳转（立即备份 / 玩家列表）。 */
  onGoto: (page: string) => void
}

export function OverviewPage({
  instance,
  metrics,
  histCpu,
  histMem,
  histPlayers,
  peakCpu,
  peakMem,
  preflight,
  fixing,
  onFix,
  onRerunPreflight,
  logs,
  onSend,
  onGoto,
}: Props) {
  const running = instance.status === 'running'

  // ── ① 体检 ──
  const check = useMemo(() => {
    if (!preflight) return null
    const hit = (codes: string[]) => preflight.issues.find((i) => codes.includes(i.code))
    const results = CHECKS.map((c) => {
      // Java 这一项除了看 issue，还要看实测主版本 —— 用户显式填的路径和后端解析的结果都算数
      if (c.key === 'java') {
        const issue = hit(c.codes)
        const ok = !issue && preflight.javaMajor > 0 && preflight.javaMajor >= preflight.needJava
        return { ...c, ok, issue }
      }
      if (c.key === 'jar') {
        const issue = hit(c.codes)
        return { ...c, ok: preflight.jarFound && !issue, issue }
      }
      const issue = hit(c.codes)
      return { ...c, ok: !issue, issue }
    })
    const failed = results.filter((r) => !r.ok)
    return {
      results,
      passed: results.length - failed.length,
      total: results.length,
      failed,
      fixable: preflight.issues.find((i) => !!i.fix) as PreflightIssue | undefined,
    }
  }, [preflight])

  const passedLabels = check ? check.results.filter((r) => r.ok).map((r) => r.label) : []

  // ── ③ 控制台 ──
  const inputRef = useRef<HTMLInputElement>(null)
  const [clearedAt, setClearedAt] = useState(0)
  const [cmd, setCmd] = useState('')
  const visibleLogs = useMemo(() => logs.slice(clearedAt), [logs, clearedAt])
  // LogBody 靠 entries 变化自动滚底；点「跟随」时把已清空水位复位，等价于「跳回最新」
  const [followKey, setFollowKey] = useState(0)

  const submit = () => {
    const c = cmd.trim()
    if (!c) return
    onSend(c)
    setCmd('')
    inputRef.current?.focus()
  }

  const memPct = metrics && instance.maxMem > 0 ? (metrics.memUsed / instance.maxMem) * 100 : 0
  const cap = Math.max(instance.maxMem, 1)

  return (
    <>
      {/* ═══ ① 启动前体检 ═══ */}
      <div className="panel reveal" style={rd('.12s')}>
        <i className="corner"></i>
        <div className="preflight">
          {check ? (
            <>
              <Hearts score={check.passed} max={check.total} />
              <div className="big">
                <b>{check.passed}</b>
                <span className="of">/ {check.total} 项通过</span>
              </div>
              <div className="txt">
                <h3>启动前体检</h3>
                <p>
                  {passedLabels.length ? `${passedLabels.join('、')} 已就绪。` : ''}
                  {check.failed.length ? (
                    <span style={{ color: 'var(--warn)' }}>
                      未通过：{check.failed.map((f) => f.issue?.title ?? f.label).join('；')}
                    </span>
                  ) : (
                    <span style={{ color: 'var(--ok)' }}>全部通过，可以开服了。</span>
                  )}
                </p>
              </div>
            </>
          ) : (
            <>
              <div className="hearts">
                {Array.from({ length: 6 }, (_, i) => (
                  <svg key={i} className="heart" style={{ color: '#2a333d' }} viewBox="0 0 16 16">
                    <use href="#i-heart" />
                  </svg>
                ))}
              </div>
              <div className="big">
                <b>—</b>
                <span className="of">/ 6 项通过</span>
              </div>
              <div className="txt">
                <h3>启动前体检</h3>
                <p>
                  正在读体检结果…
                  {running && ' 服务器跑着的时候这几项只做只读检查，端口占用会跳过（占着它的就是它自己）。'}
                </p>
              </div>
            </>
          )}
          <button className="btn sm" type="button" onClick={onRerunPreflight} disabled={!!fixing}>
            <Icon name="restart" />
            重新体检
          </button>
          <button
            className="btn btn-primary sm"
            type="button"
            disabled={!!fixing || !check?.fixable}
            onClick={() => check?.fixable?.fix && onFix(check.fixable.fix)}
          >
            <Icon name="check" />
            {fixing ? '修复中…' : '一键修复'}
          </button>
        </div>
      </div>

      {/* ═══ ② 指标带 ═══ */}
      <div className="metrics">
        {/* CPU */}
        <div className="metric tex-diamond reveal" style={rd('.16s')} data-jitter="42">
          <div className="top">
            <Icon name="cpu" style={{ color: 'var(--cyan)' }} />
            <span className="lb">CPU</span>
            <span className="tag">峰值 {Math.round(peakCpu)}%</span>
          </div>
          <div className="val">
            <Num value={Math.round(metrics?.cpu ?? 0)} />
            <span className="u">%</span>
          </div>
          <Meter pct={metrics?.cpu ?? 0} tone="cy" />
          <Spark seed={40} data={histCpu} />
          <div className="sparkcap">
            <span>近 {histCpu.length} 采样</span>
            <span className="pk">峰值 {Math.round(peakCpu)}%</span>
          </div>
          <div className="note">
            {running && <span className="pulse-dot" style={DOT}></span>}
            {running ? `进程 ${metrics?.pid ?? '—'} · 实时占用` : '未运行 · 没有采样'}
          </div>
        </div>

        {/* 内存 */}
        <div className="metric tex-diamond reveal" style={rd('.2s')} data-jitter="2380">
          <div className="top">
            <Icon name="mem" style={{ color: 'var(--copper)' }} />
            <span className="lb">MEMORY</span>
            <span className="tag">峰值 {formatMB(peakMem)}</span>
          </div>
          <div className="val">
            <Num value={Math.round(metrics?.memUsed ?? 0)} />
            <span className="u">MB</span>
            <span className="of">/ {instance.maxMem}</span>
          </div>
          <Meter pct={memPct} tone="on" />
          <Spark seed={58} data={histMem} cls="g" />
          <div className="sparkcap">
            <span>近 {histMem.length} 采样</span>
            <span className="pk">峰值 {formatMB(peakMem)}</span>
          </div>
          <div className="note">上限 {formatMB(cap)} · 超了整台机器会开始换页</div>
        </div>

        {/* 玩家 */}
        <div className="metric tex-diamond reveal" style={rd('.24s')} data-jitter="3">
          <div className="top">
            <Icon name="users" style={{ color: 'var(--ok)' }} />
            <span className="lb">PLAYERS</span>
            <span className="tag">在线</span>
          </div>
          <div className="val">
            <Num value={metrics?.players ?? 0} />
            <span className="u">人</span>
          </div>
          <Meter pct={metrics?.players ? Math.min(100, (metrics.players / 20) * 100) : 0} tone="on" />
          <Spark seed={15} data={histPlayers} cls="y" />
          <div className="sparkcap">
            <span>近 {histPlayers.length} 采样</span>
            <span className="pk">峰值 {histPlayers.length ? Math.max(...histPlayers) : 0} 人</span>
          </div>
          <div className="note">
            {running && <span className="pulse-dot" style={DOT}></span>}
            {running ? '实时在线人数' : '未运行'}
          </div>
        </div>

        {/* 运行时长 */}
        <div className="metric tex-diamond reveal" style={rd('.28s')} data-jitter="0">
          <div className="top">
            <Icon name="time" style={{ color: 'var(--gold)' }} />
            <span className="lb">UPTIME</span>
            <span className="tag">{running ? '运行中' : '未运行'}</span>
          </div>
          <div className="val">
            <b style={{ fontSize: '24px' }}>{running && metrics ? clock(metrics.uptimeSec) : '—'}</b>
          </div>
          <Meter pct={running ? 100 : 0} tone="xp" />
          <Spark seed={80} cls="o" />
          <div className="sparkcap">
            <span>本次运行</span>
            <span className="pk">
              {running && metrics ? formatUptime(metrics.uptimeSec) : '没有采样'}
            </span>
          </div>
          <div className="note">
            {instance.startedAt
              ? `本次启动 ${new Date(instance.startedAt).toLocaleString('zh-CN', { hour12: false })}`
              : '这台实例还没启动过'}
          </div>
        </div>
      </div>

      {/* ═══ ③ 控制台 + 档案 ═══ */}
      <div className="grid-2 reveal" style={rd('.32s')}>
        {/*
          ⚠ `.console` 不能少：它把面板变成 flex column 且 height:100%，
          里面的 .con-body 才会 flex:1 吃掉多余高度、输入行吸在面板底部。
          少了它，面板被右侧列拉高后底部留一整块死空白（实测 98px）——
          用户原话「控制台虚高，背景高度比内容还高」。
        */}
        <div className="panel console" style={{ marginBottom: 0 }}>
          <i className="corner"></i>
          <div className="con-head tex-stone">
            <Icon name="cmd" style={{ color: 'var(--cyan)' }} />
            <h2>控制台</h2>
            <span className="con-live" style={{ opacity: running ? 1 : 0.5 }}>
              <span className={running ? 'pulse-dot' : 'pulse-dot d'}></span>
              {running ? 'LIVE' : '已停'}
            </span>
            <div className="spacer"></div>
            <button
              className="btn xs"
              type="button"
              onClick={() => {
                setClearedAt(0)
                setFollowKey((k) => k + 1)
              }}
              title="回到最新一行"
            >
              跟随
            </button>
            <button
              className="btn btn-ghost xs"
              type="button"
              onClick={() => setClearedAt(logs.length)}
              title="清空当前视图（服务端的日志文件不动）"
            >
              清空
            </button>
            {/* 概览这块只是预览，排错要看整屏的 —— 控制台是独立分页，得有路过去 */}
            <button
              className="btn btn-ghost xs"
              type="button"
              onClick={() => onGoto('console')}
              title="控制台是独立分页，那里是整屏日志"
            >
              整页
            </button>
          </div>
          {/* ⚠ 不给 maxHeight：面板被右侧列拉高后，日志区就该 flex:1 把多余高度吃掉，
              否则多出来的高度全变成面板底部的死空白（实测 64px）—— 正是用户说的「虚高」。
              留 height=236 作最小高度：右列比它矮时控制台也不至于塌成一条。 */}
          <LogBody key={followKey} entries={visibleLogs} height={236} />
          <div className="con-input">
            <span className="ps1">{instance.name} &gt;</span>
            <input
              ref={inputRef}
              value={cmd}
              placeholder="输入指令后回车，例如 list / op Steve / whitelist on"
              aria-label="服务器指令"
              disabled={!running}
              onChange={(e) => setCmd(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') submit()
              }}
            />
            <span className="caret"></span>
          </div>
        </div>

        <div style={{ display: 'flex', flexDirection: 'column', gap: '16px' }}>
          <div className="panel" style={{ marginBottom: 0 }}>
            <i className="corner"></i>
            <div className="con-head tex-stone">
              <Icon name="book" style={{ color: 'var(--cyan)' }} />
              <h2>服务器档案</h2>
            </div>
            <div style={{ padding: '4px 14px 12px' }}>
              <KV k="服务端文件" v={basename(instance.jarPath)} mono />
              <KV k="内存上限" v={`${instance.maxMem} MB`} mono />
              <KV
                k="开机自启"
                v={<span className={`badge ${instance.autoStart ? 'wl' : ''}`}>{instance.autoStart ? '已开启' : '已关闭'}</span>}
              />
              <KV
                k="崩溃重启"
                v={<span className={`badge ${instance.autoRestart ? 'wl' : ''}`}>{instance.autoRestart ? '已开启' : '已关闭'}</span>}
              />
              <KV
                k="Java 路径"
                v={
                  preflight?.javaPath || instance.javaPath
                    ? `${preflight?.javaMajor ? `JDK ${preflight.javaMajor} · ` : ''}${basename(preflight?.javaPath || instance.javaPath || '')}`
                    : `自动 · 需要 Java ${preflight?.needJava ?? '?'}`
                }
                mono
                last
              />
            </div>
          </div>

          <div className="panel" style={{ marginBottom: 0 }}>
            <i className="corner"></i>
            <div className="con-head tex-stone">
              <Icon name="lever" style={{ color: 'var(--copper)' }} />
              <h2>快捷操作</h2>
            </div>
            <div className="quick-grid" style={{ padding: '12px 14px' }}>
              <button
                className="btn sm"
                type="button"
                disabled={!running}
                onClick={() => onSend('save-all')}
              >
                <Icon name="check" />
                保存世界
              </button>
              <button className="btn sm" type="button" onClick={() => onGoto('backup')}>
                <Icon name="chest" />
                立即备份
              </button>
              <button className="btn sm" type="button" onClick={() => onGoto('players')}>
                <Icon name="users" />
                玩家列表
              </button>
              <button
                className="btn sm"
                type="button"
                onClick={() => inputRef.current?.focus()}
              >
                <Icon name="cmd" />
                发送指令
              </button>
            </div>
          </div>
        </div>
      </div>
    </>
  )
}

/** 「服务器档案」里的一行。行距/配色照设计稿的行内样式，抽出来免得重复五遍。 */
function KV({
  k,
  v,
  mono,
  last,
}: {
  k: string
  v: React.ReactNode
  mono?: boolean
  last?: boolean
}) {
  return (
    <div
      className="field"
      style={{
        gridTemplateColumns: '110px 1fr',
        padding: '9px 0',
        borderBottom: last ? 0 : undefined,
      }}
    >
      <div className="lb" style={{ fontWeight: 400, color: 'var(--ink-3)', fontSize: '12.5px' }}>
        {k}
      </div>
      <div style={mono ? { fontFamily: 'var(--f-term)', fontSize: '15px' } : undefined} title={typeof v === 'string' ? v : undefined}>
        {v}
      </div>
    </div>
  )
}
