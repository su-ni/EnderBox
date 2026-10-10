/*
  分场景测帧率：默认视图 vs 放大后（k>=1.15 会一次放出全部 302 个描边标签）。
  同时统计「可见标签数」，判断卡顿是不是标签绘制引爆的。
  用法: node tools/probe_fps_zoom.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
/* ⚠ 固定 profile 目录、跨轮复用：一次测量就是一个 ~50MB 的 Chrome profile，
   以前每次 mkdtemp 新建一个、从不清理，OS 临时目录里攒了一堆。
   这个探针本来就该独占跑（同时跑两个 Chrome 量帧率没有意义），固定目录不会撞。 */
const profile = 'F:/tmp/uiprobe-pool/fps-zoom'
mkdirSync(profile, { recursive: true })
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
const pf = join(profile, 'DevToolsActivePort')
let port = 0
for (let i = 0; i < 60; i++) { try { port = Number(readFileSync(pf, 'utf8').split('\n')[0]); if (port) break } catch {} await new Promise(r => setTimeout(r, 200)) }
const ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
const ws = new WebSocket(ver.webSocketDebuggerUrl)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0; const pend = new Map()
ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } })
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid); await send('Runtime.enable', {}, sid)
// HiDPI 模拟：真机 retina 屏 devicePixelRatio=2 → 光栅面积 4 倍，这才是用户「卡」的真实场景
const DSF = Number(process.argv[3] || 1)
if (DSF !== 1) {
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: DSF, mobile: false }, sid)
}
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1200)

const measure = () => ev(`new Promise(res=>{
  const t=[]; let last=performance.now(), n=0;
  const tick=()=>{ const now=performance.now(); t.push(now-last); last=now;
    if(++n<150) requestAnimationFrame(tick);
    else { t.sort((a,b)=>a-b);
      res({p50:+t[75].toFixed(1), p95:+t[142].toFixed(1), max:+t[149].toFixed(1), fps:+(1000/t[75]).toFixed(1)}) } };
  requestAnimationFrame(tick);
})`)
const visLabels = () => ev(`[...document.querySelectorAll('.mcg-label')].filter(l=>{
  const s=getComputedStyle(l); return s.display!=='none' && +s.opacity>0.01 }).length`)
const zoom = () => ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='放大');b&&b.click();return 1})()`)

console.log('DEFAULT  ', JSON.stringify(await measure()), 'labels=', await visLabels())

// 放大到 k>=1.15 以上，让全部标签放出
for (let i = 0; i < 4; i++) { await zoom(); await sleep(120) }
await sleep(600)
console.log('ZOOMED   ', JSON.stringify(await measure()), 'labels=', await visLabels())

// 开「全部名称」再测一次（最坏情况）
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/名称/.test(b.title||b.textContent||''));b&&b.click();return 1})()`)
await sleep(600)
console.log('ALLNAMES ', JSON.stringify(await measure()), 'labels=', await visLabels())

ws.close(); chrome.kill()
