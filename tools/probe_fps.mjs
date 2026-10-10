/*
  帧率与主线程占用测量：在页面里挂钩 rAF，统计 2 秒内的帧间隔分布。
  同时统计长任务（longtask）与 layout/style 重算次数。
  用法: node tools/probe_fps.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'fp-'))
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
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(2000)

const r = await ev(`new Promise(res=>{
  const times=[]; let long=0;
  try{ new PerformanceObserver(l=>{for(const e of l.getEntries())long+=e.duration}).observe({entryTypes:['longtask']}) }catch{}
  let n=0; const t0=performance.now();
  const tick=(now)=>{ times.push(now); if(++n<140) requestAnimationFrame(tick); else {
    const iv=[]; for(let i=1;i<times.length;i++) iv.push(times[i]-times[i-1]);
    iv.sort((a,b)=>a-b);
    const avg=iv.reduce((a,b)=>a+b,0)/iv.length;
    const p50=iv[Math.floor(iv.length*0.5)], p95=iv[Math.floor(iv.length*0.95)];
    res({frames:iv.length, avgMs:+avg.toFixed(2), fps:+(1000/avg).toFixed(1), p50:+p50.toFixed(2), p95:+p95.toFixed(2), max:+iv[iv.length-1].toFixed(2), longTaskMs:Math.round(long), spanMs:Math.round(now-t0)})
  }};
  requestAnimationFrame(tick);
})`)
console.log('FPS', JSON.stringify(r))
ws.close(); chrome.kill()
