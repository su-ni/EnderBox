import type { ReactNode } from 'react'
import { CRASH } from '../dev'
import { ErrorBoundary } from './ErrorBoundary'

/**
 * 定向崩溃：只在 ?crash=1 时被渲染，用来验证错误边界真的能兜住。
 * 没有它的话，「任何面板抛异常都不白屏」这条承诺就只能靠读代码相信。
 */
function Boom(): never {
  throw new Error('这是一次用于验证错误边界的定向崩溃（?crash=1）')
}

/**
 * 页面外壳：滚动容器 + 淡入动效 + 错误边界。
 *
 * 为什么单独抽出来而不是直接在 App 里包一层：
 * ① main 上的 className 必须一字不差 —— anim-fade 不能少（滚动容器上只做 opacity
 *    动画，挂 transform 会生成 containing block，把内部 sticky 的层搞崩）；
 * ② 直接在 JSX 里加一层容器，整个页面主体都要重新缩进两级，真正的改动会被
 *    几百行空白改动淹没。抽成组件后页面主体的缩进层级完全不变。
 *
 * 页面切换时 App 会给它一个 key={page}，于是整块重挂载 —— 淡入重放，
 * 上一页遗留的错误状态也一并清掉。
 */
export function PageFrame({
  title,
  children,
}: {
  title?: string
  children: ReactNode
}) {
  return (
    <ErrorBoundary title={title}>
      <main className="anim-fade scroll-thin flex min-h-0 min-w-0 flex-col overflow-y-auto px-4 pt-4 pb-6 md:px-[26px] md:pt-5">
        {CRASH ? <Boom /> : children}
      </main>
    </ErrorBoundary>
  )
}
