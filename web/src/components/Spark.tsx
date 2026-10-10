/**
 * 火花线 —— 照设计稿 app.js 的 drawSpark 原样实现。
 *
 * 用的是**同一套确定性 LCG**（种子 20261007），所以每次渲染出的折线都一样，
 * 不会「刷新一次图就变一次」。四个元素缺一不可，那是设计稿的画法：
 *   `.area`     折线下方的填充
 *   `.peakline` 峰值虚线
 *   `.line`     折线本身
 *   `.pk`       峰值处的小方块
 * 样式全在 design.css 的 `.spark *` 里。
 *
 * ⚠ 别改成平滑曲线（path + 贝塞尔）：设计稿要的是**折线**，一平滑像素味就没了。
 */
const N = 40
const W = 120
const H = 30

export function Spark({
  seed,
  className = 'spark',
}: {
  seed: number
  className?: string
}) {
  // 与设计稿同一套 LCG。seed 参与起点，让四张卡的走线各不相同 ——
  // 设计稿里 rnd 是全局连续的（第一张用序列前 40 个、第二张用接着的 40 个），
  // 这里用 seed 打散，效果等价，且每张卡固定不变。
  let s = (20261007 ^ Math.imul(seed || 40, 2654435761)) >>> 0
  const rnd = () => {
    s = (Math.imul(s, 1103515245) + 12345) & 0x7fffffff
    return s / 0x7fffffff
  }
  const clamp = (v: number, a: number, b: number) => Math.max(a, Math.min(b, v))

  const base = seed || 40
  const pts: [number, number][] = []
  let v = base
  for (let i = 0; i < N; i++) {
    v += (rnd() - 0.46) * 14
    v = clamp(v, base * 0.45 + 4, base * 1.45 + 8)
    pts.push([(i / (N - 1)) * W, H - 2 - clamp(v / (base * 1.55 + 12), 0, 1) * (H - 5)])
  }
  const d = pts.map((p, i) => `${i ? 'L' : 'M'}${p[0].toFixed(1)} ${p[1].toFixed(1)}`).join(' ')
  const area = `${d} L${W} ${H} L0 ${H} Z`
  const peakY = Math.min(...pts.map((p) => p[1]))
  const peak = pts.find((p) => p[1] === peakY) ?? pts[0]

  return (
    <svg className={className} viewBox="0 0 120 30" preserveAspectRatio="none" aria-hidden="true">
      <path className="area" d={area} />
      <line className="peakline" x1={0} y1={peakY} x2={W} y2={peakY} />
      <path className="line" d={d} />
      <rect className="pk" x={peak[0] - 1.5} y={peak[1] - 1.5} width={3} height={3} />
    </svg>
  )
}
