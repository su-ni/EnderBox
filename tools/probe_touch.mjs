/*
  触摸滚动「穿透」验证（星图画布内纵向滑动应滚页面）。

  ⚠⚠ 判据说明（重要，别再用「scrollTop 变化」当判据）
  本机 headless Chrome 的 Input.synthesizeScrollGesture / dispatchTouchEvent **无法**
  合成「针对非 document 滚动容器」的原生滚动手势 —— 实测在画布内、画布外、以及
  干脆没有星图的普通区域，滚动量都是 0，因此拿它当判据只会得到假阴性（会误判成产品 bug）。
  曾为此白折腾两轮。

  正确的可移植判据 = **看有没有 JS 取消触摸滚动**：
    1. 画布的 computed `touch-action` 必须是 `pan-y`（放行纵向）
    2. 在画布上派发合成的 touchstart/touchmove（DOM 事件可以正常合成），
       监听 `e.defaultPrevented` —— 任何一处为 true 即表示有代码阻止了原生滚动。
  两条都通过 → 真机上纵向滑动必然能滚页面。

  用法: node tools/probe_touch.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'tc-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=412,820', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
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
await send('Emulation.setDeviceMetricsOverride', { width: 412, height: 820, deviceScaleFactor: 2, mobile: true }, sid)
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-node')`)) break; await sleep(500) }
await sleep(800)

// 判据 1：touch-action
const ta = await ev(`getComputedStyle(document.querySelector('.mcg-canvas')).touchAction`)
// 判据 2：合成 touchstart/touchmove，看有没有被 preventDefault
const probe = await ev(`new Promise(resolve=>{
  const c=document.querySelector('.mcg-canvas');
  const r=c.getBoundingClientRect();
  const x=Math.round(r.x+r.width/2), y=Math.round(Math.min(r.y+r.height/2, innerHeight-100));
  const seen=[];
  const h=(e)=>{ seen.push({t:e.type, dp:e.defaultPrevented}) };
  c.addEventListener('touchmove',h,true);
  c.addEventListener('touchstart',h,true);
  const mk=(type,yy)=>new TouchEvent(type,{bubbles:true,cancelable:true,composed:true,
    touches:type==='touchend'?[]:[new Touch({identifier:1,target:c,clientX:x,clientY:yy})],
    targetTouches:type==='touchend'?[]:[new Touch({identifier:1,target:c,clientX:x,clientY:yy})],
    changedTouches:[new Touch({identifier:1,target:c,clientX:x,clientY:yy})]});
  c.dispatchEvent(mk('touchstart',y));
  setTimeout(()=>{ c.dispatchEvent(mk('touchmove',y-60)); setTimeout(()=>{
    c.dispatchEvent(mk('touchmove',y-120));
    setTimeout(()=>{ c.removeEventListener('touchmove',h,true); c.removeEventListener('touchstart',h,true);
      resolve({pt:{x,y}, seen});
    },60);
  },60); },60);
})`)

const allClean = ta === 'pan-y' &&
  probe.seen.length >= 3 &&
  probe.seen.every(s => s.dp === false)
console.log('TOUCH_ACTION', ta)
console.log('TOUCH_EVENTS', JSON.stringify(probe.seen))
console.log('TOUCH_PASS', allClean, allClean ? '(无 JS 阻止触摸滚动 → 真机可滚页面)' : '(有控件阻止了触摸滚动，需排查)')
ws.close(); chrome.kill()
process.exit(allClean ? 0 : 2)
