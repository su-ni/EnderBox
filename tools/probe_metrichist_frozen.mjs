/*
 * 监控历史 —— 「停服期间不往曲线灌 0」的验证。
 *
 * 背景：守护进程采样时刻意跳过停着的实例（0 会把曲线按平、把「什么时候真在跑」淹掉）。
 * 前端如果不停手（每 3s 依然 append m.cpu=0），页面停在停服实例上十几分钟，
 * 之前那段真历史就会被 0 一节节冲掉 —— 这正是本探针要钉死的回归。
 *
 * 四阶段（对一个真实实例做 stop → 观察 → start 完整轮回）：
 *   ① RUN     载入页面，等铺底；N_a0 ≥ 后端点数（顺带确认基本接线还在）
 *   ② STOP    POST 停服 → 轮询到 status≠running + 3s 静置 → 读 N_a
 *             再等 12s（=4 个轮询周期）→ 读 N_b。判据：N_b === N_a（一个 0 都没灌）
 *             ＋ note 显示「未运行」
 *   ③ START   重新开服 → 轮询到 running → 等 12s → 读 N_c
 *             判据：N_c ≥ N_b + 2（实时采样恢复）且 note 回到「进程 pid」
 *   ④ 收尾留 running（用户侧面板能直接看到曲线继续长）
 *
 * ⚠ 会真的停一次服再开回来（约 1-2 分钟）。别对着用户正在用的实例跑。
 * 用法: node tools/probe_metrichist_frozen.mjs [baseUrl] [instanceId]
 */
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync, mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const BASE = (process.argv[2] || process.env.BASE || 'http://127.0.0.1:23333').replace(/\/$/, '')
const ID = process.argv[3] || 'i1791122149514'
const PAGE = `${BASE}/?nosse=1`
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

const profile = mkdtempSync(join(tmpdir(), 'mhf-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank',
], { stdio: 'ignore' })
const pf = join(profile, 'DevToolsActivePort')
let port = 0
for (let i = 0; i < 60; i++) { try { port = Number(readFileSync(pf, 'utf8').split('\n')[0]); if (port) break } catch {} await sleep(200) }
const ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
const ws = new WebSocket(ver.webSocketDebuggerUrl)
await new Promise((r) => ws.addEventListener('open', r, { once: true }))
let id = 0
const pend = new Map()
ws.addEventListener('message', (e) => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } })
const send = (m, p = {}, s) => new Promise((res) => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid)
await send('Runtime.enable', {}, sid)

const ev = async (expr) => {
  const r = await send('Runtime.evaluate', { expression: expr, returnByValue: true, awaitPromise: true }, sid)
  if (r.result?.exceptionDetails) console.log('EV_EXC', JSON.stringify(r.result.exceptionDetails).slice(0, 300))
  return r.result?.result?.value
}
const hist = async () => (await (await fetch(`${BASE}/api/instances/${ID}/metrics/history?minutes=60`)).json()).points
const inst = async () => (await (await fetch(`${BASE}/api/instances`)).json()).find((x) => x.id === ID) || {}
const readUI = async () =>
  ev(`(()=>{
    const cap = document.querySelector('.metrics .metric .sparkcap');
    const note = document.querySelector('.metrics .metric .note');
    return { cap: cap ? cap.textContent : null, note: note ? note.textContent : null };
  })()`)
const nOf = (cap) => { const m = (cap || '').match(/近\s*(\d+)\s*采样/); return m ? Number(m[1]) : null }
const shot = async (path) => {
  const r = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(path, Buffer.from(r.result.data, 'base64'))
}
const waitN = async (floor, tries = 120) => {
  let ui = null, n = null
  for (let i = 0; i < tries; i++) {
    ui = await readUI()
    n = nOf(ui?.cap)
    if (n != null && n >= floor) break
    await sleep(250)
  }
  return { n, ui }
}

mkdirSync('F:/tmp', { recursive: true })
const fails = []
const check = (name, ok, detail) => { console.log(`VERDICT_${name}: ${ok ? 'PASS' : 'FAIL'} ${detail}`); if (!ok) fails.push(name) }

// ── ① RUN：页面载入 + 铺底 ──
const st0 = await inst()
console.log(`[①] 初始 status=${st0.status}`)
if (st0.status !== 'running') { console.log('实例不在跑，本探针需要它先跑着（先 POST start 再来）'); ws.close(); chrome.kill(); process.exit(2) }
const Pa = (await hist()).length
await send('Page.navigate', { url: PAGE }, sid)
const r1 = await waitN(Math.max(2, Pa - 1))
console.log(`[①] 铺底 N_a0=${r1.n}（后端 P_a=${Pa}）note="${r1.ui?.note}"`)
check('RUN_SEED', r1.n != null && r1.n >= Math.max(2, Pa - 1), `N_a0=${r1.n}`)

// ── ② STOP → 冻结 ──
await fetch(`${BASE}/api/instances/${ID}/stop`, { method: 'POST' })
let stopped = false
for (let i = 0; i < 240; i++) { const s = (await inst()).status; if (s !== 'running') { stopped = true; break } await sleep(500) }
console.log(`[②] status=${(await inst()).status}（等待停服完成）`)
await sleep(3000)
const rA = await waitN(0, 4)
const Na = rA.n
console.log(`[②] 静置后 N_a=${Na} note="${rA.ui?.note}"`)
await shot('F:/tmp/metrichist-stopped.png')
await sleep(12000)
const rB = await waitN(0, 4)
const Nb = rB.n
console.log(`[②] 再等 12s → N_b=${Nb} note="${rB.ui?.note}"`)
check('FROZEN', stopped && Na != null && Nb === Na, `N_b=${Nb} === N_a=${Na}（停了 12s，一个采样都不许加）`)
check('STOPPED_NOTE', (rB.ui?.note || '').includes('未运行'), `note="${rB.ui?.note}"`)

// ── ③ START → 恢复 ──
await fetch(`${BASE}/api/instances/${ID}/start`, { method: 'POST' })
let running = false
for (let i = 0; i < 240; i++) { const s = (await inst()).status; if (s === 'running') { running = true; break } await sleep(500) }
console.log(`[③] status=${(await inst()).status}（重新开服）`)
await sleep(12000)
const rC = await readUI()
const Nc = nOf(rC?.cap)
const pidUI = (rC?.note || '').match(/进程\s*(\d+)/)?.[1]
const pidAPI = (await inst()).pid
console.log(`[③] 12s 后 N_c=${Nc} cap="${rC?.cap}" pid UI=${pidUI} API=${pidAPI}`)
await shot('F:/tmp/metrichist-resumed.png')
check('RESUMED', running && Nb != null && Nc != null && Nc >= Nb + 2, `N_c=${Nc} >= N_b+2=${(Nb ?? 0) + 2}（实时采样恢复）`)
check('PID_BACK', pidUI != null && String(pidUI) === String(pidAPI), `UI=${pidUI} API=${pidAPI}`)

console.log(fails.length ? `\n结论：${fails.length} 项未过 → ${fails.join(', ')}` : '\n结论：停服冻结 + 复跑恢复，全部通过')
ws.close()
chrome.kill()
process.exit(fails.length ? 1 : 0)
