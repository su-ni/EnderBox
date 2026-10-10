/*
  中心节点点击诊断 v4：给 trace 加上 pointermove（带坐标）/ pointercancel /
  got·lostpointercapture —— v3 已证明失败时浏览器根本没合成 click 事件，
  现在要看「down 和 up 之间到底发生了什么」。
  两个场景各测一遍：
    S1: 点普通节点 → 关面板 → 点中心
    S2: 点普通节点 → 关面板 → 复位 → 点中心（panel3 失败的那个序列）
  用法: node tools/probe_center_click.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'ctr4-'))
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
ws.addEventListener('message', e => {
  const m = JSON.parse(e.data)
  if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') console.log('JS_EXCEPTION', JSON.stringify(m.params?.exceptionDetails?.exception?.description || m.params?.exceptionDetails).slice(0, 600))
})
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid); await send('Runtime.enable', {}, sid)
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value

const state = `(()=>{
  const p=document.querySelector('.mcg-node.is-picked');
  return {panel:!!document.querySelector('.mcg-side'), pickedNid:p?p.getAttribute('data-nid'):null}
})()`
const clickAt = async (p) => {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: p.x, y: p.y, button: 'left', clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: p.x, y: p.y, button: 'left', clickCount: 1 }, sid)
}
const centerXY = `(()=>{
  const nd=document.querySelector('.mcg-canvas .mcg-node.is-center'); if(!nd) return null;
  const b=nd.querySelector('circle').getBoundingClientRect();
  return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}
})()`
const traceDump = async (from) => JSON.stringify((await ev(`window.__trace||[]`)).slice(from))

for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1500)
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(500)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(400)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1200)

// 全事件 trace（含坐标）
await ev(`(()=>{
  window.__trace=[];
  ['pointerdown','pointerup','pointermove','pointercancel','gotpointercapture','lostpointercapture','mousedown','mouseup','click'].forEach(t=>{
    window.addEventListener(t, e=>{
      const g=e.target&&e.target.closest?e.target.closest('.mcg-node'):null;
      window.__trace.push(t+'('+Math.round(e.clientX||0)+','+Math.round(e.clientY||0)+')@'+(g?('node'+g.getAttribute('data-nid')):'?'));
    }, true)
  });
  return 1
})()`)

const clickBig = async () => {
  const big = await ev(`(()=>{
    const nodes=[...document.querySelectorAll('.mcg-canvas .mcg-node')].filter(n=>!n.classList.contains('is-center'));
    let best=null,br=-1;
    for(const nd of nodes){const c=nd.querySelector('circle');const r=+c.getAttribute('r');const b=c.getBoundingClientRect();if(r>br&&b.top>60&&b.bottom<innerHeight-60){br=r;best=c}}
    if(!best)return null;
    const b=best.getBoundingClientRect();
    return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}
  })()`)
  await clickAt(big)
  await sleep(900)
}

// ───────── S1: 点节点 → 关 → 点中心 ─────────
let t0 = await ev(`window.__trace.length`)
await clickBig()
console.log('S1_NODE_STATE', JSON.stringify(await ev(state)))
await ev(`(()=>{const x=document.querySelector('.mcg-side .mcg-side-x');x&&x.click();return !!x})()`)
await sleep(500)
t0 = await ev(`window.__trace.length`)
const c1 = await ev(centerXY)
await clickAt(c1)
await sleep(900)
console.log('S1_CENTER_STATE', JSON.stringify(await ev(state)), 'XY', JSON.stringify(c1))
console.log('S1_TRACE', await traceDump(t0))

// ───────── S2: 复位 → 再点一次中心（此时中心应当能点中；若开了就关掉重来）─────────
if (await ev(`!!document.querySelector('.mcg-side')`)) {
  await ev(`(()=>{const x=document.querySelector('.mcg-side .mcg-side-x');x&&x.click();return !!x})()`)
  await sleep(400)
}
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1500)
t0 = await ev(`window.__trace.length`)
await clickBig()
console.log('S2_NODE_STATE', JSON.stringify(await ev(state)))
await ev(`(()=>{const x=document.querySelector('.mcg-side .mcg-side-x');x&&x.click();return !!x})()`)
await sleep(500)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1500)
t0 = await ev(`window.__trace.length`)
const c2 = await ev(centerXY)
await clickAt(c2)
await sleep(900)
console.log('S2_CENTER_STATE', JSON.stringify(await ev(state)), 'XY', JSON.stringify(c2))
console.log('S2_TRACE', await traceDump(t0))

ws.close(); chrome.kill()
