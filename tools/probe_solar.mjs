/*
  验证「星系式」星图改造 + 已安装详情页跳转闭环。
  用法:
    node tools/probe_solar.mjs <URL 市场详情页(带 pm)> <outPrefix> [dataDir]
  例:
    node tools/probe_solar.mjs "http://127.0.0.1:23456/?page=plugins&mod=market&pm=AANobbMI" F:/tmp/solar
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const OUT = process.argv[3] || 'F:/tmp/solar'
const profile = mkdtempSync(join(tmpdir(), 'solar-'))
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

// 等节点渲染
for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1500)

// ── A. 星系侧栏 + 聚焦压暗 + 快捷安装 ──
// 先停自转，避免点击时节点在移动
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(400)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(400)
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(500)

const target = await ev(`(()=>{
  const nodes=[...document.querySelectorAll('.mcg-canvas .mcg-node')].filter(n=>!n.classList.contains('is-center'));
  let best=null,br=-1;
  for(const nd of nodes){const c=nd.querySelector('circle');const r=+c.getAttribute('r');const b=c.getBoundingClientRect();if(r>br&&b.top>60&&b.bottom<innerHeight-60){br=r;best=c}}
  if(!best)return null;
  const b=best.getBoundingClientRect();
  return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}
})()`)
console.log('CLICK_AT', JSON.stringify(target))
let SIDE = 'NO_TARGET'
if (target) {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: target.x, y: target.y, button: 'left', clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: target.x, y: target.y, button: 'left', clickCount: 1 }, sid)
  await sleep(900) // 步进动画 ~200ms，留余量
  SIDE = JSON.stringify(await ev(`(()=>{
    const s=document.querySelector('.mcg-side');
    if(!s) return {visible:false};
    const actions=[...s.querySelectorAll('.mcg-side-actions *')].map(e=>e.textContent.trim()).filter(Boolean);
    const wiki=[...s.querySelectorAll('a')].find(a=>(a.getAttribute('href')||'').includes('mcmod.cn/class'));
    const quick=[...s.querySelectorAll('button')].find(b=>/快捷安装/.test(b.textContent));
    return {
      visible:true,
      name:s.querySelector('.mcg-side-name')?.textContent,
      hasWiki:!!wiki, wikiHref:wiki?.getAttribute('href'),
      hasQuickInstall:!!quick, quickDisabled:quick?.disabled||false,
      actionsText:actions.join(' | '),
      picked:document.querySelectorAll('.mcg-node.is-picked').length,
      dim:document.querySelectorAll('.mcg-node.is-dim').length,
      total:document.querySelectorAll('.mcg-node').length
    }
  })()`))
}
console.log('SIDE_PANEL', SIDE)

// 截一张带侧栏的
await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1000, deviceScaleFactor: 1, mobile: false }, sid)
await sleep(300)
await ev(`document.querySelector('.mcg-sec, .mcg-wrap')?.scrollIntoView({block:'start'})`); await sleep(400)
const shot = await send('Page.captureScreenshot', { format: 'png' }, sid)
writeFileSync(OUT + '_side.png', Buffer.from(shot.result.data, 'base64'))
console.log('SHOT', OUT + '_side.png')

// 关掉侧栏
await ev(`(()=>{const x=document.querySelector('.mcg-side .mcg-side-x');x&&x.click();return !!x})()`)
await sleep(400)
console.log('SIDE_AFTER_CLOSE', await ev(`!!document.querySelector('.mcg-side')`))

// ── B. 已安装详情页 → 市场详情 → 返回 闭环 ──
console.log('--- 已安装闭环 ---')
await send('Page.navigate', { url: 'http://127.0.0.1:23456/?page=plugins' }, sid)
for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.page[data-page=plugins]')`)) break; await sleep(500) }
await sleep(1000)
// 找已安装模组行（.rrow 里带启用/禁用徽标的）
const modRow = await ev(`(()=>{
  const rows=[...document.querySelectorAll('.page[data-page=plugins] .rrow')];
  if(!rows.length) return null;
  const r=rows[0].getBoundingClientRect();
  return {x:Math.round(r.x+r.width/2),y:Math.round(r.y+r.height/2),n:rows.length}
})()`)
console.log('INSTALLED_ROWS', JSON.stringify(modRow))
if (modRow) {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: modRow.x, y: modRow.y, button: 'left', clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: modRow.x, y: modRow.y, button: 'left', clickCount: 1 }, sid)
  await sleep(700)
  // 详情弹窗里的「详情页」按钮
  const hasBtn = await ev(`(()=>{const b=[...document.querySelectorAll('.dlg button')].find(b=>b.textContent.trim()==='详情页');if(!b)return false;b.click();return true})()`)
  console.log('OPEN_MARKET_BTN', hasBtn)
  await sleep(1200)
  const mkt = await ev(`(()=>{
    // ModProjectDetail 整屏：抬头「返回」文案应是「返回已安装」
    const back=[...document.querySelectorAll('button')].find(b=>/返回已安装/.test(b.textContent));
    const isDetail=!!document.querySelector('.sec-head h2'); // 市场详情页有页头
    return {backLabel:back?.textContent||null, isMarketDetail:isDetail, hasGraph:!!document.querySelector('.mcg-canvas')}
  })()`)
  console.log('MARKET_DETAIL', JSON.stringify(mkt))
  // 点返回
  await ev(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>/返回已安装/.test(b.textContent));b&&b.click();return !!b})()`)
  await sleep(900)
  const back = await ev(`(()=>{
    // 回到已安装：要么详情弹窗重现，要么已安装列表在
    const dlg=document.querySelector('.dlg');
    const rows=document.querySelectorAll('.page[data-page=plugins] .rrow').length;
    return {detailDialog:!!dlg, installedRows:rows}
  })()`)
  console.log('BACK_TO_INSTALLED', JSON.stringify(back))
} else {
  console.log('SKIP_INSTALLED 没有已安装模组行（测试数据目录可能为空），该路径靠类型检查+构建保证，请在真实环境确认')
}

ws.close(); chrome.kill()
