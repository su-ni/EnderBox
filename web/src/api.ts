import type { Instance, Metrics } from './types'

export type PluginItem = {
  file: string
  name: string
  version: string
  enabled: boolean
  size: number
  modTime: string
}

export type BackupItem = {
  file: string
  kind: 'manual' | 'pre-restore'
  size: number
  time: string
}

export type JavaRuntime = {
  path: string
  version: string
  major: number
  vendor: string
  source: 'managed' | 'path' | 'system'
  arch: string
  default: boolean
}

/** 一种可自动下载的服务端类型 */
export type FetchSource = {
  type: string
  label: string
  available: boolean
  /** 不可用时说明卡在哪；可用时说明这个源的镜像状况 */
  note: string
  mirrors: { id: string; label: string }[]
}

export type GameVersion = {
  id: string
  family: string
  prerelease: boolean
  stable: boolean
}

export type FetchBuild = {
  id: number
  channel: string
  time: string
  name: string
  size: number
  /** sha256 或 sha1，用来校验下载完整性 */
  sha: string
  shaKind: string
  url: string
  /** 这个版本要求的 Java 大版本；0 表示上游没给 */
  javaMajor: number
}

/** 一次下载任务。前端按 id 轮询它。 */
export type DownloadTask = {
  id: string
  instance: string
  file: string
  step: string
  total: number
  done: number
  percent: number
  /** 字节/秒 */
  rate: number
  resumed: boolean
  state: 'running' | 'done' | 'failed'
  err?: string
  sha: string
  verified: boolean
  startedAt: string
  endedAt?: string
}

export type AuthStatus = {
  requireAuth: boolean
  trustLoopback: boolean
  isLoopback: boolean
  hasPassword: boolean
  authenticated: boolean
  sessionHours: number
}

export type TokenView = {
  id: string
  name: string
  /** 明文前 12 位，只为让人认出「这是哪一把」 */
  prefix: string
  createdAt: string
  lastUsed?: string
}

export type GlobalSettings = {
  requireAuth: boolean
  trustLoopback: boolean
  sessionHours: number
  hasPassword: boolean
  passwordSetAt?: string
  defaultMirror: string
  listenAddr: string
  tokens: TokenView[]
  runtime: {
    /** 守护进程真正绑定的地址 */
    bindingAddr: string
    /** 配置里写的和实际绑的不一致 —— 要重启才生效 */
    restartNeeded: boolean
    /** 是否暴露在非回环地址上（决定要不要显示安全告警） */
    exposed: boolean
  }
}

export type MirrorOption = { id: string; label: string; appliesTo: string[] }

export type SystemInfo = {
  version: string
  goVersion: string
  goos: string
  goarch: string
  pid: number
  startedAt: string
  uptime: string
  dataDir: string
}

/** 一条启动前检查结果。fix 非空表示后端能一键修，前端只管把 code 传回去。 */
export type PreflightIssue = {
  code: string
  level: 'block' | 'warn'
  title: string
  detail: string
  fix?: string
}

export type Preflight = {
  ok: boolean
  jarPath: string
  jarFound: boolean
  javaPath: string
  javaMajor: number
  needJava: number
  port: number
  issues: PreflightIssue[]
}

/** server.properties 的一项。kind 决定用什么控件，options 只对 enum 有意义。 */
export type PropertyRow = {
  key: string
  value: string
  kind: 'string' | 'int' | 'bool' | 'enum'
  label: string
  group: string
  hint?: string
  options?: string[]
  known: boolean
}

/** 后端在启动被体检挡住时返回的错误体（HTTP 409） */
export class PreflightBlocked extends Error {
  report: Preflight
  constructor(message: string, report: Preflight) {
    super(message)
    this.name = 'PreflightBlocked'
    this.report = report
  }
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch('/api' + path, { credentials: 'same-origin', ...init })
  if (!r.ok) {
    let msg = r.statusText
    let body: { error?: string; preflight?: Preflight; needAuth?: boolean } | null = null
    try {
      body = await r.json()
      msg = body?.error ?? msg
    } catch {
      /* 忽略解析失败 */
    }
    // 会话过期 / 需要登录：广播一下，让顶层切回登录界面。
    // 不这么做的话，用户会看到满屏「请求失败」而不知道该去登录。
    if (r.status === 401 && body?.needAuth) {
      window.dispatchEvent(new Event('sbx-need-auth'))
    }
    // 启动被体检挡住是「预期内的失败」，不是异常 —— 把报告包进专用错误类型，
    // 上层就能直接把清单渲染出来，而不是弹一句红字了事。
    if (r.status === 409 && body?.preflight) throw new PreflightBlocked(msg, body.preflight)
    throw new Error(msg)
  }
  return r.json() as Promise<T>
}

function jsonInit(method: string, body: unknown): RequestInit {
  return {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }
}

const enc = encodeURIComponent

export const api = {
  listInstances: () => req<Instance[]>('/instances'),

  createInstance: (payload: Record<string, unknown>) =>
    req<Instance>('/instances', jsonInit('POST', payload)),

  removeInstance: (id: string) => req<{ ok: boolean }>(`/instances/${id}`, { method: 'DELETE' }),

  updateInstance: (id: string, payload: Record<string, unknown>) =>
    req<Instance>(`/instances/${id}`, jsonInit('PUT', payload)),

  preflight: (id: string) => req<Preflight>(`/instances/${id}/preflight`),

  acceptEULA: (id: string) =>
    req<{ ok: boolean; preflight: Preflight }>(`/instances/${id}/eula`, { method: 'POST' }),

  /** 把体检报告里那条 issue 的 fix 标识变成一次真实操作 */
  applyFix: (id: string, code: string) =>
    req<{ ok: boolean; preflight: Preflight }>(`/instances/${id}/fix`, jsonInit('POST', { code })),

  properties: (id: string) =>
    req<{ items: PropertyRow[]; groupDesc: Record<string, string> }>(`/instances/${id}/properties`),

  // ── 服务端 jar 下载 ──
  fetchSources: () => req<{ items: FetchSource[] }>('/fetch/sources'),

  fetchVersions: (type: string, mirror = '') =>
    req<{ items: GameVersion[]; limited: boolean }>(
      `/fetch/versions?type=${enc(type)}${mirror ? `&mirror=${enc(mirror)}` : ''}`,
    ),

  fetchBuilds: (type: string, version: string, mirror = '', limit = 20) =>
    req<{ items: FetchBuild[]; limited: boolean }>(
      `/fetch/builds?type=${enc(type)}&version=${enc(version)}` +
        `${mirror ? `&mirror=${enc(mirror)}` : ''}&limit=${limit}`,
    ),

  /** 发起下载，返回任务快照（202）。之后按 task.id 轮询。 */
  downloadServerJar: (id: string, body: { type: string; version: string; build?: number; mirror?: string }) =>
    req<DownloadTask>(`/instances/${id}/server-jar`, jsonInit('POST', body)),

  fetchTask: (taskId: string) => req<DownloadTask>(`/fetch/tasks/${enc(taskId)}`),

  cancelTask: (taskId: string) =>
    req<{ ok: boolean }>(`/fetch/tasks/${enc(taskId)}/cancel`, { method: 'POST' }),

  // ── 鉴权 ──
  authStatus: () => req<AuthStatus>('/auth/status'),
  authLogin: (password: string) => req<{ ok: boolean }>('/auth/login', jsonInit('POST', { password })),
  authLogout: () => req<{ ok: boolean }>('/auth/logout', { method: 'POST' }),

  // ── 全局设置 ──
  settings: () => req<GlobalSettings>('/settings'),

  saveSettings: (patch: Partial<Pick<GlobalSettings, 'requireAuth' | 'trustLoopback' | 'sessionHours' | 'defaultMirror' | 'listenAddr'>>) =>
    req<GlobalSettings>('/settings', jsonInit('PUT', patch)),

  setPassword: (body: { old?: string; new?: string; enable?: boolean; disable?: boolean }) =>
    req<GlobalSettings>('/settings/password', jsonInit('POST', body)),

  tokens: () => req<{ items: TokenView[] }>('/settings/tokens'),

  /** 生成令牌。明文只在这个响应里出现这一次，之后拿不回来。 */
  addToken: (name: string) =>
    req<TokenView & { token: string }>('/settings/tokens', jsonInit('POST', { name })),

  removeToken: (id: string) => req<{ ok: boolean }>(`/settings/tokens/${enc(id)}`, { method: 'DELETE' }),

  mirrorOptions: () => req<{ items: MirrorOption[] }>('/settings/mirrors'),

  saveProperties: (id: string, updates: Record<string, string>) =>
    req<{ ok: boolean }>(`/instances/${id}/properties`, jsonInit('PUT', updates)),

  action: (id: string, verb: 'start' | 'stop' | 'kill') =>
    req<{ ok: boolean }>(`/instances/${id}/${verb}`, { method: 'POST' }),

  sendCommand: (id: string, cmd: string) =>
    req<{ ok: boolean }>(`/instances/${id}/command`, jsonInit('POST', { cmd })),

  metrics: (id: string) => req<Metrics>(`/instances/${id}/metrics`),

  logHistory: (id: string) => req<string[]>(`/instances/${id}/logs/history`),

  // ── 插件 / 模组 ──
  plugins: (id: string) => req<{ dir: string; items: PluginItem[] }>(`/instances/${id}/plugins`),

  pluginToggle: (id: string, file: string, enabled: boolean) =>
    req<{ ok: boolean }>(`/instances/${id}/plugins/${enc(file)}`, jsonInit('PATCH', { enabled })),

  pluginDelete: (id: string, file: string) =>
    req<{ ok: boolean }>(`/instances/${id}/plugins/${enc(file)}`, { method: 'DELETE' }),

  pluginUpload: async (id: string, f: File) => {
    const fd = new FormData()
    fd.append('file', f)
    const r = await fetch(`/api/instances/${id}/plugins`, { method: 'POST', body: fd })
    if (!r.ok) {
      const body = await r.json().catch(() => null)
      throw new Error((body as { error?: string } | null)?.error ?? r.statusText)
    }
    return r.json() as Promise<{ ok: boolean; file: string }>
  },

  // ── 备份 ──
  backups: (id: string) => req<{ items: BackupItem[] }>(`/instances/${id}/backups`),

  backupCreate: (id: string, label: string) =>
    req<BackupItem>(`/instances/${id}/backups`, jsonInit('POST', { label })),

  backupDelete: (id: string, file: string) =>
    req<{ ok: boolean }>(`/instances/${id}/backups/${enc(file)}`, { method: 'DELETE' }),

  backupRestore: (id: string, file: string) =>
    req<{ ok: boolean; safety: string }>(`/instances/${id}/backups/${enc(file)}/restore`, {
      method: 'POST',
    }),

  // ── 系统 ──
  java: () => req<{ items: JavaRuntime[] }>('/java'),

  system: () => req<SystemInfo>('/system'),
}
