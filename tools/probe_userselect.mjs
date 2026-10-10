/*
  验证「屏蔽文本选中」：画布内 user-select:none（拖拽转图不再糊一片蓝），
  但画布外的文字（工具条标题等）必须仍可正常选中复制。
  用法: node tools/probe_userselect.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'us-'))
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
await sleep(1000)

const canvasSel = await ev(`getComputedStyle(document.querySelector('.mcg-canvas')).userSelect`)
const labelSel = await ev(`(()=>{const l=document.querySelector('.mcg-label');return l?getComputedStyle(l).userSelect:'(no label)'})()`)
// 画布外的文字仍要能选（工具条标题 / 说明文字）
const outsideSel = await ev(`(()=>{const t=document.querySelector('.mcg-tb-title')||document.querySelector('.mcg-hint');return t?getComputedStyle(t).userSelect:'(none found)'})()`)

console.log('CANVAS_userSelect  ', canvasSel)
console.log('LABEL_userSelect   ', labelSel)
console.log('OUTSIDE_userSelect ', outsideSel, '(画布外应仍是 auto/text，保证别处能复制)')

// 真拖一把：拖完看有没有被选中的文字
const c = await ev(`(()=>{const r=document.querySelector('.mcg-canvas').getBoundingClientRect();return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2)}})()`)
await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: c.x - 120, y: c.y, button: 'left', clickCount: 1 }, sid)
for (let i = 1; i <= 10; i++) {
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: c.x - 120 + i * 24, y: c.y + i * 4, button: 'left' }, sid)
  await sleep(25)
}
await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: c.x + 120, y: c.y + 40, button: 'left', clickCount: 1 }, sid)
await sleep(300)
const selLen = await ev(`(window.getSelection()?.toString()||'').length`)
console.log('DRAG_SELECTED_CHARS', selLen, selLen === 0 ? '→ 拖完没有蓝框，屏蔽生效' : '→ 仍然选中了文字，屏蔽没生效')

const ok = canvasSel === 'none' && selLen === 0
console.log(ok ? 'USER_SELECT_OK true' : 'USER_SELECT_OK false')
ws.close(); chrome.kill()
