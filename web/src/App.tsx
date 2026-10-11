import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { api, PreflightBlocked, type AuthStatus, type Preflight } from './api'
import { BackupPanel } from './components/BackupPanel'
import { ConfigPanel } from './components/ConfigPanel'
import { RulesPanel } from './components/RulesPanel'
import { ConnectionBanner } from './components/ConnectionBanner'
import { CrashPanel } from './components/CrashPanel'
import { FileManager } from './components/FileManager'
import { CreateWizard } from './components/CreateWizard'
import { Bubbles, DownloadPanel } from './components/DownloadCenter'
import { useDownloadFeed, type Toast } from './useDownloadFeed'
import { ModpackDetail } from './components/ModpackDetail'
import { ModpackMarket } from './components/ModpackMarket'
import { ErrorBoundary } from './components/ErrorBoundary'
import { stillMotion } from './blockReveal'
import { stoneBuild } from './stoneBuild'
import { OverviewPage } from './pages/OverviewPage'
import { PlayersPage } from './pages/PlayersPage'
import { ConsolePage } from './pages/ConsolePage'
import { ConfirmDialog, ToggleRow } from './components/ui'
import { Icon, parseLogLine, type LogEntry } from './components/design'
import { LoginScreen } from './components/LoginScreen'
import { HudBar } from './components/HudBar'
import { ModpackPanel } from './components/ModpackPanel'
import { PluginsPanel } from './components/PluginsPanel'
import { SettingsPanel } from './components/SettingsPanel'
import { TasksPanel } from './components/TasksPanel'
import { WorldList } from './components/WorldList'
import { STATUS_LABEL, serverLabel, formatUptime, type Instance, type Metrics } from './types'

// 截图/调试开关：带 ?nosse=1 时不建立 SSE 长连接，否则 headless 截图会被长连接卡死
const PARAMS = new URLSearchParams(location.search)
const NOSSE = PARAMS.has('nosse')
// 支持直接打开新建向导 / 直接进某一页（刷新不丢位置，也方便截图核对）
const START_CREATE = PARAMS.get('view') === 'create'
const START_MARKET = PARAMS.get('view') === 'market'
// 手机上抽屉是交互态，无头点不了，只能靠参数预置
const START_DRAWER = PARAMS.has('drawer')
// 「下载与安装」台账同理：藏在一次点击后
const START_DL = PARAMS.has('dl')
// 对话框同理 —— ?dialog=delete&files=1 能连勾选态一起截
const START_DIALOG = PARAMS.get('dialog') ?? ''

/**
 * 页签 —— 前九项与设计稿 index.html 的 `.tabbar` 完全一致（顺序、文案、图标一字不差）：
 * 概览 / 玩家 / 配置 / 插件与模组 / 整合包 / 文件 / 备份与还原 / 计划任务 / 设置。
 * 「控制台」是「概览」页里的一块，**不是**独立页签。
 * 后加的「游戏规则」紧跟「配置」——都是「这个世界的设置」，一个改 server.properties、
 * 一个改运行时的 gamerule；它的图标是后画的（设计稿里没有这一页）。
 *
 * ⚠ 页面 key 沿用本项目的命名（概览那页是 `home`，不是设计稿的 `overview`）——
 *   URL 与既有深链都指着它。
 */
const PAGES = [
  { k: 'home', label: '概览', icon: 'compass' },
  /* 控制台提上来当一级分页：它以前只是概览里的一小块预览（用户原话
     「我们缺少控制台这个分页」「把 tab 移到导航栏，而不是分页内」）。
     紧挨概览 —— 都是「这机器现在怎么样」这一类。 */
  { k: 'console', label: '控制台', icon: 'cmd' },
  { k: 'players', label: '玩家', icon: 'head' },
  { k: 'config', label: '配置', icon: 'book' },
  { k: 'rules', label: '游戏规则', icon: 'rule' },
  { k: 'plugins', label: '插件与模组', icon: 'table' },
  { k: 'modpack', label: '整合包', icon: 'cube' },
  { k: 'files', label: '文件', icon: 'folder' },
  { k: 'backup', label: '备份与还原', icon: 'chest' },
  { k: 'tasks', label: '计划任务', icon: 'clock' },
  { k: 'settings', label: '设置', icon: 'lever' },
] as const
type Page = (typeof PAGES)[number]['k']

function initialPage(): Page {
  const p = PARAMS.get('page')
  return PAGES.some((x) => x.k === p) ? (p as Page) : 'home'
}

const POLL_MS = 3000
/** 曲线铺底向守护进程要多少分钟历史（它保留 12h；1h 够看，一次拉完也够快）。 */
const HIST_MINUTES = 60
/** 曲线最多留多少个采样点：1 小时历史（10s 一点）正好 360 点，取齐。 */
const HISTORY = 360

export default function App() {
  const [instances, setInstances] = useState<Instance[]>([])
  const [activeId, setActiveId] = useState('')
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [histCpu, setHistCpu] = useState<number[]>([])
  const [histMem, setHistMem] = useState<number[]>([])
  const [histPlayers, setHistPlayers] = useState<number[]>([])
  const [online, setOnline] = useState(false)
  /* 有没有问过第一遍。没这个标记的话，页面刚打开那一瞬间会先闪一下「连不上」——
     首屏就报错是最伤信任的形态。 */
  const [probed, setProbed] = useState(false)
  /* 上一次「确认断开」没有。判据不能直接用 online（初值 false，首次成功会被误判成
     「从断开恢复」，于是刚打开面板就弹一句「已重新连上守护进程」，根本没断过）。 */
  const prevDown = useRef(false)
  // 'market' = 整合包市场（列表），'pack' = 某个包的详情页，'create' = 新建向导
  const [view, setView] = useState<'board' | 'create' | 'market' | 'pack'>(
    START_CREATE ? 'create' : PARAMS.get('pk') ? 'pack' : START_MARKET ? 'market' : 'board',
  )
  const [packId, setPackId] = useState(() => PARAMS.get('pk') ?? '')
  const [page, setPage] = useState<Page>(initialPage)
  const [filesPath, setFilesPath] = useState(PARAMS.get('path') ?? '')
  const [drawer, setDrawer] = useState(START_DRAWER)
  /** 气泡通知：**一列不是一条**（下载进度和「世界建好了」会同时发生）。 */
  const [toasts, setToasts] = useState<Toast[]>([])
  const [dlOpen, setDlOpen] = useState(START_DL)
  // null = 还没问出来；问出来之后才知道要不要弹登录界面
  const [auth, setAuth] = useState<AuthStatus | null>(null)
  // 启动前体检：只在实例停下时才刷
  const [preflight, setPreflight] = useState<Preflight | null>(null)
  const [fixing, setFixing] = useState('')
  const [starting, setStarting] = useState(false)
  // 删除确认框（应用内对话框而不是 window.confirm —— 后者没地方放「同时删除世界文件」）
  const [deleteOpen, setDeleteOpen] = useState(START_DIALOG === 'delete')
  const [deleteFiles, setDeleteFiles] = useState(START_DIALOG === 'delete' && PARAMS.has('files'))
  const [killOpen, setKillOpen] = useState(START_DIALOG === 'kill')

  const logSeq = useRef(0)

  /* 转场（方块铺展）要知道「这次切换会变的是哪一块」：
     tabbar + content = 换页；shell = 整个工作区（换视图/进向导/进市场） */
  const tabbarRef = useRef<HTMLElement>(null)
  const contentRef = useRef<HTMLDivElement>(null)
  const shellRef = useRef<HTMLDivElement>(null)

  /* 「承接式」切页（外壳不动、内容上下交接）用的一小套东西：
     glide = 那枚滑行的青色菱形；pageFlow = 正在跑的内容动画（连点时要能一把清掉）。 */
  const glideRef = useRef<HTMLElement>(null)
  const pageFlowRef = useRef<Animation[]>([])
  const pageFlowTimer = useRef(0)
  const glidePrimed = useRef(false)

  /* 换视图（工作区整块交接）的那条链：和 pageFlow 同构，只是动的是视图根、不是 .content。
     分开记账 —— 两条链动的是不同元素，谁也别说谁「还在跑」。 */
  const viewFlowRef = useRef<Animation[]>([])
  const viewFlowTimer = useRef(0)

  const refresh = useCallback(async () => {
    try {
      const data = await api.listInstances()
      const list = data ?? []
      setInstances(list)
      setOnline(true)
      setActiveId((prev) => (list.some((i) => i.id === prev) ? prev : (list[0]?.id ?? '')))
    } catch {
      setOnline(false)
    } finally {
      setProbed(true)
    }
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, POLL_MS)
    return () => clearInterval(t)
  }, [refresh])

  // 断→通的时候说一声。只报「坏了」不报「好了」，用户会一直以为还断着。
  useEffect(() => {
    const down = probed && !online
    const wasDown = prevDown.current
    prevDown.current = down
    if (wasDown && !down) setToast('已重新连上守护进程')
  }, [online, probed])

  // 指标轮询：只采当前选中的实例。曲线先拿后端历史铺底，再叠实时采样 ——
  // 刷新、切实例、隔天再打开，都从「刚才发生过什么」续上，而不是从零攒。
  useEffect(() => {
    setMetrics(null)
    setHistCpu([])
    setHistMem([])
    setHistPlayers([])
    if (!activeId) return

    let alive = true

    // 铺底：一次拉到最近 1 小时的点（10s 一个）。拿不到（旧后端/网络抖）就
    // 退化成原来的「从零攒」—— 这不是错误路径，不弹提示。
    // ⚠ 用 merge 不是 replace：铺底请求回来前的 1-2 个实时点已经进数组了，不能丢；
    //   历史在前、实时在后，顺序天然就是对的。
    api
      .metricsHistory(activeId, HIST_MINUTES)
      .then((h) => {
        if (!alive) return
        const pts = h.points ?? []
        if (!pts.length) return
        setHistCpu((cur) => [...pts.map((p) => p.cpu), ...cur].slice(-HISTORY))
        setHistMem((cur) => [...pts.map((p) => p.mem), ...cur].slice(-HISTORY))
        setHistPlayers((cur) => [...pts.map((p) => p.players), ...cur].slice(-HISTORY))
      })
      .catch(() => {})

    const tick = async () => {
      try {
        const m = await api.metrics(activeId)
        if (!alive) return
        setMetrics(m)
        // 停着的时候不往曲线里灌 0 —— 守护进程采样时就刻意跳过停着的实例
        //（「0 会把曲线按平、把『什么时候真在跑』淹掉」），前端照做：
        // 否则把页面留在停服的实例上十几分钟，之前那段真历史会被 0 一节节冲掉。
        if (!m.running) return
        setHistCpu((h) => [...h, m.cpu].slice(-HISTORY))
        setHistMem((h) => [...h, m.memUsed].slice(-HISTORY))
        setHistPlayers((h) => [...h, m.players].slice(-HISTORY))
      } catch {
        /* 单次采样失败不打扰用户，下个周期重试 */
      }
    }
    tick()
    const t = setInterval(tick, POLL_MS)
    return () => {
      alive = false
      clearInterval(t)
    }
  }, [activeId])

  // 日志流：原始行 → 结构化（时间/级别/正文）一次，交给渲染层
  useEffect(() => {
    if (!activeId) return
    setLogs([])
    // at = 面板收到这一行的时刻，只给**实时**行（历史快照给 0）。
    // 源日志里不带时间戳的行（面板自己的 [EnderBox] 行、Fabric/Forge 的 mod 列表…）
    // 靠它兜底；而拿「打开页面那一刻」当历史行的时间是撒谎，所以历史行一律 0（不显示）。
    // ⚠ forEach(push) 会把 index 当第二个参数传进来（at 收到一个序号），必须包一层。
    const push = (raw: string, at = 0) => {
      const p = parseLogLine(raw)
      setLogs((prev) =>
        prev.length > 3000
          ? [...prev.slice(-2000), { id: ++logSeq.current, raw, ...p, at }]
          : [...prev, { id: ++logSeq.current, raw, ...p, at }],
      )
    }
    if (NOSSE) {
      api.logHistory(activeId).then((ls) => ls.forEach((l) => push(l))).catch(() => {})
      return
    }
    const es = new EventSource(`/api/instances/${activeId}/logs`)
    // 后端用 snapshot-end 事件把「历史快照」和「实时推送」分开：快照行是过去发生的，
    // 拿「现在的时刻」给它们兜底会看到时间倒流（实测 17:26:20 排在 17:05:34 前面），
    // 所以快照一律 at=0。
    let live = false
    es.addEventListener('snapshot-end', () => {
      live = true
    })
    es.onmessage = (e) => push(JSON.parse(e.data) as string, live ? Date.now() : 0)
    return () => es.close()
  }, [activeId])

  const checkAuth = useCallback(async () => {
    try {
      setAuth(await api.authStatus())
    } catch {
      // 问不到状态（守护进程没起来之类）就不要拦着 —— 后面每个请求自己会报错
      setAuth(null)
    }
  }, [])

  useEffect(() => {
    checkAuth()
  }, [checkAuth])

  // 会话过期时 api 层会广播一下，这里重新问一遍状态，界面自然切回登录页
  useEffect(() => {
    const h = () => checkAuth()
    window.addEventListener('sbx-need-auth', h)
    return () => window.removeEventListener('sbx-need-auth', h)
  }, [checkAuth])

  /** 弹一条气泡通知。保留 `(字符串)` 签名：调用点二十多处，该变的是承载它的东西，不是调用方。 */
  const toastSeq = useRef(0)
  const setToast = useCallback((msg: string) => {
    if (!msg) return
    const id = ++toastSeq.current
    setToasts((t) => [...t.slice(-3), { id, text: msg }])
    setTimeout(() => setToasts((t) => t.filter((x) => x.id !== id)), 5000)
  }, [])
  const dismissToast = useCallback((id: number) => {
    setToasts((t) => t.filter((x) => x.id !== id))
  }, [])

  const active = instances.find((i) => i.id === activeId)
  const activeStatus = active?.status

  const runPreflight = useCallback(async () => {
    if (!activeId) return
    try {
      setPreflight(await api.preflight(activeId))
    } catch {
      setPreflight(null)
    }
  }, [activeId])

  // 换实例时先把上一份体检报告清掉，免得短暂显示「别人的」结论
  useEffect(() => {
    setPreflight(null)
  }, [activeId])

  /* 下载/安装结束的信号：触发一次复查 —— 「一键补齐服务端文件」下完之后，
     那条「缺少服务端文件」得自己消失。 */
  const [settleTick, setSettleTick] = useState(0)
  const onDownloadsSettled = useCallback(() => {
    refresh()
    setSettleTick((n) => n + 1)
  }, [refresh])

  // 体检：概览页那块「启动前体检」一直要显示，所以进页面就跑一遍，状态一变
  // （停了/崩了/起来了）再跑一遍。依赖里放 status 而不是整个 active ——
  // 轮询每 3 秒换一个新对象，放进去会变成每 3 秒体检一次。
  // settleTick：一次下载/修复结束后补跑，让「缺少服务端文件」那条自己消失。
  useEffect(() => {
    if (!activeId) return
    runPreflight()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeId, activeStatus, settleTick, runPreflight])

  // 后台在跑什么（下载任务 + 整合包作业）。**挂在最外层**：任务生命周期跟页面无关。
  const feed = useDownloadFeed(instances)
  const runningTasks = feed.items.filter((i) => i.running).length
  useEffect(() => {
    if (feed.settledTick === 0) return
    onDownloadsSettled()
  }, [feed.settledTick, onDownloadsSettled])

  const applyFix = async (code: string) => {
    // 「客户端模组混入」这条没有后端一键修复：要处置的是**整个** mods 目录，
    // 不是崩掉的那一个（Forge 在第一个客户端模组就崩了，禁一个、重启还会崩下一个）。
    // 所以这里不调后端，而是带着「自动核对」跳去插件页。
    if (code === 'audit_client_mods') {
      switchPage(() => {
        setPage('plugins')
        const u = new URL(location.href)
        u.searchParams.set('page', 'plugins')
        u.searchParams.set('audit', '1')
        u.searchParams.delete('view')
        history.replaceState(null, '', u)
        // 气泡跟页面一起到位：提前 250ms 弹出来会显得「说了但没动」
        setToast('已跳到「插件与模组」，正在核对哪些模组服务端装不了')
      })
      return
    }
    setFixing(code)
    try {
      const r = await api.applyFix(activeId, code)
      setPreflight(r.preflight)
      if (r.task) {
        setToast('正在下载服务端文件，进度在右下角的气泡里；下完会自动再体检一次')
      } else {
        setToast(r.preflight.ok ? '修好了，可以开服了' : '这一步处理完了，还有别的要先解决')
      }
    } catch (e) {
      setToast((e as Error).message)
    } finally {
      setFixing('')
    }
  }

  /** 换页转场的统一入口：**承接式**交接 —— 外壳和页签都不动，旧内容下沉淡出、
   *  新内容从上方接进来，页签那枚青色菱形同时滑向新页签。整体读起来像同一条内容带
   *  在往下走，场面不断 —— 就是 PPT「平滑」在没有共同物件时的默认行为。
   *  ⚠ 幕布式的石头方块**不上切页、也不上换视图**（用户点名：太断场）——
   *     它专供「进入加载」（首屏 / 登录通过），见 splash()。
   *  ⚠ 状态只能在 swap 里改 —— 提前改就等于把新页面先亮出来，交接就白做了。
   *  ⚠ 动效关着 / 系统要求减少动效时：瞬时换页，不留空档。
   *  ⚠ 连点时把上一轮动画一把清掉、瞬时换页 —— 宁可少一次动效，不要叠动画。 */
  const switchPage = useCallback((swap: () => void, originEl?: HTMLElement | null) => {
    void originEl // 承接式没有「扩散原点」这一步：动的是内容流，不是覆盖层
    const el = contentRef.current
    if (!el || stillMotion()) {
      swap()
      return
    }
    // 上一轮还在跑：清干净再瞬时换（swap 必须执行，不能吞掉）
    if (pageFlowRef.current.length) {
      pageFlowRef.current.forEach((a) => a.cancel())
      pageFlowRef.current = []
      if (pageFlowTimer.current) {
        window.clearTimeout(pageFlowTimer.current)
        pageFlowTimer.current = 0
      }
      swap()
      return
    }
    // ① 旧内容下沉淡出
    const out = el.animate(
      [
        { opacity: 1, transform: 'translateY(0px)' },
        { opacity: 0, transform: 'translateY(10px)' },
      ],
      { duration: 130, easing: 'cubic-bezier(.4, 0, 1, 1)', fill: 'forwards' },
    )
    pageFlowRef.current = [out]
    pageFlowTimer.current = window.setTimeout(() => {
      pageFlowTimer.current = 0
      swap()
      // ② 新内容从上方接进。⚠ 撤旧动画与起新动画必须在同一帧里，否则会闪一下全亮
      requestAnimationFrame(() => {
        out.cancel()
        const inn = el.animate(
          [
            { opacity: 0, transform: 'translateY(-11px)' },
            { opacity: 1, transform: 'translateY(0px)' },
          ],
          { duration: 200, easing: 'cubic-bezier(.16, .84, .44, 1)' },
        )
        pageFlowRef.current = [inn]
        inn.finished.then(
          () => {
            if (pageFlowRef.current[0] === inn) pageFlowRef.current = []
          },
          () => {},
        )
      })
    }, 130)
  }, [])

  /* 卸载时把两条交接链（换页 / 换视图）的定时器和动画都收干净
     （App 是根组件、几乎不卸载，但别留尾巴） */
  useEffect(
    () => () => {
      if (pageFlowTimer.current) window.clearTimeout(pageFlowTimer.current)
      pageFlowRef.current.forEach((a) => a.cancel())
      if (viewFlowTimer.current) window.clearTimeout(viewFlowTimer.current)
      viewFlowRef.current.forEach((a) => a.cancel())
    },
    [],
  )

  /* 页签指示条：常驻的一枚青色菱形，切页时从旧页签滑到新页签（外壳不动，只有它在动）。
     原生 .tab.on::after 已在 design-overrides.css 里隐去 —— 视觉由它独家负责。
     ⚠ 位置分两条路写：切页走「滑行」（CSS transition），换行/字体/缩放走「瞬时」归位。
     ⚠⚠ ResizeObserver 只能装一次（mount）—— observe() 必定触发一次初始回调，
        若跟着 page 重建，那次回调会在切页当下把滑行过渡砍成瞬移（2026-10-10 实测抓到）。 */
  const placeGlide = useCallback((instant: boolean) => {
    const bar = tabbarRef.current
    const ind = glideRef.current
    if (!bar || !ind) return
    const on = bar.querySelector<HTMLElement>('.tab.on')
    if (!on) {
      // 「设置」页不在这一排：没得指，收起来
      ind.style.opacity = '0'
      return
    }
    if (instant) ind.style.transition = 'none'
    ind.style.opacity = '1'
    ind.style.left = on.offsetLeft + on.offsetWidth / 2 + 'px'
    ind.style.top = on.offsetTop + on.offsetHeight + 9 + 'px'
    if (instant) {
      void ind.offsetWidth // 强制落到实处再恢复过渡，否则「瞬时」也会滑
      ind.style.transition = ''
    }
  }, [])

  useLayoutEffect(() => {
    const bar = tabbarRef.current
    if (!bar) return
    const ro = new ResizeObserver(() => placeGlide(true))
    ro.observe(bar)
    return () => ro.disconnect()
  }, [placeGlide])

  useLayoutEffect(() => {
    // 首次挂载瞬时归位（别从左上角滑进来）；此后每次切页滑行
    placeGlide(!glidePrimed.current)
    glidePrimed.current = true
  }, [page, placeGlide])

  /** 「石头拼合」加载幕（用户 2026-10-10 拍板：它是「进入加载」的动画，不是切换动画）——
   *  ① 首屏：App 一挂载就铺上，auth 查询 / 实例列表都在幕布后面跑，散场即揭幕；
   *  ② 登录通过：闸门放行的一刻再铺一次，盖住「放行之后补拉数据」的那段空窗。
   *  ⚠ 放不放的条件收在这里：自动化环境（探针；headless 里 navigator.webdriver=true）不放 ——
   *     探针要看的是页面本身、不是幕布。想验幕布：在 CDP 注入里把 webdriver 探针盖掉再跑，
   *     见 tools/probe_splash.mjs。关动效（no-anim / 系统偏好）由 stoneBuild 自己短路。 */
  const splash = useCallback(() => {
    if (navigator.webdriver) return
    stoneBuild({ cover: true, swap: () => {} }) // 底下界面从第一帧就在，揭幕不靠 swap
  }, [])
  // 首屏铺幕。放 effect 里是必须的（stoneBuild 要摸 DOM）；StrictMode 的开发态双挂载
  // 由 stoneBuild 的代次机制收掉（新一次让旧的整条作废，不会出现两层幕）。
  useEffect(() => {
    splash()
  }, [splash])

  /** 换整个工作区（新建向导 / 整合包市场 / 包详情 ↔ 面板）：和切页同一套「承接式」语言 ——
   *  侧栏与外壳都不动，工作区这一块整体淡出、新工作区接着淡入。
   *  ⚠ 只做 opacity、**不做位移**：这里的根节点同时是滚动容器（.main / PageFrame），
   *     容器上挂 transform 会生成 containing block，把内部 sticky 层搞崩（PageFrame 记过账）。
   *  ⚠ 石头拼合从这里撤下了（用户点名：那是「进入加载」的动画）—— 见 splash()。
   *  ⚠ 同 switchPage：状态只能在 swap 里改；连点把上一轮清掉、瞬时换。 */
  const switchView = useCallback((swap: () => void, originEl?: HTMLElement | null) => {
    void originEl // 承接式没有「扩散原点」这一步
    const el = shellRef.current?.lastElementChild as HTMLElement | null
    if (!el || stillMotion()) {
      swap()
      return
    }
    // 上一轮还在跑：清干净再瞬时换（swap 必须执行，不能吞掉）
    if (viewFlowRef.current.length) {
      viewFlowRef.current.forEach((a) => a.cancel())
      viewFlowRef.current = []
      if (viewFlowTimer.current) {
        window.clearTimeout(viewFlowTimer.current)
        viewFlowTimer.current = 0
      }
      swap()
      return
    }
    // ① 旧工作区淡出
    const out = el.animate([{ opacity: 1 }, { opacity: 0 }], {
      duration: 130,
      easing: 'cubic-bezier(.4, 0, 1, 1)',
      fill: 'forwards',
    })
    viewFlowRef.current = [out]
    viewFlowTimer.current = window.setTimeout(() => {
      viewFlowTimer.current = 0
      const old = el
      swap()
      // ② 新工作区接进来。
      // ⚠ React 18 的提交不保证落在下一次 rAF 之前 ——「隔一帧就抓新视图根」是在赌提交时机。
      //    改成 rAF 轮询到视图根真换人了再起淡入：提交来得早或晚，第一笔绘制都带 inn 首帧。
      // ⚠ 视图根 = shell 的最后一个子元素（结构判据，别按类名找 —— 比如向导的根是外层
      //    .scroll-thin，.wz-shell 在它里面）。
      let tries = 0
      const waitIn = () => {
        const el2 = shellRef.current?.lastElementChild as HTMLElement | null
        if (!el2) {
          viewFlowRef.current = []
          return
        }
        if (el2 === old && ++tries < 12) {
          requestAnimationFrame(waitIn)
          return
        }
        // 撤旧链与起新链必须在同一帧里，否则会闪一下全亮；新视图自带的入场动画
        //（PageFrame 的 .anim-fade 等）先撤掉再起淡入，避免交叠。
        out.cancel()
        el2.getAnimations().forEach((a) => a.cancel())
        const inn = el2.animate([{ opacity: 0 }, { opacity: 1 }], {
          duration: 200,
          easing: 'cubic-bezier(.16, .84, .44, 1)',
        })
        viewFlowRef.current = [inn]
        inn.finished.then(
          () => {
            if (viewFlowRef.current[0] === inn) viewFlowRef.current = []
          },
          () => {},
        )
      }
      requestAnimationFrame(waitIn)
    }, 130)
  }, [])

  const goto = (p: Page, originEl?: HTMLElement | null) => {
    if (p === page) return
    switchPage(() => {
      setPage(p)
      const u = new URL(location.href)
      if (p === 'home') u.searchParams.delete('page')
      else u.searchParams.set('page', p)
      // 目录只属于「文件」页，别的页带着它没有意义；回到「文件」页时补上上次的位置
      if (p === 'files' && filesPath) u.searchParams.set('path', filesPath)
      else u.searchParams.delete('path')
      u.searchParams.delete('view')
      history.replaceState(null, '', u)
    }, originEl)
  }

  /** 换视图（新建向导 / 市场 / 详情 / 回面板）都从这走。
   *  ⚠ 已经在目标视图里就整个省掉：setView 同值 React 会 bail，但切换动画照样空跑一遍
   *     （点侧栏里的世界名 = goView('board')，人明明就站在 board —— 白闪一下不能忍）。 */
  const goView = useCallback(
    (v: 'board' | 'create' | 'market' | 'pack', originEl?: HTMLElement | null) => {
      if (v === view) return
      switchView(() => setView(v), originEl)
    },
    [switchView, view],
  )

  /** 从别处跳进「文件」页并定位到某个目录（模组页的「打开文件夹」用的就是它） */
  const openFiles = useCallback(
    (p: string, originEl?: HTMLElement | null) => {
      switchPage(() => {
        setFilesPath(p)
        setPage('files')
        const u = new URL(location.href)
        u.searchParams.set('page', 'files')
        if (p) u.searchParams.set('path', p)
        else u.searchParams.delete('path')
        u.searchParams.delete('view')
        history.replaceState(null, '', u)
      }, originEl)
    },
    [switchPage],
  )

  /** 在「文件」页里换目录：状态和地址栏一起更新 */
  const changeFilesPath = useCallback((p: string) => {
    setFilesPath(p)
    const u = new URL(location.href)
    if (p) u.searchParams.set('path', p)
    else u.searchParams.delete('path')
    history.replaceState(null, '', u)
  }, [])

  const running = active?.status === 'running'

  /* 「能不能连文件一起删」和「用户勾没勾」是两件事，必须分开算。
     实例跑着时不能删文件：存档正被 JVM 按自己的节奏写。 */
  const canWipeFiles = !!active && !running && active.status !== 'starting'

  // 移动抽屉：设计稿用 body.drawer 驱动 .side 滑入；点空白处收起（app.js 同款）
  useEffect(() => {
    document.body.classList.toggle('drawer', drawer)
    return () => document.body.classList.remove('drawer')
  }, [drawer])
  useEffect(() => {
    if (!drawer) return
    const onDoc = (e: MouseEvent) => {
      const t = e.target as HTMLElement
      if (t.closest('.side') || t.closest('#drawerBtn')) return
      setDrawer(false)
    }
    document.addEventListener('click', onDoc)
    return () => document.removeEventListener('click', onDoc)
  }, [drawer])

  const act = async (verb: 'start' | 'stop' | 'kill' | 'restart') => {
    if (!activeId) return
    if (verb === 'start') {
      // 点「启动」先体检：有问题就把清单摆出来，别让用户对着一段 Java 英文报错发呆。
      setStarting(true)
      try {
        const r = await api.preflight(activeId)
        setPreflight(r)
        if (!r.ok) {
          setStarting(false)
          return
        }
      } catch {
        /* 体检本身失败不拦着启动，让后端去报真正的错 */
      }
      setStarting(false)
    }
    try {
      await api.action(activeId, verb)
      refresh()
      if (verb === 'start') runPreflight()
    } catch (e) {
      if (e instanceof PreflightBlocked) setPreflight(e.report)
      setToast((e as Error).message)
    }
  }

  const send = async (cmd: string) => {
    if (!activeId) return
    try {
      await api.sendCommand(activeId, cmd)
    } catch (e) {
      setToast((e as Error).message)
    }
  }

  const doRemove = async () => {
    if (!active) return
    const name = active.name
    const wipe = deleteFiles && canWipeFiles
    try {
      await api.removeInstance(active.id, wipe)
      setDeleteOpen(false)
      setActiveId('')
      setLogs([])
      setToast(
        wipe ? `「${name}」已删除，磁盘上的目录也一并清空了` : `「${name}」已从列表移除，目录仍保留在磁盘上`,
      )
      refresh()
    } catch (e) {
      setToast((e as Error).message)
    }
  }

  const peakCpu = histCpu.length ? Math.max(...histCpu) : 0
  const peakMem = histMem.length ? Math.max(...histMem) : 0

  // 侧栏单独包边界：世界列表崩了不该把整个面板带走 —— 它是「切到别处去」的最后退路。
  const side = (extra?: { onClose?: () => void }) => (
    <ErrorBoundary title="世界列表">
      <WorldList
        instances={instances}
        activeId={activeId}
        downloads={runningTasks}
        onDownloads={() => {
          setDlOpen(true)
          setDrawer(false)
        }}
        onSelect={(id) => {
          setActiveId(id)
          goView('board')
          setDrawer(false)
        }}
        onNew={() => {
          goView('create')
          setDrawer(false)
        }}
        onMarket={() => {
          goView('market')
          setDrawer(false)
        }}
        onImported={(id) => {
          setActiveId(id)
          goView('board')
          setDrawer(false)
          refresh()
          setToast('已把这个目录纳管进来 —— 点「启动」就能开服')
        }}
        {...extra}
      />
    </ErrorBoundary>
  )

  // 开了访问保护又没通过验证 → 只给登录界面。
  // 放在这里是刻意的：整个面板的任何数据都不该在未验证时被请求出去。
  if (auth?.requireAuth && !auth.authenticated) {
    return (
      <LoginScreen
        hint={auth.isLoopback ? '' : '本机访问默认免密码，密码对不上就去那台机器上打开面板重设'}
        onDone={() => {
          // 登录通过 ＝ 一段新的加载开始：先铺幕（盖住「放行后补拉数据」的空窗），
          // 再把首屏数据拉起来 —— 揭幕时面板已经是落定的。
          splash()
          refresh()
          checkAuth()
        }}
      />
    )
  }

  return (
    <>
      {/* 背景大气层：像素尘 + 地层网格 + 暗角。设计稿里它是 .app 的兄弟节点。 */}
      <div className="atmos" aria-hidden="true">
        <div className="grid" />
        <div className="glow-a" />
        <div className="glow-b" />
        <div className="motes">
          {[6, 14, 23, 31, 44, 52, 61, 70, 79, 87, 94].map((left, i) => (
            <i key={left} style={{ left: `${left}%`, ['--d' as string]: `${13 + ((i * 4) % 19)}s` }} />
          ))}
        </div>
        <div className="vignette" />
      </div>

      <div className="app">
        <div className="strata" />

        {/* 顶栏：品牌 + 连接状态 + 全局动作。移动端「世界」按钮是 .only-mobile（≤1080px 才出现） */}
        <header className="topbar">
          <button
            type="button"
            className="btn btn-ghost icon only-mobile"
            id="drawerBtn"
            onClick={() => setDrawer((d) => !d)}
            aria-label="世界列表"
          >
            <Icon name="grass" />
          </button>
          <div className="brand">
            <div className="mark">
              <svg viewBox="0 0 16 16" aria-hidden="true">
                <use href="#i-grass" />
              </svg>
            </div>
            <div>
              <div className="nm">ENDERBOX</div>
              <div className="sub">MC 服务器管理面板</div>
            </div>
          </div>
          <div className="spacer" />
          <div className="conn">
            <span className={`pulse-dot${probed && !online ? ' d' : ''}`} />
            {online ? '守护进程已连接' : probed ? '已断开' : '连接中…'}
          </div>
          <button type="button" className="btn btn-ghost sm" onClick={refresh}>
            <Icon name="refresh" size={12} />
            刷新
          </button>
          {/* 设置：面板自己的入口（原来挂在 tabbar 最后一个 tab，用户点名挪到顶栏）。
              tabbar = 世界的页，顶栏 = 全局动作 —— 设置属于后者。
              aria-current 兼作激活态判据（.topbar 里有一条联动样式）。 */}
          <button
            type="button"
            className="btn btn-ghost sm"
            aria-current={page === 'settings' ? 'page' : undefined}
            title="面板设置（改的是面板自己，不是某一个世界）"
            onClick={() => {
              goto('settings')
              setDrawer(false)
            }}
          >
            <Icon name="lever" size={12} />
            设置
          </button>
          <button
            type="button"
            className="btn btn-primary sm"
            onClick={(e) => {
              goView('create', e.currentTarget)
              setDrawer(false)
            }}
          >
            <Icon name="plus" size={12} />
            新建世界
          </button>
        </header>

        {/* 下载/安装的进度气泡与台账，都跟页面无关，必须挂在最外层。
            aboveHud 跟着 HUD 的渲染条件走（active 才有 HUD）—— 没有它气泡会钻进底部条里。 */}
        <Bubbles
          items={dlOpen ? [] : feed.items}
          toasts={toasts}
          aboveHud={!!active}
          onDismiss={dismissToast}
          onOpenInstance={(id) => {
            setActiveId(id)
            goView('board')
          }}
          onCancel={(it) => {
            feed.cancel(it).then(
              () => setToast(`已取消：${it.label}`),
              (e: Error) => setToast(e.message),
            )
          }}
        />
        <DownloadPanel
          open={dlOpen}
          items={feed.items}
          onClose={() => setDlOpen(false)}
          onOpenInstance={(id) => {
            setDlOpen(false)
            setActiveId(id)
            goView('board')
          }}
          onCancel={(it) => {
            feed.cancel(it).then(
              () => setToast(`已取消：${it.label}`),
              (e: Error) => setToast(e.message),
            )
          }}
          onDismiss={(it) => {
            feed.dismiss(it).then(
              () => setToast('已删除记录'),
              (e: Error) => setToast(e.message),
            )
          }}
          onClearFinished={(finished) => {
            // 批量删记录：逐条走同一个 dismiss（前端乐观隐藏 + 后端删除）。
            // 用 allSettled 不用 all —— 一条失败（守护进程重启过、记录早没了）不该连累其它条，
            // 也不该静默：删掉几条、失败几条，如实说。
            Promise.allSettled(finished.map((it) => feed.dismiss(it))).then((rs) => {
              const bad = rs.filter((r) => r.status === 'rejected').length
              setToast(
                bad > 0
                  ? `已清掉 ${finished.length - bad} 条，有 ${bad} 条没删成（可能已经被清过）`
                  : `已清空 ${finished.length} 条已结束记录`,
              )
            })
          }}
        />

        {/* 底部健康条：常驻贯穿所有页面（fixed 定位） */}
        {active && (
          <HudBar
            metrics={metrics}
            report={preflight}
            port={active.port}
            name={active.name}
            status={active.status}
          />
        )}

        <div className="shell" ref={shellRef}>
          {/* 侧栏常驻；≤1080px 由 design.css 把它 fixed 并靠 body.drawer 滑入 */}
          {side()}

          {view === 'create' ? (
            <ErrorBoundary title="新建向导" onReset={() => setView('board')}>
              <CreateWizard
                onDone={(newId) => {
                  goView('board')
                  if (newId) setActiveId(newId)
                  refresh()
                  setToast('世界建好了 —— 把服务端 jar 放进实例目录，然后点「启动」')
                }}
                onCancel={() => goView('board')}
                onError={setToast}
              />
            </ErrorBoundary>
          ) : view === 'market' ? (
            <ModpackMarket
              onOpenPack={(id) => {
                setPackId(id)
                goView('pack')
                const u = new URL(location.href)
                u.searchParams.set('view', 'market')
                u.searchParams.set('pk', id)
                u.searchParams.delete('ptab')
                history.replaceState(null, '', u)
              }}
              onCancel={() => goView('board')}
            />
          ) : view === 'pack' ? (
            <ModpackDetail
              key={packId}
              project={packId}
              onBack={() => {
                goView('market')
                const u = new URL(location.href)
                u.searchParams.delete('pk')
                u.searchParams.delete('ptab')
                u.searchParams.set('view', 'market')
                history.replaceState(null, '', u)
              }}
              onOpenInstance={(id) => {
                setActiveId(id)
                goView('board')
              }}
              onInstanceCreated={refresh}
              onToast={setToast}
            />
          ) : (
            <main className="main">
              {/* ⚠ 断连横幅放 main 里侧栏之后、世界头之前 —— 它说的是「整台守护进程」的事，
                  不属于任何一个世界。 */}
              <ConnectionBanner down={probed && !online} onRetry={refresh} />

              {/* 世界身份头：第一行「我在操作谁」+ 状态徽记 + 类型 + 生命周期操作，
                  第二行 .world-meta 是端口/内存/PID/已运行 + 路径。 */}
              <div className="world-head">
                <div className="world-title">
                  {active && (
                    <h1 className="reveal" style={{ ['--rd' as string]: '.04s' }}>
                      {active.name}
                    </h1>
                  )}
                  {active && (
                    <span
                      className={`state-pill reveal${running ? '' : ' stop'}`}
                      style={{ ['--rd' as string]: '.08s' }}
                    >
                      <i />
                      {STATUS_LABEL[active.status]}
                    </span>
                  )}
                  {active && (
                    <span className="type-chip reveal" style={{ ['--rd' as string]: '.1s' }}>
                      <Icon name="cmd" size={12} />
                      {serverLabel(active).toUpperCase()} {active.mcVersion}
                    </span>
                  )}
                  {active && (
                    <div className="world-actions">
                      {running || active.status === 'starting' ? (
                        <>
                          <button type="button" className="btn btn-stop sm" onClick={() => act('stop')}>
                            <Icon name="stop" size={12} />
                            停止
                          </button>
                          <button
                            type="button"
                            className="btn sm"
                            onClick={() => act('restart')}
                            title="先按正常流程停服存盘，再重新拉起来"
                          >
                            <Icon name="restart" size={12} />
                            重启
                          </button>
                        </>
                      ) : active.status === 'stopping' ? (
                        /* 停止卡住时才出现「强制结束」—— 设计稿里没有它是因为设计稿的服务器
                           好好的。同一个位置、同一套按钮语言：正常时是「停止」，卡住时换成它。 */
                        <button
                          type="button"
                          className="btn btn-danger sm"
                          onClick={() => setKillOpen(true)}
                          title="停不下来了：直接杀进程，来不及存盘"
                        >
                          <Icon name="ban" size={12} />
                          强制结束
                        </button>
                      ) : (
                        <button
                          type="button"
                          className="btn btn-start sm"
                          disabled={starting}
                          onClick={() => act('start')}
                        >
                          <Icon name="play" size={13} />
                          {starting ? '检查中…' : '启动'}
                        </button>
                      )}
                      <button
                        type="button"
                        className="btn btn-danger sm"
                        onClick={() => {
                          setDeleteFiles(false)
                          setDeleteOpen(true)
                        }}
                      >
                        <Icon name="trash" size={12} />
                        删除
                      </button>
                    </div>
                  )}
                </div>

                {active && (
                  <div className="world-meta">
                    <span className="kv">
                      端口 <b>{active.port}</b>
                    </span>
                    <span className="kv">
                      内存 <b>{active.maxMem} MB</b>
                    </span>
                    <span className="kv">
                      PID <b>{metrics?.pid ? metrics.pid : '—'}</b>
                    </span>
                    <span className="kv">
                      已运行 <b>{running && metrics ? formatUptime(metrics.uptimeSec) : '—'}</b>
                    </span>
                    <span className="path" title={active.dir}>
                      {active.dir}
                    </span>
                  </div>
                )}
              </div>

              {/* 页签：MC 物品槽（图标 + 文字）。
                  ⚠ 「设置」不在这排 —— 它是「面板自己」的入口，放顶栏（用户点名）；
                  这一排只放某个世界的内容页。深链 ?page=settings 仍走 PAGES 校验。 */}
              <nav className="tabbar" id="tabbar" ref={tabbarRef}>
                {/* 滑行的青色菱形（位置由上面的 useLayoutEffect 量着 .tab.on 写） */}
                <i className="tab-glide" ref={glideRef} aria-hidden="true" />
                {PAGES.filter((p) => p.k !== 'settings').map((p) => {
                  const on = page === p.k
                  return (
                    <button
                      key={p.k}
                      type="button"
                      aria-current={on}
                      /* 把「被点的那颗页签」交给转场当扩散原点：方块从它向外长 */
                      onClick={(e) => goto(p.k, e.currentTarget)}
                      className={`tab${on ? ' on' : ''}`}
                    >
                      <Icon name={p.icon} />
                      {p.label}
                    </button>
                  )
                })}
              </nav>

              {/* 内容区：.content > .page。key={page} 让切页时整块重挂载 → .reveal 入场动画重放 */}
              <div className="content" ref={contentRef}>
                <section className="page on" data-page={page} key={page}>
                  <ErrorBoundary title={PAGES.find((p) => p.k === page)?.label ?? '这一页'}>
                    {/* 还没问过第一遍时是第三种状态：既不是「没有世界」也不是「连不上」。 */}
                    {page !== 'settings' && !active && !probed && (
                      <SkeletonState />
                    )}
                    {page !== 'settings' && !active && probed && (
                      <EmptyState online={online} onNew={() => goView('create')} />
                    )}

                    {active && page === 'home' && (
                      <>
                        {active.error && (
                          <div className="banner-danger" style={{ marginTop: 12 }}>
                            <Icon name="ban" size={18} style={{ color: 'var(--bad)' }} />
                            <div className="t">
                              <h3>上次退出留下了错误</h3>
                              <p style={{ fontFamily: 'var(--f-term)', fontSize: 13 }}>{active.error}</p>
                            </div>
                          </div>
                        )}
                        {active.status === 'crashed' && (
                          <CrashPanel instance={active} fixing={fixing} onFix={applyFix} onToast={setToast} />
                        )}
                        <OverviewPage
                          instance={active}
                          metrics={metrics}
                          histCpu={histCpu}
                          histMem={histMem}
                          histPlayers={histPlayers}
                          peakCpu={peakCpu}
                          peakMem={peakMem}
                          preflight={preflight}
                          fixing={fixing}
                          onFix={applyFix}
                          onRerunPreflight={runPreflight}
                          logs={logs}
                          onSend={send}
                          onGoto={(p) => goto(p as Page)}
                        />
                      </>
                    )}

                    {active && page === 'console' && (
                      <ConsolePage
                        instance={active}
                        logs={logs}
                        running={running}
                        /* 带 ?nosse=1 时不建 SSE，日志就不是实时的 —— 这个事实必须传下去，
                           让页面自己说出来，否则用户会以为日志坏了。 */
                        live={!NOSSE}
                        onSend={send}
                      />
                    )}

                    {active && page === 'players' && <PlayersPage instance={active} onToast={setToast} />}

                    {active && page === 'config' && (
                      <ConfigPanel instance={active} onToast={setToast} onSaved={refresh} />
                    )}
                    {active && page === 'rules' && (
                      <RulesPanel instance={active} running={running} onToast={setToast} />
                    )}
                    {active && page === 'plugins' && (
                      <PluginsPanel instance={active} onToast={setToast} onOpenFiles={openFiles} />
                    )}
                    {active && page === 'modpack' && (
                      <ModpackPanel instance={active} onToast={setToast} />
                    )}
                    {active && page === 'files' && (
                      <FileManager
                        instance={active}
                        path={filesPath}
                        onPath={changeFilesPath}
                        onToast={setToast}
                      />
                    )}
                    {active && page === 'backup' && (
                      <BackupPanel instance={active} onToast={setToast} />
                    )}
                    {active && page === 'tasks' && (
                      <TasksPanel instance={active} onToast={setToast} />
                    )}
                    {page === 'settings' && (
                      <SettingsPanel instances={instances} onToast={setToast} />
                    )}
                  </ErrorBoundary>
                </section>
              </div>
            </main>
          )}
        </div>

        {/* 删除确认：给「同时删除世界文件」一个明确的位置 */}
        {deleteOpen && active && (
          <ConfirmDialog
            title={`删除「${active.name}」`}
            danger
            width={540}
            confirmLabel={deleteFiles && canWipeFiles ? '删除并清空文件' : '删除'}
            onCancel={() => setDeleteOpen(false)}
            onConfirm={doRemove}
            body={
              <>
                配置一定会被移除。磁盘上的实例目录
                <b style={{ color: 'var(--ink-1)' }}>默认保留</b> —— 存档、插件都还在，日后还能自己找回来。
              </>
            }
          >
            <div style={{ marginTop: 12, borderTop: '1px solid var(--line-1)', paddingTop: 4 }}>
              <ToggleRow
                on={deleteFiles && canWipeFiles}
                onChange={setDeleteFiles}
                disabled={!canWipeFiles}
                title="同时删除磁盘上的世界文件"
                desc={
                  canWipeFiles
                    ? `会清空 ${active.dir} 里的全部内容：world 存档、插件、服务端文件。删掉之后找不回来。`
                    : '实例正在运行，先把服务端停掉，才能连文件一起删。'
                }
              />
            </div>
          </ConfirmDialog>
        )}

        {killOpen && active && (
          <ConfirmDialog
            title={`强制结束「${active.name}」`}
            danger
            confirmLabel="强制结束"
            onCancel={() => setKillOpen(false)}
            onConfirm={() => {
              setKillOpen(false)
              act('kill')
            }}
            body={
              <>
                强制结束是直接杀进程，服务端<b style={{ color: 'var(--ink-1)' }}>来不及把地图写回磁盘</b>。
                正在建造的建筑、刚放进箱子的物品可能丢失，存档也有损坏的风险。
                <br />
                <br />
                正常做法是点「停止」——它会先让服务端自己存盘，也就十几秒的事。
              </>
            }
          />
        )}
      </div>
    </>
  )
}

/** 骨架态（正在连接守护进程…）：既不是「没有世界」也不是「连不上」，别谎报故障。 */
function SkeletonState() {
  return (
    <div className="panel reveal">
      <i className="corner" />
      <div className="con-head tex-stone">
        <Icon name="info" />
        <h2>正在连接守护进程…</h2>
      </div>
      <div className="statecard">
        <div className="sk" style={{ width: '56%' }} />
        <div className="sk" style={{ width: '82%', marginTop: 12 }} />
        <div className="sk" style={{ width: '64%', marginTop: 12 }} />
        <div style={{ display: 'flex', gap: 10, marginTop: 20 }}>
          <div className="sk" style={{ width: 110, height: 32 }} />
          <div className="sk" style={{ width: 80, height: 32 }} />
        </div>
        <p style={{ marginTop: 16, fontSize: 12.5, color: 'var(--ink-3)' }}>
          还没问过第一遍时用它 —— 既不是「没有世界」也不是「连不上」，别谎报故障。
        </p>
      </div>
    </div>
  )
}

/** 空态：还没有世界 / 连不上守护进程。 */
function EmptyState({ online, onNew }: { online: boolean; onNew: () => void }) {
  return (
    <div className="panel reveal">
      <i className="corner" />
      <div className="con-head tex-stone">
        <Icon name={online ? 'grass' : 'ban'} style={{ color: online ? 'var(--ok)' : 'var(--bad)' }} />
        <h2>{online ? '还没有世界' : '连不上守护进程'}</h2>
      </div>
      <div className="statecard">
        {online ? (
          <>
            <svg className="empty-ic" viewBox="0 0 16 16" style={{ color: 'var(--ink-3)' }} aria-hidden="true">
              <use href="#i-grass" />
            </svg>
            <h3>先在左边建一个</h3>
            <p>5 步就有了一台自己的服务器：挑服务端 → 挑版本 → 选内存 → 装 Java → 点启动。剩下的交给我们。</p>
            <div style={{ display: 'flex', gap: 8, marginTop: 18, flexWrap: 'wrap' }}>
              <button className="btn btn-start" type="button" onClick={onNew}>
                <Icon name="plus" />
                新建世界
              </button>
            </div>
          </>
        ) : (
          <>
            <h3>后端没起来</h3>
            <p>确认 enderbox.exe 在跑，或者看看它是不是换了端口。页面里的读数是断连前一刻的，别照着它下判断。</p>
          </>
        )}
      </div>
    </div>
  )
}
