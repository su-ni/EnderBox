import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type JobSnapshot, type SaveInfo } from '../api'
import { ConFoot, ConHead, Icon, Panel } from './design'
import { JobProgress } from './JobProgress'
import { Empty, SkeletonList, fmtSize, useConfirm } from './ui'
import type { Instance } from '../types'

type Props = {
  instance: Instance
  /** 服务端是否在跑。运行中不能切换世界（level-name 只在启动时读一次）。 */
  running: boolean
  onToast: (msg: string) => void
  /** 面板编号。放在多面板页（如备份页）里时传，跟同页其它面板连号；不传则用图标。 */
  num?: string
}

/**
 * 「世界存档」块 —— 单存档的搬运：导出 / 导入 / 切换当前世界。
 *
 * 和「备份与还原」的区别（别合并）：备份是**回到过去某个时间点**，范围是整目录或全存档；
 * 这里是**把某一个世界搬去别处**，对象是单个 world 目录。两边都走 zipDir，但语义、
 * 落脚目录、命名都不同。
 *
 * 三件事都是「分钟级慢操作」：导出/导入走统一作业表（进度进右下角气泡），
 * 上传本身用 XHR 报进度（fetch 拿不到上传进度）。
 */
export function SavesPanel({ instance, running, onToast, num }: Props) {
  const [items, setItems] = useState<SaveInfo[]>([])
  const [active, setActive] = useState('')
  const [loading, setLoading] = useState(true)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')

  // 当前在跑的那笔作业（导出 / 导入共用一条进度条 + 一个回调）
  const [jobId, setJobId] = useState<string | null>(null)
  const [jobKind, setJobKind] = useState<'export' | 'import' | ''>('')
  const [jobWorld, setJobWorld] = useState('')
  // 导出完给的下载条。产物在服务器上，什么时候下、下几次都行 —— 所以不自动跳下载。
  const [zip, setZip] = useState<{ file: string; world: string; size: number } | null>(null)

  // 导入
  const [impOpen, setImpOpen] = useState(false)
  const [impName, setImpName] = useState('')
  const [impFile, setImpFile] = useState<File | null>(null)
  const [impPct, setImpPct] = useState<number | null>(null)
  const [impErr, setImpErr] = useState('')
  const impRef = useRef<HTMLInputElement>(null)

  const { dialog, confirm } = useConfirm()

  const reload = useCallback(async () => {
    try {
      const d = await api.saves(instance.id)
      setItems(d.items ?? [])
      setActive(d.active ?? '')
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
      setItems([])
    } finally {
      setLoading(false)
    }
  }, [instance.id])

  // 换实例时把这一块的临时态全清掉 —— 留着上一个实例的下载条/导入表单会张冠李戴。
  useEffect(() => {
    setItems([])
    setActive('')
    setZip(null)
    setJobId(null)
    setJobKind('')
    setImpOpen(false)
    setImpFile(null)
    setImpName('')
    setImpErr('')
    setLoading(true)
    reload()
  }, [reload])

  const onJobFinish = (j: JobSnapshot) => {
    const kind = jobKind
    setJobId(null)
    setBusy('')
    setJobKind('')
    if (j.state === 'done') {
      const res = j.result as { world?: string; name?: string; size?: number; files?: number }
      const world = res.world ?? jobWorld
      if (kind === 'import') {
        onToast(`已导入「${world}」，共 ${res.files ?? 0} 个文件（${fmtSize(res.size ?? 0)}）`)
      } else {
        setZip({ file: res.name ?? '', world, size: res.size ?? 0 })
        onToast(`「${world}」已打包好（${fmtSize(res.size ?? 0)}），可以下载了`)
      }
      reload()
    } else if (j.state === 'canceled') {
      onToast(kind === 'import' ? '导入已取消' : '导出已取消')
    } else {
      onToast(j.err || (kind === 'import' ? '导入失败' : '导出失败'))
    }
  }

  const doExport = async (s: SaveInfo) => {
    setBusy('export:' + s.name)
    setJobKind('export')
    setJobWorld(s.name)
    setZip(null)
    try {
      const r = await api.saveExport(instance.id, s.name)
      setJobId(r.jobId)
    } catch (e) {
      setBusy('')
      setJobKind('')
      onToast((e as Error).message)
    }
  }

  const doActivate = (s: SaveInfo) =>
    confirm({
      title: `把「${s.name}」设为当前世界`,
      confirmLabel: '设为当前世界',
      run: async () => {
        try {
          await api.saveActivate(instance.id, s.name)
          onToast(`已切到「${s.name}」—— 下次开服会加载它，原来的世界先留着`)
          reload()
        } catch (e) {
          onToast((e as Error).message)
        }
      },
      body: (
        <>
          这只改 <span className="mono-value">server.properties</span> 里的{' '}
          <span className="mono-value">level-name</span>，指向 <b className="text-ink-1">{s.name}</b>。
          原来的世界不会被动，还在磁盘上。
          <br />
          开服时服务端读一次这个名字，所以改完要<b className="text-ink-1">重启</b>才生效。
        </>
      ),
    })

  const doImport = async () => {
    if (!impFile) {
      setImpErr('先选一个 .zip 存档包')
      return
    }
    const name = impName.trim()
    if (!name) {
      setImpErr('给这个存档起个名字（就是它以后在磁盘上的目录名）')
      return
    }
    setBusy('import')
    setImpErr('')
    setImpPct(0)
    try {
      const r = await api.saveImport(instance.id, name, impFile, setImpPct)
      setImpPct(null)
      setJobKind('import')
      setJobWorld(name)
      setJobId(r.jobId)
      setImpOpen(false)
      setImpFile(null)
      setImpName('')
    } catch (e) {
      setImpPct(null)
      setBusy('')
      setImpErr((e as Error).message)
    }
  }

  const pickFile = (f: File | null) => {
    setImpFile(f)
    setImpErr('')
    // 顺手用文件名当默认世界名（去掉扩展名、去掉打包工具常见的日期尾巴），用户能改
    if (f && !impName.trim()) {
      const base = f.name.replace(/\.zip$/i, '').replace(/[-_]\d{8}[-_]?\d{0,6}$/, '')
      setImpName(base)
    }
  }

  const total = items.reduce((s, i) => s + i.size, 0)

  return (
    <Panel style={{ '--rd': '.16s' } as React.CSSProperties}>
      <ConHead
        num={num}
        icon={num ? undefined : 'chest'}
        title="世界存档"
        sub={
          loading
            ? '正在读取…'
            : items.length > 0
              ? `${items.length} 个世界 · 共 ${fmtSize(total)}`
              : '还没有世界目录'
        }
      >
        <div className="spacer" />
        <button type="button" className="btn btn-ghost sm" onClick={reload}>
          <Icon name="refresh" />
          刷新
        </button>
        <button
          type="button"
          className="btn sm"
          disabled={busy === 'import'}
          onClick={() => {
            setImpOpen((v) => !v)
            setImpErr('')
          }}
        >
          <Icon name="ul" />
          导入存档
        </button>
      </ConHead>

      {jobId && (
        <div style={{ padding: '0 16px 12px' }}>
          <JobProgress
            jobId={jobId}
            label={jobKind === 'import' ? '正在导入存档' : '正在打包存档'}
            onFinish={onJobFinish}
          />
        </div>
      )}

      {/* 导出完成后给一条下载。产物留在服务器上，不自动触发下载 —— 用户可能只想先存着。 */}
      {zip && zip.file && (
        <div className="fupbar" style={{ margin: '0 16px 12px' }}>
          <span className="step-line">
            已打包 <b>{zip.world}</b> · {fmtSize(zip.size)}
          </span>
          <div className="spacer" style={{ marginLeft: 'auto' }} />
          <a className="btn btn-primary sm" href={api.saveDownloadUrl(instance.id, zip.file)} download={zip.file}>
            <Icon name="dl" />
            下载 zip
          </a>
          <button type="button" className="btn btn-ghost xs" onClick={() => setZip(null)} aria-label="收起">
            <Icon name="x" />
          </button>
        </div>
      )}

      {impOpen && (
        <div style={{ padding: '14px 16px 16px', borderBottom: '1px solid var(--line)' }}>
          <div style={{ fontSize: 12.5, fontWeight: 600 }}>从 .zip 导入一个世界</div>
          <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 4, lineHeight: 1.6 }}>
            包可以直接是世界目录的内容（里面有 level.dat），也可以套着一层文件夹 ——
            两种都能认出来。名字撞到已有的世界会拒绝，不会覆盖。
          </p>
          <div style={{ display: 'flex', gap: 10, marginTop: 12, flexWrap: 'wrap' }}>
            <input
              ref={impRef}
              type="file"
              accept=".zip"
              className="hidden"
              onChange={(e) => {
                pickFile(e.target.files?.[0] ?? null)
                e.target.value = ''
              }}
            />
            <button type="button" className="btn sm" onClick={() => impRef.current?.click()}>
              <Icon name="chest" />
              {impFile ? impFile.name : '选一个 zip'}
            </button>
            <input
              className="inp"
              style={{ width: 200, maxWidth: '100%' }}
              value={impName}
              maxLength={48}
              placeholder="世界名（目录名）"
              aria-label="导入后的世界名"
              onChange={(e) => setImpName(e.target.value)}
            />
            <button
              type="button"
              className="btn btn-primary sm"
              disabled={busy === 'import'}
              onClick={doImport}
            >
              <Icon name="check" />
              {busy === 'import' ? '上传中…' : '开始导入'}
            </button>
            <button type="button" className="btn btn-ghost sm" onClick={() => setImpOpen(false)}>
              取消
            </button>
          </div>
          {impPct != null && (
            <div className="fupbar" style={{ marginTop: 10 }}>
              <span className="step-line">
                正在上传 <b className="mono-value">{impPct}%</b>
              </span>
            </div>
          )}
          {impErr && (
            <p style={{ fontSize: 12, color: 'var(--bad)', marginTop: 8 }}>{impErr}</p>
          )}
        </div>
      )}

      {err ? (
        <div className="banner-danger" style={{ margin: 14 }}>
          <Icon name="ban" style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>读世界列表没成功</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn sm" onClick={reload}>
            <Icon name="refresh" />
            重试
          </button>
        </div>
      ) : loading ? (
        <div style={{ padding: 14 }}>
          <SkeletonList rows={2} />
        </div>
      ) : items.length === 0 ? (
        <div style={{ padding: 14 }}>
          <Empty>
            这个实例里还没有世界目录（要有 level.dat 才算）。开一次服就会生成一个，
            或者用上面的「导入存档」搬一个进来。
          </Empty>
        </div>
      ) : (
        <div className="rlist">
          {items.map((s) => {
            const isActive = s.name === active || s.active
            const exporting = busy === 'export:' + s.name
            return (
              <div key={s.name} className="rrow">
                <div className="ricon">
                  <Icon name="chest" size={18} style={{ color: isActive ? 'var(--ok)' : 'var(--cyan)' }} />
                </div>
                <div className="rmain">
                  <div className="rtitle">
                    {s.name}
                    {isActive && <span className="chip s g">当前使用</span>}
                  </div>
                  <div className="rsub">
                    {fmtSize(s.size)} · {s.files} 个文件
                  </div>
                </div>
                <div className="ract">
                  <button
                    type="button"
                    className="btn btn-ghost xs"
                    disabled={exporting}
                    onClick={() => doExport(s)}
                  >
                    <Icon name="dl" />
                    {exporting ? '打包中…' : '导出'}
                  </button>
                  <button
                    type="button"
                    className="btn btn-primary xs"
                    disabled={isActive || running}
                    title={
                      running
                        ? '先停服才能切换 —— level-name 只在启动时读一次'
                        : isActive
                          ? '已经是当前世界了'
                          : `下次开服加载「${s.name}」`
                    }
                    onClick={() => doActivate(s)}
                  >
                    {isActive ? '使用中' : '切换'}
                  </button>
                </div>
              </div>
            )
          })}
        </div>
      )}

      <ConFoot>
        {running
          ? '服务端正在运行：导出会先让服务端刷盘再打包（不会拿到半个区块）；切换世界请先停服。'
          : '导出的是这一个世界目录；想连插件配置一起备，用「备份与还原」。'}
      </ConFoot>
      {dialog}
    </Panel>
  )
}
