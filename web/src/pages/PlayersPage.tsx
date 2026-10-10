/**
 * 玩家页 —— 结构照设计稿 docs/design/pixel-refresh/pages/02-players.html，
 * **原样搬过来**（class 名、层级、图标 sprite 都不动），把假名单换成真接口。
 *
 * 数据来自两份接口，合并成「一个人一行」：
 *   · GET /players  → 白名单 / OP / 封禁 / 在线（名单文件 + 日志解析）
 *   · GET /live     → 坐标 / 延迟 / 首次加入（服务端里那个只读插件写的快照）
 * 白名单 / 管理员 / 封禁是同一个人身上的三种状态，**不是三段名单**。
 *
 * 合并逻辑与旧 PlayersPanel 完全一致（那边是踩过坑的），只是换了外面这层皮。
 */
import { useCallback, useEffect, useMemo, useState, type CSSProperties } from 'react'
import { api, type PlayerRoster } from '../api'
import { Chk, Chipf, Dlg, Icon, Tog } from '../components/design'
import type { Instance, LivePlayer, LiveSnapshot } from '../types'

const rd = (v: string) => ({ '--rd': v }) as CSSProperties
const PAGE_SIZE = 20
const CROWN: CSSProperties = {
  width: '10px',
  height: '10px',
  display: 'inline-block',
  verticalAlign: '-1px',
}

/** 合成后的一行：一个人一条。 */
type Row = {
  key: string
  name: string
  uuid: string
  online: boolean
  whitelisted: boolean
  opLevel: number
  banned: boolean
  banReason: string
  banAt: string
  banForever: boolean
  banExpires: string
  playTicks?: number
  ping?: number
  lastSeen?: string
  /** —— 以下来自 live 快照，只有服在跑 + 装了伴侣插件时才有 —— */
  world?: string
  x?: number
  y?: number
  z?: number
  firstPlayed?: number
  /** 用名字哈希出来的头像色相，不联网也能一人一个色。 */
  hue: number
}

function hash32(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}
const hueOf = (name: string) => hash32(name.toLowerCase()) % 360

/** tick → 人能读的时长（20 tick = 1 秒）。 */
function fmtPlaytime(ticks: number): string {
  const min = Math.floor(ticks / 20 / 60)
  if (min < 60) return `${min} 分`
  const h = Math.floor(min / 60)
  if (h < 100) return `${h} 时 ${min % 60} 分`
  return `${h} 时`
}

/** RFC3339 → 「刚刚 / N 分钟前 / N 天前」。 */
function ago(iso?: string): string {
  if (!iso) return '—'
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return '—'
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 90) return '刚刚'
  if (s < 3600) return `${Math.round(s / 60)} 分钟前`
  if (s < 86400) return `${Math.round(s / 3600)} 小时前`
  return `${Math.round(s / 86400)} 天前`
}

const fmtDate = (t?: number) =>
  t ? new Date(t).toLocaleDateString('zh-CN') : '—'

type Filter = 'all' | 'online' | 'whitelist' | 'op' | 'banned'

export function PlayersPage({
  instance,
  onToast,
}: {
  instance: Instance
  onToast: (msg: string) => void
}) {
  const [roster, setRoster] = useState<PlayerRoster | null>(null)
  const [live, setLive] = useState<LiveSnapshot | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [sortKey, setSortKey] = useState<'name' | 'status' | 'time'>('name')
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('desc')
  const [touchedSort, setTouchedSort] = useState(false)
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [expanded, setExpanded] = useState<string | null>(null)

  const [dlg, setDlg] = useState<
    null | { kind: 'add' } | { kind: 'ban'; name: string } | { kind: 'kick'; name: string }
  >(null)
  const [dlgName, setDlgName] = useState('')
  const [dlgWl, setDlgWl] = useState(true)
  const [dlgOp, setDlgOp] = useState(false)
  const [reason, setReason] = useState('')
  // IP 封禁单独一份状态：它封的目标是地址、不是人，混进上面那套「按名字」的弹窗只会互相打架
  const [ipDlg, setIpDlg] = useState(false)
  const [ipValue, setIpValue] = useState('')
  const [ipReason, setIpReason] = useState('')

  const reload = useCallback(async () => {
    try {
      // 名单是主数据，读不到就报错；实时快照读不到不算错（服没跑、没装插件都会这样）。
      const r = await api.players(instance.id)
      setRoster(r)
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    }
    try {
      setLive(await api.live(instance.id))
    } catch {
      setLive(null)
    }
  }, [instance.id])

  useEffect(() => {
    setRoster(null)
    setLive(null)
    reload()
    // 在线玩家会变，5 秒看一次就够（每次要读四个 json）
    const t = setInterval(reload, 5000)
    return () => clearInterval(t)
  }, [reload])

  /** 操作之后的刷新：命令轨要等服务端处理完再写文件，所以晚一点再拉一次。 */
  const refreshSoon = useCallback(() => setTimeout(reload, 700), [reload])

  const run = async (key: string, fn: () => Promise<unknown>, okMsg: string): Promise<boolean> => {
    setBusy(key)
    try {
      await fn()
      onToast(okMsg)
      refreshSoon()
      return true
    } catch (e) {
      onToast((e as Error).message)
      return false
    } finally {
      setBusy('')
    }
  }

  // ── 合成总表 ──
  const rows = useMemo<Row[]>(() => {
    if (!roster) return []
    const map = new Map<string, Row>()
    const put = (name: string, uuid: string): Row => {
      const k = name.toLowerCase()
      let row = map.get(k)
      if (!row) {
        row = {
          key: k,
          name,
          uuid,
          online: false,
          whitelisted: false,
          opLevel: 0,
          banned: false,
          banReason: '',
          banAt: '',
          banForever: true,
          banExpires: '',
          hue: hueOf(name),
        }
        map.set(k, row)
      } else if (uuid && !row.uuid) {
        row.uuid = uuid
      }
      return row
    }
    for (const e of roster.whitelist) put(e.name, e.uuid).whitelisted = true
    for (const o of roster.ops) put(o.name, o.uuid).opLevel = o.level
    for (const b of roster.banned) {
      const row = put(b.name, b.uuid)
      row.banned = true
      row.banReason = b.reason
      row.banAt = b.created
      row.banForever = b.expires === 'forever'
      row.banExpires = b.expires
    }
    for (const p of roster.online) put(p.name, p.uuid).online = true

    // 补时长/延迟/最后在线。取不到就保持 undefined —— 界面显示「—」，
    // 填 0 会被读成「他玩了 0 秒」，那是在替服务端撒谎。
    for (const row of map.values()) {
      const e = roster.extra?.[row.key]
      if (e) {
        row.playTicks = e.playTicks
        row.ping = e.ping
        row.lastSeen = e.lastSeen
      }
    }

    // 并入实时快照：坐标 / 延迟 / 首次加入。没装插件时这段什么也不做。
    const patch = (p: LivePlayer) => {
      const row = map.get(p.name.toLowerCase())
      if (!row) return
      row.world = p.world
      row.x = p.x
      row.y = p.y
      row.z = p.z
      if (p.ping != null) row.ping = p.ping
      if (p.firstPlayed) row.firstPlayed = p.firstPlayed
    }
    if (live?.have) {
      live.online.forEach(patch)
      live.offline.forEach(patch)
    }
    return [...map.values()]
  }, [roster, live])

  const counts = useMemo(
    () => ({
      all: rows.length,
      online: rows.filter((x) => x.online).length,
      whitelist: rows.filter((x) => x.whitelisted).length,
      op: rows.filter((x) => x.opLevel > 0).length,
      banned: rows.filter((x) => x.banned).length,
    }),
    [rows],
  )

  const q = query.trim().toLowerCase()
  const shown = useMemo(() => {
    const out = rows.filter((x) => {
      if (filter === 'online' && !x.online) return false
      if (filter === 'whitelist' && !x.whitelisted) return false
      if (filter === 'op' && x.opLevel <= 0) return false
      if (filter === 'banned' && !x.banned) return false
      if (!q) return true
      return x.name.toLowerCase().includes(q) || x.uuid.toLowerCase().includes(q)
    })
    // 没点过表头时保持「在线优先、其余按名字」——那是这个页面最该先看到的顺序。
    out.sort((a, b) => {
      if (!touchedSort) {
        if (a.online !== b.online) return a.online ? -1 : 1
        return a.name.localeCompare(b.name, 'zh')
      }
      let cmp: number
      if (sortKey === 'name') cmp = a.name.localeCompare(b.name, 'zh')
      else if (sortKey === 'status') cmp = Number(a.online) - Number(b.online)
      else cmp = (a.playTicks ?? -1) - (b.playTicks ?? -1)
      return sortDir === 'asc' ? cmp : -cmp
    })
    return out
  }, [rows, filter, q, sortKey, sortDir, touchedSort])

  const pageCount = Math.max(1, Math.ceil(shown.length / PAGE_SIZE))
  const safePage = Math.min(page, pageCount)
  const pageRows = shown.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE)

  useEffect(() => {
    setPage(1)
  }, [filter, q, sortKey, sortDir])

  const allOn = pageRows.length > 0 && pageRows.every((r) => selected.has(r.key))
  const toggleChk = (key: string) =>
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  const toggleAll = () =>
    setSelected(allOn ? new Set() : new Set(pageRows.map((r) => r.key)))

  const onSort = (k: 'name' | 'status' | 'time') => {
    if (sortKey === k && touchedSort) setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'))
    else {
      setSortKey(k)
      setSortDir('asc')
    }
    setTouchedSort(true)
  }

  const selectedRows = rows.filter((r) => selected.has(r.key))

  const doAdd = async () => {
    const n = dlgName.trim()
    if (!n) {
      onToast('先填个玩家名')
      return
    }
    let ok = true
    if (dlgWl) ok = await run('add', () => api.playerWhitelist(instance.id, n, true), `已把 ${n} 加进白名单`)
    if (ok && dlgOp) await run('add', () => api.playerOp(instance.id, n, 4, true), `已把 ${n} 设为管理员`)
    if (ok) setDlg(null)
  }

  const doBan = async (name: string) => {
    const done = await run(
      'ban:' + name,
      () => api.playerBan(instance.id, name, 'player', reason.trim(), true),
      `已封禁 ${name}`,
    )
    if (done) {
      setDlg(null)
      setReason('')
    }
  }

  const doBanIp = async () => {
    const ip = ipValue.trim()
    if (!ip) {
      onToast('先填一个 IP，例如 203.0.113.7')
      return
    }
    const done = await run('ipban', () => api.playerBan(instance.id, ip, 'ip', ipReason.trim(), true), `已封禁 ${ip}`)
    if (done) {
      setIpDlg(false)
      setIpValue('')
      setIpReason('')
    }
  }

  const doKick = async (name: string) => {
    const done = await run('kick:' + name, () => api.playerKick(instance.id, name, reason.trim()), `已把 ${name} 踢出`)
    if (done) {
      setDlg(null)
      setReason('')
    }
  }

  const batch = (kind: 'wl' | 'kick' | 'ban') => {
    const list = selectedRows
    if (!list.length) return
    if (kind === 'wl') {
      void run(
        'batch',
        () => Promise.all(list.map((r) => api.playerWhitelist(instance.id, r.name, true))),
        `已把 ${list.length} 人加进白名单`,
      )
    } else if (kind === 'kick') {
      const online = list.filter((r) => r.online)
      if (!online.length) {
        onToast('选中的这些人一个都不在线，没法踢')
        return
      }
      void run(
        'batch',
        () => Promise.all(online.map((r) => api.playerKick(instance.id, r.name, ''))),
        `已踢出 ${online.length} 人`,
      )
    } else {
      void run(
        'batch',
        () => Promise.all(list.map((r) => api.playerBan(instance.id, r.name, 'player', '', true))),
        `已封禁 ${list.length} 人`,
      )
    }
    setSelected(new Set())
  }

  const ipBans = roster?.bannedIps ?? []
  const liveMissing = roster?.running && !live?.have

  return (
    <section className="page on" data-page="players">
      <div className="sec-head reveal" style={rd('.08s')}>
        <span className="idx">02</span>
        <h2>玩家</h2>
        <span className="sub">
          {roster
            ? `共 ${counts.all} 人 · ${counts.online} 人在线 · 勾选后可批量操作`
            : '正在读名单…'}
        </span>
        <div className="spacer"></div>
        <button
          className="btn sm"
          type="button"
          disabled={!shown.length}
          onClick={() => {
            const blob = new Blob([JSON.stringify(roster, null, 2)], { type: 'application/json' })
            const a = document.createElement('a')
            a.href = URL.createObjectURL(blob)
            a.download = `${instance.name}-玩家名单.json`
            a.click()
            URL.revokeObjectURL(a.href)
          }}
        >
          <Icon name="dl" />
          导出
        </button>
        <button className="btn sm" type="button" onClick={() => void reload()}>
          <Icon name="refresh" />
          刷新
        </button>
        <button className="btn btn-primary sm" type="button" onClick={() => { setDlgName(''); setDlg({ kind: 'add' }) }}>
          <Icon name="plus" />
          添加玩家
        </button>
      </div>

      {err && (
        <div className="banner-danger reveal" style={rd('.1s')}>
          <Icon name="ban" size={20} style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>名单读不出来</h3>
            <p>{err}</p>
          </div>
          <button className="btn sm" type="button" onClick={() => void reload()}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      )}

      {/* 白名单没开时往里加人，加了也不生效 —— 这句话必须说在前面，
          否则用户加完人、玩家还是进不来，会以为是面板坏了。 */}
      {roster && !roster.whitelistEnabled && (
        <div className="banner-danger reveal" style={rd('.1s')} data-tone="warn">
          <Icon name="info" size={20} style={{ color: 'var(--warn)' }} />
          <div className="t">
            <h3>白名单现在是关着的</h3>
            <p>
              <span className="mono">white-list=false</span> —— 加了人也不会拦住别人，任何人都能进服。
              要真的启用，去「配置」页把「启用白名单」打开。
            </p>
          </div>
        </div>
      )}

      <div className="panel reveal" style={rd('.14s')}>
        <i className="corner"></i>

        {/* 批量操作条：勾选后才出现 */}
        <div className={`batchbar${selected.size > 0 ? ' show' : ''}`} id="batchbar">
          <span>已选 <b id="batchN">{selected.size}</b> 项</span>
          <button className="btn btn-primary sm" type="button" disabled={!!busy} onClick={() => batch('wl')}>
            <Icon name="check" />
            批量加白名单
          </button>
          <button className="btn sm" type="button" disabled={!!busy} onClick={() => batch('kick')}>
            <Icon name="kick" />
            批量踢出
          </button>
          <button className="btn btn-danger sm" type="button" disabled={!!busy} onClick={() => batch('ban')}>
            <Icon name="ban" />
            批量封禁
          </button>
          <div className="spacer" style={{ marginLeft: 'auto' }}></div>
          <button className="btn btn-ghost xs" id="batchClear" type="button" onClick={() => setSelected(new Set())}>
            取消选择
          </button>
        </div>

        <div className="toolbar">
          <div className="filters">
            <Chipf on={filter === 'all'} onClick={() => setFilter('all')}>
              全部<span className="n">{counts.all}</span>
            </Chipf>
            <Chipf on={filter === 'online'} onClick={() => setFilter('online')}>
              在线<span className="n">{counts.online}</span>
            </Chipf>
            <Chipf on={filter === 'whitelist'} onClick={() => setFilter('whitelist')}>
              白名单<span className="n">{counts.whitelist}</span>
            </Chipf>
            <Chipf on={filter === 'op'} onClick={() => setFilter('op')}>
              管理员<span className="n">{counts.op}</span>
            </Chipf>
            <Chipf on={filter === 'banned'} onClick={() => setFilter('banned')}>
              封禁<span className="n">{counts.banned}</span>
            </Chipf>
          </div>
          <div className="spacer" style={{ marginLeft: 'auto' }}></div>
          <div style={{ display: 'flex', alignItems: 'center', gap: '8px', minWidth: '200px' }}>
            <Icon name="search" style={{ color: 'var(--ink-3)' }} />
            <input
              className="inp"
              style={{ padding: '6px 9px' }}
              placeholder="搜名字或 UUID"
              aria-label="按名字或 UUID 过滤"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
        </div>

        {/* 可排序表头 */}
        <div className="phead">
          <Chk on={allOn} onChange={toggleAll} title="全选本页" />
          <span></span>
          {([['name', '玩家'], ['status', '状态'], ['time', '游戏时长']] as const).map(([k, label]) => (
            <button
              key={k}
              type="button"
              data-sort={k}
              className={touchedSort && sortKey === k ? sortDir : undefined}
              onClick={() => onSort(k)}
            >
              {label} <i className="ar"></i>
            </button>
          ))}
          <span></span>
        </div>

        <div className="plist" id="plist">
          {!roster && <div className="statecard"><div className="sk" style={{ width: '78%' }} /></div>}

          {roster && pageRows.length === 0 && (
            <div style={{ padding: '26px 16px', textAlign: 'center', color: 'var(--ink-3)', fontSize: 13 }}>
              {rows.length === 0
                ? '这个实例还没有任何玩家记录 —— 服务端第一次收到玩家数据之后，这里才会有人。'
                : '没有符合当前筛选/搜索的人。'}
            </div>
          )}

          {pageRows.map((p) => {
            const open = expanded === p.key
            const hasPos = p.world != null && p.x != null
            return (
              <div key={p.key} style={{ display: 'contents' }}>
                <div
                  className={`prow${p.banned ? ' off' : ''}`}
                  data-name={p.name}
                  data-status={Number(p.online)}
                  data-time={p.playTicks ?? 0}
                  onClick={() => setExpanded(open ? null : p.key)}
                >
                  <Chk on={selected.has(p.key)} onChange={() => toggleChk(p.key)} />
                  <svg className="pav" viewBox="0 0 16 16" style={{ filter: `hue-rotate(${p.hue}deg)` }} aria-hidden="true">
                    <use href="#i-head" />
                  </svg>
                  <div className="pname">
                    <span className="n">{p.name}</span>
                    {p.opLevel > 0 && (
                      <span className="badge op">
                        <svg className="pic" style={CROWN}><use href="#i-crown" /></svg> OP {p.opLevel}
                      </span>
                    )}
                    {p.whitelisted && <span className="badge wl">白名单</span>}
                    {p.banned && <span className="badge ban">已封禁</span>}
                    <span className="sub">{subOf(p, roster)}</span>
                  </div>
                  <div className="pstat">
                    <span className={`dot${p.online ? ' on' : ''}`}></span>
                    {p.online ? `在线${p.ping != null ? ` · ${p.ping} ms` : ''}` : `离线${p.lastSeen ? ` · 上次 ${ago(p.lastSeen)}` : ''}`}
                  </div>
                  <div className="pcoords">{p.playTicks != null ? `游戏 ${fmtPlaytime(p.playTicks)}` : '—'}</div>
                  <div className="pact" onClick={(e) => e.stopPropagation()}>
                    <button
                      className="btn xs"
                      type="button"
                      title={p.whitelisted ? '移出白名单' : '加入白名单'}
                      disabled={busy === `wl:${p.key}`}
                      onClick={() =>
                        run(
                          `wl:${p.key}`,
                          () => api.playerWhitelist(instance.id, p.name, !p.whitelisted),
                          p.whitelisted ? `已把 ${p.name} 移出白名单` : `已把 ${p.name} 加进白名单`,
                        )
                      }
                    >
                      <Icon name="check" />
                    </button>
                    <button
                      className="btn xs"
                      type="button"
                      title={p.opLevel > 0 ? '取消管理员' : '设为 OP'}
                      disabled={busy === `op:${p.key}`}
                      onClick={() =>
                        run(
                          `op:${p.key}`,
                          () => api.playerOp(instance.id, p.name, p.opLevel > 0 ? 0 : 4, p.opLevel === 0),
                          p.opLevel > 0 ? `已取消 ${p.name} 的管理员` : `已把 ${p.name} 设为管理员`,
                        )
                      }
                    >
                      <Icon name="crown" />
                    </button>
                    <button
                      className="btn xs"
                      type="button"
                      title={p.online ? '踢出' : '不在线，踢不了'}
                      disabled={!p.online || busy === `kick:${p.key}`}
                      onClick={() => {
                        setReason('')
                        setDlg({ kind: 'kick', name: p.name })
                      }}
                    >
                      <Icon name="kick" />
                    </button>
                    <button
                      className="btn btn-danger xs"
                      type="button"
                      title={p.banned ? '解封' : '封禁'}
                      disabled={busy === `ban:${p.key}`}
                      onClick={() => {
                        if (p.banned) {
                          void run(
                            `ban:${p.key}`,
                            () => api.playerBan(instance.id, p.name, 'player', '', false),
                            `已解封 ${p.name}`,
                          )
                        } else {
                          setReason('')
                          setDlg({ kind: 'ban', name: p.name })
                        }
                      }}
                    >
                      <Icon name="ban" />
                    </button>
                  </div>
                </div>
                <div className={`pdetail${open ? ' open' : ''}`}>
                  {p.banned ? (
                    <>
                      <Cell k="封禁原因" v={p.banReason || '（没写原因）'} />
                      <Cell k="封禁时间" v={p.banAt || '—'} />
                      <Cell k="解封时间" v={p.banForever ? '永久（不会自动解封）' : p.banExpires} />
                      <Cell k="累计时长" v={p.playTicks != null ? fmtPlaytime(p.playTicks) : '—'} />
                    </>
                  ) : (
                    <>
                      <Cell k="位置" v={hasPos ? `${p.world} ${p.x}, ${p.y}, ${p.z}` : '—'} />
                      <Cell k="首次加入" v={p.firstPlayed ? fmtDate(p.firstPlayed) : '—'} />
                      <Cell k="累计时长" v={p.playTicks != null ? fmtPlaytime(p.playTicks) : '—'} />
                      <Cell k="最后在线" v={p.online ? '刚刚' : p.lastSeen ? ago(p.lastSeen) : '—'} />
                    </>
                  )}
                </div>
              </div>
            )
          })}
        </div>

        {pageCount > 1 && (
          <div className="toolbar" style={{ borderTop: '2px solid var(--bg-0)' }}>
            <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
              第 {safePage} / {pageCount} 页 · 共 {shown.length} 人
            </span>
            <div className="spacer" style={{ marginLeft: 'auto' }}></div>
            <button className="btn xs" type="button" disabled={safePage <= 1} onClick={() => setPage(safePage - 1)}>
              <Icon name="chev-l" />
              上一页
            </button>
            <button className="btn xs" type="button" disabled={safePage >= pageCount} onClick={() => setPage(safePage + 1)}>
              下一页
              <Icon name="chev-r" />
            </button>
          </div>
        )}

        <div className="con-foot">
          <Icon name="info" style={{ color: 'var(--ink-3)' }} />
          {liveMissing
            ? '坐标和延迟要服务端里那个只读插件才给得出来 —— 现在服在跑但没读到快照，所以位置一列是空的。'
            : '白名单 / 管理员 / 封禁是同一个人身上的三种状态，所以一个人只占一行。合计时长来自世界目录里的统计文件，离线也读得到。'}
        </div>
      </div>

      {/* 封禁的 IP —— 设计稿的玩家页没有这块（它封的是网线，不是人）。
          用设计稿自己的 .rlist/.rrow，跟别的列表保持同一套语言。 */}
      <div className="panel reveal" style={rd('.18s')}>
        <i className="corner"></i>
        <div className="con-head tex-stone">
          <Icon name="ban" style={{ color: 'var(--copper)' }} />
          <h2>封禁的 IP</h2>
          <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
            封的是一根网线，不是一个人 —— 换台设备还能进
          </span>
          <div className="spacer"></div>
          <button
            className="btn btn-danger sm"
            type="button"
            onClick={() => {
              setIpValue('')
              setIpReason('')
              setIpDlg(true)
            }}
            title="把某个 IP 拉黑"
          >
            <Icon name="plus" />
            封禁 IP
          </button>
        </div>
        {ipBans.length === 0 ? (
          <div style={{ padding: '18px 16px', color: 'var(--ink-3)', fontSize: 13 }}>
            没有封禁的 IP。上面那位「批量封禁」封的是玩家名，这里封的是地址。
          </div>
        ) : (
          <div className="rlist">
            {ipBans.map((b) => (
              <div className="rrow" key={b.ip}>
                <div className="ricon">
                  <Icon name="ban" size={18} style={{ color: 'var(--bad)' }} />
                </div>
                <div className="rmain">
                  <div className="rtitle">
                    <span className="mono">{b.ip}</span>
                    <span className="chip s d">{b.expires === 'forever' ? '永久' : `至 ${b.expires}`}</span>
                  </div>
                  <div className="rsub">{b.reason || '（没写原因）'} · {b.created}</div>
                </div>
                <div className="ract">
                  <button
                    className="btn btn-ghost xs"
                    type="button"
                    title="解封这个 IP"
                    disabled={busy === `ip:${b.ip}`}
                    onClick={() =>
                      run(`ip:${b.ip}`, () => api.playerBan(instance.id, b.ip, 'ip', '', false), `已解封 ${b.ip}`)
                    }
                  >
                    <Icon name="trash" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ── 弹窗 ── */}
      {dlg?.kind === 'add' && (
        <Dlg
          title="添加玩家"
          icon="plus"
          width={520}
          onClose={() => setDlg(null)}
          footer={
            <>
              <span style={{ flex: 1 }}></span>
              <button className="btn btn-ghost" type="button" onClick={() => setDlg(null)}>
                取消
              </button>
              <button className="btn btn-primary" type="button" disabled={!!busy} onClick={() => void doAdd()}>
                <Icon name="check" />
                添加
              </button>
            </>
          }
        >
          <label style={{ display: 'block' }}>
            <span style={{ fontSize: 12.5, fontWeight: 600 }}>玩家名</span>
            <input
              className="inp"
              style={{ width: '100%', marginTop: 6 }}
              placeholder="正版名区分大小写，离线服按名字算 UUID"
              value={dlgName}
              onChange={(e) => setDlgName(e.target.value)}
            />
          </label>
          <div style={{ borderTop: '1px solid var(--line-1)', marginTop: 16, paddingTop: 14, display: 'flex', alignItems: 'center', gap: 16 }}>
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 13, fontWeight: 600 }}>加入白名单</div>
              <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 4 }}>
                {roster?.whitelistEnabled ? '白名单开着，加进去才进得来。' : '白名单现在是关的，加了也不生效。'}
              </p>
            </div>
            <Tog on={dlgWl} onChange={setDlgWl} />
          </div>
          <div style={{ borderTop: '1px solid var(--line-1)', marginTop: 14, paddingTop: 14, display: 'flex', alignItems: 'center', gap: 16 }}>
            <div style={{ flex: 1 }}>
              <div style={{ fontSize: 13, fontWeight: 600 }}>设为管理员（OP 4）</div>
              <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 4 }}>OP 能改游戏内的一切，只给自己信得过的人。</p>
            </div>
            <Tog on={dlgOp} onChange={setDlgOp} />
          </div>
        </Dlg>
      )}

      {dlg && dlg.kind !== 'add' && (
        <Dlg
          title={`${dlg.kind === 'ban' ? '封禁' : '踢出'}「${dlg.name}」`}
          icon={dlg.kind === 'ban' ? 'ban' : 'kick'}
          iconColor="var(--bad)"
          width={520}
          onClose={() => setDlg(null)}
          footer={
            <>
              <span style={{ flex: 1 }}></span>
              <button className="btn btn-ghost" type="button" onClick={() => setDlg(null)}>
                取消
              </button>
              <button
                className={`btn ${dlg.kind === 'ban' ? 'btn-danger' : 'btn-primary'}`}
                type="button"
                disabled={!!busy}
                onClick={() => {
                  if (dlg.kind === 'ban') void doBan(dlg.name)
                  else void doKick(dlg.name)
                }}
              >
                <Icon name={dlg.kind === 'ban' ? 'ban' : 'kick'} />
                {dlg.kind === 'ban' ? '封禁' : '踢出'}
              </button>
            </>
          }
        >
          <label style={{ display: 'block' }}>
            <span style={{ fontSize: 12.5, fontWeight: 600 }}>
              {dlg.kind === 'ban' ? '封禁理由' : '踢出理由'}（可留空）
            </span>
            <input
              className="inp"
              style={{ width: '100%', marginTop: 6 }}
              placeholder={dlg.kind === 'ban' ? '例如：破坏他人建筑' : '例如：服务器要维护了'}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
            />
          </label>
          {dlg.kind === 'ban' && (
            <p style={{ fontSize: 12, color: 'var(--warn)', marginTop: 12, lineHeight: 1.7 }}>
              <Icon name="info" size={12} style={{ marginRight: 6, verticalAlign: -1 }} />
              {roster?.running
                ? '服务器正在运行 —— 服务端的封禁命令不支持时长，现在只能永久封。要限时就先停服。'
                : '停服状态下可以设时长，到点服务端会自动放行。'}
            </p>
          )}
        </Dlg>
      )}

      {ipDlg && (
        <Dlg
          title="封禁一个 IP"
          icon="ban"
          iconColor="var(--bad)"
          width={520}
          onClose={() => setIpDlg(false)}
          footer={
            <>
              <span style={{ flex: 1 }}></span>
              <button className="btn btn-ghost" type="button" onClick={() => setIpDlg(false)}>
                取消
              </button>
              <button className="btn btn-danger" type="button" disabled={!!busy} onClick={() => void doBanIp()}>
                <Icon name="ban" />
                封禁
              </button>
            </>
          }
        >
          <div className="field">
            <div>
              <div className="lb">要封的 IP</div>
              <div className="hint">格式 1.2.3.4。封 IP 挡的是设备，不是账号 —— 换台机器还能进</div>
            </div>
            <input
              className="inp"
              placeholder="例如 203.0.113.7"
              value={ipValue}
              onChange={(e) => setIpValue(e.target.value)}
            />
          </div>
          <label style={{ display: 'block', marginTop: 14 }}>
            <span style={{ fontSize: 12.5, fontWeight: 600 }}>理由（可留空）</span>
            <input
              className="inp"
              style={{ width: '100%', marginTop: 6 }}
              placeholder="例如：同一个地址一直在刷登录"
              value={ipReason}
              onChange={(e) => setIpReason(e.target.value)}
            />
          </label>
        </Dlg>
      )}
    </section>
  )
}

/** 一个明细格：`.pdetail .cell`。 */
function Cell({ k, v }: { k: string; v: string }) {
  return (
    <div className="cell">
      <div className="k">{k}</div>
      <div className="v">{v}</div>
    </div>
  )
}

/** 行里那行小字：封禁信息 / 坐标 / UUID 兜底。 */
function subOf(p: Row, roster: PlayerRoster | null): string {
  if (p.banned) return p.banForever ? `${p.banAt || '已封禁'} 起 · 永久封禁` : `${p.banAt || ''} 起 · 至 ${p.banExpires}`
  if (p.world != null && p.x != null) return `${p.world} · ${p.x}, ${p.y}, ${p.z}`
  if (roster && !roster.onlineMode) return '离线模式 · UUID 按名字算'
  if (p.uuid) return `UUID ${p.uuid.slice(0, 8)}…`
  return p.whitelisted ? '白名单成员' : '尚未加入白名单'
}
