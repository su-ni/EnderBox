/**
 * 项目详情页用到的「源站内部值 → 人话」映射。
 *
 * 为什么单独一个文件：它们是纯函数 / 纯常量、没有 JSX，放在组件文件里会被
 * `react(only-export-components)` 拦下来（那个文件就不再是「只导出组件」，
 * fast refresh 会失效）。整合包页、模组详情页、插件面板都要用，所以必须共享。
 */

/**
 * Modrinth 的 categories 里混着**加载器名** —— 那是筛条件，不是模组的「分类」。
 * 展示标签时必须挑掉，否则每一行都挂着 forge / fabric，纯噪声。
 *
 * ⚠ 这份名单**只有一份**。插件面板、模组搜索结果、模组详情页都从这里取；
 * 各写一份的话，新加一个加载器（比如哪天多一个 `architectury`）就会三处表现不一致。
 */
export const MOD_LOADER_TAGS = new Set([
  'forge',
  'fabric',
  'neoforge',
  'quilt',
  'rift',
  'liteloader',
  'modloader',
  'bungeecord',
  'paper',
  'spigot',
  'purpur',
  'folia',
  'velocity',
  'waterfall',
  'bukkit',
  'sponge',
  'bta-babric',
  'babric',
  'legacy-fabric',
  'ornithe',
  'datapack',
])

/**
 * 许可证 id → 能读的话。Modrinth 给的是 `MIT` / `LicenseRef-All-Rights-Reserved` 这种键，
 * 后者直接印给用户等于把内部标识漏出来 —— 而它恰恰是最常见的那一个（默认就是"保留所有权利"）。
 */
export function licenseLabel(id: string): string {
  switch (id) {
    case 'LicenseRef-All-Rights-Reserved':
    case 'ARR':
      return '保留所有权利'
    case 'MIT':
      return 'MIT'
    case 'LGPL-3.0-only':
      return 'LGPL-3.0'
    case 'GPL-3.0-only':
      return 'GPL-3.0'
    case 'GPL-2.0-only':
      return 'GPL-2.0'
    case 'Apache-2.0':
      return 'Apache-2.0'
    case 'MPL-2.0':
      return 'MPL-2.0'
    case 'CC0-1.0':
      return 'CC0'
    case 'CC-BY-4.0':
      return 'CC-BY-4.0'
    case 'CC-BY-SA-4.0':
      return 'CC-BY-SA-4.0'
    default:
      return id
  }
}

/** client_side / server_side 的值翻成人话。这是「装上去有没有用」的直接证据。 */
export function sideLabel(s: string): string {
  switch (s) {
    case 'required':
      return '必须（装在服务端才有意义）'
    case 'optional':
      return '可用（服务端可选）'
    case 'unsupported':
      return '不支持'
    default:
      return s || '未知'
  }
}

/** 版本类型 → 人话。 */
export function releaseLabel(t: string): string {
  switch (t) {
    case 'release':
      return '正式版'
    case 'beta':
      return '测试版'
    case 'alpha':
      return '内测版'
    default:
      return t || '未知'
  }
}

/**
 * Modrinth 分类 id → 中文。**展示用**（市场行标签 / 市场卡片 tags / 详情 rail 的「分类」）。
 *
 * 为什么与筛选栏的名单分开：后端的 `fetch.CuratedCategories` 是**精选**——
 * 只放最常被点的十几个，筛选栏摆太多没人挑得动；而展示时**每一个出现的分类
 * 都要有中文名**，否则像 `decoration` 这种不在筛选栏里的，就会把英文内部值
 * 直接漏到界面上（2026-10-09 实拍：行标签印着「decoration」）。
 *
 * ⚠ 这里只收 `mods` / `modpacks` 两种搜索会出现的值（我们只搜这两种）。
 * 表里没有的一律原样返回 —— 翻不出比乱猜一个更诚实。
 */
export function catLabel(id: string): string {
  switch (id) {
    // ── mods（含与 modpacks 共享的值）──
    case 'adventure':
      return '冒险'
    case 'cursed':
      return '奇葩'
    case 'decoration':
      return '装饰'
    case 'economy':
      return '经济'
    case 'equipment':
      return '装备'
    case 'food':
      return '食物'
    case 'game-mechanics':
      return '游戏机制'
    case 'library':
      return '依赖库'
    case 'magic':
      return '魔法'
    case 'management':
      return '管理'
    case 'minigame':
      return '小游戏'
    case 'mobs':
      return '生物'
    case 'optimization':
      return '优化'
    case 'social':
      return '社交'
    case 'storage':
      return '存储'
    case 'technology':
      return '科技'
    case 'transportation':
      return '交通'
    case 'utility':
      return '实用'
    case 'worldgen':
      return '世界生成'
    // ── modpacks 专有 ──
    case 'challenging':
      return '挑战'
    case 'combat':
      return '战斗'
    case 'kitchen-sink':
      return '全家桶'
    case 'lightweight':
      return '轻量'
    case 'multiplayer':
      return '多人'
    case 'quests':
      return '任务'
    default:
      return id
  }
}

/**
 * 加载器 id → 显示名。Modrinth 给的是小写 id（forge / neoforge /…），
 * 界面上要显示成社区里的写法（Forge / NeoForge）。market.html 设计稿里
 * 加载器徽章也是这么写的。表外的一律原样返回。
 */
export function loaderLabel(id: string): string {
  switch (id) {
    case 'forge':
      return 'Forge'
    case 'fabric':
      return 'Fabric'
    case 'neoforge':
      return 'NeoForge'
    case 'quilt':
      return 'Quilt'
    case 'liteloader':
      return 'LiteLoader'
    case 'modloader':
      return 'ModLoader'
    case 'rift':
      return 'Rift'
    case 'bukkit':
      return 'Bukkit'
    case 'spigot':
      return 'Spigot'
    case 'paper':
      return 'Paper'
    case 'purpur':
      return 'Purpur'
    case 'folia':
      return 'Folia'
    case 'velocity':
      return 'Velocity'
    case 'bungeecord':
      return 'BungeeCord'
    case 'waterfall':
      return 'Waterfall'
    case 'sponge':
      return 'Sponge'
    default:
      return id
  }
}
