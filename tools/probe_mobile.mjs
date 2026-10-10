import { spawn } from 'node:child_process'
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2],OUT=process.argv[3],W=Number(process.argv[4]||520)
const profile=mkdtempSync(join(tmpdir(),'mb-'))
const chrome=spawn('C:/Program Files/Google/Chrome/Application/chrome.exe',[
  '--headless=new','--disable-gpu','--no-first-run','--hide-scrollbars',
  `--window-size=${W},2400`,'--user-data-dir='+profile,'--remote-debugging-port=0','about:blank'],{stdio:'ignore'})
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
for(let i=0;i<60;i++){if(await ev(`!!document.querySelector('.mcg-canvas svg circle')`))break;await sleep(500)}
await sleep(1500)
console.log('OVERFLOW', JSON.stringify(await ev(`(()=>{const d=document.documentElement;const bad=[];
  // ⚠ 只算**真正会造成横向溢出**的元素：
  //  1) 自身或任一祖先有 overflow:hidden/clip 的元素，其几何虽然超界但**被裁掉了**
  //     （SVG 子元素尤其典型 —— .mcg-svg 有 overflow:hidden，圆点画到框外也不溢出，
  //      但 getBoundingClientRect 仍返回未裁切的坐标 → 纯测量假象，会误报）。
  //  2) 文档级溢出真正该看的是 scrollWidth vs clientWidth；逐元素只是辅助定位。
  const clipped=e=>{let p=e;while(p&&p!==document.body){const s=getComputedStyle(p);if(/hidden|clip/.test(s.overflowX))return true;p=p.parentElement}return false};
  document.querySelectorAll('.mcg-sec *').forEach(e=>{const r=e.getBoundingClientRect(); if(r.right>d.clientWidth+2&&!clipped(e)) bad.push((e.getAttribute('class')||e.tagName)+':'+Math.round(r.right))});
  return {SW:d.scrollWidth,CW:d.clientWidth,bad:bad.slice(0,6)}})()`)))
console.log('CANVAS', JSON.stringify(await ev(`(()=>{const c=document.querySelector('.mcg-canvas');const r=c.getBoundingClientRect();return {w:Math.round(r.width),h:Math.round(r.height)}})()`)))
const r=await ev(`(()=>{const s=document.querySelector('.mcg-sec');if(!s)return null;const b=s.getBoundingClientRect();return {x:b.x,y:b.y,w:b.width,h:b.height}})()`)
if(!r){console.log('NO_SECTION 没找到 .mcg-sec —— 检查 URL 是否带了 page 与 mod/pm（深链缺 page 会落到概览页）');ws.close();chrome.kill();process.exit(2)}
await send('Emulation.setDeviceMetricsOverride',{width:W,height:Math.ceil(r.y+r.h+40),deviceScaleFactor:1,mobile:false},sid)
await sleep(600)
const r2=await ev(`(()=>{const s=document.querySelector('.mcg-sec');const b=s.getBoundingClientRect();return {x:b.x,y:b.y,w:b.width,h:b.height}})()`)
const shot=await send('Page.captureScreenshot',{format:'png',clip:{x:r2.x,y:r2.y,width:r2.w,height:r2.h,scale:1},captureBeyondViewport:true},sid)
writeFileSync(OUT,Buffer.from(shot.result.data,'base64'))
console.log('SHOT',OUT)
ws.close();chrome.kill()
