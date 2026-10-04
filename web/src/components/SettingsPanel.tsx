import { useCallback, useEffect, useState } from 'react'
import { api, type JavaRuntime, type SystemInfo } from '../api'
import { AccessPanel, SourcePanel } from './AccessPanel'
import { IconJava, IconRefresh, IconShield } from './Icons'
import { NotReady, SectionHead, fmtTime } from './ui'
import { TYPE_LABEL, formatUptime, javaForVersion, type Instance } from '../types'

const TABS = [
  { k: 'access', label: '访问安全' },
  { k: 'java', label: 'Java 运行时' },
  { k: 'source', label: '下载源' },
  { k: 'daemon', label: '守护与启动' },
  { k: 'about', label: '关于' },
] as const

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
      <h1 className="flex items-center gap-2.5 text-[22px] leading-tight font-bold text-ink-1">
        <span className="text-accent">
          <IconShield size={20} />
        </span>
        设置
      </h1>

      <nav className="pxb mt-5 flex flex-wrap" aria-label="设置分组">
        {TABS.map((t, i) => {
          const on = tab === t.k
          return (
            <button
              key={t.k}
              type="button"
              aria-current={on}
              onClick={() => setTab(t.k)}
              className={`tab-btn${on ? ' on' : ''}`}
            >
              <span className="mr-2 hidden font-mono text-[11px] opacity-70 sm:inline">0{i + 1}</span>
              {t.label}
            </button>
          )
        })}
      </nav>

      {/* key 换掉就重挂载，动画自然重放；不用额外状态管理 */}
      <div key={tab} className="anim-in mt-6">
        {tab === 'java' && (
          <JavaTab javas={javas} busy={busy} onRescan={loadJava} instances={instances} />
        )}
        {tab === 'about' && <AboutTab sys={sys} />}
        {tab === 'access' && <AccessPanel onToast={onToast} />}
        {tab === 'source' && <SourcePanel onToast={onToast} />}
        {tab === 'daemon' && (
          <div className="space-y-3">
            <SectionHead idx="04" title="守护与启动" note="这一页只管全局行为" />
            <div className="border-l-[3px] border-accent-deep bg-[#141a13] px-3.5 py-3">
              <p className="text-[13px] font-semibold text-ink-1">单个世界的开关在「配置」页</p>
              <p className="mt-1.5 max-w-[70ch] text-[12.5px] leading-[1.7] text-ink-2">
                某个世界要不要开机自启、崩了要不要自动重启，是每个世界各自的事，
                放在那个世界的「配置」页里 —— 一页管一个世界的全部设置，比在这里列一堆世界更容易看懂。
              </p>
            </div>
            <NotReady>
              这里留下的是真正的全局行为：守护进程自己要不要注册成系统服务、崩溃重启的间隔与次数上限、
              退出时是否连带关掉所有服务器。这些要等 Windows 服务那层做完再统一收进来。
            </NotReady>
          </div>
        )}
      </div>
    </div>
  )
}

function JavaTab({
  javas,
  busy,
  onRescan,
  instances,
}: {
  javas: JavaRuntime[] | null
  busy: boolean
  onRescan: () => void
  instances: Instance[]
}) {
  return (
    <div>
      <SectionHead
        idx="02"
        title="Java 运行时"
        note={javas ? `检测到 ${javas.length} 个` : '扫描中…'}
        right={
          <button type="button" className="btn btn-ghost btn-sm" disabled={busy} onClick={onRescan}>
            <IconRefresh size={12} />
            {busy ? '扫描中…' : '重新扫描'}
          </button>
        }
      />

      {javas && javas.length === 0 && (
        <NotReady>
          一个 Java 都没找到。ServerBox 自己不 bundled Java ——
          先去装一个（推荐 Eclipse Temurin），装完点「重新扫描」。
        </NotReady>
      )}

      {javas && javas.length > 0 && (
        <>
          {/* 桌面用表格：一屏横向对齐好对比 */}
          <div className="hidden sm:block">
            <table className="px-card w-full border-collapse">
              <thead>
                <tr>
                  {['版本', '厂商', '来源', '位数', '路径'].map((h) => (
                    <th
                      key={h}
                      className="eyebrow border-b-[3px] border-black bg-panel-2 px-3.5 py-2.5 text-left font-normal"
                    >
                      {h}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {javas.map((j) => (
                  <tr key={j.path}>
                    <td className="row-b px-3.5 py-3 text-[13.5px] text-ink-1">
                      <span className="font-mono font-bold">Java {j.major}</span>
                      <span className="ml-2 mono-meta">{j.version}</span>
                      {j.default && <span className="chip chip-g ml-2">默认</span>}
                    </td>
                    <td className="row-b px-3.5 py-3 text-[13.5px] text-ink-2">
                      {j.vendor}
                    </td>
                    <td className="row-b px-3.5 py-3 text-[13.5px] text-ink-2">
                      {SRC_LABEL[j.source] ?? j.source}
                    </td>
                    <td className="row-b px-3.5 py-3 text-[13.5px] text-ink-2">
                      {j.arch || '—'}
                    </td>
                    <td className="row-b mono-meta px-3.5 py-3 break-all">
                      {j.path}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {/* 窄屏改堆叠：表格塞进 500px 只能横向滚，路径还被切断，不如直接换排法 */}
          <div className="stagger px-card sm:hidden">
            {javas.map((j) => (
              <div key={j.path} className="row-b px-3.5 py-3.5">
                <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                  <span className="font-mono text-[14px] font-bold text-ink-1">Java {j.major}</span>
                  <span className="mono-meta">{j.version}</span>
                  {j.default && <span className="chip chip-g">默认</span>}
                </div>
                <p className="mt-1 font-mono text-[11.5px] text-ink-2">
                  {j.vendor} · {SRC_LABEL[j.source] ?? j.source}
                  {j.arch ? ` · ${j.arch}` : ''}
                </p>
                <p className="mt-0.5 mono-meta break-all">{j.path}</p>
              </div>
            ))}
          </div>
        </>
      )}

      <div className="mt-6">
        <SectionHead idx="02·B" title="各世界需要哪个 Java" note="按 MC 版本推算" />
        {instances.length === 0 ? (
          <p className="text-[12.5px] text-ink-3">还没有世界。</p>
        ) : (
          <div className="stagger px-card">
            {instances.map((i) => {
              const need = javaForVersion(i.mcVersion)
              const ok = (javas ?? []).some((j) => j.major >= need)
              return (
                <div
                  key={i.id}
                  className="row-b flex flex-wrap items-center gap-x-3 gap-y-1.5 px-3.5 py-3"
                >
                  <span className="min-w-0 flex-1 text-[13px] text-ink-1">{i.name}</span>
                  <span className="mono-meta">
                    {(TYPE_LABEL[i.type] ?? i.type).toUpperCase()} {i.mcVersion}
                  </span>
                  <span className="font-mono text-[12px] text-ink-2">Java {need}</span>
                  <span className={`chip ${ok ? 'chip-g' : 'chip-d'}`}>{ok ? '已满足' : '缺'}</span>
                </div>
              )
            })}
          </div>
        )}
        <p className="mt-3 text-[12.5px] leading-[1.8] text-ink-2">
          1.16 及更早要 Java 8 · 1.17～1.20.4 要 Java 17 · 1.20.5 及更新要 Java 21。
          高阶 Java 一般能跑低版本服务端，反过来不行。
        </p>
      </div>
    </div>
  )
}

function AboutTab({ sys }: { sys: SystemInfo | null }) {
  return (
    <div>
      <SectionHead idx="05" title="关于" note="运行时信息直接来自守护进程" />
      <dl className="grid grid-cols-1 gap-x-8 gap-y-3 sm:grid-cols-2">
        {[
          ['ServerBox 版本', sys?.version ?? '—'],
          ['Go 版本', sys ? `go${sys.goVersion}` : '—'],
          ['运行平台', sys ? `${sys.goos} / ${sys.goarch}` : '—'],
          ['进程号', sys ? String(sys.pid) : '—'],
          [
            '已运行',
            sys ? `${formatUptime(Number(sys.uptime))}（自 ${fmtTime(sys.startedAt)}）` : '—',
          ],
          ['数据目录', sys?.dataDir ?? '—'],
        ].map(([k, v]) => (
          <div key={k} className="row-b min-w-0 pb-3">
            <dt className="eyebrow">{k}</dt>
            <dd className="mt-1 font-mono text-[12.5px] break-all text-ink-1">{v}</dd>
          </div>
        ))}
      </dl>

      <div className="mt-6 border-l-[3px] border-accent-deep bg-[#141a13] px-3.5 py-3">
        <p className="flex items-center gap-2 text-[13px] font-bold text-ink-1">
          <span className="text-accent">
            <IconJava size={13} />
          </span>
          ServerBox 是什么
        </p>
        <p className="mt-2 max-w-[70ch] text-[12.5px] leading-[1.7] text-ink-2">
          一个给「家里有台闲置电脑」的人用的 Minecraft 开服器。不是客户端启动器（那是 PCL / HMCL 的活），
          也不是给运维用的面板 —— 目标就一个：30 秒把服开起来，然后忘了它。
        </p>
      </div>
    </div>
  )
}
