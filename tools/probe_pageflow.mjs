/*
  承接式切页探针 —— 验证「外壳与页签不动、内容上下交接、指示条滑行」真的成立。

  为什么这么写（踩过的坑）：
   ① 转场只有 330ms，CDP 两次往返就把窗口推过去了 —— 采样必须和点击跑在**同一条 rAF 时间线**上；
   ② 只靠 sleep + 截图抢不到中间帧（截图本身耗时 100ms+）——
      定格用 document.getAnimations() 暂停 + 手拨 currentTime，想定哪帧定哪帧；
   ③ 判据是数值（指示条 left / content 的 opacity+translateY 时间序列），PNG 只是给人看的。

  用法: node tools/probe_pageflow.mjs <URL> [outDir]
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) {
  console.error('用法: node tools/probe_pageflow.mjs <URL> [outDir]')
  process.exit(2)
}
const OUT = process.argv[3] || 'F:/tmp/pageflow'
mkdirSync(OUT, { recursive: true })

/* 固定 profile 跨轮复用：避免每跑一次留一个 ~50MB 的 Chrome profile。
   ⚠ 复用前必须清掉上一轮遗留的 DevToolsActivePort —— 里面是**旧端口**，
     读到它就会去连一个已经死掉的调试口（症状是 ECONNREFUSED，看起来像 Chrome 没起来）。 */
const profile = 'F:/tmp/uiprobe-pool/pageflow'
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
  } catch { /* 还没起来 / 端口还没写全，继续等 */ }
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
const shot = async (name) => {
  const r = await send('Page.captureScreenshot', { format: 'png' }, sid)
  const b = Buffer.from(r.result.data, 'base64')
  writeFileSync(join(OUT, name + '.png'), b)
  console.log('SHOT  ', name, b.length)
}

async function boot() {
  await send('Page.navigate', { url: URL_ }, sid)
  for (let i = 0; i < 40; i++) {
    if (await ev(`!!document.querySelector('#tabbar .tab') && !!document.querySelector('.tab-glide') && document.querySelector('.page')?.dataset.page !== undefined`)) break
    await sleep(400)
  }
  await sleep(1200)
}

/* ── ① 数值：连点一条 rAF 线，记录「值一变就记」的时间序列 ────────────── */
await boot()
const flow = await ev(`new Promise(res=>{
  const ind=document.querySelector('.tab-glide');
  const content=document.querySelector('.content');
  const tabs=[...document.querySelectorAll('#tabbar .tab')];
  const target=tabs.find(b=>b.textContent.includes('插件'));
  const t0=performance.now();
  const log=[]; let last='';
  const snap=(force)=>{
    const cs=getComputedStyle(content);
    const is=getComputedStyle(ind||document.body);
    const tf=cs.transform==='none'?0:Math.round(parseFloat(cs.transform.split(',')[5]||'0'));
    const page=(document.querySelector('.page')||{dataset:{}}).dataset.page||'?';
    const row=[Math.round(performance.now()-t0),page,+(+cs.opacity).toFixed(2),tf,Math.round(parseFloat(is.left)||-1),+(+is.opacity).toFixed(2)];
    const key=row.join('|');
    if(force||key!==last){log.push(row);last=key}
  };
  snap(true);
  if(target)target.click();
  let n=0;
  const tick=()=>{snap();if(++n<110)requestAnimationFrame(tick);else res({log,indFound:!!ind,targetFound:!!target,tabs:tabs.length})};
  requestAnimationFrame(tick);
})`)
console.log('FLOW  ', JSON.stringify(flow))

/* ── ② 定格截图：要拍两张中间帧 ──────────────────────────────
   ① 旧内容下沉中（out 动画 65ms 处，此时还没 swap）
   ② 新内容接进中（inn 动画 90ms 处）
   ⚠ 不能用「点击 → sleep → 截图」抓 ①：out 只有 130ms 窗口，而 CDP 截图本身要 100ms+，
     等截完 swap 早发生了（2026-10-10 实测抓了个空）。正解：
     a) 点击后**同一个同步块里**暂停动画 + 定格 currentTime —— 保证 ① 那一刻被冻住；
     b) 把 swap 的 setTimeout(130) 临时拦下来 —— 否则截图期间它照常触发、把定格冲掉；
     c) 截完再放行，接着定格 ② 的 inn 动画。 */
await boot()
const held1 = await ev(`(()=>{
  window.__origST=window.setTimeout; window.__pend=null;
  window.setTimeout=function(fn,ms){ if(ms===130){window.__pend=fn;return 0} return window.__origST.apply(this,arguments) };
  const target=[...document.querySelectorAll('#tabbar .tab')].find(b=>b.textContent.includes('插件'));
  target.click();
  /* ⚠ 只定格「这次转场自己的」动画（content 的 out + 指示条 transition）。
     别 forEach 全暂停 —— 页面里还有卡片入场等带 delay 的动画，
     一起拽回 65ms 会把它们打回「delay 都没到」的隐形状态，整页看起来像空的（2026-10-10 实测）。 */
  const c=document.querySelector('.content');
  document.getAnimations().forEach(a=>{const t=a.effect&&a.effect.target;
    if(t===c||(t&&t.classList&&t.classList.contains('tab-glide'))){a.pause();a.currentTime=65}});
  return {pend:!!window.__pend,paused:document.getAnimations().filter(a=>a.playState==='paused').length};
})()`)
console.log('HOLD1 ', JSON.stringify(held1))
await shot('1-old-sinking')
const held2 = await ev(`(async()=>{
  window.setTimeout=window.__origST;
  if(window.__pend)window.__pend();
  await new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)));
  const c=document.querySelector('.content');
  const acts=document.getAnimations().filter(a=>a.effect&&(a.effect.target===c||(a.effect.target&&a.effect.target.classList&&a.effect.target.classList.contains('tab-glide'))));
  acts.forEach(a=>{a.pause();a.currentTime=90});
  return {n:acts.length};
})()`)
console.log('HOLD2 ', JSON.stringify(held2))
await shot('2-new-rising')
await ev(`(async()=>{document.getAnimations().forEach(a=>a.play());await new Promise(r=>setTimeout(r,800));return 1})()`)
await shot('3-settled')
console.log('SETTLE', JSON.stringify(await ev(`(()=>{const c=document.querySelector('.content');const cs=getComputedStyle(c);return{page:document.querySelector('.page').dataset.page,opacity:+cs.opacity,transform:cs.transform,ind:document.querySelector('.tab-glide').style.left}})()`)))

/* ── ③ 连点：4 下快速切页，结束后不许有残留（透明/位移/动画） ─────────── */
await boot()
const busy = await ev(`new Promise(res=>{
  const tabs=[...document.querySelectorAll('#tabbar .tab')];
  const order=[0,2,4,1];
  let k=0;
  const fire=()=>{ if(k<order.length){ const b=tabs[order[k]]; if(b)b.click(); k++; setTimeout(fire,70) } };
  fire();
  setTimeout(()=>{
    const c=document.querySelector('.content');
    const cs=getComputedStyle(c);
    res({page:(document.querySelector('.page')||{dataset:{}}).dataset.page,opacity:+cs.opacity,transform:cs.transform,anims:document.getAnimations().filter(a=>a.effect&&a.playState==='running').length});
  },1100);
})`)
console.log('BUSY  ', JSON.stringify(busy))

/* ── ④ no-anim：挂上开关后点击，必须瞬时到位、不产生动画 ──────────────── */
await boot()
const noanim = await ev(`new Promise(res=>{
  document.body.classList.add('no-anim');
  const ind=document.querySelector('.tab-glide');
  const before=getComputedStyle(ind).left;
  const t=[...document.querySelectorAll('#tabbar .tab')].find(b=>b.textContent.includes('插件'));
  t.click();
  requestAnimationFrame(()=>{
    const c=document.querySelector('.content');const cs=getComputedStyle(c);
    const after=getComputedStyle(ind).left;
    res({page:document.querySelector('.page').dataset.page,opacity:+cs.opacity,transform:cs.transform,moved:before!==after,anims:document.getAnimations().filter(a=>a.playState==='running').length});
  });
})`)
console.log('NOANIM', JSON.stringify(noanim))

/* ── ⑤ 设置页：不在这一排，指示条要收起来 ──────────────────────────── */
await send('Page.navigate', { url: URL_.replace(/([?&])page=[^&]*/, '$1page=settings') }, sid)
for (let i = 0; i < 40; i++) { if (await ev(`document.querySelector('.page')?.dataset.page === 'settings'`)) break; await sleep(400) }
await sleep(900)
console.log('SETTNG', JSON.stringify(await ev(`(()=>({page:document.querySelector('.page').dataset.page,indOpacity:document.querySelector('.tab-glide').style.opacity,onTabs:document.querySelectorAll('#tabbar .tab.on').length}))()`)))

ws.close(); chrome.kill()
