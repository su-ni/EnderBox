/**
 * 游戏规则页（gamerule）—— 左侧分组导航 + 右侧「只显示这一组」。
 *
 * 页面骨架照配置页那套（.cfg-wrap / .cfg-nav / .cfg-pane / .field）：
 * 一行一条规则 = 中文名 + 机器名（等宽，方便拿去查 wiki / 敲命令）+ 说明，
 * 右侧是控件（开关 / 数字步进）。
 *
 * ▍三条产品决定，写在这里免得后来人再猜：
 * ① 这页只服务「运行中的服务器」。规则值在服务端内存里，停服时读不到 ——
 *    停服不是「空」，页面必须说清楚，不给一张空表（不静默失败）。
 * ② 开关点一下立刻发命令（一次点击 = 一个原子改动）；数字改完要按「写入」——
 *    不然打字中间态（1 → 10 → 100）会一条条发出去，控制台里刷一串垃圾命令，
 *    中间值还可能短暂难看到（比如 randomTickSpeed 突然 100）。
 * ③ 值一律以服务端回报为准：个别规则会被服务端夹进范围（比如百分比类），
 *    页面上显示的必须是实际生效值；夹过的时候弹一句说明。
 */
import { useCallback, useEffect, useMemo, useState, type CSSProperties } from 'react'
import { api, type GameruleList, type GameruleRow } from '../api'
import { Icon, Tog } from './design'
import { StepInput } from './StepInput'
import type { Instance } from '../types'

const rd = (v: string) => ({ '--rd': v }) as CSSProperties

/** 分组图标。后端只给分组名，图标在这层查表 —— 查不到用中性的文件图标，不硬猜。 */
const GROUP_ICON: Record<string, string> = {
  玩家: 'head',
  伤害: 'heart',
  掉落: 'chest',
  生物: 'users',
  世界: 'grass',
  聊天与指令: 'cmd',
  杂项: 'file',
}
const groupIcon = (g: string) => GROUP_ICON[g] ?? 'file'

type Props = {
  instance: Instance
  running: boolean
  onToast: (msg: string) => void
}

/** 页面的四种状态。'stopped' / 'notReady' 都不是错误 —— 是「现在读不到」，说法不同。 */
type Phase = 'loading' | 'ready' | 'stopped' | 'notReady' | 'error'

export function RulesPanel({ instance, running, onToast }: Props) {
  const [data, setData] = useState<GameruleList | null>(null)
  const [phase, setPhase] = useState<Phase>('loading')
  const [err, setErr] = useState('')
  // 分组做成可寻址的（?page=rules&g=玩家）：刷新、发给别人、截图脚本都能停在指定组。
  const [gkey, setGkey] = useState(() => new URLSearchParams(location.search).get('g') ?? '')
  const [query, setQuery] = useState('')
  // 正在写的规则名。每行只锁自己 —— 写 A 的时候 B 还能改。
  const [busy, setBusy] = useState('')
  // 数字的本地草稿（name → 输入框里的值）。只在不等于服务端值时才算「有改动」。
  const [draft, setDraft] = useState<Record<string, string>>({})

  const load = useCallback(async () => {
    setPhase('loading')
    setErr('')
    try {
      const d = await api.gamerules(instance.id)
      setData(d)
      setDraft({})
      setPhase('ready')
    } catch (e) {
      const er = e as Error & { status?: number; reason?: string }
      // 两种「读不到」分开说：停服要去启动；没就绪等一会儿再点重新读取。
      // 按 reason 码分流（后端给的），不匹配文案。
      if (er.reason === 'no-server') {
        setPhase('stopped')
        setErr(er.message)
      } else if (er.reason === 'not-ready') {
        setPhase('notReady')
        setErr(er.message)
      } else {
        setPhase('error')
        setErr(er.message)
      }
    }
  }, [instance.id])

  // 实例一换、运行状态一变（启/停）就重取 —— 启动完成要自动把表读出来，
  // 停服要立刻切成「读不到」的说明，别让用户对着过期值继续操作。
  useEffect(() => {
    void load()
  }, [load, running])

  const groups = data?.groups ?? []
  // URL 里给的组优先；名字对不上（换了个实例、手改过 URL）就落第一组。
  const cur = groups.includes(gkey) ? gkey : (groups[0] ?? '')

  useEffect(() => {
    const u = new URL(location.href)
    if (u.searchParams.get('g') === cur) return
    if (cur) u.searchParams.set('g', cur)
    else u.searchParams.delete('g')
    history.replaceState(null, '', u)
  }, [cur])

  const q = query.trim().toLowerCase()
  const searching = q !== ''
  const all = data?.rules ?? []
  const matched = useMemo(
    () =>
      searching
        ? all.filter((r) => `${r.label} ${r.name} ${r.note ?? ''}`.toLowerCase().includes(q))
        : all,
    [all, q, searching],
  )
  const rows = searching ? matched : matched.filter((r) => r.group === cur)

  /** 一行里当前该显示的值：本地草稿优先（正在打字），否则服务端确认过的值。 */
  const valOf = (r: GameruleRow) => draft[r.name] ?? r.value
  const dirtyOf = (r: GameruleRow) => (draft[r.name] ?? r.value) !== r.value

  /**
   * 写一条规则。三件事必须做全：
   *   ① 成功后把本地草稿清掉、用**服务端回报的值**覆盖显示（可能被夹过）；
   *   ② 被夹过要弹话说清楚（用户填 1000 得到 100，不说就是欺负人）；
   *   ③ 失败要还原草稿并报错；「服务器没了」这一类顺手切页面状态。
   */
  const write = async (r: GameruleRow, value: string) => {
    setBusy(r.name)
    try {
      const res = await api.setGamerule(instance.id, r.name, value)
      setData((d) =>
        d ? { ...d, rules: d.rules.map((x) => (x.name === r.name ? { ...x, value: res.value } : x)) } : d,
      )
      setDraft((l) => {
        const n = { ...l }
        delete n[r.name]
        return n
      })
      if (res.value !== value) {
        onToast(`${r.label}：服务端把它定在 ${res.value}（你填的是 ${value}）`)
      } else {
        const show = res.value === 'true' ? '开' : res.value === 'false' ? '关' : res.value
        onToast(`${r.label} 已设为：${show}`)
      }
    } catch (e) {
      const er = e as Error & { reason?: string }
      setDraft((l) => {
        const n = { ...l }
        delete n[r.name]
        return n
      })
      onToast(`改「${r.label}」失败：${er.message}`)
      // 服务器中途停了 / 没就绪：页面别继续装作能改，重取一次让状态跟上
      if (er.reason === 'no-server' || er.reason === 'not-ready') void load()
    } finally {
      setBusy('')
    }
  }

  const curRows = searching ? matched.length : rows.length

  return (
    <div className="min-w-0">
      <div className="sec-head reveal">
        <span className="idx">04</span>
        <h2>游戏规则</h2>
        <span className="sub">
          在调 <b style={{ color: 'var(--ink-1)' }}>{instance.name}</b>。
          规则值在服务端内存里 —— 服务器在线时改完立刻生效，停服时读不到
        </span>
        <div className="spacer"></div>
        <button
          type="button"
          className="btn sm"
          disabled={phase === 'loading'}
          onClick={() => void load()}
        >
          <Icon name="refresh" />
          {phase === 'loading' ? '读取中…' : '重新读取'}
        </button>
      </div>

      {phase === 'error' && (
        <div className="banner-danger" style={{ marginBottom: 12 }}>
          <Icon name="ban" size={18} style={{ color: 'var(--bad)' }} />
          <div className="t">
            <h3>规则读不出来</h3>
            <p>{err}</p>
          </div>
          <button type="button" className="btn sm" onClick={() => void load()}>
            重试
          </button>
        </div>
      )}

      {(phase === 'loading' || phase === 'stopped' || phase === 'notReady') && (
        <div className="panel reveal">
          <i className="corner" />
          {phase === 'loading' ? (
            <div className="statecard">
              <div className="sk" style={{ width: '46%' }} />
              <div className="sk" style={{ width: '72%', marginTop: 12 }} />
              <div className="sk" style={{ width: '58%', marginTop: 12 }} />
            </div>
          ) : phase === 'stopped' ? (
            <div className="statecard">
              <Icon name="stop" size={44} className="empty-ic" style={{ color: 'var(--ink-3)' }} />
              <h3>服务器没在跑，规则读不到</h3>
              <p>
                {err}
                。游戏规则是服务端运行时的开关，值只存在它自己的内存里 —— 不是「没有规则」，
                是现在没法读。点上面的「启动」，服务器起来之后回到这页就会自动读出来。
              </p>
            </div>
          ) : (
            <div className="statecard">
              <Icon name="time" size={44} className="empty-ic" style={{ color: 'var(--warn)' }} />
              <h3>命令发出去还没回应</h3>
              <p>
                {err}
                服务器刚启动时要花点时间加载 —— 等它打出「Done」就绪行，再点「重新读取」。
              </p>
              <button type="button" className="btn sm" style={{ marginTop: 14 }} onClick={() => void load()}>
                <Icon name="refresh" />
                重新读取
              </button>
            </div>
          )}
        </div>
      )}

      {phase === 'ready' && all.length === 0 && (
        <div className="panel reveal">
          <i className="corner" />
          <div className="statecard">
            <Icon name="info" size={44} className="empty-ic" style={{ color: 'var(--ink-3)' }} />
            <h3>一条规则都没读到</h3>
            <p>命令发出去了，但服务端一条都没回。等它完全就绪后再点「重新读取」。</p>
          </div>
        </div>
      )}

      {phase === 'ready' && all.length > 0 && (
        <>
          {searching && matched.length === 0 ? (
            <div className="panel reveal">
              <i className="corner" />
              <div className="statecard">
                <h3>没有匹配的规则</h3>
                <p>
                  没有匹配「<span className="mono">{query.trim()}</span>」的规则。换个词试试，
                  或者清空搜索框回到一组一屏。
                </p>
              </div>
            </div>
          ) : (
            <div className="cfg-wrap">
              {/* 左：分组导航（设计稿的 .cfg-nav）。数字是这一组有几条 */}
              <div className="panel reveal" style={rd('.1s')}>
                <i className="corner" />
                <nav className="cfg-nav" aria-label="规则分组">
                  {groups.map((g) => {
                    const on = !searching && g === cur
                    const n = searching
                      ? matched.filter((r) => r.group === g).length
                      : all.filter((r) => r.group === g).length
                    return (
                      <button
                        key={g}
                        type="button"
                        className={on ? 'on' : ''}
                        aria-current={on}
                        onClick={() => {
                          setGkey(g)
                          // 点分组 = 退出搜索：不然点了「世界」还满屏玩家组的搜索结果，说不通
                          setQuery('')
                        }}
                      >
                        <Icon name={groupIcon(g)} />
                        <span style={{ minWidth: 0 }}>{g}</span>
                        <span className="n">{n}</span>
                      </button>
                    )
                  })}
                </nav>
              </div>

              {/* 右：这一组的规则 */}
              <div className="panel reveal" style={rd('.14s')}>
                <i className="corner" />
                <div className="con-head tex-stone">
                  <Icon name={searching ? 'search' : groupIcon(cur)} style={{ color: 'var(--cyan)' }} />
                  <h2>{searching ? '搜索结果' : cur}</h2>
                  <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
                    {searching
                      ? `${matched.length} 条规则命中`
                      : `${curRows} 条规则 · 服务端在线时改完立刻生效`}
                  </span>
                  <div className="spacer"></div>
                  <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
                    共 {all.length} 条
                  </span>
                </div>

                {/* 搜索框：规则一多（45+ 条），按名字找人是最常见的用法 */}
                <div className="toolbar">
                  <Icon name="search" style={{ color: 'var(--ink-3)' }} />
                  <input
                    className="inp"
                    style={{ flex: 1, maxWidth: 380 }}
                    placeholder="搜规则名 / 中文名，例如「死亡」或 keepInventory"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                  <div className="spacer" style={{ marginLeft: 'auto' }}></div>
                  <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-3)' }}>
                    {searching ? `${matched.length} / ${all.length}` : `${all.length} 条`}
                  </span>
                </div>

                <div className="cfg-pane on">
                  <div style={{ padding: '6px 16px 16px' }}>
                    {rows.map((r) => (
                      <div className="field" key={r.name}>
                        <div>
                          <div className="lb">
                            {r.label}
                            <span
                              style={{
                                marginLeft: 8,
                                fontFamily: 'var(--f-term)',
                                fontSize: 14,
                                fontWeight: 400,
                                color: 'var(--ink-3)',
                              }}
                              title="服务端认的规则名 —— 可以在控制台直接用"
                            >
                              {r.name}
                            </span>
                          </div>
                          {r.note && <div className="hint">{r.note}</div>}
                        </div>
                        <div className="ctl">
                          {r.kind === 'bool' ? (
                            <Tog
                              on={valOf(r) === 'true'}
                              disabled={busy === r.name}
                              ariaLabel={r.label}
                              onChange={() => void write(r, valOf(r) === 'true' ? 'false' : 'true')}
                            />
                          ) : (
                            <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                              <StepInput
                                value={valOf(r)}
                                label={r.label}
                                onChange={(v) => setDraft((l) => ({ ...l, [r.name]: v }))}
                              />
                              {dirtyOf(r) && (
                                <button
                                  type="button"
                                  className="btn btn-primary sm"
                                  disabled={busy === r.name}
                                  onClick={() => void write(r, valOf(r))}
                                >
                                  <Icon name="check" />
                                  {busy === r.name ? '写入中…' : '写入'}
                                </button>
                              )}
                            </div>
                          )}
                        </div>
                      </div>
                    ))}
                  </div>
                </div>

                {/* 面板页脚：把交互模型说清楚（照设置页那条 .unsaved 的用法） */}
                <div className="unsaved">
                  <span className="dotw" />
                  <span style={{ fontSize: 12.5, color: 'var(--ink-1)' }}>
                    开关点一下立刻生效；数字改完按行内的
                    <b style={{ fontFamily: 'var(--f-pix)', color: 'var(--copper)' }}>写入</b>
                    —— 打字中间态不会一条条发出去
                  </span>
                </div>
              </div>
            </div>
          )}
        </>
      )}
    </div>
  )
}
