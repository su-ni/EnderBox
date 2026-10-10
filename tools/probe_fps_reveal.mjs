/*
  测「翻页转场」的帧率：在采样窗口里连点 4 次页签，让窗口整个套住 4 次转场。
  为什么不用 probe_fps_zoom 那套「先 click 再 measure」：转场只有 640ms，
  两次 CDP 往返就把被测窗口推过去了 —— 必须让点击和采样跑在同一条 rAF 时间线上。

  用法: node tools/probe_fps_reveal.mjs <URL> [dsf] [idle|noanim]
  ⚠ dsf 必须两档都跑：headless 默认 dSF=1，量不出 HiDPI 上的光栅开销（星图那轮踩过）。
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
const DSF = Number(process.argv[3] || 1)
/* idle   = 不点页签，只采基线帧率
   noanim = 挂 body.no-anim 后照常点页签：转场被短路，但 React 重渲染照跑。
            用来把「canvas 逐帧画的」和「换页重渲染的」两笔开销分开算。 */
const MODE = process.argv[4] || ''
const IDLE = MODE === 'idle'
/* ⚠ 固定 profile 目录、跨轮复用：一次测量就是一个 ~50MB 的 Chrome profile，
   以前每次 mkdtemp 新建一个、从不清理，OS 临时目录里攒了一堆。
   这个探针本来就该独占跑（同时跑两个 Chrome 量帧率没有意义），固定目录不会撞。 */
const profile = 'F:/tmp/uiprobe-pool/fps-reveal'
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
if (DSF !== 1) await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: DSF, mobile: false }, sid)
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
for (let i = 0; i < 40; i++) { if (await ev(`document.querySelectorAll('#tabbar .tab').length>4`)) break; await sleep(500) }
await sleep(1500)
if (MODE === 'noanim') await ev(`document.body.classList.add('no-anim')`)

const run = await ev(`new Promise(res=>{
  const tabs=[...document.querySelectorAll('#tabbar .tab')];
  const t=[]; let last=performance.now(), n=0, clicks=0;
  const tick=()=>{ const now=performance.now(); t.push(now-last); last=now;
    if(!${IDLE} && (n===10||n===70||n===130||n===190)){ const b=tabs[2+clicks]; if(b){b.click();clicks++} }
    if(++n<260) requestAnimationFrame(tick);
    else { const s=[...t].sort((a,b)=>a-b);
      res({dpr:devicePixelRatio, clicks, n:t.length,
        p50:+s[Math.floor(s.length*.5)].toFixed(1), p95:+s[Math.floor(s.length*.95)].toFixed(1), max:+s[s.length-1].toFixed(1),
        fps:+(1000/s[Math.floor(s.length*.5)]).toFixed(1), over25:t.filter(x=>x>25).length, over40:t.filter(x=>x>40).length}) } };
  requestAnimationFrame(tick);
})`)
console.log('REVEAL', JSON.stringify(run))
const rest = await ev(`(()=>{const cv=[...document.querySelectorAll('canvas')].find(c=>c.getAttribute('aria-hidden')==='true');return {cvHidden:!cv||cv.style.visibility==='hidden', page:document.querySelector('.page').dataset.page}})()`)
console.log('AFTER ', JSON.stringify(rest))

ws.close(); chrome.kill()
