import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, type PropertyRow } from '../api'
import { IconSearch, IconWarn } from './Icons'
import { NotReady, Toggle } from './ui'
import { StepInput } from './StepInput'
import { ServerPicker, type Picked } from './ServerPicker'
import { DownloadBar } from './DownloadBar'
import type { Instance } from '../types'

type Props = {
  instance: Instance
  onToast: (msg: string) => void
  onSaved: () => void
}

const INST_KEY = '__instance__'
const SRV_KEY = '__server__'

// 可寻址状态：?q=玩法名 & ?g=分组 —— 刷新不丢当前位置，也让「搜到某一页」能直接分享/截图
const PARAMS = new URLSearchParams(location.search)

type Group = { k: string; title: string; desc: string }

/** 一行：标签固定宽 + 控件紧随。不用「控件右对齐」——宽屏下中间会空出好几百像素。 */
function Crow({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="crow">
      <div className="crow-lab">
        <span className="crow-fl">{label}</span>
        {hint && <span className="crow-fh">{hint}</span>}
      </div>
      <div className="crow-ctl">{children}</div>
    </div>
  )
}

/** 开关行：控件右边跟一句状态文字，光看滑块位置在深色底上不够确定 */
function BoolCtl({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <span className="flex items-center gap-2.5">
      <Toggle on={on} onChange={onChange} label={label} />
      <span className="text-[12.5px] text-ink-2">{on ? '开启' : '关闭'}</span>
    </span>
  )
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
  const [gkey, setGkey] = useState(() => PARAMS.get('g') ?? INST_KEY)
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
      { k: SRV_KEY, title: '服务端文件', desc: '换服务端，或者凭校验重下一份' },
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
  const instFields: { k: string; label: string; hint?: string; kw: string; ctl: ReactNode }[] = [
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
      ctl: (
        <input
          className="px-input px-input-mono"
          value={javaPath}
          placeholder="留空 = 自动挑"
          onChange={(e) => setJavaPath(e.target.value)}
        />
      ),
    },
  ]
  const instRows = (list: typeof instFields) => (
    <>
      {list.map((f) => (
        <Crow key={f.k} label={f.label} hint={f.hint}>
          {f.ctl}
        </Crow>
      ))}
      <div className="flex flex-wrap items-center gap-x-3.5 gap-y-2 pt-4">
        <button type="button" className="btn btn-grass" disabled={savingInst} onClick={saveInstance}>
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
    if (g.k === SRV_KEY) return !q // 搜索时不列它，它是动作块不是字段
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
      : cur === SRV_KEY
        ? 1
        : (rows ?? []).filter((r) => r.group === cur).length

  const jump = (delta: number) => {
    if (hitGroups.length < 2 || searching) return
    const i = (curIdx + delta + hitGroups.length) % hitGroups.length
    setGkey(hitGroups[i].k)
  }

  // 滚到边界再继续滚就切组。
  // ⚠ 两个防误触：
  //   1) 面板本身滚不动（内容一屏放得下）时**完全不劫持滚轮** —— 否则短内容的面板会把
  //      滚轮全吃掉，页面根本滚不下去，用户会觉得「卡住了」。
  //   2) 到边界后要再滚过 ~100px 才切，不是一碰就切 —— 触控板惯性很容易多切一格。
  const accRef = useRef(0)
  const lastRef = useRef(0)
  useEffect(() => {
    const pane = paneRef.current
    if (!pane) return
    const onWheel = (e: WheelEvent) => {
      if (searching) return
      if (pane.scrollHeight <= pane.clientHeight + 2) return // 见上：不滚就不劫持
      const now = Date.now()
      if (now - lastRef.current > 500) accRef.current = 0
      lastRef.current = now

      const atBottom = pane.scrollTop + pane.clientHeight >= pane.scrollHeight - 2
      const atTop = pane.scrollTop <= 1
      if (e.deltaY > 0 && atBottom) {
        accRef.current += e.deltaY
        e.preventDefault()
        if (accRef.current > 100) {
          accRef.current = 0
          jump(1)
        }
      } else if (e.deltaY < 0 && atTop) {
        accRef.current -= e.deltaY
        e.preventDefault()
        if (accRef.current > 100) {
          accRef.current = 0
          jump(-1)
        }
      } else {
        accRef.current = 0
      }
    }
    pane.addEventListener('wheel', onWheel, { passive: false })
    return () => pane.removeEventListener('wheel', onWheel)
  })

  useEffect(() => {
    if (paneRef.current) paneRef.current.scrollTop = 0
  }, [cur, searching])

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
  const dirtyCount = propDirty.length + (instDirty ? 1 : 0)

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
    if (!r.known) return <span className="mono-value break-all">{r.value}</span>
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
      return <StepInput label={r.label} value={val(r)} min={0} onChange={(v) => setVal(r, v)} />
    }
    if (r.kind === 'enum') {
      return (
        <select className="px-input" value={val(r)} onChange={(e) => setVal(r, e.target.value)}>
          {(r.options ?? []).map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      )
    }
    return (
      <input
        className="px-input px-input-mono"
        value={val(r)}
        onChange={(e) => setVal(r, e.target.value)}
      />
    )
  }

  const running = instance.status !== 'stopped'
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
        <p className="mt-3 border-l-[3px] border-warn bg-[#1a1812] px-4 py-2.5 text-[12.5px] leading-relaxed text-[#e7dcb2]">
          实例正在运行。Windows 上运行中的 jar 是锁着的，覆盖不了 —— 先停服再换。
        </p>
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
          <DownloadBar taskId={taskId} />
        </div>
      )}

      {dlErr && (
        <p className="mt-3 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] leading-relaxed text-[#ffc2c2]">
          {dlErr}
        </p>
      )}

      <div className="mt-5 flex flex-wrap items-center gap-x-3.5 gap-y-2">
        <button
          type="button"
          className="btn btn-grass"
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
        下载带 SHA 校验和断点续传，中断了再点一次会接着下。
        换完服务端记得看一眼「实例设置」里的端口和内存还合不合适。
      </p>
    </>
  )

  const propRowsOf = (gk: string) =>
    visibleRows.filter((r) => r.group === gk).map((r) => (
      <Crow key={r.key} label={r.known ? r.label : r.key} hint={r.hint}>
        {rowControl(r)}
      </Crow>
    ))

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
  ) : (
    <div key={cur} className="anim-in-x stagger">
      {propRowsOf(cur)}
    </div>
  )

  return (
    <div className="min-w-0">
      <h1 className="text-[22px] leading-tight font-bold text-ink-1">配置</h1>
      <p className="mt-2 max-w-[68ch] text-[13.5px] leading-relaxed text-ink-2">
        在调 <b className="text-ink-1">{instance.name}</b>。
        「实例设置」是我们自己的配置，改完立刻生效；其余分组是服务端自己的 server.properties，多数要重启才读到。
      </p>

      {/* 搜索：命中字段名、键名、说明、分组名 */}
      <div className="search mt-5 mb-4 flex max-w-[400px] items-center gap-2.5 bg-[#0c0e0c] px-3 py-2">
        <span className="shrink-0 text-ink-3">
          <IconSearch size={13} />
        </span>
        <input
          type="search"
          className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink-1 outline-0 placeholder:text-ink-3"
          placeholder="搜字段名 / 键名，例如「端口」或 port"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <span className="mono-meta shrink-0">
          {searching ? `${matchedFields} / ${totalFields}` : `${totalFields} 个字段`}
        </span>
      </div>

      {err && (
        <p className="mb-4 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
          {err}
        </p>
      )}

      {portMismatch && (
        <div className="mb-4 flex flex-wrap items-center gap-x-3.5 gap-y-2 border-l-[3px] border-warn bg-[#1a1812] px-4 py-3">
          <span className="text-warn">
            <IconWarn size={13} />
          </span>
          <p className="min-w-0 flex-1 text-[12.5px] leading-relaxed text-[#e7dcb2]">
            server.properties 里的端口是 <b className="mono-value text-[#f7efd2]">{filePort}</b>，实例配置是{' '}
            <b className="mono-value text-[#f7efd2]">{instance.port}</b>。服务端真正监听的是文件里那个值。
          </p>
          <button type="button" className="btn btn-sm shrink-0" onClick={syncPort}>
            按实例配置覆盖
          </button>
        </div>
      )}

      {rows === null ? (
        <p className="text-[13px] text-ink-3">读取中…</p>
      ) : hitGroups.length === 0 ? (
        <p className="py-5 text-[13px] text-ink-2">没有匹配「{query.trim()}」的字段。</p>
      ) : (
        <div className="grid grid-cols-1 items-start gap-6 lg:grid-cols-[200px_1fr]">
          {/* 分组导航。窄屏收成顶部横向可滚的条 —— 三栏在手机上撑不住。 */}
          <nav
            className="fnav -mx-1 flex overflow-x-auto px-1 lg:sticky lg:top-0 lg:mx-0 lg:block lg:overflow-visible lg:px-0"
            aria-label="配置分组"
          >
            {hitGroups.map((g) => {
              const on = !searching && g.k === cur
              const n =
                g.k === INST_KEY
                  ? instVisible.length
                  : g.k === SRV_KEY
                    ? 1
                    : visibleRows.filter((r) => r.group === g.k).length
              return (
                <button
                  key={g.k}
                  type="button"
                  aria-current={on}
                  onClick={() => setGkey(g.k)}
                  className={`fitem${on ? ' on' : ''}`}
                >
                  <i className="bar-l" />
                  <span className="min-w-0 truncate">{g.title}</span>
                  <span className="cnt">{n}</span>
                </button>
              )
            })}
          </nav>

          <div className="min-w-0">
            <div className="fhead">
              <h3>{searching ? '搜索结果' : (curGroup?.title ?? '')}</h3>
              <span className="mono-meta">
                {searching
                  ? `${matchedFields} 个字段`
                  : cur === SRV_KEY
                    ? picked
                      ? `已选 ${picked.name}`
                      : '未选择'
                    : `${curRows} 项`}
              </span>
              <span className="text-[12.5px] text-ink-3">
                {searching
                  ? `命中「${query.trim()}」的字段，按分组列在下面`
                  : (curGroup?.desc ?? '')}
              </span>
              {!searching && hitGroups.length > 1 && (
                <span className="mono-meta ml-auto">
                  第 {curIdx + 1} / {hitGroups.length} 组
                </span>
              )}
            </div>
            <div ref={paneRef} className="fpane">
              {paneBody}
            </div>
            <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2">
              <span className="text-[12.5px] text-ink-3">
                {searching
                  ? '搜索中 —— 清空搜索框就回到一组一屏'
                  : '滚到底再往下滚 → 下一组（也可以点左边）'}
              </span>
              {!searching && hitGroups.length > 1 && (
                <span className="ml-auto flex gap-2.5">
                  <button type="button" className="btn btn-sm" onClick={() => jump(-1)}>
                    ← 上一组
                  </button>
                  <button type="button" className="btn btn-sm" onClick={() => jump(1)}>
                    下一组 →
                  </button>
                </span>
              )}
            </div>

            <div className="mt-5 flex flex-wrap items-center gap-x-3 gap-y-2">
              {propDirty.length > 0 ? (
                <>
                  <button type="button" className="btn btn-grass" disabled={saving} onClick={saveProps}>
                    {saving ? '写入中…' : `保存这 ${propDirty.length} 项`}
                  </button>
                  <button
                    type="button"
                    className="btn btn-ghost"
                    disabled={saving}
                    onClick={() => setDraft({})}
                  >
                    放弃改动
                  </button>
                  <span className="text-[12.5px] text-warn">改完要重启服务器才生效</span>
                </>
              ) : (
                <span className="mono-meta">
                  {dirtyCount > 0
                    ? '「实例设置」里有未保存的改动'
                    : cur === SRV_KEY
                      ? '这一组是文件操作，没有要保存的配置'
                      : 'server.properties 只在服务端启动时读一次，改完记得回概览页重启'}
                </span>
              )}
            </div>
          </div>
        </div>
      )}

      {!searching && cur !== INST_KEY && cur !== SRV_KEY && propDirty.length === 0 && (
        <div className="mt-6">
          <NotReady>
            改完要重启服务器才会读进去 —— 服务端只在启动时解析这个文件。端口、最大玩家数这类改动尤其容易被忘。
          </NotReady>
        </div>
      )}
    </div>
  )
}
