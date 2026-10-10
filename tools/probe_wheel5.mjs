import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2]
const profile=mkdtempSync(join(tmpdir(),'wh5-'))
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
await ev(`(()=>{const m=document.querySelector('main');const c=document.querySelector('.mcg-canvas');
  const top=c.getBoundingClientRect().top - m.getBoundingClientRect().top + m.scrollTop;
  m.scrollTop=top-100; return m.scrollTop})()`)
await sleep(400)
// 装一个计数器看 wheel 事件到底有没有到达
await ev(`(()=>{window.__wc=0;window.__wcKey='';document.addEventListener('wheel',e=>{window.__wc++;window.__wcKey=String(e.ctrlKey)},true);return 1})()`)
const t0=await ev(`document.querySelector('main').scrollTop`)
// 1) CDP 合成滚轮
const pt=await ev(`(()=>{const c=document.querySelector('.mcg-canvas');const b=c.getBoundingClientRect();return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}})()`)
await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:pt.x,y:pt.y,buttons:0},sid)
await sleep(200)
await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:pt.x,y:pt.y,deltaX:0,deltaY:400},sid)
await sleep(600)
console.log('CDP_WHEEL', JSON.stringify({count:await ev('window.__wc'), scrollTop:await ev(`document.querySelector('main').scrollTop`), before:t0}))
// 2) 合成 WheelEvent（JS 派发）
await ev(`document.querySelector('main').scrollTop=${t0}`)
await sleep(200)
await ev(`(()=>{const c=document.querySelector('.mcg-canvas');const e=new WheelEvent('wheel',{deltaY:400,bubbles:true,cancelable:true});c.dispatchEvent(e);return 1})()`)
await sleep(500)
console.log('SYNTH_WHEEL', JSON.stringify({scrollTop:await ev(`document.querySelector('main').scrollTop`), before:t0}))
ws.close();chrome.kill()
