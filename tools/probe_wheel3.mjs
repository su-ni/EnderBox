import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2]
const profile=mkdtempSync(join(tmpdir(),'wh3-'))
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
// A. 直接设 main.scrollTop 并读回
console.log('A_SET_DIRECT', await ev(`(()=>{const m=document.querySelector('main');m.scrollTop=1500;return {st:m.scrollTop}})()`))
await sleep(300)
console.log('A_AFTER_300MS', await ev(`document.querySelector('main').scrollTop`))
// B. 归零后用滚轮（鼠标在 main 视口中心）
await ev(`document.querySelector('main').scrollTop=0`)
await sleep(300)
const p=await ev(`(()=>{const m=document.querySelector('main');const b=m.getBoundingClientRect();return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}})()`)
console.log('PT', JSON.stringify(p))
console.log('HIT', await ev(`(()=>{const e=document.elementFromPoint(${p.x},${p.y});return e?e.tagName+'.'+(e.className||''):'none'})()`))
await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:p.x,y:p.y,buttons:0},sid)
await sleep(200)
await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:p.x,y:p.y,deltaX:0,deltaY:300},sid)
await sleep(700)
console.log('B_AFTER_WHEEL', await ev(`document.querySelector('main').scrollTop`))
ws.close();chrome.kill()
