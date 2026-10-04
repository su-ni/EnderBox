import { IconBlock, IconClose, IconPlus, IconTypeBlock } from './Icons'
import { STATUS_COLOR, STATUS_LABEL, TYPE_LABEL, type Instance } from '../types'

type Props = {
  instances: Instance[]
  activeId: string
  onSelect: (id: string) => void
  onNew: () => void
  /** 传了就说明当前是移动抽屉形态，多一个关闭按钮 */
  onClose?: () => void
}

export function WorldList({ instances, activeId, onSelect, onNew, onClose }: Props) {
  const running = instances.filter((i) => i.status === 'running').length

  return (
    <aside className="flex min-h-0 min-w-0 flex-1 flex-col bg-deep p-4 md:p-[18px_14px]">
      <div className="mx-1 mb-5 flex items-center gap-2.5">
        <IconBlock size={26} />
        <div className="min-w-0 flex-1">
          <div className="text-[15px] leading-tight font-bold text-ink-1">ServerBox</div>
          <div className="mono-meta">M1 · WIN</div>
        </div>
        {onClose && (
          <button
            type="button"
            onClick={onClose}
            aria-label="关闭世界列表"
            className="btn btn-ghost btn-sm -mr-1 px-2"
          >
            <IconClose size={12} />
          </button>
        )}
      </div>

      <p className="mx-1 mb-2.5 eyebrow">世界 / WORLDS</p>

      <div className="scroll-thin min-h-0 flex-1 overflow-y-auto">
        {instances.length === 0 && (
          <p className="px-2.5 py-3 text-[12.5px] leading-relaxed text-ink-3">
            还没有世界。点下面的按钮建一个，建好就能在这里一键开服。
          </p>
        )}
        {instances.map((i) => {
          const on = i.id === activeId
          return (
            <button
              key={i.id}
              type="button"
              onClick={() => onSelect(i.id)}
              aria-current={on}
              className={`world-row${on ? ' on' : ''}`}
            >
              <span style={{ opacity: i.status === 'running' ? 1 : 0.45 }}>
                <IconTypeBlock type={i.type} size={22} />
              </span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-[13.5px] leading-tight font-semibold text-ink-1">
                  {i.name}
                </span>
                <span className="mt-0.5 block mono-meta">
                  {(TYPE_LABEL[i.type] ?? i.type).toUpperCase()} {i.mcVersion}
                </span>
              </span>
              <span
                title={STATUS_LABEL[i.status]}
                className="ml-auto block h-2 w-2 shrink-0"
                style={{ background: STATUS_COLOR[i.status] }}
              />
            </button>
          )
        })}
      </div>

      <button type="button" onClick={onNew} className="btn btn-grass mt-3.5 w-full">
        <IconPlus size={13} />
        新建世界
      </button>

      <div className="mx-1 mt-4 border-t-[3px] border-black pt-3.5 text-[12px] leading-[1.9] text-ink-3">
        共 {instances.length} 个世界 · 运行中 {running} 个
      </div>
    </aside>
  )
}
