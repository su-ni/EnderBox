export type Status = 'stopped' | 'starting' | 'running' | 'stopping' | 'crashed'

export type Instance = {
  id: string
  name: string
  type: string
  mcVersion: string
  loaderVersion?: string
  dir: string
  javaPath?: string
  jvmArgs?: string
  port: number
  minMem: number
  maxMem: number
  jarPath: string
  autoStart: boolean
  autoRestart: boolean
  createdAt?: string
  // 以下来自 RuntimeState，后端把实例配置与运行时状态展平成一个对象返回
  status: Status
  pid: number
  startedAt?: string | null
  players?: number
  error?: string
}

export type Metrics = {
  running: boolean
  cpu: number
  memUsed: number
  memMax: number
  players: number
  uptimeSec: number
  pid: number
}

export const TYPE_LABEL: Record<string, string> = {
  vanilla: '原版',
  paper: 'Paper',
  fabric: 'Fabric',
  forge: 'Forge',
  neoforge: 'NeoForge',
  velocity: 'Velocity',
  bungeecord: 'BungeeCord',
}

export const STATUS_LABEL: Record<Status, string> = {
  stopped: '已停止',
  starting: '启动中',
  running: '运行中',
  stopping: '停止中',
  crashed: '已崩溃',
}

/** 状态语义色，全站唯一来源 */
export const STATUS_COLOR: Record<Status, string> = {
  stopped: 'var(--color-ink-3)',
  starting: 'var(--color-warn)',
  running: 'var(--color-accent)',
  stopping: 'var(--color-warn)',
  crashed: 'var(--color-danger)',
}

export function formatUptime(sec: number): string {
  if (sec <= 0) return '—'
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return `${d} 天 ${h} 时`
  if (h > 0) return `${h} 时 ${m} 分`
  if (m > 0) return `${m} 分`
  return `${sec} 秒`
}

export function formatMB(mb: number): string {
  if (mb >= 1024) return `${(mb / 1024).toFixed(1)} GB`
  return `${Math.round(mb)} MB`
}

/**
 * 该 MC 版本需要哪个 Java 主版本。镜像后端 daemon.JavaForVersion 的规则 ——
 * 后端那份是真正执行时用的，这份只负责在界面上提前告诉用户要准备什么。
 */
export function javaForVersion(mc: string): number {
  const m = mc.match(/^1\.(\d+)(?:\.(\d+))?/)
  if (!m) return 21
  const minor = Number(m[1])
  const patch = Number(m[2] ?? 0)
  if (minor <= 16) return 8
  if (minor === 20 && patch >= 5) return 21
  if (minor <= 20) return 17
  return 21
}
