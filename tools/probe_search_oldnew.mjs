/*
  对照实验：旧口径（原文 + 实例过滤搜一次 → items[0]） vs 新口径（名字候选 + 两段搜索 + 精确匹配）
  用途：证明「快捷安装通通找不到」的修复**抓得住旧代码**（项目守则 3：拆修复应见红）。
  实测结论（2026-10-10）：旧口径 5 样本 4 个空 → 用户看到的「通通找不到」；新口径 4 个精确命中、1 个诚实提示。
  ⚠ 依赖本机后端在 23333（`/api/mods/search` 是后端代理 Modrinth 的接口）。
  用法: node tools/probe_search_oldnew.mjs
*/
// 样本 = UI 探针跑过的节点名 + Sodium 生态经典名（用户大概率点过的）
const base = 'http://127.0.0.1:23333/api/mods/search'
const LOADER = 'forge', MC = '1.20.1'

const search = async (q, loader, mc) => {
  const u = `${base}?q=${encodeURIComponent(q)}&loader=${encodeURIComponent(loader)}&mc=${encodeURIComponent(mc)}&limit=8`
  return (await (await fetch(u)).json()).items ?? []
}

// 复刻 searchQueryVariants（与 web/src/components/ModProjectDetail.tsx 当前实现一致）
function searchQueryVariants(raw) {
  const out = []
  const push = (s) => { const t = s.trim(); if (t && !out.includes(t)) out.push(t) }
  push(raw)
  push(raw.replace(/^\[[^\]]*\]\s*/, '').replace(/[（(][^）)]*[）)]\s*$/, ''))
  const m = raw.match(/[（(]\s*([A-Za-z][\w'’\- .:&!]*?)\s*[）)]/)
  if (m) push(m[1])
  return out
}
const norm = (s) => s.toLowerCase().replace(/[\s\u3000·・]/g, '')

const names = [
  'Sodium Extra Information',
  '铟 (Indium)',
  '锂 (Lithium)',
  '钠：树叶剔除-非官方版 (SodiumLeafCulling-Unofficial)',
  'Sodium/Embeddium Options API',
]

for (const name of names) {
  // OLD：搜一次原文（带实例过滤），有结果就用 items[0]（旧版行为）；空 = 用户看到「找不到」
  const old = await search(name, LOADER, MC)
  const oldTxt = old.length ? `items[0]=「${old[0].title}」` : '✗ 空 → 提示找不到'

  // NEW：两段搜索 + 精确匹配
  const variants = searchQueryVariants(name)
  let newTxt = '✗ 诚实提示：市场里没搜到精确对应的项目'
  outer: for (const stage of [[LOADER, MC], ['', '']]) {
    for (const v of variants) {
      const items = await search(v, stage[0], stage[1])
      const hit = items.find((h) => variants.some((vv) => norm(h.title) === norm(vv) || norm(h.zhName || '') === norm(vv)))
      if (hit) {
        newTxt = `✓ 精确命中「${hit.title}${hit.zhName ? ' / ' + hit.zhName : ''}」（${stage[0] ? '带过滤' : '脱过滤'}阶段）→ 弹层`
        break outer
      }
    }
  }
  console.log(`样本「${name}」  候选=${JSON.stringify(variants)}`)
  console.log(`  OLD: ${oldTxt}`)
  console.log(`  NEW: ${newTxt}`)
}
