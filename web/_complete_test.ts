/**
 * 命令补全引擎的自测。前端没有测试框架（没有 vitest/jest），
 * 所以这份用 esbuild 打包成 js、再用 node 直接跑：
 *
 *   npx esbuild _complete_test.ts --bundle --format=esm --outfile=.tmp_test.mjs && node .tmp_test.mjs
 *
 * 为什么值得单独写：补全是「字符串切分 + 一堆 if」，边界情况（光标在中间、
 * 参数带空格、命令不存在）光靠手点很难穷尽，而它错了的表现是「补出一个不存在的命令」——
 * 用户会以为服务器支持，比不补更糟。
 */
import { applyCompletion, complete } from './src/mcComplete'

let pass = 0
let fail = 0

function check(name: string, cond: boolean, extra?: unknown) {
  if (cond) {
    pass++
    console.log('  ok   ' + name)
  } else {
    fail++
    console.log('  FAIL ' + name + (extra === undefined ? '' : '  → ' + JSON.stringify(extra)))
  }
}

function texts(line: string, caret?: number, ctx?: Parameters<typeof complete>[2]) {
  const c = complete(line, caret, ctx)
  return c ? c.items.map((i) => i.text) : []
}

console.log('\n[1] 命令名补全')
check('/ 列出全部命令', texts('/').length > 70, texts('/').length)
check('/gi 命中 give', texts('/gi').includes('give'))
check('/give 只有 give 及其前缀', texts('/give').every((t) => t.startsWith('give')), texts('/give'))
check('原版命令排在插件前', (() => {
  const t = texts('/')
  const firstPlugin = t.findIndex((x) => x === 'home' || x === 'tps')
  const lastVanilla = t.lastIndexOf('xp')
  return firstPlugin > lastVanilla
})())

console.log('\n[2] 参数补全')
check('/gamemode 给四种模式', (() => {
  const t = texts('/gamemode ')
  return t.includes('survival') && t.includes('spectator') && t.length === 4
})(), texts('/gamemode '))
check('/gamerule 给规则名', texts('/gamerule ').includes('keepInventory'))
check('/whitelist 给子命令', (() => {
  const t = texts('/whitelist ')
  return t.includes('add') && t.includes('reload')
})(), texts('/whitelist '))
check('/give 第一格是玩家（含选择器）', texts('/give ').includes('@a'))
check('/give @a 第二格吃物品（ctx 为空则无候选）', texts('/give @a ').length === 0)
check('/give @a 有 ctx.items 时给物品', texts('/give @a ', undefined, { items: ['diamond', 'dirt'] }).includes('diamond'))
check('玩家名进候选', texts('/tp ', undefined, { players: ['Steve', 'Alex'] }).includes('Alex'))
check('/stop 无参数 → 不给候选', texts('/stop ').length === 0)
check('不存在的命令 → 不给候选', texts('/nosuchcmd ').length === 0)

console.log('\n[3] 光标位置')
check('光标在中间时只替换那一格', (() => {
  //          /give @a x       caret 停在 @a 中间(t=7)
  const c = complete('/give @a ax', 7)
  return c !== null && c.start === 6 && c.end === 8
})())
check('光标在最左（还没打命令）→ 补命令名', (() => {
  const c = complete('/giv', 0)
  return c !== null && c.items.length > 0
})())
check('不以 / 开头 → 不补', complete('give ') === null)

console.log('\n[4] 写回')
check('行尾补全自动加空格', (() => {
  const c = complete('/gi')
  const r = applyCompletion('/gi', c!, 'give')
  return r.line === '/give ' && r.caret === 6
})())
check('中间替换不动后面的内容', (() => {
  const c = complete('/give @a ax', 7)
  const r = applyCompletion('/give @a ax', c!, '@a')
  return r.line === '/give @a ax'
})())

console.log('\n[5] 前缀过滤大小写不敏感')
check('/GI 也能命中 give', texts('/GI').includes('give'))

console.log(`\n通过 ${pass} · 失败 ${fail}\n`)
if (fail > 0) process.exitCode = 1
