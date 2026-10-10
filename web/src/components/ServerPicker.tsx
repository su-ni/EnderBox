import { useCallback, useEffect, useRef, useState } from 'react'
import { api, type FetchBuild, type FetchSource, type GameVersion } from '../api'
import { javaShortage, lacksJava, useLocalJava } from '../useLocalJava'
import { Icon } from './design'
import { fmtSizeMaybe, fmtTime } from './ui'

/**
 * 哪些通道算「可以放心选」。
 * 原版没有通道概念（为空）。⚠ 上游各家用的词不一样，实测：
 *   Velocity 3.5.1 → RECOMMENDED，3.4.0 / 4.2.0 → STABLE
 *   Forge          → 官方自己摆出来的两个是 RECOMMENDED（推荐）和 LATEST（最新）
 * 只认 STABLE 的话，会把上面这些上游自己推荐的版本误报成「测试版」，
 * 还会误触发下面那条「一个稳定构建都没有」的警告 —— 等于用文案骗用户。
 */
function isStable(b: FetchBuild) {
  return b.channel === '' || b.channel === 'STABLE' || b.channel === 'RECOMMENDED' || b.channel === 'LATEST'
}

/** 通道的显示文案。上游各家用的词不一样，这里统一成人话。 */
function channelLabel(ch: string) {
  if (ch === 'STABLE') return '稳定'
  if (ch === 'RECOMMENDED') return '推荐'
  if (ch === 'LATEST') return '最新'
  return ch
}

/**
 * 默认选中哪个构建：**最新的稳定构建**，而不是最新的构建。
 *
 * 实测数据（2026-10-04）：Paper 各版本的通道分布差别很大 ——
 *   1.21.4  → 100 个全是 STABLE
 *   26.3    → 62 个里 0 个 STABLE（14 BETA + 48 ALPHA）
 *   1.21.11 → 63 STABLE + 17 BETA + 12 ALPHA
 * 所以「直接取列表第一个」会把测试版塞给新手。没有稳定构建时才退回最新的，
 * 并且界面上必须明说 —— 用户有权知道自己拿的是测试版。
 */
function defaultBuild(list: FetchBuild[], runnable: (b: FetchBuild) => boolean): FetchBuild | null {
  if (list.length === 0) return null
  // 先把「本机装不了它要的 Java」的构建排掉 —— 默认给一个下完起不来的包毫无意义。
  // 全都跑不起来时（比如最新版要 Java 25、这台只有 21）才退回全列表，
  // 同时界面上会明确说清楚缺哪个 Java。
  const usable = list.filter(runnable)
  const pool = usable.length > 0 ? usable : list
  return pool.find(isStable) ?? pool[0]
}

export type Picked = {
  version: string
  build: number
  /** 上游的稳定标识（Forge 用它回源站定位；别的类型不需要） */
  tag?: string
  javaMajor: number
  /** javaMajor 的来源，确认页要如实说明；空=按旧口径兜底 */
  javaFrom?: string
  size: number
  name: string
  /** 下这个包该走哪个源。由选择器决定，调用方直接用，不要自己再拼 */
  mirror: string
}

/**
 * 服务端版本 / 构建选择器。向导和配置页共用同一个 ——
 * 两边各写一套的话，「哪个构建是最新的」这种逻辑迟早会不一致。
 *
 * 数据全部来自后端代理（浏览器直连 PaperMC / Mojang 会撞 CORS），
 * 后端那边带了 10 分钟缓存，来回点不会把人家 API 打爆。
 */
export function ServerPicker({
  type,
  value,
  onPick,
  compact,
  preferVersion,
}: {
  type: string
  value: Picked | null
  onPick: (p: Picked) => void
  compact?: boolean
  /**
   * 优先选中哪个版本。配置页必须传实例当前的 MC 版本 ——
   * 不传的话默认会落到「最新的游戏版本」，对一个已有的 1.20.4 实例来说是错的默认值。
   */
  preferVersion?: string
}) {
  const [src, setSrc] = useState<FetchSource | null>(null)
  // 支持的全部类型。只为了在「这个类型走不通」时能如实列出「现在能走的有哪些」——
  // 写死一份名单的话，哪天后端接了新类型，这句提示就会变成假话。
  const [allSrc, setAllSrc] = useState<FetchSource[]>([])
  // 「问过源站支持哪些类型了」没有。只有问过之后才有资格判定「这个类型不支持」——
  // 网络失败时 src 也是 null，那不能当成「不支持」，得让下面的请求去报真正的网络错。
  const [srcReady, setSrcReady] = useState(false)
  const [mirror, setMirror] = useState('')
  const [versions, setVersions] = useState<GameVersion[] | null>(null)
  const [builds, setBuilds] = useState<FetchBuild[] | null>(null)
  const [version, setVersion] = useState(value?.version ?? '')
  const [loading, setLoading] = useState('')
  const [err, setErr] = useState('')
  // 版本搜索：一个类型动辄几十个版本（实测 Paper 55 个），滚着找太费劲。
  const [query, setQuery] = useState('')
  // 左列版本列表。用来把「当前选中」的那个滚进视野 —— 深链带 mc= 或配置页带
  // 实例当前版本进来时，选中的版本可能排在几十个版本之后，停在顶部等于看不见它选了什么。
  const vlistRef = useRef<HTMLDivElement>(null)

  // 本机有哪些 Java。选构建时就要比对，别让人白下 50MB 才发现起不来。
  const { max: javaMax, maxRef: javaMaxRef } = useLocalJava()

  // 支持的类型 + 该类型能用的下载源。Paper 挂 BMCLAPI 是没意义的（那个路径 404），
  // 所以可选源由后端按类型给，前端不猜。
  useEffect(() => {
    let alive = true
    setSrcReady(false)
    api
      .fetchSources()
      .then((d) => {
        if (!alive) return
        const s = (d.items ?? []).find((x) => x.type === type) ?? null
        setSrc(s)
        setAllSrc(d.items ?? [])
        setSrcReady(true)
        if (s && s.mirrors.length > 0) setMirror(s.mirrors[0].id)
      })
      .catch(() => {
        if (!alive) return
        setSrcReady(false)
      })
    return () => {
      alive = false
    }
  }, [type])

  const loadVersions = useCallback(async () => {
    setLoading('versions')
    setErr('')
    try {
      const d = await api.fetchVersions(type, mirror)
      const list = d.items ?? []
      setVersions(list)
      // 进来就该看到构建列表，而不是一个空框等用户点。
      // 优先选调用方指定的版本（配置页 = 实例当前版本），其次才是最新的。
      setVersion((cur) => {
        if (cur) return cur
        if (preferVersion && list.some((v) => v.id === preferVersion)) return preferVersion
        return list[0]?.id || ''
      })
      return list
    } catch (e) {
      setErr((e as Error).message)
      setVersions([])
      return []
    } finally {
      setLoading('')
    }
  }, [type, mirror, preferVersion])

  useEffect(() => {
    setVersions(null)
    setBuilds(null)
    loadVersions()
  }, [loadVersions])

  // 选版本 → 拉构建。默认选最新那个构建，省掉一次点击。
  const loadBuilds = useCallback(
    async (ver: string) => {
      if (!ver) {
        setBuilds(null)
        return
      }
      setLoading('builds')
      setErr('')
      try {
        const d = await api.fetchBuilds(type, ver, mirror, 20)
        const list = d.items ?? []
        setBuilds(list)
        // 读 ref 而不是 state：加载函数不能因为 javaMax 变化而重建（会连带重拉列表）
        const def = defaultBuild(list, (b) => !lacksJava(b.javaMajor, javaMaxRef.current))
        if (def) {
          onPick({
            version: ver,
            build: def.id,
            tag: def.tag,
            javaMajor: def.javaMajor,
            javaFrom: def.javaFrom,
            size: def.size,
            name: def.name,
            mirror,
          })
        }
      } catch (e) {
        setErr((e as Error).message)
        setBuilds([])
      } finally {
        setLoading('')
      }
    },
    [type, mirror, onPick],
  )

  useEffect(() => {
    if (!version) return
    loadBuilds(version)
    // 只在版本或镜像变化时重拉，不在 onPick 变化时（父组件每次渲染都可能换引用）
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [version, type, mirror])

  // 让选中的版本项在左列里可见。用 block:'nearest'：元素已经可见时它什么都不做，
  // 不会把整页也带着滚一下。依赖 versions 是因为列表渲染出来之后才找得到那个元素。
  useEffect(() => {
    vlistRef.current?.querySelector('.vrow.on')?.scrollIntoView({ block: 'nearest' })
  }, [version, versions, query])

  // 版本搜索的过滤结果。只筛左栏的版本列表 —— 右栏构建是按选中版本拉的、最多 20 条，
  // 再叠一层搜索只会让「搜了版本号结果构建栏空掉」这种困惑出现。
  const q = query.trim().toLowerCase()
  const shownVersions = q ? (versions ?? []).filter((v) => v.id.toLowerCase().includes(q)) : (versions ?? [])

  // 本机缺这个构建要的 Java 吗。javaMax 为 null（还没问到）时不提示
  const javaMissing = (major: number) => lacksJava(major, javaMax)
  const pickedMissing = value !== null && javaMissing(value.javaMajor)

  // 后端认这个类型、但明说了「没接自动下载」，或者压根不认这个类型（前端传错）。
  // 两种情况都不该去拉版本列表 —— 那个接口必然 400，而界面会把它画成一个
  // 「点重试」的错误，点一百次也还是同样的错，纯属浪费感情。
  const unsupported = srcReady && (!src || !src.available)

  if (unsupported) {
    return (
      <div className="nbox w" style={{ margin: '14px 16px' }}>
        <div className="hd">
          <Icon name="info" />
          {src ? `${src.label} 还不能自动下载` : '这个服务端类型还不能自动下载'}
        </div>
        <p>{src?.note || '这个类型后端不认，可能是参数传错了。'}</p>
        <p style={{ marginTop: 8, fontSize: 12, color: 'var(--ink-3)' }}>
          {allSrc.some((s) => s.available)
            ? `现在能自动下载的有 ${allSrc.filter((s) => s.available).map((s) => s.label).join('、')}。`
            : ''}
          想用这个类型，得自己把服务端文件装好再放进实例目录。
        </p>
      </div>
    )
  }

  return (
    <div>
      {/* 下载源：只有多于一个选项时才出这一行，单个选项摆出来是噪音 */}
      {src && src.mirrors.length > 1 && (
        <div
          style={{
            display: 'flex',
            flexWrap: 'wrap',
            alignItems: 'center',
            gap: '10px 12px',
            padding: '12px 16px',
            borderBottom: '1px solid var(--line-1)',
          }}
        >
          <span className="eyebrow">下载源</span>
          <select
            className="inp"
            style={{ width: 'auto', minWidth: 160 }}
            value={mirror}
            onChange={(e) => setMirror(e.target.value)}
          >
            {src.mirrors.map((m) => (
              <option key={m.id} value={m.id}>
                {m.label}
              </option>
            ))}
          </select>
          <span className="mono-meta">{src.note}</span>
        </div>
      )}

      {err && (
        <div className="nbox d" style={{ margin: '14px 16px' }}>
          <div className="hd">
            <Icon name="ban" />
            版本列表没拿到
          </div>
          <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10 }}>
            <p style={{ minWidth: 0, flex: 1 }}>{err}</p>
            <button type="button" className="btn sm" onClick={() => loadVersions()}>
              <Icon name="refresh" size={12} />
              重试
            </button>
          </div>
        </div>
      )}

      {/* ── 版本 × 构建：左选版本、右选构建 ──
          照设计稿 wizard.html 第②步：grid 1fr 320px，右列一条左分隔线。
          原来是「版本一面 chip 墙 + 构建在下面另一块」，切个版本得在上下两处来回找。 */}
      {versions === null ? (
        <div style={{ padding: '14px 16px' }}>
          <p className="mono-meta">正在向源站要版本列表…</p>
        </div>
      ) : versions.length === 0 ? (
        <div style={{ padding: '14px 16px' }}>
          <p style={{ fontSize: 12.5, color: 'var(--ink-2)' }}>没拿到版本列表，点上面的重试。</p>
        </div>
      ) : (
        <>
          {/* .svgrid/.svcol：两列等高、列表吸底 —— 右列（提醒块 + 构建列表）有多高，
              左列就跟着多高。以前两列各自封顶，右列加载完变高、左列停在半路，
              底下空一大块（2026-10-09 用户截图）。样式在 design-overrides.css 里。 */}
          <div className={`svgrid${compact ? ' compact' : ''}`}>
            {/* 左列：版本竖排 */}
            <div className="svcol">
              {/* 搜版本：一个类型动辄几十个版本（Paper 55 个），滚着找太费劲 */}
              <div style={{ padding: '12px 14px 10px' }}>
                <div className="search" style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <span style={{ flex: 'none', color: 'var(--ink-3)' }}>
                    <Icon name="search" size={13} />
                  </span>
                  <input
                    type="search"
                    id="sbx-version-search"
                    name="version-search"
                    aria-label="搜索游戏版本"
                    style={{ minWidth: 0, flex: 1, background: 'none', border: 0, outline: 0 }}
                    className="mono-value"
                    placeholder="搜游戏版本，例如 1.20 或 1.21.4"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                  />
                  <span className="mono-meta" style={{ flex: 'none' }}>
                    {q ? `${shownVersions.length} / ${versions.length}` : `${versions.length} 个版本`}
                  </span>
                </div>
              </div>

              {/* 左列版本列表：靠 .svcol > .vpick 的 flex:1 吸到底 —— 右列（提醒块 + 构建）
                  有多高它就多高，两列底边永远齐平。77 个版本（Forge）全平铺是 2943px，
                  盒子本身内滚，不会把整步撑开。 */}
              <div className="vlist vpick" ref={vlistRef}>
                {shownVersions.length === 0 ? (
                  <p style={{ padding: '12px 14px', fontSize: 12, lineHeight: 1.7, color: 'var(--ink-3)' }}>
                    没有匹配「{query.trim()}」的版本，清掉搜索框再看看。
                  </p>
                ) : (
                  shownVersions.map((v, i) => {
                    const on = version === v.id
                    return (
                      <button
                        key={v.id}
                        type="button"
                        aria-pressed={on}
                        onClick={() => setVersion(v.id)}
                        className={`vrow${on ? ' on' : ''}`}
                        style={{ gridTemplateColumns: '130px 1fr auto' }}
                      >
                        <span className="ver">{v.id}</span>
                        <span style={{ fontSize: 12.5, color: 'var(--ink-3)' }}>
                          {v.family && v.family !== v.id ? `版本族 ${v.family}` : ''}
                        </span>
                        {on ? (
                          <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--cyan)' }}>
                            已选
                          </span>
                        ) : i === 0 ? (
                          <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ok)' }}>
                            最新
                          </span>
                        ) : null}
                      </button>
                    )
                  })
                )}
              </div>
            </div>

            {/* 右列：构建列表 */}
            <div className="svcol" style={{ padding: '14px 16px', borderLeft: '1px solid var(--line-1)' }}>
              <div className="eyebrow" style={{ marginBottom: 10 }}>
                BUILD · {version || '先选版本'}
                {builds ? ` · ${builds.length} 个` : ''}
              </div>

              {/* 提醒块排在构建列表上方 —— 它们决定「要不要在这个版本上装」 */}
              {pickedMissing && (
                <div className="nbox d" style={{ marginBottom: 10 }}>
                  <b>{javaShortage(value?.javaMajor ?? 0, javaMax)}</b>
                  ，下下来也起不来。先把 Java {value?.javaMajor} 装好（「运行环境」那步就能下载，
                  或设置页的「Java 运行时」里点「重新扫描」），或者换一个低一些的版本。
                </div>
              )}
              {builds && builds.length > 0 && !builds.some(isStable) && (
                <div className="nbox w" style={{ marginBottom: 10 }}>
                  <div className="hd">
                    <Icon name="info" />
                    {version} 目前一个稳定构建都没有
                  </div>
                  列出来的全是 BETA / ALPHA（Paper 对新版本是先出测试版、稳定了才标记）。
                  想稳一点就换一个老一些的版本，比如 1.21.4 这种已经全量稳定的。
                </div>
              )}
              {type === 'forge' && (
                <div className="nbox live" style={{ marginBottom: 10 }}>
                  Forge 跟别家不一样：下的是<b>安装器</b>，下完还要在本机跑一遍才算装好。全程自动，
                  不用你管。
                </div>
              )}
              {type === 'vanilla' && (
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginBottom: 10, lineHeight: 1.7 }}>
                  原版没有构建号，一个游戏版本只有一个包。
                </p>
              )}
              {type === 'fabric' && (
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginBottom: 10, lineHeight: 1.7 }}>
                  Fabric 固定用最新的稳定 Loader，没得挑。
                </p>
              )}

              {/* 构建列表盒：加载中 / 空态也留在这个盒子里（.svcol > .vpick 有 min-height）。
                  以前这两个状态是一段裸文字、没有盒子 —— 取到构建的那一刻右列突然长高
                  240px，整个面板跟着弹一下。 */}
              <div className="vlist vpick" style={{ border: '1px solid var(--line-1)' }}>
                {!version || loading === 'builds' ? (
                  <p style={{ padding: '12px 14px', fontSize: 12.5, color: 'var(--ink-3)' }}>
                    正在取 {version || ''} 的构建…
                  </p>
                ) : builds === null || builds.length === 0 ? (
                  <p style={{ padding: '12px 14px', fontSize: 12.5, color: 'var(--ink-2)' }}>
                    这个版本没有可下载的构建。
                  </p>
                ) : (
                  builds.map((b) => {
                    const on = value?.build === b.id && value?.version === version
                    return (
                      <button
                        key={b.id || b.name}
                        type="button"
                        aria-pressed={on}
                        onClick={() =>
                          onPick({
                            version,
                            build: b.id,
                            tag: b.tag,
                            javaMajor: b.javaMajor,
                            javaFrom: b.javaFrom,
                            size: b.size,
                            name: b.name,
                            mirror,
                          })
                        }
                        className={`vrow${on ? ' on' : ''}`}
                        style={{ gridTemplateColumns: '1fr auto' }}
                      >
                        <span style={{ minWidth: 0 }}>
                          <span style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                            <span className="ver" style={{ fontFamily: 'var(--f-body)', fontSize: 13 }}>
                              {b.label || (b.id > 0 ? `#${b.id}` : '官方')}
                            </span>
                            {b.channel && (
                              <span className={`chip s ${isStable(b) ? 'g' : 'w'}`}>
                                {channelLabel(b.channel)}
                              </span>
                            )}
                            {javaMissing(b.javaMajor) && <span className="chip s d">跑不起来</span>}
                          </span>
                          <span className="mono-meta" style={{ display: 'block', marginTop: 2 }}>
                            需要 Java {b.javaMajor || '?'} · {fmtSizeMaybe(b.size)}
                          </span>
                        </span>
                        <span className="fmeta" style={{ flex: 'none' }}>
                          {b.time ? fmtTime(b.time) : ''}
                        </span>
                      </button>
                    )
                  })
                )}
              </div>
              {/* 列表下面的通用说明：只有真拿到列表时才显示 */}
              {!loading && builds && builds.length > 0 && (
                <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 10, lineHeight: 1.7 }}>
                  一般取最新 build。老 build 只在「新版把某个插件搞坏了」时才需要回退。
                </p>
              )}
              {!loading && builds && builds.length >= 20 && (
                <p className="mono-meta" style={{ marginTop: 8 }}>
                  {type === 'forge'
                    ? '只列出 20 个。整合包锁了更老的 Forge 版本的话，去源站下那个版本的安装器，放进实例目录再跑一遍就行。'
                    : '只列出最近 20 个构建。更老的版本用的人少，需要的话直接去源站下再手动放进实例目录。'}
                </p>
              )}
            </div>
          </div>
          <p className="mono-meta" style={{ padding: '10px 16px 14px' }}>
            {type === 'forge'
              ? `${versions.length} 个版本 · 取自 Forge 官方的推荐表`
              : `${versions.length} 个正式版 · 已滤掉快照与预发布`}
          </p>
        </>
      )}
    </div>
  )
}
