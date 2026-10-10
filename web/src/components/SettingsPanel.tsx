import { useCallback, useEffect, useState } from 'react'
import { api, type JavaRuntime, type SystemInfo } from '../api'
import { AccessPanel, SourcePanel } from './AccessPanel'
import { ConHead, Icon, PaneHead, Panel, SecHead } from './design'
import { fmtTime } from './ui'
import { serverLabel, formatUptime, javaForVersion, type Instance } from '../types'

const TABS = [
  { k: 'access', label: '访问安全' },
  { k: 'java', label: 'Java 运行时' },
  { k: 'source', label: '下载源' },
  { k: 'daemon', label: '守护与启动' },
  { k: 'about', label: '关于' },
] as const

/** 分组图标。设计稿 09 的 .cfg-nav 每组都带一个（crown / cpu / dl / lever / info）。 */
const NAV_ICON: Record<string, string> = {
  access: 'crown',
  java: 'cpu',
  source: 'dl',
  daemon: 'lever',
  about: 'info',
}

/** 右面板的头：标题 + 一句「这组管什么」。 */
const TAB_HEAD: Record<string, { title: string; sub: string }> = {
  access: { title: '访问安全', sub: '谁可以打开这个面板' },
  java: { title: 'Java 运行时', sub: '用哪个 Java 跑服务端' },
  source: { title: '下载源', sub: '下载走哪条线路' },
  daemon: { title: '守护与启动', sub: '这一页只管全局行为' },
  about: { title: '关于', sub: '运行时信息直接来自守护进程' },
}

type TabKey = (typeof TABS)[number]['k']

const SRC_LABEL: Record<JavaRuntime['source'], string> = {
  managed: '本程序托管',
  path: 'PATH 默认',
  system: '系统扫描',
}

type Props = {
  instances: Instance[]
  onToast: (msg: string) => void
}

export function SettingsPanel({ instances, onToast }: Props) {
  // 分页也做成可寻址的（和 ?page= / ?g= 一套惯例）：能直接把某个分页发给别人，
  // 也能让截图脚本停在指定分页上。
  const [tab, setTab] = useState<TabKey>(() => {
    const k = new URLSearchParams(location.search).get('tab')
    return (TABS.some((t) => t.k === k) ? k : 'java') as TabKey
  })
  useEffect(() => {
    const u = new URL(location.href)
    u.searchParams.set('tab', tab)
    history.replaceState(null, '', u)
  }, [tab])
  const [javas, setJavas] = useState<JavaRuntime[] | null>(null)
  const [sys, setSys] = useState<SystemInfo | null>(null)
  const [busy, setBusy] = useState(false)

  const loadJava = useCallback(async () => {
    setBusy(true)
    try {
      const d = await api.java()
      setJavas(d.items ?? [])
    } catch (e) {
      onToast((e as Error).message)
      setJavas([])
    } finally {
      setBusy(false)
    }
  }, [onToast])

  useEffect(() => {
    loadJava()
    api.system().then(setSys).catch(() => setSys(null))
  }, [loadJava])

  return (
    <div className="min-w-0">
      <SecHead
        idx="10"
        title="设置"
        sub="这里改的是面板自己，不是某一个世界 —— 针对单个世界的往左上角的「配置」去"
        style={{ '--rd': '.08s' } as React.CSSProperties}
      />

      {/* 左「选哪一组」右「改这一组」。设计稿 09 就是这两块面板，窄屏由 .cfg-wrap 自己塌成一列。 */}
      <div className="cfg-wrap">
        <Panel style={{ '--rd': '.1s' } as React.CSSProperties}>
          <nav className="cfg-nav" aria-label="设置分组">
            {TABS.map((t) => {
              const on = tab === t.k
              return (
                <button
                  key={t.k}
                  type="button"
                  className={on ? 'on' : ''}
                  aria-current={on}
                  onClick={() => setTab(t.k)}
                >
                  <Icon name={NAV_ICON[t.k]} />
                  {t.label}
                </button>
              )
            })}
          </nav>
        </Panel>

        <Panel style={{ '--rd': '.14s' } as React.CSSProperties}>
          {/* ⚠ 面板头只有标题，没有动作按钮 —— 每组的动作（如「重新扫描」）挂在 pane 内
              区段头（PaneHead）里，照设计稿 09。 */}
          <ConHead icon={NAV_ICON[tab]} title={TAB_HEAD[tab].title} sub={TAB_HEAD[tab].sub} />

          {/* key 换掉就重挂载——.cfg-pane.on 的入场动画自然重放，不用额外状态管理 */}
          <div key={tab} className="cfg-pane on">
            {tab === 'java' && (
              <JavaTab javas={javas} instances={instances} busy={busy} onRescan={loadJava} />
            )}
            {tab === 'about' && <AboutTab sys={sys} />}
            {tab === 'access' && <AccessPanel onToast={onToast} />}
            {tab === 'source' && <SourcePanel onToast={onToast} />}
            {tab === 'daemon' && <DaemonTab />}
          </div>

          {/* 面板页脚：说明保存策略。设计稿 09 每个 pane 下都挂着它（在 cfg-pane 之外） */}
          <div className="unsaved">
            <span className="dotw" />
            <span style={{ fontSize: 12.5, color: 'var(--ink-1)' }}>
              大部分项
              <b style={{ fontFamily: 'var(--f-pix)', color: 'var(--copper)' }}>失焦即存</b>
              ；密码、令牌这类要按按钮的除外
            </span>
          </div>
        </Panel>
      </div>
    </div>
  )
}

/**
 * 02 Java 运行时。
 *
 * 区段头（PaneHead）承担「检测到 N 个 + 重新扫描」—— 2026-10-07 之前这些东西错误地
 * 挂在了面板头（ConHead）上，而设计稿的面板头只有标题，pane 才是动作的宿主。
 */
function JavaTab({
  javas,
  instances,
  busy,
  onRescan,
}: {
  javas: JavaRuntime[] | null
  instances: Instance[]
  busy: boolean
  onRescan: () => void
}) {
  return (
    <div style={{ padding: '14px 16px 18px' }}>
      <PaneHead
        num="02"
        title="Java 运行时"
        note={javas ? `检测到 ${javas.length} 个` : undefined}
      >
        <button type="button" className="btn btn-ghost sm" disabled={busy} onClick={onRescan}>
          <Icon name="refresh" />
          {busy ? '扫描中…' : '重新扫描'}
        </button>
      </PaneHead>

      {javas && javas.length === 0 && (
        <div className="nbox w" style={{ marginTop: 14 }}>
          <div className="hd">
            <Icon name="ban" />
            一个 Java 都没找到
          </div>
          EnderBox 自己不 bundled Java —— 先去装一个（推荐 Eclipse Temurin），装完点「重新扫描」。
        </div>
      )}

      {javas && javas.length > 0 && (
        <div className="rlist" style={{ marginTop: 14 }}>
          {javas.map((j) => (
            <div
              key={j.path}
              className="rrow"
              style={{ gridTemplateColumns: '32px minmax(0, 1fr) auto' }}
            >
              <div className="ricon">
                <Icon name="cpu" size={18} style={{ color: 'var(--copper)' }} />
              </div>
              <div className="rmain">
                <div className="rtitle">
                  Java {j.major}{' '}
                  <span style={{ fontSize: 13, color: 'var(--ink-3)' }}>{j.version}</span>
                  {j.default && <span className="chip s g">默认</span>}
                </div>
                {/* 设计稿里这行是 .rsub（12.5/13px、正文字体）—— 不加 mono，那是设计稿的哑弹类 */}
                <div className="rsub" style={{ fontSize: 13, wordBreak: 'break-all' }}>
                  {j.vendor} · {SRC_LABEL[j.source] ?? j.source}
                  {j.arch ? ` · ${j.arch}` : ''} · {j.path}
                </div>
              </div>
              <div className="ract" />
            </div>
          ))}
        </div>
      )}

      <div style={{ marginTop: 24, borderTop: '1px solid var(--line-1)', paddingTop: 18 }}>
        <PaneHead num="02·B" title="各世界需要哪个 Java" note="按 MC 版本推算" />
        {instances.length === 0 ? (
          <p style={{ marginTop: 12, fontSize: 12.5, color: 'var(--ink-3)' }}>还没有世界。</p>
        ) : (
          <div className="rlist" style={{ marginTop: 14 }}>
            {instances.map((i) => {
              const need = javaForVersion(i.mcVersion)
              const ok = (javas ?? []).some((j) => j.major >= need)
              // 设计稿这组行是 3 列（rmain / rmeta / ract）——没有 ricon，别照「Java 列表」抄个图标进来
              return (
                <div
                  key={i.id}
                  className="rrow"
                  style={{ gridTemplateColumns: 'minmax(0, 1fr) auto auto' }}
                >
                  <div className="rmain">
                    <div className="rtitle">{i.name}</div>
                    <div className="rsub" style={{ fontSize: 13 }}>
                      {serverLabel(i).toUpperCase()} {i.mcVersion}
                    </div>
                  </div>
                  <div className="rmeta">Java {need}</div>
                  <div className="ract">
                    <span className={`chip s${ok ? ' g' : ' d'}`}>{ok ? '已满足' : '缺'}</span>
                  </div>
                </div>
              )
            })}
          </div>
        )}
        <p style={{ marginTop: 12, fontSize: 12.5, lineHeight: 1.8, color: 'var(--ink-2)' }}>
          1.16 及更早要 Java 8 · 1.17～1.20.4 要 Java 17 · 1.20.5 及更新要 Java 21。
          高阶 Java 一般能跑低版本服务端，反过来不行。
        </p>
      </div>
    </div>
  )
}

/** 04 守护与启动。这一页现在只有两块说明 —— 真功能要等 Windows 服务那层。 */
function DaemonTab() {
  return (
    <div style={{ padding: '14px 16px 18px' }}>
      <PaneHead num="04" title="守护与启动" note="这一页只管全局行为" />

      <div
        className="nbox"
        style={{ marginTop: 16, borderLeftColor: 'var(--cyan)', background: 'rgba(51,213,207,.06)' }}
      >
        <div className="hd" style={{ color: 'var(--ink-1)' }}>
          单个世界的开关在「配置」页
        </div>
        某个世界要不要开机自启、崩了要不要自动重启，是每个世界各自的事，放在那个世界的「配置」页里
        —— 一页管一个世界的全部设置，比在这里列一堆世界更容易看懂。
      </div>

      <div className="nbox" style={{ marginTop: 12 }}>
        <div className="hd">
          <span className="badge ban">还没接</span>
          真正的全局行为还没收进来
        </div>
        守护进程自己要不要注册成系统服务、崩溃重启的间隔与次数上限、退出时是否连带关掉所有服务器。
        这些要等 Windows 服务那层做完再统一收进来。
      </div>
    </div>
  )
}

/** 05 关于。运行时信息直接来自守护进程 —— 只读，不提供任何「改」，就没放控件。 */
function AboutTab({ sys }: { sys: SystemInfo | null }) {
  return (
    <div style={{ padding: '14px 16px 18px' }}>
      <PaneHead num="05" title="关于" note="运行时信息直接来自守护进程" />

      <div className="fgrid" style={{ marginTop: 16 }}>
        {(
          [
            ['EnderBox 版本', sys?.version ?? '—'],
            ['Go 版本', sys ? `go${sys.goVersion}` : '—'],
            ['运行平台', sys ? `${sys.goos} / ${sys.goarch}` : '—'],
            ['进程号', sys ? String(sys.pid) : '—'],
            [
              '已运行',
              sys ? `${formatUptime(Number(sys.uptime))}（自 ${fmtTime(sys.startedAt)}）` : '—',
            ],
            ['数据目录', sys?.dataDir ?? '—'],
          ] as const
        ).map(([k, v]) => (
          <div key={k} className="srow" style={{ display: 'block' }}>
            <div className="k" style={{ width: 'auto' }}>
              {k}
            </div>
            <div className="v" style={{ wordBreak: 'break-all' }}>
              {v}
            </div>
          </div>
        ))}
      </div>

      <div
        className="nbox"
        style={{ marginTop: 18, borderLeftColor: 'var(--cyan)', background: 'rgba(51,213,207,.06)' }}
      >
        <div className="hd" style={{ color: 'var(--ink-1)' }}>
          EnderBox 是什么
        </div>
        一个给「家里有台闲置电脑」的人用的 Minecraft 开服器。不是客户端启动器（那是 PCL / HMCL 的活），
        也不是给运维用的面板 —— 目标就一个：30 秒把服开起来，然后忘了它。
      </div>
    </div>
  )
}
