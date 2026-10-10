import type { Preflight } from '../api'
import { IconHeart } from './Icons'
import { Icon } from './design'

/**
 * 每颗修复按钮的文案。
 * 「一键修复」太笼统了 —— 有的动作是改一个值，有的会下几十上百 MB。
 * 点下去之前就该知道会发生什么，所以按 code 给具体说法。
 */
export const FIX_LABEL: Record<string, string> = {
  download_jar: '自动下载服务端',
  accept_eula: '替我同意 EULA',
  rescan_java: '重新扫描 Java',
  // 这个不由后端执行：前端会跳到「插件与模组」并自动跑一次核对（见 App.tsx 的 applyFix）
  audit_client_mods: '核对并禁用客户端模组',
  sync_port: '把端口同步过去',
}

/**
 * 启动前体检。
 * 出现在两个时机：用户点「启动」被挡住时、以及实例处于停止态时主动检查出问题。
 *
 * 版式照设计稿：**心条 + 「N / 4 项通过」大数字 + 一句结论**，问题明细列在下面。
 * ⚠ 四项检查是「由后端真实字段推导」的，不是虚构的检查清单 ——
 *   后端 `Preflight` 只给 `jarFound / javaMajor / needJava / port / issues[]`，
 *   硬凑出设计稿里那个「6 项」就等于往界面上印假数据。
 * ⚠ 修复按钮仍然**一题一颗**（按 issue.code），不合并成一个笼统的「一键修复」：
 *   有的动作只改一个值、有的要下几十 MB，点之前必须知道会发生什么。
 */
export function PreflightPanel({
  report,
  fixing,
  onFix,
  onRecheck,
}: {
  report: Preflight
  fixing: string
  onFix: (code: string) => void
  onRecheck: () => void
}) {
  const checks = [
    { ok: report.jarFound, label: report.jarFound ? '服务端文件已就绪' : '服务端文件缺失' },
    {
      ok: report.javaMajor >= report.needJava,
      label:
        report.javaMajor >= report.needJava
          ? `Java ${report.javaMajor} 达标`
          : `Java ${report.javaMajor} 低于所需的 ${report.needJava}`,
    },
    {
      ok: !report.issues.some((i) => i.code === 'port_mismatch'),
      label: '端口一致',
    },
    {
      ok: !report.issues.some((i) => i.code === 'accept_eula'),
      label: 'EULA 已同意',
    },
  ]
  const passed = checks.filter((c) => c.ok).length
  const okText = checks.filter((c) => c.ok).map((c) => c.label).join('、')
  const badText = checks.filter((c) => !c.ok).map((c) => c.label).join('、')

  const blockers = report.issues.filter((i) => i.level === 'block')

  return (
    <section className="panel reveal" style={{ padding: 16 }}>
      <i className="corner" />
      <div className="flex flex-wrap items-start gap-x-6 gap-y-3">
        {/* 心条 + 大数字。红心=通过（MC 满血就是红的，别按「健康=绿」的直觉改） */}
        <div className="flex shrink-0 flex-col gap-1.5">
          <div className="hearts" role="img" aria-label={`体检通过 ${passed}/${checks.length} 项`}>
            {checks.map((c, i) => (
              <IconHeart key={i} size={16} className={c.ok ? 'heart on' : 'heart off'} />
            ))}
          </div>
          <div className="flex items-baseline gap-1">
            <b className="font-pix text-[24px] leading-none font-normal text-ink-1">{passed}</b>
            <span className="mono-meta">/ {checks.length} 项通过</span>
          </div>
        </div>

        <div className="min-w-0 flex-1">
          <h3 className="flex items-center gap-2 text-[13.5px] font-bold text-ink-1">
            启动前体检
            {blockers.length === 0 && (
              <span className="text-accent">
                <Icon name="check" size={12} />
              </span>
            )}
          </h3>
          <p className="mt-1 max-w-[92ch] text-[12.5px] leading-[1.7] text-ink-2">
            {okText && <>{okText}。</>}
            {badText && <span className="text-warn">未通过：{badText}</span>}
          </p>
        </div>

        <button
          type="button"
          className="btn btn-ghost sm shrink-0"
          onClick={onRecheck}
          disabled={Boolean(fixing)}
        >
          <Icon name="refresh" size={12} />
          重新检查
        </button>
      </div>

      {report.issues.length > 0 && (
        <ul className="mt-3.5 space-y-2.5 border-t border-line pt-3.5">
          {report.issues.map((i) => (
            <li key={i.code} className="flex flex-wrap items-start gap-x-3 gap-y-1.5">
              <span className={`badge ${i.level === 'block' ? 'ban' : 'w'} shrink-0`}>
                {i.level === 'block' ? '必须解决' : '提醒'}
              </span>
              <div className="min-w-0 flex-1">
                <p className="text-[13px] font-semibold text-ink-1">{i.title}</p>
                <p className="mt-0.5 max-w-[80ch] text-[12px] leading-[1.7] text-ink-2">
                  {i.detail}
                </p>
              </div>
              {i.fix && (
                <button
                  type="button"
                  className="btn btn-primary sm ml-auto shrink-0"
                  disabled={Boolean(fixing)}
                  onClick={() => onFix(i.fix as string)}
                >
                  {fixing === i.fix ? '处理中…' : (FIX_LABEL[i.fix] ?? '一键修复')}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
