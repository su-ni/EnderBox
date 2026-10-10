/*
  窄屏（手机宽度）面板体检：点开面板后查横向溢出 + 留张截图。
  用法: node tools/probe_mobile_panel.mjs <URL> [width] [outPrefix]
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const W = Number(process.argv[3] || 396)
const OUT = process.argv[4] || 'F:/tmp/mobile'
const profile = mkdtempSync(join(tmpdir(), 'mob-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  `--window-size=${W},900`, '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
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
await send('Emulation.setDeviceMetricsOverride', { width: W, height: 900, deviceScaleFactor: 1, mobile: false }, sid)
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value

for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1500)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(300)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1200)
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(500)

// 点一个可见的行星（挑视口内半径最大的）
const t = await ev(`(()=>{
  const nodes=[...document.querySelectorAll('.mcg-canvas .mcg-node')].filter(n=>!n.classList.contains('is-center'));
  let best=null,br=-1;
  for(const nd of nodes){const c=nd.querySelector('.mcg-circle');const r=+c.getAttribute('r');const b=c.getBoundingClientRect();if(r>br&&b.top>40&&b.bottom<innerHeight-40){br=r;best=c}}
  if(!best)return null;const b=best.getBoundingClientRect();
  return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}
})()`)
if (t) {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: t.x, y: t.y, button: 'left', clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: t.x, y: t.y, button: 'left', clickCount: 1 }, sid)
  await sleep(900)
}
const m = await ev(`(()=>{
  const de=document.documentElement;
  const s=document.querySelector('.mcg-side');
  const sb=s?s.getBoundingClientRect():null;
  return {
    sw:Math.max(de.scrollWidth, document.body.scrollWidth),
    cw:de.clientWidth,
    vw:innerWidth,
    panel:!!s,
    panelW:sb?Math.round(sb.width):null,
    panelRightOverflow:sb?Math.round(sb.right)>de.clientWidth:null
  }
})()`)
console.log('MOBILE', JSON.stringify(m))
console.log('OVERFLOW', m.sw > m.cw + 1 ? '❌ 有横向溢出' : '✅ 无横向溢出')
const shot = await send('Page.captureScreenshot', { format: 'png' }, sid)
writeFileSync(OUT + '.png', Buffer.from(shot.result.data, 'base64'))
console.log('SHOT', OUT + '.png')
ws.close(); chrome.kill()
