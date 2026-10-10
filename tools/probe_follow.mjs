/*
  控制台「跟随」探针（2026-10-10）—— 用户实报「点『跟随』只跟一下下就断」的回归验证。

  被验的行为（T1~T7）：
    T1  进入页面：跟随开着、贴着底（gap≈0）
    T1b 内容增长源活着（SSE 自动重连在追加行）—— 增长源死了，后面的判据全部作废
    T2  回波豁免【核心 A/B】：程序贴底状态下内容长高 → scroll 事件派发 → **跟随不许被关**
    T3  用户往回翻（scrollTop=0，走原生 scroll 事件）：让路、且内容增长不许把位置抢回去
    T4  点「跟随」按钮：立刻回到底部并继续跟随
    T5  滚轮往回滚（合成 WheelEvent，无真实滚动）：跟随关闭【A/B】
    T5b 滚轮往回滚（CDP 真轮子，有真实滚动）：跟随关闭【A/B】
    T6  用户滚回底部：跟随自动恢复（末值也得贴底 —— 只看 min 会被首采样放水）
    T7  全程无页内报错

  为什么 T2 这样测：旧实现的坑是「程序贴底自己触发的 scroll 事件」被当成用户上翻。
  该事件只在「派发前内容又长高 ≥24px」时踩坑（日志刷屏时必然遇到），而纯手工
  构造这个瞬间只能靠「scrollTop 没动过 + 内容变高 + 手工派发 scroll」——
  这正是回波事件在处理器眼里看到的全部东西：
    旧实现：gap 500 ≥ 24 → setFollow(false) → 之后 4 秒内容继续涨也不再贴底（min gap≈500+）
    新实现：scrollTop === stickyTop（程序贴底落点没动过）→ 豁免 → 下一次内容增长照常贴底（min gap≈0）

  为什么 T5 的判据不是 min gap：合成滚轮不会移动 scrollTop，peel 后元素仍在底部，
  第一次采样必然 ≈0，内容长起来才拉大差 —— 用 min 判「跟随关了」，等于判它天然必败。
  判据改为 ① 窗口内 gap 长到 ≥400（内容确实在长）② 长起来后从不被打回 <24（没偷偷回贴）。

  T3 的坑（2026-10-10 逮到）：原生 scroll 事件是**异步**派发的，置顶后若一次内容增长
  抢在事件到达前重贴底，用户的滚动就被静默抹掉。新代码在重贴前加守卫（位置明显更靠上
  → 让路）；本项同时回归「守卫不会把正常的用户上翻也拦掉」。

  ⚠ 判据一律以 **gap 采样**为准（两版产物都读得到，可做 A/B）；按钮 aria-pressed 作为
    新产物的**加强**判据 —— 旧产物没有这个属性，取不到时自动跳过（不算失败）。

  增长源：mock EventSource —— 首连 400 行，之后每次自动重连追加 12 行
  （SSE 规范：连接关闭即自动重连；body 头 `retry: 1200` 把周期压到 1.2s）。

  用法: node tools/probe_follow.mjs <URL> [outDir]
    新旧对照（旧产物基线在 F:/tmp/dist-old-follow）：
      node tools/probe_follow.mjs http://127.0.0.1:23521/ F:/tmp/follow-new   # 新产物，应全绿
      node tools/probe_follow.mjs http://127.0.0.1:23522/ F:/tmp/follow-old   # 旧产物：T2（min=500）与 T5（max=0）应 FAIL
    ⚠ T5b 在旧产物上也会过（旧的 onScroll 走真滚动的 peel 路径也能关跟随）；
      A/B 判别项就是 T2 与 T5。
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) {
  console.error('用法: node tools/probe_follow.mjs <URL> [outDir]')
  process.exit(2)
}
const OUT = process.argv[3] || 'F:/tmp/follow'
mkdirSync(OUT, { recursive: true })

/* 固定 profile 跨轮复用；⚠ 复用前必须清 DevToolsActivePort（残留会读到旧端口） */
const profile = 'F:/tmp/uiprobe-pool/follow'
mkdirSync(profile, { recursive: true })
try { unlinkSync(join(profile, 'DevToolsActivePort')) } catch { /* 没有残留，正常 */ }
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
let ws0 = ''
for (let i = 0; i < 80; i++) {
  try {
    const port = Number(readFileSync(join(profile, 'DevToolsActivePort'), 'utf8').split('\n')[0])
    if (port > 0) {
      const r = await fetch(`http://127.0.0.1:${port}/json/version`)
      const j = await r.json()
      if (j.webSocketDebuggerUrl) { ws0 = j.webSocketDebuggerUrl; break }
    }
  } catch { /* 还没起来，继续等 */ }
  await new Promise(r => setTimeout(r, 200))
}
if (!ws0) { console.error('Chrome 调试端口一直没起来'); process.exit(3) }

/* ── mock 数据 ─────────────────────────────────────────────── */

const INST = {
  id: 'probe-inst', name: '探针世界', type: 'paper', mcVersion: '1.20.1',
  dir: 'F:/tmp/probe-inst', port: 25565, minMem: 1024, maxMem: 2048,
  jarPath: 'server.jar', autoStart: false, autoRestart: false,
  status: 'stopped', pid: 0,
}

const pad2 = (n) => String(n).padStart(2, '0')
function logLine(i) {
  const t = `[08:${pad2(Math.floor(i / 60) % 60)}:${pad2(i % 60)}]`
  const msgs = [
    '[Server thread/INFO]: Starting minecraft server version 1.20.1',
    '[Server thread/INFO]: Loading properties',
    '[Server thread/INFO]: Preparing level "world"',
    '[Worker-Main-1/INFO]: Preparing spawn area: 12%',
    '[User Authenticator #1/INFO]: UUID of player Steve is replaced',
    '[Server thread/INFO]: Steve joined the game',
    '[Server thread/INFO]: Saving the game',
    '[Server thread/INFO]: Steve left the game',
  ]
  return `${t} ${msgs[i % msgs.length]} · probe #${i}`
}
let sseConn = 0
function sseBody() {
  sseConn++
  const n = sseConn === 1 ? 400 : 12
  const head = 'retry: 1200\n\nevent: snapshot-end\ndata: {}\n\n'
  let s = head
  for (let i = 0; i < n; i++) s += `data: ${JSON.stringify(logLine(sseConn * 100000 + i))}\n\n`
  return s
}

const ws = new WebSocket(ws0)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0
const pend = new Map()
ws.addEventListener('message', e => {
  const m = JSON.parse(e.data)
  if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id); return }
  if (m.method === 'Fetch.requestPaused') {
    const { requestId, request } = m.params
    const u = request.url
    let body = '{}'
    let ctype = 'application/json'
    if (u.includes('/api/auth/status')) {
      body = JSON.stringify({ requireAuth: false, trustLoopback: true, isLoopback: true, hasPassword: false, authenticated: true, sessionHours: 24 })
    } else if (u.includes('/logs/history')) {
      body = JSON.stringify(Array.from({ length: 400 }, (_, i) => logLine(i)))
    } else if (/\/api\/instances\/[^/]+\/logs$/.test(u)) {
      body = sseBody()
      ctype = 'text/event-stream'
    } else if (u.includes('/preflight')) {
      // ⚠ 这必须是**完整结构**：HudBar 会读 report.issues.some(...)，
      // mock 成 {} 会让整页被错误边界接管（探针第一次跑就栽在这）。
      body = JSON.stringify({ ok: true, jarPath: 'server.jar', jarFound: true, javaPath: 'java', javaMajor: 21, needJava: 21, port: 25565, issues: [] })
    } else if (u.includes('/metrics')) {
      body = JSON.stringify({ running: false, cpu: 0, memUsed: 0, memMax: 2048, players: 0, uptimeSec: 0, pid: 0 })
    } else if (u.includes('/api/instances')) {
      body = JSON.stringify([INST])
    }
    ws.send(JSON.stringify({
      id: ++id,
      method: 'Fetch.fulfillRequest',
      params: {
        requestId,
        responseCode: 200,
        responseHeaders: [
          { name: 'Content-Type', value: ctype },
          { name: 'Cache-Control', value: 'no-store' },
        ],
        body: Buffer.from(body).toString('base64'),
      },
      sessionId: m.sessionId,
    }))
  }
})
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid)
await send('Runtime.enable', {}, sid)
await send('Fetch.enable', { patterns: [{ urlPattern: '*api/*', requestStage: 'Request' }] }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
const shot = async name => {
  const r = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(join(OUT, name + '.png'), Buffer.from(r.result.data, 'base64'))
  console.log('SHOT  ', name)
}

const results = { checks: [], data: {} }
const check = (name, ok, detail) => {
  results.checks.push({ name, ok, detail })
  console.log(`${ok ? 'PASS ' : 'FAIL '} ${name} ${detail === undefined ? '' : JSON.stringify(detail)}`)
}
const waitFor = async (fn, timeout, step = 40) => {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) {
    if (await fn()) return true
    await sleep(step)
  }
  return false
}

/* ── 页内读表 / 操作 ────────────────────────────────────────── */

const GAP = `(() => { const el = document.querySelector('.con-body'); return el ? Math.round(el.scrollHeight - el.scrollTop - el.clientHeight) : null; })()`
const HEIGHT = `(() => { const el = document.querySelector('.con-body'); return el ? el.scrollHeight : null; })()`
const BTN = `(() => { const b = [...document.querySelectorAll('button')].find(x => x.textContent.trim() === '跟随'); return b ? { aria: b.getAttribute('aria-pressed'), cls: b.className } : null; })()`
/* 注入 500px 高的“新内容”，先清掉上一轮的注入（判据只认单次 500） */
const INJECT = `(() => { const el = document.querySelector('.con-body'); if (!el) return -1; el.querySelectorAll('[data-probe]').forEach(x => x.remove()); const d = document.createElement('div'); d.setAttribute('data-probe', '1'); d.style.height = '500px'; el.appendChild(d); return el.scrollHeight; })()`
/* 手工派发 scroll（模拟“程序贴底的回波事件”在内容已长高之后才被派发的瞬间） */
const SYNSCROLL = `(() => { const el = document.querySelector('.con-body'); if (!el) return false; el.dispatchEvent(new Event('scroll', { bubbles: true })); return true; })()`
/* 合成滚轮（无真实滚动 —— 只有 onWheel 处理器能响应它） */
const SYN_WHEEL = `(() => { const el = document.querySelector('.con-body'); if (!el) return false; el.dispatchEvent(new WheelEvent('wheel', { deltaY: -240, bubbles: true, cancelable: true })); return true; })()`
const TO_TOP = `(() => { const el = document.querySelector('.con-body'); if (!el) return null; el.scrollTop = 0; return el.scrollTop; })()`
const TO_BOTTOM = `(() => { const el = document.querySelector('.con-body'); if (!el) return null; el.scrollTop = el.scrollHeight; return el.scrollTop; })()`
const CLICK_FOLLOW = `(() => { const b = [...document.querySelectorAll('button')].find(x => x.textContent.trim() === '跟随'); if (!b) return false; b.click(); return true; })()`
const ERRS = `(window.__pf && window.__pf.errs) || []`

/** ms 毫秒内每 200ms 采一次 gap，返回最小值、末值与全部样本 */
const sampleGap = async (ms) => {
  const out = []
  const t0 = Date.now()
  while (Date.now() - t0 < ms) {
    out.push(await ev(GAP))
    await sleep(200)
  }
  const nums = out.map((x) => (x === null ? 1e9 : x))
  return { min: Math.min(...nums), last: nums[nums.length - 1], all: out }
}
/** 采样序列里「长到过 ≥200 之后有没有被打回 <24」的次数（偷偷回贴 = 跟随其实没关） */
const countSnapBacks = (all) => {
  let seenBig = false
  let snap = 0
  for (const s of all) {
    if (s === null) continue
    if (s >= 200) seenBig = true
    else if (seenBig && s < 24) snap++
  }
  return snap
}
/** 旧产物没有 aria-pressed → 取不到时自动跳过（返回 true） */
const ariaNotOn = (btn) => !btn || btn.aria !== 'true'
/** 等到「一次新的内容增长刚发生」再让用户操作（相位锁定）：
 *  下一次增长约 1.2s 后，异步的 scroll 事件有整段清空时间派发到，不与增长抢帧。
 *  不做锁定时，~20% 概率撞上「增长先于事件」的竞态，探针假红（T3/T6 都栽过）。 */
const waitFreshGrowth = async () => {
  const h0 = await ev(HEIGHT)
  return waitFor(async () => { const h = await ev(HEIGHT); return h !== null && h0 !== null && h > h0 }, 2600, 150)
}

/* ── 开跑 ──────────────────────────────────────────────────── */

await send('Page.addScriptToEvaluateOnNewDocument', {
  source: `window.__pf = { errs: [] }; window.addEventListener('error', e => window.__pf.errs.push(String((e && e.message) || 'err')));`,
}, sid)
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, sid)
await send('Page.navigate', { url: URL_ + (URL_.includes('?') ? '&' : '?') + 'page=console&cb=' + Date.now() }, sid)

/* T1 进入页面：控制台出现、可滚动、贴着底 */
let h0 = null
const ready = await waitFor(async () => {
  const g = await ev(GAP)
  const h = await ev(HEIGHT)
  h0 = h
  return g !== null && h !== null && h > 2000 && g < 24
}, 12000, 120)
const btn0 = await ev(BTN)
results.data.t1 = { h0, gap: await ev(GAP), btn: btn0 }
check('T1 控制台出现且贴着底（高>2000、gap<24）', ready, results.data.t1)

/* T1b 增长源活性：SSE 重连在追加内容 */
await sleep(2600)
const h1 = await ev(HEIGHT)
results.data.t1b = { h0, h1, sseConn }
check('T1b 内容增长源活着（scrollHeight 在长）', h1 !== null && h0 !== null && h1 > h0, results.data.t1b)

/* T2 回波豁免（核心）：贴底态 + 注入 500px + 手工派发 scroll → 跟随必须还活着 */
await ev(INJECT)
const gInject = await ev(GAP)
await ev(SYNSCROLL)
const t2 = await sampleGap(4200)
results.data.t2 = { gInject, min: t2.min }
check('T2 场景成立：注入后 gap 确实长大（>=200）', gInject !== null && gInject >= 200, { gInject })
check('T2 回波豁免：程序贴底的回波不关跟随（min gap<24）', t2.min < 24, { min: t2.min })
await shot('T2-after')

/* T3 用户往回翻（scrollTop=0，走原生 scroll 事件）：让路，且内容增长不许把位置抢回去 */
await waitFreshGrowth()
await ev(TO_TOP)
const t3 = await sampleGap(4200)
let btn3 = await ev(BTN)
/* 原生 scroll 事件是异步的：给 peel 一点到账时间（有守卫在，位置至少不会被抢回去） */
if (btn3 && btn3.aria === 'true') {
  await waitFor(async () => { const b = await ev(BTN); return b && b.aria === 'false' }, 1500, 120)
  btn3 = await ev(BTN)
}
results.data.t3 = { min: t3.min, all: t3.all, btn: btn3 }
check('T3 用户上翻后不抢（min gap>200）', t3.min > 200, { min: t3.min })
check('T3b 跟随状态确实已关（aria≠true，旧产物无此属性时跳过）', ariaNotOn(btn3), { aria: btn3 && btn3.aria })

/* T4 点「跟随」：立刻回到底部 */
const clicked = await ev(CLICK_FOLLOW)
const t4ok = await waitFor(async () => { const g = await ev(GAP); return g !== null && g < 24 }, 2500, 80)
const btn4 = await ev(BTN)
results.data.t4 = { clicked, gap: await ev(GAP), btn: btn4 }
check('T4 点「跟随」回到底部（gap<24 且 aria≠false）', clicked === true && t4ok && (!btn4 || btn4.aria !== 'false'), results.data.t4)

/* T5 滚轮往回滚（合成 WheelEvent，只有 onWheel 能响应）→ 跟随关闭。
   ⚠ 判据不是 min gap（peel 后元素还在底部，首采样必然 ≈0）：
     ① 窗口内 gap 长到 ≥400（内容在长）② 长起来后从不被打回 <24（没偷偷回贴） */
const synWheel = await ev(SYN_WHEEL)
await sleep(150)
const t5 = await sampleGap(5000)
const btn5 = await ev(BTN)
const t5max = Math.max(...t5.all.map((x) => (x === null ? 0 : x)))
const snap5 = countSnapBacks(t5.all)
results.data.t5 = { synWheel, max: t5max, snapBacks: snap5, all: t5.all, btn: btn5 }
check('T5 合成滚轮后跟随关闭（gap 长到 ≥400 且不回贴）', t5max >= 400 && snap5 === 0 && ariaNotOn(btn5), { max: t5max, snapBacks: snap5, aria: btn5 && btn5.aria })

/* T5b 真轮子（CDP，有真实滚动）→ 跟随关闭。先测完状态：T5 后应该是关的，先重开 */
await ev(CLICK_FOLLOW)
await waitFor(async () => { const g = await ev(GAP); return g !== null && g < 24 }, 2500, 80)
await ev(`document.querySelector('.con-body').scrollIntoView({ block: 'center' })`)
await sleep(120)
const rc = await ev(`(() => { const r = document.querySelector('.con-body').getBoundingClientRect(); return { x: Math.round(r.left + r.width / 2), y: Math.round(r.top + r.height / 2) }; })()`)
await send('Input.dispatchMouseEvent', { type: 'mouseWheel', x: rc.x, y: rc.y, deltaX: 0, deltaY: -320 }, sid)
await sleep(250)
const t5b = await sampleGap(4200)
const btn5b = await ev(BTN)
results.data.t5b = { rc, min: t5b.min, all: t5b.all, btn: btn5b }
check('T5b 真轮子往回滚后跟随关闭（min gap>200 且 aria≠true）', t5b.min > 200 && ariaNotOn(btn5b), { min: t5b.min, aria: btn5b && btn5b.aria })
await shot('T5b-after')

/* T6 用户滚回底部 → 自动恢复跟随（末值也得贴底，防「首采样放水」）
   ⚠ 相位锁定：等新的内容增长刚发生再滚底；随后隔一次增长再连采两次：
     恢复跟随的话，两次都应贴底（0）；没恢复的话，增长会把 gap 拉开（≈261/轮）。 */
await waitFreshGrowth()
await ev(TO_BOTTOM)
await sleep(1700)
const gA = await ev(GAP)
await sleep(900)
const gB = await ev(GAP)
results.data.t6 = { gA, gB }
check('T6 滚回底部后跟随恢复（跨一次增长的两次采样都 <24）', gA !== null && gA < 24 && gB !== null && gB < 24, { gA, gB })

/* T7 无页内报错 */
const errs = await ev(ERRS)
check('T7 全程无页内报错', Array.isArray(errs) && errs.length === 0, errs && errs.slice(0, 3))

/* ── 汇总 ─────────────────────────────────────────────────── */
console.log('SSE connections:', sseConn)
console.log('DATA', JSON.stringify(results.data))
const failed = results.checks.filter(c => !c.ok)
console.log(`RESULT ${results.checks.length - failed.length}/${results.checks.length} 通过`)
if (failed.length) {
  console.log('FAILED:', failed.map(f => f.name).join(' | '))
  process.exitCode = 1
}
ws.close()
chrome.kill()
process.exit(failed.length ? 1 : 0)
