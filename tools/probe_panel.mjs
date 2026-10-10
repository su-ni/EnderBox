/*
  星图右侧信息面板体检：全文 + 分块几何（找「底部大片留白」这类问题用数字说话）。
  用法:
    node tools/probe_panel.mjs <URL> [outPrefix]
  例:
    node tools/probe_panel.mjs "http://127.0.0.1:23333/?page=plugins&mod=market&pm=AANobbMI"
  输出: PANEL_TEXT / PANEL_BLOCKS / PANEL_GEOM / SIDE_AFTER_CLOSE 四行。
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const OUT = process.argv[3] || 'F:/tmp/panel'
const profile = mkdtempSync(join(tmpdir(), 'panl-'))
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

for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1500)

// ⚠ 截图/拖拽前必须先滚进视口：本页顶部是模组信息，星图在视口外 ——
//   不滚则「相隔 12s」对比图会双双截到页面顶部（2026-10-10 曾产出两张「假绿」对比图）。
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(600)

// ── 自转对比：相隔 12s 两张（约转过 40°，肉眼可判「绕竖直轴转盘」还是「翻滚」）──
{
  const a = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(OUT + '_spin_a.png', Buffer.from(a.result.data, 'base64'))
  console.log('SHOT', OUT + '_spin_a.png')
}
await sleep(12000)
{
  const b = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(OUT + '_spin_b.png', Buffer.from(b.result.data, 'base64'))
  console.log('SHOT', OUT + '_spin_b.png')
}

// ⚠ 顺序：先复位、后停自转 —— 反着写会被 reset() 静默覆盖（旧版 reset 里带 setSpin(true)，
//   2026-10-10 已从产品移除；探针保持此顺序对新旧版本都正确）
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1200)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(400)
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(500)

// 全量视图先留一张（不带面板）—— 给「星系形态」的评审用
{
  const shot0 = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(OUT + '_galaxy.png', Buffer.from(shot0.result.data, 'base64'))
  console.log('SHOT', OUT + '_galaxy.png')
}

// 深度档分布：确认 z2 映射（ZMIN/ZMAX=±1.2）让实际档位铺开在深度雾的渐变区
// （好分布 ≈ 档 2~9 有量、0/1/10/11 边缘少；全挤在 2 档 = 映射没跟上投影链）
{
  const bks = await ev(`(()=>[...document.querySelectorAll('.mcg-canvas .mcg-bk')].map((b,i)=>({bk:i,n:b.querySelectorAll('.mcg-node').length})))()`)
  console.log('BUCKETS', JSON.stringify(bks))
}

// 点一个「有简介」的非中心节点：挑个大点的行星
const target = await ev(`(()=>{
  const nodes=[...document.querySelectorAll('.mcg-canvas .mcg-node')].filter(n=>!n.classList.contains('is-center'));
  let best=null,br=-1;
  for(const nd of nodes){const c=nd.querySelector('circle');const r=+c.getAttribute('r');const b=c.getBoundingClientRect();if(r>br&&b.top>60&&b.bottom<innerHeight-60){br=r;best=c}}
  if(!best)return null;
  const b=best.getBoundingClientRect();
  return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}
})()`)
console.log('CLICK_AT', JSON.stringify(target))
if (target) {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: target.x, y: target.y, button: 'left', clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: target.x, y: target.y, button: 'left', clickCount: 1 }, sid)
  await sleep(900)
  // 全文
  const txt = await ev(`(()=>{const s=document.querySelector('.mcg-side');return s?s.innerText:null})()`)
  console.log('PANEL_TEXT_START')
  console.log(txt)
  console.log('PANEL_TEXT_END')
  // 分块几何：谁占了哪一块 + 底部空多少
  const geom = await ev(`(()=>{
    const s=document.querySelector('.mcg-side'); if(!s) return null;
    const box=s.getBoundingClientRect();
    const kids=[...s.children].map(el=>{const b=el.getBoundingClientRect();return {cls:el.className,top:Math.round(b.top-box.top),bottom:Math.round(b.bottom-box.top),h:Math.round(b.height)}});
    const act=s.querySelector('.mcg-side-actions');
    const ab=act?act.getBoundingClientRect():null;
    const last=kids.reduce((a,k)=>k.bottom>a?k.bottom:a,0);
    return {
      sideW:Math.round(box.width), sideH:Math.round(box.height),
      canvasW:Math.round(document.querySelector('.mcg-canvas')?.getBoundingClientRect().width||0),
      kids,
      actionsTop:ab?Math.round(ab.top-box.top):null,
      gapAboveActions:ab?Math.round(ab.top-box.top-last):null
    }
  })()`)
  console.log('PANEL_BLOCKS', JSON.stringify(geom))
  // 截图存档
  const shot = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(OUT + '_panel.png', Buffer.from(shot.result.data, 'base64'))
  console.log('SHOT', OUT + '_panel.png')
  // 关闭后应真正卸载
  await ev(`(()=>{const x=document.querySelector('.mcg-side .mcg-side-x');x&&x.click();return !!x})()`)
  await sleep(400)
  console.log('SIDE_AFTER_CLOSE', await ev(`!!document.querySelector('.mcg-side')`))
} else {
  console.log('NO_TARGET')
}

// ── 第二轮：中心节点（验证「本图中心」文案分支 + 不再出现「距中心 | 中心」这种怪行）──
// ⚠ 复位视图是步进动画：点完立刻量坐标会量到「还在路上」的位置，点空 → 面板不出现。
//   所以等足 1.5s，量完再用 elementFromPoint 确认落点确实是中心节点，最多重试两轮。
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`)
await sleep(1500)
// 复位后补关一次自转（旧版 reset 会把自转打开；已关时按钮标题是「自动旋转」→ 找不到 → 天然 no-op）
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(300)
const measureCenter = `(()=>{
  const nd=document.querySelector('.mcg-canvas .mcg-node.is-center'); if(!nd) return null;
  const b=nd.querySelector('circle').getBoundingClientRect();
  return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}
})()`
let ctr = await ev(measureCenter)
for (let attempt = 1; attempt <= 2 && ctr; attempt++) {
  console.log(`CENTER_AT_TRY${attempt}`, JSON.stringify(ctr))
  const diag = await ev(`(()=>{
    const el=document.elementFromPoint(${ctr.x},${ctr.y});
    const g=el&&el.closest?el.closest('.mcg-node'):null;
    return {cls:el?el.getAttribute('class'):null, nid:g?g.getAttribute('data-nid'):null, isCenter:g?g.classList.contains('is-center'):false}
  })()`)
  console.log(`CENTER_HIT_TRY${attempt}`, JSON.stringify(diag))
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: ctr.x, y: ctr.y, button: 'left', clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: ctr.x, y: ctr.y, button: 'left', clickCount: 1 }, sid)
  await sleep(900)
  if (await ev(`!!document.querySelector('.mcg-side')`)) break
  await sleep(700)
  ctr = await ev(measureCenter)
}
console.log('CENTER_TEXT_START')
console.log(await ev(`(()=>{const s=document.querySelector('.mcg-side');return s?s.innerText:null})()`))
console.log('CENTER_TEXT_END')
console.log('CENTER_GEOM', JSON.stringify(await ev(`(()=>{const s=document.querySelector('.mcg-side');if(!s)return null;const b=s.getBoundingClientRect();return {sideH:Math.round(b.height),sideW:Math.round(b.width)}})()`)))
// 中心面板也留一张
{
  const shot2 = await send('Page.captureScreenshot', { format: 'png' }, sid)
  writeFileSync(OUT + '_center.png', Buffer.from(shot2.result.data, 'base64'))
  console.log('SHOT', OUT + '_center.png')
}

ws.close(); chrome.kill()
