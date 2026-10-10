import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(),'hv-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new','--disable-gpu','--no-first-run',`--window-size=1440,900`,
  '--user-data-dir='+profile,'--remote-debugging-port=0','about:blank'], { stdio:'ignore' })
const pf=join(profile,'DevToolsActivePort'); let port=0
for(let i=0;i<60;i++){try{port=Number(readFileSync(pf,'utf8').split('\n')[0]);if(port)break}catch{} await new Promise(r=>setTimeout(r,200))}
const ver=await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
const ws=new WebSocket(ver.webSocketDebuggerUrl)
await new Promise(r=>ws.addEventListener('open',r,{once:true}))
let id=0;const pend=new Map()
ws.addEventListener('message',e=>{const m=JSON.parse(e.data);if(m.id&&pend.has(m.id)){pend.get(m.id)(m);pend.delete(m.id)}})
const send=(m,p={},s)=>new Promise(res=>{const i=++id;pend.set(i,res);ws.send(JSON.stringify({id:i,method:m,params:p,sessionId:s}))})
const {result:{targetId}}=await send('Target.createTarget',{url:'about:blank'})
const {result:{sessionId:sid}}=await send('Target.attachToTarget',{targetId,flatten:true})
await send('Page.enable',{},sid);await send('Runtime.enable',{},sid)
await send('Page.navigate',{url:URL_},sid)
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
const ev=async e=>(await send('Runtime.evaluate',{expression:e,returnByValue:true,awaitPromise:true},sid)).result?.result?.value
for(let i=0;i<60;i++){if(await ev(`!!document.querySelector('.mcg-canvas svg circle')`))break;await sleep(500)}
await sleep(1000)
// ⚠ 必须先停自转：图每帧在转，取坐标 → 发事件之间节点已经飘走，hover 必然落空
//   （这是探针时序问题，不是产品 bug）。停掉自转后节点静止（只剩 ~2px 呼吸），坐标才可用。
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`)
await sleep(300)
// 滚到图，然后取一个节点在视口内的坐标
await ev(`(()=>{document.querySelector('.mcg-sec').scrollIntoView({block:'start'});return 1})()`)
await sleep(600)
// ⚠⚠ 取点必须用 **circle 的 rect**，不能用 <g> 的 —— 节点用
//   <g style="transform:translate(x,y)"> 定位后，<g> 的 bbox 还包含右侧的名字文本，
//   它的中心点会偏到「圆 + 名字」的中间，落在空白处 → 必然 NO_TIP（探针误报，非产品 bug）。
const tgt = await ev(`(()=>{
  const cs=[...document.querySelectorAll('.mcg-node:not(.is-center) > circle.mcg-circle')];
  let best=null;
  for(const c of cs){const b=c.getBoundingClientRect(); if(b.width<6) continue;
    if(b.top>140&&b.bottom<700&&b.left>300&&b.right<1300){ if(!best||b.width>best.w) best={x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2),w:b.width} }}
  return best ? {x:best.x,y:best.y} : 'none';
})()`)
console.log('TARGET', JSON.stringify(tgt))
if (tgt !== 'none') {
  // 真实鼠标事件（Input 域）
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:tgt.x,y:tgt.y,buttons:0},sid)
  await sleep(300)
  console.log('TIP_AFTER_MOVE', await ev(`(()=>{const t=document.querySelector('.mcg-tip');return t?JSON.stringify({name:t.querySelector('.mcg-tip-name')?.innerText,cat:t.querySelector('.mcg-tip-cat')?.innerText,body:(t.querySelector('.mcg-tip-content')?.innerText||'').slice(0,45)}):'NO_TIP'})()`))
}
ws.close();chrome.kill()
