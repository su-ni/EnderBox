/*
  验证「图例即开关」（拓展模组分类过滤）。
  断言：
   1. 图例项是可点按钮，含 中心/前置模组/拓展模组 且各带计数
   2. 点「拓展模组」→ 圆点数量从 302 掉到 ~42，边也跟着掉
   3. 工具条副标题变成「显示 42/302」
   4. 再点一次 → 恢复 302
   5. 点「全部显示」按钮出现并生效（关两项后）
   6. 截图
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const OUT = process.argv[3]
const profile = mkdtempSync(join(tmpdir(), 'cf-'))
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
for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas circle')`)) break; await sleep(500) }
await sleep(1200)

const state = () => ev(`(()=>{const c=document.querySelectorAll('.mcg-canvas .mcg-node').length;const e=document.querySelectorAll('.mcg-canvas .mcg-edge').length;const sub=document.querySelector('.mcg-tb-sub')?.textContent||'';return {nodes:c,edges:e,sub}})()`)
const legend = () => ev(`Array.from(document.querySelectorAll('.mcg-cats .mcg-cat-btn')).map(b=>({name:b.querySelector('.mcg-cat-name')?.textContent,count:b.querySelector('.mcg-cat-count')?.textContent,off:b.classList.contains('off'),disabled:b.disabled}))`)

console.log('LEGEND', JSON.stringify(await legend()))
console.log('BEFORE', JSON.stringify(await state()))

// 点「拓展模组」
const clicked = await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-cats .mcg-cat-btn')].find(b=>b.querySelector('.mcg-cat-name')?.textContent==='拓展模组');if(!b)return false;b.click();return true})()`)
console.log('CLICK_EXT', clicked)
await sleep(400)
console.log('AFTER_HIDE', JSON.stringify(await state()))
console.log('LEGEND_AFTER', JSON.stringify(await legend()))
console.log('RESET_BTN', await ev(`!!document.querySelector('.mcg-cat-reset')`))

// 截图（藏着拓展模组的状态）
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, sid)
await sleep(300)
await ev(`document.querySelector('.mcg-sec').scrollIntoView({block:'start'})`)
await sleep(400)
if (OUT) { const shot = await send('Page.captureScreenshot', { format: 'png' }, sid); writeFileSync(OUT, Buffer.from(shot.result.data, 'base64')); console.log('SHOT', OUT) }

// 再点一次 → 恢复
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-cats .mcg-cat-btn')].find(b=>b.querySelector('.mcg-cat-name')?.textContent==='拓展模组');b&&b.click();return 1})()`)
await sleep(400)
console.log('AFTER_RESTORE', JSON.stringify(await state()))

// 关两项 → 「全部显示」→ 恢复
await ev(`(()=>{for(const nm of ['拓展模组','前置模组']){const b=[...document.querySelectorAll('.mcg-cats .mcg-cat-btn')].find(b=>b.querySelector('.mcg-cat-name')?.textContent===nm);b&&b.click()}return 1})()`)
await sleep(400)
console.log('AFTER_HIDE2', JSON.stringify(await state()))
await ev(`(()=>{const b=document.querySelector('.mcg-cat-reset');b&&b.click();return !!b})()`)
await sleep(400)
console.log('AFTER_RESETALL', JSON.stringify(await state()))

ws.close(); chrome.kill()
