import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
// 老样式表（Tailwind + 旧 token + 旧组件 CSS）。
// ⚠ 它整个在 @layer 里（components/app），层序**低于** design（见 index.css 顶部层序声明）—— 同名类以设计稿为准；
//    但 Tailwind 工具类（utilities）最高：mx-auto / mt-* / px-* 现在能覆盖两边，写了就生效。
//    保留它是因为：① 还有几个尚未搬完的旧组件（气泡/台账/回调弹窗）指着它；
//    ② 全部搬完后会把它瘦成「只剩 Tailwind base」，时机记在 docs/UI-REBUILD-TODO.md。
import './index.css'
// 设计稿样式表（docs/design/pixel-refresh/styles.css）整份引进 —— 唯一真相源，逐字节照搬。
import './design.css'
// 必须排在设计稿样式表**之后**：它是对设计稿的覆盖（演示页 → 真实面板）
import './design-overrides.css'
import App from './App.tsx'
import { ErrorBoundary } from './components/ErrorBoundary'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    {/* 最后一道防线：连 App 自己都抛异常时，也不能只留一片白屏 */}
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </StrictMode>,
)
