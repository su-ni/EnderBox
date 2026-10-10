import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { McmodNode, McmodRelations } from '../api'

/**
 * MC 百科关系星图 —— 可旋转的伪 3D 力导向图。
 *
 * ▍形态来源
 *
 * 用户原话「去看 mc 百科的是怎么做的」，所以**照抄了站方的画图参数**，不是拍脑袋。
 * 扒到它画图的 JS（`/static/public/js/class/mc.class.functions.js`）里那句配置是：
 *
 *   series: [{ type:"graph", layout:"force", roam:true, draggable:true, symbol:"circle",
 *              scaleLimit:{min:.5,max:1.5},
 *              edgeSymbol:["none","arrow"], edgeSymbolSize:[0,10], edgeSymbolOpacity:.75,
 *              label:{position:"right", formatter:"{b}", fontSize:12},
 *              lineStyle:{width:2, color:"source", curveness:0, opacity:.75},
 *              emphasis:{lineStyle:{width:5}},
 *              force:{repulsion:900, edgeLength:200, gravity:.1, friction:.2} }]
 *
 * 即：**圆圈节点 + 名字放右侧 + 带箭头的边 + 力导向布局**。
 * 不用 echarts（面板其余都是手写 SVG，为一张图拖 600KB 库不划算），
 * 用等价的力模型自己在画布尺寸里跑固定步数 —— 结果稳定、可测、无运行时依赖。
 *
 * ▍性能红线（这版重写的主因）
 *
 * 上一版把「呼吸时钟 t」放进 React state，导致 `t` 每变一次就重建 302 个点的
 * 排序数组 + 重排整个 SVG DOM，**每帧 60 次全量重渲染** —— 症状就是用户报的
 * 「鼠标移入移出闪屏」＋「滚轮缩放卡卡的」。鼠标悬停又额外触发一次 state 变化，
 * 叠加到那个循环上，抖动肉眼可见。
 *
 * 这版把「动画」和「React 渲染」彻底分家：
 *   · React 只负责**结构**（节点有哪些、边有哪些、卡里写什么）—— 低频。
 *   · 每帧的旋转/呼吸由**一个 rAF 循环直接改 DOM 属性**（transform / r / opacity）—— 不经 React。
 * 这样帧率与 React 渲染次数解耦，悬停、缩放、点选都不再引起全图重排。
 *
 * ▍交互红线
 *
 * **滚轮默认缩放**（用户明确要「滚轮放大」）。星图是画布不是文档流：
 * 地图、PDF、echarts 的 roam:true 都是滚轮缩放，站方也是。所以这里滚轮 = 缩放，
 * 并用 preventDefault 拦住页面滚动 —— **仅当指针在画布内**才拦，出画布照常滚页面。
 * 触屏仍走 pan-y 让纵向滚动穿透（见 .mcg-canvas 那条注释，那条别动）。
 */

/** 节点半径：站方 symbolSize 是直径，减半给 SVG 的 r，再夹到一个能看见的区间。 */
function nodeRadius(n: McmodNode): number {
  const s = n.size && n.size > 0 ? n.size : 20
  return Math.max(5, Math.min(18, s / 2))
}

type P = { node: McmodNode; x: number; y: number; r: number }

/** 确定性哈希的小数版（0~1）。第二参是「盐」，让同一个 id 能取到多个互不相关的值。 */
function hash01(id: string, salt: number): number {
  /*
    ⚠ 用完整的 32 位，**不要写 `% 10000`** —— 取模会把分布压出可测的偏心，
    而这里要拿它做「几成贴旋臂 / 几成撒在盘上」的比例判断，偏一点形状就散了。
  */
  return hashOf(id + '#' + salt) / 4294967296
}

/*
  ▍旋涡星系布局（用户：「我要的是星系形的」，形态确认＝「旋涡星系式」）

  为什么**弃用站方那套力导向**：力模型给的是各向同性的散点，再套一层球面投影
  （z = ±sqrt(1-r²)）就成了一团**球状点云** —— 中心点还会被归一化推到一侧，
  三条「轨道圈」跟点位毫无关系（等于乱画），339 条边全画出来更糊成毛球。
  用户看了直说「不是这样」。所以这里**直接构造星系的几何**，不再让力模型决定形状：

    ① 中心 = 原点 (0,0,0)，它是恒星的锚，**不许动**（画布正中永远有它）。
    ② 其余点按 `deep`（离中心几跳）落进同心**半径带**：基准半径走几何级数
       `R(d) = R_INNER * (R_OUTER/R_INNER)^((d-1)/(D-1))` → 内密外疏，像真星系盘。
       层内再均匀铺开一个带宽，让相邻层互相交叠，而不是一圈圈的同心环。
    ③ 角度：**每层占一段弧、层间首尾相接**。层内进度 frac 同时驱动半径和角度
       （`θ = arm*π + (d-1)*ARM_PHASE + frac*ARM_LEN`），于是每层是一小段螺旋、
       层与层接起来正是一条连续旋臂；`arm` 按全局序号交替 → 两条对称臂。
       另有 OFF_ARM 那一小撮不挂臂、撒在盘上当「场星」（真实星系的旋臂是密度波）。
    ④ z 是**盘厚**不是球壳：`|z| = 0.05 + 0.20*exp(-(r/0.45)²)` —— 中心鼓成核球、盘缘薄。
       符号由 id 哈希决定（确定性）。自转时是一张「有厚度的盘」在转，不是球在滚。
       ⚠ yaw ＝ 绕**竖直轴 z** 转盘、pitch ＝ 俯仰，别写反：绕 y 轴转＝盘在「翻滚」，
       小俯角时看不出来、抬到 3/4 视角就会露馅（2026-10-10 修正过一版）。

  ⚠ **归一化用「半径等比缩放」，不是 min/max 包围盒** —— 星系是圆对称结构，按最大半径
    等比缩放天然让中心留在画布正中；用包围盒映射会把中心推离正中（那是散点力导向图
    才需要的手法，别把那条经验套到这里）。
  ⚠ 抖动**必须确定性**（id 哈希），不能用 Math.random：否则每次进页面图形都不一样，
    用户会以为「数据变了」，截图回归也没法做。
  ⚠ 半径按「层内序号」铺开，不按依赖边排序：302 个点里 277 个不与中心直连，
    按边排会把层次信息打乱，而层次正是这张图要表达的东西。
*/
const R_INNER = 0.15 // 最内圈半径（同时给核球留出空地）
const R_OUTER = 1 // 最外圈半径
const ARM_PHASE = 1.9 // 每层的螺旋起点比内一层再转过这么多弧度（rad/层）
const ARM_LEN = 1.7 // 每层在自己那一段弧上铺开的长度（rad）—— 层间首尾相接就成了一条螺旋
const OFF_ARM = 0.12 // 一成二的行星不挂臂，撒在盘面上当「场星」（旋臂是密度波，不是唯一去处）

/*
  下面这几个是「层 → 半径 / 带宽 / 角度」的**唯一出处**：布局（spiralLayout）和旋臂骨架
  （effect 里那两条虚线）都调它们。参数只有一份 → 骨架必然穿过行星所在的那段弧。
  ⚠ 别在任何一处内联重写这套算法。旧版骨架用的是另一套公式（TWIST*ln(r/R_INNER)），
  与行星的角度规则对不上，画出来是「行星在一边、虚线在另一边」——那正是用户说的「乱画」。
*/
function baseROf(d: number, maxD: number): number {
  return R_INNER * Math.pow(R_OUTER / R_INNER, (d - 1) / Math.max(1, maxD - 1))
}

/** 层 d 的半径展开半宽。故意让相邻层交叠，盘才连续（不交叠就退化成一圈圈同心环）。 */
function halfBandOf(d: number, maxD: number): number {
  const rb = baseROf(d, maxD)
  const nextRb = d < maxD ? baseROf(d + 1, maxD) : rb
  const prevRb = d > 1 ? baseROf(d - 1, maxD) : R_INNER
  return Math.min(0.22, Math.max(0.06, (d < maxD ? nextRb - rb : rb - prevRb) * 0.75))
}

/** 给定半径反查它属于哪一层（骨架按半径等距取点，得知道该套哪层的角度）。 */
function layerOfRadius(r: number, maxD: number): number {
  let best = 1
  let bd = Infinity
  for (let d = 1; d <= maxD; d++) {
    const dd = Math.abs(baseROf(d, maxD) - r)
    if (dd < bd) {
      bd = dd
      best = d
    }
  }
  return best
}

/** 半径 r 处、第 arm 条臂的螺旋角 —— 与 spiralLayout 给行星用的公式**完全同源**。 */
function armThetaAt(r: number, arm: number, maxD: number): number {
  const d = layerOfRadius(r, maxD)
  const rb = baseROf(d, maxD)
  const half = halfBandOf(d, maxD)
  const frac = Math.min(1, Math.max(0, 0.5 + (r - rb) / (2 * half)))
  return arm * Math.PI + (d - 1) * ARM_PHASE + frac * ARM_LEN
}

/** 数据里实际的层数（骨架与布局都要用；放在组件里算一次，别每帧重复算）。 */
function maxDepthOf(data: McmodRelations): number {
  const cid = data.center?.id
  const ds = data.nodes
    .filter((n) => n.id !== cid)
    .map((n) => (typeof n.deep === 'number' && n.deep > 0 ? n.deep : 0))
  return Math.max(1, ...ds)
}

function spiralLayout(data: McmodRelations): Map<string, P & { z: number }> {
  const out = new Map<string, P & { z: number }>()
  const center = data.center
  const centerId = center?.id
  // 恒星要比行星显眼：半径至少 16（nodeRadius 给的下限只有 5，画出来认不出是中心）
  if (center) out.set(center.id, { node: center, x: 0, y: 0, z: 0, r: Math.max(16, nodeRadius(center)) })

  const others = data.nodes.filter((n) => n.id !== centerId)
  if (others.length === 0) return out

  const maxD = maxDepthOf(data)
  const depthOf = (n: McmodNode) => (typeof n.deep === 'number' && n.deep > 0 ? n.deep : maxD + 1)

  // 稳定排序：先按层、再按原始顺序（不能用随机 → 结果每次一致）
  const ordered = others
    .map((n, i) => ({ n, d: depthOf(n), i }))
    .sort((a, b) => a.d - b.d || a.i - b.i)

  const layerTotal = new Map<number, number>()
  for (const o of ordered) layerTotal.set(o.d, (layerTotal.get(o.d) ?? 0) + 1)
  const layerSeen = new Map<number, number>()

  ordered.forEach((o, rank) => {
    const k = layerSeen.get(o.d) ?? 0
    layerSeen.set(o.d, k + 1)
    const cnt = layerTotal.get(o.d) ?? 1
    const rb = baseROf(o.d, maxD)
    // 半径带：故意让相邻层**交叠**（见 halfBandOf），盘才是连续的一片。
    // 不交叠的话每层各自缩在一个窄环上，外圈那 167 个点会全挤在 r≈1 的一圈里。
    const half = halfBandOf(o.d, maxD)
    const frac = cnt > 1 ? k / (cnt - 1) : 0.5
    const r = Math.max(
      R_INNER * 0.8,
      Math.min(R_OUTER, rb + (frac - 0.5) * 2 * half + (hash01(o.n.id, 1) - 0.5) * 0.04),
    )
    /*
      ▍角度：**每层占一段弧，层与层首尾相接** → 连成一条连续螺旋

      ⚠⚠ 这里推翻了两版做法，别再往回改：
      · 第一版「θ 由 r 决定」（θ = arm*π + TWIST*ln(r/R_INNER)）：同一层的点半径几乎相同，
        于是全落在两条臂的**同一相位**上 → 画出来是两条又粗又短的竖弧，
        302 个点只铺了画布宽度的 26%，中心两侧各挂一条，根本不是盘。
      · 第二版「按全局序号排进度」：1 层只占全局进度的 8%，弧长也只有 8% →
        1 层那 24 个点全挤在 19° 的扇区里，糊成一坨。层次感也没了。
      正解就是现在这条：**角度用「层内进度」，半径用「层基准 + 层内展开」**。
      每层各自铺满自己那段弧（外层的弧更长 → 周长长、点也更多），
      而 `d * ARM_PHASE` 让每层的起点依次转过去 → 层与层接起来正是一条螺旋。
      ⚠ 判断用盐 3、盘面角度用盐 5：拿同一个随机数既做分支又做角度的话，
      落进 else 的那批角度会被截断在 [0.88*2π, 2π)，铺不满整圈。
    */
    const arm = rank % 2
    const theta =
      hash01(o.n.id, 3) < OFF_ARM
        ? hash01(o.n.id, 5) * Math.PI * 2
        : arm * Math.PI + (o.d - 1) * ARM_PHASE + frac * ARM_LEN + (hash01(o.n.id, 2) - 0.5) * 0.12
    const bulk = 0.05 + 0.20 * Math.exp(-((r / 0.45) ** 2))
    const z = ((hashOf(o.n.id) & 1) === 0 ? 1 : -1) * bulk
    out.set(o.n.id, { node: o.n, x: Math.cos(theta) * r, y: Math.sin(theta) * r, z, r: nodeRadius(o.n) })
  })
  return out
}

/** 归一化坐标 + 造出来的 z（见组件里 disp 的构造）。 */
type DP = P & { z: number }

/** 确定性哈希：同 id 永远同值，不依赖随机（保截图回归）。 */
function hashOf(id: string): number {
  let h = 2166136261
  for (let i = 0; i < id.length; i++) h = (h ^ id.charCodeAt(i)) * 16777619
  return h >>> 0
}

/** 每个点的呼吸相位/速度（由 id 决定，恒定）。 */
function breathOf(id: string): { ph: number; spd: number } {
  const h = hashOf(id)
  const ph = ((h % 1000) / 1000) * Math.PI * 2
  const spd = 0.0009 + (((h >>> 10) & 7) / 7) * 0.0009
  return { ph, spd }
}

export default function McmodGraph({
  data,
  onQuickInstall,
}: {
  data: McmodRelations
  /** 点了行星上的「快捷安装」：由父级去市场搜项目、弹版本层。不给就不渲染这个按钮。 */
  onQuickInstall?: (n: McmodNode) => void | Promise<void>
}) {
  // 星系几何：全量算一次（中心 + 两条旋臂），切档位只改「画不画」，点位不重算 → 图不跳
  const layout = useMemo(() => spiralLayout(data), [data])
  // 旋臂骨架要跟行星用同一套层参数，所以层数在组件层算一次带进 effect
  const layoutMaxD = useMemo(() => maxDepthOf(data), [data])

  /*
    ▍「只看前 N 层」（用户要的「拓展模组开关」的正确形态）

    用户原话「拓展模组…给个开关」，真实诉求是**这张图被 260 个拓展模组淹了，想瘦身**。
    但按分类整类隐藏会得到一张**空图** —— 扒了数据才知道：这张图是个依赖 DAG，
    全部 339 条边都有一端是拓展模组（拓展→拓展 163、前置→拓展 151、中心→拓展 25）。
    一旦折起拓展模组，剩下 41 个点会全部变成无边的孤岛，一屏散点，什么也读不出来。

    所以正确的「去杂」不是按分类砍，而是**按层级砍**：后端给了每个节点的 `deep`
    （离中心几跳）。实测 Sodium：第1层 24 个、第2层 27 个、第3层 83 个、第4层 167 个。
    「只看 1 层」= 中心 + 直接依赖 25 个点 25 条边 —— 这才是真的能一眼看清的核心结构。

    ⚠ 布局仍用全量点跑（`layout`），过滤只改「画不画」：来回切档位时点位是稳的，图不跳。
  */
  const [maxDepth, setMaxDepth] = useState<number>(0) // 0 = 全部

  // 有哪些层级（升序）。没有 deep 的节点（中心）不参与。
  const depths = useMemo(() => {
    const s = new Set<number>()
    for (const n of data.nodes) if (typeof n.deep === 'number' && n.deep > 0) s.add(n.deep)
    return [...s].sort((a, b) => a - b)
  }, [data])

  const isHidden = (n: McmodNode) => {
    if (n.id === data.center?.id) return false // 中心永远显示
    if (maxDepth <= 0) return false
    const d = typeof n.deep === 'number' && n.deep > 0 ? n.deep : 1
    return d > maxDepth
  }

  // 只画可见点；边要求两端都可见（否则会有箭头指向空气）
  const pts = useMemo(
    () => [...layout.values()].filter((p) => !isHidden(p.node)),
    [layout, maxDepth, data],
  )
  const visibleIds = useMemo(() => new Set(pts.map((p) => p.node.id)), [pts])
  const links = useMemo(
    () => data.links.filter((l) => visibleIds.has(l.source) && visibleIds.has(l.target)),
    [data.links, visibleIds],
  )
  const allShown = maxDepth <= 0

  // 分类计数：图例要显示「拓展模组 260」这种数字（后端把中心的 cat 留空，归一成「中心」）。
  const catCount = useMemo(() => {
    const m = new Map<string, number>()
    for (const n of data.nodes) {
      const key = n.id === data.center?.id ? '中心' : n.cat || '中心'
      m.set(key, (m.get(key) ?? 0) + 1)
    }
    return m
  }, [data])

  /*
    ▍可见集缩放（一次性、可用 useMemo）——把旋臂缩到刚好铺满画布

    ⚠⚠ **星系用「半径等比缩放」，不是 min/max 包围盒**。星系是圆对称结构，除以最大半径
    就是绕原点缩放 —— 中心永远留在画布正中，旋臂的形状一点不变。
    包围盒映射是给散点力导向图用的（那类图质心≠中心，不映射会只占半边画布），
    套到这里反而会把中心推离正中，旋臂也会被非对称地拉歪。
    ⚠ 顺带解决「档位切换后的铺满」：只看 1 层时可见点全在内圈，最大半径只有 ~0.23，
    等比放大到 1 之后这一层照样铺满画布（不会缩在角落成一团）。
  */
  const dispScale = useMemo(() => {
    let maxR = 1e-6
    for (const p of pts) {
      const rr = Math.hypot(p.x, p.y)
      if (rr > maxR) maxR = rr
    }
    return 1 / maxR
  }, [pts])
  const disp = useMemo(() => {
    const map = new Map<string, DP>()
    const k = dispScale
    for (const p of pts) map.set(p.node.id, { ...p, x: p.x * k, y: p.y * k, z: p.z * k })
    return map
  }, [pts, dispScale])

  /*
    ▍几何常数（画布 → 屏幕尺度）
    ⚠ 不让两轴自由拉伸：窄屏（396×720）会把 302 个点拉成一根竖条。
    以两轴中较小的可用半径为基准，允许另一轴最多铺 stretch 倍，超了就等比。
  */
  const [box, setBox] = useState({ w: 800, h: 520 })
  const wrapRef = useRef<HTMLDivElement>(null)
  const svgRef = useRef<SVGSVGElement>(null)

  const padX = box.w < 560 ? Math.max(28, box.w * 0.07) : Math.max(48, Math.min(150, box.w * 0.1))
  const availX = Math.max(60, box.w - padX * 2)
  const availY = Math.max(60, box.h - 48)
  const base = Math.min(availX, availY) / 2
  const stretch = box.w < 560 ? 1.8 : 1.5
  const Rx = Math.min(availX / 2, base * stretch)
  const Ry = Math.min(availY / 2, base * stretch)
  const cx = box.w / 2 - Math.min(24, box.w * 0.04)
  const cy = box.h / 2

  /*
    ▍视图状态：分成「React 管的结构态」和「每帧由 rAF 直改的动画态」

    · view/rot/spin/picked/hover/allNames/maxDepth → React state（低频，变了才重渲染结构）
    · t（呼吸相位）+ 当前 yaw/pitch 的**实时值** → 存在 ref 里，由 rAF 直接改 DOM

    为什么旋转不再放 state：旋转每帧都变，放 state 就等于每帧重渲染（上版的病根）。
    现在 rot 只保存「起始角度」，实际角度在 ref 里累积，rAF 里直接写到 <g transform>。
  */
  const [view, setView] = useState({ x: 0, y: 0, k: 1 })
  const [spin, setSpin] = useState(true)
  const [picked, setPicked] = useState<McmodNode | null>(null)
  const [hover, setHover] = useState<{ node: McmodNode; x: number; y: number } | null>(null)
  const [allNames, setAllNames] = useState(false)

  // 动画态（不进 React state）
  const yawRef = useRef(0)
  const pitchRef = useRef(0.62) // 初始俯仰 ≈35.5° —— 3/4 视角，盘的自转才有 3D 感（16° 太接近俯视）
  const spinRef = useRef(true)
  const autoHoldRef = useRef(0) // 手动操作后暂停自转的截止时间戳
  const viewRef = useRef(view)
  viewRef.current = view
  /*
    画布几何的最新值。滚轮缩放要按「指针位置」锚定，而滚轮监听挂在 deps=[] 的 effect 里，
    闭包里的 cx/cy 会过期 —— 一律从这个 ref 读最新值（渲染时同步写）。
  */
  const cxyRef = useRef({ cx: 0, cy: 0 })
  cxyRef.current.cx = cx
  cxyRef.current.cy = cy
  const namesRef = useRef(allNames)
  namesRef.current = allNames
  const hoverIdRef = useRef<string | null>(null)
  hoverIdRef.current = hover?.node.id ?? null
  const pickedIdRef = useRef<string | null>(null)
  pickedIdRef.current = picked?.id ?? null
  spinRef.current = spin
  /*
    步进动画的目标视野：点节点后把镜头平移过去，让被点的行星从右侧信息面板底下
    让出来（停在画布左 36% 处）。rAF 循环里逐帧向目标缓动，到 位后自己清空。
    用户一拖拽 / 一滚轮就作废（那是他在自己掌镜，镜头不能再抢）。
  */
  const panTargetRef = useRef<{ x: number; y: number } | null>(null)
  const [qiBusy, setQiBusy] = useState(false) // 「快捷安装」正在搜版本

  // 数据换了就复位（否则会停在上一个模组的角度与状态）
  useEffect(() => {
    setView({ x: 0, y: 0, k: 1 })
    setHover(null)
    setPicked(null)
    yawRef.current = 0
    pitchRef.current = 0.62
    autoHoldRef.current = 0
    panTargetRef.current = null
  }, [data])

  useLayoutEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const measure = () => setBox({ w: el.clientWidth, h: el.clientHeight })
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  /*
    ▍主循环：rAF 直写 DOM（不经过 React）。

    ▍所有权：**节点全由本 effect 创建和维护**
    React 只渲染空的 <g class="mcg-nodes"> 容器和边骨架（见 JSX）。
    上一版让 React 渲染节点、effect 又每帧搬它们 → 两个所有者改同一棵子树 →
    React 的 reconciler 按旧结构 insertBefore，与手写 DOM 打架 → **节点持续丢失**
    （实测 302→282→247→202）。现在所有权单一：createElementNS 建一次，之后只有 effect 改。
    ⚠ 交互（hover/click）也在建节点时挂上，不通过 React 的 props。

    ▍每帧只做两件事
      1. 算投影 → 写 <g style="transform:translate(x,y)"> 和 <circle r=…>
      2. 节点跨「深度档」时才 appendChild

    ▍为什么用「深度档容器」而不是每帧重排 DOM
    更早那版每帧对 302 个 <g> 调 appendChild 重排绘制顺序 —— 实测 DOM mutation
    4.9 万次/1.5s、帧率 31fps（p95 50ms），表现就是「闪屏 + 滚轮卡」。
    appendChild 一个已在树里的节点 = 摘除 + 插入两次树变更，还连带着样式重算。
    正解：把 z2 切成 N 个**固定档**，每档一个常驻 <g class="mcg-bk">；
    节点只在跨档时才搬家（缓慢自转时绝大多数节点一帧内不跨档 → DOM 变更接近 0）。
    档容器本身按由远及近排好，遮挡关系与「每帧精确排序」视觉等价。
    ⚠ 档内不排序 → 省掉每帧 sort(302)。中心点固定放最前档。

    ▍reduce-motion：不转不呼吸，只画一帧静态图然后停。
  */
  const BUCKETS = 12
  useEffect(() => {
    const svg = svgRef.current
    const wrap = wrapRef.current
    if (!svg || !wrap) return

    const reduce = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches

    const nodesG = svg.querySelector<SVGGElement>('.mcg-nodes')
    if (!nodesG) return
    nodesG.replaceChildren() // 本 effect 全权拥有：先清空

    const NS = 'http://www.w3.org/2000/svg'
    // 档位容器（由远及近）。第 0 档 = 最远，最后一个 = 最近。
    const buckets: SVGGElement[] = []
    for (let i = 0; i < BUCKETS; i++) {
      const g = document.createElementNS(NS, 'g')
      g.setAttribute('class', 'mcg-bk')
      nodesG.appendChild(g)
      buckets.push(g)
    }

    /*
      每帧要改 circle.r / label.x / label.opacity 等属性。
      ⚠ 建节点时把子元素引用**存下来**，别每帧 querySelector ——
      302 个点 × 每帧 2~3 次 querySelector 是纯浪费（实测是 longTask 的主要来源之一）。
    */
    type Refs = {
      g: SVGGElement
      circle: SVGCircleElement
      label: SVGTextElement
      halo: SVGCircleElement | null
      idx: number
      lastFs: number
      lastTf: string
      lastR: string
      lastLx: number
      lastShow: boolean | null
      lastVis: boolean | null
      lastDim: boolean
      isPicked: boolean
      yInit: boolean
    }
    const refs: Refs[] = []

    const centerId = data.center?.id
    const dispArr = [...disp.values()]
    const bucketOf = new Map<string, number>()
    let centerRef: Refs | null = null
    // 核球辉光的两层同心圆（只有中心节点有；建节点时收集，帧循环里跟着半径更新）
    const centerGlows: SVGCircleElement[] = []

    // 建节点：一次建好，之后只由本 effect 改
    dispArr.forEach((d, idx) => {
      const id = d.node.id
      const isCenter = id === centerId
      const g = document.createElementNS(NS, 'g')
      g.setAttribute('class', 'mcg-node' + (isCenter ? ' is-center' : ''))
      g.setAttribute('data-nid', id)

      /*
        核球辉光：中心那颗「恒星」外面套两层同心半透明圆。
        ⚠ 用同心圆叠加而不是 <radialGradient> —— 项目是零渐变；叠加半透明色块同样能
        做出「越往外越淡」的辉光，且不引入装饰性渐变。
        ⚠ 必须插在实心 circle **之前**（先绘制的在下层），否则会盖住核球本身。
      */
      if (isCenter) {
        for (const cls of ['mcg-core-glow far', 'mcg-core-glow near']) {
          const gl = document.createElementNS(NS, 'circle')
          gl.setAttribute('class', cls)
          /*
            ⚠ 光晕**不能**用节点自己的分类色：中心那颗在钠这张图里是深蓝，
            深蓝光晕画在深蓝画布底上等于没画（第一版就是这样，截图里完全找不到核球）。
            颜色交给 CSS 定成 --color-cyan（项目里「数据高亮」的既定色），
            换任何一张分类色不同的图都亮得起来。
          */
          g.appendChild(gl)
          centerGlows.push(gl)
        }
      }

      const circle = document.createElementNS(NS, 'circle')
      circle.setAttribute('class', 'mcg-circle')
      circle.setAttribute('fill', d.node.color || 'var(--accent)')
      g.appendChild(circle)

      const text = document.createElementNS(NS, 'text')
      text.setAttribute('class', 'mcg-label')
      text.textContent = shortName(d.node.name)
      g.appendChild(text)

      let halo: SVGCircleElement | null = null
      if (isCenter) {
        halo = document.createElementNS(NS, 'circle')
        halo.setAttribute('class', 'mcg-halo')
        g.appendChild(halo)
      }

      // 交互：直接挂原生监听（不走 React）
      g.addEventListener('pointerenter', (e) => {
        const pe = e as PointerEvent
        setHover({ node: d.node, x: pe.clientX, y: pe.clientY })
      })
      g.addEventListener('pointermove', (e) => {
        const pe = e as PointerEvent
        setHover((h) => (h && h.node.id === id ? { ...h, x: pe.clientX, y: pe.clientY } : h))
      })
      g.addEventListener('pointerleave', () => {
        setHover((h) => (h && h.node.id === id ? null : h))
      })
      g.addEventListener('click', () => {
        // 拖动/平移刚结束（moved）不算点击 —— 不然转完星系就冒出一张卡。
        // ⚠ 不能只看 dragRef：pointerup 先于 click 发生，那时 dragRef 已被清空；
        //   用 clickGuardRef 的时间戳才拦得住「小距离拖拽落在同一节点上」这一档。
        if (performance.now() < clickGuardRef.current || dragRef.current?.moved) return
        const turningOff = pickedIdRef.current === id
        setPicked((c) => (c?.id === id ? null : d.node))
        /*
          步进动画：把镜头缓动过去，让被点的行星停在画布左 36% 处 ——
          右侧那 300px 正好让给信息面板，点和它详情同屏可见。
          关掉面板（再点同一个）就把镜头目标清掉，不再抢镜。
        */
        if (turningOff) {
          panTargetRef.current = null
        } else {
          const p = screen.get(id)
          if (p) {
            panTargetRef.current = {
              x: viewRef.current.x + (VW * 0.36 - p.x),
              y: viewRef.current.y + (VH * 0.5 - p.y),
            }
          }
        }
      })

      buckets[0].appendChild(g)
      bucketOf.set(id, 0)
      const rf: Refs = {
        g, circle, label: text, halo, idx,
        lastFs: 0, lastTf: '', lastR: '', lastLx: -1, lastShow: null, lastVis: null, lastDim: false, isPicked: false, yInit: false,
      }
      refs.push(rf)
      if (isCenter) centerRef = rf
    })

    /*
      旋臂骨架：两条对数螺旋虚线，参数与 spiralLayout **同源**（同用 baseROf / halfBandOf / armThetaAt），
      所以行星必然落在它上面 —— 上一版那三条同心椭圆跟点位毫无关系，那是「不像星系」的一大原因。
      ⚠ 空容器由 React 渲染，路径由本 effect 创建与维护（所有权单一，与节点/边同原则）。
      ⚠ 每帧重算 `d`：旋臂不是刚体，转动时它的投影形状每帧都在变；写死成静态路径一转就与行星分离。
        48 段 × 2 条 = 96 次投影/帧，开销可忽略。
    */
    const armsG = svg.querySelector<SVGGElement>('.mcg-arms')
    if (!armsG) return
    armsG.replaceChildren()
    const ARM_SEGS = 48
    const armEls: SVGPathElement[] = []
    for (let a = 0; a < 2; a++) {
      const el = document.createElementNS(NS, 'path')
      el.setAttribute('class', 'mcg-arm')
      el.setAttribute('fill', 'none')
      armsG.appendChild(el)
      armEls.push(el)
    }

    /*
      边分两层 —— 339 条边全画会糊成毛球，这是「不像星系」的另一大原因：
      · **faint**：按源节点分类色分组（每组一条 path），压到极淡（0.12）当作「星尘」，
        颜色信息还在，但不再抢视觉。
      · **hot**：单独一条 path（青色，项目里「交互/数据高亮」的既定语义），
        只画当前选中/悬停的行星**连通的那几条**，一眼读出它跟谁连。
      ⚠ 组容器仍由 React 渲染成空的 .mcg-edges，内部全由本 effect 创建与维护。
      ⚠ hot 要**最后** append：SVG 后加的画在上面，别被星尘压住。
    */
    const edgesG = svg.querySelector<SVGGElement>('.mcg-edges')
    if (!edgesG) return
    edgesG.replaceChildren()

    const edgeGroups: { color: string; el: SVGPathElement; items: { a: string; b: string; ra: number; rb: number }[]; last: string }[] = []
    const groupOfColor = new Map<string, number>()
    for (const l of links) {
      const a = disp.get(l.source)
      const b = disp.get(l.target)
      if (!a || !b) continue
      const color = a.node.color || 'var(--line-2)'
      let gi = groupOfColor.get(color)
      if (gi == null) {
        const el = document.createElementNS(NS, 'path')
        el.setAttribute('class', 'mcg-edge')
        el.setAttribute('stroke', color)
        el.setAttribute('fill', 'none')
        el.setAttribute('stroke-width', '1.5')
        el.setAttribute('opacity', '0.12')
        edgesG.appendChild(el)
        gi = edgeGroups.length
        groupOfColor.set(color, gi)
        edgeGroups.push({ color, el, items: [], last: '' })
      }
      edgeGroups[gi].items.push({ a: l.source, b: l.target, ra: a.r, rb: b.r })
    }
    const hotEl = document.createElementNS(NS, 'path')
    hotEl.setAttribute('class', 'mcg-edge-hot')
    hotEl.setAttribute('fill', 'none')
    edgesG.appendChild(hotEl)
    let hotLast = ''

    const rFmt = (n: number) => (Math.round(n * 100) / 100).toString()
    const D = 3.2 // 透视强度（越小越强；新 z2 范围 ±~0.8 下近侧最大放大 ~+32%）

    // 邻接表：选中聚焦（is-dim 压暗非邻居）用。只查不写，建一次就够。
    const adj = new Map<string, Set<string>>()
    for (const l of links) {
      if (!adj.has(l.source)) adj.set(l.source, new Set())
      if (!adj.has(l.target)) adj.set(l.target, new Set())
      adj.get(l.source)!.add(l.target)
      adj.get(l.target)!.add(l.source)
    }

    /*
      屏幕外裁剪用的视口尺寸。effect 依赖 cx/cy/Rx/Ry（由 box 量出来），
      容器尺寸一变 effect 就重建，所以这里取一次就够，不用每帧 clientWidth（那会强制回流）。
    */
    const VW = svg.clientWidth || 1
    const VH = svg.clientHeight || 1
    // 外扩余量：要盖住圆半径 + 右侧伸出去的标签，宁可多画几个也别把半截字切掉
    const CULL = 140

    let raf = 0
    let last = performance.now()
    let t = 0
    const screen = new Map<string, { x: number; y: number; s: number; z2: number }>()

    /*
      z2 的可用范围，用来把 z2 线性映射到档位。
      ⚠ 换投影链（绕竖直轴自转）后 z2 = y1·sinP + z·cosP，幅值 ≈ sinP + 0.25 ——
      pitch 拖到极限 ±1.15rad 时 |z2| ≤ 1.02，取 ±1.2 留余量。
      （曾是 ±2.2：那是旧的绕 y 公式留下的，会把实际用到的档挤到中间一小段。）
    */
    const ZMIN = -1.2, ZMAX = 1.2

    const frame = (now: number) => {
      const dt = Math.min(64, now - last)
      last = now

      const paused = performance.now() < autoHoldRef.current
      if (!reduce && spinRef.current && !paused) yawRef.current += dt * 0.00006
      if (!reduce) t += dt

      const v = viewRef.current
      /*
        步进缓动：向点击目标滑过去（每帧靠 14%，约 200ms 内到位）。
        拖拽/滚轮/复位都把 panTarget 清空了，所以这里天然让位给用户操作。
        ⚠ 就地改 v.x/v.y —— view 对象同时挂在 viewRef 和 React state 上，
        没有任何 JSX 渲染 view 本身，就地改不会造成不一致。
      */
      const pt = panTargetRef.current
      if (pt) {
        v.x += (pt.x - v.x) * 0.14
        v.y += (pt.y - v.y) * 0.14
        if (Math.abs(pt.x - v.x) < 0.6 && Math.abs(pt.y - v.y) < 0.6) {
          v.x = pt.x
          v.y = pt.y
          panTargetRef.current = null
        }
      }
      const yaw = yawRef.current
      const pitch = pitchRef.current
      const rotY = Math.cos(yaw), sinY = Math.sin(yaw)
      const rotP = Math.cos(pitch), sinP = Math.sin(pitch)
      const k = v.k
      const sizeK = Math.pow(k, 0.3) // 节点尺寸的亚线性缩放（k=1 时=1；见下方 r 处注释）
      const names = namesRef.current
      const hoverId = hoverIdRef.current
      const pickedId = pickedIdRef.current

      /*
        1) 投影 + 写节点 +（必要时）跨档
        ⚠ 用 refs 数组 + 缓存引用，不是 Map.get + querySelector：
        每帧 Map 查找 302 次、querySelector 600+ 次都是纯开销。
        位置关系与 dispArr 一一对应（建节点时按同样顺序 push）。
      */
      for (let i = 0; i < refs.length; i++) {
        const rf = refs[i]
        const d = dispArr[i]
        const id = d.node.id
        let jx = 0, jy = 0
        if (!reduce) {
          const br = breathOf(id)
          jx = Math.sin(t * br.spd + br.ph) * 0.004
          jy = Math.sin(t * br.spd + br.ph + 1.7) * 0.004
        }
        const x0 = d.x + jx
        const y0 = d.y + jy
        /*
          ⚠⚠ 自转＝绕**竖直轴 z**（转盘），不是绕 y 轴（那是「翻滚」——旧实现写反过：
          转的是 x-z 平面，小俯角下肉眼看不出，抬到 3/4 视角立刻露馅）。2026-10-10 修正。
          链：yaw 绕 z 自转 → pitch 俯仰（绕屏幕水平轴）→ 透视除法。
        */
        const x1 = x0 * rotY - y0 * sinY
        const y1 = x0 * sinY + y0 * rotY
        const y2 = y1 * rotP - d.z * sinP
        const z2 = y1 * sinP + d.z * rotP
        const s = 1 / (1 + z2 / D)
        const sx = cx + x1 * Rx * k * s + v.x
        const sy = cy + y2 * Ry * k * s + v.y
        screen.set(id, { x: sx, y: sy, s, z2 })

        const g = rf.g

        /*
          屏幕外裁剪：跑到视口外的整个节点（含标签）直接 display:none。
          SVG 没有"视口外不画"的自动优化 —— 画到框外的东西照样进光栅化流程，
          放大看细节时框外常常堆着上百个点，纯白烧。
          ⚠ 用 display:none 而不是 opacity:0 —— 前者连排版都省掉。
        */
        const vis = sx > -CULL && sx < VW + CULL && sy > -CULL && sy < VH + CULL
        if (vis !== rf.lastVis) {
          g.style.display = vis ? '' : 'none'
          rf.lastVis = vis
        }
        // 看不见就不写任何样式：坐标已存进 screen（画线还要用），样式等它回来再补
        if (!vis) continue

        /*
          ⚠ 圆点尺寸对 k 用**亚线性**（sizeK = k^0.3，不是 k）—— 用户反馈「缩放时星系的间距拉不开」：
          旧写法位置和尺寸都按 k 等比放大，等于把整张图整体放大 —— 密度不变，簇还是簇、
          重叠的还是重叠。尺寸慢半拍之后，放大＝真的把行星彼此拉开（k=1 时系数恒为 1，默认观感不变）。
        */
        const r = d.r * sizeK * s
        /*
          ⚠⚠ 逐属性做「值没变就不写」的变更检测 —— 这是本轮性能的关键。
          原始写法每帧无条件写 302×(transform + r + label.x + fontSize + opacity) ≈ 1500 次 DOM 写，
          实测主线程 100% 占满（TaskDuration 4s/4s）、RecalcStyleCount 139/帧。
          实际上：缓慢自转时 transform 一直在变（必须写），但 **opacity 几乎不变**
          （只有 hover/选中/缩放跨过 1.15 那一下才翻），r 与 fontSize 变化也远小于坐标。
          加判断后，稳定帧的 DOM 写次数从 ~1500 降到 ~600（只剩 transform + r）。
          ⚠ 判断用「格式化后的字符串」比较，避免浮点尾差导致每次都判为"变了"。
        */
        const tf = `translate(${sx.toFixed(1)}px,${sy.toFixed(1)}px)`
        if (tf !== rf.lastTf) {
          g.style.transform = tf
          rf.lastTf = tf
        }

        // 选中态：class 由本循环维护（节点不归 React 管，不能靠 JSX 的 className）
        const wantPicked = pickedId === id
        if (wantPicked !== rf.isPicked) {
          g.classList.toggle('is-picked', wantPicked)
          rf.isPicked = wantPicked
        }

        // 选中聚焦：非邻居压暗 —— 星系里「这颗行星和谁相连」一眼可读
        const wantDim = !!pickedId && id !== pickedId && !adj.get(pickedId)?.has(id)
        if (wantDim !== rf.lastDim) {
          g.classList.toggle('is-dim', wantDim)
          rf.lastDim = wantDim
        }

        const rs = rFmt(r)
        if (rs !== rf.lastR) {
          rf.circle.setAttribute('r', rs)
          if (rf.halo) rf.halo.setAttribute('r', rFmt(r + 5))
          // 核球辉光跟着核球半径走（两层同心圆：外层更大、透明度更低）
          if (id === centerId) {
            if (centerGlows[0]) centerGlows[0].setAttribute('r', rFmt(r * 3.4))
            if (centerGlows[1]) centerGlows[1].setAttribute('r', rFmt(r * 2.2))
          }
          rf.lastR = rs
        }
        const label = rf.label
        const isCenter = id === centerId
        const show = names || isCenter || pickedId === id || hoverId === id || k >= 1.15
        /*
          ⚠⚠ 隐藏的标签用 display:none，不用 opacity:0，而且**不写 x / fontSize**。
          这两个值随透视 s 每帧微动，旧写法对 302 个标签逐个写 → 每帧 302 次文本重排，
          其中 301 个压根看不见（默认只显示中心名）。HiDPI 实测 p95 34ms、max 63ms 的掉帧
          主要就是这笔账：看不见的东西照样在排版。
          现在：看不见 → 既不排版也不绘制；看得见 → 写，且字号量化到 0.5px 少抖几次。
          ⚠ 这里不能 continue —— 后面还有深度分档，节点圆点始终要参与遮挡排序。
        */
        if (show !== rf.lastShow) {
          label.style.display = show ? '' : 'none'
          rf.lastShow = show
        }
        if (show) {
          const lx = Math.round(r + (isCenter ? 9 : 5))
          if (lx !== rf.lastLx) {
            label.setAttribute('x', String(lx))
            rf.lastLx = lx
          }
          if (!rf.yInit) {
            label.setAttribute('y', '3.5')
            rf.yInit = true
          }
          // 量化到 0.5px：s 每帧都在微动，不量化的话几乎每帧都判成"变了"
          const fs = Math.round(11 * Math.max(0.6, Math.min(1.35, s)) * 2) / 2
          if (fs !== rf.lastFs) {
            label.style.fontSize = `${fs}px`
            rf.lastFs = fs
          }
        }

        /*
          深度档：越远（z2 越大）档号越小 → 先画 → 被近处压住。
          ⚠⚠ 中心**不参与**分档（isCenter 跳过）：它被下面的「强制钉在最前档」接管。
          如果这里也按计算档搬它，就等于**每帧把它搬出去再搬回来** —— DOM 一搬，
          浏览器就把 pointer capture 收走（lostpointercapture），若按下期间正好跨帧，
          mouseup 之后浏览器干脆不合成 click：**中心点约一半概率点不开**。
          （2026-10-10 实测：事件 trace 里 down/up 都落在中心上，就是没有 click，
          连点第二次才出面板。跳过之后中心的 g 一生只搬一次，稳态零 DOM 变更。）
        */
        let bk = Math.floor(((ZMAX - z2) / (ZMAX - ZMIN)) * BUCKETS)
        if (bk < 0) bk = 0
        else if (bk >= BUCKETS) bk = BUCKETS - 1
        const cur = bucketOf.get(id)
        if (cur !== bk && !isCenter) {
          // ⚠⚠ 只在真的跨档时才动 DOM（这才是性能关键：正常缓慢自转时这里几乎不触发）
          buckets[bk].appendChild(g)
          bucketOf.set(id, bk)
        }
      }
      // 中心点强制放最前档（它的名字必须压在所有点之上）
      if (centerRef && bucketOf.get(centerId!) !== BUCKETS - 1) {
        buckets[BUCKETS - 1].appendChild(centerRef.g)
        bucketOf.set(centerId!, BUCKETS - 1)
      }

      /*
        2) 写边
        ⚠⚠ 每条边一次写 4 个属性（x1/y1/x2/y2）→ 339 条 = 1356 次 DOM 写/帧，
        实测这是每帧开销的最大头（比 302 个节点还贵）。改成**按颜色分组、每组一条 <path>**：
        每帧每组只写一次 `d`，写入次数从 1356 降到「颜色数」（通常 3~5）。
        代价：path 上挂不了单条 marker（箭头）。解法见下 —— 箭头改为在 JSX 里静态画在
        「末端小三角」上用 path，而是干脆给每条边保留一个极小的 <use> ——
        不，太复杂。折中：**箭头保留为独立 <line> 但只在缩放/旋转停下时才逐条精确定位**，
        自转中用 path 画线。视觉上自转时箭头本就糊成一团，停下来才看得清。
      */
      for (let gi = 0; gi < edgeGroups.length; gi++) {
        const grp = edgeGroups[gi]
        let d = ''
        for (const it of grp.items) {
          const A = screen.get(it.a)
          const B = screen.get(it.b)
          if (!A || !B) continue
          const dx = B.x - A.x
          const dy = B.y - A.y
          const dist = Math.hypot(dx, dy) || 1
          const ux = dx / dist
          const uy = dy / dist
          const ra = it.ra * k * A.s
          const rb = it.rb * k * B.s
          d += `M${(A.x + ux * ra).toFixed(1)} ${(A.y + uy * ra).toFixed(1)}L${(B.x - ux * rb).toFixed(1)} ${(B.y - uy * rb).toFixed(1)}`
        }
        if (d !== grp.last) {
          grp.el.setAttribute('d', d)
          grp.last = d
        }
      }

      /*
        2b) hot 层：只画「当前这颗行星」连通的那几条边（选中优先于悬停）。
        339 条全亮就是毛球，只亮这几条才读得出「它跟谁连」。
        ⚠ 没有选中/悬停时写一次空 d，别把上一帧的青线留在屏幕上。
      */
      const hotId = pickedId || hoverId
      let dh = ''
      if (hotId) {
        for (const grp of edgeGroups) {
          for (const it of grp.items) {
            if (it.a !== hotId && it.b !== hotId) continue
            const A = screen.get(it.a)
            const B = screen.get(it.b)
            if (!A || !B) continue
            const dx = B.x - A.x
            const dy = B.y - A.y
            const dist = Math.hypot(dx, dy) || 1
            const ra = it.ra * k * A.s
            const rb = it.rb * k * B.s
            dh += `M${(A.x + (dx / dist) * ra).toFixed(1)} ${(A.y + (dy / dist) * ra).toFixed(1)}L${(B.x - (dx / dist) * rb).toFixed(1)} ${(B.y - (dy / dist) * rb).toFixed(1)}`
          }
        }
      }
      if (dh !== hotLast) {
        hotEl.setAttribute('d', dh)
        hotLast = dh
      }

      /*
        2c) 旋臂骨架：两条对数螺旋，走**与节点完全相同的投影链**（yaw → pitch → 透视）。
        ⚠⚠ 采样半径分两个量：`rrU` 是布局空间半径（用来算角度，必须与 spiralLayout 里
        算 θ 时用的 r 一致），`rr = rrU * dispScale` 才是送进投影的屏幕半径。
        拿缩放后的半径去算 θ 会让角度偏移 —— 骨架就会与行星错开。
        ⚠ z 取 0：骨架画在盘的中面上当旋臂中轴，行星因为盘厚在它上下散开（那是自然的带宽）。
      */
      {
        const r0 = R_INNER * 0.75
        for (let ai = 0; ai < armEls.length; ai++) {
          let da = ''
          for (let i = 0; i <= ARM_SEGS; i++) {
            const rrU = r0 + (i / ARM_SEGS) * (R_OUTER - r0)
            const rr = rrU * dispScale
            // ⚠ 用与行星**同一份**层参数算角度（armThetaAt），不是另写一个公式
            const th = armThetaAt(rrU, ai, layoutMaxD)
            const ax = Math.cos(th) * rr
            const ay = Math.sin(th) * rr
            const x1 = ax * rotY - ay * sinY
            const y1 = ax * sinY + ay * rotY
            const y2 = y1 * rotP
            const z2 = y1 * sinP
            const ss = 1 / (1 + z2 / D)
            da +=
              (i === 0 ? 'M' : 'L') +
              (cx + x1 * Rx * k * ss + v.x).toFixed(1) +
              ' ' +
              (cy + y2 * Ry * k * ss + v.y).toFixed(1)
          }
          armEls[ai].setAttribute('d', da)
        }
      }

      raf = requestAnimationFrame(frame)
    }

    frame(performance.now())
    if (!reduce) raf = requestAnimationFrame(frame)
    return () => {
      cancelAnimationFrame(raf)
      // 节点与桶都是本 effect 创建的，卸载时一并清掉（React 只拥有空的 .mcg-nodes）
      nodesG.replaceChildren()
    }
    // ⚠ dispScale 必须在依赖里：旋臂骨架要用同一个缩放，否则切档位后骨架与行星分离
    // ⚠ layoutMaxD 同理：骨架的层参数跟着数据的层数走
  }, [disp, dispScale, layoutMaxD, links, cx, cy, Rx, Ry, data.center?.id])

  /*
    ▍拖拽有两种手势（2026-10-10 加平移 —— 用户反馈「不能上下左右移动」）
    · 左键拖 = 转动：左右 → 绕竖直轴 yaw；上下 → 俯仰 pitch（限位 ±1.15rad）。
    · 右键 / 中键 / Shift+左键拖 = 平移（把星系在画布里上下左右挪）—— 地图类画布的通例。
    ⚠ 只认鼠标；触摸端放行（setPointerCapture 会吃掉滚轮/滚动），触摸上横拖由 touch-action:pan-y 管。
    ⚠ 阈值 3px：小于它算「点击」，否则点节点选不中；超过则手势成立，松手后 300ms 内到来的
      click 一律吞掉（pointerup 先于 click，dragRef 那时已被清空，看它拦不住 —— 用 clickGuardRef）。
    ⚠ 转动期间暂停自转（autoHoldRef），松手 2.5s 后恢复；平移不暂停（它不改角度）。
    直接在 move 里写 ref，不 setState —— 手势不该触发 React 重渲染。
  */
  const dragRef = useRef<{
    sx: number
    sy: number
    mode: 'rot' | 'pan'
    oyaw: number
    opitch: number
    ox: number
    oy: number
    moved: boolean
  } | null>(null)
  const clickGuardRef = useRef(0) // 手势结束后 300ms 内的 click 不算点击
  const onPointerDown = (e: React.PointerEvent) => {
    if (e.pointerType !== 'mouse') return
    const pan = e.button === 2 || e.button === 1 || (e.button === 0 && e.shiftKey)
    if (!pan && e.button !== 0) return
    if (e.button === 1) e.preventDefault() // 中键：拦掉浏览器「自动滚动」圈
    panTargetRef.current = null // 用户开始拖 = 自己掌镜，步进目标作废
    dragRef.current = {
      sx: e.clientX,
      sy: e.clientY,
      mode: pan ? 'pan' : 'rot',
      oyaw: yawRef.current,
      opitch: pitchRef.current,
      ox: viewRef.current.x,
      oy: viewRef.current.y,
      moved: false,
    }
    ;(e.target as Element).setPointerCapture?.(e.pointerId)
  }
  const onPointerMove = (e: React.PointerEvent) => {
    const d = dragRef.current
    if (!d) return
    const dx = e.clientX - d.sx
    const dy = e.clientY - d.sy
    if (Math.abs(dx) + Math.abs(dy) > 3) d.moved = true
    if (d.mode === 'pan') {
      // 就地改 view.x/y —— view 对象同时挂 ref 和 state（没有任何 JSX 渲染它本身），
      // 与 rAF 里的步进缓动是同一套所有权，就地改不会造成不一致。
      viewRef.current.x = d.ox + dx
      viewRef.current.y = d.oy + dy
      return
    }
    autoHoldRef.current = performance.now() + 2500
    yawRef.current = d.oyaw + dx * 0.006
    pitchRef.current = Math.max(-1.15, Math.min(1.15, d.opitch - dy * 0.005))
  }
  const onPointerUp = () => {
    const d = dragRef.current
    if (d?.moved) clickGuardRef.current = performance.now() + 300
    if (d && d.mode !== 'pan') autoHoldRef.current = performance.now() + 2500
    dragRef.current = null
  }

  /*
    ▍滚轮 = 缩放（用户明确要「滚轮放大」），2026-10-10 起**按指针位置锚定**
    星图是画布，不是文档流 —— 地图/PDF/echarts roam:true 全都是滚轮缩放。
    ⚠ 锚定：指针下那个点在缩放前后停在原处（旧版锚在画布中心，视线焦点会跑掉，
      用户反馈「操作感怪怪的」）。推导：sx = cx + w·k + v.x，令指针处 u 不变 →
      v.x′ = u − (u − v.x)·(k′/k)；y 同理。
    ⚠ 用原生 non-passive 监听才能 preventDefault 拦住页面滚动。
    ⚠ 只在指针位于画布内时拦；触屏走 touch-action:pan-y 让纵向滚动穿透。
    ⚠ 缩放要在 React state 里（它影响结构渲染，且低频），但频率得节流 ——
    滚轮事件爆发很高，直接每事件 setState 会连续重渲染。
  */
  useEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const onWheelNative = (e: WheelEvent) => {
      e.preventDefault() // 画布内滚轮 = 缩放，不滚页面
      panTargetRef.current = null // 用户掌镜，步进目标作废
      const f = e.deltaY < 0 ? 1.1 : 0.9
      const rect = el.getBoundingClientRect()
      const ux = e.clientX - rect.left - cxyRef.current.cx
      const uy = e.clientY - rect.top - cxyRef.current.cy
      setView((s) => {
        const next = Math.max(0.35, Math.min(3.2, s.k * f))
        if (next === s.k) return s
        const rel = next / s.k
        return { ...s, k: next, x: ux - (ux - s.x) * rel, y: uy - (uy - s.y) * rel }
      })
    }
    el.addEventListener('wheel', onWheelNative, { passive: false })
    return () => el.removeEventListener('wheel', onWheelNative)
  }, [])

  const zoomBy = (f: number) => {
    panTargetRef.current = null
    setView((v) => ({ ...v, k: Math.max(0.35, Math.min(3.2, v.k * f)) }))
  }
  const reset = () => {
    setView({ x: 0, y: 0, k: 1 })
    yawRef.current = 0
    pitchRef.current = 0.62
    setPicked(null)
    autoHoldRef.current = 0
    panTargetRef.current = null
    /*
      ⚠ 故意**不碰**自转开关（曾经这里有一行 setSpin(true)，2026-10-10 移除）：
      自转是用户显式控制的独立设置，复位视图只归位「视角」。
      一旦这里强制打开：用户「停自转 → 复位」后盘会莫名其妙又转起来；
      探针里「先停自转再复位」的组合也会被静默覆盖（调试白查一轮的坑）。
    */
  }

  const hoverColor = hover?.node.color || 'var(--accent)'

  return (
    <div className="mcg-wrap">
      <div className="mcg-toolbar">
        <span className="mcg-tb-title">{data.center?.name}</span>
        <span className="mcg-tb-sub">
          {allShown
            ? `${data.nodes.length} 个模组 · ${data.links.length} 条关系`
            : `显示 ${pts.length}/${data.nodes.length} 个模组 · ${links.length} 条关系`}
          {data.cached && data.fetchedAt ? ` · 缓存于 ${fmtTime(data.fetchedAt)}` : ' · 刚刚抓取'}
        </span>
        {/* 右键平移这类手势没有可见入口，必须写出来（用户反馈「不能上下左右移动」） */}
        <span className="mcg-tb-help">左键拖转 · 右键拖平移 · 滚轮缩放</span>
        {/* 层级瘦身：「全部 / 1层 / 2层…」。按分类砍会砍成空图，按层级砍才留得住结构。 */}
        {depths.length > 0 && (
          <span className="mcg-depth" role="group" aria-label="显示层级">
            <button
              type="button"
              className={'mcg-depth-btn' + (maxDepth === 0 ? ' on' : '')}
              onClick={() => setMaxDepth(0)}
              title="显示全部层级"
            >
              全部
            </button>
            {depths.map((d) => (
              <button
                key={d}
                type="button"
                className={'mcg-depth-btn' + (maxDepth === d ? ' on' : '')}
                onClick={() => setMaxDepth(d)}
                title={d === 1 ? '只看中心 + 直接依赖' : `只看前 ${d} 层`}
              >
                {d === 1 ? '1 层' : `${d} 层`}
              </button>
            ))}
          </span>
        )}
        <span className="mcg-tb-actions">
          {/* 自转开关：默认开。关掉就完全静止，方便看清某个角度 */}
          <button
            type="button"
            className={'ibtn' + (spin ? ' on' : '')}
            title={spin ? '停止自转' : '自动旋转'}
            aria-pressed={spin}
            onClick={() => setSpin((v) => !v)}
          >
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <path d="M8 1.6a6.4 6.4 0 1 0 6.4 6.4" fill="none" stroke="currentColor" strokeWidth="1.6" />
              <path d="M14.4 1.6v3.4h-3.4" fill="none" stroke="currentColor" strokeWidth="1.6" />
            </svg>
          </button>
          {/* 名字全显：打开时把视图推到 1.5 倍（k=1 时 302 个名字是糊字） */}
          <button
            type="button"
            className={'ibtn' + (allNames ? ' on' : '')}
            title={allNames ? '只看中心名字' : '显示全部名字'}
            aria-pressed={allNames}
            onClick={() => {
              setAllNames((v) => {
                const next = !v
                setView((s) => ({ ...s, k: next ? 1.5 : 1, x: next ? s.x : 0, y: next ? s.y : 0 }))
                return next
              })
            }}
          >
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
              <rect x="1" y="3" width="9" height="1.6" fill="currentColor" />
              <rect x="1" y="7.2" width="14" height="1.6" fill="currentColor" />
              <rect x="1" y="11.4" width="11" height="1.6" fill="currentColor" />
            </svg>
          </button>
          <button type="button" className="ibtn" title="缩小" onClick={() => zoomBy(0.9)} aria-label="缩小">
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><rect x="3" y="7" width="10" height="2" fill="currentColor" /></svg>
          </button>
          <button type="button" className="ibtn" title="放大" onClick={() => zoomBy(1.1)} aria-label="放大">
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><rect x="3" y="7" width="10" height="2" fill="currentColor" /><rect x="7" y="3" width="2" height="10" fill="currentColor" /></svg>
          </button>
          <button type="button" className="ibtn" title="复位视图" onClick={reset} aria-label="复位视图">
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><rect x="2" y="2" width="5" height="5" fill="currentColor" /><rect x="9" y="2" width="5" height="5" fill="currentColor" /><rect x="2" y="9" width="5" height="5" fill="currentColor" /><rect x="9" y="9" width="5" height="5" fill="currentColor" /></svg>
          </button>
        </span>
      </div>

      {/*
        ▍舞台：画布 + 右侧信息面板。
        面板是 absolute 覆盖，**不是 flex 兄弟** —— 不然一开面板画布就变窄，
        ResizeObserver 触发 → effect 重建 → 302 个节点全部重画一遍，肉眼可见地闪。
        覆盖上去画布尺寸纹丝不动，节点一个都不用动。
      */}
      <div className="mcg-stage">
        <div
          ref={wrapRef}
          className="mcg-canvas"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerLeave={onPointerUp}
          /* 右键拖 = 平移 —— 必须拦掉右键菜单，否则一放手就弹出来 */
          onContextMenu={(e) => e.preventDefault()}
        >
        {/* SVG 自己负责裁剪（overflow:hidden），避免外层 .mcg-canvas 因裁剪而吞掉滚轮 */}
        <svg
          ref={svgRef}
          className="mcg-svg"
          width={box.w}
          height={box.h}
          role="img"
          aria-label={`${data.center?.name ?? ''} 的关系星图`}
        >
          <defs>
            {/* 箭头：MC 百科的 edgeSymbol 就是 'arrow'，颜色跟边一致（用 source 色） */}
            <marker id="mcg-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" fill="context-stroke" />
            </marker>
          </defs>

          {/*
            旋臂骨架：两条对数螺旋虚线，参数与 spiralLayout **完全同源**（同一组
            baseROf / halfBandOf / armThetaAt），所以行星必然落在它上面。
            ⚠⚠ 上一版那三条同心椭圆是**和点位无关**的纯装饰 —— 点根本不落在圈上，
            看着像乱画，这是「不像星系」的原因之一。
            ⚠ 它必须**跟着自转一起投影**：写死成静态 SVG 的话一转就与点分离。
            所以这里只给空容器，路径由 effect 每帧重算（2 条 × 48 段，开销可忽略）。
          */}
          <g className="mcg-arms" aria-hidden="true" />

          {/*
            边层：与节点层同样的所有权原则 —— **内部由 effect 全权创建与维护**。
            effect 把边按源节点颜色分组，每组一条 <path>，每帧每组只写一次 `d`
            （原来是每条 line 写 4 个属性 × 339 条 = 1356 次 DOM 写/帧，是最大开销）。
            ⚠ 用 path 就没法挂 marker 箭头了。这是有意的取舍：自转时箭头本就糊成一团，
            渲染开销却高得多。改用「线末端留一点空隙 + 圆的描边」表达方向感，
            真正需要看箭头时停下自转、放大即可（缩放后节点稀疏，看的是点不是线）。
          */}
          <g className="mcg-edges" />

          {/*
            节点层：同样由 effect 全权创建与维护（React 不渲染这里的子节点）。
            ⚠⚠ 这是本次修复的关键决定。上一版让 React 渲染 302 个 <g>，effect 再每帧
            把它们 appendChild 到深度档容器里 —— 等于**两个所有者同时改同一棵子树**。
            React 的 reconciler 不知道节点被搬走了，下个渲染周期就按自己记的旧结构做
            insertBefore/diff，与手写 DOM 打架，实测表现为**节点持续丢失**（302→282→247→202）。
            正解：容器由 React 渲染（空的、固定的），子节点全部由 effect 用 createElementNS
            创建一次、之后只由 effect 改属性。所有权单一，没有竞态。
          */}
          <g className="mcg-nodes" />
        </svg>
        </div>

        {/*
          ▍点行星 → 右侧信息面板（步进动画 + 聚焦压暗）
          站方是点了直接 window.open 跳百科；这里就地给面板：信息、百科链接、快捷安装。
          ⚠ 关系句按站方那套口径写，用分类名拼，别自己发明措辞。上屏是纯文本。
        */}
        {picked && (() => {
          const out = data.links.filter((l) => l.source === picked.id).length
          const inc = data.links.filter((l) => l.target === picked.id).length
          const rel = relationSentence(data, picked)
          const color = picked.color || 'var(--accent)'
          const isCenterNode = picked.id === data.center?.id
          const deepTxt =
            typeof picked.deep === 'number' && picked.deep > 0
              ? `${picked.deep} 层`
              : isCenterNode
                ? '中心'
                : '—'
          return (
            <aside className="mcg-side" role="dialog" aria-label={`${picked.name} 的关系信息`}>
              {/*
                抬头：分类色只做「左侧竖条 + chip 描边」两处点缀，**名字用正文色**。
                上一版名字直接染成分类色（深红/深紫那种），在深色面板底上对比度不够，
                而且和下面那句关系句抢视觉 —— 颜色在星图里已经用来区分分类了，
                这里让它退回点缀位，名字回归 ink-1（正文色，对比度达标）。
              */}
              <div className="mcg-side-head">
                <span className="mcg-side-bar" style={{ background: color }} aria-hidden="true" />
                <span className="mcg-side-name">{picked.name}</span>
                <button
                  type="button"
                  className="ibtn mcg-side-x"
                  title="关闭"
                  aria-label="关闭"
                  onClick={() => {
                    setPicked(null)
                    panTargetRef.current = null
                  }}
                >
                  <svg viewBox="0 0 16 16" width="12" height="12" aria-hidden="true">
                    <path d="M3 3l10 10M13 3L3 13" stroke="currentColor" strokeWidth="1.7" />
                  </svg>
                </button>
              </div>

              {/* chips 只报「类别」；「距中心」是下面数值行的活 —— 一处一事，
                  320px 的面板里同一句话出现两遍就是噪音。 */}
              {((picked.cat && picked.cat !== '中心') || isCenterNode) && (
                <p className="mcg-side-tags">
                  {picked.cat && picked.cat !== '中心' && (
                    <span className="mcg-side-chip" style={{ borderColor: color }}>
                      {picked.cat}
                    </span>
                  )}
                  {isCenterNode && <span className="mcg-side-chip">本图中心</span>}
                </p>
              )}

              {rel && <p className="mcg-side-rel">{rel}</p>}

              {/* 数值行：照设计稿的 .srow（左标签 / 右值），不再自造一段统计文字 */}
              <div className="mcg-side-rows">
                <div className="srow">
                  <span className="srow-k">依赖</span>
                  <span className="srow-v">{out} 个</span>
                </div>
                <div className="srow">
                  <span className="srow-k">被依赖</span>
                  <span className="srow-v">{inc} 个</span>
                </div>
                {!isCenterNode && (
                  <div className="srow">
                    <span className="srow-k">距中心</span>
                    <span className="srow-v">{deepTxt}</span>
                  </div>
                )}
              </div>

              {picked.content && (
                <div className="mcg-side-desc">
                  <span className="mcg-side-cap">简介</span>
                  <p>{picked.content}</p>
                </div>
              )}
              <div className="mcg-side-actions">
                {picked.classId && (
                  <a
                    className="btn btn-ghost xs"
                    href={`https://www.mcmod.cn/class/${picked.classId}.html`}
                    target="_blank"
                    rel="noreferrer"
                  >
                    MC 百科条目
                  </a>
                )}
                {!isCenterNode && onQuickInstall && (
                  <button
                    type="button"
                    className="btn xs"
                    disabled={qiBusy}
                    title="去在线市场找到这个模组并挑版本安装"
                    onClick={() => {
                      if (qiBusy) return
                      setQiBusy(true)
                      Promise.resolve(onQuickInstall(picked))
                        .catch(() => {})
                        .finally(() => setQiBusy(false))
                    }}
                  >
                    {qiBusy ? '找版本中…' : '快捷安装'}
                  </button>
                )}
                {isCenterNode && <p className="mcg-side-note">这就是当前正在看的模组</p>}
              </div>
            </aside>
          )
        })()}
      </div>

      {/* 图例：分类色 + 分类名 + 该类节点数（纯展示，「瘦身」交给层级档位） */}
      <ul className="mcg-cats">
        {data.cats.map((c, i) => {
          const name = c.name || '中心'
          const count = catCount.get(name) ?? 0
          if (count === 0) return null
          return (
            <li key={i}>
              <span className="mcg-swatch" style={{ background: c.color || 'var(--accent)' }} />
              {name}
              <span className="mcg-cat-count">{count}</span>
            </li>
          )
        })}
      </ul>

      <p className="mcg-hint">滚轮缩放 · 拖拽转动 · 点行星看详情</p>

      {/* hover 卡片：跟站方 tooltip 一样给「名字 + 简介」，点标题进百科 */}
      {hover && (
        <div className="mcg-tip" style={{ left: hover.x + 14, top: hover.y + 14, borderColor: hoverColor }}>
          <a
            className="mcg-tip-name"
            style={{ color: hoverColor }}
            href={hover.node.classId ? `https://www.mcmod.cn/class/${hover.node.classId}.html` : undefined}
            target="_blank"
            rel="noreferrer"
          >
            {hover.node.name}
          </a>
          {hover.node.cat && <span className="mcg-tip-cat">{hover.node.cat}</span>}
          {hover.node.content && <p className="mcg-tip-content">{hover.node.content}</p>}
        </div>
      )}

      <p className="mcg-src">
        {data.source}
        {data.note ? ` · ${data.note}` : ''}
      </p>
    </div>
  )
}

/**
 * 名字截断：图谱里放不下「[BSVSB]更好的钠视频设置按钮」这种长名。
 * 只截**上一个屏的名字**（hover 卡片给完整名）。
 */
function shortName(s: string): string {
  const r = [...s]
  return r.length > 14 ? r.slice(0, 13).join('') + '…' : s
}

/**
 * 一句话说清「这个点和中心是什么关系」——口径照抄 MC 百科的 tooltip 措辞。
 * ⚠ 不能只认「和中心直接相连」的点：这张图是 4 层深的 DAG，302 个里 277 个隔着好几跳。
 * 只写直接关系 → 点了深处节点得到一句空话。所以永远给出一句：直连说明方向，间接说明相连方式。
 * ⚠ 句子里**不再重复**行星名字（抬头已经写了）和跳数（「距中心」那行专门报）——
 *   320px 的面板里同一个信息说两遍，读起来像坏掉的模板。
 */
function relationSentence(data: McmodRelations, n: McmodNode): string {
  const c = data.center
  if (!c || n.id === c.id) return '这是本图的中心模组。'
  const asTarget = data.links.some((l) => l.source === c.id && l.target === n.id)
  const asSource = data.links.some((l) => l.source === n.id && l.target === c.id)
  const cat = n.cat && n.cat !== '中心' ? n.cat : '相关模组'
  if (asTarget && asSource) return `与 ${c.name} 互相引用。`
  if (asTarget || asSource) return `是 ${c.name} 的${cat}。`
  return `与 ${c.name} 经前置/依赖关系间接相连。`
}

/** 把 RFC3339 时间显示成人能读的「几天前 / 几小时前」。 */
function fmtTime(iso: string): string {
  const t = new Date(iso).getTime()
  if (!Number.isFinite(t)) return '未知'
  const mins = Math.max(0, Math.round((Date.now() - t) / 60000))
  if (mins < 1) return '刚刚'
  if (mins < 60) return `${mins} 分钟前`
  const hrs = Math.round(mins / 60)
  if (hrs < 24) return `${hrs} 小时前`
  return `${Math.round(hrs / 24)} 天前`
}
