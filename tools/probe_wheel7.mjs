import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2]
const profile=mkdtempSync(join(tmpdir(),'wh7-'))
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
await ev(`(()=>{const m=document.querySelector('main');const c=document.querySelector('.mcg-sec');
  const top=c.getBoundingClientRect().top - m.getBoundingClientRect().top + m.scrollTop;
  m.scrollTop=top-60; return m.scrollTop})()`)
await sleep(500)
// 三个测试点：A 面板头部（.sec-head）、B 画布内部、C 图例/来源行
const pts=await ev(`(()=>{const heads=document.querySelector('.mcg-sec .sec-head');const cv=document.querySelector('.mcg-canvas');
  const hb=heads.getBoundingClientRect(); const cb=cv.getBoundingClientRect();
  const toV=e=>Math.round(e.getBoundingClientRect().top+e.getBoundingClientRect().height/2);
  return {head:{x:Math.round(hb.x+120),y:toV(heads)},canvas:{x:Math.round(cb.x+cb.width/2),y:toV(cv)}}})()`)
console.log('PTS', JSON.stringify(pts))
for (const [name,p] of Object.entries(pts)) {
  const hit=await ev(`(()=>{const e=document.elementFromPoint(${p.x},${p.y});return e?e.tagName+'.'+(e.getAttribute('class')||''):'none'})()`)
  await ev(`document.querySelector('main').scrollTop=2393`)
  await sleep(150)
  await send('Input.dispatchMouseEvent',{type:'mouseMoved',x:p.x,y:p.y,buttons:0},sid)
  await sleep(150)
  await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:p.x,y:p.y,deltaX:0,deltaY:400},sid)
  await sleep(600)
  const st=await ev(`document.querySelector('main').scrollTop`)
  console.log(name, 'hit='+hit, 'scrollTop='+st, st!==2393?'MOVED ✅':'STUCK ❌')
}
ws.close();chrome.kill()
