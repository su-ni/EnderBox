// 全站溢出体检（sweep）：逐个 URL × 三档宽度跑 uiprobe，量「页面宽 > 视口宽」。
//
// 用法: node tools/sweep.mjs [base] [--w=1440,900,520] [--urls=a,b,c]
//
// 为什么不写在 uiprobe 里：uiprobe 是「一次一页」的 CLI（用完就 die），
// 而 sweep 要跑十几页 × 三档 —— 各跑一次 uiprobe、汇总 stdout，比把两个职责揉在一起清楚。
import { execFileSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'
import { tmpdir } from 'node:os'

const here = dirname(fileURLToPath(import.meta.url))
const probe = join(here, 'uiprobe.mjs')
const NODE = process.execPath

const args = process.argv.slice(2)
const base = args.find((a) => a.startsWith('http')) || 'http://127.0.0.1:23456'
const wArg = args.find((a) => a.startsWith('--w='))
const uArg = args.find((a) => a.startsWith('--urls='))
const WIDTHS = wArg ? wArg.slice(4).split(',').map(Number) : [1440, 900, 520]

const URLS = uArg
  ? uArg.slice(7).split(',')
  : [
      '/?page=home',
      '/?page=console',
      '/?page=players',
      '/?page=plugins',
      '/?page=plugins&mod=market',
      '/?page=plugins&mod=market&pm=AANobbMI',
      '/?page=modpack',
      '/?page=files',
      '/?page=backup', // ⚠ 页面 key 是 backup（不是 backups）—— 写错会静默落到概览页，
                       //   MEASURE 里的 tab 字段就是用来抓这种「量错了页」的假绿。
      '/?page=rules&g=玩家',
      '/?page=config',
      '/?page=rules',
      '/?page=tasks',
      '/?page=settings',
    ]

const MEASURE = `(()=>{const de=document.documentElement;return{sw:de.scrollWidth,cw:de.clientWidth,tab:document.querySelector('.page.on')?.getAttribute('data-page')||'?'}})()`

/** 截图产物统一丢进临时目录 —— sweep 的判据是 PROBE 那几个数字，PNG 只是 uiprobe 要求的占位。 */
const tmpPng = join(tmpdir(), 'sbx-sweep.png')

let bad = 0
let n = 0
for (const u of URLS) {
  for (const w of WIDTHS) {
    let out = ''
    try {
      // ⚠ uiprobe 的 4 个位置参数是**顺序敏感**的：url / out.png / waitForJS / probeJS。
      // 只传 3 个（把 probeJS 顶到 waitForJS 位上）会让它把探针当等待条件 —— 全程无输出、静默失败。
      // out 必须给真实路径（传空串也占位失败）。
      out = execFileSync(
        NODE,
        [
          probe,
          `${base}${u}`,
          tmpPng,
          "document.readyState==='complete'",
          MEASURE,
          `--w=${w}`,
          '--h=1000',
        ],
        { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 60000 },
      )
    } catch (e) {
      out = String(e.stdout || '') + String(e.stderr || '')
    }
    const m = out.match(/PROBE (\{.*\})/)
    n++
    if (!m) {
      bad++
      console.log(`✗ ${u} @${w}  量不到（探针没返回 PROBE）`)
      continue
    }
    const r = JSON.parse(m[1])
    const ovf = r.sw - r.cw
    // ⚠ 必须校验「量到的是不是这一页」：URL 里的 page= 拼错（如 backups vs backup）时
    //   App 会静默回退到概览页，溢出照样是 0 —— 只量溢出等于给自己发全绿。
    const want = new URLSearchParams(u.split('?')[1] ?? '').get('page')
    const wrongPage = want && r.tab && r.tab !== want
    if (ovf > 2 || wrongPage) bad++
    console.log(
      `${ovf > 2 ? '✗ 溢出' : wrongPage ? '✗ 量错页' : '·     '} ${u.padEnd(42)} @${w}  sw=${r.sw} cw=${r.cw} (${ovf >= 0 ? '+' : ''}${ovf})${wrongPage ? `  实际量到 "${r.tab}"，要的是 "${want}"` : ''}`,
    )
  }
}
console.log(`\n${n - bad}/${n} 通过${bad ? `，${bad} 处有问题` : '，零溢出'}`)
process.exit(bad ? 1 : 0)
