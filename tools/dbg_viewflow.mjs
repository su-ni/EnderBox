/* 临时调试：换视图点击后，逐帧拍 shell 子元素与动画原始状态（E 幕失败的取证） */
import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2] || 'http://127.0.0.1:23517/'
const profile = 'F:/tmp/uiprobe-pool/splashdbg'
mkdirSync(profile, { recursive: true })
try { unlinkSync(join(profile, 'DevToolsActivePort')) } catch {}
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
let ws0 = ''
for (let i = 0; i < 80; i++) {
  try {
    const port = Number(readFileSync(join(profile, 'DevToolsActivePort'), 'utf8').split('\n')[0])
    if (port > 0) { const j = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json(); if (j.webSocketDebuggerUrl) { ws0 = j.webSocketDebuggerUrl; break } }
  } catch {}
  await new Promise(r => setTimeout(r, 200))
}
const ws = new WebSocket(ws0)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0
const pend = new Map()
ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } })
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid); await send('Runtime.enable', {}, sid)
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value

await send('Page.addScriptToEvaluateOnNewDocument', { source: `Object.defineProperty(navigator, 'webdriver', { get: () => false });` }, sid)
await send('Page.navigate', { url: URL_ + '?cb=' + Date.now() }, sid)
await sleep(3200) // 等首屏幕收场

const SNAP = `({
  kids: [...(document.querySelector('.shell') || { children: [] }).children].map(c => c.tagName + '.' + String(c.className).slice(0, 40)),
  anims: [...document.getAnimations()].map(a => ({
    t: a.effect && a.effect.target ? String(a.effect.target.className).slice(0, 32) : '?',
    ps: a.playState,
    k: a.effect && a.effect.getKeyframes ? a.effect.getKeyframes().length : -1,
    d: a.effect && a.effect.getTiming ? a.effect.getTiming().duration : -1,
  })).filter(o => /wz|main|scroll/.test(o.t)),
})`

const clicked = await ev(`(() => { const b = [...document.querySelectorAll('.topbar button')].find(x => x.textContent.includes('新建世界')); if (!b) return false; b.click(); return true })()`)
console.log('clicked', clicked)
for (let i = 0; i < 16; i++) {
  await sleep(40)
  const s = await ev(SNAP)
  console.log(`t=${(i + 1) * 40}ms`, JSON.stringify(s))
}
ws.close(); chrome.kill(); process.exit(0)
