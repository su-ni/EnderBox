import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type DownloadTask, type ModpackOpListItem } from './api'
import type { Instance } from './types'

// 「后台在跑什么」的唯一数据源。
//
// 为什么它单独一个文件、而不是塞进 DownloadCenter.tsx：
//   ① 它是 hook + 类型，不是组件。和组件放一起时，React 的 fast refresh 会因为
//      「一个文件里既有组件又有非组件导出」而放弃热更新（本仓已有 useLocalJava.ts 这个先例）；
//   ② 这些任务的归属很特殊：**它们跑在后端**，跟页面上哪个组件活着毫无关系。
//      进度只存在发起它的那个组件里的话，用户切一下世界、刷新一次页面，就再也找不回来
//      （用户原话：「还在安装 forge，我切换一下世界，又回来却不是安装页面了？」）。

/** 一条「后台在跑的东西」，把两套后端数据源（下载任务 / 整合包作业）归一成同一种形状。 */
export type FeedItem = {
  /** 前缀区分数据源：`dl:` 下载任务 / `op:` 整合包作业。取消时按前缀分流。 */
  key: string
  kind: 'download' | 'install' | 'upgrade'
  /** 给人看的名字：优先实例名，其次任务自带的 label（下载 JDK 就是这种）。 */
  label: string
  /** 归属实例。空 = 没有实例可跳（JDK 下载）。 */
  instanceId: string
  /** 现在在干嘛（「下载中」「准备服务端文件」「装模组 128/1300」）。 */
  step: string
  /** 0~100。**null = 进度未知**，别拿 0 顶上 —— 那会显示成「卡在 0%」。 */
  pct: number | null
  /** 实时速度（字节/秒）。0 = 不知道（整合包作业没有这个概念，下载 JDK 那类起步阶段也是 0）。 */
  rate: number
  running: boolean
  /** 终态说明文字（失败原因 / 完成）。运行中为空。 */
  note: string
  startedAt: string
  /**
   * 结束时刻。下载任务有（后端 endedAt）；整合包作业后端不给，空串 ——
   * 台账里显示「多久之前」时退回 startedAt，别拿空串去算时间。
   */
  endedAt: string
  bytesDone: number
  bytesTotal: number
  /** 这次用了几个连接。>1 才有意义（用户抱怨过慢，得让他看得见确实开了多线程）。 */
  threads: number
  resumed: boolean
}

/** 一条纯文字通知（原来是单条 toast 字符串）。 */
export type Toast = { id: number; text: string }

const STAGE_TEXT: Record<string, string> = {
  prepare: '解析整合包',
  jar: '准备服务端文件',
  files: '装模组',
}

function opPercent(op: ModpackOpListItem): number | null {
  const p = op.progress
  // 先按文件数：装模组阶段「已装 128 / 1300 个文件」比字节数更能说明还要多久。
  if (p?.filesTotal > 0) return Math.min(100, (p.filesDone / p.filesTotal) * 100)
  if (p?.bytesTotal > 0) return Math.min(100, (p.bytesDone / p.bytesTotal) * 100)
  return null
}

function opNote(op: ModpackOpListItem): string {
  switch (op.state) {
    case 'done':
      return op.kind === 'install' ? '安装完成' : '升级完成'
    case 'canceled':
      return '已取消'
    case 'failed':
      return op.err || '失败'
    default:
      return ''
  }
}

function taskItem(t: DownloadTask, nameOf: (id: string) => string): FeedItem {
  // Forge 那类「下完安装器还得跑一遍」的阶段没有字节进度这回事，
  // 继续画百分比会让人以为卡在某个数字上。
  const installing = t.phase === 'install'
  const known = t.total > 0 && !installing
  return {
    key: 'dl:' + t.id,
    kind: installing ? 'install' : 'download',
    // 认的是「我的世界」而不是文件名；没有归属实例的（下 JDK）才退回 label。
    label: nameOf(t.instance) || t.label || t.file,
    instanceId: t.instance ?? '',
    step: t.step,
    pct: known ? Math.max(0, Math.min(100, t.percent)) : null,
    rate: t.rate ?? 0,
    running: t.state === 'running',
    note: t.state === 'failed' ? t.err || '下载失败' : t.state === 'done' ? '下载完成' : '',
    startedAt: t.startedAt,
    endedAt: t.endedAt ?? '',
    bytesDone: t.done,
    bytesTotal: t.total,
    threads: t.threads ?? 0,
    resumed: t.resumed,
  }
}

function opItem(op: ModpackOpListItem, nameOf: (id: string) => string): FeedItem {
  const stage = STAGE_TEXT[op.stage] ?? ''
  const p = op.progress
  const detail =
    op.stage === 'files' && p?.filesTotal > 0 ? `${stage} ${p.filesDone}/${p.filesTotal}` : stage
  return {
    key: 'op:' + op.id,
    kind: op.kind === 'install' ? 'install' : 'upgrade',
    // install 的新实例在后端受理那一刻就建好了，但列表刷新有最多 3 秒延迟 ——
    // 那一小段里给个「新世界」比显示一个裸 id 强。
    label: nameOf(op.instanceId) || (op.kind === 'install' ? '新世界' : op.instanceId),
    instanceId: op.instanceId,
    step: detail || (op.state === 'running' ? '准备中' : ''),
    pct: opPercent(op),
    rate: 0,
    running: op.state === 'running',
    note: opNote(op),
    startedAt: op.startedAt,
    endedAt: '',
    bytesDone: p?.bytesDone ?? 0,
    bytesTotal: p?.bytesTotal ?? 0,
    threads: 0,
    resumed: false,
  }
}

/**
 * 轮询「后台在跑什么」。返回的 items 里**既有在跑的也有刚结束的** ——
 * 台账（记录面板）要全量，气泡只筛 running 那份。
 *
 * `settledTick` 每次观察到「有东西结束了」就 +1。调用方拿它去刷新实例列表、重跑体检
 * （服务端文件补上了、实例目录变了，页面得跟着反映）。
 */
export function useDownloadFeed(instances: Instance[]) {
  const [items, setItems] = useState<FeedItem[]>([])
  const [settledTick, setSettledTick] = useState(0)

  // 实例列表每次轮询都会换一个新数组，放进依赖会把整个轮询反复重建 —— 用 ref 读最新的一份。
  // ⚠ 赋值必须放在 effect 里：渲染期间写 ref 会被 React 编译器判成「渲染副作用」。
  const instRef = useRef(instances)
  useEffect(() => {
    instRef.current = instances
  }, [instances])

  // 上一轮在跑的那些 key，用来发现「刚刚结束了哪一个」。要跨轮次比较，所以是 ref
  //（放 state 会因依赖变化重建轮询）。
  const prevRunning = useRef<Set<string>>(new Set())
  // 见过的 key。**首次轮询时已有的那些不算「新出现」** —— 任务表会保留已完成的任务半小时，
  // 不排除掉的话，每次刷新页面都会误报一次「有任务结束了」。
  const seen = useRef<Set<string>>(new Set())
  const inited = useRef(false)

  useEffect(() => {
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined

    const tick = async () => {
      const insts = instRef.current
      const nameOf = (id: string) => insts.find((i) => i.id === id)?.name ?? ''

      let tasks: DownloadTask[] = []
      let ops: ModpackOpListItem[] = []
      let got = false
      // 两个请求并发：串行的话，慢的那个会把另一个的进度一起拖到下一拍。
      const [tr, or] = await Promise.allSettled([api.fetchTasks(), api.modpackOps()])
      if (!alive) return
      if (tr.status === 'fulfilled') {
        tasks = tr.value.items ?? []
        got = true
      }
      if (or.status === 'fulfilled') {
        ops = or.value.items ?? []
        got = true
      }

      if (!got) {
        // 两个接口都没答上来（守护进程重启 / 网络断了）：**不要清空 items** ——
        // 网抖一下就把正在看的进度擦掉，用户会以为任务丢了。
        timer = setTimeout(tick, 2500)
        return
      }

      const next = [...tasks.map((t) => taskItem(t, nameOf)), ...ops.map((o) => opItem(o, nameOf))]
      // 新的在前：刚起来的任务要出现在气泡堆/台账的最上面。
      next.sort((a, b) => (a.startedAt < b.startedAt ? 1 : -1))
      setItems(next)

      const runningKeys = new Set(next.filter((x) => x.running).map((x) => x.key))
      let settled = false
      if (!inited.current) {
        for (const x of next) seen.current.add(x.key)
        inited.current = true
      } else {
        // ① 新出现，但第一次看到时就已经结束了 —— 说明它快到来不及被观察到运行态。
        //    小服务端（Fabric 那种 180 KB 的启动器）几百毫秒就下完了，
        //    只靠 ② 会永远等不到「运行中 → 结束」这个转变，
        //    结果是「服务端文件早补上了，界面还挂着『缺少服务端文件』」。
        for (const x of next) {
          if (seen.current.has(x.key)) continue
          seen.current.add(x.key)
          if (!x.running) settled = true
        }
        // ② 之前跑着的现在不跑了
        if ([...prevRunning.current].some((k) => !runningKeys.has(k))) settled = true
      }
      prevRunning.current = runningKeys
      if (settled) setSettledTick((n) => n + 1)

      // 有活干就快一点，闲着就慢一点 —— 闲时的轮询纯属白烧 CPU。
      //
      // ⚠ 空闲这一档就是「用户刚点完某个下载，侧栏计数和气泡多久之后才出现」的上限：
      // 任务的启动是在别处触发的（向导、市场、插件页、体检修复…），我们没法给每个入口都接一个回调。
      // 定 3 秒的实测后果是——一个 28MB 的包 8 线程 4 秒就下完了，而侧栏那行到它下完都还是
      // 「后台没有在跑的任务」；定 1.5 秒既能在这个窗口里露一次面，请求量也只有「忙时档」的 1.5 倍
      //（一次 1.5 秒两个本地 JSON 请求，对家用面板不构成负担）。
      // 慢档也别再往上调长：它同时决定「下完了多久才反应过来」。
      timer = setTimeout(tick, runningKeys.size > 0 ? 1000 : 1500)
    }

    tick()
    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
  }, [])

  /** 取消。两个数据源分流 —— 拿错接口会得到一个 404，用户看到的是「取消失败」。 */
  const cancel = useCallback(async (item: FeedItem) => {
    if (item.key.startsWith('op:')) await api.modpackOpCancel(item.key.slice(3))
    else await api.cancelTask(item.key.slice(3))
  }, [])

  /** 删掉一条已结束的记录（手动清账本）。
      成功后立即本地隐藏（乐观移除）—— 只靠 1.5s 轮询自然消失太慢，
      用户会以为「还是删不掉」，再点一次（第二次撞 404，反而像坏了）。
      后端删除失败时不隐藏，错误冒到调用方。
      ⚠ 隐藏集合只增不减：任务 id 全局唯一，不会误伤后来的新任务。 */
  const [dismissed, setDismissed] = useState<ReadonlySet<string>>(() => new Set())
  const dismiss = useCallback(async (item: FeedItem) => {
    if (item.key.startsWith('op:')) await api.modpackOpDismiss(item.key.slice(3))
    else await api.dismissTask(item.key.slice(3))
    setDismissed((prev) => new Set(prev).add(item.key))
  }, [])

  return {
    items: dismissed.size ? items.filter((i) => !dismissed.has(i.key)) : items,
    settledTick,
    cancel,
    dismiss,
  }
}
