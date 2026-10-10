import { spawn } from 'node:child_process'
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2],OUT=process.argv[3]
const profile=mkdtempSync(join(tmpdir(),'zl2-'))
const chrome=spawn('C:/Program Files/Google/Chrome/Application/chrome.exe',[
  '--headless=new','--disable-gpu','--no-first-run','--hide-scrollbars',
  `--window-size=1440,1400`,'--user-data-dir='+profile,'--remote-debugging-port=0','about:blank'],{stdio:'ignore'})
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
// 取整个 .mcg-sec 从画布顶到 .mcg-src 底
const r=await ev(`(()=>{const s=document.querySelector('.mcg-sec');
  const tl=s.getBoundingClientRect();
  const src=document.querySelector('.mcg-src');const sb=src.getBoundingClientRect();
  return {x:tl.x,y:tl.y,w:tl.width,h:(sb.bottom-tl.top)+8}})()`)
console.log('RECT',JSON.stringify(r))
await send('Emulation.setDeviceMetricsOverride',{width:1440,height:Math.ceil(r.y+r.h+40),deviceScaleFactor:2,mobile:false},sid); await sleep(600); const r2=await ev(`(()=>{const s=document.querySelector('.mcg-sec');const tl=s.getBoundingClientRect();const src=document.querySelector('.mcg-src');const sb=src.getBoundingClientRect();return {x:tl.x,y:tl.y,w:tl.width,h:(sb.bottom-tl.top)+8}})()`); const shot=await send('Page.captureScreenshot',{format:'png',clip:{x:r2.x,y:Math.max(0,r2.y),width:r2.w,height:r2.h,scale:1},captureBeyondViewport:true},sid)
writeFileSync(OUT,Buffer.from(shot.result.data,'base64'))
console.log('SHOT',OUT)
ws.close();chrome.kill()
