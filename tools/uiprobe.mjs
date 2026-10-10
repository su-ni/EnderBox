/**
 * uiprobe —— 用 CDP 驱动一台**真实**的 headless Chrome，把「渲染后的文本 + 测量数字」带回来。
 *
 * 为什么需要它（项目踩过的坑，别再重走）：
 *   ① `--dump-dom` 在 load 事件时就输出，而 React 挂载是异步的 → 拿到空壳；
 *   ② `--screenshot` 的 PNG 模型**看不了**（读图被过滤）→ 只截图等于什么都没验；
 *   ③ `--virtual-time-budget` 会快进定时器 → 量滚动/动画/延迟一律失真；
 *   ④ 固定调试端口 + 固定 user-data-dir → 两个脚本撞车，症状是「页面加载慢」这种假象。
 * 所以：端口交给 Chrome 自己选（profile 里的 DevToolsActivePort 读回）、时间戳+pid profile、
 * 真实时间、returnByValue 三层取值。并行跑多个实例是安全的。
 *
 * 用法（Node >= 22，内置 WebSocket，不用装包）：
 *   node tools/uiprobe.mjs <url> <out.png> [waitForJS] [probeJS] [--w=1440] [--h=1000]
 *                              [--click=x,y] [--after=450] [--clip=x,y,w,h[,scale]]
 *
 *   waitForJS  可选。轮询到它返回真值再截图（默认等 document.readyState==='complete'）。
 *   probeJS    可选。一段**返回可序列化对象**的表达式；用 `return` 会报错，写成表达式或 IIFE。
 *              想用 await 就写 `(async()=>{ ...; return {...} })()`。
 *   --click    可选。在 waitFor 成立后派发**真实鼠标点击**（mousePressed+mouseReleased）到视口坐标。
 *              用途：打开原生控件弹层（如 base-select 的 picker）——`el.click()` 开不了它们。
 *   --after    --click 之后额外等的毫秒数（默认 450，等弹层/动画落位）。
 *   --clip     可选。截图裁到 x,y,w,h（视口坐标系），第 5 段 scale 放大倍数（默认 1）。
 *              用途：局部放大核对（比 PIL 裁图少一步）。
 *
 * 输出：先一行 `PROBE <json>`，再 `SHOT <path> <bytes>`。**PROBE 才是判据**，PNG 是给人看的。
 */
import { spawn } from 'node:child_process'
import { writeFileSync, mkdirSync, readFileSync, unlinkSync } from 'node:fs'
import { dirname } from 'node:path'

const argv = process.argv.slice(2)
const flags = Object.fromEntries(
  argv.filter((a) => a.startsWith('--')).map((a) => {
    const [k, v] = a.slice(2).split('=')
    return [k, v ?? '1']
  }),
)
const pos = argv.filter((a) => !a.startsWith('--'))
const [url, out, waitForArg, probeArg] = pos

if (!url) {
  console.error('用法: node tools/uiprobe.mjs <url> <out.png> [waitForJS] [probeJS] [--w=1440] [--h=1000]')
  process.exit(2)
}

const W = Number(flags.w || 1440)
const H = Number(flags.h || 1000)
const CHROME = flags.chrome || 'C:/Program Files/Google/Chrome/Application/chrome.exe'
/* 端口交给 Chrome 自己选（--remote-debugging-port=0），真实端口写进 profile 的
   DevToolsActivePort 文件里再读回来。
   ⚠ 以前是 `9600 + Date.now() % 300` —— 同一毫秒里启动的两个实例会算出同一个端口，
   抢同一个调试口。症状极其阴险：其中一个实例连到另一个 Chrome 的页面上去，
   探头量到的是「别人的页面」，看起来就像产品在某个瞬间渲染错了（2026-10-09 踩过）。 */
/* profile 目录从固定槽位池里取，**用完不删**。
   ⚠ 别改回「时间戳+pid 命名、用完不管」：每跑一次就是一个 ~16MB 的 Chrome profile，
   2026-10-10 清点时 F:/tmp 里堆了 1300+ 个、20 多 GB，机器越跑越慢，最后探针自己开始超时。
   ⚠ 也别想着「用完删掉」：这个环境里 node 的 rmSync 被 safe-delete 静默吞掉（报成功、
   目录一个文件没少，实测过）。复用槽位则连删都不需要 —— 目录数有硬上界。
   ⚠ 复用不等于共享状态：本项目前端一行 localStorage 都没用（grep 过 = 0），
   唯一会跨轮带过去的是面板自身的登录 cookie（默认不开访问保护）。 */
function takeProfile() {
  const root = 'F:/tmp/uiprobe-pool'
  mkdirSync(root, { recursive: true })
  for (let i = 0; i < 6; i++) {
    const dir = `${root}/slot-${i}`
    const lock = `${dir}.lock`
    try {
      mkdirSync(dir, { recursive: true })
      writeFileSync(lock, `${process.pid} ${Date.now()}`, { flag: 'wx' })
      return { dir, lock }
    } catch { /* 这个槽有人了，看下一个 */ }
    // 陈旧锁：持有者进程已经没了，或者锁老得不可能还在跑 —— 抢过来
    try {
      const [pid, ts] = readFileSync(lock, 'utf8').split(' ')
      let alive = true
      try { process.kill(Number(pid), 0) } catch { alive = false }
      if (!alive || Date.now() - Number(ts) > 15 * 60 * 1000) {
        writeFileSync(lock, `${process.pid} ${Date.now()}`)
        return { dir, lock }
      }
    } catch { /* 锁文件没了，下一轮再抢 */ }
  }
  // 槽位全满：宁可多开一个临时目录，也别让探针跑不起来（正常不会走到这）
  const dir = `${root}/over-${Date.now()}-${process.pid}`
  mkdirSync(dir, { recursive: true })
  return { dir, lock: '' }
}

const slot = takeProfile()
const profile = slot.dir

const chrome = spawn(
  CHROME,
  [
    '--headless=new',
    '--disable-gpu',
    '--hide-scrollbars',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-extensions',
    `--user-data-dir=${profile}`,
    '--remote-debugging-port=0',
    `--window-size=${W},${H}`,
    'about:blank',
  ],
  { stdio: 'ignore' },
)

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

async function wsUrl() {
  for (let i = 0; i < 150; i++) {
    try {
      // 文件首行是端口号，第二行是浏览器级 ws 路径
      const port = Number(readFileSync(`${profile}/DevToolsActivePort`, 'utf8').split('\n')[0].trim())
      if (port > 0) {
        const r = await fetch(`http://127.0.0.1:${port}/json/version`)
        const j = await r.json()
        if (j.webSocketDebuggerUrl) return j.webSocketDebuggerUrl
      }
    } catch {
      /* 还没起来 */
    }
    await sleep(120)
  }
  throw new Error('Chrome 调试端口一直没起来')
}

let ws
let nextId = 1
const pending = new Map()

function send(method, params = {}, sessionId) {
  const id = nextId++
  const msg = { id, method, params }
  if (sessionId) msg.sessionId = sessionId
  ws.send(JSON.stringify(msg))
  return new Promise((res, rej) => {
    pending.set(id, { res, rej })
    setTimeout(() => {
      if (pending.has(id)) {
        pending.delete(id)
        rej(new Error(`${method} 超时`))
      }
    }, 45000)
  })
}

function die(code, msg) {
  if (msg) console.error(msg)
  try { chrome.kill() } catch { /* 已退出 */ }
  /* 还槽：只删那个锁文件，profile 目录留着下轮复用。
     ⚠ 删不掉也无所谓 —— takeProfile 有「进程已死就抢锁」兜底，不影响正确性。 */
  try { if (slot.lock) unlinkSync(slot.lock) } catch { /* 交给陈旧锁兜底 */ }
  process.exit(code)
}

const ws0 = await wsUrl()
ws = new WebSocket(ws0)
await new Promise((r, j) => {
  ws.addEventListener('open', r, { once: true })
  ws.addEventListener('error', j, { once: true })
})
ws.addEventListener('message', (ev) => {
  const m = JSON.parse(ev.data)
  if (m.id && pending.has(m.id)) {
    const { res, rej } = pending.get(m.id)
    pending.delete(m.id)
    if (m.error) rej(new Error(m.error.message))
    else res(m)
  }
})

let sessionId
try {
  const t = await send('Target.createTarget', { url: 'about:blank' })
  const targetId = t.result.targetId
  const a = await send('Target.attachToTarget', { targetId, flatten: true })
  sessionId = a.result.sessionId
  await send('Page.enable', {}, sessionId)
  await send('Runtime.enable', {}, sessionId)

  /** Runtime.evaluate 的三层取值：send 返回 {result:{result:{value}}}。少一层就是 undefined。 */
  const ev = async (expression) => {
    const r = await send(
      'Runtime.evaluate',
      { expression, returnByValue: true, awaitPromise: true },
      sessionId,
    )
    if (r.result?.exceptionDetails) {
      throw new Error('表达式抛错: ' + (r.result.exceptionDetails.exception?.description || '').slice(0, 300))
    }
    return r.result?.result?.value
  }

  await send('Page.navigate', { url }, sessionId)
  await sleep(600)

  // 轮询等条件成立（**不要**用固定 sleep：加载时间浮动会把竞态误判成产品 bug）
  const waitFor = waitForArg && waitForArg.trim() ? waitForArg : "document.readyState === 'complete'"
  let ok = false
  for (let i = 0; i < 60; i++) {
    try {
      if (await ev(waitFor)) { ok = true; break }
    } catch { /* 时机太早，元素还没出现 */ }
    await sleep(250)
  }
  if (!ok) console.error('⚠ waitFor 一直没成立，仍然继续（结果可能偏早）')
  await sleep(900) // 布局/字体/滚动落位

  // --click=x,y：真实鼠标点击（原生弹层用 el.click() 开不了，必须走 Input 域）
  if (flags.click) {
    const [cx, cy] = String(flags.click).split(',').map(Number)
    if (Number.isFinite(cx) && Number.isFinite(cy)) {
      const base = { x: cx, y: cy, button: 'left', clickCount: 1 }
      await send('Input.dispatchMouseEvent', { type: 'mouseMoved', ...base }, sessionId)
      await send('Input.dispatchMouseEvent', { type: 'mousePressed', ...base }, sessionId)
      await send('Input.dispatchMouseEvent', { type: 'mouseReleased', ...base }, sessionId)
      await sleep(Number(flags.after || 450))
    } else {
      console.error('⚠ --click 需要 x,y 坐标，已忽略')
    }
  }

  if (probeArg && probeArg.trim()) {
    const v = await ev(probeArg)
    console.log('PROBE ' + JSON.stringify(v))
  }

  if (out) {
    mkdirSync(dirname(out), { recursive: true })
    let shotParams = { format: 'png' }
    if (flags.clip) {
      const parts = String(flags.clip).split(',').map(Number)
      const [x, y, width, height, scale = 1] = parts
      if ([x, y, width, height].every(Number.isFinite)) {
        shotParams = { format: 'png', clip: { x, y, width, height, scale }, captureBeyondViewport: true }
      } else {
        console.error('⚠ --clip 需要 x,y,w,h[,scale]，已忽略')
      }
    }
    const s = await send('Page.captureScreenshot', shotParams, sessionId)
    const buf = Buffer.from(s.result.data, 'base64')
    writeFileSync(out, buf)
    console.log(`SHOT ${out} ${buf.length}`)
  }
  die(0)
} catch (e) {
  die(1, 'uiprobe 失败: ' + (e && e.message ? e.message : String(e)))
}
