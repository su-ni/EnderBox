/*
  石头拼合过场探针 —— 验证「两侧先来 → 拼到中间 → 拼满闪一下 → 换内容 → 按序散场 → 收尾」。

  ── 为什么是手摇时钟 ────────────────────────────────────────────────
    ① 石头是**一块 canvas 逐帧画**的，不是 CSS/WAAPI 动画 —— document.getAnimations() 看不见它。
       正解：把手摇柄换上 —— requestAnimationFrame 换成收队列的实现，__step(16) 一帧一帧点名，
       时间线完全由探针说了算（定格想停哪停哪）。
    ② 判据用「语义定格」：步进到**采样画布出现目标图案**为止（如 cov[0]>60 && cov[8]>60
       && cov[1]<=60 ＝「两侧先来、中间还没到」），不死磕第几毫秒。
    ③ 采样：9 列中心各取 0.6 格宽小块的 R 通道最大值 —— 砖的灰 (≥90) 与空槽 (#15181d, ~21)
       差一个量级，阈值 60 稳。

  ── 三个踩过的坑（2026-10-10 实锤，别再踩）────────────────────────────
    ① **guard 的 ms 是 1892，不是 1836。** 源码 S(17) 会被 Math.max(24,·) 抬到 24 →
       T_FULL=1462、T_SWAP=1642、guard=T_SWAP+250=**1892**。硬编码 1836 会漏拦，
       guard 在真实 1892ms 处提前换页（症状：还没拼满就出现新界面）。
       → 本探针按 1892 拦，并且把**所有** setTimeout 调用记进 __st 输出 —— 真变了看得见。
    ② **假 cancelAnimationFrame 绝不能"清全队列"。** 换页时 React commit 会跑被删子树里
       某数字滚动组件的 cleanup → cancelAnimationFrame(它自己的帧)；清全队列会把石头自己的
       pending 帧一并误杀（症状：画面定格在某一帧、Canvas 永不休止）。→ 按 id 记账（Map）。
    ③ guard 拦下来后永远不补放 —— 它的职责（rAF 停摆时兜底换内容）完全由假时钟接管。

  用法: node tools/probe_stone.mjs <URL> [outDir]
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) {
  console.error('用法: node tools/probe_stone.mjs <URL> [outDir]')
  process.exit(2)
}
const OUT = process.argv[3] || 'F:/tmp/stone'
mkdirSync(OUT, { recursive: true })

/* 固定 profile 跨轮复用；⚠ 复用前必须清 DevToolsActivePort（残留会读到旧端口 → ECONNREFUSED） */
const profile = 'F:/tmp/uiprobe-pool/stone'
mkdirSync(profile, { recursive: true })
const pf = join(profile, 'DevToolsActivePort')
try { unlinkSync(pf) } catch { /* 没有残留，正常 */ }
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
  } catch { /* 还没起来，继续等 */ }
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
/* HiDPI：dSF=2 —— 顺带覆盖高分辨率下的纹理/尺寸路径 */
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 2, mobile: false }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
const shot = async (name) => {
  const r = await send('Page.captureScreenshot', { format: 'png' }, sid)
  const b = Buffer.from(r.result.data, 'base64')
  writeFileSync(join(OUT, name + '.png'), b)
  console.log('SHOT  ', name, b.length)
}

async function boot() {
  await send('Page.navigate', { url: URL_ }, sid)
  for (let i = 0; i < 50; i++) {
    if (await ev(`[...document.querySelectorAll('.side button')].some(b=>b.textContent.includes('新建世界'))`)) break
    await sleep(400)
  }
  await sleep(1200)
}

/* ── 手摇柄安装：换掉 rAF / 拦 guard / 备好采样与步进助手 ─────────────────── */
const INSTALL = `(()=>{
  const w = window;
  w.__origRAF = w.requestAnimationFrame.bind(w);
  w.__origCAF = w.cancelAnimationFrame.bind(w);
  w.__origST = w.setTimeout.bind(w);
  w.__q = new Map(); w.__rid = 0; w.__t = performance.now();
  w.__guard = null; w.__swallowed = []; w.__st = []; w.__err = []; w.__hitCov = null;
  w.addEventListener('error', e => w.__err.push(String(e.message || e.error || 'err')));
  /* 按 id 记账：别人取消自己的帧，不许连坐（坑②） */
  w.requestAnimationFrame = function(cb){ const i = ++w.__rid; w.__q.set(i, cb); return i };
  w.cancelAnimationFrame = function(id){ w.__q.delete(id) };
  /* guard ms = T_SWAP(1642) + 250 = 1892（坑①：S(17)→24 的下限）—— 只拦这一个，且全量记账 */
  w.setTimeout = function(fn, ms){
    if (w.__st.length < 80) w.__st.push({ ms, src: String(fn).slice(0, 48) });
    if (ms === 1892) { w.__swallowed.push(ms); w.__guard = fn; return -1 }
    return w.__origST(fn, ms);
  };
  w.__step = function(dt){
    w.__t += dt;
    const batch = [...w.__q.values()]; w.__q.clear();
    for (const cb of batch) { try { cb(w.__t) } catch (e) { w.__err.push('raf:' + e.message) } }
  };
  w.__findCv = function(){ return [...document.querySelectorAll('canvas')].find(c => c.style.position === 'fixed' && c.style.zIndex === '70' && c.getAttribute('aria-hidden') === 'true') || null };
  w.__cov = function(){
    const c = w.__findCv(); if (!c) return null;
    const g = c.getContext('2d'); if (!g) return null;
    const dpr = Math.round((devicePixelRatio || 1) * 100) / 100;
    const vw = innerWidth, vh = innerHeight;
    /* 同 stoneBuild 的几何：cell = clamp(floor(min(vw,vh)*0.82/9), 36, 96) */
    const cell = Math.max(36, Math.min(96, Math.floor((Math.min(vw, vh) * 0.82) / 9)));
    const span = cell * 9;
    const x0 = Math.round((vw - span) / 2), y0 = Math.round((vh - span) / 2);
    const out = [];
    for (let i = 0; i < 9; i++){
      const cx = Math.round((x0 + i * cell + cell / 2) * dpr);
      const cy = Math.round((y0 + 4 * cell + cell / 2) * dpr);   /* 正中那行 */
      const R = Math.round(cell * 0.3 * dpr);
      let m = 0;
      try {
        const img = g.getImageData(cx - R, cy - R, R * 2, R * 2).data;
        for (let k = 0; k < img.length; k += 16) if (img[k] > m) m = img[k];
      } catch (e) { w.__err.push('cov:' + e.message); m = -1 }
      out.push(m);
    }
    return out;
  };
  w.__until = async function(cond, max){
    let i = 0;
    while (i++ < max){
      if (cond()) return { ok: true, i };
      w.__step(16);
      if (i % 4 === 0) await new Promise(r => w.__origST(r, 0));   /* 让 React 有机会刷 DOM */
    }
    return { ok: false, i };
  };
  w.__restore = function(){ w.requestAnimationFrame = w.__origRAF; w.cancelAnimationFrame = w.__origCAF; w.setTimeout = w.__origST };
  return true;
})()`

/* ── ① 拼合全流程定格（主戏）───────────────────────────────────────── */
await boot()
const patched = await ev(INSTALL)
const click = await ev(`(()=>{
  const scope = document.querySelector('.side') || document;
  const btn = [...scope.querySelectorAll('button')].find(b => b.textContent.includes('新建世界'));
  if (!btn) return { found: false };
  window.__t = performance.now();          /* 对表：stoneBuild 马上会读 performance.now() 当 t0 */
  btn.click();
  window.__step(16); window.__step(16);
  const c = window.__findCv();
  return { found: true, hidPI: devicePixelRatio, cv: !!c, vis: c && c.style.visibility, cw: c && c.width, q: window.__q.size };
})()`)
console.log('CLICK ', JSON.stringify(click), 'patched=', patched)

const A = await ev(`(async()=>{
  const r = await window.__until(()=>{ const c = window.__cov();
    if (c && c[0] > 60 && c[8] > 60 && c[1] <= 60) { window.__hitCov = c.slice(); return true } return false }, 400);
  return { r, cov: window.__hitCov, vis: (window.__findCv()||{style:{}}).style.visibility };
})()`)
console.log('A两侧先来', JSON.stringify(A))
await shot('1-sides-first')

const B = await ev(`(async()=>{
  const r = await window.__until(()=>{ const c = window.__cov();
    if (c && c[2] > 60 && c[3] <= 60) { window.__hitCov = c.slice(); return true } return false }, 400);
  return { r, cov: window.__hitCov };
})()`)
console.log('B中间还空', JSON.stringify(B))
await shot('2-middle-still-empty')

const C = await ev(`(async()=>{
  const r = await window.__until(()=>{ const c = window.__cov();
    if (c && c.every(v => v > 60)) { window.__hitCov = c.slice(); return true } return false }, 400);
  for (let i = 0; i < 12; i++) window.__step(16);   /* 再走 ~190ms，进整片闪白窗口 */
  return { r, cov: window.__hitCov, wzBeforeSwap: !!document.querySelector('.wz-shell') };
})()`)
console.log('C拼满闪白', JSON.stringify(C))
await shot('3-full-flash')

const D = await ev(`(async()=>{
  const r = await window.__until(()=> !!document.querySelector('.wz-shell'), 300);
  for (let i = 0; i < 12; i++) window.__step(16);   /* 散场起步：两边的先走（vanish 从 colOrd 0/1 开始） */
  return { r, cov: window.__cov(), wz: true };
})()`)
console.log('D散场起步', JSON.stringify(D))
await shot('4-vanish-early')

const E = await ev(`(async()=>{
  for (let i = 0; i < 15; i++) window.__step(16);
  await new Promise(r => window.__origST(r, 60));
  return { cov: window.__cov() };
})()`)
console.log('E散场后段', JSON.stringify(E))
await shot('5-vanish-late')

const F = await ev(`(async()=>{
  const r = await window.__until(()=>{ const c = window.__findCv(); return !!(c && c.style.visibility === 'hidden') }, 300);
  return { r, cov: window.__cov(), wz: !!document.querySelector('.wz-shell'), err: window.__err, swallowed: window.__swallowed, stTail: window.__st.slice(0, 6), q: window.__q.size };
})()`)
console.log('F收尾', JSON.stringify(F))
if (JSON.stringify(F.swallowed) !== '[1892]') console.log('WARN  guard 拦截异常:', JSON.stringify(F.swallowed))
await ev(`window.__restore()`)
await ev(`new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))`)
await sleep(400)
await shot('6-settled-wizard')

/* ── ② 连点护栏：取消（石头#1）中途立刻「新建世界」（石头#2）—— 旧的作废、新的接管 ── */
await ev(INSTALL)
const DBL = await ev(`(async()=>{
  const pick = (t) => [...document.querySelectorAll('button')].find(b => b.textContent.includes(t));
  window.__t = performance.now();
  pick('返回世界列表').click();                       /* 石头 #1：回面板 */
  window.__step(16); window.__step(16); window.__step(16);
  const mid1 = window.__findCv() && window.__findCv().style.visibility;   /* 应 visible：正在拼 */
  window.__t = performance.now();                     /* 对表给 #2 */
  pick('新建世界').click();                           /* 石头 #2：立刻取消 #1 */
  const r = await window.__until(()=>{ const c = window.__findCv(); return !!document.querySelector('.wz-shell') && !!(c && c.style.visibility === 'hidden') }, 400);
  const canvases = [...document.querySelectorAll('canvas')].filter(c => c.style.zIndex === '70').length;
  return { mid1, r, canvases, wz: !!document.querySelector('.wz-shell'), err: window.__err, swallowed: window.__swallowed.length };
})()`)
console.log('DBL连点 ', JSON.stringify(DBL))
await ev(`window.__restore()`)
await ev(`new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))`)
await sleep(300)
await shot('7-doubleclick-settled')

/* ── ③ no-anim：关掉动效 → 点击必须瞬时切换、不排一帧动画 ─────────────────── */
await ev(INSTALL)
const NA = await ev(`(async()=>{
  document.body.classList.add('no-anim');
  const pick = (t) => [...document.querySelectorAll('button')].find(b => b.textContent.includes(t));
  const qBefore = window.__q.size;
  pick('返回世界列表').click();
  await new Promise(r => window.__origST(r, 150));
  const c = window.__findCv();
  return { wz: !!document.querySelector('.wz-shell'), vis: c && c.style.visibility, qDelta: window.__q.size - qBefore, swallowed: window.__swallowed.length };
})()`)
console.log('NOANIM ', JSON.stringify(NA))
await ev(`document.body.classList.remove('no-anim')`)
await ev(`window.__restore()`)
await sleep(250)
await shot('8-noanim-board')

/* ── ④ 往返全程（不截图，只记数）：面板→新建 走完整条，再从向导「返回」走完整条 ── */
await ev(INSTALL)
const RT = await ev(`(async()=>{
  const pick = (t) => [...document.querySelectorAll('button')].find(b => b.textContent.includes(t));
  window.__t = performance.now();
  pick('新建世界').click();
  const a = await window.__until(()=>{ const c = window.__findCv(); return !!document.querySelector('.wz-shell') && !!(c && c.style.visibility === 'hidden') }, 400);
  window.__t = performance.now();
  pick('返回世界列表').click();
  const b = await window.__until(()=>{ const c = window.__findCv(); return !document.querySelector('.wz-shell') && !!(c && c.style.visibility === 'hidden') }, 400);
  return { toCreate: a, toBoard: b, wz: !!document.querySelector('.wz-shell'), err: window.__err, swallowed: window.__swallowed.length };
})()`)
console.log('ROUNDTRIP', JSON.stringify(RT))
await ev(`window.__restore()`)

console.log('SUMMARY', JSON.stringify({ click, A: A.r, B: B.r, C: C.r, D: D.r, E: E.cov, F: F.r, guard: F.swallowed, dbl: DBL.r, na: NA, rt: RT }))
ws.close(); chrome.kill()
