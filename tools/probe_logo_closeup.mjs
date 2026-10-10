/*
  logo 特写捕获 —— 在「石头拼合」的 logo 定格期，把画布中央的 logo 区按**原生设备像素**
  抠出来存成 PNG（外加同帧整屏一张），给用户做清晰度复核 / 留档。

  为什么这么拍：
    · 定格期（T_LOGO0+LOGO_SPAN+LOGO_FADE ≈ 2112ms → T_SWAP ≈ 2592ms）石头全部翻新完毕、
      还没开始散场 —— 这是 logo 最完整的一拍。
    · 走 1920×1080@dSF2：画布按 devicePixelRatio 放大，抠出来的就是「2 倍原生像素」，
      在普通屏上看等于放大镜，像素画的方边方角看得一清二楚。
    · logo 区几何**按 stoneBuild.ts 的同一套公式在页内重算**（cell/cols/rows/li0/lj0），
      不猜、不量 —— 抠的位置和产品自己画的严格同源。

  用法: node tools/probe_logo_closeup.mjs <URL> [outDir]
    例: node tools/probe_logo_closeup.mjs http://127.0.0.1:23456/ docs/probe-splash
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) {
  console.error('用法: node tools/probe_logo_closeup.mjs <URL> [outDir]')
  process.exit(2)
}
const OUT = process.argv[3] || 'docs/probe-splash'
mkdirSync(OUT, { recursive: true })

const profile = 'F:/tmp/uiprobe-pool/logoshot'
mkdirSync(profile, { recursive: true })
try { unlinkSync(join(profile, 'DevToolsActivePort')) } catch { /* 无残留，正常 */ }
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1920,1080', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
let ws0 = ''
for (let i = 0; i < 80; i++) {
  try {
    const port = Number(readFileSync(join(profile, 'DevToolsActivePort'), 'utf8').split('\n')[0])
    if (port > 0) {
      const r = await fetch(`http://127.0.0.1:${port}/json/version`)
      const j = await r.json()
      if (j.webSocketDebuggerUrl) { ws0 = j.webSocketDebuggerUrl; break }
    }
  } catch { /* 还没起来 */ }
  await new Promise(r => setTimeout(r, 200))
}
if (!ws0) { console.error('Chrome 调试端口一直没起来'); process.exit(3) }

const ws = new WebSocket(ws0)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0
const pend = new Map()
ws.addEventListener('message', e => {
  const m = JSON.parse(e.data)
  if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) }
})
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid)
await send('Runtime.enable', {}, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)).result?.result?.value

/* 盖掉 webdriver 探针 —— 不然产品看到自动化环境直接跳过幕布 */
await send('Page.addScriptToEvaluateOnNewDocument', {
  source: `Object.defineProperty(navigator, 'webdriver', { get: () => false });`,
}, sid)

await send('Emulation.setDeviceMetricsOverride', { width: 1920, height: 1080, deviceScaleFactor: 2, mobile: false }, sid)
await send('Page.navigate', { url: URL_ + (URL_.includes('?') ? '&' : '?') + 'cb=' + Date.now() }, sid)

/* 等幕布画布出现（visibility=visible 是 stoneBuild 起跑线） */
const FIND = `[...document.querySelectorAll('canvas')].find(x => x.style && x.style.position === 'fixed' && x.style.zIndex === '70')`
let t0 = 0
for (let i = 0; i < 200; i++) {
  const vis = await ev(`(() => { const c = ${FIND}; return !!(c && c.style.visibility === 'visible'); })()`)
  if (vis) { t0 = Date.now(); break }
  await sleep(25)
}
if (!t0) { console.error('画布一直没出现'); process.exit(4) }

/* 定格期 ≈ 2112..2592ms，取 2350 这一拍 */
const target = 2350
const waitMs = Math.max(0, target - (Date.now() - t0))
await sleep(waitMs)

/* 抠图：几何与 stoneBuild.ts 的 fit() 同源重算（DENSITY/SPAN/CELL 上下限/LOGO_N） */
const CROP = `(() => {
  const c = ${FIND};
  if (!c || c.style.visibility !== 'visible') return { ok: false, why: 'canvas gone' };
  const vw = window.innerWidth, vh = window.innerHeight;
  const DENSITY = 9, CELL_MIN = 36, CELL_MAX = 144, SPAN = 0.82, LOGO_N = 10;
  const dpr = Math.round((window.devicePixelRatio || 1) * 100) / 100;
  const cell = Math.max(CELL_MIN, Math.min(CELL_MAX, Math.floor((Math.min(vw, vh) * SPAN) / DENSITY)));
  const cols = Math.max(1, Math.ceil(vw / cell));
  const rows = Math.max(1, Math.ceil(vh / cell));
  const x0 = Math.round((vw - cols * cell) / 2);
  const y0 = Math.round((vh - rows * cell) / 2);
  const ln = Math.min(LOGO_N, cols, rows);
  const li0 = Math.floor((cols - ln) / 2), lj0 = Math.floor((rows - ln) / 2);
  const m = cell * 0.7; // 留 ~0.7 格边距：看得到 logo 与周围石头的交界
  const sx = Math.round((x0 + li0 * cell - m) * dpr);
  const sy = Math.round((y0 + lj0 * cell - m) * dpr);
  const side = Math.round((ln * cell + 2 * m) * dpr);
  const t = document.createElement('canvas');
  t.width = side; t.height = side;
  const tg = t.getContext('2d');
  tg.imageSmoothingEnabled = false;
  tg.drawImage(c, sx, sy, side, side, 0, 0, side, side);
  // 顺带在抠出来的图上数草绿（清晰度之外，图案对不对也有个数）
  const img = tg.getImageData(0, 0, side, side).data;
  let green = 0, cnt = 0;
  for (let i = 0; i < img.length; i += 16) {
    cnt++;
    const r = img[i], g2 = img[i + 1], b = img[i + 2];
    if (g2 > 110 && g2 - r > 25 && g2 - b > 40) green++;
  }
  return { ok: true, sx, sy, side, cell, cols, rows, ln, x0, y0, dpr,
           greenRatio: cnt ? green / cnt : 0, url: t.toDataURL('image/png') };
})()`
const crop = await ev(CROP)
if (!crop || !crop.ok) { console.error('抠图失败', crop); process.exit(5) }

const b64 = crop.url.split(',')[1]
writeFileSync(join(OUT, 'logo-hold-closeup.png'), Buffer.from(b64, 'base64'))
console.log('CLOSEUP', JSON.stringify({ ...crop, url: '(png ' + b64.length + ' b64 chars)' }))

/* 同帧整屏对照 */
const full = await send('Page.captureScreenshot', { format: 'png' }, sid)
writeFileSync(join(OUT, 'logo-hold-full.png'), Buffer.from(full.result.data, 'base64'))
console.log('FULL   logo-hold-full.png')

ws.close()
chrome.kill()
process.exit(0)
