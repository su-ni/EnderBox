import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from 'react'
import {
  api,
  ConfigRejected,
  type ConfigContent,
  type ConfigExcluded,
  type ConfigFile,
  type ConfigIssue,
} from '../api'
import { Icon } from './design'
import { Empty, NotReady, SkeletonFields, fmtSizeMaybe, fmtTime } from './ui'
import type { Instance } from '../types'

type Props = {
  instance: Instance
  onToast: (msg: string) => void
}

const PARAMS = new URLSearchParams(location.search)
/** 行高必须和 CSS 里的 --cfg-line 一致，跳行是拿它算偏移的 */
const LINE_H = 21

const LANG_LABEL: Record<string, string> = {
  yaml: 'YAML',
  toml: 'TOML',
  json: 'JSON',
  properties: 'properties',
  text: '纯文本',
}

// ── 语法高亮 ─────────────────────────────────────────
//
// 自己写一个而不是引 CodeMirror：整站的排版、配色、零圆角都是自己搭的，
// 塞进来一整套外来主题比省下的代码更贵。这里只需要「一眼分清键、值、注释」，
// 正则就能覆盖，而且渲染出来的是 React 节点 —— 天然没有 XSS。

/** 拆出注释部分。规则按语言来：YAML/TOML 要求 # 前面是空白才算注释，
 *  properties 只在行首算（Java 的规矩）。这样 `url: http://x#y` 不会被拦腰截断。 */
function splitComment(s: string, lang: string): [string, string] {
  if (lang === 'json' || lang === 'text') return [s, '']
  let quote: string | null = null
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (quote) {
      if (ch === '\\') {
        i++
        continue
      }
      if (ch === quote) quote = null
      continue
    }
    if (ch === '"' || ch === "'") {
      quote = ch
      continue
    }
    const isProp = lang === 'properties'
    const hit = ch === '#' || (isProp && ch === '!')
    if (!hit) continue
    if (isProp) return i === 0 ? ['', s] : [s, '']
    if (i === 0 || /\s/.test(s[i - 1])) return [s.slice(0, i), s.slice(i)]
  }
  return [s, '']
}

const TOKEN_RE = /"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|-?\d+(?:\.\d+)?|\b(?:true|false|null|yes|no|on|off)\b/gi

/** 值部分再细分：字符串 / 数字 / 布尔。逐字符覆盖，保证原样输出不错位。 */
function tokenizeValue(s: string, key: string): ReactNode[] {
  const out: ReactNode[] = []
  let i = 0
  let n = 0
  TOKEN_RE.lastIndex = 0
  for (let m = TOKEN_RE.exec(s); m; m = TOKEN_RE.exec(s)) {
    if (m.index > i) out.push(s.slice(i, m.index))
    const tok = m[0]
    const low = tok.toLowerCase()
    const cls =
      tok[0] === '"' || tok[0] === "'"
        ? 'tk-str'
        : /^-?\d/.test(tok)
          ? 'tk-num'
          : low === 'true' || low === 'false' || low === 'yes' || low === 'no' || low === 'on' || low === 'off'
            ? 'tk-bool'
            : 'tk-nil'
    out.push(
      <span key={`${key}-t${n++}`} className={cls}>
        {tok}
      </span>,
    )
    i = m.index + tok.length
  }
  if (i < s.length) out.push(s.slice(i))
  return out
}

/** 高亮一整行。返回的节点必须严格拼回原文 —— 少一个字符就会整列错位。 */
function highlightLine(line: string, lang: string): ReactNode {
  if (lang === 'text') return line || '\u00a0'
  const [code, comment] = splitComment(line, lang)
  const parts: ReactNode[] = []
  let rest = code

  if (lang === 'toml' && /^\s*\[.+\]\s*$/.test(code)) {
    // [section] 整行当标题色
    parts.push(
      <span key="sec" className="tk-sec">
        {code}
      </span>,
    )
    rest = ''
  } else {
    // YAML 用 `:`，TOML/properties 用 `=` 分隔键值
    const sep = lang === 'yaml' ? /^(\s*-?\s*)([^:#]*?)(\s*:\s*)/ : /^(\s*)([^=]*?)(\s*=\s*)/
    const m = rest.match(sep)
    if (m && m[2].trim()) {
      parts.push(m[1])
      parts.push(
        <span key="k" className="tk-key">
          {m[2]}
        </span>,
      )
      parts.push(<span key="s" className="tk-punct">{m[3]}</span>)
      rest = rest.slice(m[0].length)
    } else if (lang === 'json' || lang === 'json5') {
      const jm = rest.match(/^(\s*)("(?:\\.|[^"\\])*")(\s*:\s*)/)
      if (jm) {
        parts.push(jm[1])
        parts.push(
          <span key="k" className="tk-key">
            {jm[2]}
          </span>,
        )
        parts.push(<span key="s" className="tk-punct">{jm[3]}</span>)
        rest = rest.slice(jm[0].length)
      }
    }
  }

  parts.push(...tokenizeValue(rest, 'v'))
  if (comment) {
    parts.push(
      <span key="c" className="tk-cmt">
        {comment}
      </span>,
    )
  }
  return parts.length ? parts : '\u00a0'
}

// ── 组件 ─────────────────────────────────────────────

export function ConfigEditor({ instance, onToast }: Props) {
  const [files, setFiles] = useState<ConfigFile[] | null>(null)
  const [excluded, setExcluded] = useState<ConfigExcluded[]>([])
  const [showExcluded, setShowExcluded] = useState(false)
  const [path, setPath] = useState(() => PARAMS.get('cf') ?? '')
  // 深链只认第一次：?cf= 带的路径在首屏消费掉就作废。
  // 不这么做的话，切到别的实例时会拿旧路径去开新实例的文件，报一串莫名其妙的错。
  // （用 ref 而不是 state：消费这个动作本身不该触发渲染。）
  const pendingRef = useRef<string | null>(PARAMS.get('cf') || null)
  // 打开文件后要落到的行（0 = 无报错，回到顶部）。
  // 同样用 ref：它只是给「cur 变了之后」那个 effect 的一次性指令，不该触发渲染。
  const pendingJumpRef = useRef(0)
  const [cur, setCur] = useState<ConfigContent | null>(null)
  const [draft, setDraft] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [issues, setIssues] = useState<ConfigIssue[]>([])
  const [banner, setBanner] = useState<{ kind: 'warn' | 'err' | 'ok'; text: string } | null>(null)
  const [line, setLine] = useState(1)
  const [allowBroken, setAllowBroken] = useState(false)

  const taRef = useRef<HTMLTextAreaElement>(null)
  const hlRef = useRef<HTMLPreElement>(null)
  const gutRef = useRef<HTMLDivElement>(null)

  const loadList = useCallback(async () => {
    try {
      const d = await api.configs(instance.id)
      setFiles(d.files ?? [])
      setExcluded(d.excluded ?? [])
    } catch (e) {
      onToast((e as Error).message)
      setFiles([])
    }
  }, [instance.id, onToast])

  useEffect(() => {
    setFiles(null)
    setCur(null)
    setBanner(null)
    // ⚠ 这里**不能**无脑 setPath('')：首屏 ?cf= 深链的值就是被这一句抹掉的，
    // 症状是「链接切到了配置分组，但文件不自动打开」。没有待消费的深链才清空。
    setPath(pendingRef.current ?? '')
    if (pendingRef.current === null) {
      // 换实例后 URL 里残留的 cf= 已失效，留着会让刷新时去开一个不存在的文件
      const u = new URL(location.href)
      if (u.searchParams.has('cf')) {
        u.searchParams.delete('cf')
        history.replaceState(null, '', u)
      }
    }
    void loadList()
  }, [loadList])

  const open = useCallback(
    async (p: string) => {
      if (!p) {
        setPath('')
        setCur(null)
        setBanner(null)
        return
      }
      setLoading(true)
      setBanner(null)
      setIssues([])
      setAllowBroken(false)
      try {
        const c = await api.configFile(instance.id, p)
        setCur(c)
        setDraft(c.text)
        setIssues(c.issues ?? [])
        setPath(p)
        const u = new URL(location.href)
        u.searchParams.set('page', 'config')
        u.searchParams.set('cf', p)
        history.replaceState(null, '', u)
        // 别在这里直接设 scrollTop —— 此刻 textarea 里还是**上一份**文件的内容，
        // 浏览器会按旧内容把 scrollTop 夹回去（表现成「滚了但滚不到位」）。
        // 记下目标行，等 draft 真的换过来之后在 effect 里滚。
        pendingJumpRef.current = (c.issues ?? []).find((it) => it.line > 0)?.line ?? 0
      } catch (e) {
        onToast((e as Error).message)
      } finally {
        setLoading(false)
      }
    },
    [instance.id, onToast],
  )

  // 深链：?cf= 带了个文件就直接打开。只在文件列表回来时消费一次 ——
  // 不消费的话切实例/切分组会反复用旧路径打开。
  useEffect(() => {
    if (!files) return
    const want = pendingRef.current
    if (!want) return
    pendingRef.current = null
    void open(want)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [files])

  const dirty = cur !== null && draft !== cur.text
  const canEdit = cur !== null && cur.textual && !cur.problem

  const errorLines = useMemo(() => {
    const s = new Set<number>()
    for (const it of issues) if (it.line > 0) s.add(it.line)
    return s
  }, [issues])

  const lineCount = useMemo(() => draft.split('\n').length, [draft])

  // 高亮层和 textarea 必须逐像素重合：同字体、同行高、同内边距，滚动一起走。
  // 差一个像素，光标位置和看到的字就错开了，那种编辑器没法用。
  const syncScroll = () => {
    const ta = taRef.current
    if (!ta) return
    if (hlRef.current) {
      hlRef.current.scrollTop = ta.scrollTop
      hlRef.current.scrollLeft = ta.scrollLeft
    }
    if (gutRef.current) gutRef.current.scrollTop = ta.scrollTop
  }

  const jumpTo = (n: number) => {
    const ta = taRef.current
    if (!ta || n < 1) return
    ta.scrollTop = Math.max(0, (n - 1) * LINE_H - 60)
    ta.setSelectionRange(0, 0)
    ta.blur()
    syncScroll()
    setLine(n)
  }

  // 打开文件后把视图落到「第一个报错行」上；没有报错就回顶部。
  // 依赖 cur 而不是 draft：换文件时 cur 才变，用户之后打字不该再被拽走一次。
  useEffect(() => {
    const n = pendingJumpRef.current
    pendingJumpRef.current = 0
    const ta = taRef.current
    if (!ta) return
    ta.scrollTop = n > 0 ? Math.max(0, (n - 1) * LINE_H - 60) : 0
    syncScroll()
    if (n > 0) setLine(n)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cur])

  const switchTo = (p: string) => {
    if (dirty && !confirm(`「${cur?.name}」有没保存的改动，换文件就丢了。确定要换吗？`)) return
    void open(p)
  }

  const save = async (validate = true) => {
    if (!cur) return
    setSaving(true)
    setBanner(null)
    try {
      await api.saveConfigFile(instance.id, cur.path, draft, cur.eol, cur.modTime, validate)
      onToast(`已保存 ${cur.name}`)
      setAllowBroken(false)
      await open(cur.path)
    } catch (e) {
      if (e instanceof ConfigRejected) {
        setIssues(e.issues)
        setBanner({ kind: 'err', text: e.message })
      } else {
        setBanner({ kind: 'err', text: (e as Error).message })
      }
    } finally {
      setSaving(false)
    }
  }

  const restore = async () => {
    if (!cur) return
    if (!confirm(`用上一次保存前的版本覆盖「${cur.name}」？现在的改动会丢。`)) return
    try {
      await api.restoreConfigFile(instance.id, cur.path)
      onToast('已还原到上一版')
      await open(cur.path)
    } catch (e) {
      setBanner({ kind: 'err', text: (e as Error).message })
    }
  }

  // Tab 缩进，不要跳焦点 —— 在配置文件里按 Tab 十次有十次是想缩进
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Tab') {
      e.preventDefault()
      const ta = e.currentTarget
      const s = ta.selectionStart
      const t = ta.selectionEnd
      const next = draft.slice(0, s) + '  ' + draft.slice(t)
      setDraft(next)
      requestAnimationFrame(() => ta.setSelectionRange(s + 2, s + 2))
      return
    }
    // Ctrl+S 保存：手改配置的人肌肉记忆里都有这个
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault()
      if (dirty && !saving) void save(true)
    }
  }

  if (files === null) return <SkeletonFields rows={4} />

  return (
    <div className="min-w-0">
      <p className="text-[13px] leading-relaxed text-ink-2">
        bukkit.yml / spigot.yml / paper-global.yml、每个插件自己的 config.yml、模组的 config/*.toml
        —— 这些没有统一 schema，做成表单是妄想，所以给一个能看见能改的编辑器。
        保存前会检查结构，<b className="text-ink-1">语法错了不写盘</b>；写之前还会留一份上一版。
      </p>

      {/* 文件列表：不做成左侧第二列 —— 配置页左边已经有一列分组了，
          再嵌一列会挤成夹心。横着摊在上面反而更好扫。 */}
      <div className="cfg-list mt-4">
        {files.length === 0 ? (
          <Empty>
            这个实例目录下没找到可编辑的配置文件。装了插件之后 plugins/&lt;插件名&gt;/config.yml
            会出现在这里。
          </Empty>
        ) : (
          files.map((f) => (
            <button
              key={f.path}
              type="button"
              className={`cfg-fitem${f.path === path ? ' on' : ''}`}
              onClick={() => switchTo(f.path)}
              title={f.path}
            >
              <Icon name="file" size={13} />
              <span className="cfg-fname">{f.path}</span>
              <span className="cfg-fmeta">
                {LANG_LABEL[f.lang] ?? f.lang} · {fmtSizeMaybe(f.size)}
              </span>
            </button>
          ))
        )}
      </div>

      {excluded.length > 0 && (
        <div className="mt-3">
          <button
            type="button"
            className="btn btn-ghost sm"
            onClick={() => setShowExcluded((v) => !v)}
            aria-expanded={showExcluded}
          >
            {showExcluded ? '收起' : `为什么有 ${excluded.length} 个文件不在这里`}
          </button>
          {showExcluded && (
            <ul className="nbox" style={{ marginTop: 10 }}>
              {excluded.map((e) => (
                <li key={e.path} className="py-1.5 text-[12.5px] leading-relaxed text-ink-3">
                  <span className="mono-value text-ink-2">{e.path}</span> —— {e.reason}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      {!path ? (
        <div className="mt-5">
          <NotReady>从上面挑一个文件开始改。</NotReady>
        </div>
      ) : loading && !cur ? (
        <SkeletonFields rows={6} />
      ) : cur?.problem ? (
        <div className="nbox w" style={{ marginTop: 20 }}>
          {cur.problem}
        </div>
      ) : cur ? (
        <>
          <div className="cfg-meta mt-5">
            <span className="mono-value">{cur.path}</span>
            <span className="cfg-dot" />
            <span>{LANG_LABEL[cur.lang] ?? cur.lang}</span>
            <span className="cfg-dot" />
            <span>{fmtSizeMaybe(cur.size)}</span>
            <span className="cfg-dot" />
            <span>改于 {fmtTime(cur.modTime)}</span>
            <span className="cfg-dot" />
            <span>{cur.eol === 'crlf' ? 'CRLF 行尾' : 'LF 行尾'}</span>
            {dirty && (
              <>
                <span className="cfg-dot" />
                <span className="text-warn">有未保存的改动</span>
              </>
            )}
          </div>

          {banner && (
            <div className={`nbox ${banner.kind === 'err' ? 'd' : 'w'}`} style={{ marginTop: 12 }}>
              {banner.text}
              {banner.kind === 'err' && issues.length > 0 && (
                <label style={{ marginTop: 10, display: 'flex', alignItems: 'center', gap: 8 }}>
                  <input
                    type="checkbox"
                    checked={allowBroken}
                    onChange={(e) => setAllowBroken(e.target.checked)}
                  />
                  我知道有问题，仍然保存（比如这份文件由别的工具读，语法本来就特殊）
                </label>
              )}
            </div>
          )}

          {issues.length > 0 && (
            <ul className="cfg-issues mt-3">
              {issues.map((it, i) => (
                <li key={i}>
                  <button
                    type="button"
                    className="cfg-issue"
                    onClick={() => it.line > 0 && jumpTo(it.line)}
                    disabled={it.line < 1}
                  >
                    <Icon name="ban" size={12} />
                    <span className="cfg-issue-loc">
                      {it.line > 0 ? `第 ${it.line} 行${it.approx ? '附近' : ''}` : '整份文件'}
                    </span>
                    <span className="cfg-issue-msg">{it.message}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}

          <div className="cfg-edit mt-3">
            <div className="cfg-gutter" ref={gutRef} aria-hidden="true">
              {Array.from({ length: lineCount }, (_, i) => (
                <div key={i} className={`cfg-ln${errorLines.has(i + 1) ? ' bad' : ''}`}>
                  {i + 1}
                </div>
              ))}
            </div>
            <div className="cfg-code">
              <pre className="cfg-hl" ref={hlRef} aria-hidden="true">
                {draft.split('\n').map((l, i) => (
                  <div key={i} className={`cfg-line${errorLines.has(i + 1) ? ' bad' : ''}`}>
                    {highlightLine(l, cur.lang)}
                  </div>
                ))}
              </pre>
              <textarea
                ref={taRef}
                className="cfg-ta"
                value={draft}
                spellCheck={false}
                autoCapitalize="off"
                autoCorrect="off"
                aria-label={`${cur.name} 内容`}
                disabled={!canEdit}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={onKeyDown}
                onScroll={syncScroll}
                onSelect={(e) =>
                  setLine(draft.slice(0, e.currentTarget.selectionStart).split('\n').length)
                }
              />
            </div>
          </div>

          <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2">
            <span className="mono-meta">
              第 {line} 行 · 共 {lineCount} 行
            </span>
            <span className="flex-1" />
            {cur.hasBackup && (
              <button type="button" className="btn btn-ghost sm" onClick={restore}>
                <Icon name="refresh" size={13} /> 还原上一版
              </button>
            )}
            <button
              type="button"
              className="btn btn-ghost sm"
              disabled={!dirty || saving}
              onClick={() => {
                setDraft(cur.text)
                setIssues(cur.issues ?? [])
                setBanner(null)
              }}
            >
              放弃改动
            </button>
            <button
              type="button"
              className="btn btn-primary sm"
              disabled={!dirty || saving || !canEdit}
              onClick={() => void save(!allowBroken)}
            >
              {saving ? (
                '保存中…'
              ) : (
                <>
                  <Icon name="check" size={13} /> 保存
                </>
              )}
            </button>
          </div>

          <p className="mt-3 text-[12px] leading-relaxed text-ink-3">
            <Icon name="cmd" size={11} /> Ctrl+S 保存、Tab 缩进。服务端正在跑的时候改配置基本都要重启才读到
            —— 这一页只负责把文件写对，不假装能热加载。
          </p>
          {cur.hasBackup && (
            <p className="mt-1.5 text-[12px] leading-relaxed text-ink-3">
              上一次保存前的版本存在
              <span className="mono-value"> {cur.backup}</span>
              （放在面板数据目录里，没有丢进实例目录 —— 免得被服务端或插件当配置文件扫到）。
            </p>
          )}
          {!dirty && issues.length === 0 && (
            <p className="mt-1.5 flex items-center gap-1.5 text-[12px] text-ink-3">
              <Icon name="check" size={12} /> 没发现结构问题
            </p>
          )}
        </>
      ) : null}
    </div>
  )
}
