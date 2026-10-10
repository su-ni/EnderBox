import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
const URL_=process.argv[2],OUT=process.argv[3]
const profile=mkdtempSync(join(tmpdir(),'an-'))
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
console.log('TOOLBAR_BTNS', await ev(`Array.from(document.querySelectorAll('.mcg-toolbar button')).map(b=>b.title||b.getAttribute('aria-label')||'?').join(' | ')`))
const cnt=()=>ev(`(()=>{const t=[...document.querySelectorAll('.mcg-label')];return {total:t.length,visible:t.filter(e=>getComputedStyle(e).opacity!=='0').length}})()`)
console.log('BEFORE', JSON.stringify(await cnt()))
// 点「显示全部名字」
await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/显示全部名字|只看中心名字/.test(b.title||''));b&&b.click();return !!b})()`)
await sleep(500)
console.log('AFTER_CLICK', JSON.stringify(await cnt()))
console.log('BTN_TITLE_NOW', await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/显示全部名字|只看中心名字/.test(b.title||''));return b?b.title:'?'})()`))
// 滚轮语义测试：**画布内滚轮 = 缩放，且不滚页面**（用户在 2026-10-09 明确要「滚轮放大」）
// ⚠ 这条断言在加星图旋转那轮之后**改过**：原来是「滚轮要放行给页面滚动」（项目规范#6），
//   但星图是画布不是文档流（地图/PDF/echarts roam 都这样），用户明确要求滚轮缩放。
//   所以现在的正确断言是「页面不动 + 星图缩放」，两个都成立才算过。
await ev(`(()=>{document.querySelector('.mcg-sec').scrollIntoView({block:'start'});return 1})()`)
await sleep(400)
const y0=await ev('scrollY')
const rBefore=await ev(`(()=>{const c=document.querySelector('.mcg-nodes .mcg-node circle');return c?+c.getAttribute('r'):0})()`)
const rc=await ev(`(()=>{const c=document.querySelector('.mcg-canvas');const b=c.getBoundingClientRect();return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}})()`)
await send('Input.dispatchMouseEvent',{type:'mouseWheel',x:rc.x,y:rc.y,deltaX:0,deltaY:-300},sid)
await sleep(500)
const y1=await ev('scrollY')
const rAfter=await ev(`(()=>{const c=document.querySelector('.mcg-nodes .mcg-node circle');return c?+c.getAttribute('r'):0})()`)
console.log('WHEEL_SEMANTICS', JSON.stringify({pageMoved:y1!==y0, rBefore:Math.round(rBefore*100), rAfter:Math.round(rAfter*100), zoomed:rAfter>rBefore}))
const el=await ev(`(()=>{const c=document.querySelector('.mcg-canvas');const b=c.getBoundingClientRect();return {x:Math.round(b.x+b.width/2),y:Math.round(b.y+b.height/2)}})()`)
await send('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false},sid)
await sleep(400)
const shot=await send('Page.captureScreenshot',{format:'png'},sid)
writeFileSync(OUT,Buffer.from(shot.result.data,'base64'))
console.log('SHOT',OUT)
ws.close();chrome.kill()
