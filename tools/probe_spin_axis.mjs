/*
  旋转轴运动学诊断：拖拽 100px（yaw +0.6rad）前后，对比每个节点的屏幕位移。
  核心判据 = **位移/半径比值（RATIO）**：
    · 转盘（绕盘法线，正确形态）：所有点的 RATIO ≈ 恒定 ≈ 2sin(ΔY/2)≈0.59 —— 与方位无关。
    · 翻滚（绕水平轴，旧 bug 形态）：RATIO ∝ |x0|/r = |cosφ| —— 竖直中线(x0≈0)的点 RATIO≈0，
      在屏幕竖中线上会有一列"不动点"。
  附带输出：CROSS 符号一致性（全体同向环绕）、最静点的半径与方位（是否聚在竖中线）。
  用法: node tools/probe_spin_axis.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'spin-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
const pf = join(profile, 'DevToolsActivePort')
let port = 0
for (let i = 0; i < 60; i++) { try { port = Number(readFileSync(pf, 'utf8').split('\n')[0]); if (port) break } catch { } await new Promise(r => setTimeout(r, 200)) }
const ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
const ws = new WebSocket(ver.webSocketDebuggerUrl)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0; const pend = new Map()
ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } })
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid); await send('Runtime.enable', {}, sid)
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value

for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1500)

// ⚠ 画布必须先滚进视口 —— 否则拖拽坐标是「视口外」的，事件全落在 HTML 上（2026-10-10 踩过：
//   rect.y=2620 时 dispatch 的 mousePressed 命中的是 document root，拖拽静默不启动）
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(500)
/*
  ⚠ 顺序：先复位、后停自转 —— 反着写会被 reset() 静默覆盖（旧版 reset 里有 setSpin(true)，
  2026-10-10 已从产品移除；探针保持这个顺序对新旧版本都正确）。
*/
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1200)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(500)
console.log('SPIN_BTN_TITLE', await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/自动旋转|停止自转/.test(b.title||''));return b?b.title:null})()`), '（应为「自动旋转」= 已停）')

const snap = `(()=>{const out={};document.querySelectorAll('.mcg-canvas .mcg-node').forEach(n=>{const c=n.querySelector('circle');const b=c.getBoundingClientRect();out[n.dataset.nid]=[Math.round((b.x+b.width/2)*10)/10,Math.round((b.y+b.height/2)*10)/10]});return out})()`
const a = await ev(snap)
console.log('SNAP_A_NODES', Object.keys(a).length)

// 挂事件 trace（document capture 级）：看拖拽事件是否完整送达 + 每步 clientX + capture 行为
await ev(`(()=>{
  window.__dt = []
  const rec = (ev) => (e) => window.__dt.push(ev + '(' + Math.round(e.clientX) + ',' + Math.round(e.clientY) + ')@' + (e.target.tagName||'?') + '.' + String(e.target.className?.baseVal ?? e.target.className ?? '').slice(0,20))
  for (const t of ['pointerdown','pointermove','pointerup','pointercancel','gotpointercapture','lostpointercapture']) document.addEventListener(t, rec(t), true)
  return 1
})()`)

// 拖拽：从画布左上角空白处开始，+100px 分 10 步（yaw +0.6rad，弦/弧 ≈ 0.985）
const rect = await ev(`(()=>{const b=document.querySelector('.mcg-canvas').getBoundingClientRect();return {x:Math.round(b.x),y:Math.round(b.y)}})()`)
const sx = rect.x + 80, sy = rect.y + 80
console.log('DRAG_START', sx, sy, ' HIT', await ev(`(()=>{const el=document.elementFromPoint(${sx},${sy});return el.tagName+'.'+String(el.className?.baseVal??el.className??'').slice(0,30)})()`))
await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: sx, y: sy, button: 'left', buttons: 1, clickCount: 1 }, sid)
for (let i = 1; i <= 10; i++) {
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: sx + i * 10, y: sy, button: 'left', buttons: 1 }, sid)
  await sleep(50)
}
await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: sx + 100, y: sy, button: 'left', buttons: 0, clickCount: 1 }, sid)
await sleep(700)
console.log('DRAG_TRACE', (await ev(`window.__dt.join(' | ')`)))
await ev(`(()=>{delete window.__dt; return 1})()`)

const b2 = await ev(snap)
const ids = Object.keys(a)
const c0a = a['0'], c0b = b2['0']
const ds = []
let pos = 0, neg = 0
for (const nid of ids) {
  if (nid === '0') continue
  const dx = b2[nid][0] - a[nid][0], dy = b2[nid][1] - a[nid][1]
  const d = Math.hypot(dx, dy)
  const rr = Math.hypot(a[nid][0] - c0a[0], a[nid][1] - c0a[1]) // 起点离中心距离（屏幕）
  const rx = a[nid][0] - c0a[0], ry = a[nid][1] - c0a[1]
  const cr = rx * dy - ry * dx
  if (cr > 0) pos++; else if (cr < 0) neg++
  ds.push({ nid, d, dx, dy, rr, ratio: rr > 5 ? d / rr : null })
}
const withR = ds.filter(x => x.ratio !== null).sort((x, y) => x.ratio - y.ratio)
const ratios = withR.map(x => x.ratio)
const q = (arr, p) => arr[Math.min(arr.length - 1, Math.floor(arr.length * p))]
console.log('CENTER_MOVED', Math.hypot(c0b[0] - c0a[0], c0b[1] - c0a[1]).toFixed(1), '  RADIUS(px) min/med/max',
  q(ds.map(x => x.rr).sort((x, y) => x - y), 0).toFixed(0), '/',
  q(ds.map(x => x.rr).sort((x, y) => x - y), 0.5).toFixed(0), '/',
  ds.reduce((m, x) => Math.max(m, x.rr), 0).toFixed(0))
console.log('RATIO(位移/半径) min/p10/med/p90/max',
  ratios[0].toFixed(3), '/', q(ratios, 0.1).toFixed(3), '/', q(ratios, 0.5).toFixed(3), '/', q(ratios, 0.9).toFixed(3), '/', ratios[ratios.length - 1].toFixed(3),
  '  （转盘期望≈0.59 恒定；翻滚值域 0→0.59）')
console.log('QUIET10 (ratio 最低的 10 个点): nid:ratio:离竖中线的横向偏移 dx',
  withR.slice(0, 10).map(x => `${x.nid}:${x.ratio.toFixed(2)}:${(a[x.nid][0] - c0a[0]).toFixed(0)}`).join(' '))
console.log('QUIET10 半径', withR.slice(0, 10).map(x => x.rr.toFixed(0)).join(','), '（转盘:都是小半径; 翻滚:大半径但贴竖中线）')
console.log('CROSS pos', pos, 'neg', neg)
console.log('VERDICT', (() => {
  const rmed = q(ratios, 0.5), r10 = q(ratios, 0.1)
  if (r10 > rmed * 0.55) return '→ RATIO 全域恒定 = 转盘(正确)'
  const quiet = withR.slice(0, 10)
  const onCenterline = quiet.filter(x => Math.abs(a[x.nid][0] - c0a[0]) < 12 && x.rr > 60).length
  if (onCenterline >= 5) return `→ ${onCenterline}/10 个最静点在大半径处贴竖中线 = 翻滚(旧版/回归)`
  return '→ 混合信号，人工判读'
})())

// ── 回归断言：复位视图不得偷偷重开自转（2026-10-10 从 reset() 里移除的 setSpin(true)）──
// 此时自转已停（SPIN_BTN_TITLE 应为「自动旋转」）。点复位后 title 必须仍是「自动旋转」；
// 在旧代码上这里会变回「停止自转」= 断言抓住旧行为（拆修复应变红）。
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1000)
{
  const t2 = await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/自动旋转|停止自转/.test(b.title||''));return b?b.title:null})()`)
  console.log('RESET_KEEPS_SPIN_OFF', t2 === '自动旋转', `(title=${t2})`, t2 === '自动旋转' ? '✓' : '✗ 复位偷偷重开了自转（回归！）')
}
// 反向：手动开启自转 → 复位 → 应保持「停止自转」（复位对自转开关双向都不碰）
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/自动旋转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(300)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(600)
{
  const t3 = await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/自动旋转|停止自转/.test(b.title||''));return b?b.title:null})()`)
  console.log('RESET_KEEPS_SPIN_ON', t3 === '停止自转', `(title=${t3})`, t3 === '停止自转' ? '✓' : '✗ 复位把自转关了（回归！）')
}

ws.close(); chrome.kill()
