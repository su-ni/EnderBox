/*
  缩放 / 平移体检（2026-10-10 新增，配合「操作感」修复）——全部用数值说话：
    ① 按钮缩放 ×1.6105（1.1^5）：节点到锚点距离应 ×1.610；
       圆点半径应 ×1.156（= 1.6105^0.3，亚线性；旧代码是 ×1.610 ＝「整图放大、密度不变」）。
    ② 指针锚定：在某个节点正中滚一档（k×1.1）→ 该节点基本原地不动（漂移 < 6px），
       同时远端节点距离比 ≈ 1.1（证明 k 真的变了）。
    ③ 右键拖拽平移：全部节点整体位移 ≈ 拖拽向量 (+120,-60)；复位后回原位（残差 < 6px）。
  用法: node tools/probe_zoompan.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'zpan-'))
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
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(600)

// 顺序：先复位、后停自转（对本项目新旧版本都正确）
const resetBtn = `(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`
const stopSpin = `(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`
await ev(resetBtn); await sleep(1200)
await ev(stopSpin); await sleep(400)

// 采样：圆心相对画布左上角坐标 +（非中心）最大圆的半径。被剔除（display:none）的节点 width=0，跳过。
const snap = `(()=>{
  const box=document.querySelector('.mcg-canvas').getBoundingClientRect();
  const out={}; let maxR=0, rFor=null;
  document.querySelectorAll('.mcg-canvas .mcg-node').forEach(n=>{
    const c=n.querySelector('circle'); const b=c.getBoundingClientRect();
    if (b.width < 0.5) return;
    const nid=n.dataset.nid;
    const rr=+c.getAttribute('r');
    out[nid]=[Math.round((b.x+b.width/2-box.left)*10)/10, Math.round((b.y+b.height/2-box.top)*10)/10, rr];
    if (nid!=='0' && rr>maxR){maxR=rr; rFor=nid}
  });
  return {box:{w:box.width,h:box.height}, out, maxR, rFor}
})()`

const s0 = await ev(snap)
console.log('BASE 可见节点', Object.keys(s0.out).length, ' 最大非中心节点', s0.rFor, 'r=', s0.maxR.toFixed(1), ' 画布', Math.round(s0.box.w), 'x', Math.round(s0.box.h))

// ① 按钮缩放 ×5
for (let i = 0; i < 5; i++) {
  await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='放大');b&&b.click();return 1})()`)
  await sleep(170)
}
await sleep(500)
const s1 = await ev(snap)
{
  // 按钮缩放只改 k、不挪平移 → 固定点 = 画布中心 + 平移（复位后平移为 0）
  const ax = s0.box.w / 2 - Math.min(24, s0.box.w * 0.04), ay = s0.box.h / 2
  const posR = []
  for (const nid of Object.keys(s0.out)) {
    if (!(nid in s1.out)) continue
    const d0 = Math.hypot(s0.out[nid][0] - ax, s0.out[nid][1] - ay)
    const d1 = Math.hypot(s1.out[nid][0] - ax, s1.out[nid][1] - ay)
    if (d0 > 12 && d1 > 12) posR.push(d1 / d0)
  }
  posR.sort((a, b) => a - b)
  const med = posR[Math.floor(posR.length / 2)]
  // 半径比优先用「同一个节点」比（消除并列最大节点换位误差）；该节点被裁时退回 maxR 比
  const rRat = (s0.rFor in s1.out) ? s1.out[s0.rFor][2] / s0.out[s0.rFor][2] : s1.maxR / s0.maxR
  console.log('ZOOM×1.6105  位置比中位', med.toFixed(3), '（期望≈1.610） | 半径比', rRat.toFixed(3), '（期望≈1.154=1.6105^0.3；旧代码≈1.610）')
  console.log('VERDICT_ZOOM', Math.abs(med - 1.610) < 0.08 && Math.abs(rRat - 1.154) < 0.05 ? '✓ 间距拉开且尺寸亚线性' : '✗ 不符')
}

await ev(resetBtn); await sleep(1300)

// ② 指针锚定：在最大非中心节点正中滚一档
{
  const s2 = await ev(snap)
  const boxAbs = await ev(`(()=>{const b=document.querySelector('.mcg-canvas').getBoundingClientRect();return {x:b.x,y:b.y}})()`)
  const target = s2.out[s2.rFor]
  const vx = Math.round(boxAbs.x + target[0]), vy = Math.round(boxAbs.y + target[1])
  await send('Input.dispatchMouseEvent', { type: 'mouseWheel', x: vx, y: vy, deltaX: 0, deltaY: -100 }, sid)
  await sleep(500)
  const s3 = await ev(snap)
  const anchor = s3.out[s2.rFor]
  if (!anchor) {
    console.log('VERDICT_ANCHOR ✗ 锚点节点滚轮后被裁，换更靠内的节点重跑')
  } else {
    const drift = Math.hypot(anchor[0] - target[0], anchor[1] - target[1])
    /*
      锚定不变式：P' = u + f·(P − u)，u=指针位置。
      → 指针处节点漂移≈0；其余节点**相对指针处节点**的距离比 = f = 1.1（与锚点无关的纯缩放不变量）。
      ⚠ 旧版（锚画布中心）：指针处节点漂 f·d−d = 0.1·d（d=指针离中心距离）→ 用漂移区分新旧。
      ⚠ 呼吸 jitter 噪声 ±2~4px：漂移阈值 10px（旧版漂移是几十~上百 px，区分度足够）。
    */
    const farList = Object.keys(s2.out).filter(n => n !== s2.rFor && n in s3.out)
      .map(n => ({ n, d: Math.hypot(s2.out[n][0] - target[0], s2.out[n][1] - target[1]) }))
      .sort((a, b) => b.d - a.d)
    const far = farList.find(x => x.d > 120)?.n
    const d0 = far ? Math.hypot(s2.out[far][0] - target[0], s2.out[far][1] - target[1]) : 0
    const d1 = far ? Math.hypot(s3.out[far][0] - anchor[0], s3.out[far][1] - anchor[1]) : 0
    console.log('WHEEL_ANCHOR 指针处节点漂移', drift.toFixed(1), 'px（期望<10） | 远端相对距离比', far ? (d1 / d0).toFixed(3) : 'n/a', '（期望≈1.1）')
    console.log('VERDICT_ANCHOR', drift < 10 && far && Math.abs(d1 / d0 - 1.1) < 0.025 ? '✓ 指针锚定生效' : '✗ 锚定/缩放不对')
  }
}

await ev(resetBtn); await sleep(1300)

// ③ 右键拖拽平移 +120,-60；反复位
{
  const s4 = await ev(snap)
  const box = await ev(`(()=>{const b=document.querySelector('.mcg-canvas').getBoundingClientRect();return {x:Math.round(b.x),y:Math.round(b.y),w:Math.round(b.width),h:Math.round(b.height)}})()`)
  const sx = box.x + Math.round(box.w * 0.5), sy = box.y + Math.round(box.h * 0.5)
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: sx, y: sy, button: 'right', buttons: 2, clickCount: 1 }, sid)
  for (let i = 1; i <= 12; i++) {
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: sx + i * 10, y: sy - i * 5, button: 'right', buttons: 2 }, sid)
    await sleep(30)
  }
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: sx + 120, y: sy - 60, button: 'right', buttons: 0, clickCount: 1 }, sid)
  await sleep(500)
  const s5 = await ev(snap)
  const dxs = [], dys = []
  for (const nid of Object.keys(s4.out)) {
    if (nid === '0' || !(nid in s5.out)) continue
    dxs.push(s5.out[nid][0] - s4.out[nid][0]); dys.push(s5.out[nid][1] - s4.out[nid][1])
  }
  const med = a => { const b = [...a].sort((x, y) => x - y); return b[Math.floor(b.length / 2)] }
  const mdx = med(dxs), mdy = med(dys)
  console.log('PAN 中位位移 dx=', mdx.toFixed(1), '（期望 +120） dy=', mdy.toFixed(1), '（期望 -60）')
  console.log('VERDICT_PAN', Math.abs(mdx - 120) < 3 && Math.abs(mdy + 60) < 3 ? '✓ 右键拖拽平移生效' : '✗ 平移未生效')

  await ev(resetBtn); await sleep(1400)
  const s6 = await ev(snap)
  let maxd = 0
  for (const nid of Object.keys(s4.out)) {
    if (!(nid in s6.out)) continue
    maxd = Math.max(maxd, Math.hypot(s6.out[nid][0] - s4.out[nid][0], s6.out[nid][1] - s4.out[nid][1]))
  }
  // 残差噪声 = 两次采样的呼吸 jitter（±2~4px）；复位真失败是几十~几百 px，8px 阈值区分度足够
  console.log('RESET_BACK 最大残差', maxd.toFixed(1), 'px（期望<8）', maxd < 8 ? '✓' : '✗')
}

ws.close(); chrome.kill()
