import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2]
const profile=mkdtempSync(join(tmpdir(),'wh-'))
const chrome=spawn('C:/Program Files/Google/Chrome/Application/chrome.exe',[
  '--headless=new','--disable-gpu','--no-first-run','--hide-scrollbars',
  `--window-size=1440,1000`,'--user-data-dir='+profile,'--remote-debugging-port=0','about:blank'],{stdio:'ignore'})
const pf=join(profile,'DevToolsActivePort');let port=0
for(let i=0;i<60;i++){try{port=Number(readFileSync(pf,'utf8').split('\n')[0]);if(port)break}catch{}await new Promise(r=>setTimeout(r,200))}
const ver=await(await fetch(`http://127.0.0.1:${port}/json/version`)).json()
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
for(let i=0;i<40;i++){if(await ev(`!!document.querySelector('.mcg-canvas circle')`))break;await sleep(500)}
await sleep(1200)
// 1) 先说清楚滚动容器是谁
console.log('SCROLLERS', await ev(`(()=>{const out=[];document.querySelectorAll('*').forEach(e=>{if(e.scrollHeight>e.clientHeight+10){const s=getComputedStyle(e);out.push((e.className||e.tagName)+':'+s.overflowY)}});return out.slice(0,8)})()`))
// 2) 直接设 scrollTop 看页面能不能滚（排除"是不是根本没得滚"）
console.log('CAN_SET_SCROLL', await ev(`(()=>{const before=scrollY;window.scrollTo(0,500);const a=scrollY;window.scrollTo(0,before);return {max:document.documentElement.scrollHeight-document.documentElement.clientHeight,a}})()`))
// 3) 鼠标移到画布中心，滚轮
const el=await ev(`(()=>{const c=document.querySelector('.mcg-canvas');const b=c.getBoundingClientRect();return {x:Math.round(b.x+b.width/2),y:Math.round(Math.min(b.y+120,b.bottom-20))}})()`)
console.log('CANVAS_PT', JSON.stringify(el))
await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:el.x,y:el.y,buttons:0},sid)
await sleep(200)
// 检查命中元素与它的 touch-action / 上层拦截者
console.log('HIT', await ev(`(()=>{const e=document.elementFromPoint(${el.x},${el.y});if(!e)return 'none';const s=getComputedStyle(e);return {tag:e.tagName,cls:e.getAttribute('class'),touchAction:s.touchAction,ovY:s.overflowY,pe:s.pointerEvents}})()`))
await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:el.x,y:el.y,deltaX:0,deltaY:500},sid)
await sleep(600)
console.log('AFTER_WHEEL_ON_CANVAS', await ev('scrollY'))
// 4) 空白处滚
await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:40,y:500,buttons:0},sid)
await sleep(200)
await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:40,y:500,deltaX:0,deltaY:500},sid)
await sleep(600)
console.log('AFTER_WHEEL_ON_EDGE', await ev('scrollY'))
ws.close();chrome.kill()
