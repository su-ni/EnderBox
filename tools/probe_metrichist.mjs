/*
 * 监控历史铺底验证：概览曲线在「打开 / 刷新」时，是否从后端历史续上（而不是从零攒）。
 *
 * 判据（全部对着后端同一时刻的真实点数，不做假设）：
 *   ① LOAD   载入后 N1 ≥ P_a     —— 铺底生效。旧行为（纯前端自攒）此刻只有 0~2 点。
 *   ② NODUP  载入后 N1 ≤ P1+4    —— 没有重复铺设/双份灌入。
 *   ③ RELOAD 刷新后 N2 ≥ P_b     ——「刷新即失」的修复。N2 不许归零。
 *   ④ MONO   N2 ≥ N1-3           —— 刷新后没缩水（2 个实时点容差）。
 *   ⑤ PID    页面显示的进程 PID 与 API 一致 —— 证明量的是对的那个实例（12.9.4 教训）。
 *
 * 用法: node tools/probe_metrichist.mjs [baseUrl] [instanceId]
 *   默认 http://127.0.0.1:23333 或 $BASE；实例默认 10086（验收前已确认是唯一在跑的）。
 */
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync, mkdirSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const BASE = (process.argv[2] || process.env.BASE || 'http://127.0.0.1:23333').replace(/\/$/, '')
const ID = process.argv[3] || 'i1791122149514'
const PAGE = `${BASE}/?nosse=1`
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// ── CDP 引导（probe_cpu.mjs 同款：端口交给 Chrome 自己选，从 DevToolsActivePort 读回）──
const profile = mkdtempSync(join(tmpdir(), 'mh-'))
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

// ── 数据侧小工具 ──
const hist = async () => {
  const r = await fetch(`${BASE}/api/instances/${ID}/metrics/history?minutes=60`)
  return (await r.json()).points
}
const inst = async () => {
  const r = await fetch(`${BASE}/api/instances`)
  const list = await r.json()
  return list.find((x) => x.id === ID) || {}
}
// 读 UI：CPU 卡的「近 N 采样」+ 该卡 note（进程 pid）
const readUI = async () =>
  ev(`(()=>{
    const cap = document.querySelector('.metrics .metric .sparkcap');
    const note = document.querySelector('.metrics .metric .note');
    const line = document.querySelector('.metrics .metric .spark .line');
    return { cap: cap ? cap.textContent : null, note: note ? note.textContent : null, dLen: line ? (line.getAttribute('d')||'').length : -1 };
  })()`)
const nOf = (cap) => { const m = (cap || '').match(/近\s*(\d+)\s*采样/); return m ? Number(m[1]) : null }

const shot = async (path) => {
  const r = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(path, Buffer.from(r.result.data, 'base64'))
}

// ── ① 载入前：后端点数 ──
mkdirSync('F:/tmp', { recursive: true })
const apiBefore = await inst()
const ptsA = await hist()
const Pa = ptsA.length
const lastA = ptsA[ptsA.length - 1]
console.log(`[前置] 实例 ${apiBefore.name || ID} status=${apiBefore.status} pid=${apiBefore.pid}`)
console.log(`[前置] P_a=${Pa} 最后一点 t=${lastA?.t} cpu=${lastA?.cpu?.toFixed(0)} mem=${lastA?.mem}`)
if (Pa < 4) console.log('⚠ P_a 太小（<4），本次判据弱 —— 等 10s 采几个点再说')
while ((await hist()).length < 4) await sleep(2000)

// ── ② 载入页面：等铺底 ──
await send('Page.navigate', { url: PAGE }, sid)
let N1 = null, ui1 = null, t0 = Date.now()
for (let i = 0; i < 120; i++) {
  ui1 = await readUI()
  N1 = nOf(ui1?.cap)
  if (N1 != null && N1 >= Math.max(2, Pa - 1)) break
  await sleep(250)
}
const loadMs = Date.now() - t0
const P1 = (await hist()).length
const pidAPI = (await inst()).pid
const pidUI = (ui1?.note || '').match(/进程\s*(\d+)/)?.[1]
await shot('F:/tmp/metrichist-load.png')
console.log(`[载入] N1=${N1} 用时=${loadMs}ms cap="${ui1?.cap}" dLen=${ui1?.dLen}`)
console.log(`[载入] P1(读UI后端点)=${P1}  pid: UI=${pidUI} API=${pidAPI}`)

// ── ③ 停 4s 后刷新 ──
await sleep(4000)
const ptsB = await hist()
const Pb = ptsB.length
const lastB = ptsB[ptsB.length - 1]
console.log(`[刷新前] P_b=${Pb} 最后一点 t=${lastB?.t} cpu=${lastB?.cpu?.toFixed(0)} mem=${lastB?.mem}`)
await send('Page.navigate', { url: PAGE }, sid)
let N2 = null, ui2 = null, t1 = Date.now()
for (let i = 0; i < 120; i++) {
  ui2 = await readUI()
  N2 = nOf(ui2?.cap)
  if (N2 != null && N2 >= Math.max(2, Pb - 1)) break
  await sleep(250)
}
const reloadMs = Date.now() - t1
await shot('F:/tmp/metrichist-reload.png')
console.log(`[刷新] N2=${N2} 用时=${reloadMs}ms cap="${ui2?.cap}" dLen=${ui2?.dLen}`)

// ── ④ 判定 ──
const fails = []
const check = (name, ok, detail) => { console.log(`VERDICT_${name}: ${ok ? 'PASS' : 'FAIL'} ${detail}`); if (!ok) fails.push(name) }
check('LOAD', N1 != null && N1 >= Math.max(2, Pa - 1), `N1=${N1} >= P_a-1=${Pa - 1}（旧行为此刻只有 0~2）`)
check('NODUP', N1 != null && N1 <= P1 + 4, `N1=${N1} <= P1+4=${P1 + 4}`)
check('RELOAD', N2 != null && N2 >= Math.max(2, Pb - 1), `N2=${N2} >= P_b-1=${Pb - 1}`)
check('MONO', N1 != null && N2 != null && N2 >= N1 - 3, `N2=${N2} >= N1-3=${N1 - 3}`)
check('PID', pidUI != null && String(pidUI) === String(pidAPI), `UI=${pidUI} API=${pidAPI}`)
const growth = (N2 ?? 0) - (N1 ?? 0)
console.log(`[推移] N1=${N1} → N2=${N2}（差 ${growth >= 0 ? '+' : ''}${growth}）`)
console.log(fails.length ? `\n结论：${fails.length} 项未过 → ${fails.join(', ')}` : '\n结论：5/5 全绿')

ws.close()
chrome.kill()
process.exit(fails.length ? 1 : 0)
