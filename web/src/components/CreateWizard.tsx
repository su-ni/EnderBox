import { useEffect, useState } from 'react'
import { api } from '../api'
import { ConHead, Icon } from './design'
import { StepInput } from './StepInput'
import { TYPE_LABEL, javaForVersion } from '../types'
import { lacksJava, useLocalJava } from '../useLocalJava'
import { ServerPicker, type Picked } from './ServerPicker'
import { JavaPicker } from './JavaPicker'
import { DownloadBar } from './DownloadBar'
import { fmtSizeMaybe } from './ui'

type Props = {
  onDone: (newId?: string) => void
  onCancel: () => void
  onError: (m: string) => void
}

/** 类型卡的图标（照设计稿 wizard.html 的 .kind）：原版/Paper 用 jar，模组系用方块/工作台，代理端用命令块。 */
const KIND_ICON: Record<string, string> = {
  vanilla: 'jar',
  paper: 'jar',
  fabric: 'cube',
  forge: 'table',
  neoforge: 'table',
  mohist: 'cmd',
  arclight: 'cmd',
  velocity: 'cmd',
  bungeecord: 'cmd',
}

const STEPS = [
  { t: '类型', s: '选服务端' },
  { t: '版本与构建', s: '选游戏版本' },
  { t: '运行环境', s: 'Java 与服务端文件' },
  { t: '基本信息', s: '名称 · 端口 · 内存' },
  { t: '确认创建', s: '检查并开服' },
]

/**
 * URL 上**显式**点了版本才优先选它（?view=create&type=forge&mc=1.20.4）。
 * 没点就交给 ServerPicker 按原设计落到最新版本 —— 对「新建」来说最新是好默认值。
 * 这个口子主要是给深链和截图核对用的：无头浏览器点不了按钮，只能靠 URL 直达某一步。
 */
const URL_MC = new URLSearchParams(location.search).get('mc') ?? ''

/** 每种服务端一句人话解释 —— 新手就卡在「这些名词都是啥」这一步。
 *  ⚠ 每句都要能塞进卡片的两行（`.kind .ds` 是 line-clamp:2，超了会截断加省略号）：
 *  卡片宽 ~200px、描述区 ~171px，两行约 26~30 个汉字位。改文案先数这两行放不放得下。 */
const TYPE_INFO: Record<string, { desc: string; chips: { t: string; tone?: 'g' | 'w' | 'd' }[] }> = {
  paper: {
    desc: '性能优化过的原版，支持插件。开服首选，生态最全。',
    chips: [{ t: '推荐', tone: 'g' }, { t: '插件' }],
  },
  vanilla: { desc: '官方服务端，纯净无改动。原汁原味、红石机器选它。', chips: [{ t: '无插件' }] },
  fabric: { desc: '轻量模组加载器，装模组首选，更新快。', chips: [{ t: '模组' }] },
  forge: {
    desc: '老牌模组加载器，大型整合包多基于它，兼容最广。',
    chips: [{ t: '模组' }, { t: '整合包' }],
  },
  neoforge: { desc: 'Forge 分叉出来的现代化版本，新模组多往这边走。', chips: [{ t: '模组' }] },
  // 混合端：模组和插件能同时装。desc 必须把「首次开服会再下几十 MB」写出来 ——
  // 这两个端下下来的不是一个装完就能跑的小 jar，它们首次启动会自己把 Forge 的库
  // 拉进实例目录（Arclight 那边是启动时现装），不给提示的话用户会以为卡死了。
  mohist: {
    desc: '混合端：模组和插件同时装。首启再装库，会慢一截。',
    chips: [{ t: '模组 + 插件' }, { t: '需联网' }],
  },
  arclight: {
    desc: '混合端，和上面一个路子。⚠ 走第三方加速代理下载。',
    chips: [{ t: '模组 + 插件' }, { t: '第三方代理', tone: 'w' }],
  },
  velocity: {
    desc: '代理端：多个服务器连成一个入口，跨服无感。',
    chips: [{ t: '代理' }, { t: '进阶' }],
  },
  bungeecord: {
    desc: '老一代代理端，新项目建议用上面那个。',
    chips: [{ t: '代理' }, { t: '已过时', tone: 'd' }],
  },
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
  // 类型能从 URL 预置：?view=create&type=forge
  // 和上面的 step 同一个套路 —— 除了让「新建时直达某个加载器」可分享，
  // 也让这个向导的每一步都能被无头浏览器直接截到（否则点不了按钮就进不去）。
  // 类型必须校验：手改 URL 可以塞任意字符串，直接吃下去会把后端搞懵。
  const [form, setForm] = useState(() => {
    const t = new URLSearchParams(location.search).get('type') ?? ''
    return {
      name: '',
      // 不预选类型：必须先自己点一个（用户拍板）。URL 里显式点名了才认 ——
      // 「下一步」已被 canNext[0] = Boolean(form.type) 挡住，不用再改逻辑。
      type: t in TYPE_LABEL ? t : '',
      mcVersion: URL_MC || '1.21.4',
      jarPath: 'server.jar',
      // 空串 = 自动挑一个够用的。做到「运行环境」那一步时用户可以改。
      javaPath: '',
      maxMem: 4096,
      port: 25565,
      autoStart: true,
      autoRestart: true,
    }
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
        javaPath: form.javaPath.trim(),
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
          tag: picked?.tag ?? '',
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


  /** 下载失败后的重试。抽出来只为让 render 里少一坨内联 async。 */
  const retryDownload = async () => {
    setDlErr('')
    try {
      const t = await api.downloadServerJar(newId, {
        type: form.type,
        version: form.mcVersion,
        build: picked?.build ?? 0,
        tag: picked?.tag ?? '',
        mirror: picked?.mirror ?? '',
      })
      setTaskId(t.id)
      setPhase('downloading')
    } catch (e) {
      setDlErr(`还是不行：${(e as Error).message}`)
      setPhase('failed')
    }
  }

  return (
    /* ⚠ 新建向导不走 PageFrame（它是 view=create 的独立分支），所以拿不到 .main 的样式。
       底部必须自己留出健康条的高度 —— 否则最后那排「取消 / 上一步 / 下一步」会被 HUD 压住。
       版式照 docs/design/pixel-refresh/wizard.html：版心 .wz-shell 不再封 1080px（超宽屏两侧
       空太多），左右 gutter 与标准页 .content 一致 = 30px（--gutter）。
       五个步骤全落在一只 .panel 里。 */
    <div className="scroll-thin flex min-h-0 min-w-0 flex-1 flex-col overflow-y-auto px-[30px] pt-4 pb-[calc(var(--hud-h)+10px)]">
      {/* max-width 的放宽在 design-overrides.css 的 .wz-shell（同层覆盖设计稿口径） */}
      <div className="wz-shell">
        <button type="button" onClick={onCancel} className="btn btn-ghost sm reveal self-start">
          <Icon name="chev-l" size={12} />
          返回世界列表
        </button>

        <div className="sec-head reveal" style={{ marginTop: 14 }}>
          <span className="idx">NEW</span>
          <h2>建一台自己的服务器</h2>
          <span className="sub">选类型 → 选版本 → 备运行环境 → 填基本信息 → 确认创建</span>
        </div>

        {/* 步骤条：只有「已经走过的步」可点回去，未来步禁用（照现有实现） */}
        <div className="wz-steps reveal">
          {STEPS.map((st, i) => {
            const cls = i === step ? ' on' : i < step ? ' done wz-done' : ' future'
            return (
              <button
                key={st.t}
                type="button"
                onClick={() => i < step && setStep(i)}
                disabled={i > step}
                aria-current={i === step}
                className={`wz-step${cls}`}
              >
                <span className="no">{i < step ? <Icon name="check" size={11} /> : i + 1}</span>
                <span className="tx">
                  <b>{st.t}</b>
                  <span>STEP {i + 1}</span>
                </span>
              </button>
            )
          })}
        </div>

        <div className="panel reveal">
          <i className="corner" />

          {/* ① 类型：不预选，必须自己点一个 */}
          {step === 0 && (
            <div className="cfg-pane on">
              <ConHead icon="cmd" title="选服务端" sub="不预选 —— 必须自己点一个" />
              <div style={{ padding: '14px 16px' }}>
                <div className="kinds">
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
                        className={`kind${on ? ' on' : ''}${off ? ' off' : ''}`}
                      >
                        <Icon name={KIND_ICON[v] ?? 'cube'} size={30} className="ico" />
                        <b className="nm">{TYPE_LABEL[v]}</b>
                        {/* 描述单独一行、占满卡片宽度（连图标下面那格一起）。
                            以前它是「图标 | 文字块」两列里的第二列，只剩 127px ——
                            一句话要折三行，卡片高矮差到 108px（实测 202 / 310）。
                            原来这里还有枚「还不能自动下载」小标签，一并去掉了：
                            不可用卡显示的就是后端 note，开头那句「还没接……」已经说明同一件事，
                            说两遍要多占一行、卡片就不齐了。 */}
                        <span className="ds">{off ? info?.note || t.desc : t.desc}</span>
                      </button>
                    )
                  })}
                </div>
                <div className="hintbox" style={{ marginTop: 14 }}>
                  <Icon name="info" style={{ color: 'var(--cyan)' }} />
                  「插件」「模组」不是一回事：Paper 系吃 <code>plugins</code>，Fabric/Forge 吃{' '}
                  <code>mods</code>。选错了得重建，所以先想清楚。
                </div>
              </div>
            </div>
          )}

          {/* ② 版本与构建：版本列表 + 构建列表（picked 决定能否下一步） */}
          {step === 1 && (
            <div className="cfg-pane on">
              <ConHead icon="time" title="版本与构建" sub="没选构建之前，「下一步」一直是禁用的">
                <div className="ml-auto" />
                <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
                  {TYPE_LABEL[form.type] || '服务端'}
                </span>
              </ConHead>
              <ServerPicker
                type={form.type}
                value={picked}
                preferVersion={URL_MC || undefined}
                onPick={(p) => {
                  setPicked(p)
                  set('mcVersion', p.version)
                }}
              />
            </div>
          )}

          {/* ③ 运行环境：jarPath 非空才放行 */}
          {step === 2 && (
            <div className="cfg-pane on">
              <ConHead icon="jar" title="运行环境" sub="Java 自动挑，服务端文件下载前还没落地" />
              <div style={{ padding: '14px 16px' }}>
                <div className="kv-list" style={{ marginBottom: 14 }}>
                  <div className="kv2">
                    <span className="k">这个版本需要</span>
                    <span
                      className="v"
                      style={{ color: lacksJava(java, javaMax) ? 'var(--bad)' : 'var(--ok)' }}
                    >
                      Java {java}
                      {lacksJava(java, javaMax) ? ' · 本机还没有' : ''}
                    </span>
                  </div>
                  <div className="kv2">
                    <span className="k">本机检测到</span>
                    <span className="v">{(javaMax ?? 0) > 0 ? `最高 Java ${javaMax}` : '还没扫到 Java'}</span>
                  </div>
                </div>

                <JavaPicker value={form.javaPath} onChange={(p) => set('javaPath', p)} needMajor={java} />

                <div style={{ marginTop: 16 }}>
                  <div className="eyebrow" style={{ marginBottom: 8 }}>
                    服务端文件（启动文件）
                  </div>
                  <div className="field" style={{ paddingTop: 0 }}>
                    <div>
                      <div className="lb">jarPath</div>
                      <div className="hint">
                        默认就写{' '}
                        <span style={{ fontFamily: 'var(--f-term)', fontSize: 14 }}>server.jar</span>
                        ，下完自动落到实例目录
                      </div>
                    </div>
                    <input
                      className="inp"
                      value={form.jarPath}
                      onChange={(e) => set('jarPath', e.target.value)}
                    />
                  </div>
                  <div className="pol-foot">
                    <div className="row">
                      <span className="k">下载源</span>
                      <span className="v">自动（官方不通就换镜像）</span>
                    </div>
                    <div className="row">
                      <span className="k">断点续传</span>
                      <span className="v">连接被掐断会接着已有进度下</span>
                    </div>
                    {picked && (
                      <div className="row">
                        <span className="k">{form.type === 'forge' ? '先下载' : '将下载'}</span>
                        <span className="v" style={{ wordBreak: 'break-all' }}>
                          {picked.name}
                        </span>
                      </div>
                    )}
                    {picked && (
                      <div className="row">
                        <span className="k">{form.type === 'forge' ? '安装器大小' : '大小'}</span>
                        <span className="v">{fmtSizeMaybe(picked.size)}</span>
                      </div>
                    )}
                  </div>
                </div>

                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 10, lineHeight: 1.7 }}>
                  {form.type === 'forge'
                    ? 'Forge 分两步：先下安装器，再自动跑一遍装依赖（额外下几十到一百多 MB）。'
                      + '装完「服务端文件」会自动指向该启动的文件，不用你认。'
                    : '创建后直接从源站下到实例目录：多线程下载、断线自动续传；下完自动写好端口与标语。'}
                  <br />
                  「这个版本要 Java {java}」的数值来自
                  {picked?.javaFrom === 'mojang'
                    ? ' Mojang 官方元数据（权威值）'
                    : picked?.javaFrom === 'versionline'
                      ? ' 按版本线推断（上游 API 不提供这个信息）'
                      : ' 按版本号推断（没拿到权威值）'}
                  。
                </p>
              </div>
            </div>
          )}

          {/* ④ 基本信息：名称非空才放行 */}
          {step === 3 && (
            <div className="cfg-pane on">
              <ConHead icon="book" title="基本信息" sub="这些之后都能在「配置」里改" />
              <div style={{ padding: '6px 16px 16px' }}>
                <div className="field">
                  <div>
                    <div className="lb">实例名</div>
                    <div className="hint">显示在列表和日志里的名字，填了才能进下一步</div>
                  </div>
                  <input
                    className="inp"
                    value={form.name}
                    onChange={(e) => set('name', e.target.value)}
                    placeholder="生存服 · 长期档"
                  />
                </div>
                <div className="field">
                  <div>
                    <div className="lb">端口</div>
                    <div className="hint">朋友连进来的地址就是 IP:端口；下完会自动写进 server.properties</div>
                  </div>
                  <StepInput
                    label="端口"
                    value={form.port}
                    min={1}
                    max={65535}
                    onChange={(v) => set('port', Number(v))}
                  />
                </div>
                <div className="field">
                  <div>
                    <div className="lb">内存上限 (MB)</div>
                    <div className="hint">默认 4096；最小内存自动取 min(1024, 上限)</div>
                  </div>
                  <StepInput
                    label="内存分配"
                    value={form.maxMem}
                    step={512}
                    min={512}
                    onChange={(v) => set('maxMem', Number(v))}
                  />
                </div>
                <div className="field">
                  <div>
                    <div className="lb">Java 路径</div>
                    <div className="hint">留空 = 按 MC 版本自动挑（这个版本要 Java {java}+）</div>
                  </div>
                  <input
                    className="inp"
                    value={form.javaPath}
                    onChange={(e) => set('javaPath', e.target.value)}
                    placeholder="留空 = 自动挑最合适的"
                  />
                </div>

                <div style={{ marginTop: 16, borderTop: '1px solid var(--line-1)', paddingTop: 4 }}>
                  {[
                    {
                      on: form.autoStart,
                      flip: () => set('autoStart', !form.autoStart),
                      title: '开机自动启动',
                      desc: '守护进程起来时自动把它拉起来，不用你手动点。',
                    },
                    {
                      on: form.autoRestart,
                      flip: () => set('autoRestart', !form.autoRestart),
                      title: '崩溃自动重启',
                      desc: '进程异常退出时自动重开。崩之前最后 200 行日志会单独存档，方便查原因。',
                    },
                  ].map((r) => (
                    <div key={r.title} style={{ display: 'flex', alignItems: 'flex-start', gap: 12, padding: '10px 0' }}>
                      <button
                        type="button"
                        role="switch"
                        aria-checked={r.on}
                        aria-label={r.title}
                        onClick={r.flip}
                        className={`tog${r.on ? ' on' : ''}`}
                      >
                        <span className="knob" />
                      </button>
                      <div>
                        <div style={{ fontSize: 13, fontWeight: 600, color: 'var(--ink-1)' }}>{r.title}</div>
                        <p style={{ fontSize: 12, color: 'var(--ink-3)', lineHeight: 1.6 }}>{r.desc}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            </div>
          )}

          {/* ⑤ 确认创建 · idle */}
          {step === 4 && phase === 'idle' && (
            <div className="cfg-pane on">
              <ConHead
                icon="check"
                iconColor="var(--ok)"
                title="确认创建"
                sub="最后一遍，勾了同意书才能点创建"
              />
              <div style={{ padding: '14px 16px' }}>
                <div className="kv-list">
                  {[
                    /* ⚠ 兜底不能省：TYPE_LABEL[''] 是 undefined，深链带了个不在表里的 type
                       （或类型还没选）时会把「服务端 undefined 1.21.4」直接印在确认页上。 */
                    [
                      '服务端',
                      TYPE_LABEL[form.type]
                        ? `${TYPE_LABEL[form.type]} ${form.mcVersion}`
                        : `${form.mcVersion}（类型未选）`,
                    ],
                    // Forge 的 id 只是列表里的行号（它的版本号不是整数），显示成「#1」会让人以为那是构建号。
                    // 它有意义的身份是 tag，也就是 1.20.4-49.0.30 这种。
                    [
                      '构建',
                      picked
                        ? form.type === 'forge'
                          ? picked.tag || picked.name
                          : picked.build > 0
                            ? `#${picked.build}（${picked.name}）`
                            : picked.name
                        : '（还没选）',
                    ],
                    [
                      '运行环境',
                      form.javaPath.trim()
                        ? `指定用 ${form.javaPath.trim()}`
                        : `自动挑一个够用的 Java（这个版本需要 ${java}+）`,
                    ],
                    ['实例名', form.name.trim() || '（还没填）'],
                    ['端口', String(form.port)],
                    ['内存上限', `${form.maxMem} MB`],
                    ['服务端文件', form.jarPath.trim() || '（还没填）'],
                    ['开机自启 / 崩溃重启', `${form.autoStart ? '开' : '关'} / ${form.autoRestart ? '开' : '关'}`],
                  ].map(([k, v]) => (
                    <div className="kv2" key={k}>
                      <span className="k">{k}</span>
                      <span className="v" style={{ wordBreak: 'break-word' }}>
                        {v}
                      </span>
                    </div>
                  ))}
                </div>

                {needsEULA ? (
                  <div
                    style={{
                      marginTop: 16,
                      padding: 14,
                      background: 'rgba(242,193,78,.08)',
                      borderLeft: '3px solid var(--warn)',
                    }}
                  >
                    <button
                      type="button"
                      role="checkbox"
                      aria-checked={agree}
                      onClick={() => setAgree((v) => !v)}
                      style={{
                        display: 'flex',
                        gap: 10,
                        alignItems: 'flex-start',
                        cursor: 'pointer',
                        background: 'none',
                        border: 0,
                        padding: 0,
                        textAlign: 'left',
                      }}
                    >
                      <span className={`chk${agree ? ' on' : ''}`} style={{ marginTop: 2 }} />
                      <span style={{ fontSize: 12.5, lineHeight: 1.7, color: 'var(--ink-2)' }}>
                        我同意 Minecraft 的 <b style={{ color: 'var(--ink-1)' }}>EULA</b>（
                        <a
                          href="https://aka.ms/MinecraftEULA"
                          target="_blank"
                          rel="noreferrer"
                          style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--cyan)' }}
                          onClick={(e) => e.stopPropagation()}
                        >
                          aka.ms/MinecraftEULA
                        </a>
                        ）。同意后由面板代写 eula.txt —— 不让新手卡在「手工改错一行、服务端一句英文退出」上。
                      </span>
                    </button>
                  </div>
                ) : (
                  <p style={{ marginTop: 16, fontSize: 12.5, color: 'var(--ink-3)' }}>
                    代理端（Velocity / BungeeCord）没有 eula 这一步。
                  </p>
                )}

                <div className="pol-foot">
                  <div className="row">
                    <span className="k">会写到哪里</span>
                    <span className="v">{'实例目录 instances/<新 id>'}</span>
                  </div>
                  <div className="row">
                    <span className="k">创建之后</span>
                    <span className="v">
                      {form.type === 'forge'
                        ? '先下安装器再自动装好 Forge，进度在「下载与安装」里看'
                        : '立刻开始下载服务端文件，进度在「下载与安装」里看'}
                    </span>
                  </div>
                  <div className="row">
                    <span className="k">启动之前</span>
                    <span className="v">会先体检一遍（jar 在不在、Java 够不够、端口占没占）</span>
                  </div>
                </div>
              </div>
            </div>
          )}

          {/* ⑤ 确认创建 · 下载中 / 下载失败（phase !== idle） */}
          {step === 4 && phase !== 'idle' && (
            <div className="cfg-pane on">
              <ConHead
                icon={phase === 'downloading' ? 'dl' : 'ban'}
                iconColor={phase === 'downloading' ? 'var(--cyan)' : 'var(--bad)'}
                title={
                  phase === 'downloading'
                    ? form.type === 'forge'
                      ? '正在下载并安装 Forge'
                      : '正在准备服务端'
                    : form.type === 'forge'
                      ? 'Forge 没能装好'
                      : '服务端下载没成功'
                }
              >
                <span className="badge wl" style={{ marginLeft: 4 }}>
                  世界已经建好了
                </span>
                <div className="ml-auto" />
                <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
                  下载失败不算创建失败，回头能再下
                </span>
              </ConHead>
              <div style={{ padding: '14px 16px' }}>
                {phase === 'downloading' && taskId && (
                  <DownloadBar taskId={taskId} onDone={() => onDone(newId)} />
                )}
                {phase === 'failed' && (
                  <>
                    <div className="nbox d">
                      <div className="hd">
                        <Icon name="ban" />
                        {dlErr}
                      </div>
                      世界已经建好了，只是服务端文件没下来。点「重试下载」再来一次；也可以先不管 ——
                      之后在「配置」页的「服务端文件」里能重新下载。
                    </div>
                    <div style={{ marginTop: 14, display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                      <button type="button" className="btn btn-primary" onClick={() => void retryDownload()}>
                        重试下载
                      </button>
                      <button type="button" className="btn" onClick={() => onDone(newId)}>
                        先不管，进世界
                      </button>
                    </div>
                  </>
                )}
                {phase === 'downloading' && (
                  <div
                    className="con-foot"
                    style={{ marginTop: 14, borderTop: '1px solid var(--line-1)', padding: '12px 0 0', background: 'none' }}
                  >
                    <Icon name="check" style={{ color: 'var(--ok)' }} />
                    {needsEULA ? 'eula.txt 已写好 · ' : ''}端口与标语会在下完后写进 <code>server.properties</code>
                  </div>
                )}
              </div>
            </div>
          )}

          {/* 底部操作条：第 N / 5 步 + 取消 / 上一步 / 下一步·下一阶名字 */}
          <div className="wz-foot">
            <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
              第{' '}
              <b style={{ fontFamily: 'var(--f-pix)', color: 'var(--cyan)' }}>{step + 1}</b> / {STEPS.length} 步 ·{' '}
              {STEPS[step].t}
            </span>
            <div className="ml-auto" />
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
                className="btn btn-start"
                disabled={!canNext}
                onClick={() => setStep((s) => s + 1)}
              >
                下一步 · {STEPS[step + 1].t}
              </button>
            ) : (
              <button
                type="button"
                className="btn btn-start"
                disabled={busy || !canNext || phase !== 'idle'}
                onClick={submit}
              >
                <Icon name="plus" size={13} />
                {busy ? '创建中…' : phase === 'downloading' ? '准备中…' : '创建这个世界'}
              </button>
            )}
          </div>
        </div>

        <p className="hintbox reveal" style={{ marginTop: 14 }}>
          <Icon name="info" style={{ color: 'var(--cyan)' }} />
          「下一步」灰着 = 这一步还缺东西：按提示补齐就会亮，不弹错误、不会跳步。
        </p>
      </div>
    </div>
  )
}
