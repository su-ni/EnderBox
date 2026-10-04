import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type PluginItem } from '../api'
import { IconPackage, IconSearch, IconTrash, IconUpload } from './Icons'
import { Empty, ListRow, NotReady, SectionHead, Toggle, fmtSize, fmtTime } from './ui'
import type { Instance } from '../types'

/** 模组端放 mods，插件端放 plugins —— 告诉用户往哪丢文件，别让他猜 */
function contentDirLabel(type: string): string {
  return type === 'fabric' || type === 'forge' || type === 'neoforge' ? 'mods' : 'plugins'
}

type Props = {
  instance: Instance
  onToast: (msg: string) => void
}

export function PluginsPanel({ instance, onToast }: Props) {
  const [items, setItems] = useState<PluginItem[]>([])
  const [dir, setDir] = useState('')
  const [busy, setBusy] = useState(false)
  const [drag, setDrag] = useState(false)
  const [filter, setFilter] = useState('')
  const [err, setErr] = useState('')
  const fileRef = useRef<HTMLInputElement>(null)

  const reload = useCallback(async () => {
    try {
      const d = await api.plugins(instance.id)
      setItems(d.items ?? [])
      setDir(d.dir)
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [instance.id])

  useEffect(() => {
    setItems([])
    reload()
  }, [reload])

  const upload = async (f: File) => {
    setBusy(true)
    try {
      const r = await api.pluginUpload(instance.id, f)
      onToast(`已放入 ${contentDirLabel(instance.type)}/${r.file} —— 重启服务器后生效`)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const toggle = async (p: PluginItem, on: boolean) => {
    try {
      await api.pluginToggle(instance.id, p.file, on)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const remove = async (p: PluginItem) => {
    if (!window.confirm(`删除「${p.name}」？\n\n文件会从磁盘上删掉，删了就得重新下。`)) return
    try {
      await api.pluginDelete(instance.id, p.file)
      reload()
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const onCount = items.filter((i) => i.enabled).length
  const dirName = contentDirLabel(instance.type)

  // 装了二三十个插件之后，靠眼睛在列表里找某一个是不现实的
  const fq = filter.trim().toLowerCase()
  const shown = !fq
    ? items
    : items.filter((p) =>
        (p.name + ' ' + p.file + ' ' + (p.version ?? '')).toLowerCase().includes(fq),
      )

  return (
    <div className="min-w-0">
      <h1 className="text-[22px] leading-tight font-bold text-ink-1">插件与模组</h1>
      <p className="mt-2 max-w-[68ch] text-[13.5px] leading-relaxed text-ink-2">
        正在管 <b className="text-ink-1">{instance.name}</b> 的 {dirName} 目录。
        这里的东西只有重启服务器才会重新加载。
      </p>

      {err && (
        <p className="mt-4 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
          {err}
        </p>
      )}

      {/* ── 已安装 ── */}
      <div className="mt-7">
        <SectionHead
          idx="01"
          title="已安装"
          note={
            fq
              ? `${shown.length} / ${items.length}`
              : items.length > 0
                ? `${items.length} 个 · 启用 ${onCount} 个`
                : "目录是空的"
          }
          right={
            <button type="button" className="btn btn-ghost btn-sm" onClick={reload}>
              刷新
            </button>
          }
        />

        {dir && <p className="mono-meta mb-3 break-all">{dir}</p>}

        {items.length > 0 && (
          <div className="search mb-4 flex max-w-[360px] items-center gap-2.5 px-3 py-2">
            <span className="shrink-0 text-ink-3">
              <IconSearch size={13} />
            </span>
            <input
              type="search"
              className="min-w-0 flex-1 bg-transparent font-mono text-[12.5px] text-ink-1 outline-0 placeholder:text-ink-3"
              placeholder="按名字 / 文件名过滤"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            {fq && <span className="mono-meta shrink-0">{shown.length} / {items.length}</span>}
          </div>
        )}

        {items.length === 0 ? (
          <Empty>
            这个 {dirName} 目录还是空的。从下面传一个 jar，或者手动把文件丢进上面的目录再点刷新。
          </Empty>
        ) : shown.length === 0 ? (
          <Empty>没有匹配「{filter.trim()}」的插件。</Empty>
        ) : (
          <div className="stagger px-card">
            {shown.map((p) => (
              <ListRow
                key={p.file}
                icon={
                  <span style={{ opacity: p.enabled ? 1 : 0.4 }}>
                    <IconPackage size={18} />
                  </span>
                }
                title={p.name}
                tags={p.version ? <span className="chip">{p.version}</span> : undefined}
                meta={
                  <>
                    {/* 窄屏只留文件名和时间：三个字段挤一行会把时间戳截掉，而时间是这里最该看清的 */}
                    <span className="sm:hidden">
                      {p.file} · {fmtTime(p.modTime)}
                    </span>
                    <span className="hidden sm:inline">
                      {p.file} · {fmtSize(p.size)} · {fmtTime(p.modTime)}
                    </span>
                  </>
                }
                actions={
                  <>
                    <span title={p.enabled ? '已启用' : '已停用'}>
                      <Toggle on={p.enabled} onChange={(v) => toggle(p, v)} label={`${p.name} 启用状态`} />
                    </span>
                    <button
                      type="button"
                      className="btn btn-ghost btn-sm"
                      onClick={() => remove(p)}
                      aria-label={`删除 ${p.name}`}
                    >
                      <IconTrash size={12} />
                    </button>
                  </>
                }
              />
            ))}
          </div>
        )}
      </div>

      {/* ── 添加 ── */}
      <div className="mt-8">
        <SectionHead idx="02" title="放进一个 jar" note="支持 .jar，同名文件会覆盖" />
        <div
          onDragOver={(e) => {
            e.preventDefault()
            setDrag(true)
          }}
          onDragLeave={() => setDrag(false)}
          onDrop={(e) => {
            e.preventDefault()
            setDrag(false)
            const f = e.dataTransfer.files?.[0]
            if (f) upload(f)
          }}
          className="px-card flex flex-wrap items-center gap-x-4 gap-y-3 px-4 py-4"
          style={drag ? { background: 'var(--color-raised)' } : undefined}
        >
          <span className="text-accent">
            <IconUpload size={18} />
          </span>
          <p className="min-w-0 flex-1 text-[13px] text-ink-2">
            把 jar 拖到这里，或者从磁盘挑一个 —— 一次一个
          </p>
          <button
            type="button"
            className="btn btn-grass"
            disabled={busy}
            onClick={() => fileRef.current?.click()}
          >
            {busy ? '上传中…' : '选择文件'}
          </button>
          <input
            ref={fileRef}
            type="file"
            accept=".jar"
            className="hidden"
            onChange={(e) => {
              const f = e.target.files?.[0]
              if (f) upload(f)
              e.target.value = ''
            }}
          />
        </div>
      </div>

      {/* ── 在线市场 ── */}
      <div className="mt-8">
        <SectionHead idx="03" title="在线市场" note="Modrinth / CurseForge" />
        <NotReady>
          在线搜索还没接。要真接上得先解决三件事：Modrinth 与 CurseForge 两套 API 的字段对齐、
          按 MC 版本和加载器过滤、以及下载走的镜像源（国内直连经常超时）。
          在那之前，你从 Modrinth 或 MCBBS 下好 jar，用上面那块传进来，效果一样。
        </NotReady>
      </div>
    </div>
  )
}
