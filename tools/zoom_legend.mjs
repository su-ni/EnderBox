import { spawn } from 'node:child_process'
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2],OUT=process.argv[3]
const profile=mkdtempSync(join(tmpdir(),'zl-'))
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
await ev(`(()=>{const el=document.querySelector('.mcg-sec');el.scrollIntoView({block:'end'});return 1})()`)
await sleep(700)
const a=await ev(`getComputedStyle(document.querySelector('.mcg-cats')).fontSize`)
const b=await ev(`getComputedStyle(document.querySelector('.mcg-src')).fontSize`)
const t=await ev(`document.querySelector('.mcg-src').textContent`)
console.log('CATS_FS', a, '| SRC_FS', b, '| SRC_TEXT', JSON.stringify(t))
const r=await ev(`(()=>{const s=document.querySelector('.mcg-sec');const b=s.getBoundingClientRect();return {sx:b.x,sy:b.y,sw:b.width,sh:b.height}})()`)
const shot=await send('Page.captureScreenshot',{format:'png',clip:{x:r.sx,y:Math.max(0,r.sy+r.sh-110),width:r.sw,height:110,scale:2}},sid)
writeFileSync(OUT,Buffer.from(shot.result.data,'base64'))
console.log('SHOT',OUT)
ws.close();chrome.kill()
