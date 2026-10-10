/**
 * Minecraft 服务端命令表 —— 控制台补全的地基。
 *
 * ⚠ 这只是**补全用的词表**，不是权限校验：面板不拦命令，敲什么照发什么。
 *   它的唯一职责是「让手别打全」，所以宁可少列也不要列错 —— 补出一个不存在的命令
 *   比不补更糟（用户会以为服务器支持）。
 *
 * 版本口径：以原版 1.21 为准（含 1.19~1.21 新增的 `/random` `/transfer` `/fillbiome`、
 * 1.20.5 改名的 `/damage` 等）。插件命令单列在 PLUGIN_COMMANDS 里，
 * 因为它们**装了才有**，不能混进原版表让没装插件的用户看到。
 */

/** 参数类型决定这一格补什么：枚举给候选、玩家给在线名单、数字给提示、其余给自由文本 */
export type ArgType = 'enum' | 'player' | 'number' | 'text' | 'item' | 'gamerule' | 'world'

export type CmdArg = {
  /** 显示名。`<>` 必填、`[]` 可选，只影响提示文案 */
  name: string
  type: ArgType
  /** type=enum 时的候选。给全，别偷懒 —— 少一个用户就得多打一次字 */
  values?: string[]
  /** 该参数可以重复（如 `/gamerule` 的多个值、`/time` 的 `<time>` 后还能跟单位） */
  rest?: boolean
}

export type McCommand = {
  name: string
  /** 一句话说明。太长会撑爆候选列表，控制在 30 字内 */
  desc: string
  args?: CmdArg[]
  /**
   * 别名。MC 里短别名用得很凶（/tp = /teleport、/w = /msg），
   * 补全和查找都必须认 —— 只认正名的话，玩家敲 /tp 就永远补不出来。
   */
  aliases?: string[]
}

const GAMEMODE: CmdArg = {
  name: '<模式>',
  type: 'enum',
  values: ['survival', 'creative', 'adventure', 'spectator'],
}
const COORD: CmdArg = { name: '<x y z>', type: 'text' }

/** 原版命令。按字母序，方便人肉校对 */
export const VANILLA_COMMANDS: McCommand[] = [
  { name: 'advancement', desc: '授予或收回进度', args: [
    { name: '<grant|revoke>', type: 'enum', values: ['grant', 'revoke'] },
    { name: '<目标>', type: 'player' },
    { name: '<only|through|from|until>', type: 'enum', values: ['only', 'through', 'from', 'until'] },
    { name: '<进度>', type: 'text' },
  ] },
  { name: 'attribute', desc: '读改实体属性', args: [
    { name: '<目标>', type: 'player' }, { name: '<属性>', type: 'text' },
  ] },
  { name: 'ban', desc: '封禁玩家（记 UUID）', args: [{ name: '<玩家>', type: 'player' }, { name: '[原因…]', type: 'text', rest: true }] },
  { name: 'ban-ip', desc: '封禁 IP', args: [{ name: '<IP>', type: 'text' }, { name: '[原因…]', type: 'text', rest: true }] },
  { name: 'banlist', desc: '查看封禁名单', args: [{ name: '[ips|players]', type: 'enum', values: ['ips', 'players'] }] },
  { name: 'bossbar', desc: '管理 Boss 血条', args: [
    { name: '<add|remove|set|get>', type: 'enum', values: ['add', 'remove', 'set', 'get'] },
  ] },
  { name: 'clear', desc: '清空玩家背包', args: [{ name: '[目标]', type: 'player' }, { name: '[物品]', type: 'item' }] },
  { name: 'clone', desc: '复制方块区域', args: [COORD, { name: '<目标位置>', type: 'text' }] },
  { name: 'damage', desc: '对实体造成伤害（1.20.5 前叫 /hurt）', args: [
    { name: '<目标>', type: 'player' }, { name: '<伤害值>', type: 'number' },
  ] },
  { name: 'data', desc: '读写方块/实体的 NBT', args: [
    { name: '<get|merge|modify|remove>', type: 'enum', values: ['get', 'merge', 'modify', 'remove'] },
  ] },
  { name: 'datapack', desc: '启停数据包', args: [
    { name: '<enable|disable|list>', type: 'enum', values: ['enable', 'disable', 'list'] },
  ] },
  { name: 'defaultgamemode', desc: '设新玩家的默认模式', args: [GAMEMODE] },
  { name: 'deop', desc: '取消管理员', args: [{ name: '<玩家>', type: 'player' }] },
  { name: 'difficulty', desc: '设置难度', args: [
    { name: '<peaceful|easy|normal|hard>', type: 'enum', values: ['peaceful', 'easy', 'normal', 'hard'] },
  ] },
  { name: 'effect', desc: '给药水效果', args: [
    { name: '<give|clear>', type: 'enum', values: ['give', 'clear'] },
    { name: '<目标>', type: 'player' }, { name: '<效果>', type: 'text' },
  ] },
  { name: 'enchant', desc: '给手持物品附魔', args: [
    { name: '<目标>', type: 'player' }, { name: '<附魔>', type: 'text' },
  ] },
  { name: 'execute', desc: '条件执行（子命令最多）', args: [
    { name: '<子命令>', type: 'enum', values: ['as', 'at', 'if', 'unless', 'run', 'positioned', 'facing', 'align', 'in', 'store'] },
  ] },
  { name: 'experience', desc: '给经验', aliases: ['xp'], args: [
    { name: '<add|set|query>', type: 'enum', values: ['add', 'set', 'query'] },
    { name: '<目标>', type: 'player' }, { name: '<数量>', type: 'number' },
  ] },
  { name: 'fill', desc: '填充方块区域', args: [COORD, { name: '<方块>', type: 'text' }] },
  { name: 'fillbiome', desc: '填充生物群系', args: [COORD, { name: '<群系>', type: 'text' }] },
  { name: 'forceload', desc: '强制加载区块', args: [
    { name: '<add|remove|query>', type: 'enum', values: ['add', 'remove', 'query'] },
  ] },
  { name: 'function', desc: '执行数据包函数', args: [{ name: '<函数>', type: 'text' }] },
  { name: 'gamemode', desc: '切换游戏模式', args: [GAMEMODE, { name: '[目标]', type: 'player' }] },
  { name: 'gamerule', desc: '查改游戏规则（补全会带规则名）', args: [
    { name: '<规则>', type: 'gamerule' }, { name: '[值]', type: 'text' },
  ] },
  { name: 'give', desc: '给玩家物品', args: [
    { name: '<目标>', type: 'player' }, { name: '<物品>', type: 'item' }, { name: '[数量]', type: 'number' },
  ] },
  { name: 'help', desc: '命令帮助', args: [{ name: '[命令]', type: 'text' }] },
  { name: 'item', desc: '改方块容器里的物品（1.20.5+）', args: [
    { name: '<replace|modify>', type: 'enum', values: ['replace', 'modify'] },
  ] },
  { name: 'kick', desc: '踢出玩家（可重进）', args: [{ name: '<玩家>', type: 'player' }, { name: '[原因…]', type: 'text', rest: true }] },
  { name: 'kill', desc: '杀死实体', args: [{ name: '[目标]', type: 'player' }] },
  { name: 'list', desc: '列出在线玩家', args: [{ name: '[uuids]', type: 'enum', values: ['uuids'] }] },
  { name: 'locate', desc: '定位结构/群系', args: [
    { name: '<structure|biome|poi>', type: 'enum', values: ['structure', 'biome', 'poi'] },
    { name: '<目标>', type: 'text' },
  ] },
  { name: 'loot', desc: '把战利品表吐到某处', args: [
    { name: '<spawn|give|insert|replace>', type: 'enum', values: ['spawn', 'give', 'insert', 'replace'] },
  ] },
  { name: 'me', desc: '发一条动作消息', args: [{ name: '<动作…>', type: 'text', rest: true }] },
  { name: 'msg', desc: '私聊', aliases: ['tell', 'w'], args: [
    { name: '<目标>', type: 'player' }, { name: '<消息…>', type: 'text', rest: true },
  ] },
  { name: 'op', desc: '授予管理员', args: [{ name: '<玩家>', type: 'player' }] },
  { name: 'pardon', desc: '解除封禁', args: [{ name: '<玩家>', type: 'player' }] },
  { name: 'pardon-ip', desc: '解除 IP 封禁', args: [{ name: '<IP>', type: 'text' }] },
  { name: 'particle', desc: '生成粒子效果', args: [{ name: '<粒子>', type: 'text' }, COORD] },
  { name: 'playsound', desc: '播放音效', args: [
    { name: '<音效>', type: 'text' }, { name: '<来源>', type: 'enum', values: ['master', 'music', 'record', 'weather', 'block', 'hostile', 'neutral', 'player', 'ambient', 'voice'] },
  ] },
  { name: 'random', desc: '随机数（1.20.2+）', args: [
    { name: '<value|roll|reset>', type: 'enum', values: ['value', 'roll', 'reset'] },
  ] },
  { name: 'recipe', desc: '解锁/锁定配方', args: [
    { name: '<give|take>', type: 'enum', values: ['give', 'take'] },
  ] },
  { name: 'reload', desc: '重载数据包与函数', args: [] },
  { name: 'save-all', desc: '把世界写盘（备份前先敲它）', args: [
    { name: '[flush]', type: 'enum', values: ['flush'] },
  ] },
  { name: 'save-off', desc: '暂停自动存盘', args: [] },
  { name: 'save-on', desc: '恢复自动存盘', args: [] },
  { name: 'say', desc: '全服广播', args: [{ name: '<消息…>', type: 'text', rest: true }] },
  { name: 'schedule', desc: '定时执行函数', args: [
    { name: '<function|clear>', type: 'enum', values: ['function', 'clear'] },
  ] },
  { name: 'scoreboard', desc: '记分板', args: [
    { name: '<objectives|players>', type: 'enum', values: ['objectives', 'players'] },
  ] },
  { name: 'seed', desc: '看世界种子', args: [{ name: '[值]', type: 'text' }] },
  { name: 'setblock', desc: '放置单个方块', args: [COORD, { name: '<方块>', type: 'text' }] },
  { name: 'setidletimeout', desc: '挂机踢出分钟数', args: [{ name: '<分钟>', type: 'number' }] },
  { name: 'setworldspawn', desc: '设世界出生点', args: [{ name: '[坐标]', type: 'text' }] },
  { name: 'spawnpoint', desc: '设玩家出生点', args: [{ name: '[目标]', type: 'player' }, { name: '[坐标]', type: 'text' }] },
  { name: 'spectate', desc: '旁观某个实体', args: [{ name: '<目标>', type: 'player' }] },
  { name: 'spreadplayers', desc: '随机散布玩家', args: [{ name: '<中心>', type: 'text' }] },
  { name: 'stop', desc: '停服（面板的「停止」走的是它）', args: [] },
  { name: 'stopsound', desc: '停音效', args: [{ name: '[目标]', type: 'player' }] },
  { name: 'summon', desc: '生成实体', args: [{ name: '<实体>', type: 'text' }, { name: '[坐标]', type: 'text' }] },
  { name: 'tag', desc: '改实体标签', args: [{ name: '<目标>', type: 'player' }] },
  { name: 'team', desc: '队伍管理', args: [
    { name: '<add|remove|join|leave|list|modify|empty>', type: 'enum', values: ['add', 'remove', 'join', 'leave', 'list', 'modify', 'empty'] },
  ] },
  { name: 'teammsg', desc: '给队内发消息', aliases: ['tm'], args: [{ name: '<消息…>', type: 'text', rest: true }] },
  { name: 'teleport', desc: '传送', aliases: ['tp'], args: [
    { name: '<目标>', type: 'player' }, { name: '<位置>', type: 'text' },
  ] },
  { name: 'tellraw', desc: '发 JSON 富文本', args: [
    { name: '<目标>', type: 'player' }, { name: '<JSON>', type: 'text' },
  ] },
  { name: 'time', desc: '查询或设置时间', args: [
    { name: '<add|query|set>', type: 'enum', values: ['add', 'query', 'set'] },
    { name: '<值>', type: 'enum', values: ['day', 'noon', 'night', 'midnight', 'daytime', 'gametime'] },
  ] },
  { name: 'title', desc: '屏幕标题文字', args: [
    { name: '<目标>', type: 'player' },
    { name: '<clear|reset|title|subtitle|actionbar|times>', type: 'enum', values: ['clear', 'reset', 'title', 'subtitle', 'actionbar', 'times'] },
  ] },
  { name: 'transfer', desc: '把玩家转到另一台服（1.20.5+）', args: [
    { name: '<主机>', type: 'text' }, { name: '[端口]', type: 'number' },
  ] },
  { name: 'trigger', desc: '触发记分板触发器', args: [{ name: '<目标>', type: 'text' }] },
  { name: 'weather', desc: '设天气', args: [
    { name: '<clear|rain|thunder>', type: 'enum', values: ['clear', 'rain', 'thunder'] },
    { name: '[时长]', type: 'number' },
  ] },
  { name: 'whitelist', desc: '白名单管理（面板「玩家」页走的是它）', args: [
    { name: '<add|remove|list|on|off|reload>', type: 'enum', values: ['add', 'remove', 'list', 'on', 'off', 'reload'] },
    { name: '[玩家]', type: 'player' },
  ] },
  { name: 'worldborder', desc: '世界边界', args: [
    { name: '<add|center|damage|get|set|warning>', type: 'enum', values: ['add', 'center', 'damage', 'get', 'set', 'warning'] },
  ] },
]

/**
 * 常用插件命令。**装了才有**，所以单独一张表、单独一档候选 ——
 * 混进原版表的话，没装插件的用户会以为 `/home` 能用。
 */
export const PLUGIN_COMMANDS: McCommand[] = [
  { name: 'version', desc: '服务端版本', aliases: ['ver', 'about'], args: [] },
  { name: 'plugins', desc: '已装插件列表', aliases: ['pl'], args: [] },
  { name: 'bukkit:help', desc: 'Bukkit 帮助', args: [] },
  { name: 'home', desc: '回自己的家（EssentialsX）', args: [{ name: '[家名]', type: 'text' }] },
  { name: 'sethome', desc: '设家（EssentialsX）', args: [{ name: '[家名]', type: 'text' }] },
  { name: 'spawn', desc: '回出生点（EssentialsX）', args: [] },
  { name: 'tpa', desc: '请求传送到某人（EssentialsX）', args: [{ name: '<玩家>', type: 'player' }] },
  { name: 'tpaccept', desc: '接受传送请求（EssentialsX）', args: [] },
  { name: 'warp', desc: '去传送点（EssentialsX）', args: [{ name: '<名称>', type: 'text' }] },
  { name: 'setwarp', desc: '建传送点（EssentialsX）', args: [{ name: '<名称>', type: 'text' }] },
  { name: 'kit', desc: '领取套装（EssentialsX）', args: [{ name: '[名称]', type: 'text' }] },
  { name: 'heal', desc: '回满血（EssentialsX）', args: [{ name: '[玩家]', type: 'player' }] },
  { name: 'feed', desc: '喂饱（EssentialsX）', args: [{ name: '[玩家]', type: 'player' }] },
  { name: 'ec', desc: '末影箱（EssentialsX）', args: [{ name: '[玩家]', type: 'player' }] },
  { name: 'back', desc: '回上一个位置（EssentialsX）', args: [] },
  { name: 'lag', desc: '看服务器负载（EssentialsX）', args: [] },
  { name: 'tps', desc: '看 TPS（Spigot/Paper 内置）', args: [] },
  { name: 'co', desc: '查方块记录（CoreProtect）', args: [
    { name: '<i|lookup|rollback|restore>', type: 'enum', values: ['i', 'lookup', 'rollback', 'restore'] },
  ] },
  { name: 'money', desc: '看余额（EssentialsX 经济）', args: [{ name: '[玩家]', type: 'player' }] },
  { name: 'pay', desc: '转账（EssentialsX 经济）', args: [{ name: '<玩家>', type: 'player' }, { name: '<金额>', type: 'number' }] },
  { name: 'gm', desc: '切模式（EssentialsX 短命令）', args: [GAMEMODE] },
  { name: 'fly', desc: '飞行开关（EssentialsX）', args: [{ name: '[玩家]', type: 'player' }] },
  { name: 'god', desc: '无敌开关（EssentialsX）', args: [{ name: '[玩家]', type: 'player' }] },
]

/** 单例给外面用：原版在前、插件在后 */
export const ALL_COMMANDS: McCommand[] = [...VANILLA_COMMANDS, ...PLUGIN_COMMANDS]

/** 命中一次补全要用的查询结构 */
export function findCommand(name: string): McCommand | undefined {
  return ALL_COMMANDS.find((c) => c.name === name || c.aliases?.includes(name))
}

/**
 * MC 原版游戏规则名（`/gamerule` 的补全用）。
 * ⚠ 只列**服务端常见**的那些。写全 200 条没有意义，反而让候选列表变成一堵墙。
 */
export const COMMON_GAMERULES = [
  'keepInventory', 'mobGriefing', 'doDaylightCycle', 'doWeatherCycle', 'doMobSpawning',
  'doFireTick', 'doMobLoot', 'doTileDrops', 'doEntityDrops', 'naturalRegeneration',
  'randomTickSpeed', 'spawnRadius', 'maxEntityCramming', 'playersSleepingPercentage',
  'fallDamage', 'fireDamage', 'drowningDamage', 'freezeDamage', 'showDeathMessages',
  'announceAdvancements', 'commandBlockOutput', 'logAdminCommands', 'sendCommandFeedback',
  'spectatorsGenerateChunks', 'disableElytraMovementCheck', 'forgiveDeadPlayers',
  'universalAnger', 'doInsomnia', 'doPatrolSpawning', 'doTraderSpawning', 'doWardenSpawning',
  'doImmediateRespawn', 'keepInventory', 'reducedDebugInfo', 'pvp',
]
