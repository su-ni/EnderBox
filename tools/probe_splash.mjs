/*
  石头加载幕探针 —— 验证「进入加载幕」新定位下的全部行为（2026-10-10 第二轮）。

  用户拍板后的产品语义：
    ① 石头拼合是**进入加载**动画：桌面端一开 / 网页一进铺首屏幕；登录通过再铺一次。
    ② 不再上切页 / 换视图（视图切换改「承接式」opacity 淡入淡出，本探针第五幕顺手验）。
    ③ 自适应屏幕大小，**总时长不随屏幕变**（铺满 1080p 和 4K 严格同长）。

  ── 五幕剧 ──────────────────────────────────────────────────────────
    A. 尺寸矩阵：1440×1000 / 1920×1080 / 1366×768 / 1920×1080@dSF2，各跑一遍首屏幕：
       幕布铺满（角点 alpha=255）→ 石头方阵铺满（亮像素包围盒 ≈ cell*9 居中）→
       散场期幕布 smoothstep 淡出（角点 alpha 从 255 平滑降到 <60）→ 收场。
       断言：四档**时长一致**（≈3298ms；含 logo 段）；包围盒随短边缩放且居中。
    B. 自动化跳过：不盖 webdriver 探针 → navigator.webdriver=true → 幕布**不许出现**
       （探针要看的是页面本身，不是幕布）。
    C. 关动效跳过：body.no-anim → 幕布不出现（stoneBuild 自己短路）。
    D. 登录通过：Fetch 域 mock /api/auth/status|login|instances →
       首屏幕 → 登录页 → 提交 → **第二幕铺起** → 揭幕后面板已落定。
    E. 换视图：点「新建世界」→ **不许再出现幕布**；视图根上跑 opacity 淡出/淡入；
       向导落定；返回亦然。

  ── 测法（为什么这么测）─────────────────────────────────────────────
    · canvas 的 alpha/包围盒一律**页内 getImageData 回读**（读的是画布自身缓冲，不被合成干扰，
      6,6 角点在方阵外 → 幕布纯色，alpha 直读幕布不透明度）。
    · 幕布出现/消失用**页内 32ms 轮询**记时间戳（CDP 往返太粗），时长 = hide-show。
    · 时长常数（S(17)→24 的下限别忘；hold 默认 480、logo 段 120+380+150）：
      T_FULL=1462 / T_LOGO0=1582 / T_SWAP=2592 / T_V0=2632 / T_END=3298。
    · 全程走**本地静态服务 + 临时构建产物**（不碰线上实例，web/dist 一根汗毛都不动）。

  用法: node tools/probe_splash.mjs <URL> [outDir]
    例: node tools/probe_splash.mjs http://127.0.0.1:23517/ F:/tmp/splash
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) {
  console.error('用法: node tools/probe_splash.mjs <URL> [outDir]')
  process.exit(2)
}
const OUT = process.argv[3] || 'F:/tmp/splash'
mkdirSync(OUT, { recursive: true })

/* 固定 profile 跨轮复用；⚠ 复用前必须清 DevToolsActivePort（残留会读到旧端口 → ECONNREFUSED） */
const profile = 'F:/tmp/uiprobe-pool/splash'
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

let mockAuthed = false // 登录 mock 的状态位（D 幕用）
const ws = new WebSocket(ws0)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0
const pend = new Map()
ws.addEventListener('message', e => {
  const m = JSON.parse(e.data)
  if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id); return }
  /* D 幕：Fetch 域拦到请求 → 就地造响应（auth/status、auth/login、instances 三件套） */
  if (m.method === 'Fetch.requestPaused') {
    const { requestId, request } = m.params
    const u = request.url
    let body = '{}'
    if (u.includes('/api/auth/status')) {
      body = JSON.stringify({ requireAuth: true, trustLoopback: false, isLoopback: false, hasPassword: true, authenticated: mockAuthed, sessionHours: 24 })
    } else if (u.includes('/api/auth/login')) {
      mockAuthed = true
      body = JSON.stringify({ ok: true })
    } else if (u.includes('/api/instances')) {
      body = '[]'
    }
    ws.send(JSON.stringify({
      id: ++id,
      method: 'Fetch.fulfillRequest',
      params: {
        requestId,
        responseCode: 200,
        responseHeaders: [{ name: 'Content-Type', value: 'application/json' }, { name: 'Cache-Control', value: 'no-store' }],
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
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value
const shot = async name => {
  const r = await send('Page.captureScreenshot', { format: 'png' }, sid)
  const b = Buffer.from(r.result.data, 'base64')
  writeFileSync(join(OUT, name + '.png'), b)
  console.log('SHOT  ', name, b.length)
}

/* ── 页内注入三件套 ─────────────────────────────────────────────────── */

/* ① 盖掉自动化探针：headless 里 navigator.webdriver=true，产品会据此跳过幕布（B 幕要验的正是它）。
      A/C/D/E 幕想看到幕布，就得在页面跑起来之前把这个探针盖成 false。 */
const WD_OFF = `Object.defineProperty(navigator, 'webdriver', { get: () => false });`

/* ② 幕布记录仪：32ms 轮询「石头画布」的出现/消失（页内时间戳），可见期间顺带回读
      左上角 (6,6) 的像素 —— 它在方阵之外，alpha 直读幕布的不透明度。 */
const REC = `(()=>{
  window.__sp = { log: [], samples: [], errs: [] };
  window.addEventListener('error', e => window.__sp.errs.push(String((e && e.message) || 'err')));
  const find = () => [...document.querySelectorAll('canvas')].find(c => c.style && c.style.position === 'fixed' && c.style.zIndex === '70');
  let last = false;
  setInterval(() => {
    const c = find();
    const vis = !!(c && c.style.visibility === 'visible');
    if (vis !== last) { last = vis; window.__sp.log.push({ vis, t: Math.round(performance.now()) }); }
    if (vis && c.width > 64) {
      try {
        const d = c.getContext('2d').getImageData(6, 6, 1, 1).data;
        window.__sp.samples.push({ t: Math.round(performance.now()), a: d[3] });
      } catch (e) { window.__sp.errs.push('smp:' + e.message); }
    }
  }, 32);
})()`

/* ③ 页内加 no-anim（一见到 body 就加，必须赶在 React 首帧之前 —— 1ms 轮询足够早） */
const NOANIM = `(()=>{ const t = setInterval(()=>{ if (document.body) { document.body.classList.add('no-anim'); clearInterval(t); } }, 1); })()`

/* 通用注入管理：每一幕开始前清空上一幕的注入 */
let inj = []
const addInj = async src => { const r = await send('Page.addScriptToEvaluateOnNewDocument', { source: src }, sid); inj.push(r.result.identifier) }
const clearInj = async () => {
  for (const identifier of inj) await send('Page.removeScriptToEvaluateOnNewDocument', { identifier }, sid)
  inj = []
}

/* 页内测量：亮像素包围盒（装置像素） + 画布尺寸 */
const BBOX = `(() => {
  const c = [...document.querySelectorAll('canvas')].find(x => x.style && x.style.position === 'fixed' && x.style.zIndex === '70');
  if (!c) return null;
  const g = c.getContext('2d');
  const W = c.width, H = c.height;
  const img = g.getImageData(0, 0, W, H).data;
  let minx = 1e9, maxx = -1, miny = 1e9, maxy = -1, n = 0;
  for (let y = 0; y < H; y += 2) for (let x = 0; x < W; x += 2) {
    const k = (y * W + x) * 4;
    if (img[k] > 95 && img[k + 1] > 95) {
      n++;
      if (x < minx) minx = x; if (x > maxx) maxx = x;
      if (y < miny) miny = y; if (y > maxy) maxy = y;
    }
  }
  return { W, H, minx, maxx, miny, maxy, n, dpr: Math.round(devicePixelRatio * 100) / 100 };
})()`

const PAGE_STATE = `({
  z70: !!([...document.querySelectorAll('canvas')].find(x => x.style && x.style.position === 'fixed' && x.style.zIndex === '70')),
  vis: (() => { const c = [...document.querySelectorAll('canvas')].find(x => x.style && x.style.position === 'fixed' && x.style.zIndex === '70'); return c ? c.style.visibility : 'none'; })(),
  shell: !!document.querySelector('.shell'),
  login: !!document.querySelector('.login-card'),
  wizard: !!document.querySelector('.wz-shell'),
  main: !!document.querySelector('.main'),
  errs: (window.__sp && window.__sp.errs) || [],
})`

/* 页内测量：logo 绿像素占比 —— 全屏步进采样，判「草绿系」像素
   （G 明显高于 R/B：#6fbf44→G-R=80、#4c8f2a→G-R=67；灰石砖/土棕/深棕都过不了）。
   用途：logo 段的存在性断言 —— 拼满期应≈0，定格期应≥5%。 */
const LOGO_GREEN = `(() => {
  const c = [...document.querySelectorAll('canvas')].find(x => x.style && x.style.position === 'fixed' && x.style.zIndex === '70');
  if (!c || c.style.visibility !== 'visible') return { vis: false, ratio: 0 };
  const g = c.getContext('2d');
  const W = c.width, H = c.height;
  const img = g.getImageData(0, 0, W, H).data;
  let green = 0, cnt = 0;
  for (let i = 0; i < img.length; i += 16) {
    cnt++;
    const r = img[i], gg = img[i + 1], b = img[i + 2];
    if (gg > 110 && gg - r > 25 && gg - b > 40) green++;
  }
  return { vis: true, ratio: cnt ? green / cnt : 0, green, cnt };
})()`

const results = { checks: [], data: {} }
const check = (name, ok, detail) => {
  results.checks.push({ name, ok, detail })
  console.log(`${ok ? 'PASS ' : 'FAIL '} ${name} ${detail === undefined ? '' : JSON.stringify(detail)}`)
}
const waitFor = async (fn, timeout, step = 30) => {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) {
    if (await fn()) return true
    await sleep(step)
  }
  return false
}
const navigate = async (url, w, h, dsf) => {
  await send('Emulation.setDeviceMetricsOverride', { width: w, height: h, deviceScaleFactor: dsf, mobile: false }, sid)
  await send('Page.navigate', { url: url + (url.includes('?') ? '&' : '?') + 'cb=' + Date.now() }, sid)
}

/* ── A 幕：尺寸矩阵 ─────────────────────────────────────────────────── */
const SIZES = [
  { w: 1440, h: 1000, dsf: 1, shots: true },
  { w: 1920, h: 1080, dsf: 1, shots: false },
  { w: 1366, h: 768, dsf: 1, shots: false },
  { w: 1920, h: 1080, dsf: 2, shots: true },
]
const durs = []
for (const cfg of SIZES) {
  await clearInj()
  await addInj(WD_OFF)
  await addInj(REC)
  const tag = `A-${cfg.w}x${cfg.h}@${cfg.dsf}`
  await navigate(URL_, cfg.w, cfg.h, cfg.dsf)
  const shown = await waitFor(() => ev(`window.__sp && window.__sp.log.length >= 1`), 4000, 25)
  const d0 = Date.now()
  if (!shown) { check(`${tag} 幕布出现`, false, await ev(`window.__sp && window.__sp.log`)); continue }
  /* 460ms：拼合中（截图给人看）；1520ms：拼满闪白；2350ms：logo 定格（应全亮）；
     3000ms：散场中段 */
  await sleep(Math.max(0, 460 - (Date.now() - d0)))
  if (cfg.shots) await shot(`${tag}-assemble`)
  await sleep(Math.max(0, 1520 - (Date.now() - d0)))
  const bbFull = await ev(BBOX)
  const greenBefore = await ev(LOGO_GREEN)
  if (cfg.shots) await shot(`${tag}-full`)
  await sleep(Math.max(0, 2350 - (Date.now() - d0)))
  const greenOn = await ev(LOGO_GREEN)
  if (cfg.shots) await shot(`${tag}-logo`)
  await sleep(Math.max(0, 3000 - (Date.now() - d0)))
  const bbMid = await ev(BBOX)
  if (cfg.shots) await shot(`${tag}-vanish`)
  await sleep(Math.max(0, 3650 - (Date.now() - d0)))
  const st = await ev(PAGE_STATE)
  const sp = await ev(`window.__sp`)
  if (cfg.shots) await shot(`${tag}-after`)

  results.data[tag] = { bbFull, bbMid, greenBefore, greenOn, log: sp.log, samples: sp.samples, state: st }
  const log = sp.log
  const dur = log.length >= 2 && log[0].vis && !log[1].vis ? log[1].t - log[0].t : -1
  durs.push(dur)
  check(`${tag} 幕布完整亮相一次`, !!log[0] && log[0].vis === true && log.length === 2 && log[1].vis === false, log)
  check(`${tag} 全程时长≈3298`, Math.abs(dur - 3298) <= 150, dur)

  /* 几何（铺满版）：cell = clamp(floor(min*0.82/9), 36, 144)；
     行列 = ceil(w/cell) × ceil(h/cell) → 石阵 ≥ 视口、居中裁边、铺满整屏 */
  const min = Math.min(cfg.w, cfg.h)
  const cellExp = Math.max(36, Math.min(144, Math.floor((min * 0.82) / 9)))
  const colsExp = Math.ceil(cfg.w / cellExp)
  const rowsExp = Math.ceil(cfg.h / cellExp)
  if (bbFull) {
    const bw = bbFull.maxx - bbFull.minx
    const bh = bbFull.maxy - bbFull.miny
    check(`${tag} 石阵铺满视口·宽（≥W−2格）`, bw >= bbFull.W - 2 * cellExp * cfg.dsf, { bw, W: bbFull.W, cellExp, colsExp })
    check(`${tag} 石阵铺满视口·高（≥H−2格）`, bh >= bbFull.H - 2 * cellExp * cfg.dsf, { bh, H: bbFull.H, cellExp, rowsExp })
  } else {
    check(`${tag} 拼满帧可回读`, false, null)
  }
  /* 幕布 alpha 曲线：装配期 255 → 散场期平滑降到 <60 */
  const smp = sp.samples
  const maxA = Math.max(...smp.map(s => s.a))
  const lastA = smp.length ? smp[smp.length - 1].a : -1
  const midN = smp.filter(s => s.a > 40 && s.a < 220).length
  check(`${tag} 幕布装配期全不透明`, maxA >= 250, { maxA })
  check(`${tag} 散场期平滑淡出`, lastA <= 60 && midN >= 4, { lastA, midN, n: smp.length })
  /* logo 段（拼满后中央拼出草方块）：拼满期应还没绿、定格期绿占比应≥5%。
     ⚠ 这条能抓住「图案函数被无视」类回退：随机马赛克/没画 logo 时全屏无草绿。 */
  check(`${tag} logo 定格期有草绿（占比≥5%）`, !!greenOn && greenOn.ratio >= 0.05, greenOn)
  check(`${tag} 拼满期还没绿（占比<2%）`, !greenBefore || greenBefore.ratio < 0.02, greenBefore)
  check(`${tag} 收场后画布隐藏、页面在`, st && st.vis === 'hidden' && st.shell, st)
  check(`${tag} 无页内报错`, !sp.errs.length, sp.errs.slice(0, 3))
}
const dd = durs.filter(d => d > 0)
check('四档屏幕时长一致（波动≤150ms）', dd.length === SIZES.length && Math.max(...dd) - Math.min(...dd) <= 150, durs)

/* ── B 幕：自动化跳过（不盖 webdriver） ─────────────────────────────── */
await clearInj()
await addInj(REC) // 只有记录仪，不盖探针
await navigate(URL_, 1440, 1000, 1)
await sleep(2400)
{
  const st = await ev(PAGE_STATE)
  const sp = await ev(`window.__sp`)
  results.data['B-skip'] = { log: sp.log, state: st }
  check('B 自动化环境不放幕（log 为空）', sp.log.length === 0, sp.log)
  check('B 页面本身正常', st && st.shell, st && { shell: st.shell })
}

/* ── C 幕：no-anim 跳过 ─────────────────────────────────────────────── */
await clearInj()
await addInj(WD_OFF)
await addInj(REC)
await addInj(NOANIM)
await navigate(URL_, 1440, 1000, 1)
await sleep(1500)
{
  const st = await ev(PAGE_STATE)
  const sp = await ev(`window.__sp`)
  results.data['C-noanim'] = { log: sp.log, state: st }
  check('C 关动效时不放幕', sp.log.length === 0 && st && st.vis === 'none', { log: sp.log, vis: st && st.vis })
  check('C 页面本身正常', st && st.shell, st && { shell: st.shell })
}

/* ── D 幕：登录通过 → 第二幕铺起 ────────────────────────────────────── */
await clearInj()
mockAuthed = false
await addInj(WD_OFF)
await addInj(REC)
await send('Fetch.enable', { patterns: [
  { urlPattern: '*api/auth/status*', requestStage: 'Request' },
  { urlPattern: '*api/auth/login*', requestStage: 'Request' },
  { urlPattern: '*api/instances*', requestStage: 'Request' },
] }, sid)
await navigate(URL_, 1440, 1000, 1)
{
  /* 首屏幕走完 → 登录页露出来 */
  const gate = await waitFor(() => ev(`window.__sp && window.__sp.log.length >= 2 && window.__sp.log[1].vis === false && !!document.querySelector('.login-card')`), 6000, 50)
  check('D 首屏幕之后是登录页', gate, await ev(`({log: window.__sp.log, login: !!document.querySelector('.login-card')})`))
  await shot('D-1-login')
  /* 填密码 + 点「进入」 */
  const filled = await ev(`(() => {
    const inp = document.querySelector('.login-card input');
    if (!inp) return false;
    const set = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value').set;
    set.call(inp, 'splash-test-123');
    inp.dispatchEvent(new Event('input', { bubbles: true }));
    return true;
  })()`)
  check('D 密码框可填', filled, filled)
  const clicked = await ev(`(() => {
    const b = document.querySelector('.login-card .btn-primary');
    if (!b) return false;
    b.click();
    return true;
  })()`)
  check('D 可以点「进入」', clicked, clicked)
  /* 等第二幕：log 应新增一对 vis true→false */
  const second = await waitFor(() => ev(`window.__sp && window.__sp.log.length >= 4 && window.__sp.log[3].vis === false`), 6000, 40)
  const sp = await ev(`window.__sp`)
  const log = sp.log
  results.data['D-login'] = { log, samples: sp.samples }
  const dur2 = log.length >= 4 ? log[3].t - log[2].t : -1
  check('D 登录通过后第二幕铺起', second && log.length === 4 && log[2].vis === true && log[3].vis === false, log)
  check('D 第二幕时长≈3298', Math.abs(dur2 - 3298) <= 160, dur2)
  await sleep(300)
  const st = await ev(PAGE_STATE)
  check('D 揭幕后是面板（登录页已撤）', st && st.shell && !st.login, st && { shell: st.shell, login: st.login, vis: st.vis })
  await shot('D-2-after-login')
}
await send('Fetch.disable', {}, sid)

/* ── E 幕：换视图不再放幕，改承接式淡入淡出 ─────────────────────────── */
await clearInj()
await addInj(WD_OFF)
await addInj(REC)
await navigate(URL_, 1440, 1000, 1)
{
  /* 等首屏幕收场 */
  await waitFor(() => ev(`window.__sp && window.__sp.log.length >= 2 && window.__sp.log[1].vis === false`), 6000, 40)
  const L0 = await ev(`window.__sp.log.length`)
  const clicked = await ev(`(() => {
    const b = [...document.querySelectorAll('.topbar button')].find(x => x.textContent.includes('新建世界'));
    if (!b) return false;
    b.click();
    return true;
  })()`)
  check('E 点到「新建世界」', clicked, clicked)
  await sleep(70)
  const anim1 = await ev(`document.getAnimations().filter(a => a.playState === 'running').map(a => ({ t: a.effect && a.effect.target ? String(a.effect.target.className) : '?', k: a.effect && a.effect.getKeyframes ? a.effect.getKeyframes().length : 0, d: a.effect && a.effect.getTiming ? a.effect.getTiming().duration : 0 }))`)
  await shot('E-1-fadeout')
  /* 淡入段：轮询 70~820ms，判据 = **视图根**（shell 的最后一个子元素）上出现
     2 关键帧、时长 200ms 的动画（= switchView 的 inn）。
     ⚠ 别按类名找「.wz-shell」——向导的根是外层 .scroll-thin，.wz-shell 在它里面
       （2026-10-10 第一次跑就栽在这：inn 明明在跑，探针却盯着错的类名说没抓到）。 */
  let sawRootIn = null
  for (let i = 0; i < 25 && !sawRootIn; i++) {
    await sleep(30)
    const list = await ev(`(() => {
      const shell = document.querySelector('.shell');
      const root = shell && shell.lastElementChild;
      return [...document.getAnimations()].filter(a => a.playState === 'running').map(a => ({
        t: a.effect && a.effect.target ? String(a.effect.target.className).slice(0, 40) : '?',
        isRoot: !!(a.effect && a.effect.target === root),
        k: a.effect && a.effect.getKeyframes ? a.effect.getKeyframes().length : 0,
        d: a.effect && a.effect.getTiming ? a.effect.getTiming().duration : 0,
      }));
    })()`)
    if (list.some(a => a.isRoot && a.k === 2 && a.d === 200)) sawRootIn = { at: 70 + i * 30, root: list.filter(a => a.isRoot) }
  }
  await sleep(300)
  const st = await ev(PAGE_STATE)
  const L1 = await ev(`window.__sp.log.length`)
  results.data['E-view'] = { anim1, sawRootIn, L0, L1 }
  check('E 淡出段：旧视图根(.main)上有 130ms 淡出', anim1.some(a => a.t.includes('main') && a.k === 2 && a.d === 130), anim1.filter(a => a.t.includes('main')))
  check('E 淡入段：新视图根上有 200ms 淡入', !!sawRootIn, sawRootIn ? sawRootIn.root : '未捕捉到')
  check('E 向导落定', st && st.wizard, st && { wizard: st.wizard })
  check('E 全程没有再放幕（log 没新增）', L1 === L0 && st && st.vis === 'hidden', { L0, L1, vis: st && st.vis })
  await shot('E-2-wizard')
  /* 返回亦然 */
  const back = await ev(`(() => {
    const b = [...document.querySelectorAll('.wz-shell button')].find(x => x.textContent.includes('返回世界列表'));
    if (!b) return false;
    b.click();
    return true;
  })()`)
  await sleep(500)
  const st2 = await ev(PAGE_STATE)
  const L2 = await ev(`window.__sp.log.length`)
  check('E 返回面板（不再放幕）', back && st2 && st2.main && !st2.wizard && L2 === L0, { back, main: st2 && st2.main, L2 })
}

/* ── 汇总 ───────────────────────────────────────────────────────────── */
const failed = results.checks.filter(c => !c.ok)
console.log('DATA', JSON.stringify(results.data))
console.log(`RESULT ${results.checks.length - failed.length}/${results.checks.length} 通过`)
if (failed.length) {
  console.log('FAILED:', failed.map(f => f.name).join(' | '))
  process.exitCode = 1
}
ws.close()
chrome.kill()
process.exit(failed.length ? 1 : 0)
