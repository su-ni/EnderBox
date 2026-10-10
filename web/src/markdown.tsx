import { Fragment, type ReactNode } from 'react'

// 市场正文渲染：把整合包 / 模组详情页的 body 画出来。
//
// 为什么自己写而不引一个 markdown 库：
//   ① 为 30 行用到的东西拉一整个依赖不划算；
//   ② 更关键的 —— **这条内容来自第三方**（Modrinth 上任意作者写的），
//      通用库的默认用法几乎必开 dangerouslySetInnerHTML。本文件的安全边界只有一句：
//      **全程不出现 innerHTML，只产出 React 元素**，文本一律交给 React 自动转义。
//
// ⚠ 支持的是**混合方言**，不是标准 Markdown。
// 实测（2026-10-06，抽样 60 个整合包正文，全部非空，中位 4KB、最大 53KB）：
//   Markdown 侧：链接 46/60、图片 38、加粗 37、标题 36、无序列表 34、分割线 15、斜体 11、行内代码 8、有序列表 8、引用 7
//   **裸 HTML 40/60**：<iframe> 21、<details>/<summary> 19、<img> 18、<a> 18、<p>/<br> 15、<strong> 8、<center> 6
// 也就是说三分之一的正文本身是 HTML。只认 Markdown 的话，用户看到的是满屏 `<details>` 标签原样印出来 ——
// 这和项目早先在别处踩过的「Markdown 标记印到界面上」是同一个坑，只是方向反过来。
//
// 所以这里不开「允许任意 HTML」的口子，而是**只认一张标签白名单**：
//   <a href> / <img src> / <strong> <b> / <em> <i> / <code> / <br> / <details> <summary> / <iframe>（降级成链接）
//   <p> <div> <center> <span> 这类纯容器：丢标签、**留内容**（它们是排版，不是外链资源）
// 白名单外的标签连同标签一起丢掉，不等于给 HTML 开门。

// ── 内联类型 ──────────────────────────────────────────────
// 为什么多这一层中间结构、而不是一边扫一边吐 JSX：加粗里套链接、链接里套代码这种嵌套，
// 直接拼 JSX 会在「先遇到哪个结束标记」上打架。先解析成树再渲染，嵌套自然闭合。

type Inline =
  | { k: 'text'; v: string }
  | { k: 'strong' | 'em'; kids: Inline[] }
  | { k: 'code'; v: string }
  | { k: 'link'; href: string; kids: Inline[] }
  | { k: 'img'; src: string; alt: string }
  | { k: 'br' }

/**
 * HTML 实体表。
 *
 * 为什么它不是「三五个就够」：实测抽样 79 个整合包正文，真正出现过的只有
 * nbsp(195) / eacute(17) / amp(14) / rsquo(6) / ndash(1) —— 但那是**恰好被抽到的那批**。
 * 缺一个的表现很难看：界面上印着 `Capture Pok&eacute;mon`（这轮就抓到了这一个）。
 * 所以这里按「拉丁字母变音 + 常见排版符号」收一份够用的，剩下的交给数字实体那条通用规则。
 */
const ENTITIES: Record<string, string> = {
  nbsp: '\u00a0',
  amp: '&',
  lt: '<',
  gt: '>',
  quot: '"',
  apos: "'",
  // 西欧变音字母
  eacute: 'é',
  egrave: 'è',
  agrave: 'à',
  aacute: 'á',
  atilde: 'ã',
  auml: 'ä',
  ccedil: 'ç',
  iacute: 'í',
  ntilde: 'ñ',
  oacute: 'ó',
  ouml: 'ö',
  otilde: 'õ',
  oslash: 'ø',
  uacute: 'ú',
  uuml: 'ü',
  szlig: 'ß',
  // 排版符号
  hellip: '…',
  mdash: '—',
  ndash: '–',
  lsquo: '\u2018',
  rsquo: '\u2019',
  ldquo: '\u201c',
  rdquo: '\u201d',
  laquo: '«',
  raquo: '»',
  middot: '·',
  bull: '•',
  dagger: '†',
  // 常见符号
  copy: '©',
  reg: '®',
  trade: '™',
  deg: '°',
  plusmn: '±',
  times: '×',
  divide: '÷',
  frac12: '½',
  euro: '€',
  pound: '£',
  yen: '¥',
  cent: '¢',
  sect: '§',
  para: '¶',
  larr: '←',
  rarr: '→',
  harr: '↔',
  check: '✓',
}

/**
 * 解实体。
 *
 * 两条规则：① 名字在表里的按表解；② **数字实体一律按码点解**（`&#233;` / `&#xE9;`）——
 * 这条是通用的，表里没收的字符靠它兜住，不用为了一个新字符回来改代码。
 * ⚠ 单次扫描、不递归：`&amp;eacute;` 应该得到字面的 `&eacute;` 而不是 `é`（作者想显示的就是这个）。
 */
function decodeEntities(s: string): string {
  if (s.indexOf('&') < 0) return s
  return s.replace(/&(#[0-9]+|#[xX][0-9a-fA-F]+|[a-zA-Z][a-zA-Z0-9]*);/g, (m, body: string) => {
    if (body[0] === '#') {
      const code = body[1] === 'x' || body[1] === 'X' ? parseInt(body.slice(2), 16) : Number(body.slice(1))
      // 码点范围要挡一下：`&#0;` 会产出 \0，`&#1114112;` 超出 Unicode 会抛异常。
      if (!Number.isFinite(code) || code <= 0 || code > 0x10ffff) return ''
      try {
        return String.fromCodePoint(code)
      } catch {
        return ''
      }
    }
    return ENTITIES[body.toLowerCase()] ?? m
  })
}

/**
 * URL 白名单。这是本文件**唯一**一条安全相关判据，别绕过：
 * 只放行 http / https / mailto，`javascript:`、`data:`、`vbscript:` 一律作废。
 * 正文是任意第三方写的 —— 不校验协议，一个 `[点我](javascript:alert(1))` 就够了。
 */
function safeUrl(u: string): string {
  const s = (u ?? '').trim()
  if (/^(https?:\/\/|mailto:)/i.test(s)) return s
  return ''
}

/** `<a>`/`<img>` 里取属性值。属性顺序不固定，只能按名取，不能指望它是第一个。 */
function attr(raw: string, name: string): string {
  const m = raw.match(new RegExp(`\\b${name}\\s*=\\s*(["'])([^"']*)\\1`, 'i'))
  return m ? decodeEntities(m[2]) : ''
}

// ── 内联解析 ──────────────────────────────────────────────

// 白名单里「纯容器」的标签（`<p>` `<div>` `<center>` `<span>` 这类）由 parseInline 末尾那条
// 通用规则统一丢掉 —— 它们只承担排版（居中、分段），留着也是无害的换行，逐个实现没有意义。
// 不再单列一张表：多一张表就多一次「加了标签忘了同步」的机会。

/**
 * 解析一段行内内容成树。
 *
 * 采用「在剩余串里找**最早出现**的那个标记」而不是「按类型依次替字符串」：
 * 后者会让 `[![封面](a.png)](b.html)` 这种套娃先把外层链接吃掉、
 * 里面的感叹号方括号就变成普通文本积木 —— 用户在市场站上看到的明明是一张可点的封面图。
 */
function parseInline(src: string): Inline[] {
  const out: Inline[] = []
  let rest = src
  let pending = ''

  const flush = () => {
    if (pending) {
      out.push({ k: 'text', v: decodeEntities(pending) })
      pending = ''
    }
  }

  const push = (node: Inline) => {
    flush()
    out.push(node)
  }

  while (rest) {
    // 每个候选：[匹配到的起始下标, 这段标记的长度, 生成什么节点]
    type Cand = [number, number, Inline]
    const cands: Cand[] = []
    const add = (m: RegExpMatchArray | null, make: (m: RegExpMatchArray) => Inline) => {
      if (m && m.index !== undefined) cands.push([m.index, m[0].length, make(m)])
    }

    // 行内代码先于一切：反引号里的星号 / 方括号**不该**被当成标记
    add(rest.match(/`([^`]+)`/), (m) => ({ k: 'code', v: m[1] }))
    add(rest.match(/<(code)\b[^>]*>([\s\S]*?)<\/\1>/i), (m) => ({ k: 'code', v: m[2] }))

    /*
      ⚠ 结尾那个 `(?:\s*\))?` **不能省**。
      以前这里只匹配到 `[文字](地址`，闭合的 `)` 就留在正文里当普通文字印出来 ——
      实测每一个 Markdown 链接后面都跟着一个孤零零的 `)`：
      「Also check out Fabric Loader**)** , the …」。作者写的是标准写法，
      多出来的括号是我们吃掉了左括号却没吃右括号。
      写成「有就吃、没有也认」而不是硬要求右括号：真实正文里偶尔有
      地址本身带不成对括号的野写法（`[x](http://a(b)`），硬要求会让整条链接
      退化成原文印出来，比现在更糟。
    */
    add(rest.match(/!\[([^\]]*)\]\(\s*([^)\s]+)(?:\s*\))?/), (m) => {
      const u = safeUrl(m[2])
      return u ? { k: 'img', src: u, alt: m[1] } : { k: 'text', v: m[1] || '(图片)' }
    })
    add(rest.match(/<img\b[^>]*>/i), (m) => {
      const u = safeUrl(attr(m[0], 'src'))
      // 拿不到合法 src 就整个丢掉：一个破图占位比少一张图更刺眼。
      if (!u) return { k: 'text', v: '' }
      return { k: 'img', src: u, alt: attr(m[0], 'alt') }
    })

    add(rest.match(/\[([^\]]*)\]\(\s*([^)\s]+)(?:\s*\))?/), (m) => {
      const u = safeUrl(m[2])
      if (!u) return { k: 'text', v: m[1] }
      return { k: 'link', href: u, kids: parseInline(m[1]) }
    })
    add(rest.match(/<a\b[^>]*>([\s\S]*?)<\/a>/i), (m) => {
      const u = safeUrl(attr(m[0], 'href'))
      if (!u) return { k: 'text', v: m[1] }
      return { k: 'link', href: u, kids: parseInline(m[1]) }
    })

    add(rest.match(/\*\*([\s\S]+?)\*\*/), (m) => ({ k: 'strong', kids: parseInline(m[1]) }))
    add(rest.match(/<(strong|b)\b[^>]*>([\s\S]*?)<\/\1>/i), (m) => ({
      k: 'strong',
      kids: parseInline(m[2]),
    }))
    add(rest.match(/__([\s\S]+?)__/), (m) => ({ k: 'strong', kids: parseInline(m[1]) }))

    // 行内的 `*` 只在**词边界**处才当斜体标记，否则 `2*3*4` 会被排在空中。
    add(rest.match(/(?<![^\s(*])\*([^*\n]+?)\*(?![\w*])/), (m) => ({
      k: 'em',
      kids: parseInline(m[1]),
    }))
    add(rest.match(/<(em|i)\b[^>]*>([\s\S]*?)<\/\1>/i), (m) => ({ k: 'em', kids: parseInline(m[2]) }))

    add(rest.match(/<br\s*\/?>/i), () => ({ k: 'br' }))

    // <iframe>：正文里多是 YouTube / Discord 挂件。**不内联它** ——
    // 在一台开服面板里自动播视频、自动跑第三方脚本，既惹人烦也是不必要的攻击面。
    // 降级成一条链接：想要的人点得动，不想要的人不会被打扰。
    add(rest.match(/<iframe\b[^>]*>/i), (m) => {
      const u = safeUrl(attr(m[0], 'src'))
      if (!u) return { k: 'text', v: '' }
      return { k: 'link', href: u, kids: [{ k: 'text', v: '嵌入内容（视频 / 挂件）' }] }
    })

    // 兜底：任何**看起来像标签**的东西都丢掉（只丢标签，内容继续扫）。
    // 没有这一条，`<span style=…>`、`<font color=…>`、`<u>`、作者自造标签会原样印到界面上 ——
    // 实测 Cobblemon 的正文里有 66 个 `<span>` + 16 个 `<font>`，全漏出来就是满屏尖括号。
    // ⚠ 这里**必须排在最后**参与「最早出现」的比较，并且靠长度择优先于其它候选：
    // 同一起点上 `<a href=…>文字</a>` 比裸标签长，不会被它抢走。
    add(rest.match(/<\/?[a-zA-Z][a-zA-Z0-9-]*(?:\s[^<>]*)?\/?>/), () => ({ k: 'text', v: '' }))

    if (cands.length === 0) {
      // 没认出任何标记：把这一整段当文本收掉，结束。
      pending += rest
      break
    }

    // 同一起点上**长的优先**：`<a href=x>文</a>` 必须赢过兜底那条只吃 `<a href=x>` 的规则，
    // 否则链接的文字会被留在外面、变成一段没有归属的裸文本。
    cands.sort((a, b) => a[0] - b[0] || b[1] - a[1])
    const [at, len, node] = cands[0]
    pending += rest.slice(0, at)
    rest = rest.slice(at + len)
    push(node)
  }

  flush()
  return out
}

/**
 * 把一段文本里的标签**全部**去掉，只留内容。只给「不能出富文本的地方」用 ——
 * 目前只有 `<details>` 的 `<summary>`（它的标题用纯文本渲染，留着标签就会原样印出来）。
 *
 * ⚠ 别拿它处理段落 / 标题：那会把 `<a>`、`<img>` 一起吃掉，链接和图片就没了。
 * 那些地方走 parseInline —— 它认得出白名单标签，也丢得掉白名单外的。
 */
function stripAllTags(s: string): string {
  return s.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim()
}

// ── 块级解析 ──────────────────────────────────────────────

/**
 * HTML 块级标签降级成 Markdown。
 *
 * 为什么要这一步：相当一部分作者的正文是**整篇 HTML**（实测 Cobblemon 的整合包正文：
 * 54 个 `<p>`、186 个 `<li>`、8 个 `<h4>`、66 个 `<span>`，一行空行都没有）。
 * 不降级的话它不是「渲染差一点」，而是**整篇塌成一个大段落** ——
 * 二级标题、列表、引用全成了同一坨文字，层次信息彻底丢失。
 *
 * ⚠ 只动**块级**标签。`<a>` `<img>` `<strong>` `<em>` `<code>` `<iframe>` 这些行内标签不动，
 * 它们由 parseInline 各自处理（`<a>` 要保链接、`<img>` 要保图）—— 在这里替它们做决定反而会丢信息。
 */
type HtmlBlockRepl = string | ((substring: string, ...args: string[]) => string)

const HTML_BLOCK_RULES: [RegExp, HtmlBlockRepl][] = [
  [/<br\s*\/?>/gi, '\n'],
  // 列表项：转成 `- `。`</li>` 给一个换行，让相邻两项各自成行
  [/<li\b[^>]*>/gi, '\n- '],
  [/<\/li>/gi, '\n'],
  [/<(ul|ol|dl)\b[^>]*>/gi, '\n'],
  [/<\/(ul|ol|dl)>/gi, '\n\n'],
  // 标题：**保留级别**，直接换成 #。作者用它分层，这里压平等于替他扔掉结构。
  [/<h([1-6])\b[^>]*>/gi, (_m, n) => '\n\n' + '#'.repeat(Number(n) || 1) + ' '],
  [/<\/h[1-6]>/gi, '\n\n'],
  [/<blockquote\b[^>]*>/gi, '\n\n> '],
  [/<\/blockquote>/gi, '\n\n'],
  [/<hr\s*\/?>/gi, '\n\n---\n\n'],
  [/<(p|div|tr|section|article|figure)\b[^>]*>/gi, '\n\n'],
  [/<\/(p|div|tr|section|article|figure)>/gi, '\n\n'],
]

function htmlBlocksToMarkdown(src: string): string {
  let out = src
  for (const [re, rep] of HTML_BLOCK_RULES) {
    // 分成两支是 TS 重载的要求：String.replace 把「替换串」和「替换函数」声明成了两个重载，
    // 传一个联合类型两边都不匹配。这不影响运行时，但少了这个分支就编译不过。
    out = typeof rep === 'string' ? out.replace(re, rep) : out.replace(re, rep)
  }
  return out
}

type Block =
  | { k: 'p'; kids: Inline[] }
  | { k: 'h'; level: number; kids: Inline[] }
  | { k: 'ul' | 'ol'; items: Inline[][] }
  | { k: 'quote'; blocks: Block[] }
  | { k: 'code'; lang: string; code: string }
  | { k: 'hr' }
  | { k: 'img'; src: string; alt: string }
  | { k: 'table'; head: Inline[][]; rows: Inline[][] }
  | { k: 'details'; summary: string; blocks: Block[] }

const RE_FENCE = /^```/
const RE_HR = /^(?:-{3,}|\*{3,}|_{3,})$/
const RE_HEAD = /^(#{1,6})\s+(.*)$/
const RE_QUOTE = /^>\s?(.*)$/
const RE_UL = /^(\s*)[-*+]\s+(.*)$/
const RE_OL = /^(\s*)\d+[.)]\s+(.*)$/
const RE_TABLE = /^\s*\|/
/** 一行只有纯容器标签（`<p>`、`</div>` 这种）——它不承担任何内容，跳过即可。 */
const RE_WRAPPER_LINE = /^(?:<\/?(?:p|div|center|section|article|figure)\b[^>]*>|\s*)+$/

function isTableSep(s: string): boolean {
  return /^\s*\|?[\s:-]*-[\s|:-]*\|?\s*$/.test(s) && s.includes('-')
}

function splitRow(s: string): Inline[][] {
  const t = s.trim().replace(/^\|/, '').replace(/\|$/, '')
  return t.split('|').map((c) => parseInline(c.trim()))
}

function parseBlocks(lines: string[]): Block[] {
  const out: Block[] = []
  let i = 0

  while (i < lines.length) {
    const raw = lines[i]
    const line = raw.trim()

    if (line === '') {
      i++
      continue
    }
    if (RE_WRAPPER_LINE.test(line)) {
      i++
      continue
    }

    // 代码块：优先于一切。里面的 `#`、`` ` ``、`|` 都只是字符 ——
    // 一份写着 `-Xmx4G` 的启动参数被当成列表渲染出来，用户照抄就起不来服。
    if (RE_FENCE.test(line)) {
      const lang = line.slice(3).trim()
      i++
      const buf: string[] = []
      while (i < lines.length && !RE_FENCE.test(lines[i].trim())) {
        buf.push(lines[i])
        i++
      }
      i++ // 吃掉收尾的 ```
      out.push({ k: 'code', lang, code: buf.join('\n') })
      continue
    }

    if (RE_HR.test(line)) {
      out.push({ k: 'hr' })
      i++
      continue
    }

    // <details>：折叠块。**真的用 `<details>` 元素**而不是自己做一个展开/收起 ——
    // 作者用它藏的是长清单（模组列表、FAQ），浏览器的原生折叠既省代码也符合预期。
    if (/^<details\b/i.test(line)) {
      const buf: string[] = [line]
      let closed = /<\/details>/i.test(line)
      i++
      while (!closed && i < lines.length) {
        buf.push(lines[i])
        if (/<\/details>/i.test(lines[i])) closed = true
        i++
      }
      const joined = buf.join('\n')
      const sm = joined.match(/<summary\b[^>]*>([\s\S]*?)<\/summary>/i)
      const summary = sm ? stripAllTags(sm[1]) : '展开'
      const inner = joined.replace(/<summary\b[^>]*>[\s\S]*?<\/summary>/i, '')
      out.push({
        k: 'details',
        summary,
        blocks: parseBlocks(inner.replace(/<\/?details\b[^>]*>/gi, '').split('\n')),
      })
      continue
    }

    const head = line.match(RE_HEAD)
    if (head) {
      out.push({ k: 'h', level: head[1].length, kids: parseInline(head[2]) })
      i++
      continue
    }

    if (RE_QUOTE.test(line)) {
      const buf: string[] = []
      while (i < lines.length) {
        const m = lines[i].trim().match(RE_QUOTE)
        if (!m) break
        buf.push(m[1])
        i++
      }
      out.push({ k: 'quote', blocks: parseBlocks(buf) })
      continue
    }

    if (RE_UL.test(raw) || RE_OL.test(raw)) {
      const ordered = RE_OL.test(raw)
      const items: Inline[][] = []
      while (i < lines.length) {
        const m = lines[i].match(ordered ? RE_OL : RE_UL)
        if (!m) break
        items.push(parseInline(m[2]))
        i++
      }
      out.push({ k: ordered ? 'ol' : 'ul', items })
      continue
    }

    // 表格：`| --- |` 分隔行是它的身份证。只凭「以 | 开头」判会把
    // 任意一行管道符当成表格，渲染出一张一列 n 行的怪东西。
    if (RE_TABLE.test(raw) && i + 1 < lines.length && isTableSep(lines[i + 1])) {
      const headRow = splitRow(lines[i])
      i += 2
      const rows: Inline[][] = []
      while (i < lines.length && RE_TABLE.test(lines[i])) {
        rows.push(splitRow(lines[i])[0] ?? [])
        i++
      }
      out.push({ k: 'table', head: [headRow[0] ?? []], rows })
      continue
    }

    // 独立成行的图片（含 `<img>`）：给一个块，不塞进段落里。
    // 塞进去的话图会被行高挤成一细条，作者花时间做的大图只剩一条缝。
    const soloImg = line.match(/^!\[([^\]]*)\]\(\s*([^)\s]+)/) ?? line.match(/^<img\b[^>]*>$/i)
    if (soloImg && line.replace(soloImg[0], '').trim() === '') {
      const src = safeUrl(soloImg[2] ?? attr(soloImg[0], 'src'))
      const alt = soloImg[1] ?? attr(soloImg[0], 'alt')
      if (src) out.push({ k: 'img', src, alt })
      i++
      continue
    }

    // 普通段落：一直吃到空行、或者下一块的开头。
    const buf: string[] = [line]
    i++
    while (i < lines.length) {
      const nx = lines[i]
      const t = nx.trim()
      if (t === '') break
      if (
        RE_FENCE.test(t) ||
        RE_HR.test(t) ||
        RE_HEAD.test(t) ||
        RE_QUOTE.test(t) ||
        RE_UL.test(nx) ||
        RE_OL.test(nx) ||
        RE_WRAPPER_LINE.test(t) ||
        /^<details\b/i.test(t)
      ) {
        break
      }
      buf.push(t)
      i++
    }
    out.push({ k: 'p', kids: parseInline(buf.join(' ')) })
  }

  return out
}

// ── 渲染 ──────────────────────────────────────────────────

export function renderInline(nodes: Inline[]): ReactNode {
  return nodes.map((n, idx) => {
    switch (n.k) {
      case 'text':
        return <Fragment key={idx}>{n.v}</Fragment>
      case 'strong':
        return (
          <strong key={idx} className="font-semibold text-ink-1">
            {renderInline(n.kids)}
          </strong>
        )
      case 'em':
        return (
          <em key={idx} className="text-ink-2 italic">
            {renderInline(n.kids)}
          </em>
        )
      case 'code':
        return (
          <code key={idx} className="md-code">
            {n.v}
          </code>
        )
      case 'link':
        return (
          <a key={idx} className="md-link" href={n.href} target="_blank" rel="noreferrer noopener">
            {renderInline(n.kids)}
          </a>
        )
      case 'img':
        return (
          // loading=lazy：正文里几十张图同时拉会把详情首屏拖成白屏。
          <img key={idx} className="md-img" src={n.src} alt={n.alt} loading="lazy" />
        )
      case 'br':
        return <br key={idx} />
      default:
        return null
    }
  })
}

function renderBlocks(blocks: Block[]): ReactNode {
  return blocks.map((b, idx) => {
    switch (b.k) {
      case 'p':
        // 只剩白空东西（比如一整行是白名单外标签）就不出一个空段落 ——
        // 一堆空段落会在简介下方堆出一截空白，看着像页面坏了。
        if (b.kids.every((k) => k.k === 'text' && !k.v.trim())) return null
        return (
          <p key={idx} className="md-p">
            {renderInline(b.kids)}
          </p>
        )
      case 'h': {
        // 作者爱用 6 级标题当小字。全部按 1:1 渲染的话，一个 `#` 就顶到页面标题那么大，
        // 作者原本的层次反而被压平了。所以整体往下压：# → h3，## → h4，更深的都按最低。
        const kids = renderInline(b.kids)
        const cls = 'md-h'
        if (b.level <= 1) return <h3 key={idx} className={cls + ' md-h3'}>{kids}</h3>
        if (b.level === 2) return <h4 key={idx} className={cls + ' md-h4'}>{kids}</h4>
        return <h5 key={idx} className={cls + ' md-h5'}>{kids}</h5>
      }
      case 'ul':
        return (
          <ul key={idx} className="md-list">
            {b.items.map((it, j) => (
              <li key={j}>{renderInline(it)}</li>
            ))}
          </ul>
        )
      case 'ol':
        return (
          <ol key={idx} className="md-list md-ol">
            {b.items.map((it, j) => (
              <li key={j}>{renderInline(it)}</li>
            ))}
          </ol>
        )
      case 'quote':
        return (
          <blockquote key={idx} className="md-quote">
            {renderBlocks(b.blocks)}
          </blockquote>
        )
      case 'code':
        return (
          <pre key={idx} className="md-pre">
            <code>{b.code}</code>
          </pre>
        )
      case 'hr':
        return <hr key={idx} className="md-hr" />
      case 'img':
        return <img key={idx} className="md-img" src={b.src} alt={b.alt} loading="lazy" />
      case 'table':
        return (
          <div key={idx} className="md-table-wrap">
            <table className="md-table">
              <thead>
                {(b.head ?? []).map((row, r) => (
                  <tr key={r}>
                    {row.map((c, c2) => (
                      <th key={c2}>{renderInline([c])}</th>
                    ))}
                  </tr>
                ))}
              </thead>
              <tbody>
                {b.rows.map((row, r) => (
                  <tr key={r}>
                    {row.map((c, c2) => (
                      <td key={c2}>{renderInline([c])}</td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )
      case 'details':
        return (
          <details key={idx} className="md-details">
            <summary>{b.summary}</summary>
            <div className="md-details-body">{renderBlocks(b.blocks)}</div>
          </details>
        )
      default:
        return null
    }
  })
}

/**
 * 把一段市场正文画成 React 元素。
 *
 * @param src Markdown / 白名单 HTML 混合的正文（可以为空，返回 null）
 */
export function renderMarkdown(src: string): ReactNode {
  if (!src || !src.trim()) return null
  // 顺序要紧：先把 HTML 的块级结构降级成 Markdown（否则整篇 HTML 会塌成一个大段落），
  // 再按行切块。反过来做的话，降级产生的换行不会参与分块。
  const md = htmlBlocksToMarkdown(src.replace(/\r\n?/g, '\n'))
  return <div className="md-body">{renderBlocks(parseBlocks(md.split('\n')))}</div>
}
