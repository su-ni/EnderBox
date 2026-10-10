import { useState } from 'react'
import { api, type ModpackExportOne, type ModpackExportResult, type JobSnapshot } from '../api'
import { JobProgress } from './JobProgress'
import type { Instance } from '../types'
import { Chk, ConHead, Icon, Panel } from './design'

/**
 * 制作整合包 —— 导入的反向操作，所以和「整合包升级」放在同一页。
 *
 * 三段式：**填参数 → 预览 → 导出**。预览那一段不能省：
 * 用户得先看到「会打包哪些文件、哪些查不到会走 overrides、包大概多大」再决定导不导 ——
 * **发出去的包改不了**，预览是唯一的反悔机会。
 *
 * ⚠ 不自己算任何东西，全部交给后端 `/modpack/export/preview`。
 *   前端一旦自己算，就可能出现「预览说 12 个、真导出了 13 个」这种对不上的情况。
 */

function fmtMB(n: number): string {
  if (n <= 0) return '0'
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
  return (n / 1024 / 1024).toFixed(1) + ' MB'
}

/**
 * 勾选项外面那层按钮的重置样式。
 *
 * 写成常量而不是 CSS 类：它只在这一处用，而且这个文件里所有版式都是内联的
 * （跟旁边的字段/按钮保持一致）。**别用原生 checkbox** —— 系统会按自己那套画
 * 圆角/阴影/点击色，在这套像素风格里非常突兀。
 */
const TOGGLE_BTN: React.CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  padding: 0,
  background: 'none',
  border: 0,
  cursor: 'pointer',
  color: 'inherit',
  font: 'inherit',
  textAlign: 'left',
}

/** 一个包的结果卡片 */
function OneResult({ title, r }: { title: string; r?: ModpackExportOne }) {
  if (!r) return null
  return (
    <div className="nbox" style={{ marginTop: 10 }}>
      <div style={{ display: 'flex', alignItems: 'baseline', gap: 8, flexWrap: 'wrap' }}>
        <b style={{ fontSize: 13 }}>{title}</b>
        <span className="mono-meta">
          清单 {r.IndexEntries} 项 · 打包带走的 {r.OverrideFiles} 个
        </span>
        {/* 体积只在预览时有意义（真导出后 Bytes 才是真实大小） */}
        {r.DryRun && <span className="mono-meta">约 {fmtMB(r.OverrideBytes)}</span>}
        {!r.DryRun && r.Bytes > 0 && <span className="mono-meta">{fmtMB(r.Bytes)}</span>}
      </div>

      {/* ⚠ 这两份列表一定要显示出来，不能只给个数字。
          本地模组 → 用户才知道「包为什么这么大」；
          被剔的   → 用户才知道「为什么这个模组不在包里」，直接丢掉会被当成漏了文件。 */}
      {r.LocalFiles.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <div style={{ fontSize: 11.5, color: 'var(--ink-3)' }}>
            不是从 Modrinth 装的，会连文件一起打包带走：
          </div>
          <div style={{ marginTop: 4, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {r.LocalFiles.map((f) => (
              <span key={f} className="chip">
                {f}
              </span>
            ))}
          </div>
        </div>
      )}
      {r.Skipped.length > 0 && (
        <div style={{ marginTop: 8 }}>
          <div style={{ fontSize: 11.5, color: 'var(--ink-3)' }}>这个包装不了、已剔掉：</div>
          <div style={{ marginTop: 4, display: 'flex', flexWrap: 'wrap', gap: 4 }}>
            {r.Skipped.map((f) => (
              <span key={f} className="chip chip-w">
                {f}
              </span>
            ))}
          </div>
        </div>
      )}
      {!r.DryRun && r.Path && (
        <div className="mono-meta" style={{ marginTop: 8, wordBreak: 'break-all' }}>
          已导出：{r.Path}
        </div>
      )}
    </div>
  )
}

export function ModpackExportPanel({
  instance,
  onToast,
}: {
  // ⚠ 只要 id 和 name：ModpackPanel 拿到的实例本来就是 `Pick<Instance, 'id' | 'name'>`，
  //   而这里也确实只用到这两个字段。别为了"以后可能用到"把类型放宽成完整 Instance ——
  //   那会把一个宽松的契约悄悄变成紧的。
  instance: Pick<Instance, 'id' | 'name'>
  onToast: (s: string) => void
}) {
  const [name, setName] = useState('')
  const [version, setVersion] = useState('')
  const [loaderVer, setLoaderVer] = useState('')
  const [wantClient, setWantClient] = useState(true)
  const [wantServer, setWantServer] = useState(true)
  const [result, setResult] = useState<ModpackExportResult | null>(null)
  const [busy, setBusy] = useState<'' | 'preview' | 'export'>('')
  const [err, setErr] = useState('')

  const query = () => ({
    name,
    version,
    loaderVer,
    // 后端用 `!= "0"` 判断，所以关掉才传 '0'
    client: wantClient ? undefined : '0',
    server: wantServer ? undefined : '0',
  })

  const preview = async () => {
    setBusy('preview')
    setErr('')
    try {
      const r = await api.modpackExportPreview(instance.id, query())
      setResult(r)
    } catch (e) {
      setErr((e as Error).message)
      setResult(null)
    } finally {
      setBusy('')
    }
  }

  const [exportJob, setExportJob] = useState<string | null>(null)

  const onExportFinish = (j: JobSnapshot) => {
    setExportJob(null)
    setBusy('')
    if (j.state === 'done') {
      setResult(j.result as unknown as Parameters<typeof setResult>[0])
      onToast('整合包已导出')
    } else if (j.state === 'canceled') {
      setErr('导出已取消')
    } else {
      setErr(j.err || '导出失败')
    }
  }

  const doExport = async () => {
    setBusy('export')
    setErr('')
    try {
      const r = await api.modpackExport(instance.id, query())
      setExportJob(r.jobId)
    } catch (e) {
      setErr((e as Error).message)
      setBusy('')
    }
  }

  return (
    <Panel style={{ '--rd': '.16s' } as React.CSSProperties}>
      <ConHead
        icon="cube"
        iconColor="var(--cyan)"
        title="制作整合包"
        sub="把这台服打包成 .mrpack，发给别人装"
      />
      <div style={{ padding: '12px 16px 16px' }}>
        {exportJob ? (
          <JobProgress jobId={exportJob} label="正在导出" onFinish={onExportFinish} />
        ) : null}
        <div className="field">
          <div>
            <div className="lb">包名</div>
            <div className="hint">留空就用实例名「{instance.name}」</div>
          </div>
          <input
            className="inp"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={instance.name}
            aria-label="包名"
          />
        </div>

        <div className="field">
          <div>
            <div className="lb">版本号</div>
            <div className="hint">留空用今天的日期</div>
          </div>
          <input
            className="inp"
            value={version}
            onChange={(e) => setVersion(e.target.value)}
            placeholder="例如 1.0.0"
            aria-label="版本号"
          />
        </div>

        <div className="field">
          <div>
            <div className="lb">加载器版本</div>
            <div className="hint">
              实例里只记了「用哪种加载器」，没记版本号 —— 填了才会写进清单
            </div>
          </div>
          <input
            className="inp"
            value={loaderVer}
            onChange={(e) => setLoaderVer(e.target.value)}
            placeholder="例如 0.15.0"
            aria-label="加载器版本"
          />
        </div>

        {/* 两种包的开关。默认都要 —— 用户心智是「我有一台服，要分发给两类人」。
            ⚠ 用设计稿的 `.chk` 而不是原生 checkbox：原生控件在这套像素风格里非常突兀
              （系统会按自己那套画圆角/阴影/点击色），而且没法统一进「硬边框 + 青色选中」的语言。
              项目里 CreateWizard / design.tsx 都是这么用的，我一开始图省事写了原生控件。 */}
        <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap', marginTop: 14 }}>
          <button
            type="button"
            onClick={() => setWantClient((v) => !v)}
            aria-pressed={wantClient}
            style={TOGGLE_BTN}
          >
            <Chk on={wantClient} />
            <span style={{ fontSize: 12.5 }}>客户端包（给玩家装进启动器）</span>
          </button>
          <button
            type="button"
            onClick={() => setWantServer((v) => !v)}
            aria-pressed={wantServer}
            style={TOGGLE_BTN}
          >
            <Chk on={wantServer} />
            <span style={{ fontSize: 12.5 }}>服务端包（给别的服主）</span>
          </button>
        </div>

        <div style={{ display: 'flex', gap: 8, marginTop: 12, flexWrap: 'wrap' }}>
          <button
            type="button"
            className="btn"
            disabled={busy !== '' || (!wantClient && !wantServer)}
            onClick={() => void preview()}
          >
            <Icon name="ul" />
            {busy === 'preview' ? '计算中…' : '预览'}
          </button>
          <button
            type="button"
            className="btn btn-primary"
            // 没预览过不让导出 —— 让人先看清楚再落盘
            disabled={busy !== '' || !result}
            onClick={() => void doExport()}
          >
            <Icon name="cube" />
            {busy === 'export' ? '导出中…' : '导出'}
          </button>
          {!wantClient && !wantServer && (
            <span style={{ fontSize: 11.5, color: 'var(--warn)', alignSelf: 'center' }}>
              两种包至少要选一个
            </span>
          )}
        </div>

        {err && (
          <div className="nbox d" style={{ marginTop: 12 }}>
            {err}
          </div>
        )}

        {result && (
          <div style={{ marginTop: 12 }}>
            <OneResult title="客户端包" r={result.Client} />
            <OneResult title="服务端包" r={result.Server} />
          </div>
        )}
      </div>
    </Panel>
  )
}
