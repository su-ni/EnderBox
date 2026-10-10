/*
  验证「层级瘦身」（用户要的「拓展模组开关」的正确形态）。
  实测 Sodium：deep 1/2/3/4 = 24/27/83/167 + 中心 1 = 全部 302。
  断言：
   1. 工具条里有层级档位按钮，一般是 全部 / 1 层 / 2 层 / 3 层 / 4 层
   2. 点「1 层」→ 节点 25、边 25（中心 + 24 个直接依赖，结构完整不是孤岛）
   3. 点「2 层」→ 节点 ~52
   4. 点「全部」→ 恢复 302
   5. 图例有分类计数（前置模组 41 / 拓展模组 260 / 中心 1）
   6. 截图
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const OUT = process.argv[3]
const profile = mkdtempSync(join(tmpdir(), 'df-'))
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

const state = () => ev(`(()=>{const n=document.querySelectorAll('.mcg-canvas .mcg-node').length;const e=document.querySelectorAll('.mcg-canvas .mcg-edge').length;const sub=document.querySelector('.mcg-tb-sub')?.textContent||'';return {nodes:n,edges:e,sub}})()`)
const setDepth = (label) => ev(`(()=>{const b=[...document.querySelectorAll('.mcg-depth-btn')].find(b=>b.textContent.trim()==='${label}');if(!b)return false;b.click();return true})()`)

console.log('DEPTH_BTNS', await ev(`Array.from(document.querySelectorAll('.mcg-depth-btn')).map(b=>b.textContent.trim()+(b.classList.contains('on')?'*':'')).join(' | ')`))
console.log('LEGEND', await ev(`Array.from(document.querySelectorAll('.mcg-cats li')).map(li=>li.textContent.trim()).join(' ; ')`))
console.log('BEFORE', JSON.stringify(await state()))

for (const lv of ['1 层', '2 层']) {
  console.log('CLICK', lv, await setDepth(lv))
  await sleep(350)
  console.log('  ->', JSON.stringify(await state()))
}

// 截图 1 层（核心结构）——最容易看出「留得住结构」的那一档
await setDepth('1 层'); await sleep(350)
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, sid)
await sleep(300)
await ev(`document.querySelector('.mcg-sec').scrollIntoView({block:'start'})`)
await sleep(400)
if (OUT) { const shot = await send('Page.captureScreenshot', { format: 'png' }, sid); writeFileSync(OUT, Buffer.from(shot.result.data, 'base64')); console.log('SHOT', OUT) }

console.log('CLICK 全部', await setDepth('全部'))
await sleep(350)
console.log('AFTER_ALL', JSON.stringify(await state()))

ws.close(); chrome.kill()
