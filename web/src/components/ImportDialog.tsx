import { useCallback, useEffect, useState } from 'react'
import { api, type DirListing, type ImportScan } from '../api'
import { Dialog } from './ui'
import { Icon } from './design'
import { TYPE_LABEL } from '../types'

type Props = {
  onClose: () => void
  /** 导入成功：把新实例 id 交给上层去选中并刷新列表 */
  onImported: (id: string) => void
}

type Form = {
  name: string
  type: string
  mcVersion: string
  jarPath: string
  port: string
}

/**
 * 导入已有服务器。
 * 先浏览目录挑一个，再让后端识别「这是什么服务端」，用户确认（可改几项）后登记成实例。
 * 文件原地不动 —— 只是把已有的那份目录纳管进来，不复制也不移动。
 */
export function ImportDialog({ onClose, onImported }: Props) {
  const [listing, setListing] = useState<DirListing | null>(null)
  const [path, setPath] = useState('')
  const [scan, setScan] = useState<ImportScan | null>(null)
  const [form, setForm] = useState<Form | null>(null)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')

  const loadDirs = useCallback(async (p: string) => {
    setErr('')
    try {
      const d = await api.fsDirs(p)
      setListing(d)
      setPath(d.path)
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [])

  useEffect(() => {
    loadDirs('')
  }, [loadDirs])

  const runScan = async (dir: string) => {
    if (!dir.trim()) return
    setBusy(true)
    setErr('')
    try {
      const sc = await api.importScan(dir.trim())
      setScan(sc)
      setForm({
        name: sc.name,
        type: sc.type,
        mcVersion: sc.mcVersion,
        jarPath: sc.jarPath,
        port: String(sc.port),
      })
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy(false)
    }
  }

  const confirm = async () => {
    if (!scan || !form) return
    const port = Number(form.port)
    if (!Number.isFinite(port) || port < 1 || port > 65535) {
      setErr('端口要在 1-65535 之间')
      return
    }
    setBusy(true)
    setErr('')
    try {
      const r = await api.doImport({
        dir: scan.dir,
        name: form.name.trim(),
        type: form.type,
        mcVersion: form.mcVersion.trim(),
        loaderVersion: scan.loaderVersion,
        jarPath: form.jarPath.trim(),
        port,
      })
      onImported(r.instance.id)
      onClose()
    } catch (e) {
      setErr((e as Error).message)
      setBusy(false)
    }
  }

  return (
    <Dialog
      title="导入已有服务器"
      width={660}
      onClose={onClose}
      actions={
        <>
          <button type="button" className="btn btn-ghost" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button
            type="button"
            className="btn btn-primary"
            onClick={confirm}
            disabled={!scan || busy}
          >
            {busy ? '处理中…' : '导入'}
          </button>
        </>
      }
    >
      <p className="max-w-[76ch] text-[12.5px] leading-[1.7] text-ink-2">
        选一个已经在跑的服务端目录 —— 文件原地不动，只是把它登记进来，之后就能在这个面板里启停、看日志、配置和备份。
      </p>

      {/* 路径栏 */}
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <input
          className="px-input px-input-mono min-w-0 flex-1"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          placeholder="例如 D:\mc\server，或从下面逐层点进去"
          spellCheck={false}
        />
        <button
          type="button"
          className="btn btn-ghost sm shrink-0"
          onClick={() => runScan(path)}
          disabled={busy || !path.trim()}
        >
          扫描这个目录
        </button>
        <button
          type="button"
          className="btn btn-ghost sm shrink-0"
          onClick={() => loadDirs(listing?.parent ?? '')}
          disabled={!listing?.parent}
        >
          上一级
        </button>
      </div>

      {err && (
        <div className="nbox d" style={{ marginTop: 10 }}>
          {err}
        </div>
      )}

      {/* 目录列表（选了目录、出了识别结果之后就收起来，避免和结果抢注意力） */}
      {!scan && listing && (
        <div className="scroll-thin mt-3 max-h-[240px] overflow-y-auto border-t border-line">
          {listing.dirs.length === 0 && (
            <p className="px-1 py-3 text-[12.5px] text-ink-3">这个目录下没有子文件夹。</p>
          )}
          {listing.dirs.map((d) => (
            <button
              key={d.path}
              type="button"
              onClick={() => loadDirs(d.path)}
              className="flex w-full items-center gap-2.5 border-b border-line px-1.5 py-2 text-left hover:bg-panel-2"
            >
              <span className="shrink-0 text-ink-3">
                <Icon name="folder" size={13} />
              </span>
              <span className="min-w-0 flex-1 truncate text-[13px] text-ink-1">{d.name}</span>
              {d.looksLikeServer && <span className="chip s g shrink-0">像服务端</span>}
            </button>
          ))}
        </div>
      )}

      {/* 识别结果 + 可改的几项 */}
      {scan && form && (
        <div style={{ marginTop: 16, borderTop: '1px solid var(--line-1)', paddingTop: 12 }}>
          <p className="eyebrow">识别结果</p>
          <p className="mono-meta mt-1 break-all">{scan.dir}</p>

          <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2">
            <label className="block min-w-0">
              <span className="field-label">实例名</span>
              <input
                className="px-input mt-1 w-full"
                value={form.name}
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </label>

            <label className="block min-w-0">
              <span className="field-label">类型</span>
              <select
                className="px-input mt-1 w-full"
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value })}
              >
                {Object.entries(TYPE_LABEL).map(([k, v]) => (
                  <option key={k} value={k}>
                    {v}
                  </option>
                ))}
              </select>
            </label>

            <label className="block min-w-0">
              <span className="field-label">游戏版本</span>
              <input
                className="px-input px-input-mono mt-1 w-full"
                value={form.mcVersion}
                onChange={(e) => setForm({ ...form, mcVersion: e.target.value })}
                placeholder="例如 1.21.4"
                spellCheck={false}
              />
            </label>

            <label className="block min-w-0">
              <span className="field-label">端口</span>
              <input
                className="px-input px-input-mono mt-1 w-full"
                value={form.port}
                inputMode="numeric"
                onChange={(e) => setForm({ ...form, port: e.target.value })}
              />
            </label>

            <label className="block min-w-0 sm:col-span-2">
              <span className="field-label">启动文件</span>
              {scan.candidates.length > 0 ? (
                <select
                  className="px-input px-input-mono mt-1 w-full"
                  value={form.jarPath}
                  onChange={(e) => setForm({ ...form, jarPath: e.target.value })}
                >
                  {/* 找出来但当前选中的不在名单里时补一项，免得选择框显示成空白 */}
                  {!scan.candidates.includes(form.jarPath) && (
                    <option value={form.jarPath}>{form.jarPath}</option>
                  )}
                  {scan.candidates.map((c) => (
                    <option key={c} value={c}>
                      {c}
                    </option>
                  ))}
                </select>
              ) : (
                <input
                  className="px-input px-input-mono mt-1 w-full"
                  value={form.jarPath}
                  onChange={(e) => setForm({ ...form, jarPath: e.target.value })}
                  spellCheck={false}
                />
              )}
            </label>
          </div>

          {scan.notes.length > 0 && (
            <ul className="mt-3 space-y-1.5">
              {scan.notes.map((n, i) => (
                <li key={i} className="flex gap-2 text-[12px] leading-[1.7] text-ink-2">
                  <span className="mt-0.5 shrink-0 text-warn">
                    <Icon name="info" size={12} />
                  </span>
                  <span>{n}</span>
                </li>
              ))}
            </ul>
          )}

          <button
            type="button"
            className="btn btn-ghost sm mt-3"
            onClick={() => {
              setScan(null)
              setForm(null)
            }}
          >
            换个目录
          </button>
        </div>
      )}
    </Dialog>
  )
}