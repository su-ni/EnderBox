/*
  石头动画取证脚本（一次性诊断，不是回归探针）—— 回答三个问题：
    ① guard 的 window.setTimeout(..., b+250) 到底有没有走我们 patch 的入口？ms 是多少？
    ② 谁在调 cancelAnimationFrame？（嫌疑：换页重渲染时某组件的 rAF 清理把我们手摇队列整个清空）
    ③ 石头 step 链什么时候断的？（靠 clearRect 计数器：每帧必 clear 一次）

  用法: node tools/diag_stone.mjs <URL>
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) { console.error('用法: node tools/diag_stone.mjs <URL>'); process.exit(2) }

const profile = 'F:/tmp/uiprobe-pool/stone-diag'
mkdirSync(profile, { recursive: true })
const pf = join(profile, 'DevToolsActivePort')
try { unlinkSync(pf) } catch { /* ok */ }
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
let ws0 = ''
for (let i = 0; i < 80; i++) {
  try {
    const port = Number(readFileSync(pf, 'utf8').split('\n')[0])
    if (port > 0) {
      const r = await fetch(`http://127.0.0.1:${port}/json/version`)
      const j = await r.json()
      if (j.webSocketDebuggerUrl) { ws0 = j.webSocketDebuggerUrl; break }
    }
  } catch { /* wait */ }
  await new Promise(r => setTimeout(r, 200))
}
if (!ws0) { console.error('Chrome 调试端口一直没起来'); process.exit(3) }
const ws = new WebSocket(ws0)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0; const pend = new Map()
ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } })
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid); await send('Runtime.enable', {}, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value

await send('Page.navigate', { url: URL_ }, sid)
for (let i = 0; i < 50; i++) {
  if (await ev(`[...document.querySelectorAll('.side button')].some(b=>b.textContent.includes('新建世界'))`)) break
  await sleep(400)
}
await sleep(1200)

/* ── 安装「只记录、不拦截」的取证补丁 ─────────────────────────────────── */
const INS = `(()=>{
  const w = window;
  w.__origRAF = w.requestAnimationFrame.bind(w);
  w.__origST = w.setTimeout.bind(w);
  w.__q = []; w.__t = performance.now();
  w.__st = []; w.__caf = []; w.__clrN = 0; w.__rafN = 0; w.__err = [];
  w.addEventListener('error', e => w.__err.push(String(e.message || e.error || 'err')));
  /* setTimeout：全量记录（ms + 调用栈），照常放行 */
  w.setTimeout = function(fn, ms){
    let stack;
    try { stack = String(new Error().stack).split('\\n').slice(1, 4).map(s => s.trim().slice(0, 130)); } catch {}
    w.__st.push({ ms, at: Math.round(performance.now()), src: String(fn).slice(0, 56), stack });
    return w.__origST(fn, ms);
  };
  w.requestAnimationFrame = function(cb){ w.__rafN++; w.__q.push(cb); return w.__q.length };
  /* cAF：全量记录（含栈）——故意保留「清全队列」原始行为，复现 bug */
  w.cancelAnimationFrame = function(id){
    let stack;
    try { stack = String(new Error().stack).split('\\n').slice(1, 7).map(s => s.trim().slice(0, 150)); } catch {}
    w.__caf.push({ id, at: Math.round(performance.now()), qlen: w.__q.length, stack });
    w.__q = [];
  };
  w.__step = function(dt){ w.__t += dt; const q = w.__q; w.__q = []; for (const cb of q) { try { cb(w.__t) } catch (e) { w.__err.push('raf:' + e.message) } } };
  w.__findCv = function(){ return [...document.querySelectorAll('canvas')].find(c => c.style.position === 'fixed' && c.style.zIndex === '70') || null };
  const origClear = CanvasRenderingContext2D.prototype.clearRect;
  CanvasRenderingContext2D.prototype.clearRect = function(){ w.__clrN++; return origClear.apply(this, arguments) };
  return { now: String(w.setTimeout).slice(0, 40) };
})()`
console.log('INST  ', JSON.stringify(await ev(INS)))

/* ── 点击 → 快速步进 150 帧（fake t≈2400，越过换页/收尾）→ 记录每批状态 ── */
const run = await ev(`(async()=>{
  const pick = (t) => [...document.querySelectorAll('button')].find(b => b.textContent.includes(t));
  const btn = pick('新建世界');
  window.__t = performance.now();
  const rt0 = performance.now();
  btn.click();
  const a0 = window.__findCv();
  const seq = [];
  for (let b = 0; b < 15; b++){
    for (let i = 0; i < 10; i++) window.__step(16);
    await new Promise(r => window.__origST(r, 0));
    const c = window.__findCv();
    seq.push({
      rt: Math.round(performance.now() - rt0),
      clears: window.__clrN,
      q: window.__q.length,
      wz: !!document.querySelector('.wz-shell'),
      vis: c && c.style.visibility,
      rafN: window.__rafN,
    });
  }
  /* 补一刀：直连测试拦截入口是否生效 */
  const before = window.__st.length;
  window.setTimeout(function directProbe(){}, 1836);
  return { seq, direct1836: window.__st.length - before, err: window.__err };
})()`)
console.log('RUN   ', JSON.stringify(run, null, 1))

/* 步进完成后，真实空等 2.6s：看 guard（若有）会不会在真实时间上补刀 */
await sleep(2600)
const after = await ev(`(()=>{
  const c = window.__findCv();
  return { st: window.__st, caf: window.__caf, clrN: window.__clrN, q: window.__q.length,
           wz: !!document.querySelector('.wz-shell'), vis: c && c.style.visibility,
           err: window.__err, rafN: window.__rafN };
})()`)
console.log('ST    ', JSON.stringify(after.st))
console.log('CAF   ', JSON.stringify(after.caf))
console.log('AFTER ', JSON.stringify({ clrN: after.clrN, q: after.q, wz: after.wz, vis: after.vis, err: after.err, rafN: after.rafN }))

ws.close(); chrome.kill()
