import { IconWarn } from './Icons'

/**
 * 全局断连提示条。
 *
 * 以前这个信息只出现在概览页的空态里 —— 也就是说守护进程挂掉时，正在配置页或备份页
 * 的用户只会看到「操作失败」，不知道整体已经断了。断连是全局事实，提示也该是全局的。
 *
 * 措辞上必须先安抚一件事：正在跑的服务器不归浏览器管，守护进程才是它的宿主。
 * 否则用户第一反应是「我的服务器是不是停了」，然后去点一堆没用的按钮。
 */
export function ConnectionBanner({ down, onRetry }: { down: boolean; onRetry: () => void }) {
  if (!down) return null

  return (
    <div
      role="status"
      aria-live="polite"
      className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b-[3px] border-danger bg-[#1c1314] px-4 py-2.5 md:px-[26px]"
    >
      <span className="flex items-center gap-2 text-[13px] leading-tight font-bold text-[#ffc2c2]">
        <span className="blip block h-2 w-2 shrink-0" style={{ background: '#e0574c' }} aria-hidden="true" />
        <span className="shrink-0 text-[#e0574c]">
          <IconWarn size={13} />
        </span>
        连不上守护进程
      </span>
      <p className="min-w-0 flex-1 text-[12.5px] leading-[1.6] text-[#e8cfcf]">
        页面上的数字可能已经过期，每 3 秒会自动重试。<b>正在运行的服务器不受影响</b>
        —— 它由守护进程托管，只有守护进程整个退出才会跟着停。
      </p>
      <button type="button" className="btn btn-sm shrink-0" onClick={onRetry}>
        立即重试
      </button>
    </div>
  )
}
