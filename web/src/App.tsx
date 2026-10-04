import { useCallback, useEffect, useState } from 'react'
import { api, PreflightBlocked, type AuthStatus, type Preflight } from './api'
import { BackupPanel } from './components/BackupPanel'
import { ConfigPanel } from './components/ConfigPanel'
import { CreateWizard } from './components/CreateWizard'
import { IconKill, IconMenu, IconPlay, IconStop, IconTrash, IconTypeBlock } from './components/Icons'
import { LoginScreen } from './components/LoginScreen'
import { LogConsole } from './components/LogConsole'
import { MetricBand } from './components/MetricBand'
import { PluginsPanel } from './components/PluginsPanel'
import { PreflightPanel } from './components/PreflightPanel'
import { SettingsPanel } from './components/SettingsPanel'
import { WorldList } from './components/WorldList'
import { STATUS_COLOR, STATUS_LABEL, TYPE_LABEL, formatUptime, type Instance, type Metrics } from './types'

// 截图/调试开关：带 ?nosse=1 时不建立 SSE 长连接，否则 headless 截图会被长连接卡死
const PARAMS = new URLSearchParams(location.search)
const NOSSE = PARAMS.has('nosse')
// 支持直接打开新建向导 / 直接进某一页（刷新不丢位置，也方便截图核对）
const START_CREATE = PARAMS.get('view') === 'create'
// 直接以抽屉打开状态启动：手机上抽屉是交互态，截图工具点不了，只能靠参数
const START_DRAWER = PARAMS.has('drawer')

const PAGES = [
  { k: 'home', label: '概览' },
  { k: 'config', label: '配置' },
  { k: 'plugins', label: '插件与模组' },
  { k: 'backup', label: '备份与还原' },
  { k: 'settings', label: '设置' },
] as const
type Page = (typeof PAGES)[number]['k']

function initialPage(): Page {
  const p = PARAMS.get('page')
  return PAGES.some((x) => x.k === p) ? (p as Page) : 'home'
}

const POLL_MS = 3000
const HISTORY = 60

export default function App() {
  const [instances, setInstances] = useState<Instance[]>([])
  const [activeId, setActiveId] = useState('')
  const [logs, setLogs] = useState<string[]>([])
  const [metrics, setMetrics] = useState<Metrics | null>(null)
  const [histCpu, setHistCpu] = useState<number[]>([])
  const [histMem, setHistMem] = useState<number[]>([])
  const [online, setOnline] = useState(false)
  const [view, setView] = useState<'board' | 'create'>(START_CREATE ? 'create' : 'board')
  const [page, setPage] = useState<Page>(initialPage)
  const [drawer, setDrawer] = useState(START_DRAWER)
  const [toast, setToast] = useState('')
  // null = 还没问出来；问出来之后才知道要不要弹登录界面。
  // 不能默认「已登录」—— 那样开着保护的部署会先闪一下完整界面再被踢回登录页。
  const [auth, setAuth] = useState<AuthStatus | null>(null)
  // 启动前体检：只在实例停下时才刷，跑起来的实例查它没意义（还白拉几次 JVM 探测）
  const [preflight, setPreflight] = useState<Preflight | null>(null)
  const [fixing, setFixing] = useState('')
  const [starting, setStarting] = useState(false)

  const refresh = useCallback(async () => {
    try {
      const data = await api.listInstances()
      const list = data ?? []
      setInstances(list)
      setOnline(true)
      setActiveId((prev) => (list.some((i) => i.id === prev) ? prev : (list[0]?.id ?? '')))
    } catch {
      setOnline(false)
    }
  }, [])

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, POLL_MS)
    return () => clearInterval(t)
  }, [refresh])

  // 指标轮询：只采当前选中的实例；峰值在客户端攒，后端不存历史
  useEffect(() => {
    setMetrics(null)
    setHistCpu([])
    setHistMem([])
    if (!activeId) return

    let alive = true
    const tick = async () => {
      try {
        const m = await api.metrics(activeId)
        if (!alive) return
        setMetrics(m)
        setHistCpu((h) => [...h, m.cpu].slice(-HISTORY))
        setHistMem((h) => [...h, m.memUsed].slice(-HISTORY))
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

  // 日志流
  useEffect(() => {
    if (!activeId) return
    setLogs([])
    if (NOSSE) {
      api.logHistory(activeId).then(setLogs).catch(() => {})
      return
    }
    const es = new EventSource(`/api/instances/${activeId}/logs`)
    es.onmessage = (e) => {
      const line = JSON.parse(e.data) as string
      setLogs((prev) => (prev.length > 3000 ? [...prev.slice(-2000), line] : [...prev, line]))
    }
    return () => es.close()
  }, [activeId])

  const checkAuth = useCallback(async () => {
    try {
      setAuth(await api.authStatus())
    } catch {
      // 问不到状态（守护进程没起来之类）就不要拦着 —— 后面每个请求自己会报错，
      // 在这里假装「需要登录」只会让人以为密码打错了。
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

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(() => setToast(''), 4000)
    return () => clearTimeout(t)
  }, [toast])

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

  // 只在实例处于「停着」的状态下体检。跑着的时候既没意义，又白拉几次 java -version 探测。
  // 依赖里放 status 而不是整个 active：轮询每 3 秒换一个新对象，放进去会变成每 3 秒体检一次。
  useEffect(() => {
    setPreflight(null)
    if (!activeId) return
    if (activeStatus === 'stopped' || activeStatus === 'crashed') runPreflight()
  }, [activeId, activeStatus, runPreflight])

  const applyFix = async (code: string) => {
    setFixing(code)
    try {
      const r = await api.applyFix(activeId, code)
      setPreflight(r.preflight)
      setToast(r.preflight.ok ? '修好了，可以开服了' : '这一步处理完了，还有别的要先解决')
    } catch (e) {
      setToast((e as Error).message)
    } finally {
      setFixing('')
    }
  }

  const goto = (p: Page) => {
    setPage(p)
    const u = new URL(location.href)
    if (p === 'home') u.searchParams.delete('page')
    else u.searchParams.set('page', p)
    u.searchParams.delete('view')
    history.replaceState(null, '', u)
  }

  const running = active?.status === 'running'

  const act = async (verb: 'start' | 'stop' | 'kill') => {
    if (!activeId) return
    if (verb === 'start') {
      // 点「启动」先体检：有问题就把清单摆出来，别让用户对着一段 Java 英文报错发呆。
      // 后端也会在 start 里再查一次（那是权威），这里只为提前一步、少一次失败往返。
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
      // 后端在 start 里挡住了：把报告直接铺到界面上
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

  const remove = async () => {
    if (!active) return
    if (!window.confirm(`删除「${active.name}」？\n\n配置会被移除。服务器目录默认保留在磁盘上，不会自动清空。`)) return
    try {
      await api.removeInstance(active.id)
      setActiveId('')
      setLogs([])
      refresh()
    } catch (e) {
      setToast((e as Error).message)
    }
  }

  const peakCpu = histCpu.length ? Math.max(...histCpu) : 0
  const peakMem = histMem.length ? Math.max(...histMem) : 0

  const side = (extra?: { onClose: () => void }) => (
    <WorldList
      instances={instances}
      activeId={activeId}
      onSelect={(id) => {
        setActiveId(id)
        setView('board')
        setDrawer(false)
      }}
      onNew={() => {
        setView('create')
        setDrawer(false)
      }}
      {...extra}
    />
  )

  // 开了访问保护又没通过验证 → 只给登录界面。
  // 放在这里是刻意的：整个面板的任何数据都不该在未验证时被请求出去。
  if (auth?.requireAuth && !auth.authenticated) {
    return (
      <LoginScreen
        hint={auth.isLoopback ? '' : '本机访问默认免密码，密码对不上就去那台机器上打开面板重设'}
        onDone={checkAuth}
      />
    )
  }

  return (
    <div className="relative grid h-full min-h-0 grid-cols-1 grid-rows-1 md:grid-cols-[236px_1fr]">
      {/* 桌面侧栏：常驻 */}
      <div className="hidden min-h-0 border-r-[3px] border-black md:flex md:flex-col">{side()}</div>

      {/* 移动抽屉：侧栏在手机上会吃掉半屏，收进抽屉里 */}
      {drawer && (
        <>
          <div
            className="fixed inset-0 z-40 bg-black/70 md:hidden"
            onClick={() => setDrawer(false)}
            aria-hidden="true"
          />
          <div className="fixed inset-y-0 left-0 z-50 flex w-[264px] max-w-[84vw] border-r-[3px] border-black md:hidden">
            {side({ onClose: () => setDrawer(false) })}
          </div>
        </>
      )}

      {view === 'create' ? (
        <CreateWizard
          onDone={(newId) => {
            setView('board')
            if (newId) setActiveId(newId)
            refresh()
            setToast('世界建好了 —— 把服务端 jar 放进实例目录，然后点「启动」')
          }}
          onCancel={() => setView('board')}
          onError={setToast}
        />
      ) : (
        <main
          key={page}
          className="anim-fade scroll-thin flex min-h-0 min-w-0 flex-col overflow-y-auto px-4 pt-4 pb-6 md:px-[26px] md:pt-5"
        >
          {/* 页面导航 + 移动端汉堡 */}
          <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 border-b-[3px] border-black pb-3">
            <button
              type="button"
              className="btn btn-ghost btn-sm md:hidden"
              onClick={() => setDrawer(true)}
              aria-label="打开世界列表"
            >
              <IconMenu size={13} />
              世界
            </button>
            <div className="flex min-w-0 flex-wrap gap-2 md:ml-auto">
              {PAGES.map((p) => {
                const on = page === p.k
                return (
                  <button
                    key={p.k}
                    type="button"
                    aria-current={on}
                    onClick={() => goto(p.k)}
                    className={`nav-pill${on ? ' on' : ''}`}
                  >
                    {p.label}
                  </button>
                )
              })}
            </div>
          </div>

          {page === 'settings' && <SettingsPanel instances={instances} onToast={setToast} />}

          {page !== 'settings' && !active && (
            <div className="px-card flex flex-1 flex-col justify-center gap-3 p-8">
              <h1 className="text-[20px] font-bold text-ink-1">
                {online ? '还没有世界' : '连不上守护进程'}
              </h1>
              <p className="max-w-[62ch] text-[13.5px] leading-relaxed text-ink-2">
                {online
                  ? '先在左边（手机上点「世界」）建一个，5 步就有了一台自己的服务器。'
                  : '后端没起来。确认 serverbox.exe 在跑，或者看看它是不是换了端口。'}
              </p>
              <div className="flex items-center gap-2 text-[12px] text-ink-3">
                <span className="block h-2 w-2" style={{ background: online ? '#7cc24f' : '#e0574c' }} />
                {online ? '守护进程已连接' : '未连接'}
              </div>
            </div>
          )}

          {active && page !== 'settings' && (
            <>
              <div className="flex flex-wrap items-start gap-4 border-b-[3px] border-black pb-4">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-3">
                    <IconTypeBlock type={active.type} size={24} />
                    <h1 className="text-[22px] leading-tight font-bold text-ink-1">{active.name}</h1>
                    <span className="inline-flex items-center gap-2 bg-panel-2 px-2.5 py-1 text-[12px] text-ink-2">
                      <span className="block h-2 w-2" style={{ background: STATUS_COLOR[active.status] }} />
                      {STATUS_LABEL[active.status]}
                    </span>
                  </div>
                  <p className="mono-value mt-1.5">
                    {(TYPE_LABEL[active.type] ?? active.type).toUpperCase()} {active.mcVersion} · 端口{' '}
                    {active.port}
                  </p>
                  {/* 目录单独一行：路径长，跟在类型后面会在窄屏把整行挤爆 */}
                  <p className="mono-meta mt-0.5 break-all">{active.dir}</p>
                </div>

                {page === 'home' && (
                  <div className="flex w-full flex-wrap gap-2.5 md:ml-auto md:w-auto">
                    {running || active.status === 'starting' ? (
                      <>
                        <button type="button" className="btn" onClick={() => act('stop')}>
                          <IconStop size={12} />
                          停止
                        </button>
                        <button
                          type="button"
                          className="btn btn-danger"
                          onClick={() => {
                            if (window.confirm('强制结束不会保存地图，可能损坏存档。确定？')) act('kill')
                          }}
                        >
                          <IconKill size={12} />
                          强制结束
                        </button>
                      </>
                    ) : (
                      <button
                        type="button"
                        className="btn btn-grass"
                        disabled={active.status === 'stopping' || starting}
                        onClick={() => act('start')}
                      >
                        <IconPlay size={13} />
                        {active.status === 'stopping' ? '停止中…' : starting ? '检查中…' : '启动'}
                      </button>
                    )}
                    <button type="button" className="btn btn-ghost" onClick={remove} aria-label="删除这个世界">
                      <IconTrash size={12} />
                      删除
                    </button>
                  </div>
                )}
              </div>

              {page === 'home' && active.error && (
                <div className="mt-4 border-l-[3px] border-danger bg-[#1c1314] px-3.5 py-2.5">
                  <p className="font-mono text-[11.5px] leading-relaxed text-[#ffc2c2]">{active.error}</p>
                </div>
              )}

              {page === 'home' && (
                <>
                  {/* 体检报告放在指标带之前：它决定你能不能开服，比读数更该先看到 */}
                  {!running && preflight && (
                    <PreflightPanel
                      report={preflight}
                      fixing={fixing}
                      onFix={applyFix}
                      onRecheck={runPreflight}
                    />
                  )}

                  <div className="mt-5">
                    <MetricBand metrics={metrics} peakCpu={peakCpu} peakMem={peakMem} />
                  </div>

                  <LogConsole
                    instance={active}
                    logs={logs}
                    running={running}
                    onSend={send}
                    onClear={() => setLogs([])}
                  />

                  <dl className="mt-6 grid grid-cols-1 gap-5 sm:grid-cols-3">
                    <div className="min-w-0">
                      <dt className="eyebrow">服务端文件</dt>
                      <dd className="mono-value mt-1.5 break-all">{active.jarPath}</dd>
                    </div>
                    <div>
                      <dt className="eyebrow">内存上限</dt>
                      <dd className="mt-1.5 text-[13.5px] text-ink-1">{(active.maxMem / 1024).toFixed(1)} GB</dd>
                    </div>
                    <div>
                      <dt className="eyebrow">守护策略</dt>
                      <dd className="mt-1.5 text-[13.5px] text-ink-1">
                        {active.autoStart ? '开机自启' : '不自动启动'} ·{' '}
                        {active.autoRestart ? '崩溃重启' : '崩溃不重启'}
                        {metrics?.uptimeSec ? ` · 已运行 ${formatUptime(metrics.uptimeSec)}` : ''}
                      </dd>
                    </div>
                  </dl>
                </>
              )}

              {page === 'config' && (
                <div className="mt-5">
                  <ConfigPanel instance={active} onToast={setToast} onSaved={refresh} />
                </div>
              )}

              {page === 'plugins' && (
                <div className="mt-5">
                  <PluginsPanel instance={active} onToast={setToast} />
                </div>
              )}

              {page === 'backup' && (
                <div className="mt-5">
                  <BackupPanel instance={active} onToast={setToast} />
                </div>
              )}
            </>
          )}
        </main>
      )}

      {toast && (
        <div
          role="status"
          className="pxb fixed bottom-5 left-1/2 z-50 max-w-[90vw] -translate-x-1/2 bg-panel px-4 py-2.5 text-[13px] text-ink-1"
        >
          {toast}
        </div>
      )}
    </div>
  )
}
