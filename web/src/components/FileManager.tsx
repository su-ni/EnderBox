import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { api, type FileEntry, type FileListing } from '../api'
import { ConFoot, Icon, Panel, SecHead } from './design'
import { Dialog, Empty, SkeletonList, fmtSize, fmtTime, useConfirm } from './ui'
import type { Instance } from '../types'

// 文件管理：不离开面板就能看实例目录里有什么、改个配置文件、把 zip 解压开。
//
// 为什么值得单独做一页：模组 / 插件只是实例目录的一部分。改配置要进 config/，
// 装整合包要解开 zip，删掉一个捣乱的目录……以前这些都只能让用户自己去开资源管理器找目录。

/**
 * 过滤 + 固定排序。**目录恒在最前、组内按名字升序**：资源管理器就是这个规矩，
 * 而且它保证「进子目录」这个最常用的动作永远在列表最上面。
 *
 * 放到组件外当纯函数，是为了能被单独测（排序错一次，用户就会以为文件丢了）。
 */
function sortEntries(entries: FileEntry[], fq: string): FileEntry[] {
  const arr = fq ? entries.filter((e) => e.name.toLowerCase().includes(fq)) : entries.slice()
  const byName = (a: FileEntry, b: FileEntry) =>
    a.name.toLowerCase().localeCompare(b.name.toLowerCase())
  return arr.filter((e) => e.isDir).sort(byName).concat(arr.filter((e) => !e.isDir).sort(byName))
}

/** 行图标：目录 / zip / jar / 普通文件。jar 单独认出来 —— 它是这页最常被找的东西。 */
function iconOf(e: FileEntry): string {
  if (e.isDir) return 'folder'
  if (e.zip) return 'zip'
  if (e.name.toLowerCase().endsWith('.jar')) return 'jar'
  return 'file'
}

/** 目录名（实例目录的最后一段）。面包屑的第一段就用它，比写「根目录」更像回事。 */
function rootLabel(dir: string): string {
  const parts = dir.replace(/[\\/]+$/, '').split(/[\\/]/)
  return parts[parts.length - 1] || '实例目录'
}

type Props = {
  instance: Instance
  /** 当前目录（相对实例目录，空 = 根）。由 App 持有，这样「打开文件夹」能直接定位。 */
  path: string
  onPath: (p: string) => void
  onToast: (msg: string) => void
}

export function FileManager({ instance, path, onPath, onToast }: Props) {
  const [list, setList] = useState<FileListing | null>(null)
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [filter, setFilter] = useState('')
  const [edit, setEdit] = useState<FileEntry | null>(null)
  const [extract, setExtract] = useState<FileEntry | null>(null)
  const [busy, setBusy] = useState(false)

  const { dialog, confirm } = useConfirm()
  const uploadRef = useRef<HTMLInputElement>(null)
  const zipRef = useRef<HTMLInputElement>(null)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      const d = await api.files(instance.id, path)
      setList(d)
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
      setList(null)
    } finally {
      setLoading(false)
    }
  }, [instance.id, path])

  useEffect(() => {
    setFilter('')
    reload()
  }, [reload])

  // 换实例时把目录拉回根 —— 否则会带着 A 的路径去问 B，得到一个莫名其妙的「找不到」。
  // 只在「真的换了实例」时做，首次挂载不动 —— 否则 ?page=files&path=mods 这种深链
  // 会被它自己清掉，用户看到的是根目录。
  const prevInst = useRef(instance.id)
  useEffect(() => {
    if (prevInst.current !== instance.id) {
      prevInst.current = instance.id
      onPath('')
    }
  }, [instance.id, onPath])

  /** 上传进度：正在传第几个、一共几个、这一个的百分比。 */
  const [up, setUp] = useState<{ i: number; n: number; pct: number } | null>(null)

  /**
   * 一次传多个。**逐个排队传，不并发**：五个大文件并发会把带宽切碎、每个都变慢，
   * 而且中途失败时更难说清是哪个。逐个还能给出「第 3/5 个」这种能看懂的进度。
   */
  const uploadMany = async (files: File[], extractZip: boolean) => {
    const list = [...files]
    if (list.length === 0) return
    setBusy(true)
    let ok = 0
    const bad: string[] = []
    for (let i = 0; i < list.length; i++) {
      const n = i + 1
      setUp({ i: n, n: list.length, pct: 0 })
      try {
        await uploadWithProgress(
          instance.id,
          path,
          list[i],
          { extract: extractZip, into: 'folder' },
          (pct) => setUp({ i: n, n: list.length, pct }),
        )
        ok++
      } catch (e) {
        bad.push(`${list[i].name}：${(e as Error).message}`)
      }
    }
    setUp(null)
    setBusy(false)
    reload()
    onToast(
      bad.length === 0
        ? extractZip
          ? `已解压 ${ok} 个包`
          : `已上传 ${ok} 个文件`
        : `成了 ${ok} 个，${bad.length} 个没成 —— ${bad[0]}`,
    )
  }

  const doExtract = async (entry: FileEntry, into: 'folder' | 'here') => {
    setBusy(true)
    try {
      const r = await api.fileExtract(instance.id, entry.path, into)
      onToast(`已解压到 ${r.extractedTo}/`)
      setExtract(null)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const removeNow = async (entry: FileEntry) => {
    try {
      await api.fileDelete(instance.id, entry.path)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const remove = (entry: FileEntry) =>
    confirm({
      title: `删除「${entry.name}」`,
      danger: true,
      confirmLabel: entry.isDir ? '删除整个目录' : '删除文件',
      run: () => removeNow(entry),
      body: entry.isDir ? (
        <>
          这是个目录，<b className="text-ink-1">里面的东西会一起删掉</b>。
          <span className="mono-value mt-1.5 block break-all">{entry.path}/</span>
          删掉之后就找不回来了。
        </>
      ) : (
        <>
          文件会<b className="text-ink-1">从磁盘上删掉</b>。
          <span className="mono-value mt-1.5 block break-all">{entry.path}</span>
          想找回得你自己有另一份。
        </>
      ),
    })

  /** 正在问名字：新建文件夹，或给某一项改名。 */
  const [naming, setNaming] = useState<
    { mode: 'mkdir' } | { mode: 'rename'; entry: FileEntry } | null
  >(null)

  /** 下载。走后端 raw 直链 —— Cookie 会话浏览器自己带上，前端不用拼任何鉴权。 */
  const download = (entry: FileEntry) => {
    const a = document.createElement('a')
    a.href = `/api/instances/${instance.id}/file/raw?path=${encodeURIComponent(entry.path)}`
    a.download = entry.name
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  const submitName = (name: string) => {
    if (!naming) return
    const job =
      naming.mode === 'mkdir'
        ? api
            .fileMkdir(instance.id, path ? `${path}/${name}` : name)
            .then(() => `已建文件夹「${name}」`)
        : api
            .fileMove(instance.id, naming.entry.path, dirOf(naming.entry.path) + name)
            .then(() => `已改名为「${name}」`)
    setBusy(true)
    job.then(
      (msg) => {
        setBusy(false)
        setNaming(null)
        onToast(msg)
        reload()
      },
      (e: Error) => {
        setBusy(false)
        onToast(e.message) // 目标已存在时后端回 409，文案原样显示（"这里已经有同名的东西了：xxx"）
      },
    )
  }

  const fq = filter.trim().toLowerCase()
  const entries = list?.entries ?? []
  const shown = useMemo(() => sortEntries(entries, fq), [entries, fq])

  // 面包屑：每一段都能点回去
  const segs = path ? path.split('/') : []
  const crumbs: { label: string; path: string }[] = [{ label: rootLabel(instance.dir), path: '' }]
  segs.forEach((s, i) => crumbs.push({ label: s, path: segs.slice(0, i + 1).join('/') }))

  return (
    <div className="min-w-0">
      <SecHead
        idx="07"
        title="文件"
        sub={
          <>
            正在管 <b style={{ color: 'var(--ink-1)' }}>{instance.name}</b> 的整个实例目录。
            配置文件可以在这里直接改；zip 能直接解开。
          </>
        }
        style={{ '--rd': '.08s' } as React.CSSProperties}
        right={
          <button type="button" className="btn sm" onClick={reload}>
            <Icon name="refresh" />
            刷新
          </button>
        }
      />

      {err && (
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>读目录没成功</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn sm" onClick={reload}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      )}

      <Panel style={{ '--rd': '.12s' } as React.CSSProperties}>
        {/* 第一行：上一级 + 面包屑（每一段都能点回去，root 用实例目录名） */}
        <div className="pathbar">
          {path ? (
            <button
              type="button"
              className="btn btn-ghost sm"
              onClick={() => onPath(list?.parent ?? '')}
            >
              <Icon name="chev-l" />
              上一级
            </button>
          ) : null}
          <div className="crumbs">
            {crumbs.map((c, i) => (
              <Fragment key={c.path || 'root'}>
                {i > 0 && <span className="sep">/</span>}
                {i === crumbs.length - 1 ? (
                  <span className="cur">{c.label}</span>
                ) : (
                  <button type="button" onClick={() => onPath(c.path)}>
                    {c.label}
                  </button>
                )}
              </Fragment>
            ))}
          </div>
        </div>

        {/* 第二行：过滤 + 上传文件 / 上传并解压（两个独立动作，各带自己的选文件框） */}
        <div className="toolbar">
          <div
            style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1, minWidth: 180, maxWidth: 300 }}
          >
            <Icon name="search" style={{ color: 'var(--ink-3)' }} />
            <input
              className="inp"
              style={{ padding: '6px 9px' }}
              placeholder="按文件名过滤"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
          </div>
          <div className="spacer" style={{ marginLeft: 'auto' }} />
          <button
            type="button"
            className="btn btn-ghost sm"
            disabled={busy}
            onClick={() => setNaming({ mode: 'mkdir' })}
          >
            <Icon name="folder" />
            新建文件夹
          </button>
          <button type="button" className="btn sm" disabled={busy} onClick={() => uploadRef.current?.click()}>
            <Icon name="ul" />
            上传文件
          </button>
          <button type="button" className="btn sm" disabled={busy} onClick={() => zipRef.current?.click()}>
            <Icon name="chest" />
            上传并解压
          </button>
          <input
            ref={uploadRef}
            type="file"
            multiple
            className="hidden"
            onChange={(e) => {
              const fs = e.target.files
              if (fs?.length) void uploadMany([...fs], false)
              e.target.value = ''
            }}
          />
          <input
            ref={zipRef}
            type="file"
            accept=".zip"
            multiple
            className="hidden"
            onChange={(e) => {
              const fs = e.target.files
              if (fs?.length) void uploadMany([...fs], true)
              e.target.value = ''
            }}
          />
        </div>

        {/* 上传进度：一次传多个时逐个排队传，这里显示「第几个 + 这一个走了多少」 */}
        {up && (
          <div className="fupbar">
            <span className="step-line">
              正在上传 第 <b className="mono-value">{up.i}</b>/{up.n} 个
            </span>
            <div className="prog">
              <div className="fill" style={{ width: `${up.pct}%` }} />
              <div className="lb2">{up.pct}%</div>
            </div>
          </div>
        )}

        {loading ? (
          <div style={{ padding: 14 }}>
            <SkeletonList rows={5} />
          </div>
        ) : shown.length === 0 ? (
          <div style={{ padding: 14 }}>
            <Empty>
              {fq
                ? `这个目录里没有匹配「${filter.trim()}」的东西。`
                : '这个目录是空的。可以用上面的按钮传文件，或者解开一个 zip。'}
            </Empty>
          </div>
        ) : (
          <div className="rlist">
            {shown.map((e) => (
              /* ⚠ 列定义走 inline（照设计稿 files 块）：.frow 的 CSS 默认是 5 列
                 （图标/名称/大小/时间/操作），那个形态没进设计稿的实际使用 ——
                 每行都是 3 列覆盖：图标 30px / 名称+fmeta 副行 / 操作。 */
              <div key={e.path} className="frow" style={{ gridTemplateColumns: '30px 1fr auto' }}>
                <Icon name={iconOf(e)} className="pic fic" size={24} />
                <div style={{ minWidth: 0 }}>
                  {e.isDir || e.text ? (
                    <button
                      type="button"
                      className="fname"
                      onClick={() => (e.isDir ? onPath(e.path) : setEdit(e))}
                    >
                      <span className="n">
                        {e.name}
                        {e.isDir ? '/' : ''}
                      </span>
                    </button>
                  ) : (
                    <span style={{ fontSize: 13, fontWeight: 600, color: 'var(--ink-1)' }}>{e.name}</span>
                  )}
                  {/* 副行：目录标「目录」（它没有大小这个概念），文件给「大小 · 修改时间」 */}
                  <div className="fmeta" style={{ marginTop: 3 }}>
                    {e.isDir ? '目录' : `${fmtSize(e.size)} · ${fmtTime(e.modTime)}`}
                  </div>
                </div>
                <div className="fact">
                  {e.text && (
                    <button
                      type="button"
                      className="btn btn-ghost xs"
                      onClick={() => setEdit(e)}
                      aria-label={`编辑 ${e.name}`}
                    >
                      <Icon name="book" />
                      编辑
                    </button>
                  )}
                  {e.zip && (
                    <button type="button" className="btn btn-ghost xs" onClick={() => setExtract(e)}>
                      <Icon name="chest" />
                      解压
                    </button>
                  )}
                  <button
                    type="button"
                    className="btn btn-danger xs"
                    onClick={() => remove(e)}
                    aria-label={`删除 ${e.name}`}
                  >
                    <Icon name="trash" />
                  </button>
                  {/* 不常用的动作收进「更多」：全摆出来会把文件名挤没。
                      目录没有「下载」这一项 —— 后端不支持（打包 zip 还没做），
                      给了就是「看得见点不动」，点了才报错更糟。 */}
                  <RowMenu
                    items={[
                      {
                        label: '重命名',
                        icon: 'cmd',
                        run: () => setNaming({ mode: 'rename', entry: e }),
                      },
                      ...(e.isDir ? [] : [{ label: '下载', icon: 'dl' as const, run: () => download(e) }]),
                    ]}
                  />
                </div>
              </div>
            ))}
          </div>
        )}

        <ConFoot>
          {loading ? '正在读取…' : `${shown.length} 项`} —— 目录名与文本文件名都能点：目录点进去、
          文本文件点开编辑；jar 只有删除；zip 多一个「解压」；「⋯」里是重命名和下载；删除一律先弹确认框
        </ConFoot>
      </Panel>

      {edit && (
        <EditDialog
          id={instance.id}
          entry={edit}
          onClose={() => setEdit(null)}
          onSaved={() => {
            setEdit(null)
            onToast(`${edit.name} 已保存`)
            reload()
          }}
        />
      )}

      {extract && (
        <ExtractDialog
          entry={extract}
          busy={busy}
          onCancel={() => setExtract(null)}
          onPick={(into) => doExtract(extract, into)}
        />
      )}

      {naming && (
        <NameDialog
          title={naming.mode === 'mkdir' ? '新建文件夹' : `重命名「${naming.entry.name}」`}
          label={naming.mode === 'mkdir' ? '文件夹名' : '新的名字'}
          hint={
            naming.mode === 'mkdir'
              ? `会建在「${path || '实例目录'}」下面。名字里不能带 / —— 想在更深的目录里建，先点进去。`
              : '只改名字、不挪位置。名字里不能带 /（那是「移动到别的目录」）。'
          }
          initial={naming.mode === 'mkdir' ? '' : naming.entry.name}
          confirmLabel={naming.mode === 'mkdir' ? '创建' : '改名'}
          busy={busy}
          onClose={() => setNaming(null)}
          onOk={submitName}
        />
      )}

      {dialog}
    </div>
  )
}

/** 文本编辑。能编辑的东西就是「读得出来」的那些 —— 读不出来（二进制 / 太大）的文件压根没有编辑入口。 */
function EditDialog({
  id,
  entry,
  onClose,
  onSaved,
}: {
  id: string
  entry: FileEntry
  onClose: () => void
  onSaved: () => void
}) {
  const [text, setText] = useState<string | null>(null)
  const [saved, setSaved] = useState('')
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    api
      .fileRead(id, entry.path)
      .then((d) => {
        setText(d.content)
        setSaved(d.content)
      })
      .catch((e) => {
        setErr((e as Error).message)
        setText('')
      })
  }, [id, entry.path])

  const dirty = text !== null && text !== saved

  const save = async () => {
    if (text === null) return
    setBusy(true)
    try {
      await api.fileWrite(id, entry.path, text)
      setSaved(text)
      onSaved()
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog
      title={`编辑 ${entry.name}`}
      onClose={onClose}
      width={760}
      actions={
        <>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button type="button" className="btn btn-primary" disabled={busy || !dirty} onClick={save}>
            {busy ? '保存中…' : dirty ? '保存' : '没有改动'}
          </button>
        </>
      }
    >
      <p className="mono-meta mb-2 break-all">
        {entry.path} · {fmtSize(entry.size)}
      </p>
      {err && <p className="mb-2 text-[12.5px] text-[#ffc2c2]">{err}</p>}
      {text === null ? (
        <SkeletonList rows={4} icon={false} />
      ) : (
        <>
          <textarea
            className="file-edit"
            spellCheck={false}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
            改完记得保存，然后重启服务器才会生效。改错了没法撤销 —— 重要文件建议先备份。
          </p>
        </>
      )}
    </Dialog>
  )
}

/** 解压前让用户明确选一次落点。默认那个是安全的（不动现有文件），另一个会并进当前目录。 */
function ExtractDialog({
  entry,
  busy,
  onCancel,
  onPick,
}: {
  entry: FileEntry
  busy: boolean
  onCancel: () => void
  onPick: (into: 'folder' | 'here') => void
}) {
  return (
    <Dialog
      title={`解压「${entry.name}」`}
      onClose={onCancel}
      width={560}
      actions={
        <button type="button" className="btn btn-ghost" onClick={onCancel}>
          取消
        </button>
      }
    >
      <p className="text-[13px] leading-relaxed text-ink-2">
        选解压到哪。如果这个包里只有一层文件夹（网上下来的整合包几乎都这样），
        会自动把那层剥掉 —— 免得你得到 <span className="mono-value">包名/包名/mods</span> 这种还要手动搬一次的目录。
      </p>
      <div className="mt-4 flex flex-col gap-2.5">
        <button
          type="button"
          className="btn btn-primary justify-start"
          disabled={busy}
          onClick={() => onPick('folder')}
        >
          <Icon name="folder" size={14} />
          解压到同名文件夹（推荐）
        </button>
        <p className="pl-1 text-[12px] leading-relaxed text-ink-3">
          会新建一个「{entry.name.replace(/\.zip$/i, '')}」文件夹。已有同名文件夹时会拒绝，不会覆盖。
        </p>

        <button
          type="button"
          className="btn mt-1 justify-start"
          disabled={busy}
          onClick={() => onPick('here')}
        >
          <Icon name="zip" size={14} />
          直接解压到当前目录
        </button>
        <p className="pl-1 text-[12px] leading-relaxed text-ink-3">
          包里的 mods/、config/ 会<b className="text-ink-1">并进当前目录</b>。整合包常用这个；
          但同名文件会被覆盖，心里得有数。
        </p>
      </div>
    </Dialog>
  )
}

type MenuItem = { label: string; icon: string; danger?: boolean; run: () => void }

/**
 * 行尾「更多」菜单（重命名 / 下载 之类不常用的动作收在这里）。
 *
 * 为什么需要它：一行同时要放 编辑 / 解压 / 重命名 / 下载 / 删除 五个动作，
 * 全摆出来会把文件名挤得只剩几个字。常用动作留在外面，其余收进来。
 *
 * ⚠ 用 portal 挂到 body：祖先上的 transform（.frow:hover 带 translateX）会成为定位包含块，
 * 固定定位会被它带跑（守则：fixed 覆盖层一律 portal）。
 */
function RowMenu({ items }: { items: MenuItem[] }) {
  const [open, setOpen] = useState(false)
  const [pos, setPos] = useState({ x: 0, y: 0 })
  const btn = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      if (menu.current?.contains(t) || btn.current?.contains(t)) return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <>
      <button
        ref={btn}
        type="button"
        className="btn btn-ghost xs"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="更多操作"
        title="更多操作"
        onClick={() => {
          const r = btn.current?.getBoundingClientRect()
          // 菜单右缘对齐按钮右缘（向左展开）—— 按钮在行尾，向右开会顶出屏幕
          if (r) setPos({ x: r.right, y: r.bottom + 4 })
          setOpen((v) => !v)
        }}
      >
        <Icon name="lever" size={12} />
      </button>
      {open &&
        createPortal(
          <div
            ref={menu}
            className="rmenu"
            style={{ left: pos.x, top: pos.y, transform: 'translateX(-100%)' }}
            role="menu"
          >
            {items.map((it) => (
              <button
                key={it.label}
                type="button"
                role="menuitem"
                className={`rmenu-i${it.danger ? ' danger' : ''}`}
                onClick={() => {
                  setOpen(false)
                  it.run()
                }}
              >
                <Icon name={it.icon} size={12} />
                {it.label}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </>
  )
}

/**
 * 问一个名字：新建文件夹和重命名共用。
 * 名字里带 / 的一律不让提交 —— 那是「换目录」不是「改名」，混在一起会让用户以为文件飞了。
 */
function NameDialog({
  title,
  label,
  hint,
  initial,
  confirmLabel,
  busy,
  onClose,
  onOk,
}: {
  title: string
  label: string
  hint?: string
  initial: string
  confirmLabel: string
  busy?: boolean
  onClose: () => void
  onOk: (name: string) => void
}) {
  const [v, setV] = useState(initial)
  const ref = useRef<HTMLInputElement>(null)
  useEffect(() => {
    ref.current?.select()
  }, [])
  const name = v.trim()
  const bad = name.length === 0 || name === initial || name.includes('/') || name.includes('\\')
  return (
    <Dialog
      title={title}
      onClose={onClose}
      width={470}
      actions={
        <>
          <button type="button" className="btn btn-ghost" onClick={onClose}>
            取消
          </button>
          <button
            type="button"
            className="btn btn-primary"
            disabled={busy || bad}
            onClick={() => onOk(name)}
          >
            {busy ? '处理中…' : confirmLabel}
          </button>
        </>
      }
    >
      <label className="block text-[12.5px] text-ink-3">{label}</label>
      <input
        ref={ref}
        className="inp mt-1.5"
        style={{ width: '100%' }}
        value={v}
        autoFocus
        spellCheck={false}
        onChange={(e) => setV(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && !bad) onOk(name)
        }}
      />
      {hint && <p className="mt-2 text-[12px] leading-relaxed text-ink-3">{hint}</p>}
    </Dialog>
  )
}

/** 一个路径的「所在目录」前缀（'a/b/c.txt' → 'a/b/'，根目录下为空）。 */
function dirOf(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i + 1)
}

/**
 * 上传一个文件，并把进度回报出来。
 *
 * 用 XHR 而不是 fetch：**fetch 拿不到上传进度**（请求体流式上传在浏览器里支持得还不全），
 * 于是「传一个 100MB 的包到底走到哪了、是不是卡住了」只能靠猜 —— 而猜错的下场是用户
 * 以为卡死、中途关掉，留一个半截文件在磁盘上。
 */
function uploadWithProgress(
  id: string,
  dir: string,
  f: File,
  opts: { extract?: boolean; into?: string },
  onPct: (pct: number) => void,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const fd = new FormData()
    fd.append('file', f)
    fd.append('path', dir)
    if (opts.extract) fd.append('extract', '1')
    if (opts.into) fd.append('into', opts.into)
    const xhr = new XMLHttpRequest()
    xhr.open('POST', `/api/instances/${id}/files/upload`)
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onPct(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve()
        return
      }
      let msg = ''
      try {
        msg = (JSON.parse(xhr.responseText) as { error?: string }).error ?? ''
      } catch {
        /* 不是 JSON（代理 / 网关的错误页）就用状态码兜底 */
      }
      reject(new Error(msg || `上传失败（HTTP ${xhr.status}）`))
    }
    xhr.onerror = () => reject(new Error('上传失败：网络中断'))
    xhr.onabort = () => reject(new Error('上传已中断'))
    xhr.send(fd)
  })
}