/*
  整合包「一键装最新版」确认框 + 进度接回 探针（2026-10-10）
  —— 用户需求：「点击安装直接提示默认安装最新版本并提示用户确认」
     + 「整合包安装过程中预览其他页面会丢失全局整合包安装进度」

  链路与判据（对真实守护进程跑，数据源是线上市场）：
    C1  深链 ?pk=<project> 详情页渲染出头部「安装」按钮（且没被禁用）
    C2  点「安装」弹 Dialog，标题＝「安装整合包」
    C3  正文把「装哪个」说清：含「最新版本 {versionNumber}」——版本号对照
        /api/modpacks/versions 的真数据（口径 latestVerOf：第一个 release，缺则第一个）
    C4  正文把「代价」说清：含「新建一个世界」「不会改动任何已有实例」「右下角」（切页面不耽误）
    C5  两个出口：「我自己挑版本」与「确认安装」都在，且确认安装可用
    C6  点「我自己挑版本」→ 弹窗关、落到「下载」页（页签 aria-current=true）
    C7  进度接回：把 opId 按包写进 sessionStorage → 刷新（＝切页回来）→
        页面必须**接回作业视图**（「正在装成新世界」段 + 作业卡），而不是把进度丢了
    C8  全程无页内报错

  ⚠ 不点「确认安装」：那会真去下载整合包（几十 MB 起）并在展示数据里新建实例。
     确认框本身的正确性用 C3~C5 锚定；安装执行链路由后端测试与真机验证覆盖。
  ⚠ C7 用的是**假 opId**（后端会答 404）——要验的只是「接回作业视图」这条前端路径：
     真作业在后台跑时，进到这里看到的就是同一张卡（进度/取消/完成都在它里面）。

  用法: node tools/probe_packconfirm.mjs <URL> [project]
    例: node tools/probe_packconfirm.mjs http://127.0.0.1:23456/ fabulously-optimized
*/

import { spawn } from 'node:child_process'
import { readFileSync, mkdirSync, writeFileSync, unlinkSync } from 'node:fs'
import { join } from 'node:path'

const URL_ = process.argv[2]
if (!URL_) {
  console.error('用法: node tools/probe_packconfirm.mjs <URL> [project]')
  process.exit(2)
}
const PROJECT = process.argv[3] || 'fabulously-optimized'
const OUT = process.argv[4] || 'F:/tmp/packconfirm'
mkdirSync(OUT, { recursive: true })

const profile = 'F:/tmp/uiprobe-pool/packconfirm'
mkdirSync(profile, { recursive: true })
try { unlinkSync(join(profile, 'DevToolsActivePort')) } catch { /* 正常 */ }
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
  } catch { /* 等 */ }
  await new Promise(r => setTimeout(r, 200))
}
if (!ws0) { console.error('Chrome 调试端口一直没起来'); process.exit(3) }

const ws = new WebSocket(ws0)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0
const pend = new Map()
ws.addEventListener('message', e => {
  const m = JSON.parse(e.data)
  if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id); return }
  if (m.method === 'Runtime.exceptionThrown') {
    console.log('EXCEPTION:', m.params.exceptionDetails?.exception?.description || m.params.exceptionDetails?.text)
  }
})
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid)
await send('Runtime.enable', {}, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async (e, await_ = true) => (await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: await_ }, sid)).result?.result?.value
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
const waitFor = async (fn, timeout, step = 120) => {
  const t0 = Date.now()
  while (Date.now() - t0 < timeout) { if (await fn()) return true; await sleep(step) }
  return false
}

/* ── 页内读表 ───────────────────────────────────────────── */

const BTN_INSTALL = `(() => { const b = [...document.querySelectorAll('button')].find(x => x.textContent.trim() === '安装' && !x.closest('.dlg')); return b ? { disabled: b.disabled } : null; })()`
const DLG = `(() => {
  const d = document.querySelector('.dlg-overlay .dlg, .dlg')
  if (!d) return null
  const head = d.querySelector('.dlg-head')
  const body = d.querySelector('.dlg-body')
  const btns = [...d.querySelectorAll('.dlg-foot button')].map(x => ({ t: x.textContent.trim(), disabled: x.disabled }))
  return { title: head ? head.textContent.trim() : '', body: body ? body.innerText : '', btns }
})()`
const CLICK_INSTALL = `(() => { const b = [...document.querySelectorAll('button')].find(x => x.textContent.trim() === '安装' && !x.closest('.dlg')); if (!b) return false; b.click(); return true; })()`
const CLICK_PICK = `(() => { const b = [...document.querySelectorAll('.dlg-foot button')].find(x => x.textContent.trim() === '我自己挑版本'); if (!b) return false; b.click(); return true; })()`
const TAB_DL_ON = `(() => { const b = [...document.querySelectorAll('.fchip.tab')].find(x => x.textContent.includes('下载')); return b ? b.getAttribute('aria-current') === 'true' : null; })()`
const ERRS = `(window.__pf && window.__pf.errs) || []`

/* ── 开跑 ────────────────────────────────────────────────── */

await send('Page.addScriptToEvaluateOnNewDocument', {
  source: `window.__pf = { errs: [] }; window.addEventListener('error', e => window.__pf.errs.push(String((e && e.message) || 'err')));`,
}, sid)
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, sid)

const u = new URL(URL_)
u.searchParams.set('pk', PROJECT)
await send('Page.navigate', { url: u.toString() }, sid)

/* C1 详情页 + 头部「安装」按钮 */
const c1 = await waitFor(async () => (await ev(BTN_INSTALL)) !== null, 20000)
const btn = await ev(BTN_INSTALL)
const tabsReady = await waitFor(async () => (await ev(TAB_DL_ON)) !== null, 8000)
results.data.c1 = { btn, tabsReady }
check('C1 详情页渲染出「安装」按钮（未禁用）', c1 && btn && btn.disabled === false, results.data.c1)
await shot('01-detail')

/* 期望的最新版（口径 latestVerOf：第一个 release，缺则第一个） */
const expect = await ev(`(async () => {
  try {
    const r = await fetch('/api/modpacks/versions?project=' + encodeURIComponent(${JSON.stringify(PROJECT)}))
    const j = await r.json()
    const items = j.items || []
    const v = items.find(x => x.releaseType === 'release') || items[0]
    return v ? { versionNumber: v.versionNumber, id: v.id, n: items.length } : null
  } catch (e) { return { err: String(e) } }
})()`)
results.data.expect = expect

/* C2 点「安装」→ 确认框 */
const clicked = await ev(CLICK_INSTALL)
const dlgOk = await waitFor(async () => (await ev(DLG)) !== null, 8000)
/* 版本列表是异步的：等正文从「正在取版本列表…」变成实数据 */
if (dlgOk) {
  await waitFor(async () => { const d = await ev(DLG); return d && d.body.includes('最新版本') }, 20000)
}
const dlg = await ev(DLG)
results.data.c2 = { clicked, dlg }
check('C2 点「安装」弹出确认框（标题=安装整合包）', clicked === true && dlgOk && !!dlg && dlg.title.includes('安装整合包'), { title: dlg && dlg.title })
await shot('02-dialog')

/* C3 正文说清装哪个：最新版本 + 版本号对得上真数据 */
const c3ok = !!dlg && !!expect && !expect.err && typeof expect.versionNumber === 'string' && dlg.body.includes('最新版本') && dlg.body.includes(expect.versionNumber)
check('C3 正文含「最新版本 {真数据版本号}」', c3ok, { want: expect && expect.versionNumber, got: dlg && dlg.body.slice(0, 160) })

/* C4 正文说清代价 */
const c4ok = !!dlg && dlg.body.includes('新建一个世界') && dlg.body.includes('不会改动任何已有实例') && dlg.body.includes('右下角')
check('C4 正文含「新建一个世界 / 不会改动已有实例 / 进度挂右下角」', c4ok, {})

/* C5 两个出口都在 */
const hasPick = !!dlg && dlg.btns.some(b => b.t === '我自己挑版本')
const hasConfirm = !!dlg && dlg.btns.some(b => b.t === '确认安装' && !b.disabled)
check('C5 「我自己挑版本」+「确认安装」都在且确认可用', hasPick && hasConfirm, { btns: dlg && dlg.btns })

/* C6 「我自己挑版本」→ 弹窗关 + 落到下载页 */
const pickClicked = await ev(CLICK_PICK)
const dlgGone = await waitFor(async () => (await ev(DLG)) === null, 3000)
await sleep(300)
const tabOn = await ev(TAB_DL_ON)
results.data.c6 = { pickClicked, dlgGone, tabOn }
check('C6 点「我自己挑版本」→ 弹窗关、落到「下载」页', pickClicked === true && dlgGone && tabOn === true, results.data.c6)
await shot('03-dl-tab')

/* C7 进度接回：seed stash → 刷新（＝切页回来）→ 必须接回作业视图 */
await ev(`(() => { sessionStorage.setItem('sbx-pack-op:' + ${JSON.stringify(PROJECT)}, JSON.stringify({ opId: 'probe-fake-op', instId: 'probe-fake-inst' })); return true })()`)
await send('Page.navigate', { url: u.toString() + '&ptab=dl' }, sid)
const stashOk = await waitFor(async () => await ev(`document.body.innerText.includes('正在装成新世界')`), 20000)
const opCard = await ev(`(() => { const n = document.querySelector('.nbox'); return { nbox: n ? n.innerText.slice(0, 120) : null } })()`)
const stashTab = await ev(TAB_DL_ON)
results.data.c7 = { stashOk, opCard, stashTab }
check('C7 刷新后接回作业视图（正在装成新世界 + 作业卡）', stashOk === true && typeof (opCard && opCard.nbox) === 'string', results.data.c7)
await shot('04-stash-reconnect')

/* C8 无页内报错 */
const errs = await ev(ERRS)
check('C8 全程无页内报错', Array.isArray(errs) && errs.length === 0, errs && errs.slice(0, 3))

/* ── 汇总 ── */
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
