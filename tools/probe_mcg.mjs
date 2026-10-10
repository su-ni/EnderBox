import { spawn } from 'node:child_process'
import { readFileSync, writeFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_ = process.argv[2], OUT = process.argv[3]
const W = Number(process.argv[4]||1440), H = Number(process.argv[5]||1000)
const profile = mkdtempSync(join(tmpdir(),'mcgp-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check',
  `--window-size=${W},${H}`,'--user-data-dir='+profile,'--remote-debugging-port=0','about:blank',
], { stdio:'ignore' })
const pf = join(profile,'DevToolsActivePort')
let port=0
for (let i=0;i<60;i++){ try{port=Number(readFileSync(pf,'utf8').split('\n')[0]); if(port)break}catch{} await new Promise(r=>setTimeout(r,200)) }
const ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
const ws = new WebSocket(ver.webSocketDebuggerUrl)
await new Promise(r=>ws.addEventListener('open',r,{once:true}))
let id=0; const pend=new Map()
ws.addEventListener('message',e=>{const m=JSON.parse(e.data); if(m.id&&pend.has(m.id)){pend.get(m.id)(m);pend.delete(m.id)}})
const send=(method,params={},sid)=>new Promise(res=>{const i=++id;pend.set(i,res);ws.send(JSON.stringify({id:i,method,params,sessionId:sid}))})
const {result:{targetId}}=await send('Target.createTarget',{url:'about:blank'})
const {result:{sessionId:sid}}=await send('Target.attachToTarget',{targetId,flatten:true})
await send('Page.enable',{},sid); await send('Runtime.enable',{},sid)
await send('Page.navigate',{url:URL_},sid)
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
const ev=async e=>(await send('Runtime.evaluate',{expression:e,returnByValue:true,awaitPromise:true},sid)).result?.result?.value
for(let i=0;i<60;i++){ if(await ev(`!!document.querySelector('.mcg-canvas svg circle')`)) break; await sleep(500) }
await sleep(1200)

// 溢出检查：整个文档
const ov = await ev(`(()=>{const d=document.documentElement;const bad=[];
  document.querySelectorAll('.mcg-sec *').forEach(e=>{const r=e.getBoundingClientRect(); if(r.right>d.clientWidth+2) bad.push(e.className+':'+Math.round(r.right))});
  return {SW:d.scrollWidth, CW:d.clientWidth, bad:bad.slice(0,5)}})()`)
console.log('OVERFLOW', JSON.stringify(ov))
// 标签在 k=1 时应当**只有中心**可见
const labelsAt1 = await ev(`[...document.querySelectorAll('.mcg-label')].filter(e=>e.getAttribute('opacity')==='1').length`)
console.log('LABELS_AT_K1', labelsAt1)
// 放大：连点 3 次 + 按钮
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-tb-actions .ibtn')].find(x=>x.title==='放大'); for(let i=0;i<3;i++) b.click(); return 1})()`)
await sleep(700)
const labelsZoom = await ev(`[...document.querySelectorAll('.mcg-label')].filter(e=>e.getAttribute('opacity')==='1').length`)
console.log('LABELS_AFTER_ZOOM3', labelsZoom, 'k=', await ev(`document.querySelector('.mcg-canvas')?'':''`))
// 悬停一个非中心节点
const hv = await ev(`(()=>{const g=[...document.querySelectorAll('.mcg-node')].find(n=>!n.classList.contains('is-center'));
  if(!g) return 'none'; const r=g.getBoundingClientRect();
  g.dispatchEvent(new PointerEvent('pointerenter',{bubbles:true,clientX:r.x+r.width/2,clientY:r.y+r.height/2}));
  return {x:Math.round(r.x),y:Math.round(r.y)}})()`)
await sleep(400)
const tip = await ev(`(()=>{const t=document.querySelector('.mcg-tip'); return t?{name:t.querySelector('.mcg-tip-name')?.innerText, cat:t.querySelector('.mcg-tip-cat')?.innerText, body:(t.querySelector('.mcg-tip-content')?.innerText||'').slice(0,50)}:'NO_TIP'})()`)
console.log('HOVER', JSON.stringify(hv), 'TIP', JSON.stringify(tip))
// 截一张放大后的
const r = await ev(`(()=>{const s=document.querySelector('.mcg-sec');const b=s.getBoundingClientRect();return {x:b.x,y:b.y,w:b.width,h:b.height}})()`)
await send('Emulation.setDeviceMetricsOverride',{width:W,height:Math.ceil(r.y+r.h+40),deviceScaleFactor:1,mobile:false},sid)
await sleep(600)
const r2 = await ev(`(()=>{const s=document.querySelector('.mcg-sec');const b=s.getBoundingClientRect();return {x:b.x,y:b.y,w:b.width,h:b.height}})()`)
const shot=await send('Page.captureScreenshot',{format:'png',clip:{x:r2.x,y:r2.y,width:r2.w,height:r2.h,scale:1},captureBeyondViewport:true},sid)
writeFileSync(OUT,Buffer.from(shot.result.data,'base64'))
console.log('SHOT',OUT)
ws.close(); chrome.kill()
