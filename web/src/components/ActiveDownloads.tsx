import { useEffect, useRef, useState } from 'react'
import { api, type DownloadTask } from '../api'
import type { Instance } from '../types'
import { fmtSize } from './ui'

/** 只在跑的任务需要露面。做完/失败的由它自己的页面（向导、下载条）去交代。 */
function running(list: DownloadTask[]): DownloadTask[] {
  return list.filter((t) => t.state === 'running')
}

/**
 * 「有东西正在下 / 正在装」的全局提示条。
 *
 * 为什么非要单独有这么一条（原来只有向导里那个下载条）：
 * 服务端文件是**后端**在下的，任务的生命周期跟页面上哪个组件活着毫无关系 ——
 * 但进度只存在向导这个组件的 state 里，于是用户「切一下世界再回来」就再也看不到进度了。
 * 用户的原话就是：「还在安装 forge，我切换一下世界，又回来却不是安装页面了？」
 *
 * 所以这里改成从后端（唯一的事实来源）轮询任务表：
 *   · 切页面、切世界、刷新浏览器，进度都还在；
 *   · 一个实例在装的同时去看另一个实例，也不会把它甩掉；
 *   · 也顺带解决了「同时下两个」的显示问题。
 */
export function ActiveDownloads({
  instances,
  onOpen,
  onSettled,
}: {
  instances: Instance[]
  onOpen: (id: string) => void
  /** 有任务从「跑着」变成别的状态时喊一声 —— 调用方该去刷新实例列表了（jarPath 变了） */
  onSettled: () => void
}) {
  const [tasks, setTasks] = useState<DownloadTask[]>([])
  // 上一轮跑着的任务 id。用来发现「刚刚结束了哪一个」——需要跨轮次比较，
  // 放进 state 会因为依赖变化重建轮询，所以用 ref。
  const prevRunning = useRef<Set<string>>(new Set())

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined

    const tick = async () => {
      let next: DownloadTask[] = []
      try {
        const d = await api.fetchTasks()
        if (!alive) return
        next = running(d.items ?? [])
        setTasks(next)
      } catch {
        // 轮询失败不打扰用户：下个周期自己会重试。这里刻意不动 tasks，
        // 免得网络抖一下就把正在看的进度条擦掉。
      }

      const ids = new Set(next.map((t) => t.id))
      const settled = [...prevRunning.current].some((id) => !ids.has(id))
      prevRunning.current = ids
      if (settled) onSettled()

      // 有活干就快一点，闲着就慢一点 —— 闲时的轮询纯属白烧 CPU
      timer = setTimeout(tick, ids.size > 0 ? 1000 : 5000)
    }
    tick()

    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
    // onSettled 由父组件每次渲染都可能换引用，放进依赖会把轮询反复重建
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (tasks.length === 0) return null

  return (
    <div className="border-b-[3px] border-black bg-[#131b1a] px-4 py-2.5">
      <div className="flex flex-col gap-1.5">
        {tasks.map((t) => {
          const inst = instances.find((i) => i.id === t.instance)
          const installing = t.phase === 'install'
          const pct = t.total > 0 ? Math.min(100, t.percent) : 0
          const unknownTotal = t.total <= 0 || installing
          return (
            <div key={t.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5">
              <span className="shrink-0 text-[11.5px] font-bold tracking-[0.08em] text-cyan">
                {installing ? '安装中' : '下载中'}
              </span>
              <button
                type="button"
                onClick={() => onOpen(t.instance)}
                className="min-w-0 shrink truncate text-[13px] font-semibold text-ink-1 hover:underline"
                title={t.file}
              >
                {inst?.name ?? t.file}
              </button>
              <span className="mono-meta shrink-0">{t.step}</span>
              {t.resumed && <span className="chip shrink-0">续传中</span>}
              {!installing && (t.threads ?? 0) > 1 && <span className="chip shrink-0">{t.threads} 线程</span>}

              {/* 进度条放在中间，占满剩下的宽度 —— 这一条要在任何屏宽下都能一眼看出「还在动」 */}
              <div className={`pbar min-w-[80px] flex-1${unknownTotal ? ' unknown' : ''}`}>
                <i style={{ width: unknownTotal ? '100%' : `${pct}%` }} />
              </div>

              <span className="mono-meta shrink-0">
                {installing
                  ? '在装库文件'
                  : unknownTotal
                    ? `已下载 ${fmtSize(t.done)}`
                    : `${fmtSize(t.done)} / ${fmtSize(t.total)}`}
              </span>
            </div>
          )
        })}
      </div>
    </div>
  )
}
