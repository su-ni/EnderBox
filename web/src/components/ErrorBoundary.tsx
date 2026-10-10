import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Icon } from './design'

/**
 * 错误边界：任何一个面板抛异常，都不该让整页变成白屏。
 *
 * 三件必须做到的事：
 * ① 先说清「服务器没受影响」—— 用户看到红字时，最怕的是「我的存档是不是没了」；
 * ② 给出下一步（重试 / 重载），而不是只丢一句失败；
 * ③ 技术细节收进可折叠区：默认不吓人，真排查时要拿得到。
 */
type Props = {
  /** 标题里出现的对象名（比如世界名）。不传就用通用文案。 */
  title?: string
  children: ReactNode
  onReset?: () => void
}

type State = { err: Error | null; detail: string }

export class ErrorBoundary extends Component<Props, State> {
  state: State = { err: null, detail: '' }

  static getDerivedStateFromError(err: Error): Partial<State> {
    return { err }
  }

  componentDidCatch(err: Error, info: ErrorInfo) {
    // 必须打到控制台：headless 截图和真机排查都靠它。
    // 静默吞掉异常比直接崩溃更难查。
    console.error('[EnderBox] 界面部件渲染失败', err, info.componentStack)
    this.setState({ detail: info.componentStack ?? '' })
  }

  private reset = () => {
    this.setState({ err: null, detail: '' })
    this.props.onReset?.()
  }

  render() {
    const { err, detail } = this.state
    if (!err) return this.props.children

    const tech = (err.stack || err.message || String(err)) + (detail ? `\n\n组件栈：${detail}` : '')

    return (
      <div className="panel min-w-0" style={{ padding: 24 }}>
        <i className="corner" />
        <div className="flex items-center gap-2.5">
          <span className="text-warn">
            <Icon name="ban" size={15} />
          </span>
          <h2 className="text-[16px] leading-tight font-bold text-ink-1">
            {this.props.title ? `「${this.props.title}」这一块没能显示出来` : '这一块没能显示出来'}
          </h2>
        </div>

        <p className="mt-2.5 max-w-[68ch] text-[13px] leading-[1.7] text-ink-2">
          界面部件出错了。<b className="text-ink-1">守护进程和正在运行的服务器都没受影响</b>
          —— 这问题只发生在浏览器里，点「再试一次」通常就能恢复。
        </p>
        <p className="mt-2 max-w-[68ch] text-[12px] leading-[1.6] text-ink-3">
          想确认服务器还好不好，刷新页面看左边的世界列表就行；列表能出来就说明守护进程活着。
        </p>

        <div className="mt-4 flex flex-wrap gap-2.5">
          <button type="button" className="btn btn-primary" onClick={this.reset}>
            再试一次
          </button>
          <button type="button" className="btn btn-ghost" onClick={() => location.reload()}>
            重载页面
          </button>
        </div>

        <details className="mt-4">
          <summary className="cursor-pointer text-[12.5px] text-ink-3">技术细节</summary>
          <pre
            className="nbox d"
            style={{
              marginTop: 8,
              maxHeight: 240,
              overflow: 'auto',
              fontFamily: 'var(--f-term)',
              fontSize: 14,
              lineHeight: 1.5,
              whiteSpace: 'pre-wrap',
            }}
          >
            {tech}
          </pre>
        </details>
      </div>
    )
  }
}
