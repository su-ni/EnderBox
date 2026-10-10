import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2]
const profile=mkdtempSync(join(tmpdir(),'zm-'))
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
const cnt=()=>ev(`(()=>{const t=[...document.querySelectorAll('.mcg-label')];return {total:t.length,visible:t.filter(e=>getComputedStyle(e).opacity!=='0').length}})()`)
console.log('K=1', JSON.stringify(await cnt()))
// 点两次放大按钮
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/放大/.test(b.title||b.getAttribute('aria-label')||''));b&&b.click();return !!b})()`)
await sleep(200)
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/放大/.test(b.title||b.getAttribute('aria-label')||''));b&&b.click();return !!b})()`)
await sleep(400)
console.log('AFTER_ZOOMx2', JSON.stringify(await cnt()))
console.log('PX_W>canvas? ', await ev(`(()=>{const c=document.querySelector('.mcg-canvas');return c.scrollWidth<=c.clientWidth+2})()`))
ws.close();chrome.kill()
