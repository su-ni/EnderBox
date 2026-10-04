import { useCallback, useEffect, useState } from 'react'
import { api, type FetchBuild, type FetchSource, type GameVersion } from '../api'
import { javaShortage, lacksJava, useLocalJava } from '../useLocalJava'
import { IconRefresh } from './Icons'
import { fmtSizeMaybe, fmtTime } from './ui'

/**
 * 哪些通道算「稳定可用」。
 * 原版没有通道概念（为空）。⚠ Velocity 用的是 RECOMMENDED —— 实测 3.5.1 是 RECOMMENDED、
 * 3.4.0 与 4.2.0 是 STABLE。只认 STABLE 的话，会把 Velocity 的推荐版误报成测试版，
 * 还会误触发下面那条「一个稳定构建都没有」的警告。
 */
function isStable(b: FetchBuild) {
  return b.channel === '' || b.channel === 'STABLE' || b.channel === 'RECOMMENDED'
}

/** 通道的显示文案。上游各家用的词不一样，这里统一成人话。 */
function channelLabel(ch: string) {
  if (ch === 'STABLE') return '稳定'
  if (ch === 'RECOMMENDED') return '推荐'
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
  // 「问过源站支持哪些类型了」没有。只有问过之后才有资格判定「这个类型不支持」——
  // 网络失败时 src 也是 null，那不能当成「不支持」，得让下面的请求去报真正的网络错。
  const [srcReady, setSrcReady] = useState(false)
  const [mirror, setMirror] = useState('')
  const [versions, setVersions] = useState<GameVersion[] | null>(null)
  const [builds, setBuilds] = useState<FetchBuild[] | null>(null)
  const [version, setVersion] = useState(value?.version ?? '')
  const [loading, setLoading] = useState('')
  const [err, setErr] = useState('')

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

  // 本机缺这个构建要的 Java 吗。javaMax 为 null（还没问到）时不提示
  const javaMissing = (major: number) => lacksJava(major, javaMax)
  const pickedMissing = value !== null && javaMissing(value.javaMajor)

  // 后端认这个类型、但明说了「没接自动下载」，或者压根不认这个类型（前端传错）。
  // 两种情况都不该去拉版本列表 —— 那个接口必然 400，而界面会把它画成一个
  // 「点重试」的错误，点一百次也还是同样的错，纯属浪费感情。
  const unsupported = srcReady && (!src || !src.available)

  if (unsupported) {
    return (
      <div className="border-l-[3px] border-warn bg-[#1a1812] px-4 py-3">
        <p className="text-[13px] font-semibold text-[#e7dcb2]">
          {src ? `${src.label} 还不能自动下载` : '这个服务端类型还不能自动下载'}
        </p>
        <p className="mt-1 text-[12.5px] leading-relaxed text-[#e7dcb2]">
          {src?.note || '这个类型后端不认，可能是参数传错了。'}
        </p>
        <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
          现在能自动下载的只有 Paper 和原版。想用这个类型，得自己把服务端文件装好再放进实例目录。
        </p>
      </div>
    )
  }

  return (
    <div>
      {/* 下载源：只有多于一个选项时才出这一行，单个选项摆出来是噪音 */}
      {src && src.mirrors.length > 1 && (
        <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2">
          <span className="field-label mb-0">下载源</span>
          <select className="px-input" value={mirror} onChange={(e) => setMirror(e.target.value)}>
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
        <div className="mb-4 flex flex-wrap items-center gap-x-3 gap-y-2 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5">
          <p className="min-w-0 flex-1 text-[12.5px] leading-relaxed text-[#ffc2c2]">{err}</p>
          <button type="button" className="btn btn-sm shrink-0" onClick={() => loadVersions()}>
            <IconRefresh size={12} />
            重试
          </button>
        </div>
      )}

      {/* ── 版本 ── */}
      <p className="field-label">
        游戏版本
        {versions && (
          <span className="mono-meta ml-3 font-normal">
            {versions.length} 个正式版 · 已滤掉快照与预发布
          </span>
        )}
      </p>
      {versions === null ? (
        <p className="text-[12.5px] text-ink-3">正在向源站要版本列表…</p>
      ) : versions.length === 0 ? (
        <p className="text-[12.5px] text-ink-2">没拿到版本列表，点上面的重试。</p>
      ) : (
        <>
          <div
            className={`mt-1 flex flex-wrap gap-2 overflow-y-auto ${compact ? 'max-h-[112px]' : 'max-h-[144px]'}`}
          >
            {versions.map((v) => (
              <button
                key={v.id}
                type="button"
                aria-pressed={version === v.id}
                onClick={() => setVersion(v.id)}
                className={`ver-chip${version === v.id ? ' sel' : ''}`}
              >
                {v.id}
              </button>
            ))}
          </div>
          {versions.length > 12 && (
            <p className="mono-meta mt-1.5">
              框里能滚 —— 一共 {versions.length} 个，从新到旧排
            </p>
          )}
        </>
      )}

      {/* ── 构建 ── */}
      {version && (
        <div className="mt-5">
          <p className="field-label">
            构建
            {type === 'vanilla' && <span className="mono-meta ml-3 font-normal">原版没有构建号，只有一个</span>}
            {type === 'fabric' && (
              <span className="mono-meta ml-3 font-normal">Fabric 固定用最新的稳定 Loader，没得挑</span>
            )}
          </p>
          {loading === 'builds' ? (
            <p className="text-[12.5px] text-ink-3">正在取 {version} 的构建…</p>
          ) : builds === null || builds.length === 0 ? (
            <p className="text-[12.5px] text-ink-2">这个版本没有可下载的构建。</p>
          ) : (
            <>
              {pickedMissing && (
                <p className="mb-3 border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] leading-relaxed text-[#ffc2c2]">
                  <b className="text-ink-1">{javaShortage(value?.javaMajor ?? 0, javaMax)}</b>
                  ，下下来也起不来。先把 Java {value?.javaMajor} 装好（设置页的「Java 运行时」里点
                  「重新扫描」就能认出来），或者换一个低一些的版本。
                </p>
              )}
              {!builds.some(isStable) && (
                <p className="mb-3 border-l-[3px] border-warn bg-[#1a1812] px-4 py-2.5 text-[12.5px] leading-relaxed text-[#e7dcb2]">
                  <b className="text-ink-1">{version} 目前一个稳定构建都没有</b>
                  ，列出来的全是 BETA / ALPHA（Paper 对新版本是先出测试版、稳定了才标记）。
                  想稳一点就换一个老一些的版本，比如 1.21.4 这种已经全量稳定的。
                </p>
              )}
              <div className="px-card">
              {builds.map((b) => {
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
                        javaMajor: b.javaMajor,
                        javaFrom: b.javaFrom,
                        size: b.size,
                        name: b.name,
                        mirror,
                      })
                    }
                    className="row-b flex w-full flex-wrap items-center gap-x-3.5 gap-y-1.5 px-4 py-2.5 text-left"
                    style={{ background: on ? 'var(--color-raised)' : 'transparent' }}
                  >
                    <span
                      className={`mono-value shrink-0 ${on ? 'font-bold text-accent' : ''}`}
                      style={{ minWidth: 74 }}
                    >
                      {b.label || (b.id > 0 ? `#${b.id}` : '官方')}
                    </span>
                    {b.channel && (
                      <span className={`chip shrink-0 ${isStable(b) ? 'chip-g' : 'chip-w'}`}>
                        {channelLabel(b.channel)}
                      </span>
                    )}
                    <span className="mono-meta shrink-0">{fmtSizeMaybe(b.size)}</span>
                    <span className={`mono-meta shrink-0${javaMissing(b.javaMajor) ? ' text-[#ffc2c2]' : ''}`}>
                      需要 Java {b.javaMajor || '?'}
                    </span>
                    {javaMissing(b.javaMajor) && <span className="chip chip-d shrink-0">跑不起来</span>}
                    <span className="mono-meta ml-auto shrink-0">{b.time ? fmtTime(b.time) : ''}</span>
                  </button>
                )
              })}
                {builds.length >= 20 && (
                  <p className="mono-meta px-4 py-2.5">
                    只列出最近 20 个构建。更老的版本用的人少，需要的话直接去源站下再手动放进实例目录。
                  </p>
                )}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}
