import { useState } from 'react'
import { Icon } from './design'
import { STATUS_LABEL, serverLabel, type Instance } from '../types'
import { ImportDialog } from './ImportDialog'

type Props = {
  instances: Instance[]
  activeId: string
  onSelect: (id: string) => void
  onNew: () => void
  /** 去整合包市场挑一个包开新服。和「新建世界」并列 —— 都是开一台新服，只是起点不同。 */
  onMarket: () => void
  /** 后台正在下 / 正在装的任务数。0 也要显示入口 —— 用户得知道「去哪儿看」。 */
  downloads: number
  /** 打开「下载与安装」台账 */
  onDownloads: () => void
  onImported?: (id: string) => void
  /** 传了就说明当前是移动抽屉形态，多一个关闭按钮 */
  onClose?: () => void
}

/** 每个世界的方块头像给一个稳定的色相偏移（同一个世界每次进来颜色一样）。 */
function hueOf(id: string): number {
  let h = 0
  for (let i = 0; i < id.length; i++) h = (h * 31 + id.charCodeAt(i)) >>> 0
  return h % 360
}

/**
 * 侧栏世界列表 —— 结构照设计稿 `aside.side`：
 * `.side-cap`（标题 + 计数）· `.worlds`（世界行）· `.side-foot`（入口）· `.side-sum`（汇总）。
 * 样式全部来自 design.css，这里不写任何布局类。
 *
 * ⚠ 世界行用 `<div role=button>` 而不是 `<button>`：设计稿就是 div，`.world` 没有按钮
 *   重置样式（button 自带的 border/background 会跟 `.world.on` 的底色打架）。
 */
export function WorldList({
  instances,
  activeId,
  onSelect,
  onNew,
  onMarket,
  downloads,
  onDownloads,
  onImported,
  onClose,
}: Props) {
  const running = instances.filter((i) => i.status === 'running').length
  const [importOpen, setImportOpen] = useState(false)

  return (
    <aside className="side">
      <div className="side-cap">
        <span className="eyebrow">Worlds</span>
        <span className="n">{instances.length}</span>
        {onClose && (
          <button
            type="button"
            className="btn btn-ghost xs"
            style={{ marginLeft: 8 }}
            onClick={onClose}
            aria-label="关闭世界列表"
          >
            <Icon name="x" size={12} />
          </button>
        )}
      </div>

      <div className="worlds">
        {instances.length === 0 && (
          <p style={{ padding: '12px 10px', fontSize: 12.5, lineHeight: 1.7, color: 'var(--ink-3)' }}>
            还没有世界。点下面的按钮建一个，建好就能在这里一键开服。
          </p>
        )}
        {instances.map((i, n) => {
          const on = i.id === activeId
          return (
            <div
              key={i.id}
              role="button"
              tabIndex={0}
              aria-current={on}
              className={`world reveal${on ? ' on' : ''}`}
              style={{ ['--rd' as string]: `${(0.02 + n * 0.04).toFixed(2)}s` }}
              onClick={() => onSelect(i.id)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault()
                  onSelect(i.id)
                }
              }}
            >
              <svg
                className="ava"
                viewBox="0 0 16 16"
                style={{ filter: `hue-rotate(${hueOf(i.id)}deg)` }}
                aria-hidden="true"
              >
                <use href="#i-grass" />
              </svg>
              <div className="txt">
                <div className="nm">{i.name}</div>
                <div className="sb">
                  {serverLabel(i).toUpperCase()} {i.mcVersion}
                </div>
              </div>
              <span
                title={STATUS_LABEL[i.status]}
                className={`st${i.status === 'running' ? ' run' : i.status === 'crashed' ? ' crash' : ''}`}
              />
            </div>
          )
        })}
      </div>

      <div className="side-foot">
        <button
          type="button"
          onClick={onNew}
          className="btn btn-start"
          style={{ justifyContent: 'center' }}
        >
          <Icon name="plus" size={13} />
          新建世界
        </button>
        {/* 和「新建世界」并列的第二条开服入口：不想自己挑类型和版本、只想选个整合包的人走这边。 */}
        <button
          type="button"
          onClick={onMarket}
          className="btn btn-ghost"
          style={{ justifyContent: 'center' }}
        >
          <Icon name="cube" size={13} />
          从整合包市场开服
        </button>
        <button
          type="button"
          onClick={() => setImportOpen(true)}
          className="btn btn-ghost"
          style={{ justifyContent: 'center' }}
        >
          <Icon name="ul" size={13} />
          导入已有服务器
        </button>
        {/* 全局下载入口。⚠ 任务数为 0 时**入口也要在** —— 这个按钮的作用之一是
            「告诉用户去哪儿看」，只在有事时才出现的话，他永远不知道有这么个地方。 */}
        <button
          type="button"
          onClick={onDownloads}
          className="btn btn-ghost"
          style={{ justifyContent: 'center' }}
          title="下载与安装 —— 在跑的和最近跑完的都在这里"
        >
          <Icon name="dl" size={12} />
          下载与安装
          {downloads > 0 && <span className="chip s g">{downloads}</span>}
        </button>
      </div>

      <div className="side-sum">
        共 {instances.length} 个世界 · 运行中 {running} 个
      </div>

      {importOpen && (
        <ImportDialog
          onClose={() => setImportOpen(false)}
          onImported={(id) => {
            setImportOpen(false)
            onImported?.(id)
          }}
        />
      )}
    </aside>
  )
}
