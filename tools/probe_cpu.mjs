/*
  星图卡顿定位：抓 CPU profile，按函数聚合 self time，找出真正吃 CPU 的热点。
  用法: node tools/probe_cpu.mjs <URL> [ms]
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const MS = Number(process.argv[3] || 4000)
const profile = mkdtempSync(join(tmpdir(), 'cpuprof-'))
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
await sleep(1200)

// 页面里自己量 rAF 回调的**自耗时**（这才是「我们的代码吃多少」）
const selfMs = await ev(`new Promise(res=>{
  const durs=[]; let n=0;
  const tick=()=>{ const t0=performance.now();
    requestAnimationFrame(()=>{ durs.push(performance.now()-t0); if(++n<120) tick(); else {
      durs.sort((a,b)=>a-b);
      res({p50:durs[60].toFixed(2), p95:durs[113].toFixed(2), max:durs[119].toFixed(2)})
    }})
  }; tick();
})`)
console.log('RAF_INTERVAL', JSON.stringify(selfMs))

// CPU profile：按函数聚合 self time
await send('Profiler.enable', {}, sid)
await send('Profiler.setSamplingInterval', { interval: 200 }, sid)
await send('Profiler.start', {}, sid)
await sleep(MS)
const { result: { profile: prof } } = await send('Profiler.stop', {}, sid)

const byId = new Map()
for (const n of prof.nodes) byId.set(n.id, n)
const self = new Map()
const total = prof.samples.length
for (const s of prof.samples) {
  const n = byId.get(s)
  if (!n) continue
  const f = n.callFrame
  const key = `${f.functionName || '(anonymous)'} @${(f.url || '').split('/').pop()}:${f.lineNumber}`
  self.set(key, (self.get(key) || 0) + 1)
}
const top = [...self.entries()].sort((a, b) => b[1] - a[1]).slice(0, 14)
console.log(`SAMPLES ${total} over ${MS}ms`)
for (const [k, v] of top) console.log(`  ${(v / total * 100).toFixed(1).padStart(5)}%  ${v.toString().padStart(5)}  ${k}`)

ws.close(); chrome.kill()
