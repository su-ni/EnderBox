/** 换页 / 换视图的转场：方块铺展（深板岩石头材质）。
 *
 *  ── 为什么是一块 canvas，而不是几百个 div ─────────────────────────
 *  ① 一次铺满要 500～1000 个方块。这台面板要跑在家里的闲置电脑上，
 *     DOM + CSS 动画那点合成开销，弱机上就是「切页掉帧」；
 *  ② 逐帧自己画，时序完全由这份文件说了算 —— 不受 CSS @layer 优先级影响
 *     （design.css 是冻结层，新样式一律要跟它斗优先级，这里一条 CSS 都不写）；
 *  ③ 方块底色/纹理的随机抖动，在 canvas 里是三行代码，在 DOM 里是几百个 inline style。
 *
 *  ── 节奏（用户审过的三拍）───────────────────────────────────────
 *     铺满 250ms  →  换页（调用方给的 swap）  →  从原点向外退去 320ms
 *  换页排在铺满之后 30ms：退去动画前 30% 只在放大、方块之间还不留缝，
 *  真正开始露出新页面还要再往后 70ms —— React 重渲染完全来得及，看不到旧内容。
 *  也就是说：**页面的状态切换是延后的**，延迟被这层方块吃掉了，不表现为卡顿。
 *
 *  ⚠ 全部状态都在模块里，绝不进 React state（沿用星图那条规矩）。
 *  ⚠ 只画 transform（缩放）+ alpha，不碰任何布局属性。
 *  ⚠ body.no-anim / prefers-reduced-motion 一律直接换页，不留一段「点了没反应」的空白。
 */

/** 方块边长（CSS px），基准值。太小 → 方块数爆炸；太大 → 看不出「铺展」。 */
const TILE = 30
/** 一帧最多画多少个方块。每块一次 drawImage，软渲染下 8000 块一帧就是 60ms 起。
 *  屏幕越大，方块按面积等比放大（大屏上块头更大是合理的），块数封到这。 */
const MAX_TILES = 900
/** 当前这套网格实际用的边长。build 时按面积算出来，draw 直接读。 */
let size = TILE
/** 单个方块淡入的时长，以及最远的那颗比最近的那颗晚多久开始（按距离归一化到 0..1）。 */
const IN_MS = 110
const IN_STAG = 140
/** 退去：单块时长 + 距离错峰。比铺满慢一点，收尾才不显得「啪」地消失。 */
const OUT_MS = 200
const OUT_STAG = 120
/** 铺满与退去之间的静默拍子（留给 React 换页）。 */
const GAP_MS = 30

const COVER_END = IN_MS + IN_STAG
const OUT_START = COVER_END + GAP_MS
const TOTAL = OUT_START + OUT_MS + OUT_STAG + 60

/** 深板岩四档底色。
 *  ⚠ 别调亮 —— 石头用 MC 那种亮灰（#7f7f7f）的话，每切一次页整屏闪一次白，
 *     比现在这个「渐缓渐入」还难受。纹理留着，亮度压进面板的暗色里。 */
const TONES = ['#333a42', '#2c323a', '#3a424b', '#282e35']

type Tile = { x: number; y: number; d: number; tone: number }

let cv: HTMLCanvasElement | null = null
let cx: CanvasRenderingContext2D | null = null
let tiles: Tile[] = []
/** 区域尺寸指纹：变了（换窗口大小 / 换视图）就重建方块网格。 */
let geo = ''
let raf = 0
/** 代次：新的转场调用会让旧的那次立刻作废（连它挂着的换页一起取消）。 */
let gen = 0
let stone: HTMLCanvasElement[] = []

/** 固定种子的伪随机：每次铺出来的石头花纹都一样，切页才不会「图案在跳」。 */
function rnd(seed: number) {
  let s = seed >>> 0
  return () => {
    s = (s * 1664525 + 1013904223) >>> 0
    return s / 4294967296
  }
}

/** 预渲染四档石头方块。噪点用「一个方块切十格」的粗粒 ——
 *  细噪点缩起来看不清，还会在缩放时闪成一片。 */
function makeStone(dpr: number, tile: number) {
  const S = Math.round(tile * dpr) // 贴图边长（设备像素）
  const px = Math.max(2, Math.round(S / 10))
  const out: HTMLCanvasElement[] = []
  for (let i = 0; i < TONES.length; i++) {
    const c = document.createElement('canvas')
    c.width = S
    c.height = S
    const g = c.getContext('2d')
    if (!g) continue
    g.fillStyle = TONES[i] ?? TONES[0]!
    g.fillRect(0, 0, S, S)
    const R = rnd(9781 + i * 613)
    for (let y = 0; y < S; y += px) {
      for (let x = 0; x < S; x += px) {
        const v = R()
        if (v < 0.11) g.fillStyle = 'rgba(0,0,0,.20)'
        else if (v < 0.22) g.fillStyle = 'rgba(255,255,255,.055)'
        else continue
        g.fillRect(x, y, px, px)
      }
    }
    out.push(c)
  }
  return out
}

function layer() {
  if (cv) return cv
  const c = document.createElement('canvas')
  c.setAttribute('aria-hidden', 'true')
  // z-index 70：压过侧栏抽屉(55)、吸顶条(40/60)，但低于对话框(80)和右键菜单(95)
  // —— 转场不该盖住一个正在开的确认框。
  c.style.cssText = 'position:fixed;left:0;top:0;z-index:70;pointer-events:none;visibility:hidden'
  document.body.appendChild(c)
  const g = c.getContext('2d')
  if (!g) return null
  // 像素风：不要让浏览器把石头纹理插值糊掉
  g.imageSmoothingEnabled = false
  cv = c
  cx = g
  return c
}

/** 取一组元素的并集矩形，再裁进视口。滚过页的时候，只盖「看得见的那块」。 */
function areaOf(els: HTMLElement[]) {
  let l = 1e9
  let t = 1e9
  let r = -1e9
  let b = -1e9
  let any = false
  for (const e of els) {
    const q = e.getBoundingClientRect()
    if (q.width < 2 || q.height < 2) continue
    any = true
    l = Math.min(l, q.left)
    t = Math.min(t, q.top)
    r = Math.max(r, q.right)
    b = Math.max(b, q.bottom)
  }
  if (!any) return null
  l = Math.max(0, l)
  t = Math.max(0, t)
  r = Math.min(window.innerWidth, r)
  b = Math.min(window.innerHeight, b)
  if (r - l < 16 || b - t < 16) return null
  return { l, t, w: r - l, h: b - t }
}

const ease = (u: number) => 1 - Math.pow(1 - u, 3)

/** 动效开关：关着（body.no-anim）或系统要求减少动效时为 true。
 *  导出给「承接式切页」（App.tsx 的 switchToPage）用同一套判据 ——
 *  两边各写一份的话，迟早出现「一个还在动、一个已经不动」的精神分裂。 */
export function stillMotion() {
  if (document.body.classList.contains('no-anim')) return true
  return typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
}

function draw(t: number, w: number, h: number) {
  if (!cx) return
  cx.clearRect(0, 0, w, h)
  for (let i = 0; i < tiles.length; i++) {
    const tl = tiles[i]!
    const lt = t - tl.d * IN_STAG
    if (lt < 0) continue
    let al = 1
    let s = 1
    if (lt < IN_MS) {
      const u = ease(lt / IN_MS)
      al = u
      s = 0.55 + 0.45 * u
    } else {
      const ot = t - OUT_START - tl.d * OUT_STAG
      if (ot >= 0) {
        if (ot >= OUT_MS) continue
        const u = ease(ot / OUT_MS)
        if (u < 0.3) {
          // 先轻轻鼓一下再塌掉 —— 和体检那六颗心、按钮附魔是同一套手感
          s = 1 + 0.07 * (u / 0.3)
        } else {
          const v = (u - 0.3) / 0.7
          al = 1 - v
          s = 1.07 * (1 - v)
        }
      }
    }
    if (al <= 0.006 || s <= 0.02) continue
    const img = stone[tl.tone]
    if (!img) continue
    const side = size * s
    const off = (size - side) / 2
    cx.globalAlpha = al
    cx.drawImage(img, tl.x + off, tl.y + off, side, side)
  }
  cx.globalAlpha = 1
}

export type RevealOpts = {
  /** 要被方块盖住的元素（取并集）。传进来的元素是「这次切换真正会变的那一块」。 */
  els: (HTMLElement | null | undefined)[]
  /** 扩散原点，一般是刚被点的那颗页签/按钮。不给就从区域中心爆开。 */
  originEl?: HTMLElement | null
  /** 铺满之后才执行的真正切换。**只有它会改状态** —— 别在外面再改一遍。 */
  swap: () => void
}

/** 铺一层石头方块 → 盖满了换页 → 从原点向外退去。 */
export function blockReveal(o: RevealOpts) {
  const els = o.els.filter(Boolean) as HTMLElement[]
  const a = els.length && !stillMotion() ? areaOf(els) : null
  // 量不到区域（元素还没挂上 / 被藏了）或动效被关：直接切，别让人等
  if (!a || !layer() || !cx) {
    o.swap()
    return
  }
  const c = cv!
  const g = cx

  const dpr = Math.round((window.devicePixelRatio || 1) * 100) / 100
  const key = `${a.l}|${a.t}|${a.w}|${a.h}|${dpr}`
  if (key !== geo) {
    geo = key
    c.width = Math.round(a.w * dpr)
    c.height = Math.round(a.h * dpr)
    c.style.left = `${a.l}px`
    c.style.top = `${a.t}px`
    c.style.width = `${a.w}px`
    c.style.height = `${a.h}px`
    g.setTransform(dpr, 0, 0, dpr, 0, 0)
    g.imageSmoothingEnabled = false
    // 屏幕越大方块越大（按面积等比），但块数封顶 —— 4K 屏按 30px 铺要 8000+ 块
    size = Math.max(TILE, Math.min(96, Math.round(Math.sqrt((a.w * a.h) / MAX_TILES))))
    stone = makeStone(dpr, size)
    const R = rnd(4211)
    const cols = Math.ceil(a.w / size)
    const rows = Math.ceil(a.h / size)
    tiles = []
    for (let r = 0; r < rows; r++) {
      for (let k = 0; k < cols; k++) tiles.push({ x: k * size, y: r * size, d: 0, tone: (R() * 4) | 0 })
    }
  }
  if (!stone.length || !tiles.length) {
    o.swap()
    return
  }

  // 原点：优先用被点的那个元素（从页签向外扩散），拿不到就中心开花
  let ox = a.w / 2
  let oy = a.h / 2
  const q = o.originEl?.getBoundingClientRect()
  if (q && q.width > 0) {
    ox = q.left + q.width / 2 - a.l
    oy = q.top + q.height / 2 - a.t
  }
  ox = Math.min(Math.max(ox, 0), a.w)
  oy = Math.min(Math.max(oy, 0), a.h)

  // 距离归一化到 0..1 —— 这样不管窗口多宽，总时长都是定值（否则 4K 屏上要等半秒才铺满）
  let far = 1
  for (const tl of tiles) {
    const dx = tl.x + size / 2 - ox
    const dy = tl.y + size / 2 - oy
    tl.d = Math.hypot(dx, dy)
    if (tl.d > far) far = tl.d
  }
  for (const tl of tiles) tl.d /= far

  gen++
  const me = gen
  if (raf) cancelAnimationFrame(raf)
  c.style.visibility = 'visible'
  const w = a.w
  const h = a.h
  const t0 = performance.now()
  let swapped = false
  const doSwap = () => {
    if (swapped) return
    swapped = true
    o.swap()
  }
  // 兜底：这个标签页切到后台时 rAF 会停摆，但换页不能跟着停
  const guard = window.setTimeout(() => {
    if (me === gen) doSwap()
  }, COVER_END + 140)

  const step = (now: number) => {
    if (me !== gen) return
    const t = now - t0
    if (!swapped && t >= COVER_END) doSwap()
    if (t >= TOTAL) {
      clearTimeout(guard)
      g.clearRect(0, 0, w, h)
      c.style.visibility = 'hidden'
      raf = 0
      return
    }
    draw(t, w, h)
    raf = requestAnimationFrame(step)
  }
  raf = requestAnimationFrame(step)
}
