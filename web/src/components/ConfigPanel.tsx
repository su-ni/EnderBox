import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, type PropertyRow } from '../api'
import { Icon, Tog } from './design'
import { StepInput } from './StepInput'
import { ServerPicker, type Picked } from './ServerPicker'
import { JavaPicker } from './JavaPicker'
import { DownloadBar } from './DownloadBar'
import { ConvertPanel } from './ConvertPanel'
import { ConfigEditor } from './ConfigEditor'
import type { Instance } from '../types'

type Props = {
  instance: Instance
  onToast: (msg: string) => void
  onSaved: () => void
}

const INST_KEY = '__instance__'
const SRV_KEY = '__server__'
const CFG_KEY = '__configfiles__'

// 可寻址状态：?q=玩法名 & ?g=分组 —— 刷新不丢当前位置，也让「搜到某一页」能直接分享/截图
const PARAMS = new URLSearchParams(location.search)

type Group = { k: string; title: string; desc: string }

/** 一行：标签固定宽 + 控件紧随。不用「控件右对齐」——宽屏下中间会空出好几百像素。
 *  wide：控件要占满整行（Java 选择器这种），换到标签下方铺开。
 *  tags：跟在标签后面的小标记（「文件里没写」「可立即生效」这类），
 *        放标签行而不是控件列 —— 控件列本来就只有 300px，再塞标记会把控件挤没。 */
/** 一行字段：设计稿的 `.field`（190px 标签列 + 控件列）。控件要**撑满**控件列 ——
 *  设计稿里输入框 width:100%、stepper 通栏（630px）、开关左对齐 52px。
 *  这里是全页唯一的一处排版出口 —— 改行距/对齐只改这一个组件。
 *  ⚠ 2026-10-09：以前控件包了 `justify-content:flex-end`，输入框塌成固有宽度全挤右边、
 *  中间空好几百像素（用户：「配置页重新设计，好丑」）。撑满的 CSS 在 design-overrides
 *  的 `.field > .ctl` 一节。 */
function Crow({
  label,
  hint,
  children,
  wide,
  tags,
}: {
  label: string
  hint?: string
  children: ReactNode
  wide?: boolean
  tags?: ReactNode
}) {
  return (
    <div className="field" style={wide ? { gridTemplateColumns: '1fr', gap: 10 } : undefined}>
      <div>
        <div className="lb">
          {label}
          {tags}
        </div>
        {hint && <div className="hint">{hint}</div>}
      </div>
      <div className="ctl">{children}</div>
    </div>
  )
}

/** 开关行：用设计稿的 `.tog`（滑块位置 + 色相双通道，深色底上也看得出开关）。 */
function BoolCtl({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label: string }) {
  return <Tog on={on} onChange={onChange} ariaLabel={label} />
}

/**
 * 配置页：左侧分组导航 + 右侧只显示当前一组。
 * 组内容长了在面板内滚；滚到底再继续滚就切下一组，左侧同步高亮。
 * 搜索时切成「全部命中结果」铺开 —— 搜东西的时候不该还逼你按组翻。
 */
export function ConfigPanel({ instance, onToast, onSaved }: Props) {
  const [name, setName] = useState(instance.name)
  const [port, setPort] = useState(String(instance.port))
  const [maxMem, setMaxMem] = useState(String(instance.maxMem))
  const [javaPath, setJavaPath] = useState(instance.javaPath ?? '')
  const [autoStart, setAutoStart] = useState(instance.autoStart)
  const [autoRestart, setAutoRestart] = useState(instance.autoRestart)
  const [savingInst, setSavingInst] = useState(false)

  const [rows, setRows] = useState<PropertyRow[] | null>(null)
  const [groupDesc, setGroupDesc] = useState<Record<string, string>>({})
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [err, setErr] = useState('')

  // 换服务端：选了哪个构建 + 下载任务
  const [picked, setPicked] = useState<Picked | null>(null)
  const [taskId, setTaskId] = useState('')
  const [dlErr, setDlErr] = useState('')
  const [query, setQuery] = useState(() => PARAMS.get('q') ?? '')
  // cf= 优先于 g=：带文件路径的链接应当直接落到那个文件上。
  // （只认 g= 的话，?cf= 会被忽略 —— 编辑器压根不挂载，分享出去的链接全落到「实例设置」。）
  const [gkey, setGkey] = useState(() =>
    PARAMS.get('cf') ? CFG_KEY : (PARAMS.get('g') ?? INST_KEY),
  )
  // 新增自定义键（插件要用的那种）
  const [newKey, setNewKey] = useState('')
  const [newValue, setNewValue] = useState('')
  // 提到组件顶部：下面好几处都要用，留在原处会让先用到它的代码踩进 TDZ
  const running = instance.status !== 'stopped'
  const paneRef = useRef<HTMLDivElement>(null)

  // 切换实例时把表单重置，否则会带着上一个实例的值去保存 —— 最容易出事故的一种 bug
  useEffect(() => {
    setName(instance.name)
    setPort(String(instance.port))
    setMaxMem(String(instance.maxMem))
    setJavaPath(instance.javaPath ?? '')
    setAutoStart(instance.autoStart)
    setAutoRestart(instance.autoRestart)
  }, [
    instance.id,
    instance.name,
    instance.port,
    instance.maxMem,
    instance.javaPath,
    instance.autoStart,
    instance.autoRestart,
  ])

  const loadProps = useCallback(async () => {
    try {
      const d = await api.properties(instance.id)
      setRows(d.items ?? [])
      setGroupDesc(d.groupDesc ?? {})
      setDraft({})
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
      setRows([])
    }
  }, [instance.id])

  useEffect(() => {
    setRows(null)
    loadProps()
  }, [loadProps])

  // ── 分组：实例设置排第一，其余按后端 spec 的顺序 ──
  const groups = useMemo<Group[]>(() => {
    const out: Group[] = [
      { k: INST_KEY, title: '实例设置', desc: '我们自己的配置，改完立刻生效' },
      { k: SRV_KEY, title: '服务端文件', desc: '换服务端、凭校验重下一份，或转成混合端（模组 + 插件）' },
      { k: CFG_KEY, title: '其它配置文件', desc: 'bukkit.yml / paper-global.yml / 插件与模组的配置' },
    ]
    for (const r of rows ?? []) {
      if (out.some((g) => g.k === r.group)) continue
      if (r.group === '其它（不认得）') {
        out.push({ k: r.group, title: '其它', desc: 'server.properties 里我们不认得的键，只读' })
        continue
      }
      out.push({ k: r.group, title: r.group, desc: groupDesc[r.group] ?? '' })
    }
    return out
  }, [rows, groupDesc])

  const q = query.trim().toLowerCase()
  const matchRow = (r: PropertyRow) =>
    !q || (r.label + r.key + (r.hint ?? '') + r.group).toLowerCase().includes(q)

  // 实例设置也做成字段列表，而不是写死一段 JSX ——
  // 写死的话搜索没法过滤它，会出现「搜端口却把 6 项全列出来」这种自相矛盾的结果。
  // kw 是给搜索用的关键词，把中文名、英文名、常见叫法都塞进去。
  const instFields: { k: string; label: string; hint?: string; kw: string; wide?: boolean; ctl: ReactNode }[] = [
    {
      k: 'name',
      label: '名称',
      hint: '显示在列表和日志里的名字',
      kw: '名称 name 世界名',
      ctl: <input className="px-input" value={name} onChange={(e) => setName(e.target.value)} />,
    },
    {
      k: 'port',
      label: '端口',
      hint: '朋友连进来的地址就是 你的IP:端口',
      kw: '端口 port',
      ctl: <StepInput label="端口" value={port} min={1} max={65535} onChange={setPort} />,
    },
    {
      k: 'maxMem',
      label: '内存上限 (MB)',
      hint: '原版 2 GB 够 5 个人玩',
      kw: '内存 memory maxmem mb 分配',
      ctl: <StepInput label="内存上限" value={maxMem} step={512} min={512} onChange={setMaxMem} />,
    },
    {
      k: 'autoStart',
      label: '开机自动启动',
      hint: '守护进程起来时自动把它拉起来',
      kw: '开机 自启 自动启动 autostart',
      ctl: <BoolCtl on={autoStart} onChange={setAutoStart} label="开机自动启动" />,
    },
    {
      k: 'autoRestart',
      label: '崩溃自动重启',
      hint: '5 秒 / 20 秒 / 60 秒三次退避，连着 3 次起不来就停下',
      kw: '崩溃 重启 restart autorestart',
      ctl: <BoolCtl on={autoRestart} onChange={setAutoRestart} label="崩溃自动重启" />,
    },
    {
      k: 'javaPath',
      label: 'Java 路径',
      hint: '留空就按 MC 版本自动挑一个够用的',
      kw: 'java 路径 path jdk 运行时',
      wide: true,
      ctl: (
        <div className="w-full min-w-0">
          {/* 选择器要占整行，挤在 300px 的控件列里会没法看 */}
          <JavaPicker value={javaPath} onChange={setJavaPath} />
          <label className="mt-3 block">
            <span className="field-label">或手动填一个路径（兜底）</span>
            <input
              className="px-input px-input-mono"
              value={javaPath}
              placeholder="留空 = 自动挑"
              onChange={(e) => setJavaPath(e.target.value)}
            />
          </label>
        </div>
      ),
    },
  ]
  const instRows = (list: typeof instFields) => (
    <>
      {list.map((f) => (
        <Crow key={f.k} label={f.label} hint={f.hint} wide={f.wide}>
          {f.ctl}
        </Crow>
      ))}
      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2 pt-4">
        <button type="button" className="btn btn-primary" disabled={savingInst} onClick={saveInstance}>
          {savingInst ? '保存中…' : '保存实例设置'}
        </button>
        {instance.status !== 'stopped' && (
          <span className="text-[12.5px] text-warn">实例正在运行，端口和内存要重启才生效</span>
        )}
      </div>
    </>
  )

  const visibleRows = (rows ?? []).filter(matchRow)
  // 实例设置那几项也要能被搜到，否则搜「端口」会漏掉实例端口
  const instVisible = instFields.filter((f) => !q || (f.label + ' ' + f.kw).toLowerCase().includes(q))
  const instHit = instVisible.length > 0
  const totalFields = instFields.length + (rows ?? []).length
  const matchedFields = instVisible.length + visibleRows.length

  const hitGroups = groups.filter((g) => {
    // 搜索时不列动作块（换服务端、改其它配置文件）—— 它们不是可搜索的字段，
    // 混在搜索结果里只会让人以为「搜出来的这组没结果」
    if (g.k === SRV_KEY || g.k === CFG_KEY) return !q
    if (g.k === INST_KEY) return instHit
    return visibleRows.some((r) => r.group === g.k)
  })
  const searching = Boolean(q)
  const cur = hitGroups.some((g) => g.k === gkey) ? gkey : (hitGroups[0]?.k ?? INST_KEY)
  const curIdx = hitGroups.findIndex((g) => g.k === cur)
  const curGroup = hitGroups.find((g) => g.k === cur)
  const curRows =
    cur === INST_KEY
      ? instVisible.length
      : cur === SRV_KEY || cur === CFG_KEY
        ? 1
        : (rows ?? []).filter((r) => r.group === cur).length

  const jump = (delta: number) => {
    if (hitGroups.length < 2 || searching) return
    const i = (curIdx + delta + hitGroups.length) % hitGroups.length
    setGkey(hitGroups[i].k)
  }

  useEffect(() => {
    if (paneRef.current) paneRef.current.scrollTop = 0
  }, [cur, searching])

  // 左栏选中态写回 URL：刷新不丢当前位置，也让「切到某一组」可分享、可截图。
  // 只动 g，其余参数（cf= 等）原样保留 —— 否则会和配置编辑器写的 cf= 互相覆盖。
  useEffect(() => {
    const u = new URL(location.href)
    if (u.searchParams.get('g') === gkey) return
    u.searchParams.set('g', gkey)
    history.replaceState(null, '', u)
  }, [gkey])

  const saveInstance = async () => {
    const p = Number(port)
    const m = Number(maxMem)
    if (!Number.isFinite(p) || p < 1 || p > 65535) {
      onToast('端口要填 1-65535 之间的数字')
      return
    }
    if (!Number.isFinite(m) || m < 512) {
      onToast('内存至少给 512 MB')
      return
    }
    setSavingInst(true)
    try {
      await api.updateInstance(instance.id, {
        ...instance,
        name: name.trim() || instance.name,
        port: p,
        maxMem: m,
        minMem: Math.min(instance.minMem || 1024, m),
        javaPath: javaPath.trim(),
        autoStart,
        autoRestart,
      })
      onToast('实例配置已保存')
      onSaved()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setSavingInst(false)
    }
  }

  const propDirty = Object.keys(draft).filter((k) => {
    const row = rows?.find((r) => r.key === k)
    return row && row.value !== draft[k]
  })
  const instDirty =
    name !== instance.name ||
    port !== String(instance.port) ||
    maxMem !== String(instance.maxMem) ||
    javaPath !== (instance.javaPath ?? '') ||
    autoStart !== instance.autoStart ||
    autoRestart !== instance.autoRestart

  const saveProps = async () => {
    if (propDirty.length === 0) return
    setSaving(true)
    try {
      const updates: Record<string, string> = {}
      for (const k of propDirty) updates[k] = draft[k]
      await api.saveProperties(instance.id, updates)
      onToast(`已写入 ${propDirty.length} 项。多数配置要重启服务器才生效。`)
      await loadProps()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  /**
   * 「立即生效」只在**这次改的每一项**都有对应指令时才出现。
   *
   * 为什么是一项都不许漏：允许「一半生效、一半没生效」是最坑的结果 ——
   * 用户点完看到成功提示，以为全好了，重启之后一半改动打回原形，而且想不出为什么。
   * 所以后端那边也是「有一个不支持就整体拒绝」。
   */
  const liveReady = running && propDirty.length > 0 && propDirty.some((k) => rows?.find((r) => r.key === k)?.live) && propDirty.every((k) => rows?.find((r) => r.key === k)?.live)

  const applyLive = async () => {
    if (propDirty.length === 0) return
    setSaving(true)
    try {
      const updates: Record<string, string> = {}
      for (const k of propDirty) updates[k] = draft[k]
      const res = await api.applyProperties(instance.id, updates)
      onToast(`已立即生效：${(res.commands ?? []).join('   /   ')}`)
      await loadProps()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  // 新增一个我们和文件里都没有的键（插件要用的那种）。加完就是个普通草稿，
  // 走同一条保存路径 —— 后端允许写未知键，只挡 = : 换行和空白这些会写坏文件名的字符。
  const addCustomKey = () => {
    const k = newKey.trim()
    if (!k) {
      onToast('先填一个键名')
      return
    }
    if (/[=:\s]/.test(k)) {
      onToast('键名里不能有空格、= 或 : —— 服务端会把它读成另一个键')
      return
    }
    if (rows?.some((r) => r.key === k)) {
      onToast(`「${k}」已经在列表里了`)
      return
    }
    setRows((prev) => [
      ...(prev ?? []),
      {
        key: k,
        value: newValue,
        kind: 'string',
        label: k,
        group: '其它（不认得）',
        inFile: false,
        known: false,
      },
    ])
    setDraft((d) => ({ ...d, [k]: newValue }))
    setNewKey('')
    setNewValue('')
    setGkey('其它（不认得）')
    onToast(`已加入草稿：${k}（保存后才写进文件）`)
  }

  const syncPort = async () => {
    try {
      await api.applyFix(instance.id, 'sync_port')
      onToast('已把实例端口写进 server.properties')
      await loadProps()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const val = (r: PropertyRow) => draft[r.key] ?? r.value
  const setVal = (r: PropertyRow, v: string) => setDraft((d) => ({ ...d, [r.key]: v }))

  const filePort = rows?.find((r) => r.key === 'server-port')?.value
  const portMismatch = filePort !== undefined && filePort !== String(instance.port)

  const rowControl = (r: PropertyRow) => {
    if (r.kind === 'bool') {
      return (
        <BoolCtl
          on={val(r) === 'true'}
          onChange={(v) => setVal(r, v ? 'true' : 'false')}
          label={r.label}
        />
      )
    }
    if (r.kind === 'int') {
      // 范围由后端下发，别在这里写 min={0} ——
      // 所有 int 共用一个下限的时候，监听端口能被一步步减到 0，
      // 而服务端读不懂这个值会把**整份** server.properties 静默回退成默认值。
      return (
        <StepInput
          label={r.label}
          value={val(r)}
          min={r.min}
          max={r.max}
          onChange={(v) => setVal(r, v)}
          right={
            r.min !== undefined || r.max !== undefined ? (
              <span className="mono-meta shrink-0">
                {r.min !== undefined && r.max !== undefined
                  ? `${r.min}–${r.max}`
                  : r.min !== undefined
                    ? `≥${r.min}`
                    : `≤${r.max}`}
              </span>
            ) : null
          }
        />
      )
    }
    if (r.kind === 'enum') {
      return (
        <select className="px-input" value={val(r)} onChange={(e) => setVal(r, e.target.value)}>
          {(r.options ?? []).map((o) => (
            <option key={o} value={o}>
              {r.labels?.[o] ?? o}
            </option>
          ))}
        </select>
      )
    }
    // 不认得的键也给输入框 —— 插件往这个文件里加的键，用户在这页改不了
    // 就只能去文件管理里翻，而那正是「可见但不可点」。
    return (
      <input
        className="px-input px-input-mono"
        type={r.secret ? 'password' : 'text'}
        autoComplete={r.secret ? 'new-password' : undefined}
        value={val(r)}
        onChange={(e) => setVal(r, e.target.value)}
      />
    )
  }

  const serverGroup = (
    <>
      {/* 这一行必须是「实例里真正在用的那个文件」，不能显示成下面选中的那个 ——
          两行都写文件名，用户会以为已经换好了 */}
      <Crow label="当前在用" hint={`实例配置里写的文件名，服务端启动时找的就是它`}>
        <span className="mono-value break-all">{instance.jarPath}</span>
      </Crow>
      {picked && (
        <Crow label="已选中" hint="点下面的按钮会下载它并覆盖上面那个文件">
          <span className="mono-value break-all">{picked.name}</span>
        </Crow>
      )}

      {running && (
        <div className="nbox w" style={{ marginTop: 12 }}>
          实例正在运行。Windows 上运行中的 jar 是锁着的，覆盖不了 —— 先停服再换。
        </div>
      )}

      <div className="mt-5">
        <ServerPicker
          type={instance.type}
          value={picked}
          onPick={setPicked}
          compact
          preferVersion={instance.mcVersion}
        />
      </div>

      {taskId && (
        <div className="mt-4">
          <DownloadBar taskId={taskId} onToast={onToast} />
        </div>
      )}

      {dlErr && (
        <div className="nbox d" style={{ marginTop: 12 }}>
          {dlErr}
        </div>
      )}

      <div className="mt-5 flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <button
          type="button"
          className="btn btn-primary"
          disabled={!picked || running}
          onClick={async () => {
            if (!picked) return
            setDlErr('')
            setTaskId('')
            try {
              const t = await api.downloadServerJar(instance.id, {
                type: instance.type,
                version: picked.version,
                build: picked.build,
                mirror: picked.mirror,
              })
              setTaskId(t.id)
            } catch (e) {
              setDlErr((e as Error).message)
            }
          }}
        >
          下载并替换
        </button>
        {!picked && <span className="text-[12.5px] text-ink-3">先在上面选一个版本和构建</span>}
        {picked && (
          <span className="mono-meta">
            下完会覆盖 {instance.jarPath}，旧的会被删掉
          </span>
        )}
      </div>
      <p className="mt-3 text-[12px] leading-relaxed text-ink-3">
        下载走多线程，连接被掐断会自动重试、接着已有进度下；源站给了校验和的会校验一遍。
        换完服务端记得看一眼「实例设置」里的端口和内存还合不合适。
      </p>

      {/* 模组服想装插件、或者插件的服想加模组，就得换成混合端。
          这件事用户自己做容易做错（下哪个 jar、启动文件指谁、类型在哪改、顺序还不能反），
          所以做成一个动作，由后端保证「先下好文件、校验过、再改实例」。 */}
      <ConvertPanel instance={instance} onToast={onToast} onDone={onSaved} />
    </>
  )

  const rowTags = (r: PropertyRow) => (
    <>
      {!r.known && (
        <span className="chip s ml-2 align-middle" title="这个键我们不认识，按普通字符串原样写回">
          插件加的键
        </span>
      )}
      {r.known && !r.inFile && (
        <span className="chip s ml-2 align-middle" title="文件里没有这一项，现在显示的是官方默认值">
          文件里没写
        </span>
      )}
      {r.live && running && (
        <span className="chip s g ml-2 align-middle" title="改完可以不重启，直接发到服务器控制台">
          可立即生效
        </span>
      )}
    </>
  )

  const propRowsOf = (gk: string) =>
    visibleRows
      .filter((r) => r.group === gk)
      .map((r) => (
        <Crow
          key={r.key}
          label={r.known ? r.label : r.key}
          hint={
            r.known
              ? r.hint
              : '这个键在文件里，但我们不认识它 —— 按普通字符串处理，值原样写回'
          }
          tags={rowTags(r)}
        >
          {rowControl(r)}
        </Crow>
      ))

  const UNKNOWN_GROUP = '其它（不认得）'

/**
 * 分组导航的图标。分组名来自后端（server.properties 的语义分组），所以这里按名字查表 ——
 * 查不到就用一个中性的方块图标，不硬猜。设计稿那六个分组的图标就是下面这几个。
 */
const NAV_ICON: Record<string, string> = {
  实例设置: 'book',
  服务端文件: 'cube',
  其它配置文件: 'file',
  网络与身份: 'compass',
  玩法: 'table',
  世界生成: 'grass',
  玩家管理: 'users',
  性能与稳定: 'cpu',
  可见性与查询: 'search',
  资源包: 'chest',
  远程控制: 'lever',
}/* 其余分组（含「其它」）落到默认图标 */
const navIcon = (title: string) => NAV_ICON[title] ?? 'file'

  // 「其它」组末尾的「加一个键」。没有这个入口的话，一个文件里还不存在的键
  // 就永远加不进来 —— 插件文档让你往 server.properties 里加一行，你在这页做不到。
  const addKeyRow = (
    <div className="mt-5 border-t border-line pt-4">
      <h4 className="text-[13px] font-bold text-ink-1">加一个键</h4>
      <p className="mt-1 text-[12px] leading-relaxed text-ink-3">
        插件文档让你往 server.properties 里补一行的时候用这里。键名不能带空格、
        <span className="mono-value">=</span> 或 <span className="mono-value">:</span>
        —— 服务端会把它们当分隔符，读出来是另一个键。加进来先算草稿，保存之后才写进文件。
      </p>
      <div className="mt-3 flex flex-wrap items-end gap-2.5">
        <label className="min-w-[180px] flex-1">
          <span className="field-label">键名</span>
          <input
            className="px-input px-input-mono"
            value={newKey}
            placeholder="例如 zzz-plugin-toggle"
            onChange={(e) => setNewKey(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && addCustomKey()}
          />
        </label>
        <label className="min-w-[160px] flex-1">
          <span className="field-label">值</span>
          <input
            className="px-input px-input-mono"
            value={newValue}
            placeholder="例如 true"
            onChange={(e) => setNewValue(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && addCustomKey()}
          />
        </label>
        <button type="button" className="btn sm" onClick={addCustomKey}>
          <Icon name="plus" size={13} /> 加入草稿
        </button>
      </div>
    </div>
  )

  const paneBody = searching ? (
    // 命中多个组时全部铺开，组间用分组头分隔
    <div className="anim-in">
      {hitGroups.map((g) => (
        <section key={g.k}>
          <h3 className="mt-4 border-b border-line pb-2 text-[14px] font-bold text-ink-1">
            {g.title}
            <span className="mono-meta ml-3 font-normal">
              {g.k === INST_KEY
                ? instVisible.length
                : visibleRows.filter((r) => r.group === g.k).length}{' '}
              项
            </span>
          </h3>
          {g.k === INST_KEY ? (
            <div className="stagger">{instRows(instVisible)}</div>
          ) : (
            <div className="stagger">{propRowsOf(g.k)}</div>
          )}
        </section>
      ))}
    </div>
  ) : cur === INST_KEY ? (
    <div key={INST_KEY} className="anim-in-x stagger">
      {instRows(instFields)}
    </div>
  ) : cur === SRV_KEY ? (
    <div key={SRV_KEY} className="anim-in-x">
      {serverGroup}
    </div>
  ) : cur === CFG_KEY ? (
    <div key={CFG_KEY} className="anim-in-x">
      <ConfigEditor instance={instance} onToast={onToast} />
    </div>
  ) : (
    <div key={cur} className="anim-in-x stagger">
      {propRowsOf(cur)}
      {cur === UNKNOWN_GROUP && addKeyRow}
    </div>
  )

  return (
    <>
      <div className="sec-head reveal">
        <span className="idx">03</span>
        <h2>配置</h2>
        <span className="sub">
          在调 <b style={{ color: 'var(--ink-1)' }}>{instance.name}</b>。改完立即生效的分组在前，
          其余是服务端自己的 server.properties，改完要重启才读到
        </span>
      </div>

      {err && (
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" size={18} style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>配置读不出来</h3>
            <p>{err}</p>
          </div>
        </div>
      )}

      {portMismatch && (
        <div className="banner-danger" style={{ marginBottom: 12 }} data-tone="warn">
          <Icon name="info" size={18} style={{ color: 'var(--warn)' }} />
          <div className="t">
            <h3>端口对不上</h3>
            <p>
              server.properties 里的端口是{' '}
              <b className="mono" style={{ color: 'var(--ink-1)' }}>{filePort}</b>，实例配置是{' '}
              <b className="mono" style={{ color: 'var(--ink-1)' }}>{instance.port}</b>。
              服务端真正监听的是文件里那个值。
            </p>
          </div>
          <button type="button" className="btn sm" onClick={syncPort}>
            按实例配置覆盖
          </button>
        </div>
      )}

      {rows === null ? (
        <div className="panel reveal">
          <i className="corner" />
          <div className="statecard">
            <div className="sk" style={{ width: '52%' }} />
            <div className="sk" style={{ width: '78%', marginTop: 12 }} />
            <div className="sk" style={{ width: '66%', marginTop: 12 }} />
          </div>
        </div>
      ) : hitGroups.length === 0 ? (
        <div className="panel reveal">
          <i className="corner" />
          <div className="statecard">
            <h3>没有匹配的字段</h3>
            <p>
              没有匹配「<span className="mono">{query.trim()}</span>」的字段。换个词试试，或者清空搜索框回到一组一屏。
            </p>
          </div>
        </div>
      ) : (
        <div className="cfg-wrap">
          {/* 分组导航：设计稿的 .cfg-nav（MC 物品槽那套） */}
          <div className="panel reveal">
            <i className="corner"></i>
            <div className="cfg-nav" id="cfgNav">
              {hitGroups.map((g) => {
                const on = !searching && g.k === cur
                const n =
                  g.k === INST_KEY
                    ? instVisible.length
                    : g.k === SRV_KEY || g.k === CFG_KEY
                      ? 0
                      : visibleRows.filter((r) => r.group === g.k).length
                return (
                  <button
                    key={g.k}
                    type="button"
                    data-pane={g.k}
                    aria-current={on}
                    onClick={() => setGkey(g.k)}
                    className={on ? 'on' : ''}
                  >
                    <Icon name={navIcon(g.title)} />
                    <span style={{ minWidth: 0 }}>{g.title}</span>
                    {n > 0 && <span className="n">{n}</span>}
                  </button>
                )
              })}
            </div>
          </div>

          <div className="panel reveal">
            <i className="corner"></i>
            <div className="con-head tex-stone">
              <Icon name="book" style={{ color: 'var(--cyan)' }} />
              <h2>{searching ? '搜索结果' : (curGroup?.title ?? '')}</h2>
              <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
                {searching
                  ? `${matchedFields} 个字段`
                  : cur === SRV_KEY || cur === CFG_KEY
                    ? (curGroup?.desc ?? '')
                    : `${curRows} 项 · ${curGroup?.desc ?? ''}`}
              </span>
              <div className="spacer"></div>
              <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
                {searching ? (
                  <>{matchedFields} / {totalFields} 个字段</>
                ) : cur === SRV_KEY ? (
                  picked ? `已选 ${picked.name}` : '未选择'
                ) : cur === CFG_KEY ? (
                  '可编辑的配置文件'
                ) : (
                  <>
                    第 <b>{curIdx + 1}</b> / {hitGroups.length} 组
                  </>
                )}
              </span>
            </div>

            {/* 搜索框：设计稿的 .inp */}
            <div className="toolbar">
              <Icon name="search" style={{ color: 'var(--ink-3)' }} />
              <input
                className="inp"
                style={{ flex: 1, maxWidth: 380 }}
                placeholder="搜字段名 / 键名，例如「端口」或 port"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
              <div className="spacer" style={{ marginLeft: 'auto' }}></div>
              <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
                {searching ? `${matchedFields} / ${totalFields}` : `${totalFields} 个字段`}
              </span>
            </div>

            <div ref={paneRef} className="cfg-pane on">
              {/* 设计稿每个 cfg-pane 里都垫了一层内衬（index.html:863 `padding:6px 16px 16px`）——
                  少了它，字段行会直接贴到面板边框上（用户 10-09：「有些超出边距」）。
                  设置页的同类内衬在 SettingsPanel / AccessPanel 里（14px 16px 18px）。 */}
              <div style={{ padding: '6px 16px 16px' }}>{paneBody}</div>
            </div>

            {/* 未保存条 / 组间跳转：设计稿的 .unsaved */}
            <div className="unsaved">
              <span className="dotw"></span>
              {cur === CFG_KEY ? (
                <span style={{ fontSize: 12.5, color: 'var(--ink-1)' }}>
                  这一组是文件编辑器，保存按钮在编辑器右下角 —— 两个保存键放一起会让人点错。
                </span>
              ) : searching ? (
                <span style={{ fontSize: 12.5, color: 'var(--ink-1)' }}>
                  搜索中 —— 清空搜索框就回到一组一屏
                </span>
              ) : propDirty.length + (instDirty ? 1 : 0) > 0 ? (
                <span style={{ fontSize: 12.5, color: 'var(--ink-1)' }}>
                  有{' '}
                  <b style={{ fontFamily: 'var(--f-pix)', color: 'var(--copper)' }}>
                    {propDirty.length + (instDirty ? 1 : 0)}
                  </b>{' '}
                  处改动尚未保存
                </span>
              ) : (
                <span style={{ fontSize: 12.5, color: 'var(--ink-1)' }}>当前这一组没有未保存的改动</span>
              )}

              {/* 有未保存改动时，保存键就长在这条里 —— 跟设计稿的 .unsaved 一致 */}
              {!searching && cur !== CFG_KEY && propDirty.length > 0 && (
                <>
                  {liveReady && (
                    <button type="button" className="btn btn-start sm" disabled={saving} onClick={applyLive}>
                      <Icon name="check" />
                      {saving ? '发送中…' : '写入并立即生效'}
                    </button>
                  )}
                  <button
                    type="button"
                    className={liveReady ? 'btn sm' : 'btn btn-start sm'}
                    disabled={saving}
                    onClick={saveProps}
                  >
                    <Icon name="check" />
                    {saving ? '写入中…' : `保存这 ${propDirty.length} 项`}
                  </button>
                  <button type="button" className="btn btn-ghost sm" disabled={saving} onClick={() => setDraft({})}>
                    放弃改动
                  </button>
                  <span style={{ fontSize: 12, color: 'var(--ink-3)' }}>
                    {liveReady ? '左边那个不用重启，右边那个要重启才读到' : '改完要重启服务器才生效'}
                  </span>
                </>
              )}

              <div className="spacer" style={{ marginLeft: 'auto' }}></div>
              {!searching && hitGroups.length > 1 && (
                <>
                  <button type="button" className="btn sm" onClick={() => jump(-1)}>
                    <Icon name="chev-l" />
                    上一组
                  </button>
                  <button type="button" className="btn sm" onClick={() => jump(1)}>
                    下一组
                    <Icon name="chev-r" />
                  </button>
                </>
              )}
            </div>
          </div>
        </div>
      )}

      {/* server.properties 只在启动时读一次 —— 这条提醒不属于任何一组，放在组下面 */}
      {!searching && cur !== INST_KEY && cur !== SRV_KEY && cur !== CFG_KEY && propDirty.length === 0 && (
        <div className="nbox" style={{ marginTop: 12 }}>
          <div className="hd">
            <Icon name="info" />
            改完要重启才读得进去
          </div>
          服务端只在启动时解析 server.properties —— 端口、最大玩家数这类改动尤其容易被忘。
        </div>
      )}
    </>
  )
}
