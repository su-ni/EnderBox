import { Icon } from './design'

/**
 * 全局断连提示条。
 *
 * 以前这个信息只出现在概览页的空态里 —— 也就是说守护进程挂掉时，正在配置页或备份页
 * 的用户只会看到「操作失败」，不知道整体已经断了。断连是全局事实，提示也该是全局的。
 *
 * 版式照设计稿 pages/R2-abnormal-states.html 的 `.banner-danger`（图标 + 文案 + 一个动作）。
 *
 * 措辞上必须先安抚一件事：正在跑的服务器不归浏览器管，守护进程才是它的宿主。
 * 否则用户第一反应是「我的服务器是不是停了」，然后去点一堆没用的按钮。
 */
export function ConnectionBanner({ down, onRetry }: { down: boolean; onRetry: () => void }) {
  if (!down) return null

  return (
    <div role="status" aria-live="polite" className="banner-danger">
      <Icon name="ban" size={20} style={{ color: 'var(--bad)' }} />
      <div className="t">
        <h3>和守护进程断开了</h3>
        <p>
          页面里显示的读数停在断连前一刻，别再照着它下判断。每 3 秒会自动重试一次。
          <b>正在运行的服务器不受影响</b> —— 它由守护进程托管，只有守护进程整个退出才会跟着停。
        </p>
      </div>
      <button className="btn sm" type="button" onClick={onRetry}>
        <Icon name="refresh" />
        重连
      </button>
    </div>
  )
}
