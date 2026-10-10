/** 「石头拼合」过场：从两侧向中间、一列一列拼出一整屏石头，拼满 → 换内容 → 散场。
 *
 *  ── 两个身份（用户 2026-10-10 拍板）─────────────────────────────────
 *  ① **进入加载幕**（当前唯一在用）：桌面端一开 / 网页一进 / 登录通过时铺满整屏
 *     （cover: true），首屏数据在幕布后面装好；拼满 ≈ 加载完成，散场 ≈ 揭幕。
 *     调用点在 App.tsx 的 splash() —— 那里还负责「自动化环境不放幕」的开关。
 *  ② 通用过场：拼满后 swap 换内容再散场（不铺幕）。留着，别的场景想用还能叫。
 *  ⚠ 它**不上切页、不上换视图**（用户点名：工作区切换用承接式淡入淡出就行，幕布太断场）。
 *
 *  ── 形态（用户 10-10 二轮拍板：铺满整个启动窗口，不是居中一个小方阵）────
 *  石阵**铺满整个视口**（行列数按视口推导，见下）→ 从**左右两边**开始、**一列一列**把石头
 *  落进槽里（列内从上到下、带重力下落，落地压扁 + 扬一点灰）→ 一直拼到**正中那一列**＝拼满
 *  → 定格一拍（这拍里把内容换掉：swap）→ **按拼合次序**一块块散掉，露出新内容。
 *  材质四种（MC 系）：平滑石头 / 石头 / 圆石 / 深板岩，按双尺度噪声成片分布。
 *
 *  ── 为什么是一块 canvas（和 blockReveal 同一套路）──────────────────
 *  上百块带像素纹理的方块，用 DOM 就是几百个画布/div；单 canvas + rAF 逐帧画，
 *  时序完全由这份文件说了算，也不跟冻结层 design.css 斗优先级。状态全在模块里，不进 React。
 *
 *  ── 自适应（用户点名的要求）────────────────────────────────────────
 *  几何只看视口：格子边长 = 短边 × SPAN ÷ DENSITY（clamp CELL_MIN~CELL_MAX 定档），
 *  **列数 = ceil(视口宽 ÷ 格边长)、行数 = ceil(视口高 ÷ 格边长)** —— 石阵 ≥ 视口、居中裁切，
 *  铺满整屏（1440×900 ≈ 18×11；竖屏/超宽屏自然换行数，不用改代码）。
 *  视口中途变了连画布带格阵一起重排（resize 监听，散场结束摘掉）。
 *  **时间线不随屏幕变** —— 铺满 1080p 和铺满 4K 的总时长严格相同：
 *  列/行间隔不是写死值，而是**按固定预算反推**（列间隔×列数 + 行间隔×行数 = 常数），
 *  格子多了每列就快一点，总节奏一秒不差（常数只认 speed 倍率）。
 *
 *  ── logo 段（用户 10-10 三轮拍板：拼满后中央要「拼成我们的 logo」）──────
 *  拼满闪白稍息后，石阵中央 min(10, 行列数) 格见方的区域里，砖块**从中心向外**、
 *  一块块翻新成「草方块 logo」——配色就是 UI 里 IconBlock 的四色
 *  （亮草绿 #6fbf44 / 深草绿 #4c8f2a / 土棕 #6f4520 / 深棕 #3b2a1a）。
 *  ⚠ 图案不是自由发挥：把 IconBlock 的 16×16 像素画**抽稀**到砖格（点缀优先、
 *  底色取多数）——平直草边 + 精确点缀，远看就是一枚放大的草方块。
 *  单格翻新 = 灰砖交叉淡出 + 彩砖从 1.16 缩回 1 「崩出来」+ 短促白闪。
 *  ⚠ 区内格数、图案采样、扩散距离全按**归一化**算：任意屏幕大小图案不变形。
 *
 *  ── 节奏 ─────────────────────────────────────────────────────────
 *  起手 240ms → 逐列拼合 → 拼满（闪一下）→ 稍息 → logo 逐格翻新
 *  → 定格 hold(默认 480ms，让 logo 亮够一拍) → swap
 *  → 40ms 后开始散场（同序散掉，中央 logo 最后消失）→ 全清。
 *  总预算等价于原 9×9 版：列间 105ms/列内 17ms 的「9 列预算」被行列数重新分配。
 *  ⚠ S() 有 24ms 下限：S(17)→24 —— 别按 17 心算时长（T_FULL 是 1462 不是 1406）。
 *  标准速度全程约 3.3s（含 logo 段约 0.65s）；speed 倍率可以把整条时间线等比缩放。
 *
 *  ⚠ 动效关着（body.no-anim）/ 系统要求减少动效 → 直接 swap，不留空档。
 *  ⚠ swap 是**唯一**改状态的地方，且只在拼满之后调用；提前改＝石头白拼。
 *  ⚠ 标签页切后台时 rAF 停摆，但 swap 不能跟着停 —— 有 setTimeout 兜底。
 *  ⚠ cover 的幕布跟着槽位底板一起 smoothstep 淡出，散场结束正好透干净，不留「啪一下没了」；
 *    石头自己按各自时序散（不乘幕布透明度），最后一块消失时幕布也正好归零。
 */

import { stillMotion } from './blockReveal'

/** 密度基准：短边大约摆 11 格（≈ 9 / 0.82）。格子边长按它定档，行列数由视口推导。 */
const DENSITY = 9
/** 一格的边长上下限（CSS px）：太小看不清纹理，太大显得块数少。 */
const CELL_MIN = 36
/* 上限 144 是 16 的整倍数（纹理放大最干净），也刚好在 1440p 及以下不截断 SPAN 比例：
   短边 ≈1580px 以内，格子严格按「短边 × 0.82 ÷ 9」走；再大的屏（4K@100%）才见顶。 */
const CELL_MAX = 144
/** 格子边长相对「视口短边」的比例（定档用，不再是方阵占比 —— 石阵要铺满整屏）。 */
const SPAN = 0.82

/** MC 四材质：平滑石头（亮、几乎平）/ 石头（细噪点）/ 圆石（大颗杂色）/ 深板岩（竖纹、最深）。 */
const MAT: { base: string; mode: 'plain' | 'speck' | 'cobble' | 'stripe' }[] = [
  { base: '#c6c6c6', mode: 'plain' },
  { base: '#9e9e9e', mode: 'speck' },
  { base: '#828282', mode: 'cobble' },
  { base: '#6b6b72', mode: 'stripe' },
]

/** logo 翻新材质：草方块四色（与 UI 里 IconBlock 同一套色板）。
 *  ⚠ plain 而不是 speck：每格是 logo 的「一块」，要接近纯色才清晰；
 *  雪花噪点会把像素画糊成疙瘩（实测踩过）。 */
const LOGO_MAT: { base: string; mode: 'plain' | 'speck' | 'cobble' | 'stripe' }[] = [
  { base: '#6fbf44', mode: 'plain' },
  { base: '#4c8f2a', mode: 'plain' },
  { base: '#6f4520', mode: 'plain' },
  { base: '#3b2a1a', mode: 'plain' },
]
/** logo 区边长（格）：居中一块 10×10。小屏放不下时按 cols/rows 收缩（见 fit）。 */
const LOGO_N = 10

/** IconBlock 的 16×16 图案重演（值 = LOGO_MAT 索引：0 亮绿 / 1 深绿 / 2 土棕 / 3 深棕）。
 *  ⚠ 必须和 Icons.tsx 里 IconBlock 的 rect 顺序逐条对齐 —— 那是品牌 mark 的正身，
 *  这里「按同一份画法再画一遍」，抽稀后才是「拼成我们的 logo」而不是自由发挥。 */
const LOGO_ICON: number[] = (() => {
  const g = new Array<number>(16 * 16).fill(3) // body 深棕打底
  const rect = (x: number, y: number, w: number, h: number, v: number) => {
    for (let yy = y; yy < y + h; yy++) for (let xx = x; xx < x + w; xx++) g[yy * 16 + xx] = v
  }
  rect(0, 0, 16, 5, 1) // 草皮（深绿）
  rect(1, 2, 2, 2, 0) // 亮绿点缀
  rect(6, 1, 2, 2, 2) // 土棕点缀
  rect(2, 7, 3, 2, 2)
  rect(8, 9, 4, 2, 0) // 亮绿点缀（土区里）
  rect(10, 12, 3, 2, 2)
  return g
})()

type Tile = {
  i: number
  x: number
  y: number
  tex: HTMLCanvasElement
  colOrd: number
  row: number
  /** logo 材质索引（-1 = 非 logo 格）；texLogo 是翻新后的彩砖纹理（同上，null = 非 logo）。 */
  lmat: number
  texLogo: HTMLCanvasElement | null
  /** logo 点亮起始偏移（0..1，乘 LOGO_SPAN 得 ms）——按到区中心距离 + 抖动。 */
  loft: number
}

let cv: HTMLCanvasElement | null = null
let cx: CanvasRenderingContext2D | null = null
/** 代次：新的调用让旧的那次立刻作废（连它挂着的 swap、resize 监听一起取消）。 */
let gen = 0
let raf = 0
let tiles: Tile[] = []
let cols = 9
let rows = 9
let cell = 50
let x0 = 0
let y0 = 0
/** 在跑的那一轮挂的 resize 监听摘钩（换新轮 / 散场收尾时必须摘，不然旧几何会反打回来）。 */
let unlisten: (() => void) | null = null

/** 固定种子伪随机：每次拼出来的花纹都一样，不会「图案在跳」。 */
function rndOf(seed: number) {
  let s = seed >>> 0
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return s / 4294967296
  }
}

function hash(x: number, y: number, s: number) {
  const h = Math.sin(x * 127.1 + y * 311.7 + s * 74.7) * 43758.5453
  return h - Math.floor(h)
}

function shade(hex: string, amt: number) {
  const n = parseInt(hex.slice(1), 16)
  const c = (v: number) => Math.max(0, Math.min(255, Math.round(v + amt * 255)))
  return `rgb(${c((n >> 16) & 255)},${c((n >> 8) & 255)},${c(n & 255)})`
}

/** logo 图案：把 16×16 的 IconBlock 抽稀到 ln×ln 的砖格。
 *  格 (ii,jj) 覆盖源像素块 [ii·s,(ii+1)·s)×[jj·s,(jj+1)·s)（s = 16/ln）：
 *  ① 点缀优先 —— 2×2 的彩色小块只要沾到就赢（丢了点缀 logo 就不像了）；
 *  ② 否则取覆盖最多的底色（草/土边界因此落在最近的像素行上，是平的，不参差）。 */
function logoMatAt(ii: number, jj: number, ln: number): number {
  const s = 16 / ln
  const x0 = Math.floor(ii * s)
  const x1 = Math.min(16, Math.ceil((ii + 1) * s))
  const y0 = Math.floor(jj * s)
  const y1 = Math.min(16, Math.ceil((jj + 1) * s))
  const cnt = [0, 0, 0, 0]
  for (let y = y0; y < y1; y++) {
    for (let x = x0; x < x1; x++) cnt[LOGO_ICON[y * 16 + x]!]!++
  }
  if (cnt[0]) return 0 // 亮绿点缀
  if (cnt[2]) return 2 // 土棕点缀
  return cnt[1]! >= cnt[3]! ? 1 : 3 // 底色：草 / 土
}

/** logo 点亮次序：按到区中心的距离从内向外扩散（0..1），±小抖动让边缘不齐成一圈死边。 */
function logoDelayAt(ii: number, jj: number, ln: number): number {
  const c = (ln - 1) / 2
  const d = Math.hypot(ii - c, jj - c) / Math.max(0.5, c * Math.SQRT2)
  const jit = (hash(ii, jj, 71) - 0.5) * 0.12
  return Math.max(0, Math.min(1, d + jit))
}

/** 预渲染 16×16 的方块纹理（每材质 3 个变体）。放大到格子尺寸时保持像素风。
 *  mats 参数化：石阵传 MAT，logo 彩砖传 LOGO_MAT —— 同一套噪声管线。 */
function makeTex(px: number, mats: typeof MAT = MAT): HTMLCanvasElement[][] {
  const out: HTMLCanvasElement[][] = []
  for (let mi = 0; mi < mats.length; mi++) {
    const variant: HTMLCanvasElement[] = []
    for (let vi = 0; vi < 3; vi++) {
      const c = document.createElement('canvas')
      c.width = px
      c.height = px
      const g = c.getContext('2d')
      if (!g) continue
      const m = mats[mi]!
      // 基准尺寸按 16px 设计，按 px 缩放绘制
      const u = px / 16
      g.fillStyle = m.base
      g.fillRect(0, 0, px, px)
      const R = rndOf(mi * 977 + vi * 131 + 7)
      const dot = (xx: number, yy: number, w: number, h: number, style: string) => {
        g.fillStyle = style
        g.fillRect(Math.round(xx * u), Math.round(yy * u), Math.max(1, Math.round(w * u)), Math.max(1, Math.round(h * u)))
      }
      if (m.mode === 'plain') {
        for (let k = 0; k < 5; k++) dot(R() * 16, R() * 16, 1, 1, shade(m.base, (R() < 0.5 ? 1 : -1) * (0.02 + R() * 0.03)))
      } else if (m.mode === 'speck') {
        for (let k = 0; k < 34; k++) dot(R() * 16, R() * 16, R() < 0.25 ? 2 : 1, 1, shade(m.base, R() < 0.5 ? 0.05 + R() * 0.07 : -(0.05 + R() * 0.07)))
      } else if (m.mode === 'cobble') {
        for (let k = 0; k < 7; k++) dot(R() * 15, R() * 15, 2 + ((R() * 3) | 0), 2 + ((R() * 2) | 0), shade(m.base, -(0.1 + R() * 0.08)))
        for (let k = 0; k < 10; k++) dot(R() * 16, R() * 16, 2, 1, shade(m.base, 0.07 + R() * 0.08))
        for (let k = 0; k < 16; k++) dot(R() * 16, R() * 16, 1, 1, shade(m.base, R() < 0.5 ? 0.04 : -0.05))
      } else {
        let xx = 0
        while (xx < 16) {
          const w = 1 + ((R() * 3) | 0)
          g.fillStyle = shade(m.base, (R() - 0.5) * 0.13)
          g.fillRect(Math.round(xx * u), 0, Math.max(1, Math.round(w * u)), px)
          xx += w
        }
        for (let k = 0; k < 12; k++) dot(R() * 16, R() * 16, 1, 1, shade(m.base, R() < 0.5 ? 0.05 : -0.06))
      }
      // 斜面：上/左提亮、下/右压暗 —— 3px 宽（按 16px 纹理放大后的比例）
      g.fillStyle = 'rgba(255,255,255,0.16)'
      g.fillRect(0, 0, px, Math.max(1, Math.round(u)))
      g.fillRect(0, 0, Math.max(1, Math.round(u)), px)
      g.fillStyle = 'rgba(0,0,0,0.20)'
      g.fillRect(0, px - Math.max(1, Math.round(u)), px, Math.max(1, Math.round(u)))
      g.fillRect(px - Math.max(1, Math.round(u)), 0, Math.max(1, Math.round(u)), px)
      variant.push(c)
    }
    out.push(variant)
  }
  return out
}

function layer(): HTMLCanvasElement | null {
  if (cv) return cv
  const c = document.createElement('canvas')
  c.setAttribute('aria-hidden', 'true')
  // z-index 70：压过侧栏抽屉/吸顶条，低于对话框(80)与右键菜单(95) —— 同 blockReveal
  c.style.cssText = 'position:fixed;left:0;top:0;z-index:70;pointer-events:none;visibility:hidden'
  document.body.appendChild(c)
  const g = c.getContext('2d')
  if (!g) return null
  g.imageSmoothingEnabled = false
  cv = c
  cx = g
  return c
}

export type StoneBuildOpts = {
  /** 拼满之后才执行的真正切换。**只有它会改状态** —— 别在外面再改一遍。
   *  （当加载幕用时给空函数：底下界面从第一帧就在，揭幕不靠 swap。） */
  swap: () => void
  /** 速度倍率：1 = 标准（全程约 2.35s）；1.4 更快，0.8 更慢。 */
  speed?: number
  /** 拼满之后、swap 之前的定格拍子（ms）。 */
  hold?: number
  /** 加载幕：整个视口铺一层底色幕布（在槽位底板下面），散场期和底板一起淡出揭幕。 */
  cover?: boolean
}

/** 拼石头铺满整屏 → 拼满换内容 → 按序散场。 */
export function stoneBuild(o: StoneBuildOpts) {
  if (typeof document === 'undefined' || stillMotion()) {
    o.swap()
    return
  }
  if (!layer() || !cx) {
    o.swap()
    return
  }
  const c = cv!
  const g = cx

  // 上一轮还在跑：先摘掉它的 resize 监听（它的几何已经过期，再让它重排就是反打）
  unlisten?.()
  unlisten = null

  let vw = 0
  let vh = 0
  let dpr = 0
  let tex: HTMLCanvasElement[][] = makeTex(16) // 占位，fit() 立刻按真实尺寸重建
  let texLogo: HTMLCanvasElement[][] = makeTex(16, LOGO_MAT) // logo 彩砖纹理，同上
  let colOrd: number[] = []
  /** 节奏间隔（fit 里按行列数重算；总和 = 固定预算，屏幕/尺寸无关）。 */
  let colGap = 0
  let rowGap = 0
  let vColGap = 0
  let vRowGap = 0

  const texOf = (arr: HTMLCanvasElement[][], i: number, j: number) => {
    const v = hash(Math.floor(i / 2), Math.floor(j / 2), 7) * 0.62 + hash(i, j, 13) * 0.38
    const mi = v < 0.34 ? 0 : v < 0.62 ? 1 : v < 0.84 ? 2 : 3
    return arr[mi]![(hash(i, j, 31) * 3) | 0]!
  }

  /** 视口变了就重排：画布尺寸、格子边长、行列数（铺满）、列序、每一块的落点，
   *  以及把固定节奏预算重新分给新的行列数 —— 一次到位。
   *  ⚠ 重设 canvas 尺寸会清画布＋重置变换 —— 下一帧 draw 本来就是全量重画，不用补。 */
  const fit = (force: boolean) => {
    const vw2 = window.innerWidth
    const vh2 = window.innerHeight
    const dpr2 = Math.round((window.devicePixelRatio || 1) * 100) / 100
    if (!force && vw2 === vw && vh2 === vh && dpr2 === dpr) return
    vw = vw2
    vh = vh2
    dpr = dpr2
    cell = Math.max(CELL_MIN, Math.min(CELL_MAX, Math.floor((Math.min(vw, vh) * SPAN) / DENSITY)))
    // 铺满：行列数按视口推导（石阵 ≥ 视口，居中裁切一点点边）
    cols = Math.max(1, Math.ceil(vw / cell))
    rows = Math.max(1, Math.ceil(vh / cell))
    x0 = Math.round((vw - cols * cell) / 2)
    y0 = Math.round((vh - rows * cell) / 2)
    c.width = Math.round(vw * dpr)
    c.height = Math.round(vh * dpr)
    c.style.width = `${vw}px`
    c.style.height = `${vh}px`
    g.setTransform(dpr, 0, 0, dpr, 0, 0)
    g.imageSmoothingEnabled = false
    // 纹理像素 = 格子设备像素，16→放大整数倍最干净
    tex = makeTex(Math.max(16, Math.round(cell * dpr)))
    texLogo = makeTex(Math.max(16, Math.round(cell * dpr)), LOGO_MAT)
    // 列序：左右两边交替、一列一列拼到正中
    const colSeq: number[] = []
    {
      let l = 0
      let r = cols - 1
      while (l <= r) {
        colSeq.push(l)
        if (r !== l) colSeq.push(r)
        l++
        r--
      }
    }
    colOrd = new Array<number>(cols)
    colSeq.forEach((col, k) => {
      colOrd[col] = k
    })
    // 逐块重建（格数随视口变，不能复用清单）
    tiles = []
    // logo 区：中央 ln×ln 格（小屏放不下就缩到最短边）；图案 / 距离全按归一化算
    const ln = Math.min(LOGO_N, cols, rows)
    const li0 = Math.floor((cols - ln) / 2)
    const lj0 = Math.floor((rows - ln) / 2)
    for (let j = 0; j < rows; j++) {
      for (let i = 0; i < cols; i++) {
        let lmat = -1
        let loft = 0
        if (i >= li0 && i < li0 + ln && j >= lj0 && j < lj0 + ln) {
          lmat = logoMatAt(i - li0, j - lj0, ln)
          loft = logoDelayAt(i - li0, j - lj0, ln)
        }
        tiles.push({
          i,
          x: x0 + i * cell,
          y: y0 + j * cell,
          tex: texOf(tex, i, j),
          // ⚠ 不能走 texOf：那个函数自己 hash 选材质 —— 会把 lmat 图案整个无视掉，
          //   拼出来就是随机马赛克（实测踩过）。这里按 lmat 精确取色，只让变体散列。
          texLogo: lmat >= 0 ? texLogo[lmat]![(hash(i, j, 31) * 3) | 0]! : null,
          lmat,
          loft,
          colOrd: colOrd[i]!,
          row: j,
        })
      }
    }
    // 节奏分配：固定预算 ÷ 新行列数（列间隔×列数 + 行间隔×行数 = 常数）。
    // 行间隔以「列内逐个落下」为准（有上限），剩余全给列间 —— 两侧向中间的推进感保住。
    rowGap = Math.min(ROW_GAP0, (B_ASM * 0.35) / Math.max(1, rows - 1))
    colGap = (B_ASM - rowGap * (rows - 1)) / Math.max(1, cols - 1)
    vRowGap = Math.min(VG_ROW0, (B_VAN * 0.35) / Math.max(1, rows - 1))
    vColGap = (B_VAN - vRowGap * (rows - 1)) / Math.max(1, cols - 1)
  }

  // ── 时间线（speed 等比缩放）──────────────────────────────────────
  const speed = o.speed && o.speed > 0 ? o.speed : 1
  const hold = o.hold ?? 480
  const S = (ms: number) => Math.max(24, Math.round(ms / speed))
  const T_PLACE = S(240)
  /** 原 9×9 版的列间/列内间隔 —— 作为「固定预算」的基准，不再直接使用。 */
  const COL_GAP0 = S(105)
  const ROW_GAP0 = S(17)
  /** 装配节奏总预算：列间隔×(9-1) + 行间隔×(9-1)（≡ 原版 T_FULL 的错峰部分）。 */
  const B_ASM = (DENSITY - 1) * COL_GAP0 + (DENSITY - 1) * ROW_GAP0
  const FALL = S(190)
  const SQUASH = S(85)
  const T_FULL = T_PLACE + B_ASM + FALL
  /** logo 段：稍息 → 逐格翻新（扩散窗口）→ 单格交叉收尾。 */
  const LOGO_DELAY = S(120)
  const LOGO_SPAN = S(380)
  const LOGO_FADE = S(150)
  const T_LOGO0 = T_FULL + LOGO_DELAY
  const T_SWAP = T_LOGO0 + LOGO_SPAN + LOGO_FADE + hold
  const T_V0 = T_SWAP + S(40)
  /** 散场节奏基准与预算（同理，原 9×9 的口径）。 */
  const VG_COL0 = S(38)
  const VG_ROW0 = S(7)
  const B_VAN = (DENSITY - 1) * VG_COL0 + (DENSITY - 1) * VG_ROW0
  const V_DUR = S(170)
  const T_END = T_V0 + B_VAN + V_DUR
  const FLASH = S(220)

  const tStartOf = (t: Tile) => T_PLACE + t.colOrd * colGap + t.row * rowGap
  const tVanishOf = (t: Tile) => T_V0 + t.colOrd * vColGap + t.row * vRowGap
  const fallDistOf = (j: number) => cell * 3.8 + j * cell * 0.28

  fit(true)
  const onRsz = () => fit(false)
  window.addEventListener('resize', onRsz)
  unlisten = () => window.removeEventListener('resize', onRsz)

  const draw = (t: number) => {
    g.clearRect(0, 0, vw, vh)
    // 散场期的整体退场透明度：1 → 0（smoothstep），到 T_END 正好透干净。
    // 幕布与槽位底板都乘它；石头自己按各自时序散（不乘）。
    let exitA = 1
    if (t >= T_V0) {
      const p = Math.min(1, (t - T_V0) / (T_END - T_V0))
      exitA = 1 - p * p * (3 - 2 * p)
    }
    // 加载幕：铺满整个视口，盖住底下「正在装」的界面（#080b0e = --color-base）
    if (o.cover && exitA > 0) {
      g.globalAlpha = exitA
      g.fillStyle = '#080b0e'
      g.fillRect(0, 0, vw, vh)
      g.globalAlpha = 1
    }
    // 槽位底板（空着的格子也要看得见 —— 目标区域先立在那儿）
    g.globalAlpha = exitA
    g.fillStyle = '#0a0c0f'
    g.fillRect(x0 - 2, y0 - 2, cols * cell + 4, rows * cell + 4)
    for (let j = 0; j < rows; j++) {
      for (let i = 0; i < cols; i++) {
        const sx = x0 + i * cell
        const sy = y0 + j * cell
        g.fillStyle = '#15181d'
        g.fillRect(sx + 1, sy + 1, cell - 2, cell - 2)
        g.fillStyle = 'rgba(0,0,0,0.4)'
        g.fillRect(sx + 1, sy + 1, cell - 2, Math.max(2, Math.round(cell * 0.06)))
      }
    }
    g.globalAlpha = 1
    // 拼满时整片闪一下
    let flashA = 0
    if (t >= T_FULL && t < T_FULL + FLASH) {
      flashA = 0.22 * Math.sin(Math.PI * ((t - T_FULL) / FLASH))
    }
    for (const tl of tiles) {
      const ts = tStartOf(tl)
      if (t < ts) continue
      let yoff = 0
      let alpha = 1
      let squash = 1
      let f = flashA
      if (t < ts + FALL) {
        const p = (t - ts) / FALL
        yoff = -fallDistOf(tl.row) * (1 - p * p) // 重力加速（ease-in）
      } else {
        const tq = t - (ts + FALL)
        if (tq < SQUASH) squash = 1 - 0.13 * Math.sin((Math.PI * tq) / SQUASH)
      }
      const tv = tVanishOf(tl)
      if (t >= tv) {
        const p2 = (t - tv) / V_DUR
        if (p2 >= 1) continue
        yoff += -cell * 0.18 * p2
        alpha = 1 - p2 * p2
        // 散场时先闪白一瞬再没（「直接消失」的干脆感）
        f = Math.max(f, p2 < 0.3 ? 1 - p2 / 0.3 : 0)
        squash = 1
      }
      const w = cell * (1 + (1 - squash) * 0.34)
      const h = cell * squash
      const dx = Math.round(tl.x + (cell - w) / 2)
      const dy = Math.round(tl.y + cell - 1 - h + yoff)
      // ── logo 翻新：到点儿后灰砖交叉淡出，彩砖从 1.16 缩回 1「崩」出来 ──
      let logoP = 0
      if (tl.texLogo) {
        const tl0 = T_LOGO0 + tl.loft * LOGO_SPAN
        if (t >= tl0) logoP = Math.min(1, (t - tl0) / LOGO_FADE)
      }
      if (logoP < 1) {
        g.globalAlpha = alpha * (1 - logoP)
        g.drawImage(tl.tex, dx, dy, Math.round(w), Math.round(h))
      }
      if (logoP > 0 && tl.texLogo) {
        const sc = 1 + 0.16 * Math.pow(1 - logoP, 1.4)
        const cw = Math.round(w * sc)
        const ch = Math.round(h * sc)
        const cdx = Math.round(tl.x + (cell - cw) / 2)
        const cdy = Math.round(tl.y + (cell - ch) / 2 + yoff)
        g.globalAlpha = alpha * logoP
        g.drawImage(tl.texLogo, cdx, cdy, cw, ch)
        // 翻新瞬间短促白闪（「咔」的一下）
        const lf = 0.85 * Math.pow(1 - logoP, 2)
        if (lf > 0.02) {
          g.fillStyle = `rgba(255,255,255,${lf.toFixed(3)})`
          g.fillRect(cdx, cdy, cw, ch)
        }
      }
      if (f > 0.02) {
        g.fillStyle = `rgba(255,255,255,${f.toFixed(3)})`
        g.fillRect(dx, dy, Math.round(w), Math.round(h))
      }
      g.globalAlpha = 1
    }
    // 落地扬尘（确定性：位置由「谁的、什么时候落的地」派生，回放也一致）
    for (const tl of tiles) {
      const tLand = tStartOf(tl) + FALL
      const pt = t - tLand
      if (pt < 0 || pt > S(300)) continue
      for (let k = 0; k < 5; k++) {
        const sd = tl.row * 131 + tl.colOrd * 17 + k * 7
        const dir = hash(sd, 0, 3) < 0.5 ? -1 : 1
        const vx = dir * cell * (0.0014 + hash(sd, 1, 5) * 0.003)
        const vy = -cell * (0.0008 + hash(sd, 2, 9) * 0.002)
        const px2 = tl.x + cell / 2 + vx * pt
        const py2 = tl.y + cell - cell * 0.08 + vy * pt + cell * 0.000011 * pt * pt
        const pa = 1 - pt / S(300)
        g.fillStyle = `rgba(160,160,160,${(pa * 0.55).toFixed(3)})`
        const ps = pt < S(160) ? Math.max(2, Math.round(cell * 0.06)) : Math.max(2, Math.round(cell * 0.04))
        g.fillRect(Math.round(px2), Math.round(py2), ps, ps)
      }
    }
  }

  gen++
  const me = gen
  if (raf) cancelAnimationFrame(raf)
  c.style.visibility = 'visible'
  const t0 = performance.now()
  let swapped = false
  const doSwap = () => {
    if (swapped) return
    swapped = true
    o.swap()
  }
  // 兜底：切后台时 rAF 停摆，但换内容不能跟着停
  const guard = window.setTimeout(() => {
    if (me === gen) doSwap()
  }, T_SWAP + 250)

  const step = (now: number) => {
    if (me !== gen) return
    const t = now - t0
    if (!swapped && t >= T_SWAP) doSwap()
    if (t >= T_END) {
      clearTimeout(guard)
      unlisten?.()
      unlisten = null
      g.clearRect(0, 0, vw, vh)
      c.style.visibility = 'hidden'
      raf = 0
      return
    }
    draw(t)
    raf = requestAnimationFrame(step)
  }
  raf = requestAnimationFrame(step)
}
