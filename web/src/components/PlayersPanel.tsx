import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, type PlayerProfile, type PlayerRoster } from '../api'
import type { Instance } from '../types'
import { Dialog, Empty, Field, SectionHead, Sheet, SkeletonList, ToggleRow, fmtAgo } from './ui'
import LivePanel from './LivePanel'
import {
  IconBlock,
  IconCheck,
  IconCopy,
  IconDownload,
  IconExit,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconShield,
  IconWarn,
} from './Icons'

const OP_LEVELS: { v: number; label: string }[] = [
  { v: 1, label: '1 · 绕过出生点保护' },
  { v: 2, label: '2 · 可用大部分指令' },
  { v: 3, label: '3 · 可管理玩家' },
  { v: 4, label: '4 · 完全权限' },
]

/** 标识图案的取色。直接取项目色板，不引入新颜色。 */
const BADGE_COLORS = [
  '#6fbf44',
  '#33d5cf',
  '#f2c14e',
  '#e8564a',
  '#8b5a2b',
  '#9aa39b',
  '#4c8f2a',
  '#b48ef0',
]

function hash32(s: string): number {
  let h = 2166136261
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return h >>> 0
}

/**
 * 玩家标识图案：UUID 哈希出来的 5×5 像素图，左右镜像。
 *
 * 为什么不是真实皮肤头像：离线服根本拿不到皮肤；联网拉 Crafatar 那类服务在国内
 * 多半超时，超时还得处理占位图。自己画的这个**永远立即可用**，而且同一个人永远是
 * 同一个图案 —— 扫名单时比一串纯文字好认得多。
 *
 * ⚠ title 里写明它不是皮肤，别让人以为头像没加载出来。
 */
function PlayerBadge({ seed, size = 22 }: { seed: string; size?: number }) {
  const h = hash32(seed || '?')
  const fg = BADGE_COLORS[h % BADGE_COLORS.length]
  const cell = size / 5
  const cells: ReactNode[] = []
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 5; col++) {
      // 左右镜像：只取左半 3 列的信息，看着才像「一张脸」而不是随机噪点
      const src = col < 3 ? col : 4 - col
      if (((h >>> (row * 3 + src + 3)) & 1) !== 1) continue
      cells.push(
        <rect
          key={`${row}-${col}`}
          x={col * cell}
          y={row * cell}
          width={cell}
          height={cell}
          fill={fg}
        />,
      )
    }
  }
  return (
    <svg
      className="pbadge"
      width={size}
      height={size}
      viewBox={`0 0 ${size} ${size}`}
      role="img"
      aria-label={`${seed} 的标识图案`}
    >
      <title>由 UUID 生成的标识图案，不是真实皮肤</title>
      {cells}
    </svg>
  )
}

/**
 * 服务端写进名单文件的时间是 Java 的 `yyyy-MM-dd HH:mm:ss Z`（`2026-10-05 12:00:00 +0800`），
 * 不是 ISO 8601。直接丢给 `new Date()` 各浏览器行为不一致，自己拆更稳。
 */
function parseMCTime(s: string): Date | null {
  const m = s.match(/^(\d{4})-(\d{2})-(\d{2}) (\d{2}):(\d{2}):(\d{2}) ([+-])(\d{2})(\d{2})$/)
  if (!m) return null
  const [, y, mo, d, h, mi, se, sign, th, tm] = m
  const offsetMin = (Number(th) * 60 + Number(tm)) * (sign === '-' ? -1 : 1)
  return new Date(Date.UTC(+y, +mo - 1, +d, +h, +mi, +se) - offsetMin * 60_000)
}

/** 说成「3 小时前」。解析不出来就原样返回 —— 绝不显示 Invalid Date。 */
function mcAgo(s: string): string {
  const d = parseMCTime(s)
  return d ? fmtAgo(d.toISOString()) : s
}

/**
 * 说成一个**将来**的时间点（封禁到期）。
 *
 * ⚠ 不能复用 mcAgo：`fmtAgo` 是拿 `now - 时间` 算的，喂它一个未来时间会得到
 * `days < 0` → 一律显示「刚刚」。那会让「7 天后自动解封」看起来像「刚刚解封了」——
 * 正好把意思说反。
 */
function mcUntil(s: string): string {
  const d = parseMCTime(s)
  if (!d) return s
  const ms = d.getTime() - Date.now()
  if (ms <= 0) return '已到期'
  const hours = Math.floor(ms / 3_600_000)
  if (hours < 1) return '不到 1 小时后'
  if (hours < 24) return `${hours} 小时后`
  const days = Math.floor(hours / 24)
  if (days < 30) return `${days} 天后`
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/**
 * 封禁时长。**永久与限时是两个不同的东西**：服务端认的「永久」是字面量 forever，
 * 限时是一个时间串。
 */
type Dur = 'forever' | '1d' | '7d' | '30d' | 'custom'

const DUR_LABEL: Record<Dur, string> = {
  forever: '永久',
  '1d': '1 天',
  '7d': '7 天',
  '30d': '30 天',
  custom: '指定时间',
}

/** 把时长选择换算成后端要的 RFC3339。空串 = 永久。 */
function durToISO(d: Dur, custom: string): string {
  if (d === 'forever') return ''
  if (d === 'custom') {
    if (!custom) return ''
    const t = new Date(custom)
    return Number.isNaN(t.getTime()) ? '' : t.toISOString()
  }
  const days = d === '1d' ? 1 : d === '7d' ? 7 : 30
  return new Date(Date.now() + days * 86_400_000).toISOString()
}

/** 「12 小时 30 分」。tick → 人类时间，换算只在展示层做。 */
function fmtPlaytime(ticks: number): string {
  if (ticks <= 0) return '不到 1 分钟'
  const mins = Math.floor(ticks / 20 / 60)
  if (mins < 60) return `${mins} 分钟`
  const h = Math.floor(mins / 60)
  const m = mins % 60
  return m ? `${h} 小时 ${m} 分` : `${h} 小时`
}

type Filter = 'all' | 'online' | 'whitelist' | 'op' | 'banned'
type Sort = 'online' | 'name' | 'ban'

const PAGE_SIZE = 20

/* 无头浏览器点不了「添加玩家」，也点不开详情抽屉 —— 想核对它们长什么样只能从 URL 进。
   和 ?taskdialog= / ?drawer=1 是同一个套路。 */
const PREVIEW_DLG = new URLSearchParams(location.search).get('playerdlg')
const PREVIEW_OPEN = new URLSearchParams(location.search).get('player') ?? ''

/**
 * 这一页的三个分组。**一次只显示一组** —— 从前三段是平铺在同一个滚动流里的，
 * 空态下「说明文字 + 三处空态 + 两段注意事项」全堆在一屏，看着就是一堵灰字墙。
 * 分组切换的可寻址状态是 `?g=`（与配置页同一套约定），刷新不丢、也能直接截图。
 */
const PGROUPS = [
  { k: 'live', title: '实时状态', desc: '谁在线、在哪、身上有什么' },
  { k: 'roster', title: '玩家名单', desc: '白名单 / 管理员 / 封禁，一个人只有一行' },
  { k: 'ip', title: '封禁的 IP', desc: '封的是网线，不是某个人' },
] as const
type PGroup = (typeof PGROUPS)[number]['k']

function readPGroup(): PGroup {
  const g = new URLSearchParams(location.search).get('g')
  return PGROUPS.some((x) => x.k === g) ? (g as PGroup) : 'live'
}

/** 合成后的一行：一个人一条。白名单 / 管理员 / 封禁是他的三种状态，不是三个名单。 */
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
  /** 到点自动解封的时间（Java 格式串）。'forever' = 永久。 */
  banExpires: string
  /** 累计游玩刻数。undefined = 没有档案（不是 0），界面显示「—」 */
  playTicks?: number
  /** 延迟毫秒。undefined = 不在线或没装 companion 插件 */
  ping?: number
  /** playerdata 的写入时间（RFC3339）。undefined = 没有档案 */
  lastSeen?: string
}

type Props = {
  instance: Pick<Instance, 'id' | 'name'>
  onToast: (msg: string) => void
}

export function PlayersPanel({ instance, onToast }: Props) {
  // 当前分组。默认「实时状态」—— 打开玩家页第一眼要看的是谁在线，不是四份名单文件。
  const [pg, setPg] = useState<PGroup>(readPGroup)
  const [roster, setRoster] = useState<PlayerRoster | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  // 正在操作的对象。只锁自己那一行 —— 「一个忙全禁用」会被读成「点了没反应」。
  const [busy, setBusy] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [sort, setSort] = useState<Sort>('online')
  const [query, setQuery] = useState('')
  const [page, setPage] = useState(1)

  // 详情抽屉。存 key（小写名字）而不是整行对象 —— 名单每 5 秒刷新一次，
  // 存对象的话抽屉里显示的是打开那一刻的快照，用户会看到它慢慢变旧。
  const [open, setOpen] = useState(PREVIEW_OPEN.toLowerCase())
  const [profile, setProfile] = useState<PlayerProfile | null>(null)
  const [profErr, setProfErr] = useState('')
  const [profLoading, setProfLoading] = useState(false)

  // null = 没开；add = 添加玩家；ban = 封禁某人；kick = 踢某人；ip = 封禁 IP
  const [dlg, setDlg] = useState<
    null | { kind: 'add' } | { kind: 'ban'; name: string } | { kind: 'kick'; name: string } | { kind: 'ip' }
  >(
    PREVIEW_DLG === 'add'
      ? { kind: 'add' }
      : PREVIEW_DLG === 'ban'
        ? { kind: 'ban', name: 'Steve' }
        : PREVIEW_DLG === 'kick'
          ? { kind: 'kick', name: 'Alex' }
          : PREVIEW_DLG === 'ip'
            ? { kind: 'ip' }
            : null,
  )
  const [dlgName, setDlgName] = useState('')
  const [dlgWl, setDlgWl] = useState(true)
  const [dlgOp, setDlgOp] = useState(false)
  const [dlgOpLevel, setDlgOpLevel] = useState('4')
  const [dlgReason, setDlgReason] = useState('')
  const [dur, setDur] = useState<Dur>('forever')
  const [durCustom, setDurCustom] = useState('')
  const [ipInput, setIpInput] = useState('')
  const [ipReason, setIpReason] = useState('')
  const [ipDur, setIpDur] = useState<Dur>('forever')
  const [ipDurCustom, setIpDurCustom] = useState('')

  const reload = useCallback(async () => {
    try {
      setRoster(await api.players(instance.id))
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  useEffect(() => {
    setLoading(true)
    setRoster(null)
    reload()
  }, [reload])

  // 在线玩家会变，所以得定期看。5 秒一次比主面板的 3 秒慢些 ——
  // 这份数据变化不频繁，而每次都要读四个 json 文件。
  useEffect(() => {
    const t = setInterval(reload, 5000)
    return () => clearInterval(t)
  }, [reload])

  /**
   * 操作之后的刷新。
   *
   * ⚠ 服务端在跑的时候走的是**命令轨**：命令发出去、服务端处理、再写文件，
   * 中间有个空档。立刻刷新会拿到旧名单，用户看到的就是「点了没反应」。
   * 所以先等一拍再拉。
   */
  const refreshSoon = useCallback(() => {
    setTimeout(reload, 700)
  }, [reload])

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

  const r = roster

  /**
   * 开关详情抽屉，并把状态写回地址栏。
   *
   * 顺便修掉一个真实的体验问题：抽屉开着时刷新页面（或误按返回），
   * 它原来会直接丢掉，用户又得在名单里重新找那一行。放进 URL 之后，
   * 刷新还在、链接能发给别人、后退也有东西可退 —— 和
   * `?page= / ?view= / ?drawer=` 是同一套「可寻址状态」的做法。
   */
  const openPlayer = (key: string) => {
    setOpen(key)
    const u = new URL(location.href)
    if (key) u.searchParams.set('player', key)
    else u.searchParams.delete('player')
    history.replaceState(null, '', u)
  }

  // 合成总表。先铺三份名单（名字以名单文件为准），再并入在线 ——
  // 在线名单是从日志里解析的，只有名字，UUID 可能为空。
  const rows = useMemo<Row[]>(() => {
    if (!r) return []
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
        }
        map.set(k, row)
      } else if (uuid && !row.uuid) {
        row.uuid = uuid
      }
      return row
    }
    for (const e of r.whitelist) put(e.name, e.uuid).whitelisted = true
    for (const o of r.ops) put(o.name, o.uuid).opLevel = o.level
    for (const b of r.banned) {
      const row = put(b.name, b.uuid)
      row.banned = true
      row.banReason = b.reason
      row.banAt = b.created
      row.banForever = b.expires === 'forever'
      row.banExpires = b.expires
    }
    for (const p of r.online) put(p.name, p.uuid).online = true
    // 补时长/延迟。取不到就保持 undefined —— 界面显示「—」，
    // 填 0 会被读成「他玩了 0 秒」，那是在替服务端撒谎。
    for (const row of map.values()) {
      const e = r.extra?.[row.key]
      if (!e) continue
      row.playTicks = e.playTicks
      row.ping = e.ping
      row.lastSeen = e.lastSeen
    }
    return [...map.values()]
  }, [r])

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
    // 在线的排前面（那是现在要关心的），其余按名字排
    out.sort((a, b) => {
      if (sort === 'name') return a.name.localeCompare(b.name)
      if (sort === 'ban') {
        if (a.banned !== b.banned) return a.banned ? -1 : 1
        const ta = parseMCTime(a.banAt)?.getTime() ?? 0
        const tb = parseMCTime(b.banAt)?.getTime() ?? 0
        return tb - ta
      }
      if (a.online !== b.online) return a.online ? -1 : 1
      return a.name.localeCompare(b.name)
    })
    return out
  }, [rows, filter, q, sort])

  const pageCount = Math.max(1, Math.ceil(shown.length / PAGE_SIZE))
  const safePage = Math.min(page, pageCount)
  const pageRows = shown.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE)

  // 换筛选/搜索/排序之后还停在原来那一页，很可能落到空白页上 —— 回到第一页。
  useEffect(() => {
    setPage(1)
  }, [filter, q, sort])

  const current = r && open ? rows.find((x) => x.key === open) ?? null : null

  // 抽屉里那个人的档案。只在「换了人」时拉一次 ——
  // 名单 5 秒一轮询，但档案是磁盘上的历史数据，没必要跟着刷。
  useEffect(() => {
    if (!current) {
      setProfile(null)
      setProfErr('')
      return
    }
    let alive = true
    setProfLoading(true)
    api
      .playerProfile(instance.id, current.name, current.uuid)
      .then((p) => {
        if (alive) {
          setProfile(p)
          setProfErr('')
        }
      })
      .catch((e: Error) => {
        if (alive) {
          setProfile(null)
          setProfErr(e.message)
        }
      })
      .finally(() => {
        if (alive) setProfLoading(false)
      })
    return () => {
      alive = false
    }
    // current?.name 而不是 current：每 5 秒 roster 都会产出新对象，
    // 依赖整个对象会让这个 effect 跟着每 5 秒重跑一次，档案就被反复重拉。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [instance.id, current?.name, current?.uuid])

  // 抽屉开着的时候那个人从名单里消失了（被移出所有名单且离线）—— 关掉它，
  // 别让用户对着一个不存在的人操作。
  useEffect(() => {
    if (open && roster && !rows.some((x) => x.key === open)) openPlayer('')
  }, [open, roster, rows])

  const openAdd = () => {    setDlgName('')
    setDlgWl(true)
    setDlgOp(false)
    setDlgOpLevel('4')
    setDlg({ kind: 'add' })
  }

  const openBan = (name: string) => {
    setDlgReason('')
    setDur('forever')
    setDurCustom('')
    setDlg({ kind: 'ban', name })
  }

  const openKick = (name: string) => {
    setDlgReason('')
    setDlg({ kind: 'kick', name })
  }

  /** 导出当前筛选结果。纯前端 —— 名单本来就在手上，没必要为它加一条接口。 */
  const exportJSON = () => {
    const payload = shown.map((x) => ({
      name: x.name,
      uuid: x.uuid,
      online: x.online,
      whitelisted: x.whitelisted,
      opLevel: x.opLevel,
      banned: x.banned,
      banReason: x.banned ? x.banReason : undefined,
      banCreated: x.banned ? x.banAt : undefined,
      banExpires: x.banned ? x.banExpires : undefined,
    }))
    const blob = new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json' })
    const a = document.createElement('a')
    a.href = URL.createObjectURL(blob)
    a.download = `${instance.name}-玩家-${new Date().toISOString().slice(0, 10)}.json`
    a.click()
    URL.revokeObjectURL(a.href)
    onToast(`已导出 ${payload.length} 条`)
  }

  const copyText = async (text: string, what: string) => {
    if (!text) return
    try {
      await navigator.clipboard.writeText(text)
      onToast(`已复制${what}`)
    } catch {
      // 剪贴板权限被拒（非 https、或浏览器策略）时别静默 —— 让用户知道要手动选
      onToast('浏览器不让写剪贴板，请手动选中复制')
    }
  }

  const submitAdd = async () => {
    const name = dlgName.trim()
    if (!name) return
    if (!dlgWl && !dlgOp) {
      onToast('至少选一个：加进白名单，或者设为管理员')
      return
    }
    const level = Number(dlgOpLevel)
    // 两条命令依次发。前一条失败就停在那句上 —— 报错会显示在页面上，不静默继续。
    if (dlgWl) {
      const ok = await run(
        'add:wl',
        () => api.playerWhitelist(instance.id, name, true),
        `已把 ${name} 加进白名单`,
      )
      if (!ok) return
    }
    if (dlgOp) {
      const ok = await run(
        'add:op',
        () => api.playerOp(instance.id, name, level, true),
        `已把 ${name} 设为 ${level} 级管理员`,
      )
      if (!ok) return
    }
    setDlg(null)
    openPlayer(name.toLowerCase())
  }

  // 名字从参数传进来而不是读 dlg：dlg 是 state，闭包里没法收窄成 ban 那一支。
  const submitBan = async (name: string) => {
    const expiresAt = durToISO(dur, durCustom)
    if (dur === 'custom' && !expiresAt) {
      onToast('请填一个解封时间')
      return
    }
    const ok = await run(
      `ban:${name}`,
      () => api.playerBan(instance.id, name, 'player', dlgReason.trim(), true, expiresAt),
      expiresAt ? `已封禁 ${name} 到 ${expiresAt.slice(0, 10)}` : `已永久封禁 ${name}`,
    )
    if (ok) setDlg(null)
  }

  const submitKick = async (name: string) => {
    const ok = await run(
      `kick:${name}`,
      () => api.playerKick(instance.id, name, dlgReason.trim()),
      `已把 ${name} 踢下线`,
    )
    if (ok) setDlg(null)
  }

  const submitIP = async () => {
    const ip = ipInput.trim()
    if (!ip) return
    const expiresAt = durToISO(ipDur, ipDurCustom)
    if (ipDur === 'custom' && !expiresAt) {
      onToast('请填一个解封时间')
      return
    }
    const ok = await run(
      'ip:add',
      () => api.playerBan(instance.id, ip, 'ip', ipReason.trim(), true, expiresAt),
      `已封禁 IP ${ip}`,
    )
    if (ok) {
      setIpInput('')
      setIpReason('')
      setIpDur('forever')
      setIpDurCustom('')
    }
  }

  const banHint = r?.running
    ? '服务器正在运行 —— 服务端的封禁命令不支持时长，现在只能永久封。要限时请先停服。'
    : '到点之后服务端会自动放行，不需要手动解封。'

  return (
    <div className="stagger">
      {/* 分组导航：**一次只显示一组**。
          从前三段是平铺在同一个滚动流里的 —— 空态下「两段解释 + 三处空态 + 一段注意事项」
          全挤在一屏，看过去就是一堵灰字墙。可寻址状态 ?g=，刷新不丢、也能直接截图。 */}
      <nav className="fnav flex overflow-x-auto" aria-label="玩家分组">
        {PGROUPS.map((g) => (
          <button
            key={g.k}
            type="button"
            aria-current={pg === g.k}
            className={`fitem w-auto shrink-0${pg === g.k ? ' on' : ''}`}
            onClick={() => {
              setPg(g.k)
              const u = new URL(location.href)
              u.searchParams.set('g', g.k)
              history.replaceState(null, '', u)
            }}
          >
            <i className="bar-l" />
            <span className="min-w-0 truncate">{g.title}</span>
            {/* 计数只在有数据时给。拿不到数据还印一个「0」，是在替服务端撒谎。 */}
            {g.k === 'roster' && r && <span className="cnt">{counts.all}</span>}
            {g.k === 'ip' && r && <span className="cnt">{r.bannedIps.length}</span>}
            {g.k === 'live' && r && <span className="cnt">{counts.online}</span>}
          </button>
        ))}
      </nav>
      {/* 每组一句话，替掉从前散在页面上那几段解释性长文 */}
      <p className="mt-3 text-[12.5px] text-ink-3">{PGROUPS.find((x) => x.k === pg)?.desc}</p>

      {pg === 'live' && <LivePanel instance={instance} />}

      {pg === 'roster' && (
      <div className="mt-6">
        <SectionHead
          idx="02"
          title="玩家"
        note={r ? `共 ${counts.all} 人 · ${counts.online} 人在线` : undefined}
        right={
          <>
            <button type="button" className="btn btn-sm btn-ghost" onClick={exportJSON} disabled={!shown.length}>
              <IconDownload size={12} />
              导出
            </button>
            <button type="button" className="btn btn-sm" onClick={() => void reload()}>
              <IconRefresh size={12} />
              刷新
            </button>
            <button type="button" className="btn btn-sm btn-grass" onClick={openAdd}>
              <IconPlus size={12} />
              添加玩家
            </button>
          </>
        }
      />

      {err && (
        <div className="mt-3 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
          {err}
        </div>
      )}

      {/* 白名单没开的时候往里加人，加了也不生效 —— 这句话必须说在前面，
          否则用户加完人、玩家还是进不来，会以为是面板坏了。 */}
      {r && !r.whitelistEnabled && (
        <div className="mt-3 flex items-start gap-2.5 border-l-[3px] border-warn bg-[#1a1812] px-4 py-2.5">
          <span className="mt-[2px] shrink-0 text-warn">
            <IconWarn size={13} />
          </span>
          <p className="text-[12.5px] leading-[1.7] text-[#e7dcb2]">
            白名单现在是<b>关着的</b>（<span className="mono-value">white-list=false</span>）
            —— 加了人也不会拦住别人，任何人都能进服。要真的启用，去「配置」页把「启用白名单」打开。
          </p>
        </div>
      )}

      {/* 筛选 + 排序 + 搜索同一行。分成两行会把名单往下推，一屏扫到的人就少了。 */}
      <div className="mt-4 flex flex-wrap items-center gap-2">
        <button
          type="button"
          className={`fchip tab${filter === 'all' ? ' on' : ''}`}
          onClick={() => setFilter('all')}
        >
          全部 {counts.all}
        </button>
        <button
          type="button"
          className={`fchip tab${filter === 'online' ? ' on' : ''}`}
          onClick={() => setFilter('online')}
        >
          在线 {counts.online}
        </button>
        <button
          type="button"
          className={`fchip tab${filter === 'whitelist' ? ' on' : ''}`}
          onClick={() => setFilter('whitelist')}
        >
          白名单 {counts.whitelist}
        </button>
        <button
          type="button"
          className={`fchip tab${filter === 'op' ? ' on' : ''}`}
          onClick={() => setFilter('op')}
        >
          管理员 {counts.op}
        </button>
        <button
          type="button"
          className={`fchip tab${filter === 'banned' ? ' on' : ''}`}
          onClick={() => setFilter('banned')}
        >
          封禁 {counts.banned}
        </button>

        <select
          className="px-input ml-auto w-[132px] py-1.5 text-[12.5px]"
          value={sort}
          aria-label="排序方式"
          onChange={(e) => setSort(e.target.value as Sort)}
        >
          <option value="online">在线优先</option>
          <option value="name">按名字</option>
          <option value="ban">最近封禁</option>
        </select>

        <div className="search flex w-full items-center gap-2.5 px-3 py-2 sm:w-[220px]">
          <span className="shrink-0 text-ink-3">
            <IconSearch size={13} />
          </span>
          <input
            type="search"
            className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink-1 outline-0 placeholder:text-ink-3"
            placeholder="搜名字或 UUID"
            aria-label="按名字或 UUID 过滤"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </div>
      </div>

      {loading ? (
        <div className="mt-4">
          <SkeletonList rows={3} icon={false} />
        </div>
      ) : shown.length === 0 ? (
        <Empty>
          {counts.all === 0
            ? r?.running
              ? '现在没人在线，名单也还是空的。玩家进服之后会出现在这里；你也可以点「添加玩家」先把人加进白名单。'
              : '服务器没在运行，名单也是空的。可以先点「添加玩家」把人加进白名单，下次开服就生效。'
            : '这个筛选下没有匹配的人。换个标签，或者清空搜索框。'}
        </Empty>
      ) : (
        <>
          <div className="mt-3 border-y border-line">
            {pageRows.map((x) => (
              <div key={x.key} className={`prow${x.banned ? ' off' : ''}`}>
                {/* 点主体开详情。做成按钮而不是给整行挂 onClick —— 整行可点会把
                    右上角那四个动作也包进去，点「封禁」时容易先触发详情。 */}
                <button
                  type="button"
                  className="prow-main prow-open"
                  onClick={() => openPlayer(x.key)}
                  title={`查看 ${x.name} 的详情`}
                >
                  <PlayerBadge seed={x.uuid || x.name} />
                  <div className="prow-id">
                    <div className="prow-name">
                      <span>{x.name}</span>
                      {x.opLevel > 0 && <span className="chip chip-c">管理员 {x.opLevel} 级</span>}
                      {x.banned && (
                        <span className="chip chip-d">
                          {x.banForever ? '永久封禁' : '限时封禁'}
                        </span>
                      )}
                    </div>
                    {/* 第二行只在**被封禁**时出现，放原因和到期时间 —— 那是管理员要的信息。
                        UUID / 「离线服」这类解释放这里是纯噪音：一屏八行重复同一句话，
                        而它对任何人都不构成「现在要做一个决定」的依据。UUID 在详情抽屉里。 */}
                    {x.banned && (
                      <div className="prow-sub">
                        {x.banReason || '未填原因'}
                        {x.banForever
                          ? x.banAt
                            ? ` · ${mcAgo(x.banAt)}`
                            : ''
                          : ` · ${mcUntil(x.banExpires)}自动解封`}
                      </div>
                    )}
                  </div>
                </button>

                {/* 状态独立成列（原来挂在名字后面的 chip，位置逐行浮动、没法竖着扫）。
                    封禁 > 在线 > 离线，**每行都渲染** —— 离线行不渲染的话行高会矮一截，
                    一列扫下来是锯齿。 */}
                <div
                  className={`pstat${x.banned ? ' bad' : x.online ? ' on' : ''}`}
                  title={x.banned ? '已被封禁' : x.online ? '当前在线' : '当前不在线'}
                >
                  <i />
                  {/* 延迟只有 companion 插件给得出。没有就不显示数字 ——
                      原版服务端不暴露玩家级 ping，编一个「0 ms」比不显示更糟。 */}
                  {x.banned
                    ? '已封禁'
                    : x.online
                      ? x.ping
                        ? `在线 ${x.ping}ms`
                        : '在线'
                      : '离线'}
                </div>

                {/* 游戏时长。取不到显示「—」—— 那表示没有档案，不是「玩了 0 秒」。
                    title 里给最后进服时间，那是同一个档案文件带出来的。 */}
                <div
                  className="ptime"
                  title={x.lastSeen ? `最后进服 ${x.lastSeen}` : '没有这个玩家的档案'}
                >
                  {x.playTicks ? fmtPlaytime(x.playTicks) : '—'}
                </div>

                <div className="prow-acts">
                  {/* 白名单：亮 = 已在名单里，点一下移出 */}
                  <button
                    type="button"
                    className={`ibtn wl${x.whitelisted ? ' on' : ''}`}
                    disabled={busy === `wl:${x.name}`}
                    title={x.whitelisted ? `把 ${x.name} 移出白名单` : `把 ${x.name} 加进白名单`}
                    aria-label={
                      x.whitelisted ? `把 ${x.name} 移出白名单` : `把 ${x.name} 加进白名单`
                    }
                    onClick={() =>
                      void run(
                        `wl:${x.name}`,
                        () => api.playerWhitelist(instance.id, x.name, !x.whitelisted),
                        x.whitelisted
                          ? `已把 ${x.name} 移出白名单`
                          : `已把 ${x.name} 加进白名单`,
                      )
                    }
                  >
                    <IconCheck size={13} />
                  </button>

                  {/* 管理员：亮 = 已是管理员，点一下撤销。
                      服务器在跑时 op 命令固定给 4 级 —— 这是服务端限制，title 里说明。 */}
                  <button
                    type="button"
                    className={`ibtn op${x.opLevel > 0 ? ' on' : ''}`}
                    disabled={busy === `op:${x.name}`}
                    title={
                      x.opLevel > 0
                        ? `撤销 ${x.name} 的管理员权限`
                        : r?.running
                          ? `把 ${x.name} 设为管理员（服务器在跑，只能给 4 级）`
                          : `把 ${x.name} 设为 4 级管理员`
                    }
                    aria-label={
                      x.opLevel > 0 ? `撤销 ${x.name} 的管理员权限` : `把 ${x.name} 设为管理员`
                    }
                    onClick={() =>
                      void run(
                        `op:${x.name}`,
                        () => api.playerOp(instance.id, x.name, 4, x.opLevel === 0),
                        x.opLevel > 0
                          ? `已撤销 ${x.name} 的管理员权限`
                          : `已把 ${x.name} 设为管理员`,
                      )
                    }
                  >
                    <IconShield size={13} />
                  </button>

                  {/* 踢出：只把人请下线，他还能再进来。理由是可选的，
                      所以要开对话框而不是点一下直接踢。 */}
                  <button
                    type="button"
                    className="ibtn kick"
                    disabled={!x.online || busy === `kick:${x.name}`}
                    title={x.online ? `把 ${x.name} 踢下线（他还能再进来）` : '他不在线，踢不了'}
                    aria-label={`把 ${x.name} 踢下线`}
                    onClick={() => openKick(x.name)}
                  >
                    <IconExit size={13} />
                  </button>

                  {/* 封禁 / 解封。封禁要填原因和时长，所以走对话框，不直接执行。 */}
                  <button
                    type="button"
                    className={`ibtn${x.banned ? ' bad' : ' ban'}`}
                    disabled={busy === `ban:${x.name}`}
                    title={x.banned ? `给 ${x.name} 解封` : `封禁 ${x.name}`}
                    aria-label={x.banned ? `给 ${x.name} 解封` : `封禁 ${x.name}`}
                    onClick={() =>
                      x.banned
                        ? void run(
                            `ban:${x.name}`,
                            () => api.playerBan(instance.id, x.name, 'player', '', false),
                            `已给 ${x.name} 解封`,
                          )
                        : openBan(x.name)
                    }
                  >
                    <IconBlock size={13} />
                  </button>
                </div>
              </div>
            ))}
          </div>

          {pageCount > 1 && (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={safePage <= 1}
                onClick={() => setPage(safePage - 1)}
              >
                上一页
              </button>
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={safePage >= pageCount}
                onClick={() => setPage(safePage + 1)}
              >
                下一页
              </button>
              <span className="text-[12.5px] text-ink-3">
                第 {safePage} / {pageCount} 页 · 共 {shown.length} 人
              </span>
            </div>
          )}
        </>
      )}

      </div>
      )}

      {/* ── 03 封禁的 IP ──
          单独一块：封 IP 和封玩家不是一回事（封 IP 会误伤同路由器下的人）。
          添加表单收进了对话框 —— 页面上摊一长条输入框，会让「封 IP」看着像个随手能点的动作。 */}
      {pg === 'ip' && (
      <div className="mt-6">
        <SectionHead
          idx="03"
          title="封禁的 IP"
          note={r ? `${r.bannedIps.length} 条` : undefined}
          right={
            <button type="button" className="btn btn-sm btn-danger" onClick={() => setDlg({ kind: 'ip' })}>
              <IconBlock size={12} />
              封禁新 IP
            </button>
          }
        />

        <p className="text-[12.5px] leading-[1.7] text-ink-3">
          封 IP 是把<b className="text-ink-2">整条网线</b>挡在门外 —— 同一个路由器下的人会一起被拒。能封玩家就别封 IP。
        </p>

        {loading || !r ? null : r.bannedIps.length === 0 ? (
          <p className="mt-3 border-y border-line px-3 py-4 text-[12.5px] text-ink-3">
            还没有封禁任何 IP。
          </p>
        ) : (
          <div className="mt-3 border-y border-line">
            {r.bannedIps.map((b) => (
              <div key={b.ip} className="prow">
                <div className="prow-main">
                  <div className="prow-id">
                    <div className="prow-name">
                      <span className="mono-value">{b.ip}</span>
                      <span className={`chip ${b.expires === 'forever' ? 'chip-d' : 'chip-w'}`}>
                        {b.expires === 'forever' ? '永久' : '限时'}
                      </span>
                    </div>
                    <div className="prow-sub">
                      {b.reason || '未填原因'}
                      {b.created ? ` · ${mcAgo(b.created)}` : ''}
                    </div>
                  </div>
                </div>
                <div className="prow-acts">
                  <button
                    type="button"
                    className="ibtn bad"
                    disabled={busy === `ip:del:${b.ip}`}
                    title={`给 ${b.ip} 解封`}
                    aria-label={`给 ${b.ip} 解封`}
                    onClick={() =>
                      void run(
                        `ip:del:${b.ip}`,
                        () => api.playerBan(instance.id, b.ip, 'ip', '', false),
                        `已给 ${b.ip} 解封`,
                      )
                    }
                  >
                    <IconBlock size={13} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>
      )}

      {/* 只在「玩家名单」这组提一句离线服的 UUID。
          从前这里还有一句「名单就是 whitelist.json / ops.json … 我们直接读写它们」——
          那是我们自己怎么实现的，用户不需要知道；而它是页面上最后一堵灰字墙。 */}
      {pg === 'roster' && r && !r.onlineMode && (
        <p className="mt-6 text-[12px] leading-relaxed text-ink-3">
          关掉正版验证时，玩家的 UUID 是按
          <span className="mono-value"> OfflinePlayer:名字 </span>
          算出来的，和正版账号的 UUID 不一样 —— 这是正常现象。
        </p>
      )}

      {/* ── 玩家详情抽屉 ── */}
      {r && current && (
        <Sheet
          title={current.name}
          subtitle={current.uuid || '正版服，UUID 没记下'}
          onClose={() => openPlayer('')}
          footer={
            <>
              <button
                type="button"
                className="btn btn-sm btn-ghost"
                disabled={!current.online || busy === `kick:${current.name}`}
                title={current.online ? undefined : '他不在线，踢不了'}
                onClick={() => openKick(current.name)}
              >
                <IconExit size={12} />
                踢下线
              </button>
              {current.banned ? (
                <button
                  type="button"
                  className="btn btn-sm"
                  disabled={busy === `ban:${current.name}`}
                  onClick={() =>
                    void run(
                      `ban:${current.name}`,
                      () => api.playerBan(instance.id, current.name, 'player', '', false),
                      `已给 ${current.name} 解封`,
                    )
                  }
                >
                  解封
                </button>
              ) : (
                <button
                  type="button"
                  className="btn btn-sm btn-danger"
                  onClick={() => openBan(current.name)}
                >
                  <IconBlock size={12} />
                  封禁
                </button>
              )}
            </>
          }
        >
          <div className="flex items-center gap-3">
            <PlayerBadge seed={current.uuid || current.name} size={44} />
            <div className="flex flex-wrap items-center gap-1.5">
              {current.online ? <span className="chip chip-g">在线</span> : <span className="chip">离线</span>}
              {current.whitelisted && <span className="chip chip-g">白名单</span>}
              {current.opLevel > 0 && <span className="chip chip-c">管理员 {current.opLevel} 级</span>}
              {current.banned && (
                <span className="chip chip-d">
                  {current.banForever ? '永久封禁' : '限时封禁'}
                </span>
              )}
            </div>
          </div>

          {current.banned && (
            <p className="mt-3 border-l-[3px] border-danger bg-[#1c1314] px-3.5 py-2.5 text-[12.5px] leading-[1.7] text-[#ffc2c2]">
              封禁原因：{current.banReason || '未填'}
              {current.banAt ? ` · ${mcAgo(current.banAt)}` : ''}
              {current.banForever
                ? ' · 永久'
                : ` · ${current.banExpires ? `${mcUntil(current.banExpires)}自动解封` : '限时'}`}
            </p>
          )}

          <div className="sgroup">
            <div className="sgroup-head">状态</div>
            <ToggleRow
              on={current.whitelisted}
              disabled={busy === `wl:${current.name}`}
              onChange={(v) =>
                void run(
                  `wl:${current.name}`,
                  () => api.playerWhitelist(instance.id, current.name, v),
                  v ? `已把 ${current.name} 加进白名单` : `已把 ${current.name} 移出白名单`,
                )
              }
              title="白名单"
              desc={
                r.whitelistEnabled
                  ? r.running
                    ? '立刻生效。'
                    : '服务器没在跑，下次启动时生效。'
                  : '白名单现在是关着的 —— 加进来也不拦人，先去「配置」页把它打开。'
              }
            />

            <ToggleRow
              on={current.opLevel > 0}
              disabled={busy === `op:${current.name}`}
              onChange={(v) =>
                void run(
                  `op:${current.name}`,
                  () => api.playerOp(instance.id, current.name, v ? current.opLevel || 4 : 0, v),
                  v ? `已把 ${current.name} 设为管理员` : `已撤销 ${current.name} 的管理员权限`,
                )
              }
              title="管理员"
              desc="能用游戏内的管理指令（ban、tp、gamemode 这些）。"
            />
            {current.opLevel > 0 && (
              <div className="pb-2.5 pl-[52px]">
                {r.running ? (
                  <span className="text-[12px] leading-relaxed text-ink-3">
                    服务器正在运行，只能给「完全权限」（4 级）。要设别的等级请先停服 ——
                    控制台的 <span className="mono-value">op</span> 命令固定给 4 级。
                  </span>
                ) : (
                  <select
                    className="px-input w-full"
                    value={String(current.opLevel)}
                    aria-label="管理员等级"
                    disabled={busy === `op:${current.name}`}
                    onChange={(e) =>
                      void run(
                        `op:${current.name}`,
                        () => api.playerOp(instance.id, current.name, Number(e.target.value), true),
                        `已把 ${current.name} 设为 ${e.target.value} 级管理员`,
                      )
                    }
                  >
                    {OP_LEVELS.map((l) => (
                      <option key={l.v} value={String(l.v)}>
                        {l.label}
                      </option>
                    ))}
                  </select>
                )}
              </div>
            )}
          </div>

          <div className="sgroup">
            <div className="sgroup-head">
              档案
              {profile && !profLoading && <span className="font-normal">来自世界存档</span>}
            </div>
            {profLoading ? (
              <SkeletonList rows={3} icon={false} />
            ) : profErr ? (
              <p className="border-l-[3px] border-danger bg-[#1c1314] px-3.5 py-2.5 text-[12px] leading-relaxed text-[#ffc2c2]">
                读不到档案：{profErr}
              </p>
            ) : !profile ? null : profile.note ? (
              /* 拿不到就得说清是为什么。显示成「0 小时 / 0 次死亡」是在编数据 ——
                 他可能只是还没进过服。 */
              <p className="text-[12px] leading-[1.7] text-ink-3">{profile.note}</p>
            ) : (
              <div>
                <div className="srow">
                  <span className="srow-k">游玩时长</span>
                  <span className="srow-v mono-value">
                    {profile.hasStats ? fmtPlaytime(profile.playTicks) : '没记录'}
                  </span>
                </div>
                <div className="srow">
                  <span className="srow-k">死亡</span>
                  <span className="srow-v mono-value">
                    {profile.hasStats ? `${profile.deaths} 次` : '没记录'}
                  </span>
                </div>
                <div className="srow">
                  <span className="srow-k">击杀玩家 / 生物</span>
                  <span className="srow-v mono-value">
                    {profile.hasStats ? `${profile.playerKills} / ${profile.mobKills}` : '没记录'}
                  </span>
                </div>
                <div className="srow">
                  <span className="srow-k">最后进服</span>
                  <span className="srow-v">{profile.lastSeen ? fmtAgo(profile.lastSeen) : '没记录'}</span>
                </div>
              </div>
            )}
          </div>

          <div className="sgroup">
            <div className="sgroup-head">标识</div>
            <div className="srow">
              <span className="srow-k">UUID</span>
              <span className="srow-v">
                <span className="mono-value" style={{ fontSize: 11.5 }}>
                  {current.uuid || '没记下'}
                </span>
                {current.uuid && (
                  <button
                    type="button"
                    className="ibtn"
                    style={{ width: 24, height: 24 }}
                    title="复制 UUID"
                    aria-label="复制 UUID"
                    onClick={() => void copyText(current.uuid, ' UUID')}
                  >
                    <IconCopy size={12} />
                  </button>
                )}
              </span>
            </div>
            <p className="mt-2 text-[11.5px] leading-relaxed text-ink-3">
              头像方块是按 UUID 哈希出来的图案，不是真实皮肤 —— 离线服拿不到皮肤，
              联网拉头像在国内也多半超时。
            </p>
          </div>
        </Sheet>
      )}

      {/* ── 添加玩家 ── */}
      {dlg?.kind === 'add' && (
        <Dialog
          title="添加玩家"
          onClose={() => setDlg(null)}
          actions={
            <>
              <button type="button" className="btn btn-sm" onClick={() => setDlg(null)}>
                取消
              </button>
              <button
                type="button"
                className="btn btn-sm btn-grass"
                disabled={!dlgName.trim() || busy !== ''}
                onClick={() => void submitAdd()}
              >
                添加
              </button>
            </>
          }
        >
          <label className="field-label" htmlFor="p-add-name">
            玩家名
          </label>
          <input
            id="p-add-name"
            className="px-input px-input-mono w-full"
            value={dlgName}
            placeholder="例如 Steve"
            data-autofocus
            onChange={(e) => setDlgName(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void submitAdd()
            }}
          />
          <p className="field-hint">
            只能是 1-16 位的字母、数字或下划线 —— Java 版不支持中文名。
          </p>

          <div className="mt-5 border-t border-line pt-2">
            <ToggleRow
              on={dlgWl}
              onChange={setDlgWl}
              title="同时加进白名单"
              desc={
                !r?.whitelistEnabled
                  ? '注意：白名单现在是关着的，加了也不拦人 —— 先去「配置」页把它打开。'
                  : r.running
                    ? '服务器在运行，这条改动会立刻生效。'
                    : '服务器没在跑，改动会在下次启动时生效。'
              }
            />
          </div>

          <div className="mt-2 border-t border-line pt-2">
            <ToggleRow
              on={dlgOp}
              onChange={setDlgOp}
              title="同时设为管理员"
              desc="管理员能用游戏内的管理指令（ban、tp、gamemode 这些）。"
            />
            {dlgOp && (
              <select
                className="px-input mt-2 w-full"
                value={dlgOpLevel}
                aria-label="管理员等级"
                onChange={(e) => setDlgOpLevel(e.target.value)}
              >
                {OP_LEVELS.map((l) => (
                  <option key={l.v} value={String(l.v)}>
                    {l.label}
                  </option>
                ))}
              </select>
            )}
            {dlgOp && r?.running && dlgOpLevel !== '4' && (
              <p className="field-hint">
                服务器在运行时只能用控制台的 <span className="mono-value">op</span> 命令，
                它固定给 4 级。要设其他等级，先把服务器停掉再改。
              </p>
            )}
          </div>
        </Dialog>
      )}

      {/* ── 封禁玩家 ── */}
      {dlg?.kind === 'ban' && (
        <Dialog
          title={`封禁 ${dlg.name}`}
          danger
          onClose={() => setDlg(null)}
          actions={
            <>
              <button
                type="button"
                className="btn btn-sm"
                data-autofocus
                onClick={() => setDlg(null)}
              >
                取消
              </button>
              <button
                type="button"
                className="btn btn-sm btn-danger"
                disabled={busy !== ''}
                onClick={() => void submitBan(dlg.name)}
              >
                封禁
              </button>
            </>
          }
        >
          <label className="field-label" htmlFor="p-ban-reason">
            封禁原因（可不填）
          </label>
          <input
            id="p-ban-reason"
            className="px-input w-full"
            value={dlgReason}
            placeholder="例如 破坏建筑"
            onChange={(e) => setDlgReason(e.target.value)}
          />
          <p className="field-hint">
            原因会写进 banned-players.json，被挡的人在游戏里也能看到。解封随时能在列表里点一下。
          </p>

          <div className="mt-5 border-t border-line pt-3">
            <DurationPicker
              value={dur}
              onChange={setDur}
              custom={durCustom}
              onCustom={setDurCustom}
              disabled={!!r?.running}
              hint={banHint}
            />
          </div>
        </Dialog>
      )}

      {/* ── 踢下线 ── */}
      {dlg?.kind === 'kick' && (
        <Dialog
          title={`把 ${dlg.name} 踢下线`}
          onClose={() => setDlg(null)}
          actions={
            <>
              <button
                type="button"
                className="btn btn-sm"
                data-autofocus
                onClick={() => setDlg(null)}
              >
                取消
              </button>
              <button
                type="button"
                className="btn btn-sm"
                disabled={busy !== ''}
                onClick={() => void submitKick(dlg.name)}
              >
                踢下线
              </button>
            </>
          }
        >
          <label className="field-label" htmlFor="p-kick-reason">
            理由（可不填）
          </label>
          <input
            id="p-kick-reason"
            className="px-input w-full"
            value={dlgReason}
            placeholder="例如 服务器要重启"
            onChange={(e) => setDlgReason(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') void submitKick(dlg.name)
            }}
          />
          <p className="field-hint">
            理由会显示在玩家的断线提示里。踢下线不是封禁 —— 他随时能再进来。
          </p>
        </Dialog>
      )}

      {/* ── 封禁 IP ── */}
      {dlg?.kind === 'ip' && (
        <Dialog
          title="封禁 IP"
          danger
          width={520}
          onClose={() => setDlg(null)}
          actions={
            <>
              <button type="button" className="btn btn-sm" data-autofocus onClick={() => setDlg(null)}>
                关闭
              </button>
              <button
                type="button"
                className="btn btn-sm btn-danger"
                disabled={!ipInput.trim() || busy === 'ip:add'}
                onClick={() => void submitIP()}
              >
                <IconBlock size={12} />
                封禁这个 IP
              </button>
            </>
          }
        >
          <Field label="IP 地址" hint="IPv4 或 IPv6。写错了服务端会当成一个无效条目，不会有别的影响。">
            <input
              className="px-input px-input-mono w-full"
              value={ipInput}
              placeholder="例如 203.0.113.7"
              onChange={(e) => setIpInput(e.target.value)}
            />
          </Field>
          <div className="mt-3">
            <Field label="原因（可不填）">
              <input
                className="px-input w-full"
                value={ipReason}
                placeholder="例如 刷屏机器人"
                onChange={(e) => setIpReason(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') void submitIP()
                }}
              />
            </Field>
          </div>
          <div className="mt-3">
            <DurationPicker
              value={ipDur}
              onChange={setIpDur}
              custom={ipDurCustom}
              onCustom={setIpDurCustom}
              disabled={!!r?.running}
              hint={banHint}
            />
          </div>

          <div className="mt-5 border-t border-line pt-3">
            <div className="field-label">已经在封禁名单里的（{r?.bannedIps.length ?? 0}）</div>
            {!r || r.bannedIps.length === 0 ? (
              <p className="text-[12.5px] text-ink-3">还没有封禁任何 IP。</p>
            ) : (
              r.bannedIps.map((b) => (
                <div key={b.ip} className="srow">
                  <span className="srow-k mono-value">{b.ip}</span>
                  <span className="srow-v">
                    <span className="text-[12px] text-ink-3">
                      {b.reason || '未填原因'}
                      {b.created ? ` · ${mcAgo(b.created)}` : ''}
                    </span>
                    <button
                      type="button"
                      className="btn btn-sm btn-ghost"
                      disabled={busy === `ip:del:${b.ip}`}
                      onClick={() =>
                        void run(
                          `ip:del:${b.ip}`,
                          () => api.playerBan(instance.id, b.ip, 'ip', '', false),
                          `已给 ${b.ip} 解封`,
                        )
                      }
                    >
                      解封
                    </button>
                  </span>
                </div>
              ))
            )}
          </div>
        </Dialog>
      )}
    </div>
  )
}

/**
 * 封禁时长选择。
 *
 * ⚠ 服务器在跑的时候整组禁用：服务端的 ban 命令不收时长（`tempban` 是插件给的），
 * 硬选一个「7 天」只会得到一个永久封禁 —— 那是最坏的一种错（用户以为会自己解）。
 * 所以直接把选择权收掉，并在下面写明白为什么。
 */
function DurationPicker({
  value,
  onChange,
  custom,
  onCustom,
  disabled,
  hint,
}: {
  value: Dur
  onChange: (v: Dur) => void
  custom: string
  onCustom: (v: string) => void
  disabled: boolean
  hint: string
}) {
  const kinds: Dur[] = ['forever', '1d', '7d', '30d', 'custom']
  return (
    <div>
      <div className="field-label">封禁到什么时候</div>
      <div className="flex flex-wrap gap-1.5">
        {kinds.map((k) => (
          <button
            key={k}
            type="button"
            className={`fchip tab${value === k ? ' on' : ''}`}
            disabled={disabled && k !== 'forever'}
            style={disabled && k !== 'forever' ? { opacity: 0.4, cursor: 'not-allowed' } : undefined}
            onClick={() => onChange(k)}
          >
            {DUR_LABEL[k]}
          </button>
        ))}
      </div>
      {value === 'custom' && !disabled && (
        <input
          type="datetime-local"
          className="px-input mt-2 w-full"
          value={custom}
          aria-label="解封时间"
          onChange={(e) => onCustom(e.target.value)}
        />
      )}
      <p className="field-hint">{hint}</p>
    </div>
  )
}
