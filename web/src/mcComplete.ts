/**
 * Minecraft 命令补全引擎 —— 纯函数，不碰 DOM。
 *
 * 为什么单独拆出来：补全是「一堆 if 加字符串切分」，最容易写出一堆边界 bug
 * （光标在中途、参数里有空格、引号包着的文本…）。做成纯函数之后，
 * 每种情况都能单独喂输入验一遍，不用开浏览器点。
 *
 * 行为对齐 MC 客户端的习惯：
 *   `/`            → 列出全部命令
 *   `/gi`          → 挑出 /give
 *   `/give `       → 给玩家候选（含 @a @p @r @s @e）
 *   `/give @a `    → 给物品候选
 *   `/gamerule `   → 给规则名候选
 *   `/gamemode `   → 给模式候选（survival/creative/…）
 * 候选为空时**不弹面板**（宁可不补，也不弹一片空）。
 */
import { ALL_COMMANDS, COMMON_GAMERULES, findCommand, PLUGIN_COMMANDS } from './mcCommands'

export type Candidate = {
  /** 选中后写进输入框的文本 */
  text: string
  /** 列表主文案，默认同 text */
  label?: string
  /** 右侧灰字说明 */
  hint?: string
  kind: 'command' | 'plugin' | 'player' | 'value'
}

export type Completion = {
  /** 要被替换掉的区间 [start, end) */
  start: number
  end: number
  items: Candidate[]
}

export type Ctx = {
  /** 在线玩家名（用来补 <玩家> 这类参数） */
  players?: string[]
  /** 已知物品 ID（没有就不给物品候选，别编） */
  items?: string[]
}

/** 实体选择器。原版一律支持，和有没有人在线无关 */
const SELECTORS = ['@a', '@p', '@r', '@s', '@e', '@a[', '@p[']

function commandCandidates(frag: string): Candidate[] {
  const f = frag.toLowerCase()
  const out: Candidate[] = []
  for (const c of ALL_COMMANDS) {
    const isPlugin = PLUGIN_COMMANDS.includes(c)
    const kind = isPlugin ? 'plugin' : 'command'
    // 正名和别名都要能补出来 —— 只补正名的话，敲 `/tp` 会一个候选都没有
    for (const n of [c.name, ...(c.aliases ?? [])]) {
      if (n.startsWith(f)) out.push({ text: n, hint: c.desc, kind })
    }
  }
  // 原版排前面：装了插件的服也以原版命令为主，插件命令是补充
  return out.sort((a, b) => {
    if (a.kind !== b.kind) return a.kind === 'command' ? -1 : 1
    return a.text.localeCompare(b.text)
  })
}

function argCandidates(type: string, values: string[] | undefined, frag: string, ctx: Ctx): Candidate[] {
  const f = frag.toLowerCase()
  let pool: string[] = []
  switch (type) {
    case 'enum':
      pool = values ?? []
      break
    case 'player':
      // 选择器在前、真实玩家名在后 —— 和 MC 客户端的顺序一致
      pool = [...SELECTORS.slice(0, 5), ...(ctx.players ?? [])]
      break
    case 'gamerule':
      pool = COMMON_GAMERULES
      break
    case 'item':
      pool = ctx.items ?? []
      break
    default:
      return []
  }
  const seen = new Set<string>()
  return pool
    .filter((v) => {
      if (!v.toLowerCase().startsWith(f)) return false
      if (seen.has(v)) return false
      seen.add(v)
      return true
    })
    .map((v) => ({
      text: v,
      kind: (v.startsWith('@') ? 'player' : 'value') as Candidate['kind'],
    }))
}

/**
 * 算候选。`caret` 是光标位置（不传就认为在末尾）。
 *
 * 补全只发生在**光标所在的那个 token** 上：`/give @a █ sword` 这种把光标停在中间的情况，
 * 也只替换 `@a` 那一段，不会把后面的 `sword` 吃掉。
 */
export function complete(line: string, caret?: number, ctx: Ctx = {}): Completion | null {
  const pos = caret ?? line.length
  if (!line.startsWith('/') || pos < 0 || pos > line.length) return null

  const before = line.slice(0, pos)
  const after = line.slice(pos)

  // 光标前最后一段非空白 = 正在输入的片段
  const fragMatch = /(\S*)$/.exec(before)
  const frag = fragMatch ? fragMatch[1] : ''
  const start = pos - frag.length
  // 替换到下一个空白为止（把光标后面的半个词也吃掉，才是「补全」而不是「插入」）
  const tailMatch = /^\S*/.exec(after)
  const end = pos + (tailMatch ? tailMatch[0].length : 0)

  const head = before.slice(0, start)
  const tokens = head.trim().length ? head.trim().split(/\s+/) : []

  // 还没打完命令名
  if (tokens.length === 0) {
    // ⚠⚠ 片段必须**剥掉开头的 `/`**。它是命令行提示符，不是命令名的一部分；
    //     带着它去匹配，就等于在问「哪个命令以 / 开头」—— 一个都匹配不到，
    //     表现是「补全完全不工作」。这条是自测 `_complete_test.ts` 抓出来的。
    const hasSlash = frag.startsWith('/')
    return {
      start: hasSlash ? start + 1 : start,
      end,
      items: commandCandidates(hasSlash ? frag.slice(1) : frag),
    }
  }

  const cmd = findCommand(tokens[0].slice(1))
  if (!cmd || !cmd.args || cmd.args.length === 0) return { start, end, items: [] }

  const argIndex = tokens.length - 1
  // rest 参数往后全用它自己的类型
  const arg = cmd.args[Math.min(argIndex, cmd.args.length - 1)]
  if (!arg) return { start, end, items: [] }
  if (argIndex >= cmd.args.length && !cmd.args[cmd.args.length - 1].rest) {
    return { start, end, items: [] }
  }

  return { start, end, items: argCandidates(arg.type, arg.values, frag, ctx) }
}

/** 把某个候选写回整行，返回新行与新光标位置（补完自动补一个空格，接着打下一个参数） */
export function applyCompletion(
  line: string,
  comp: Completion,
  text: string,
): { line: string; caret: number } {
  const next = line.slice(0, comp.start) + text + line.slice(comp.end)
  const caret = comp.start + text.length
  // 已经是行尾才补空格，中间替换不动后面的内容
  const atEnd = comp.end >= line.length
  return { line: atEnd ? next + ' ' : next, caret: atEnd ? caret + 1 : caret }
}

/** 命令名（不含参数）—— 给「命令历史去重」之类的用途 */
export function commandNameOf(line: string): string {
  const m = /^\/(\S+)/.exec(line)
  return m ? m[1] : ''
}
