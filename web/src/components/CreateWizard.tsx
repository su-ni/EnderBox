import { useEffect, useState, type ReactNode } from 'react'
import { api } from '../api'
import { IconBack, IconCheck, IconPlus, IconTypeBlock } from './Icons'
import { StepInput } from './StepInput'
import { TYPE_LABEL, javaForVersion } from '../types'
import { lacksJava, useLocalJava } from '../useLocalJava'
import { ServerPicker, type Picked } from './ServerPicker'
import { DownloadBar } from './DownloadBar'

type Props = {
  onDone: (newId?: string) => void
  onCancel: () => void
  onError: (m: string) => void
}

const STEPS = [
  { t: '类型', s: '选服务端' },
  { t: '版本与构建', s: '选游戏版本' },
  { t: '运行环境', s: 'Java 与服务端文件' },
  { t: '基本信息', s: '名称 · 端口 · 内存' },
  { t: '确认创建', s: '检查并开服' },
]

/** 每种服务端一句人话解释 —— 新手就卡在「这些名词都是啥」这一步 */
const TYPE_INFO: Record<string, { desc: string; chips: { t: string; tone?: 'g' | 'w' | 'd' }[] }> = {
  paper: {
    desc: '性能优化过的原版，支持插件。开服首选，插件生态最全。',
    chips: [{ t: '推荐', tone: 'g' }, { t: '插件' }],
  },
  vanilla: { desc: '官方服务端。纯净无改动，想要原汁原味或做红石机器选它。', chips: [{ t: '无插件' }] },
  fabric: { desc: '轻量模组加载器。装模组首选，更新快，客户端同步方便。', chips: [{ t: '模组' }] },
  forge: {
    desc: '老牌模组加载器。大型整合包多基于它，兼容性最广、体积也最大。',
    chips: [{ t: '模组' }, { t: '整合包' }],
  },
  neoforge: { desc: 'Forge 分叉出来的现代化版本，新版本模组越来越多往这边走。', chips: [{ t: '模组' }] },
  velocity: {
    desc: '代理端。把多个服务器连成一个入口，玩家无感跨服切换。',
    chips: [{ t: '代理' }, { t: '进阶' }],
  },
  bungeecord: {
    desc: '老一代代理端。仅在你已有 Bungee 架构时才需要，新项目建议上面那个。',
    chips: [{ t: '代理' }, { t: '已过时', tone: 'd' }],
  },
}

function Field({ label, desc, children }: { label: string; desc?: string; children: ReactNode }) {
  return (
    <label className="flex min-w-0 flex-col gap-2">
      <span className="text-[13px] font-semibold text-ink-1">{label}</span>
      {children}
      {desc && <span className="text-[11.5px] leading-relaxed text-ink-3">{desc}</span>}
    </label>
  )
}

function Toggle({
  on,
  onChange,
  title,
  desc,
}: {
  on: boolean
  onChange: (v: boolean) => void
  title: string
  desc: string
}) {
  return (
    <div className="flex items-start gap-3 py-2.5">
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label={title}
        onClick={() => onChange(!on)}
        className={`tg-sw${on ? ' on' : ''}`}
      />
      <div>
        <div className="text-[13px] font-semibold text-ink-1">{title}</div>
        <p className="text-[11.5px] leading-relaxed text-ink-3">{desc}</p>
      </div>
    </div>
  )
}

function StepBody({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h2 className="mb-4 border-b-[3px] border-black pb-2.5 text-[16px] font-bold text-ink-1">{title}</h2>
      {children}
    </section>
  )
}

export function CreateWizard({ onDone, onCancel, onError }: Props) {
  // 当前步骤同步进 URL：刷新页面不会把进度清零，误点返回也不会前功尽弃
  const [step, setStep] = useState(() => {
    const n = Number(new URLSearchParams(location.search).get('step') ?? '1')
    return Number.isFinite(n) ? Math.min(STEPS.length - 1, Math.max(0, n - 1)) : 0
  })
  const [busy, setBusy] = useState(false)
  const [agree, setAgree] = useState(false)
  // 选了哪个构建由 ServerPicker 给，不再由前端编版本号
  const [picked, setPicked] = useState<Picked | null>(null)
  // 创建流程分两段：先把实例建出来，再下载服务端。
  // 分开是因为「建实例」几乎不会失败，而「下载」会 —— 得能单独重试。
  const [phase, setPhase] = useState<'idle' | 'downloading' | 'failed'>('idle')
  const [newId, setNewId] = useState('')
  const [taskId, setTaskId] = useState('')
  const [dlErr, setDlErr] = useState('')
  // 本机 Java 的最高主版本 —— 判断「选的这个构建在这台机器上跑不跑得起来」
  const { max: javaMax } = useLocalJava()
  // 后端说哪些类型接了自动下载。以此为准，而不是前端写死 ——
  // 哪天 Forge 接上了，这里自动就放开了，不用回来改这个文件。
  const [srcAvail, setSrcAvail] = useState<Record<string, { label: string; available: boolean; note: string }>>({})
  useEffect(() => {
    let alive = true
    api
      .fetchSources()
      .then((d) => {
        if (!alive) return
        const m: Record<string, { label: string; available: boolean; note: string }> = {}
        for (const s of d.items ?? []) m[s.type] = { label: s.label, available: s.available, note: s.note }
        setSrcAvail(m)
      })
      .catch(() => {})
    return () => {
      alive = false
    }
  }, [])
  const [form, setForm] = useState({
    name: '',
    type: 'paper',
    mcVersion: '1.21.4',
    jarPath: 'server.jar',
    maxMem: 4096,
    port: 25565,
    autoStart: true,
    autoRestart: true,
  })

  const set = <K extends keyof typeof form>(k: K, v: (typeof form)[K]) => setForm((f) => ({ ...f, [k]: v }))
  // 当前步骤写进 URL：刷新页面不清零进度，误点返回也不用重来
  useEffect(() => {
    const u = new URL(location.href)
    u.searchParams.set('view', 'create')
    u.searchParams.set('step', String(step + 1))
    history.replaceState(null, '', u)
  }, [step])

  // Java 需求优先用源站给的权威值（Mojang 元数据里直接写着），拿不到才按版本号兜底。
  // 我那张硬编码表遇到新版号制（26.x 这种）根本算不出来，不能当主来源。
  const java = picked?.javaMajor || javaForVersion(form.mcVersion)
  // 代理端没有 eula 这一步，别拿原版那套去卡它
  const needsEULA = form.type !== 'velocity' && form.type !== 'bungeecord'

  const canNext = [
    Boolean(form.type),
    Boolean(picked),
    form.jarPath.trim().length > 0,
    form.name.trim().length > 0,
    !needsEULA || agree,
  ][step]

  const submit = async () => {
    setBusy(true)
    try {
      const inst = await api.createInstance({
        ...form,
        name: form.name.trim(),
        jarPath: form.jarPath.trim(),
        minMem: Math.min(1024, form.maxMem),
      })
      if (!inst?.id) throw new Error('创建失败：后端没返回实例 id')
      setNewId(inst.id)

      // 同意书是用户勾的，落盘由我们代劳 —— 让用户自己去改 eula.txt 是新手第一个卡点，
      // 改错了只会看到服务端一句英文退出。写失败不拦着后面的流程，但要报出来。
      if (needsEULA) {
        try {
          await api.acceptEULA(inst.id)
        } catch (e) {
          onError(`世界建好了，但 eula.txt 没写成功：${(e as Error).message}`)
        }
      }

      // 起下载。失败不算「创建失败」—— 实例已经在了，服务端文件回头还能再下。
      try {
        const t = await api.downloadServerJar(inst.id, {
          type: form.type,
          version: form.mcVersion,
          build: picked?.build ?? 0,
          mirror: picked?.mirror ?? '',
        })
        setTaskId(t.id)
        setPhase('downloading')
      } catch (e) {
        setDlErr(`服务端下载没起来：${(e as Error).message}`)
        setPhase('failed')
      }
    } catch (e) {
      onError((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="scroll-thin flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto px-4 pt-5 pb-10 md:px-[26px]">
      <button type="button" onClick={onCancel} className="inline-flex items-center gap-1.5 self-start text-[12.5px] text-ink-3 hover:text-accent">
        <IconBack size={12} />
        返回世界列表
      </button>
      <h1 className="mt-2.5 text-[22px] leading-tight font-bold text-ink-1">新建世界</h1>
      <p className="mt-1.5 text-[12.5px] text-ink-3">
        5 步搞定。每一步都说清「为什么要选这个」，不用先懂服务端的区别。
      </p>

      {/* 手机屏上 5 步全列会占掉整屏，压成一条进度块；宽屏才展开成完整步骤条 */}
      <div className="pxb mt-5.5 mb-6 flex items-center gap-3 bg-panel px-4 py-3 sm:hidden">
        <span className="flex shrink-0 gap-1">
          {STEPS.map((s, i) => (
            <i
              key={s.t}
              className={`block h-3.5 w-5 ${i < step ? 'bg-accent-deep' : i === step ? 'bg-accent' : 'bg-panel-2'}`}
            />
          ))}
        </span>
        <span className="min-w-0 truncate text-[13px] font-semibold text-ink-1">
          {step + 1}. {STEPS[step].t}
        </span>
      </div>

      <div className="pxb mt-5.5 mb-7 hidden sm:flex sm:flex-row">
        {STEPS.map((s, i) => {
          const cls = i === step ? 'cur' : i < step ? 'done' : ''
          return (
            <button
              key={s.t}
              type="button"
              onClick={() => i < step && setStep(i)}
              disabled={i > step}
              aria-current={i === step}
              className={`step ${cls} flex-col items-start sm:flex-row sm:items-center ${i > step ? 'cursor-default' : ''}`}
            >
              <span className="step-n">{i < step ? <IconCheck size={11} /> : i + 1}</span>
              <span className="step-t min-w-0 text-[13px] font-semibold">
                <span className="block truncate">{s.t}</span>
                <span className="block text-[11px] font-normal text-ink-3">{s.s}</span>
              </span>
            </button>
          )
        })}
      </div>

      {step === 0 && (
        <StepBody title="选择服务端类型">
          <p className="mb-3.5 -mt-2 text-[12.5px] text-ink-3">
            不确定就选 Paper —— 90% 的人用这个
          </p>
          <div className="grid grid-cols-1 gap-3.5 sm:grid-cols-2 xl:grid-cols-4">
            {Object.keys(TYPE_LABEL).map((v) => {
              const t = TYPE_INFO[v]
              const on = form.type === v
              // 后端说这个类型还没接自动下载 —— 那就别让它可选。
              // 列出来（而不是藏起来）是为了让你知道「有这么个东西，只是现在走不通」。
              // 源站还没问出来时（srcAvail 为空）一律按可选处理，别误伤。
              const info = srcAvail[v]
              const off = info ? !info.available : false
              return (
                <button
                  key={v}
                  type="button"
                  onClick={() => set('type', v)}
                  disabled={off}
                  aria-pressed={on}
                  className={`type-card${on ? ' sel' : ''}`}
                >
                  {on && (
                    <span className="absolute top-2.5 right-2.5 grid h-4 w-4 place-items-center bg-accent-deep text-[#0c1108]">
                      <IconCheck size={10} />
                    </span>
                  )}
                  <span className="flex items-center gap-2.5">
                    <IconTypeBlock type={v} size={28} />
                    <span className="min-w-0">
                      <span className="block text-[14px] leading-tight font-bold text-ink-1">{TYPE_LABEL[v]}</span>
                      <span className="block mono-meta">{v.toUpperCase()}</span>
                    </span>
                  </span>
                  <span className="mt-2.5 block min-h-10 text-[12.5px] leading-relaxed text-ink-2">{t.desc}</span>
                  <span className="mt-2.5 block">
                    {off && <span className="chip chip-w">还不能自动下载</span>}
                    {t.chips.map((c) => (
                      <span key={c.t} className={`chip${c.tone ? ` chip-${c.tone}` : ''}`}>
                        {c.t}
                      </span>
                    ))}
                  </span>
                  {off && (
                    <span className="mt-2 block border-t-[3px] border-black pt-2 text-[11.5px] leading-relaxed text-[#e7dcb2]">
                      {info?.note}
                    </span>
                  )}
                </button>
              )
            })}
          </div>
        </StepBody>
      )}

      {step === 1 && (
        <StepBody title="选择游戏版本与构建">
          <ServerPicker
            type={form.type}
            value={picked}
            onPick={(p) => {
              setPicked(p)
              set('mcVersion', p.version)
            }}
          />
          {picked && (
            <p className="mt-4 text-[12.5px] leading-relaxed text-ink-2">
              装模组的话建议往下看两三个版本 —— 模组作者追新版本普遍慢半拍。
              只列正式版，快照和预发布版已经滤掉了。
            </p>
          )}
        </StepBody>
      )}

      {step === 2 && (
        <StepBody title="运行环境">
          <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
            <div className="px-card p-4">
              <div className="mb-3 flex items-center gap-2">
                <IconTypeBlock type="paper" size={16} />
                <b className="text-[13.5px] text-ink-1">Java 运行时</b>
                <span className="chip chip-g ml-auto">自动挑</span>
              </div>
              <p className="text-[12.5px] leading-relaxed text-ink-2">
                启动时扫一遍本机的 Java，挑一个版本够用的 —— 精确匹配优先，没有就往上取更高的。
                你不用自己填路径。
              </p>
              <dl className="mt-3 grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 text-[13px]">
                <dt className="eyebrow">这个版本要</dt>
                <dd>
                  {lacksJava(java, javaMax) ? (
                    <span className="chip chip-d">本机缺 Java {java}</span>
                  ) : (
                    <span className="chip chip-g">Java {java} 或更高</span>
                  )}
                </dd>
                <dt className="eyebrow">数值来自</dt>
                <dd className="text-[12.5px] text-ink-2">
                  {picked?.javaMajor ? 'Mojang 官方元数据（权威值）' : '按版本号推断（没拿到权威值）'}
                </dd>
              </dl>
            </div>

            <div className="px-card p-4">
              <div className="mb-3 flex items-center gap-2">
                <IconTypeBlock type="vanilla" size={16} />
                <b className="text-[13.5px] text-ink-1">服务端文件</b>
                <span className="chip chip-g ml-auto">自动下载</span>
              </div>
              <p className="text-[12.5px] leading-relaxed text-ink-2">
                创建后直接从源站下到实例目录里，带 SHA 校验和断点续传。
                下完会自动把端口和标语写进 server.properties —— 也就是你在下一步填的那些值。
              </p>
              {picked && (
                <dl className="mt-3 grid grid-cols-[auto_1fr] items-center gap-x-4 gap-y-2 text-[13px]">
                  <dt className="eyebrow">将下载</dt>
                  <dd className="mono-value break-all">{picked.name}</dd>
                  <dt className="eyebrow">大小</dt>
                  <dd className="mono-value">{(picked.size / 1048576).toFixed(1)} MB</dd>
                </dl>
              )}
            </div>
          </div>

          <div className="px-card mt-5 p-4">
            <h3 className="mb-2 text-[13.5px] font-bold text-ink-1">关于构建号</h3>
            <p className="text-[12.5px] leading-relaxed text-ink-2">
              Paper 的构建号（build）是同一个游戏版本下的第几次打包，编号越大越新。
              列表按从新到旧排，但<b className="text-ink-1">默认给你选的是最新的「稳定」构建</b>
              —— Paper 对新版本会先发 BETA，直接上最新的可能踩到测试中的问题。
              某个版本一个稳定构建都没有时，界面会明确告诉你。原版没有构建号，一个版本就一个包。
            </p>
          </div>
        </StepBody>
      )}

      {step === 3 && (
        <StepBody title="基本信息">
          <div className="px-card p-4">
            <div className="grid grid-cols-1 gap-5 md:grid-cols-3">
              <Field label="世界名称" desc="显示在列表和日志里的名字，随便取。">
                <input
                  className="px-input"
                  value={form.name}
                  onChange={(e) => set('name', e.target.value)}
                  placeholder="生存服 · 长期档"
                />
              </Field>
              <Field label="端口" desc="朋友连进来的地址就是 你的IP:端口。">
                <StepInput
                  label="端口"
                  value={form.port}
                  min={1}
                  max={65535}
                  onChange={(v) => set('port', Number(v))}
                />
              </Field>
              <Field label="内存分配 (MB)" desc="原版 2 GB 够 5 个人玩；装 20 个插件建议给 4 GB。">
                <StepInput
                  label="内存分配"
                  value={form.maxMem}
                  step={512}
                  min={512}
                  onChange={(v) => set('maxMem', Number(v))}
                />
              </Field>
            </div>

            <div className="mt-4 border-t-[3px] border-black pt-2">
              <Toggle
                on={form.autoStart}
                onChange={(v) => set('autoStart', v)}
                title="开机自动启动"
                desc="守护进程起来时自动把它拉起来，不用你手动点。"
              />
              <Toggle
                on={form.autoRestart}
                onChange={(v) => set('autoRestart', v)}
                title="崩溃自动重启"
                desc="进程异常退出时自动重开。崩之前最后 200 行日志会单独存档，方便查原因。"
              />
            </div>
          </div>
        </StepBody>
      )}

      {step === 4 && phase !== 'idle' && (
        <StepBody title={phase === 'downloading' ? '正在准备服务端' : '服务端下载没成功'}>
          <div className="px-card p-4">
            <p className="mb-3.5 text-[12.5px] leading-relaxed text-ink-2">
              世界已经建好了。这一步是在把服务端文件下到实例目录里 ——
              下完了回概览页点启动就能开服。
            </p>
            {phase === 'downloading' && taskId && (
              <DownloadBar taskId={taskId} onDone={() => onDone(newId)} />
            )}
            {phase === 'failed' && (
              <>
                <p className="border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] leading-relaxed text-[#ffc2c2]">
                  {dlErr}
                </p>
                <div className="mt-3.5 flex flex-wrap gap-2.5">
                  <button
                    type="button"
                    className="btn btn-grass"
                    onClick={async () => {
                      setDlErr('')
                      try {
                        const t = await api.downloadServerJar(newId, {
                          type: form.type,
                          version: form.mcVersion,
                          build: picked?.build ?? 0,
                          mirror: picked?.mirror ?? '',
                        })
                        setTaskId(t.id)
                        setPhase('downloading')
                      } catch (e) {
                        setDlErr(`还是不行：${(e as Error).message}`)
                      }
                    }}
                  >
                    重试下载
                  </button>
                  <button type="button" className="btn" onClick={() => onDone(newId)}>
                    先不管，进世界
                  </button>
                </div>
                <p className="mt-3 text-[12px] leading-relaxed text-ink-3">
                  点「先不管」也能进去 —— 之后在「配置」页的「服务端文件」里可以重新下载。
                </p>
              </>
            )}
          </div>
        </StepBody>
      )}

      {step === 4 && phase === 'idle' && (
        <StepBody title="确认创建">
          <div className="px-card">
            {[
              ['服务端', `${TYPE_LABEL[form.type]} ${form.mcVersion}`],
              ['构建', picked ? (picked.build > 0 ? `#${picked.build}（${picked.name}）` : picked.name) : '（还没选）'],
              ['运行环境', `自动挑一个够用的 Java（这个版本需要 ${java}+）`],
              ['世界名称', form.name.trim() || '（还没填）'],
              ['监听端口', String(form.port)],
              ['服务端文件', form.jarPath.trim() || '（还没填）'],
              ['开机自启 / 崩溃重启', `${form.autoStart ? '开' : '关'} / ${form.autoRestart ? '开' : '关'}`],
            ].map(([k, v]) => (
              <div
                key={k}
                className="grid grid-cols-1 gap-1 border-b-[3px] border-black px-4 py-3 last:border-b-0 sm:grid-cols-[170px_1fr] sm:gap-3.5"
              >
                <span className="eyebrow">{k}</span>
                <span className="text-[13.5px] break-words text-ink-1">{v}</span>
              </div>
            ))}
          </div>

          {needsEULA && (
            <div className="px-card mt-4 p-4">
              <button
                type="button"
                role="checkbox"
                aria-checked={agree}
                onClick={() => setAgree((v) => !v)}
                className="flex w-full items-start gap-3 bg-transparent p-0 text-left"
              >
                <span
                  className="mt-[1px] grid h-5 w-5 shrink-0 place-items-center"
                  style={{
                    background: agree ? 'var(--color-accent-deep)' : '#0d100d',
                    boxShadow: '0 -2px 0 0 #000, 0 2px 0 0 #000, -2px 0 0 0 #000, 2px 0 0 0 #000',
                  }}
                >
                  {agree && <IconCheck size={12} />}
                </span>
                <span className="min-w-0">
                  <span className="block text-[13px] font-semibold text-ink-1">
                    我已阅读并同意 Minecraft 最终用户许可协议
                  </span>
                  <span className="mt-1 block text-[11.5px] leading-relaxed text-ink-3">
                    点一下左边那个方块就行 —— 勾了我们会替你写 eula.txt。
                    不勾的话服务端第一次启动会直接退出，日志里只有一句英文，新手基本都会卡在这。
                    {' '}
                    <a
                      href="https://aka.ms/MinecraftEULA"
                      target="_blank"
                      rel="noreferrer"
                      className="text-cyan hover:text-accent"
                      onClick={(e) => e.stopPropagation()}
                    >
                      看原文
                    </a>
                  </span>
                </span>
              </button>
            </div>
          )}

          <p className="mt-3.5 text-[12.5px] leading-relaxed text-ink-2">
            点下面的按钮会：建目录写配置 → 写 eula.txt →{' '}
            <b className="text-ink-1">把服务端下载进去</b>。下完回概览页点「启动」就行。
            启动前它会先体检一遍（jar 在不在、Java 够不够、端口占没占），有问题会把清单和修复按钮直接摆出来。
          </p>
        </StepBody>
      )}

      <div className="px-card mt-7 flex flex-wrap items-center gap-3 px-4 py-3.5">
        <span className="text-[12.5px] text-ink-3">
          第 {step + 1} / {STEPS.length} 步 · {STEPS[step].t}
        </span>
        <div className="ml-auto flex w-full gap-2.5 sm:w-auto">
          <button type="button" className="btn btn-ghost" onClick={onCancel}>
            取消
          </button>
          <button
            type="button"
            className="btn"
            disabled={step === 0}
            onClick={() => setStep((s) => Math.max(0, s - 1))}
          >
            上一步
          </button>
          {step < STEPS.length - 1 ? (
            <button
              type="button"
              className="btn btn-grass"
              disabled={!canNext}
              onClick={() => setStep((s) => s + 1)}
            >
              下一步 · {STEPS[step + 1].t}
            </button>
          ) : (
            <button
              type="button"
              className="btn btn-grass"
              disabled={busy || !canNext || phase !== 'idle'}
              onClick={submit}
            >
              <IconPlus size={13} />
              {busy ? '创建中…' : phase === 'downloading' ? '准备中…' : '创建这个世界'}
            </button>
          )}
        </div>
      </div>
    </div>
  )
}
