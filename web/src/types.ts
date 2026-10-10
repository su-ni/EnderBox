export type Status = 'stopped' | 'starting' | 'running' | 'stopping' | 'crashed'

/** 自动备份策略。两种触发方式可同时用；lastRun 由后端维护，前端只读。 */
export type BackupPolicy = {
  enabled: boolean
  /** 间隔式触发；0 = 不用 */
  everyHours: number
  /** 每天固定时刻，格式 "HH:MM"；空数组 = 不用。
   *  历史上后端会把「没有」序列化成 null，所以消费前一律兜底成 []。 */
  atTimes: string[]
  /** 只保留最近 N 份（手动/自动都算）；0 = 不限 */
  keep: number
  /** all = 整个实例目录；worlds = 只存存档 */
  scope: 'all' | 'worlds'
  /** 上次自动备份开始的时间；零值表示从没跑过 */
  lastRun?: string
}

/** 策略 + 后端算好的下次时间。下次时间刻意不由前端算 —— 调度规则只能有一份实现。 */
export type BackupPolicyView = BackupPolicy & {
  nextRun?: string | null
}

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
  /** 旧实例没有这个字段，用前要兜底 */
  backup?: BackupPolicy
  /** 要不要注入面板自带的只读插件。后端是 *bool：缺省（undefined）= 开 */
  companion?: boolean
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

/**
 * 类型显示名。⚠ 这张表和**后端 `fetch.Sources()` 是两份**，加类型时两边都要动：
 * 后端那份决定「能不能自动下载」，这份决定「向导里有没有这个入口」。
 * 只加后端不加这里 = 后端能下、用户在建服向导里根本选不到（Mohist 就这样漏过一次）。
 */
export const TYPE_LABEL: Record<string, string> = {
  vanilla: '原版',
  paper: 'Paper',
  fabric: 'Fabric',
  forge: 'Forge',
  neoforge: 'NeoForge',
  // 混合端：本体是模组端，另外挂了一套 Bukkit 实现 → 模组和插件能同时装。
  // 名字要短：这张表同时被 serverLabel() 拿去当「启动文件名认不出类型时」的兜底，
  // 侧栏里出现过长的名字会把版本号挤掉。
  mohist: 'Mohist',
  arclight: 'Arclight',
  velocity: 'Velocity',
  bungeecord: 'BungeeCord',
}

/** 从启动文件名里捡不出版本名的名字 —— 这些印出去等于没印，退回类型名。 */
const UNINFORMATIVE_JAR = new Set(['minecraft', 'server', 'mc', 'java', 'launcher'])

/**
 * 侧栏/标题上那个服务端标签。
 *
 * 内部类型是**故意合并**的：Paper / Spigot / Purpur / Folia 在面板眼里都是 `paper`
 * （插件目录、EULA、启动方式完全一致，拆成四份只会多出四套一模一样的逻辑）。
 * 但**显示**不能直接印内部类型 —— 用户明明装的是 Purpur，看到「PAPER」只会以为面板认错了。
 *
 * 所以标签从启动文件名里取，而且是**通用规则、不写死名单**：写名单就得跟着上游加端，
 * 加漏一个就又是一次「面板认错了」。规则就是取文件名开头那一串字母：
 * `purpur-1.20.1-2062.jar` → Purpur、`arclight.jar` → Arclight、
 * `CatServer-4168d848-universal.jar` → CatServer（内部大写原样保留）。
 *
 * 取不出名字的两种情况都退回类型名：名字本身没意义的（`minecraft_server.jar`）、
 * 以及启动文件根本不是 jar 的（Forge 的 `win_args.txt`，那里没端名可捡）。
 */
export function serverLabel(i: Pick<Instance, 'type' | 'jarPath'>): string {
  const base = (i.jarPath ?? '').split(/[\\/]/).pop() ?? ''
  if (!/\.jar$/i.test(base)) return TYPE_LABEL[i.type] ?? i.type
  const raw = /^[A-Za-z]+/.exec(base.slice(0, -4))?.[0] ?? ''
  const low = raw.toLowerCase()
  if (!raw || UNINFORMATIVE_JAR.has(low)) return TYPE_LABEL[i.type] ?? i.type
  return TYPE_LABEL[low] ?? raw.charAt(0).toUpperCase() + raw.slice(1)
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

// ── 实时玩家数据（伴侣插件）──
//
// 这些字段和服务端里那个只读插件写出的 JSON 一一对应。
// 它能拿到坐标、背包、血量这些东西，是因为它跑在服务端进程里 ——
// 光靠读盘永远读不到。插件不监听任何事件，只在后台定时写文件。

export interface LiveItem {
  slot: number
  id: string
  count: number
  /** 物品自带的自定义名（已去掉 § 颜色码） */
  name?: string
  /** 最终显示名：自定义 > 中文 > 英文 > 裸 id */
  label: string
  lore?: string[]
  damage?: number
  maxDamage?: number
  unbreakable?: boolean
  potion?: string
  enchants?: Record<string, number>
  enchLabels?: Record<string, string>
  /** 图标地址，空 = 这个物品没图标 */
  icon?: string
}

export interface LiveEffect {
  id: string
  amplifier: number
  /** 剩余时长，单位 tick（20 tick = 1 秒） */
  duration: number
  ambient?: boolean
  label: string
  /** 罗马数字等级（I / II / III），0 级时为空 */
  amplLabel?: string
}

export interface LivePlayer {
  uuid: string
  name: string
  displayName?: string
  online: boolean
  world?: string
  dim?: string
  dimLabel?: string
  x: number
  y: number
  z: number
  yaw: number
  pitch: number
  health: number
  maxHealth: number
  food: number
  saturation: number
  level: number
  exp: number
  gamemode?: string
  op: boolean
  dead: boolean
  ping?: number
  skin?: string
  firstPlayed?: number
  lastPlayed?: number
  lastSeenAt?: number
  effects?: LiveEffect[]
  inventory?: LiveItem[]
  equipment?: Record<string, LiveItem | null>
  enderChest?: LiveItem[]
}

export interface LiveSupport {
  supported: boolean
  reason?: string
  /** 混合端（Mohist/Arclight 这类）：能用，但没人替我们兜底 */
  hybrid?: boolean
  /** Folia：长得像 Paper，但它会直接拒绝加载插件。要单独说，别混进「不支持」 */
  folia?: boolean
}

export interface LiveSnapshot {
  /** 有没有读到快照。false 时看 note，别把一堆 0 当成真实数据。 */
  have: boolean
  note?: string
  /** 服务器在跑，但快照超过 30 秒没更新 */
  stale?: boolean
  generatedAt?: string
  pluginVersion?: string
  serverVersion?: string
  onlineCount: number
  online: LivePlayer[]
  offline: LivePlayer[]
  support: LiveSupport
  installed: boolean
  running: boolean
}

export interface LiveResult {
  id: string
  op: string
  ok: boolean
  message?: string
  error?: string
  doneAt: number
}

export interface LiveAudit {
  at: string
  who: string
  instance: string
  op: string
  target: string
  args?: Record<string, unknown>
  ok: boolean
  error?: string
}
