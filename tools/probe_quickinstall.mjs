/*
  快捷安装体检（2026-10-10 回归探针）——用户反馈「点击快捷安装通通提示找不到」。
  链路：点星图节点 → 右侧面板 → 快捷安装 → 市场两段搜索（先带实例过滤、再脱过滤）
        → ① 命中项目 → 弹层（有版本列表 / 「没有匹配的版本」）
        → ② 未命中 → toast「市场里没搜到和「…」精确对应的项目」
  判据：对 Modrinth 上真实存在的模组（铟/钠/磷 这类邻居），必须出现**弹层**——
        「项目找得到、只是可能没有适配当前实例的版本」，而不是旧版「通通提示找不到」。

  ⚠ v2 修正了三个探针自身的坑（v1 首跑全灭的原因）：
    ① 合成事件没有中间 mouseMoved → hover 卡片 .mcg-tip 停在旧位置、挡住新点击点
       （真用户「移到→按下」天然避开：tooltip 永远在鼠标右下 14px，点击点在鼠标处）。
       → v2 每次点击前先 mouseMoved 到目标、等 hover 刷新。
    ② 预取坐标在面板开合 / 布局变化后失效 → v2 每 trial 从 label 重新找节点、重算坐标。
    ③ 节点圆圈重叠（大圆压小圆）→ v2 用 elementFromPoint 逐候选点校验，落点必须归属目标节点。
  输出: QI_NODES / SPIN_CHECK / QI_TRIAL<i> / QI_SUMMARY / VERDICT_QUICKINSTALL
  用法: node tools/probe_quickinstall.mjs <URL>
*/
import { spawn } from 'node:child_process'
import { readFileSync, mkdtempSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

const URL_ = process.argv[2]
const profile = mkdtempSync(join(tmpdir(), 'qi-'))
const chrome = spawn('C:/Program Files/Google/Chrome/Application/chrome.exe', [
  '--headless=new', '--disable-gpu', '--no-first-run', '--hide-scrollbars',
  '--window-size=1440,1000', '--user-data-dir=' + profile, '--remote-debugging-port=0', 'about:blank'], { stdio: 'ignore' })
const pf = join(profile, 'DevToolsActivePort')
let port = 0
for (let i = 0; i < 60; i++) { try { port = Number(readFileSync(pf, 'utf8').split('\n')[0]); if (port) break } catch { } await new Promise(r => setTimeout(r, 200)) }
const ver = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json()
const ws = new WebSocket(ver.webSocketDebuggerUrl)
await new Promise(r => ws.addEventListener('open', r, { once: true }))
let id = 0; const pend = new Map()
ws.addEventListener('message', e => { const m = JSON.parse(e.data); if (m.id && pend.has(m.id)) { pend.get(m.id)(m); pend.delete(m.id) } })
const send = (m, p = {}, s) => new Promise(res => { const i = ++id; pend.set(i, res); ws.send(JSON.stringify({ id: i, method: m, params: p, sessionId: s })) })
const { result: { targetId } } = await send('Target.createTarget', { url: 'about:blank' })
const { result: { sessionId: sid } } = await send('Target.attachToTarget', { targetId, flatten: true })
await send('Page.enable', {}, sid); await send('Runtime.enable', {}, sid)
await send('Page.navigate', { url: URL_ }, sid)
const sleep = ms => new Promise(r => setTimeout(r, ms))
const ev = async e => {
  const r = await send('Runtime.evaluate', { expression: e, returnByValue: true, awaitPromise: true }, sid)
  if (r.result?.exceptionDetails) console.log('EV_EXC', (r.result.exceptionDetails.exception?.description || '').split('\n')[0].slice(0, 200))
  return r.result?.result?.value
}

for (let i = 0; i < 40; i++) { if (await ev(`!!document.querySelector('.mcg-canvas .mcg-node')`)) break; await sleep(500) }
await sleep(1500)
await ev(`document.querySelector('.mcg-canvas').scrollIntoView({block:'center'})`)
await sleep(600)

// 复位 + 停自转：目标节点坐标在「选取 → 点击」之间必须稳定
const resetBtn = `(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>(b.title||'')==='复位视图');b&&b.click();return 1})()`
const stopSpin = `(()=>{const b=[...document.querySelectorAll('.mcg-toolbar button')].find(b=>/停止自转/.test(b.title||''));b&&b.click();return 1})()`
await ev(resetBtn); await sleep(1200)
await ev(stopSpin); await sleep(400)

// 自转是否真停：两次快照比最大位移（呼吸 jitter ±2~4px，>6px 说明还在转）
{
  const posSnap = `(()=>{const o={};document.querySelectorAll('.mcg-canvas .mcg-node').forEach(n=>{const c=n.querySelector('circle');const b=c.getBoundingClientRect();o[n.dataset.nid]=[Math.round(b.x*10)/10,Math.round(b.y*10)/10]});return o})()`
  const p1 = await ev(posSnap); await sleep(900); const p2 = await ev(posSnap)
  let maxd = 0
  if (p1 && p2) for (const k in p1) { if (p2[k]) maxd = Math.max(maxd, Math.hypot(p2[k][0] - p1[k][0], p2[k][1] - p1[k][1])) }
  console.log('SPIN_CHECK maxDelta', maxd.toFixed(1), 'px', maxd < 6 ? '✓ 自转已停' : '⚠ 仍在转，点击会飘')
}

const centerName = await ev(`(()=>{const n=document.querySelector('.mcg-node.is-center .mcg-label');return n?n.textContent:''})()`)
/* 与产品 shortName 同规则：>14 字截成前 13 字 + '…'（label 是截断名、面板是全名，比对要归一） */
const sn = (s) => { const r = [...s]; return r.length > 14 ? r.slice(0, 13).join('') + '…' : s }

// ── 候选：prefer 命中优先（短名>截断名）、远离中心 110px、viewport 内，最多 3 个 ──
const targets = await ev(`(()=>{
  const prefer=['铟','锂','磷','钠','Indium','Lithium','Phosphor','Sodium']
  const cb=document.querySelector('.mcg-canvas').getBoundingClientRect()
  const ccx=cb.left+cb.width/2, ccy=cb.top+cb.height/2
  const rows=[]
  for(const n of document.querySelectorAll('.mcg-canvas .mcg-node')){
    if(n.classList.contains('is-center')) continue
    const name=((n.querySelector('.mcg-label')||{}).textContent||'')
    const c=n.querySelector('circle'); const b=c.getBoundingClientRect()
    if(b.width<0.5) continue
    const x=b.x+b.width/2, y=b.y+b.height/2
    if(x<40||x>innerWidth-40||y<70||y>innerHeight-70) continue
    if(Math.hypot(x-ccx,y-ccy)<80) continue
    const pri=prefer.findIndex(p=>name.startsWith(p))
    rows.push({name, pri: pri<0?99:pri, trunc: name.includes('…')?1:0, len:[...name].length})
  }
  rows.sort((a,b)=> a.pri-b.pri || a.trunc-b.trunc || a.len-b.len)
  return rows.slice(0,3).map(r=>r.name)
})()`)
console.log('QI_NODES 中心=', JSON.stringify(centerName), ' 候选=', JSON.stringify(targets))

// 按 label 重新找节点 + elementFromPoint 校验可点位置（外偏候选点兜底重叠遮挡）
const pickPoint = async (name) => await ev(`(()=>{
  const g=[...document.querySelectorAll('.mcg-canvas .mcg-node')].find(n=>((n.querySelector('.mcg-label')||{}).textContent||'')===${JSON.stringify(name)})
  if(!g) return {err:'gone'}
  const c=g.querySelector('circle'); const b=c.getBoundingClientRect()
  const x=b.x+b.width/2, y=b.y+b.height/2, r=Math.max(6,b.width/4)
  const dirs=[[1,0],[-1,0],[0,1],[0,-1],[0.71,0.71],[-0.71,-0.71],[0.71,-0.71],[-0.71,0.71]]
  const cands=[[x,y]]
  for(const s of [0.45,0.8]) for(const d of dirs) cands.push([x+d[0]*r*s, y+d[1]*r*s])
  for(const [px,py] of cands){
    if(px<2||py<2||px>innerWidth-2||py>innerHeight-2) continue
    const el=document.elementFromPoint(px,py)
    const hit=el&&el.closest?el.closest('.mcg-node'):null
    if(hit===g) return {x:Math.round(px),y:Math.round(py)}
  }
  return {x:Math.round(x),y:Math.round(y),blind:true}
})()`)

const moveTo = (x, y) => send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y, buttons: 0 }, sid)
const clickAt = async (x, y) => {
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', buttons: 1, clickCount: 1 }, sid)
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', buttons: 0, clickCount: 1 }, sid)
}
const readPanelName = async () => await ev(`(()=>{const s=document.querySelector('.mcg-side');return s?((s.querySelector('.mcg-side-name')||{}).textContent||''):null})()`)
const closePanel = async () => { await ev(`(()=>{const x=document.querySelector('.mcg-side .mcg-side-x');if(x)x.click();return 1})()`); await sleep(300) }

const stats = []
for (let i = 0; i < targets.length; i++) {
  const want = targets[i]
  const href0 = await ev(`location.href`)
  let pt = await pickPoint(want)
  let panelName = null
  for (let attempt = 0; attempt < 2; attempt++) {
    if (!pt || pt.err) break
    // ⚠ 先 mouseMoved 再点击：让 hover 卡片刷新到「目标右下 14px」，不再挡点击点
    await moveTo(pt.x - 120, pt.y - 80); await sleep(120)
    await moveTo(pt.x, pt.y); await sleep(400)
    await clickAt(pt.x, pt.y)
    await sleep(850)
    panelName = await readPanelName()
    if (panelName) break
    pt = await pickPoint(want) // 重算坐标再试一次
  }
  const href1 = await ev(`location.href`)
  if (href0 !== href1) console.log(`  (页面已跳转: ${href1})`)

  if (!panelName) {
    console.log(`QI_TRIAL${i + 1} 目标「${want}」 pt=${JSON.stringify(pt)} result=no-panel`)
    stats.push({ name: want, result: 'no-panel' })
    continue
  }
  if (panelName === centerName) { // 点到中心（重叠遮挡），非产品缺陷
    console.log(`QI_TRIAL${i + 1} 目标「${want}」 result=hit-center（面板=「${panelName}」）`)
    stats.push({ name: want, result: 'hit-center' })
    await closePanel()
    continue
  }
  if (panelName !== want && sn(panelName) !== want) {
    console.log(`QI_TRIAL${i + 1} 目标「${want}」 result=mismatch（实际面板=「${panelName}」）`)
    stats.push({ name: want, result: 'mismatch' })
    await closePanel()
    continue
  }

  const btnOk = await ev(`(()=>{const b=[...document.querySelectorAll('.mcg-side-actions button.btn')].find(b=>b.textContent.includes('快捷安装'));if(!b)return false;b.click();return true})()`)
  if (!btnOk) {
    console.log(`QI_TRIAL${i + 1} 节点「${panelName}」 result=no-button`)
    stats.push({ name: panelName, result: 'no-button' })
    await closePanel()
    continue
  }

  /*
    轮询等待结果（两段搜索最多 2×3 次请求，网络好时 1~2s 出结果）。
    ⚠ toast 只活 5s：一旦「看到过」就记下，后续 detail 抓不到也不影响判定。
    ⚠ busy 兜底：按钮从「找版本中…」回到「快捷安装」= 流程已结束。
  */
  let st = { dep: false, noProj: false, busy: true, toast: '' }
  for (let k = 0; k < 28; k++) {
    const r = await ev(`(()=>{
      const card=document.querySelector('.dep-card')
      const txt=document.body.innerText
      const btn=[...document.querySelectorAll('.mcg-side-actions button.btn')].find(b=>/快捷安装|找版本中/.test(b.textContent))
      return {dep:!!card, noProj:txt.includes('没搜到和'), busy:!!(btn&&btn.disabled),
              toast:(txt.match(/市场里没搜到[^\\n]{0,90}/)||[''])[0]}
    })()`)
    if (!r) { console.log('  (轮询读到空值，页面可能正在导航)') ; break }
    st = r
    if (st.dep || st.noProj) break
    if (!st.busy && k >= 6) break
    await sleep(500)
  }
  const sawNoProj = st.noProj, sawToast = st.toast
  await sleep(2600) // 找版本阶段（find→vers）落定

  const detail = await ev(`(()=>{
    const card=document.querySelector('.dep-card')
    const dlg=card?card.closest('.dlg'):null
    const txt=document.body.innerText
    return {
      dep:!!card, noVer:txt.includes('没有匹配的版本'), noProj:txt.includes('没搜到和'),
      rows:document.querySelectorAll('.dep-card .vrow').length,
      env:(txt.match(/环境[^\\n]{0,60}/)||[''])[0],
      head:(dlg?dlg.innerText.replace(/\\s+/g,' ').slice(0,190):'')
    }
  })()`) ?? {}
  const result = detail.dep
    ? (detail.noVer ? 'dep-card-no-version' : (detail.rows > 0 ? 'dep-card-with-versions' : 'dep-card-loading'))
    : ((sawNoProj || detail.noProj) ? 'toast-no-project' : 'unknown')
  stats.push({ name: panelName, result })
  console.log(`QI_TRIAL${i + 1} 节点「${panelName}」 result=${result} 版本行=${detail.rows ?? '?'}`)
  console.log(`  弹层头: ${detail.head || '（无）'}`)
  if (detail.env) console.log(`  环境行: ${detail.env}`)
  if (result === 'toast-no-project') console.log(`  toast: ${sawToast || '（已过期，但轮询时见到过）'}`)

  // 清场：关弹层 → 关面板 → 复位
  await ev(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='稍后再说');if(b){b.click();return 1}const ov=document.querySelector('.dlg-overlay');if(ov){ov.click();return 1}return 0})()`)
  await sleep(500)
  await closePanel()
  await ev(resetBtn); await sleep(700)
}

const depN = stats.filter(s => s.result.startsWith('dep-card')).length
const toastN = stats.filter(s => s.result === 'toast-no-project').length
console.log('QI_SUMMARY', JSON.stringify(stats))
console.log('VERDICT_QUICKINSTALL', depN >= 1
  ? `✓ 项目搜得到（${depN} 次弹层：${stats.filter(s => s.result.startsWith('dep-card')).map(s => s.name + '→' + s.result.replace('dep-card-', '')).join('、')}）` +
    (toastN ? `；另有 ${toastN} 次诚实提示（名字不在 Modrinth 精确匹配）` : '')
  : '✗ 一次弹层都没出现 —— 快捷安装仍不通')

ws.close(); chrome.kill()
