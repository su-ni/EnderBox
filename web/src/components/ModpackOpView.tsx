import type { ModpackOp } from '../api'
import { Icon } from './design'
import { fmtSize } from './ui'

/**
 * 一笔整合包作业（安装 / 升级）的进度与结算。
 *
 * 为什么从 ModpackPanel 里抽出来：整合包市场装新实例要显示的是**同一种**进度。
 * 抄第二份的话，那些判断（「done 但有 failed 不算全部成功」「失败要逐条列出来」
 * 「取消只在进行中可点」）各有两份实现，总有一边会漏。
 *
 * 两条容易写错的地方，都在下面标出来了：
 *   ① 动词必须由调用方给，**不能**从 op.kind 推 —— 见 verb 参数的说明；
 *   ② 服务端文件那一段（stage=jar）文件数恒为 0，进度条只能走「未知总量」形态，
 *      不给阶段名的话用户会对着一个空进度条等十几分钟。
 */
export function ModpackOpView({
  op,
  opErr,
  busy,
  verb,
  onCancel,
  onOpenInstance,
}: {
  op: ModpackOp | null
  opErr: string
  /** 正在忙的那个按钮标识（'cancel'）—— 只锁自己，别整块变灰 */
  busy: string
  /**
   * 「安装」或「升级」。**由调用方定，不从 op.kind 推**：
   * 往一个已有实例里装本地 .mrpack 时，后端记的 kind 是 upgrade（它走的是同一个接口），
   * 但用户看到的是「安装」。拿 kind 当动词，那一步会显示成「升级完成」。
   */
  verb: string
  onCancel: () => void
  /** 给了且作业已成功完成时，显示「打开这个实例」（市场装完要跳过去看） */
  onOpenInstance?: () => void
}) {
  if (opErr && !op) {
    return (
      <div className="nbox d mt-3.5">
        <p>{opErr}</p>
      </div>
    )
  }
  if (!op) {
    return <p className="mt-3.5 text-[12.5px] text-ink-3">正在起{verb}任务…</p>
  }

  const running = op.state === 'running'
  const p = op.progress
  // 进度以「文件数」为主（每份文件一次落盘，最贴合用户感知）。
  // filesTotal 为 0 时（准备服务端文件那一段、或极少见的空计划）退成「在动」的未知态，
  // 别假装 100% —— 一个走到头的进度条配着「还在装」是自相矛盾的。
  const pct = p.filesTotal > 0 ? Math.min(100, (p.filesDone / p.filesTotal) * 100) : 0
  const unknown = p.filesTotal <= 0

  const stateLabel =
    op.state === 'running'
      ? `${verb}中`
      : op.state === 'done'
        ? `${verb}完成`
        : op.state === 'canceled'
          ? '已取消'
          : `${verb}失败`

  // 阶段：服务端文件和模组是两件不同的事，说清现在在哪一段。
  // jar 那一段要跑 Forge 的安装器，几百 MB、好几分钟，而文件数一直是 0 ——
  // 不点名阶段，用户只会看到进度条不动。
  const stage = running ? stageLabel(op.stage, verb) : ''

  /*
    作业卡的色调跟着状态走。设计稿的 nbox 只有中性 / 警告（w）/ 危险（d）三态，
    「成功」和「进行中」是本产品必需的两态 —— 按同一套材质补在覆盖层（.nbox.ok / .nbox.live）。
  */
  const tone =
    op.state === 'done' ? ' ok' : op.state === 'failed' ? ' d' : op.state === 'canceled' ? ' w' : ' live'

  return (
    <div className={`nbox${tone} mt-3.5`}>
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="text-[13px] font-semibold text-ink-1">{stateLabel}</span>
        {stage && <span className="chip s">{stage}</span>}
        {running && !unknown && (
          <span className="mono-meta">
            {p.filesDone} / {p.filesTotal} 个文件
          </span>
        )}
        {running && p.current && (
          <span className="mono-meta ml-auto max-w-full truncate" title={p.current}>
            正在处理 {p.current}
          </span>
        )}
      </div>

      {running && (
        <div className={`pbar mt-2.5${unknown ? ' unknown' : ''}`}>
          <i style={{ width: unknown ? '100%' : `${pct}%` }} />
        </div>
      )}

      <p className="mt-2 text-[12px] leading-relaxed text-ink-2">
        {running
          ? stage === '正在准备服务端文件'
            ? '正在下载并安装服务端启动文件（Forge / Fabric 这类要先跑一次官方安装器，几百 MB，慢是正常的）。模组会在它之后开始装。'
            : `已下载 ${fmtSize(p.bytesDone)}${p.bytesTotal > 0 ? ` / ${fmtSize(p.bytesTotal)}` : ''}。${verb}在后台进行 —— 切到别的页面也不影响：进度会一直挂在右下角，回来这里也还接着显示。`
          : op.state === 'done'
            ? `新增 ${op.result.added} · 变更 ${op.result.replaced} · 删除 ${op.result.removed} · 跳过 ${op.result.skipped}。`
            : op.state === 'canceled'
              ? `${verb}已取消，已经处理的部分按实际情况保留（不会回滚）。`
              : op.err || `${verb}过程中出错。`}
      </p>

      {/* 部分失败：state=done 也要列出来，因为 failed 非空就不算「全部成功」。
          说成「全部成功」的话，用户不会去查，问题就一直埋着。 */}
      {op.result.failed.length > 0 && (
        <div className="nbox d mt-2.5">
          <div className="hd">
            <Icon name="ban" size={14} />
            有 {op.result.failed.length} 个文件没处理好（部分失败，不是全部成功）
          </div>
          <ul className="space-y-1">
            {op.result.failed.map((f) => (
              <li key={f} className="mono-meta break-all">
                {f}
              </li>
            ))}
          </ul>
        </div>
      )}

      {/* warnings：「这条我们没接、你自己补一步」（服务端文件没能自动准备等）。
          刻意用**黄**而不是红 —— 它是可照做的提示，不是失败。混进 failed 里报红，
          用户会以为装坏了去重装，而重装也照样缺那个启动文件。 */}
      {op.result.warnings.length > 0 && (
        <div className="nbox w mt-2.5">
          <div className="hd">
            <Icon name="ban" size={14} />
            要你自己补一步
          </div>
          <ul className="space-y-1.5">
            {op.result.warnings.map((w) => (
              <li key={w}>{w}</li>
            ))}
          </ul>
        </div>
      )}

      {/* 收尾动作。取消只在进行中给 —— 已结束的作业没有可取消的东西，
          留一个灰按钮在那里只会让人怀疑它是不是还能点。 */}
      <div className="mt-3 flex flex-wrap items-center gap-2.5">
        {running && (
          <button
            type="button"
            className="btn btn-ghost sm"
            disabled={busy === 'cancel'}
            onClick={onCancel}
          >
            <Icon name="x" size={12} />
            {busy === 'cancel' ? '取消中…' : `取消${verb}`}
          </button>
        )}
        {op.state === 'done' && onOpenInstance && (
          <button type="button" className="btn sm" onClick={onOpenInstance}>
            打开这个实例
          </button>
        )}
      </div>
    </div>
  )
}

/** 阶段 → 人话。空串是终态（作业结束了，没有「正在做什么」可说）。 */
function stageLabel(stage: string, verb: string): string {
  switch (stage) {
    case 'jar':
      return '正在准备服务端文件'
    case 'files':
      return `正在${verb}模组`
    default:
      return ''
  }
}
