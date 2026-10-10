import { maybeDelay } from './dev'
import type {
  BackupPolicy,
  BackupPolicyView,
  Instance,
  LiveAudit,
  LiveResult,
  LiveSnapshot,
  Metrics,
} from './types'

export type ModDep = {
  id: string
  version?: string
  kind: 'required' | 'optional'
  /** 运行环境本身（MC / Java / 加载器），不是需要你去装的东西 */
  builtin?: boolean
}

/** 从 jar 里读出来的真实元数据。读不到时为 undefined，此时 name/version 是按文件名猜的。 */
export type ModMeta = {
  id: string
  name: string
  version: string
  description?: string
  authors?: string[]
  loader?: string
  source?: string
  homepage?: string
  requires?: ModDep[]
  optional?: ModDep[]
  iconPath?: string
  /**
   * 该装在哪一侧：'client'（仅客户端）/ 'server'（仅服务端）/ 空（两端都行、或读不出）。
   *
   * ⚠ 只有 Fabric / Quilt 读得出来（fabric.mod.json 的 environment）；Forge 的 mods.toml
   * 没有这个字段，那边永远是空 —— **别把它当成「两端都行」的断言**，它只是「读不出」。
   */
  side?: string
}

/** 一条依赖问题：前置缺失，或装了但被你自己禁用了 */
export type ModIssue = {
  depId: string
  depName?: string
  kind: 'required' | 'optional'
  problem: 'missing' | 'disabled'
}

export type PluginItem = {
  file: string
  name: string
  version: string
  enabled: boolean
  size: number
  modTime: string
  /** 从 jar 读到的元数据；没有表示读不出来，界面要如实标注 */
  meta?: ModMeta
  /** 读不出元数据的原因 */
  metaErr?: string
  /** jar 内有没有图标（决定要不要请求图标接口） */
  hasIcon?: boolean
  /** 依赖问题列表 */
  issues?: ModIssue[]
}

/** 文件管理里的一行 */
export type FileEntry = {
  name: string
  path: string
  isDir: boolean
  size: number
  modTime: string
  text: boolean
  zip: boolean
}

export type FileListing = {
  path: string
  parent: string
  root: string
  entries: FileEntry[]
}

/** 在线市场搜索结果 */
export type MarketHit = {
  id: string
  slug?: string
  title: string
  description: string
  author?: string
  iconUrl?: string
  downloads: number
  follows?: number
  dateCreated?: string
  dateUpdated?: string
  categories?: string[]
  /** 中文名。后端用译名表精确对上的才有，配不上就是空 —— 界面照旧显示英文名，绝不猜。 */
  zhName?: string
  projectType?: string
  /**
   * 源站给的「这一侧能不能用」：required / optional / unsupported。
   *
   * 列表里标出来是刚需：整合包的过滤只管得住包项目自己，管不到包内每个 mod ——
   * 用户就是从市场装了个「服务端可用」的包，结果里面有纯客户端模组，启动当场崩。
   */
  clientSide?: string
  serverSide?: string
}

/** 市场分类栏里的一条（名单来自后端，前端不另写一份） */
export type MarketCategory = {
  key: string
  label: string
}

/** 榜单口径。空串交给后端决定：有关键词→相关度，没关键词→下载量榜。 */
export type MarketSort = '' | 'relevance' | 'downloads' | 'newest' | 'updated' | 'follows'

/**
 * 市场线路。空串 = 自动（配置地址 → 官方 → 镜像依次试）。
 * 「仅官方 / 仅镜像」是有意不做兜底的：用户拿它来判断到底哪条线不通。
 */
export type MarketSource = '' | 'auto' | 'official' | 'mirror'

/** 一个项目的最基本信息 —— 只用来把一串 ID 变成人能认的名字（前置依赖展示） */
export type MarketProject = {
  id: string
  title: string
  iconUrl?: string
  description?: string
  /** 这个项目一共有多少个构建（含其它 MC 版本 / 加载器的）。用来告诉用户「还有多少没列出来」。 */
  versionCount?: number
}

export type MarketSearchResult = {
  items: MarketHit[]
  /** 真正答上来的源站地址 */
  base: string
  /** 走的不是首选源站（说明首选那个没通，后端自动换了路） */
  mirrored: boolean
  /**
   * 中文关键词被换成英文名后，后端把这次改写如实回传。
   * 换了词就一定要让用户看得见 —— 换错的时候他得能一眼看出来，
   * 而不是以为「搜出来这东西就是这么回事」。
   * ⚠ 字段名是 `zh`（跟后端对齐），别再起别的名字 —— 曾经写成 zhQuery，界面就永远不显示。
   */
  zh?: { from: string; matched: string; english: string }
  /**
   * 只回「服务端能装」的（整合包市场专用）。
   * 默认 true；带 all=1 搜时是 false —— 那时列表里会有 server_side=unsupported 的纯客户端包，
   * 界面必须如实说明「这个列表里有装了没用的」。模组搜索不带这个字段（模组不分服务端/客户端）。
   */
  onlyServer?: boolean
}

/** 市场里一个可下载的版本 */
export type MarketVersion = {
  id: string
  name: string
  versionNumber: string
  releaseType: string
  gameVersions?: string[]
  loaders?: string[]
  fileName: string
  fileUrl: string
  size: number
  sha1?: string
  /** 这一版被下了多少次。同一个模组几十个版本，挑的时候这是最有用的信息之一。 */
  downloads?: number
  /** 这一版发布的 ISO 时间，界面上显示成「1 年 2 个月前」。 */
  datePublished?: string
  requiredDeps?: { projectId?: string; versionId?: string; kind: string }[]
  /**
   * 这一版挂的文件里有 .mrpack 包本体。
   * 整合包的版本列表接口**只回 true 的那些**（装不上的版本在界面上就是个死按钮），
   * 所以这个字段恒为 true；留着是因为它在后端字段表里，别自己拿别的字段去推断。
   */
  hasMrpack?: boolean
}

/**
 * 单个项目的完整面貌 —— 详情页用。**只有详情页能看它的 body**，列表一律用搜索结果里的 description。
 *
 * body 是 Modrinth 正文的 **Markdown + 少量 HTML 原文**（不是清洗过的纯文本）。
 * 这是整个前端唯一一处接收富文本的地方，渲染规则见 web/src/markdown.tsx：
 * 不接受裸 HTML，白名单之外的标签连同标签一起丢。
 */
export type MarketProjectDetail = {
  id: string
  slug?: string
  title: string
  author?: string
  iconUrl?: string
  color?: number
  zhName?: string
  description: string
  body: string
  projectType?: string
  categories?: string[]
  loaders?: string[]
  gameVersions?: string[]
  /** required / optional / unsupported / unknown。装到服务端上有没有用就看它。 */
  clientSide?: string
  serverSide?: string
  downloads: number
  follows?: number
  datePublished?: string
  dateModified?: string
  license?: { id?: string; name?: string; url?: string }
  links?: {
    issues?: string
    source?: string
    wiki?: string
    discord?: string
    donation?: { label: string; url: string }[]
  }
  gallery?: { url: string; title?: string }[]
  /** 一共发过多少个版本（含别的 MC 版本 / 加载器）。详情页只会列出当前能装的那些。 */
  versionCount?: number
  /** 站上的页面地址，由后端按 projectType 拼好 —— 前端拼错了看不出来。 */
  webUrl?: string
}

/** MC 百科关系星图里的一个节点。id 是画图用的、links 引用它。 */
export type McmodNode = {
  id: string
  name: string
  /** 百科的 /class/<classId>.html，可点进详情；中心节点由后端回填。 */
  classId?: string
  /** 简介（百科给的是 HTML，后端已剥成纯文本，长了会带省略号）。 */
  content?: string
  /** 站方原始的 symbolSize，越大越核心。 */
  size?: number
  /** 分类名：前置模组 / 拓展模组 / 联动模组 / 中心。 */
  cat?: string
  /** 分类色（站方配色，直接用）。 */
  color?: string
  deep?: number
}

export type McmodLink = {
  source: string
  target: string
}

export type McmodCat = {
  name: string
  color?: string
}

/**
 * 一个模组在 MC 百科上的关系星图。
 *
 * 数据来自百科自己画图用的**公开静态 JSON**（不是我们猜网页结构），所以内容是完整的
 * 前置/拓展/联动图，而不是只有直接邻居。cached=true 说明这次是命中缓存（磁盘/内存），
 * fetchedAt 是数据的抓取时间 —— 界面上要如实标注来源与时间。
 */
export type McmodRelations = {
  center: McmodNode | null
  nodes: McmodNode[]
  links: McmodLink[]
  cats: McmodCat[]
  /** 数据来源说明。界面必须显示 —— 这是第三方站点的数据，不是稳定接口。 */
  source: string
  /** 本次是否命中缓存。 */
  cached: boolean
  /** 数据抓取时间（RFC3339）。 */
  fetchedAt?: string
  /** 后端给的提示（比如「百科没登记关系」），有就显示。 */
  note?: string
}

export type BackupItem = {
  file: string
  /** auto = 定时任务产生的；pre-restore = 还原前自动存的安全网 */
  kind: 'manual' | 'auto' | 'pre-restore'
  size: number
  time: string
}

// ── 计划任务 ──

/** 触发方式。四个字段按 kind 各用其一，其余留空。 */
export type TaskTrigger = {
  kind: 'interval' | 'daily' | 'weekly' | 'cron'
  /** interval：每隔多少分钟 */
  everyMinutes?: number
  /** daily / weekly：触发时刻 "HH:MM" */
  at?: string
  /** weekly：0=周日 … 6=周六 */
  weekdays?: number[]
  /** cron：五段式「分 时 日 月 周」 */
  cron?: string
}

export type TaskActionKind = 'command' | 'start' | 'stop' | 'restart' | 'kill' | 'backup' | 'delay'

/** 一个动作。Actions 是有顺序的流水线，前一步做完才做下一步。 */
export type TaskAction = {
  kind: TaskActionKind
  /** command=要发的指令；delay=等待秒数；其余动作留空 */
  value?: string
}

export type ScheduledTask = {
  id: string
  name: string
  enabled: boolean
  trigger: TaskTrigger
  actions: TaskAction[]
  /** 这段启用期的起点（从关到开时会重置） */
  since: string
  /** 上次执行时刻 —— 不论成败。没跑过时是 Go 的零值时间，别当真实时间显示 */
  lastRun: string
  /** 累计执行次数 */
  count: number
  /** 上次失败原因；空 = 上次是成功的 */
  lastError?: string
}

/** 任务视图 = 任务本身 + 后端算好的下次执行时间（前端不重算调度规则） */
export type TaskView = ScheduledTask & {
  nextRun?: string
  running?: boolean
}

/** 提交给后端的新建/更新载荷。服务端状态（since / lastRun / count）不由前端决定。 */
export type TaskPayload = {
  name: string
  enabled: boolean
  trigger: TaskTrigger
  actions: TaskAction[]
}

// ── 玩家管理 ──
//
// 字段名照抄服务端写的 whitelist.json / ops.json / banned-*.json，
// 这样界面显示的就是服务端真实存着的东西，中间没有一层「我们自己起的名字」。

export type PlayerEntry = {
  uuid: string
  name: string
}

export type OpEntry = {
  uuid: string
  name: string
  /** 1-4，4 = 全部权限 */
  level: number
  bypassesPlayerLimit: boolean
}

export type BanEntry = {
  uuid: string
  name: string
  created: string
  source: string
  /** "forever" 或者一个时间串 */
  expires: string
  reason: string
}

export type IPBanEntry = {
  ip: string
  created: string
  source: string
  expires: string
  reason: string
}

/** 一次取全的玩家名单。 */
/**
 * 名册每一行的补充信息。三个字段都可能缺 —— **缺了就是缺了，别当 0 用**：
 * playTicks 为 0 表示「没有档案」，不是「他玩了 0 秒」，界面该显示「—」。
 */
export type PlayerExtra = {
  /** 累计游玩**刻**数（20 tick = 1 秒）。换算是展示层的事 */
  playTicks?: number
  /** playerdata 的写入时间（RFC3339）。空 = 没有档案 */
  lastSeen?: string
  /** 延迟毫秒。只有 companion 插件在服运行时才给得出，原版服务端不暴露玩家级 ping */
  ping?: number
}

export type PlayerRoster = {
  /** 在线玩家。uuid 可能为空 —— 正版服的 UUID 算不出来，界面按「没记下」显示，不编一个 */
  online: PlayerEntry[]
  whitelist: PlayerEntry[]
  ops: OpEntry[]
  banned: BanEntry[]
  bannedIps: IPBanEntry[]
  /** server.properties 里的 white-list。没开的时候往白名单里加人不会生效 */
  whitelistEnabled: boolean
  /** server.properties 里的 online-mode。false = 离线服，此时 uuid 是按名字算出来的 */
  onlineMode: boolean
  /** 服务端在不在跑。决定操作走命令轨还是改文件轨，也决定踢人能不能点 */
  running: boolean
  /**
   * 每行的补充信息（累计时长 / 最后在线 / 延迟），键是**小写玩家名**。
   * 时长来自世界目录里的 stats 文件（离线也读得到），延迟来自 companion 插件。
   * 条目是空对象 = 这个人没有档案，不是「玩了 0 秒」。
   */
  extra: Record<string, PlayerExtra>
}

/** 一个玩家的只读档案：服务端在**世界目录**里留下的痕迹，不是名单状态。
 *  读的是 <世界>/stats/<uuid>.json 和 <世界>/playerdata/<uuid>.dat 的 mtime。 */
export type PlayerProfile = {
  name: string
  uuid: string
  /** 服务端写过统计文件。false 时下面几个数字一律无意义，别显示成 0 */
  hasStats: boolean
  /** 总游玩时间，单位 **tick**（20 tick = 1 秒）。换算成小时是展示层的事 */
  playTicks: number
  deaths: number
  playerKills: number
  mobKills: number
  /** 最后进服时间（RFC3339）。空 = 没有 playerdata，也就是他没进过服或世界没存过档 */
  lastSeen: string
  /** 拿不到数据的原因。空 = 一切正常，不用解释 */
  note: string
  /** 实际用的世界目录名（level-name 可能被改过） */
  worldName: string
}

/**
 * 游戏规则的一条。名字/分组/说明的拼法在后端（daemon/gamerules.go）——
 * 前端只负责把 kind 映射成控件、把 group 映射成导航。
 */
export type GameruleRow = {
  /** 机器名（keepInventory 这种）。设置时回传的就是它 */
  name: string
  /** 显示名。后端优先给中文；中文表没收录的新规则会是英文显示名或机器名 */
  label: string
  /** 说明。只有「看名字猜不出来」的规则才有 —— 空就别占地方 */
  note?: string
  /** 分组中文名。分组顺序在 groups 里，别在前端硬编码 */
  group: string
  /** 当前值。bool 是 "true"/"false"，int 是十进制字符串 */
  value: string
  kind: 'bool' | 'int'
}

export type GameruleList = {
  rules: GameruleRow[]
  /** 本次真的读到规则的组，按显示顺序。空组不出现 */
  groups: string[]
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

/** 一个可下载的 JDK 主版本（静态清单，不触发外部请求）。 */
export type JavaMajor = {
  major: number
  lts: boolean
  /** 这个 Java 适合跑哪些 MC 版本 —— 按 MC 版本说人话，而不是列 JDK 年份 */
  forMc: string
}

/** 某个主版本目前能下的那一份（版本 / 大小 / 校验和都来自源站实况）。 */
export type JDKRelease = {
  major: number
  version: string
  name: string
  /** 0 = 没问到大小，界面按「大小未知」显示，不要编数字 */
  size: number
  /** 空 = 这个镜像没提供校验和 */
  sha: string
  url: string
  mirror: string
  mirrorLabel: string
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

/** 「转换为混合端」的一个候选目标。真相源在后端 daemon/convert.go，前端别另写名单。 */
export type ConvertTarget = {
  type: string
  label: string
  available: boolean
  /** 转换后这个实例的启动文件会叫什么（相对实例目录）—— 旧的那个会留着 */
  newJar: string
  /** 转过去是什么、代价是什么。不可用时这里放的是「这个实例为什么现在不行」 */
  note: string
}

/** 从当前类型出发、我们**故意不做**的转换。空着不给理由，用户会以为面板没这功能。 */
export type ConvertRefusal = {
  label: string
  reason: string
}

export type ConvertInfo = {
  current: string
  currentLabel: string
  mcVersion: string
  jarPath: string
  running: boolean
  targets: ConvertTarget[]
  refusals: ConvertRefusal[]
  /** 转换前必须看到的代价（会复用 libraries/、模组冲突风险、先备份） */
  warning: string
}

/** 一个可选的游戏版本。后端只返回正式版（快照/预发布在源侧就滤掉了），
 *  所以这里没有「是否稳定」的字段 —— 别再加一个恒为 true 的字段假装有判据。 */
export type GameVersion = {
  id: string
  /** Paper 的版本族（1.21 这种），原版为空 */
  family: string
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
  /** 构建行的显示名（Fabric 用它显示 Loader 版本）；留空则按 #编号/官方 显示 */
  label?: string
  /**
   * 上游对这个构建的稳定标识，下载时按它回源站定位。
   * Forge 必须用它 —— 版本号不是整数（49.0.30、14.23.5.2860），塞不进 id，
   * 而按 id 定位会在上游加了新构建之后悄悄下成另一个版本。
   */
  tag?: string
  /** javaMajor 是哪来的：mojang=官方元数据、versionline=按版本线推断 */
  javaFrom?: string
}

/** 一次下载任务。前端按 id 轮询它。 */
export type DownloadTask = {
  id: string
  instance: string
  file: string
  step: string
  /** 没有归属实例时（下载 JDK 就是这种）显示的名字；有实例时前端优先显示实例名 */
  label?: string
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
  /**
   * 当前处于哪一段：download=在下文件，install=下完之后的安装步骤。
   * 只有 Forge 会走到 install（它下的是安装器，还得真跑一遍）——
   * 这一段没有「下了多少字节」这回事，界面要继续画字节数会让人以为卡死了。
   */
  phase?: string
  /** 这次用了几个连接并行下载（0/1 = 单连接）。大文件走多线程时会 >1。 */
  threads?: number
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
  /** 在线模组市场的地址（默认 Modrinth 官方 API；国内可换成镜像 / 代理） */
  marketBase: string
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

/** 一条崩溃判读结论：现象 / 原因 / 怎么办，外加命中的原始行当证据。 */
export type CrashDiagnosis = {
  code: string
  /** fatal = 这次崩溃的直接原因；warn = 可能有关系 */
  severity: 'fatal' | 'warn'
  title: string
  cause: string
  action: string
  /** 命中的原始日志行，给人核对用；可能为空 */
  evidence?: string
  /** 复用 preflight 的 fix 标识；空表示只能手动处理 */
  fix?: string
}

/** 一份崩溃现场（磁盘上的 <id>-crash-<时间>.log）。 */
export type CrashReport = {
  file: string
  time: string
  exitInfo?: string
  lineCount: number
  diagnoses: CrashDiagnosis[]
}

/** 导入已有服务器：对一个目录的识别结果（只读、无副作用）。 */
export type ImportScan = {
  dir: string
  name: string
  type: string
  mcVersion: string
  loaderVersion?: string
  jarPath: string
  port: number
  eula: boolean
  hasWorld: boolean
  /** 目录里其它可作启动文件的东西，换一个用 */
  candidates: string[]
  /** 识别过程中的说明与不确定项 —— 一条都没有说明很确定 */
  notes: string[]
}

export type DirEntry = {
  name: string
  path: string
  /** 像服务端根目录的先标出来，帮用户在盘里快速找到目标 */
  looksLikeServer: boolean
}

export type DirListing = {
  /** 空串 = 正在列盘符（Windows） */
  path: string
  parent: string
  dirs: DirEntry[]
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
  /** 枚举值 → 中文名。只影响显示，写进文件的仍是英文值 */
  labels?: Record<string, string>
  /** int 的取值范围（后端权威，前端别自己另写一套） */
  min?: number
  max?: number
  /** 官方默认值 */
  default?: string
  /** 运行中能否走控制台指令立即生效。只有 difficulty/gamemode/pvp/white-list 为真 */
  live?: boolean
  /** 密码类字段，界面遮一下 */
  secret?: boolean
  /** 这个键在文件里真的存在吗。false = 显示的是官方默认值，不是当前配置 */
  inFile: boolean
  known: boolean
}

/** 一条可编辑的配置文件 */
export type ConfigFile = {
  path: string
  name: string
  size: number
  modTime: string
  lang: 'yaml' | 'toml' | 'json' | 'properties' | 'text'
  depth: number
}

/** 被排除的文件 + 理由。静默不列会让人以为文件丢了 */
export type ConfigExcluded = { path: string; reason: string }

export type ConfigIssue = {
  line: number
  col?: number
  /** 行号只是「附近」。YAML 库给的位置天生不精确，界面不能拍胸脯保证 */
  approx?: boolean
  message: string
}

export type ConfigContent = ConfigFile & {
  text: string
  textual: boolean
  eol: 'lf' | 'crlf'
  /** 打不开的原因（太大 / 不是文本）。有值时 text 为空 */
  problem?: string
  issues: ConfigIssue[]
  hasBackup: boolean
  backup?: string
}

/**
 * 保存配置时被后端的结构校验挡住（HTTP 422）。
 *
 * 单独一个错误类型而不是丢一句 Error：issues 里带行号，界面要能把出错行标出来。
 * 手改 YAML 少一个缩进，服务端是启动时才炸的，报错还埋在几十万行日志里 ——
 * 当场指出是哪一行，省的是用户一整晚。
 */
export class ConfigRejected extends Error {
  issues: ConfigIssue[]
  constructor(message: string, issues: ConfigIssue[]) {
    super(message)
    this.name = 'ConfigRejected'
    this.issues = issues
  }
}

/** 后端在启动被体检挡住时返回的错误体（HTTP 409） */
export class PreflightBlocked extends Error {  report: Preflight
  constructor(message: string, report: Preflight) {
    super(message)
    this.name = 'PreflightBlocked'
    this.report = report
  }
}

// ── 整合包升级（接口契约见 docs/MODPACK.md 第八节 8.2）──
//
// 字段名逐字对齐契约。集合字段后端一律保证是 [] 而不是 null —— null.length 会当场炸。
// have 永远存在：它是「这个实例是不是整合包装的」的唯一判据，别用别的字段去推断。

/** 加载器版本表：{ "forge": "47.4.10" } 这种 key→版本 的映射，不写死加载器种类。 */
export type ModpackLoader = Record<string, string>

/** 实例的整合包身份（GET /instances/{id}/modpack）。 */
export type ModpackInfo = {
  /** 永远存在。false = 这个实例不是整合包装的，没有升级这回事。 */
  have: boolean
  /** have=false 时的说明（例如「这个实例不是由整合包安装的」）。 */
  note?: string
  // 以下字段只在 have=true 时有意义；后端对 have=false 不保证给，所以全是可选
  format?: string
  slug?: string
  projectId?: string
  versionId?: string
  versionNumber?: string
  name?: string
  minecraft?: string
  loader?: ModpackLoader
  /** RFC3339，零值不要当真实时间显示。 */
  installedAt?: string
  /** 上次升级时间；空串 = 从没升过。 */
  updatedAt?: string
  fileCount?: number
  overrideCount?: number
}

/** 升级计划里的一条文件变更。reason 只在「要删」和「跳过」时有意义。 */
export type ModpackFileChange = {
  path: string
  action: 'add' | 'replace' | 'remove' | 'skip'
  /** remove / skip 才有：为什么要删、为什么没动。界面必须原样摊给用户看。 */
  reason?: string
}

/** 升级目标版本信息（计划里的 target）。 */
export type ModpackTarget = {
  versionId: string
  versionNumber: string
  minecraft: string
  loader: ModpackLoader
}

/**
 * 只算不装的结果（GET /instances/{id}/modpack/upgrade?version=）。
 *
 * 为什么单独一条「算」的接口：升级会删文件，是破坏性操作，必须先把「要删哪些」摊给用户看，
 * 再决定要不要真升。这条返回的 refusal / levelJump / running / 四个数组就是给用户做决定的全部依据。
 */
export type ModpackUpgradePlan = {
  ok: boolean
  /** 非空 = 不能升（目标不再是服务端包等）。这时界面只显示原因、禁用开始按钮。 */
  refusal: string
  summary: { add: number; replace: number; remove: number; keep: number; skip: number }
  /** 预计下载量（字节）。0 也可能是合理的（纯删 / 只覆盖本地文件）。 */
  downloadBytes: number
  /** 加载器或游戏版本发生了跳跃（换了一台服）。界面要在按钮之前用醒目框警告并建议备份。 */
  levelJump: boolean
  /** levelJump 时的说明，里面已经写了「先做一次备份」。原样显示，别改写。 */
  levelJumpNote: string
  /** 实例正在运行 —— 升级要先停服，否则 Windows 上锁文件写不进去。 */
  running: boolean
  toAdd: ModpackFileChange[]
  toReplace: ModpackFileChange[]
  toRemove: ModpackFileChange[]
  skipped: ModpackFileChange[]
  target: ModpackTarget
  /**
   * 这份计划的指纹（服务端算出来的）。用户点「开始升级」时原样带回 POST，
   * 服务端用它判断「你预览时看到的那份」和「现在要执行的那份」是不是同一份 ——
   * 防止竞态（预览之后包被更新了 / 文件被谁动了），不能让用户批准 A、系统执行 B。
   * 后端对空 planHash 直接 400（「先预览再开始」）—— 不存在「没指纹照常」的兜底；
   * 前端始终原样回传预览响应里服务端给的这份哈希。
   */
  planHash: string
}

/** 真升级的返回（POST .../upgrade → 202）。 */
export type ModpackOpStart = { opId: string }

/** 一次升级任务的轮询结果（GET /modpacks/ops/{opId}）。 */
export type ModpackOpProgress = {
  filesDone: number
  filesTotal: number
  bytesDone: number
  bytesTotal: number
  /** 当前正在处理的文件（相对实例目录）。 */
  current: string
}

/** 升级终态的结算。部分失败不算成功：failed 非空就要如实列出来，不能说「全部成功」。 */
export type ModpackOpResult = {
  added: number
  replaced: number
  removed: number
  skipped: number
  /** 跳过的路径清单（与 skipped 计数对应；详情在计划的 skipped 列表里）。 */
  skippedPaths: string[]
  /** 失败的路径。非空 = 部分失败，界面必须列出来。 */
  failed: string[]
  /**
   * 「这一步我们没接、你自己来」类的话（服务端文件没能自动准备等）。恒为 []（不是 null）。
   * ⚠ 与 failed 不是一回事：failed 是「该做的没做成」（红，要去查），
   * warnings 是「这条我们没接」（黄，照它说的补一步就好）。
   * 混在一起会让「装好了但少个启动文件」被读成「安装失败」，用户就去重装了 —— 而重装也没用。
   */
  warnings: string[]
}

/**
 * 统一作业快照（GET /api/jobs/{id}）。
 *
 * 所有「分钟级慢操作」——备份创建/还原、整合包导出、模组升级/安装——共用这一份结构，
 * 后端也只用一张作业表（`internal/jobs`）。「每一个慢操作一套私有作业 + 一套轮询」的做法
 * 已经废掉：那种写法改一处漏两处，进度条常常只有最新加的那个功能是准的。
 *
 * ⚠ startedAt 是毫秒时间戳（不是 RFC3339 字符串）：生活了 24 小时的作业会被清理，
 * 「跑了多久」要能算，数字比字符串省一parse，也更难搞错时区。
 */
export type JobSnapshot = {
  id: string
  /** 归属实例 id（作业表里的 owner）。 */
  owner: string
  /** backup-create / backup-restore / modpack-export / upgrade / install …… */
  kind: string
  state: 'running' | 'done' | 'failed' | 'canceled'
  /** 现在在干嘛；终态必定为空串（Finish 会清掉，避免报错了还写着「正在装模组」）。 */
  stage: string
  progress: {
    filesDone: number
    filesTotal: number
    bytesDone: number
    bytesTotal: number
    current: string
  }
  /** 各 kind 自己填的结算；running 期间也有可能已经有值（取消会先落终态）。 */
  result: Record<string, unknown>
  err: string
  startedAt: number
}

/** 把统一快照摆成旧整合包作业的形状，让那些还没改造的组件继续能读。 */
function jobToModpackOp(j: JobSnapshot): ModpackOp & { id: string; startedAt: string } {
  return {
    id: j.id,
    startedAt: new Date(j.startedAt).toISOString(),
    state: j.state,
    kind: (j.kind === 'install' ? 'install' : 'upgrade') as 'install' | 'upgrade',
    instanceId: j.owner,
    stage: j.stage,
    progress: j.progress,
    result: (j.result ?? {}) as unknown as ModpackOpResult,
    err: j.err,
  }
}

/** 整合包作业（统一作业表 /api/jobs 上，四处入口共用）。 */
export type ModpackOp = {
  state: 'running' | 'done' | 'failed' | 'canceled'
  /**
   * 这笔作业是「从市场装成新实例」（install）还是「升已有的」（upgrade）。
   * 服务端对老作业缺省报 upgrade —— 所以这里恒有值，别当可选。
   * 界面靠它决定动词（安装 / 升级）和「装完跳去哪」。
   */
  kind: 'install' | 'upgrade'
  /** 作业归属的实例 id。install 是新实例，upgrade 是当前这个。 */
  instanceId: string
  /** 当前阶段：jar = 在准备服务端文件，files = 在装模组，'' = 已结束。 */
  stage: string
  progress: ModpackOpProgress
  result: ModpackOpResult
  err: string
}

/**
 * 作业列表里的一条 = 单条快照 + 两个只有「列表」才需要带上的字段。
 * 单条接口按 opId 查，所以不需要 id；列表要靠它定位、靠 startedAt 排序与显示「跑了多久」。
 */
export type ModpackOpListItem = ModpackOp & {
  id: string
  startedAt: string
}

// ── 整合包市场（P1，接口契约见 docs/MODPACK.md 第九节）──
//
// 与上面「升级」那条线的根本区别：这里的 install **永远新建一个实例**，不碰任何已有实例。
// 「装进已有实例」是升级页（?version=）的事 —— 两件事的入口在界面上也该分开，
// 否则用户在实例 A 的详情页里点「从市场安装」，会以为装的是 A，结果多了个 B。

/**
 * 预览结果（POST /modpacks/inspect）。
 *
 * 这一步会**真的把包下下来**（几十 MB，fastly 那条线实测 ~60KB/s），算一遍计划再把包留着 ——
 * 所以它回一个取件码，安装时凭码取用，不用重下第二遍。
 * ok=false 时 token 是空串：被拒的包不留取件码，也不该留几十 MB 的临时文件在磁盘上。
 */
export type ModpackInspect = {
  /** 取件码。传给 install 用，二十分钟内有效、且**只能用一次**。ok=false 时是空串。 */
  token: string
  /** false = 装不了。这时只看 refusal，其余字段不要拿来渲染。 */
  ok: boolean
  /** 非空 = 装不了以及为什么。后端已经给了三条能走通的路，原样显示，别改写。 */
  refusal: string
  name: string
  slug: string
  projectId: string
  versionId: string
  versionNumber: string
  minecraft: string
  /** 包里声明的加载器键（forge / neoforge / fabric-loader / quilt-loader…）。 */
  loaderKind: string
  loaderVersion: string
  /** 面板的服务端类型。**认不出的加载器这里是空串，且 ok=false** —— 空串永远伴随拒绝。 */
  serverType: string
  /** 会装进去的文件数（server_side 是 required / optional 的，不含已跳过的）。 */
  install: number
  /** 明确标了 unsupported、会被跳过的客户端文件数。要在预览里如实说，不能让用户以为全装了。 */
  skipped: number
  /**
   * 「.mrpack 说该装、但 Modrinth 说服务端用不了」的文件 —— 作者标错了，或者压根没标。
   *
   * 这类会被**真装进去**，然后服务端启动崩（真机案例：ETF 装进 Forge 服务端）。
   * ⚠ 核对不上（离线 / 文件来自非 Modrinth 源）时是空数组 —— **空不等于没问题**，
   * 只等于「这次没核出来」，界面措辞不能反过来说「已确认没问题」。
   */
  suspectClient?: { file: string; title?: string }[]
  overrideCount: number
  /** 包本体大小（从源站拿的 Content-Length）。 */
  packBytes: number
  /** 模组文件的总下载量（不含包本体）。 */
  downloadBytes: number
  /** 服务端启动文件能不能由面板自动准备。forge / fabric / vanilla / paper 能；neoforge 不能。 */
  canAutoJar: boolean
  /** canAutoJar=false 时的说明，里面写了「你自己要做什么」。放在安装按钮**上方**，别藏在下面。 */
  jarNote: string
}

/** 安装请求。name / port / javaPath 都可选：不给就由服务端定（包名 / 自动挑端口 / 全局 Java）。 */
export type ModpackInstallReq = {
  token: string
  name?: string
  port?: number
  javaPath?: string
}

/**
 * 安装已受理（202）。**实例这时已经建好了**，作业在后台跑。
 *
 * ⚠ EULA 不在这条链路里：装完拿到 instanceId 之后，前端再单独调 acceptEULA。
 * 让安装顺手替你同意 EULA 是替用户按了法律条款 —— 该他自己点。
 */
export type ModpackInstallStart = { instanceId: string; opId: string }

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  // 只有 ?slow= 时才等待。默认 0，正常路径上是个已 resolve 的 Promise。
  await maybeDelay(path)
  const r = await fetch('/api' + path, { credentials: 'same-origin', ...init })
  if (!r.ok) {
    let msg = r.statusText
    let body: {
      error?: string
      /** 后端给的机器可读错误码（如游戏规则的 "no-server"）。界面按码分流，不匹配文案 */
      reason?: string
      preflight?: Preflight
      needAuth?: boolean
      issues?: ConfigIssue[]
    } | null = null
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
    // 配置结构校验没过（422）：把带行号的清单带上去，界面要能标出来并跳过去 ——
    // 只丢一句「保存失败」的话，用户得自己在那份文件里逐行找哪少了缩进
    if (r.status === 422 && body?.issues) throw new ConfigRejected(msg, body.issues)
    // 普通失败也把状态码与 reason 挂上去：有些页要按它们分流（例如游戏规则在
    // 停服时是 409 / no-server，页面该切「服务器起来后才读得到」的说明，
    // 而不是甩一句报错）。HTTP 状态挂在 Error 上比让各页自己解析文案稳。
    const err = new Error(msg) as Error & { status?: number; reason?: string }
    err.status = r.status
    err.reason = body?.reason
    throw err
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

/** multipart 上传。错误处理和 req 保持一致，省得每个上传各写一遍解析逻辑。 */
async function postForm<T>(path: string, fd: FormData): Promise<T> {
  const r = await fetch('/api' + path, { method: 'POST', credentials: 'same-origin', body: fd })
  if (!r.ok) {
    const body = await r.json().catch(() => null)
    throw new Error((body as { error?: string } | null)?.error ?? r.statusText)
  }
  return r.json() as Promise<T>
}

const enc = encodeURIComponent

/** 制作整合包的入参。空值会被 queryOf 丢掉（不发给后端）。 */
export type ExportQuery = {
  /** 包名，缺省取实例名 */
  name?: string
  /** 版本号，缺省用当天日期（如 2026.10.09） */
  version?: string
  /** 加载器版本号。实例里只存「哪种加载器」，存不了版本，所以要外面给 */
  loaderVer?: string
  /** '0' = 不要客户端包 */
  client?: string
  /** '0' = 不要服务端包 */
  server?: string
  /** 额外要收的目录，逗号分隔 */
  extra?: string
}

/** 一个包的产出。字段对齐后端 internal/modpack.PackResult。 */
export type ModpackExportOne = {
  /** .mrpack 绝对路径。预览时为 ''（没落盘） */
  Path: string
  /** 写进清单的文件数（走 CDN，不占包体） */
  IndexEntries: number
  /** 走了 overrides 的文件数（＝反查不到的本地文件） */
  OverrideFiles: number
  /** 反查不到、只能打包带走的相对路径 */
  LocalFiles: string[]
  /** 因 environment 被剔掉的文件（如客户端包剔掉 server_only 的模组） */
  Skipped: string[]
  /** 产物字节数。预览时为 0 */
  Bytes: number
  /** overrides 那批文件的原始大小之和 —— **预览要看它**（包大概会多大） */
  OverrideBytes: number
  DryRun: boolean
}

export type ModpackExportResult = {
  Client?: ModpackExportOne
  Server?: ModpackExportOne
}

/** 把可选查询参数拼成 ?a=1&b=2。空值一律丢掉，不发给后端。 */
function queryOf(q?: ExportQuery): string {
  if (!q) return ''
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(q)) {
    if (v != null && v !== '') sp.set(k, v)
  }
  const s = sp.toString()
  return s ? '?' + s : ''
}

export const api = {
  listInstances: () => req<Instance[]>('/instances'),

  createInstance: (payload: Record<string, unknown>) =>
    req<Instance>('/instances', jsonInit('POST', payload)),

  // withFiles 才连磁盘目录一起删。默认只摘配置 —— 存档不该因为少传一个参数就没了。
  removeInstance: (id: string, withFiles = false) =>
    req<{ ok: boolean }>(`/instances/${id}${withFiles ? '?files=1' : ''}`, { method: 'DELETE' }),

  // ── 下载任务 ──
  // instance 传实例 id 时只返回它的任务 —— 切页面回来要接着看进度，得先找得到任务。
  fetchTasks: (instance?: string) =>
    req<{ items: DownloadTask[] }>(`/fetch/tasks${instance ? `?instance=${enc(instance)}` : ''}`),

  updateInstance: (id: string, payload: Record<string, unknown>) =>
    req<Instance>(`/instances/${id}`, jsonInit('PUT', payload)),

  preflight: (id: string) => req<Preflight>(`/instances/${id}/preflight`),

  acceptEULA: (id: string) =>
    req<{ ok: boolean; preflight: Preflight }>(`/instances/${id}/eula`, { method: 'POST' }),

  /** 把体检报告里那条 issue 的 fix 标识变成一次真实操作。
   *  有的修复是后台任务（比如「补齐服务端文件」要下载），这时会带回 task，界面去看进度。 */
  applyFix: (id: string, code: string) =>
    req<{ ok: boolean; preflight: Preflight; task?: DownloadTask }>(
      `/instances/${id}/fix`,
      jsonInit('POST', { code }),
    ),

  /** 崩溃现场判读。最新的一份在最前，每份都带「现象/原因/怎么办」。 */
  crashReports: (id: string, limit = 5) =>
    req<{ items: CrashReport[] }>(`/instances/${id}/crash-reports?limit=${limit}`),

  /** 读回某一份崩溃现场的原文，给「查看原始日志」用。 */
  crashReportText: (id: string, file: string) =>
    req<{ file: string; text: string }>(`/instances/${id}/crash-reports/${enc(file)}`),

  properties: (id: string) =>
    req<{ items: PropertyRow[]; groupDesc: Record<string, string> }>(`/instances/${id}/properties`),

  /** 运行中改配置、不重启就生效。只有后端认得的那四个键支持，别的会 400 */
  applyProperties: (id: string, updates: Record<string, string>) =>
    req<{ ok: boolean; commands: string[] }>(
      `/instances/${id}/properties/apply`,
      jsonInit('POST', updates),
    ),

  // ── 通用配置文件编辑器（bukkit.yml / paper-global.yml / 插件 config…）──
  configs: (id: string) =>
    req<{ root: string; files: ConfigFile[]; excluded: ConfigExcluded[] }>(
      `/instances/${id}/configs`,
    ),

  configFile: (id: string, path: string) =>
    req<ConfigContent>(`/instances/${id}/configs/file?path=${enc(path)}`),

  saveConfigFile: (
    id: string,
    path: string,
    text: string,
    eol: string,
    modTime: string,
    validate = true,
  ) =>
    req<{ ok: boolean }>(
      `/instances/${id}/configs/file`,
      jsonInit('PUT', { path, text, eol, modTime, validate }),
    ),

  restoreConfigFile: (id: string, path: string) =>
    req<{ ok: boolean }>(`/instances/${id}/configs/restore`, jsonInit('POST', { path })),

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
  downloadServerJar: (
    id: string,
    body: { type: string; version: string; build?: number; mirror?: string; tag?: string },
  ) => req<DownloadTask>(`/instances/${id}/server-jar`, jsonInit('POST', body)),

  fetchTask: (taskId: string) => req<DownloadTask>(`/fetch/tasks/${enc(taskId)}`),

  // ── 普通模组服 → 对应的混合端 ──
  // 分两条：先问「能转成什么、代价是什么」，再真转。
  // 不让前端自己拼一个 type 去调下载接口 —— 那样类型会先于文件被改掉。
  convertInfo: (id: string) => req<ConvertInfo>(`/instances/${enc(id)}/convert`),
  convert: (id: string, body: { target: string; build?: number; tag?: string; mirror?: string }) =>
    req<DownloadTask>(`/instances/${enc(id)}/convert`, jsonInit('POST', body)),

  cancelTask: (taskId: string) =>
    req<{ ok: boolean }>(`/fetch/tasks/${enc(taskId)}/cancel`, { method: 'POST' }),

  /** 删掉一条已结束的下载记录。运行中的会被后端拒（409）—— 先取消再删。 */
  dismissTask: (taskId: string) =>
    req<{ ok: boolean }>(`/fetch/tasks/${enc(taskId)}/dismiss`, { method: 'POST' }),

  // ── 鉴权 ──
  authStatus: () => req<AuthStatus>('/auth/status'),
  authLogin: (password: string) => req<{ ok: boolean }>('/auth/login', jsonInit('POST', { password })),
  authLogout: () => req<{ ok: boolean }>('/auth/logout', { method: 'POST' }),

  // ── 全局设置 ──
  settings: () => req<GlobalSettings>('/settings'),

  saveSettings: (patch: Partial<Pick<GlobalSettings, 'requireAuth' | 'trustLoopback' | 'sessionHours' | 'defaultMirror' | 'marketBase' | 'listenAddr'>>) =>
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

  action: (id: string, verb: 'start' | 'stop' | 'restart' | 'kill') =>
    req<{ ok: boolean }>(`/instances/${id}/${verb}`, { method: 'POST' }),

  sendCommand: (id: string, cmd: string) =>
    req<{ ok: boolean }>(`/instances/${id}/command`, jsonInit('POST', { cmd })),

  metrics: (id: string) => req<Metrics>(`/instances/${id}/metrics`),

  logHistory: (id: string) => req<string[]>(`/instances/${id}/logs/history`),

  // ── 插件 / 模组 ──
  plugins: (id: string) => req<{ dir: string; items: PluginItem[] }>(`/instances/${id}/plugins`),

  pluginToggle: (id: string, file: string, enabled: boolean) =>
    req<{ ok: boolean }>(`/instances/${id}/plugins/${enc(file)}`, jsonInit('PATCH', { enabled })),

  /**
   * 核对已装模组「在服务端能不能用」。按每个 jar 的 sha1 去 Modrinth 反查项目，
   * 再取项目的 server_side —— 这是唯一能覆盖 Forge 包的办法（Forge 的元数据里没有 side）。
   *
   * ⚠ 会联网，需要几秒。
   * ⚠ `identified` 可能小于 `scanned`：差值是**没核出来**的（本地改过的 jar、
   *   或没发布在 Modrinth 上的模组）—— 「没核出来」不等于「确认没问题」，界面别反过来说。
   */
  pluginSideAudit: (id: string) =>
    req<{
      scanned: number
      identified: number
      clientOnly: { file: string; projectId?: string; title?: string }[]
    }>(`/instances/${id}/plugins/side-audit`, { method: 'POST' }),

  pluginDelete: (id: string, file: string) =>
    req<{ ok: boolean }>(`/instances/${id}/plugins/${enc(file)}`, { method: 'DELETE' }),

  pluginUpload: (id: string, f: File) => {
    const fd = new FormData()
    fd.append('file', f)
    return postForm<{ ok: boolean; file: string }>(`/instances/${id}/plugins`, fd)
  },

  /** 模组图标地址。带 ?v= 文件时间戳，换了文件浏览器才会重新取。 */
  pluginIconUrl: (id: string, file: string, v: string | number) =>
    `/api/instances/${id}/plugins/${enc(file)}/icon?v=${enc(String(v))}`,

  /** 从直链装一个模组 / 插件。在线市场选好版本后也走这条。 */
  pluginInstallURL: (
    id: string,
    body: { url: string; filename?: string; sha1?: string; size?: number; label?: string },
  ) => req<DownloadTask>(`/instances/${id}/plugins/install-url`, jsonInit('POST', body)),

  // ── 文件管理 ──
  files: (id: string, path: string) =>
    req<FileListing>(`/instances/${id}/files?path=${enc(path)}`),

  fileRead: (id: string, path: string) =>
    req<{ path: string; content: string }>(`/instances/${id}/file?path=${enc(path)}`),

  fileWrite: (id: string, path: string, content: string) =>
    req<{ ok: boolean }>(`/instances/${id}/file`, jsonInit('PUT', { path, content })),

  fileDelete: (id: string, path: string) =>
    req<{ ok: boolean }>(`/instances/${id}/file?path=${enc(path)}`, { method: 'DELETE' }),

  fileUpload: (id: string, dir: string, f: File, opts?: { extract?: boolean; into?: string }) => {
    const fd = new FormData()
    fd.append('file', f)
    fd.append('path', dir)
    if (opts?.extract) fd.append('extract', '1')
    if (opts?.into) fd.append('into', opts.into)
    return postForm<{ ok: boolean; file?: string; extractedTo?: string }>(
      `/instances/${id}/files/upload`,
      fd,
    )
  },

  fileExtract: (id: string, path: string, into: 'folder' | 'here') =>
    req<{ ok: boolean; extractedTo: string }>(
      `/instances/${id}/files/extract`,
      jsonInit('POST', { path, into }),
    ),

  /**
   * 批量删除。后端逐条处理并回「哪几条成了、哪几条没成、各自为什么」——
   * 前端必须照实说，只报「删了 N 个」等于把失败的那几个藏起来。
   */
  filesBatchDelete: (id: string, paths: string[]) =>
    req<{ ok: string[]; failed: { path: string; err: string }[] }>(
      `/instances/${id}/files/batch-delete`,
      jsonInit('POST', { paths }),
    ),

  /** 新建文件夹。已存在时后端回 409（冲突），错误文案照原样抛给界面。 */
  fileMkdir: (id: string, path: string) =>
    req<{ path: string }>(`/instances/${id}/files/mkdir`, jsonInit('POST', { path })),

  /** 移动 / 重命名（同一件事：改名就是「移到同目录的另一个名字下」）。 */
  fileMove: (id: string, from: string, to: string) =>
    req<{ path: string }>(`/instances/${id}/files/move`, jsonInit('POST', { from, to })),

  fileCopy: (id: string, from: string, to: string) =>
    req<{ path: string }>(`/instances/${id}/files/copy`, jsonInit('POST', { from, to })),

  // ── 在线市场（Modrinth）──
  // sort / cat / offset 都允许留空：留空时后端按「有关键词→相关度，没关键词→热门榜」决定口径，
  // 这样页面一进来就能列出东西，不需要先输入关键词。
  modSearch: (
    q: string,
    loader: string,
    mc: string,
    opts?: { sort?: MarketSort; cat?: string; src?: MarketSource; offset?: number; limit?: number },
  ) =>
    req<MarketSearchResult>(
      `/mods/search?q=${enc(q)}&loader=${enc(loader)}&mc=${enc(mc)}` +
        `&sort=${enc(opts?.sort ?? '')}&cat=${enc(opts?.cat ?? '')}&src=${enc(opts?.src ?? '')}` +
        `&offset=${opts?.offset ?? 0}&limit=${opts?.limit ?? 20}`,
    ),

  modCategories: () => req<{ items: MarketCategory[] }>(`/mods/categories`),

  modGameVersions: () => req<{ items: string[] }>(`/mods/game-versions`),

  /**
   * 列某个项目的构建。
   * `all` 为 true 时不按当前实例的加载器 / MC 版本筛，把这个项目的**全部**构建列出来 ——
   * 默认只列这台服务器能用的（装了必定生效）。看全量是「点开关才拉」，
   * 因为 JEI 那种模组有三千多个构建，一进页面就拉不划算。
   */
  modVersions: (project: string, loader: string, mc: string, all = false) =>
    req<{ items: MarketVersion[] }>(
      `/mods/versions?project=${enc(project)}&loader=${enc(loader)}&mc=${enc(mc)}${all ? '&all=1' : ''}`,
    ),

  /** 中文简介。**尽力而为**：拿不到就是空对象，界面显示原文，不算失败。 */
  modTranslate: (ids: string[]) =>
    req<{ items: Record<string, string> }>(`/mods/translate?ids=${enc(ids.join(','))}`),

  /** 把版本里的「前置 project_id 列表」变成人能认的名字。 */
  modProjects: (ids: string[]) =>
    req<{ items: MarketProject[] }>(`/mods/projects?ids=${enc(ids.join(','))}`),

  /**
   * 单个模组项目的详情（独立详情页那一屏）。
   *
   * 和 modProjects（批量）的区别只有正文：批量那条**不带 body**（中位 4KB、最大 53KB，
   * 只为给列表补个标题不值得），详情页要的恰恰就是正文。
   * mirrored 表示这次是从镜像源读的 —— 要显示出来，否则信息对不上时用户没线索。
   */
  modProject: (project: string) =>
    req<{ item: MarketProjectDetail; base?: string; mirrored?: boolean }>(
      `/mods/project?project=${enc(project)}`,
    ),

  /**
   * 从 MC 百科取某个模组的关系星图（前置 / 拓展 / 联动）。
   *
   * 后端三级缓存（内存 30 分钟 → 磁盘 7 天 → 联网），所以重复进同一个模组是秒开、
   * 也不会反复打百科的服务器。传 force=true 可跳过缓存重抓（页面一直开着时的定时刷新用）。
   * 名字配不上（百科里没这个模组 / 站方没画图）后端返回 404，界面照实说。
   */
  mcmodRelations: (name: string, force = false) =>
    req<McmodRelations>(`/mods/mcmod-relations?name=${enc(name)}${force ? '&force=1' : ''}`),

  // ── 备份 ──
  backups: (id: string) => req<{ items: BackupItem[] }>(`/instances/${id}/backups`),

  /** 异步：回 jobId，真结果要轮询 /api/jobs/{id}（result = { file, size }）。 */
  backupCreate: (id: string, label: string) =>
    req<{ jobId: string }>(`/instances/${id}/backups`, jsonInit('POST', { label })),

  backupDelete: (id: string, file: string) =>
    req<{ ok: boolean }>(`/instances/${id}/backups/${enc(file)}`, { method: 'DELETE' }),

  /** 异步：回 jobId，result = { safety }（还原前的那份后悔药文件名）。 */
  backupRestore: (id: string, file: string) =>
    req<{ jobId: string }>(`/instances/${id}/backups/${enc(file)}/restore`, {
      method: 'POST',
    }),

  /** 自动备份策略（含后端算好的下次时间） */
  backupPolicy: (id: string) => req<BackupPolicyView>(`/instances/${id}/backup-policy`),

  saveBackupPolicy: (id: string, p: BackupPolicy) =>
    req<BackupPolicyView>(`/instances/${id}/backup-policy`, jsonInit('PUT', p)),

  // ── 计划任务 ──
  tasks: (id: string) => req<{ items: TaskView[] }>(`/instances/${id}/tasks`),

  taskCreate: (id: string, t: TaskPayload) =>
    req<TaskView>(`/instances/${id}/tasks`, jsonInit('POST', t)),

  taskUpdate: (id: string, taskId: string, t: TaskPayload) =>
    req<TaskView>(`/instances/${id}/tasks/${enc(taskId)}`, jsonInit('PUT', t)),

  /** 只改启用状态。单独一条，免得在一个旧表单上点开关把整条任务回退成旧值。 */
  taskToggle: (id: string, taskId: string, enabled: boolean) =>
    req<TaskView>(`/instances/${id}/tasks/${enc(taskId)}`, jsonInit('PATCH', { enabled })),

  taskDelete: (id: string, taskId: string) =>
    req<{ ok: boolean }>(`/instances/${id}/tasks/${enc(taskId)}`, { method: 'DELETE' }),

  // ── 玩家管理 ──
  /** 一次取全：在线 + 白名单 + OP + 玩家封禁 + IP 封禁 + 两个开关状态 */
  players: (id: string) => req<PlayerRoster>(`/instances/${id}/players`),

  /** 单个玩家的只读档案。只在详情抽屉打开时拉一次 —— 列表每 5 秒轮询，
   *  把这份摊进列表等于每 5 秒翻一遍世界目录。 */
  playerProfile: (id: string, name: string, uuid = '') =>
    req<PlayerProfile>(
      `/instances/${id}/players/profile?name=${enc(name)}${uuid ? `&uuid=${enc(uuid)}` : ''}`,
    ),

  playerWhitelist: (id: string, name: string, on: boolean) =>
    req<{ ok: boolean }>(`/instances/${id}/players/whitelist`, jsonInit('POST', { name, on })),

  playerOp: (id: string, name: string, level: number, on: boolean) =>
    req<{ ok: boolean }>(`/instances/${id}/players/op`, jsonInit('POST', { name, level, on })),

  /** expiresAt 是 RFC3339，空串 = 永久。
   *  ⚠ 限时只能在停服时封：服务端的 ban 命令不收时长（后端会给 409）。 */
  playerBan: (
    id: string,
    target: string,
    kind: 'player' | 'ip',
    reason: string,
    on: boolean,
    expiresAt = '',
  ) =>
    req<{ ok: boolean }>(
      `/instances/${id}/players/ban`,
      jsonInit('POST', { target, kind, reason, on, expiresAt }),
    ),

  playerKick: (id: string, name: string, reason: string) =>
    req<{ ok: boolean }>(`/instances/${id}/players/kick`, jsonInit('POST', { name, reason })),

  // ── 游戏规则（gamerule）──
  /** 读全部规则与当前值。服务器没跑会是 409（reason: "no-server"）——
   *  规则值是服务端内存里的东西，停服时读不到，不是「空」而是「不可读」。 */
  gamerules: (id: string) => req<GameruleList>(`/instances/${id}/gamerules`),

  /** 改一条规则。服务端会把个别值夹进合法范围，所以拿返回的 value 当事实。 */
  setGamerule: (id: string, name: string, value: string) =>
    req<{ ok: boolean; name: string; value: string }>(
      `/instances/${id}/gamerules`,
      jsonInit('POST', { name, value }),
    ),

  // ── 实时玩家数据 ──
  /** 服务端里那个只读插件写的快照：坐标 / 血量 / 背包 / 末影箱。
   *  服务器没跑也能调 —— 会带回「为什么没有数据」，而不是一片空白。 */
  live: (id: string) => req<LiveSnapshot>(`/instances/${id}/live`),

  /** 会**改玩家东西**的操作（给物品 / 清空背包 / 传送 / 治疗 / 切模式）。
   *  后端会记审计，失败也会记 —— 事后能查到谁在什么时候做了什么。 */
  liveCommand: (id: string, op: string, target: string, args: Record<string, unknown> = {}) =>
    req<LiveResult>(`/instances/${id}/live/command`, jsonInit('POST', { op, target, args })),

  liveAudit: (id: string) => req<LiveAudit[]>(`/instances/${id}/live/audit`),

  /** 实时数据的总开关。改动要下次启动服务器才生效。 */
  companionToggle: (id: string, on: boolean) =>
    req<{ ok: boolean; on: boolean; note: string }>(
      `/instances/${id}/companion`,
      jsonInit('PUT', { on }),
    ),

  // ── 导入已有服务器 ──
  /** 列目录（只列文件夹名，不读内容）。path 为空时列盘符。 */
  fsDirs: (path = '') => req<DirListing>(`/fs/dirs${path ? `?path=${enc(path)}` : ''}`),

  /** 识别一个目录是什么服务端。只读，不改任何东西。 */
  importScan: (dir: string) => req<ImportScan>('/import/scan', jsonInit('POST', { dir })),

  /** 按识别结果（或用户改过的值）把目录纳管成实例，带回一次体检报告。 */
  doImport: (payload: Record<string, unknown>) =>
    req<{ instance: Instance; preflight: Preflight }>('/import', jsonInit('POST', payload)),

  // ── 整合包升级（契约见 docs/MODPACK.md 第八节 8.2）──
  // 四个接口各管一件事：问「装的是哪个包」、只算不装看代价、真升、轮询。
  // 版本列表接口还没做，所以「选版本」这一环前端暂时让用户手填 Modrinth 的版本号 / id。

  /** 这个实例装的是哪个整合包、什么版本。have=false 说明不是整合包装的。 */
  modpackInfo: (id: string) => req<ModpackInfo>(`/instances/${enc(id)}/modpack`),

  /* ── 制作整合包（导入的反向操作） ──────────────────
     预览 GET（只算不写盘）与导出 POST（会落盘）。两者共用后端同一个构造函数，
     所以「预览看到的」和「真导出的」必然一致 —— 发出去的包改不了，
     预览是用户唯一的反悔机会。 */

  modpackExportPreview: (id: string, q?: ExportQuery) =>
    req<ModpackExportResult>(`/instances/${enc(id)}/modpack/export/preview${queryOf(q)}`),
  /** 异步：回 jobId，result = { client, server }。 */
  modpackExport: (id: string, q?: ExportQuery) =>
    req<{ jobId: string }>(`/instances/${enc(id)}/modpack/export${queryOf(q)}`, {
      method: 'POST',
    }),

  /**
   * 只算不装：先把「要删哪些文件」摊给用户看，再决定要不要真升。
   * version（市场里挑的版本号 / id）与 pack（本地 .mrpack 绝对路径）二选一，
   * 按存在的那个拼 query。开始升级时也只带回预览用的那一个 —— 服务端要拿同一个输入重算计划，
   * planHash 才对得上。
   */
  modpackUpgradePlan: (id: string, q: { version?: string; pack?: string }) => {
    const sp = new URLSearchParams()
    if (q.version) sp.set('version', q.version)
    if (q.pack) sp.set('pack', q.pack)
    const qs = sp.toString()
    return req<ModpackUpgradePlan>(`/instances/${enc(id)}/modpack/upgrade${qs ? `?${qs}` : ''}`)
  },

  /**
   * 真升级：起下载任务 + 落文件 + 删旧 + 写回 packstate。202 带回 opId，之后按它轮询。
   *
   * planHash 是预览时服务端给的指纹 —— 原样带回，让服务端校验「要执行的」和「你刚预览的」是不是同一份。
   * confirmStale 默认 false：服务端发现 planHash 对不上（计划变了）会回 409；
   * 这时前端**不**自动重试，而是重新预览、把新清单摊出来，等用户看过后**再点一次**才传 true。
   * ⚠ 和 levelJump 是两回事：版本跳跃是界面上的可见警告，confirmStale 管的是「看不见的竞态」。
   *
   * 这个方法单独走 fetch（不复用 req）：错误上要带 HTTP 状态码 + 服务端给的 reason 码，
   * 好让界面区分同一个 409 的几种语义 ——「计划变了（stale_plan，走重预览流程）」
   * 「实例在跑（instance_running）」「已有作业在跑（op_running）」—— 和别的失败，
   * 做出不同的下一步。req 那边把非 401/409/422 的错误都归一成纯文本 Error，拿不到状态码。
   */
  modpackUpgradeStart: (
    id: string,
    // version / pack 二选一（装新包只带 pack），与预览时用的输入保持同一个。
    body: { version?: string; pack?: string; planHash?: string; confirmStale?: boolean },
  ) =>
    (async () => {
      const r = await fetch('/api' + `/instances/${enc(id)}/modpack/upgrade`, jsonInit('POST', body))
      if (r.ok) return (await r.json()) as ModpackOpStart
      const j = (await r.json().catch(() => null)) as { error?: string; reason?: string } | null
      const e = new Error(j?.error ?? r.statusText) as Error & { status?: number; reason?: string }
      e.status = r.status
      e.reason = j?.reason
      throw e
    })(),

  /**
   * 统一作业端点。所有分钟级慢操作（备份/还原/导出/模组升级安装）共用这一组接口 ——
   * 别再为某一类操作新开一套 ops 路由，那是「改一处漏两处」的起点。
   */
  jobs: {
    get: (id: string) => req<JobSnapshot>(`/api/jobs/${enc(id)}`),
    /** owner 非空时只列该实例（正是「这个实例正在做什么」那一栏要的）。 */
    list: (owner?: string) =>
      req<{ items: JobSnapshot[] }>(`/api/jobs${owner ? `?owner=${enc(owner)}` : ''}`),
    cancel: (id: string) => req<{ ok: boolean }>(`/api/jobs/${enc(id)}/cancel`, { method: 'POST' }),
    /** 关账：把看过的终态记录从表里删掉。仍在跑的会被后端拒（409）。 */
    dismiss: (id: string) => req<{ ok: boolean }>(`/api/jobs/${enc(id)}/dismiss`, { method: 'POST' }),
  },

  modpackOp: async (opId: string): Promise<ModpackOp> =>
    jobToModpackOp(await req<JobSnapshot>(`/api/jobs/${enc(opId)}`)),

  modpackOpCancel: (opId: string) =>
    req<{ ok: boolean }>(`/api/jobs/${enc(opId)}/cancel`, { method: 'POST' }),

  /** 删掉一条已结束的整合包作业记录。运行中的会被后端拒（409）。 */
  modpackOpDismiss: (opId: string) =>
    req<{ ok: boolean }>(`/api/jobs/${enc(opId)}/dismiss`, { method: 'POST' }),

  // ── 整合包市场（P1，契约见 docs/MODPACK.md 第九节）──
  // 四步：搜 → 选版本 → 预览（只算不装，但要把包下下来）→ 安装（新建一个实例）。
  // 复用上面那套 MarketHit / MarketSearchResult / MarketVersion：后端返回的是同一批结构体，
  // 前端再抄一份类型，两边迟早各说各话。

  /**
   * 搜整合包。**默认只列服务端装得上的**（后端按 server_side 过滤，滤掉约 30% 的纯客户端包）。
   * all=true 关掉过滤 —— 那时必须把 onlyServer=false 说给用户听，别让他以为点进去都能装。
   */
  modpackSearch: (opts?: {
    q?: string
    loader?: string
    mc?: string
    sort?: MarketSort
    src?: MarketSource
    offset?: number
    limit?: number
    all?: boolean
  }) =>
    req<MarketSearchResult>(
      `/modpacks/search?q=${enc(opts?.q ?? '')}&loader=${enc(opts?.loader ?? '')}` +
        `&mc=${enc(opts?.mc ?? '')}&sort=${enc(opts?.sort ?? '')}&src=${enc(opts?.src ?? '')}` +
        `&offset=${opts?.offset ?? 0}&limit=${opts?.limit ?? 20}${opts?.all ? '&all=1' : ''}`,
    ),

  /**
   * 单个整合包的完整信息（详情页那一屏）。id 或 slug 都行 —— 列表里有的是 id，
   * 地址栏里手打的多半是 slug，两种都认。
   *
   * 详情会比列表多出这些：**正文 Markdown 原文**（要渲染）、链接、许可证、图集、加载器 / MC 版本全集。
   * ⚠ body 是**未清洗**的原始 Markdown，只有详情页能显示它；列表一律用 description。
   */
  modpackProject: (opts: { project: string }) =>
    req<{ item: MarketProjectDetail; base: string; mirrored: boolean }>(
      `/modpacks/project?project=${enc(opts.project)}`,
    ),

  /**
   * 某个包的版本列表。**只回带 .mrpack 的版本** —— 点了装不了的版本在界面上就是个死按钮。
   * 平台找不到这个项目时回 404（「已下架」），和「市场连不上」的 502 分得开：
   * 前者该回搜索页重挑，后者只能重试。
   */
  modpackVersions: (opts: { project: string; loader?: string; mc?: string }) =>
    req<{ items: MarketVersion[]; base: string; mirrored: boolean }>(
      `/modpacks/versions?project=${enc(opts.project)}` +
        `&loader=${enc(opts.loader ?? '')}&mc=${enc(opts.mc ?? '')}`,
    ),

  /**
   * 预览：把包下下来、算一遍计划。慢（要下几十 MB），但换来的是安装时不用重下。
   * version（市场版本 id）与 pack（本地 .mrpack 绝对路径）二选一 —— 服务端两个都给或都不给都回 400。
   */
  modpackInspect: (body: { version?: string; pack?: string }) =>
    req<ModpackInspect>('/modpacks/inspect', jsonInit('POST', body)),

  /**
   * 列出全部整合包作业（在跑的 + 最近结束的）。
   *
   * 为什么需要它：安装/升级是**后端在跑**的，跟「用户停在哪一页」无关。
   * 只靠发起时拿到的那个 opId 的话，刷新一下页面这笔作业就成了不可见的 ——
   * 它还在装，界面却是「什么都没发生」。全局那个下载入口就靠这份列表。
   */
  /**
   * 后台作业列表（右下角气泡 / 下载台账的唯一来源）。
   * ⚠ 返回**原始快照**，不要再经 jobToModpackOp 映射 —— 那个映射器会把非 install 的 kind
   * 一律当成 upgrade，备份/还原/导出会因此被显示成「升级」。消费方按 kind 自己出文案。
   */
  modpackOps: () => req<{ items: JobSnapshot[] }>('/api/jobs'),

  /**
   * 安装：新建实例 + 起作业，202 带回 instanceId 与 opId。
   *
   * 用 fetch 单独走（不复用 req）是为了拿到 HTTP 状态码：取件码过期是 400（要重新预览），
   * 「已经有作业在跑」是 409 op_running（等它结束）—— 两者都被 req 归一成纯文本，界面上做不出区分。
   */
  modpackInstall: (body: ModpackInstallReq) =>
    (async () => {
      const r = await fetch('/api/modpacks/install', jsonInit('POST', body))
      if (r.ok) return (await r.json()) as ModpackInstallStart
      const j = (await r.json().catch(() => null)) as { error?: string; reason?: string } | null
      const e = new Error(j?.error ?? r.statusText) as Error & { status?: number; reason?: string }
      e.status = r.status
      e.reason = j?.reason
      throw e
    })(),

  // ── 系统 ──
  java: () => req<{ items: JavaRuntime[] }>('/java'),

  /** 可选的主版本清单（静态，零外部请求） */
  javaReleases: () => req<{ items: JavaMajor[] }>('/java/releases'),

  /** 查某个主版本目前能下的那一份（会去问源站，有 10 分钟缓存） */
  javaRelease: (major: number) => req<JDKRelease>(`/java/releases/${major}`),

  /** 起一个 JDK 下载任务（复用下载器的多线程 / 续传 / 校验，下完自动解压） */
  javaDownload: (major: number) => req<DownloadTask>('/java/download', jsonInit('POST', { major })),

  system: () => req<SystemInfo>('/system'),
}
