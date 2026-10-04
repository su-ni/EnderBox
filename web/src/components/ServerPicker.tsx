import { useCallback, useEffect, useState } from 'react'
import { api, type FetchBuild, type FetchSource, type GameVersion } from '../api'
import { IconRefresh } from './Icons'
import { fmtSize, fmtTime } from './ui'

/** 原版没有通道概念（channel 为空），按稳定处理 */
function isStable(b: FetchBuild) {
  return b.channel === '' || b.channel === 'STABLE'
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
function defaultBuild(list: FetchBuild[]): FetchBuild | null {
  if (list.length === 0) return null
  return list.find(isStable) ?? list[0]
}

export type Picked = {
  version: string
  build: number
  javaMajor: number
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
  const [mirror, setMirror] = useState('')
  const [versions, setVersions] = useState<GameVersion[] | null>(null)
  const [builds, setBuilds] = useState<FetchBuild[] | null>(null)
  const [version, setVersion] = useState(value?.version ?? '')
  const [loading, setLoading] = useState('')
  const [err, setErr] = useState('')

  // 支持的类型 + 该类型能用的下载源。Paper 挂 BMCLAPI 是没意义的（那个路径 404），
  // 所以可选源由后端按类型给，前端不猜。
  useEffect(() => {
    let alive = true
    api
      .fetchSources()
      .then((d) => {
        if (!alive) return
        const s = (d.items ?? []).find((x) => x.type === type) ?? null
        setSrc(s)
        if (s && s.mirrors.length > 0) setMirror(s.mirrors[0].id)
      })
      .catch(() => {})
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
        const def = defaultBuild(list)
        if (def) {
          onPick({
            version: ver,
            build: def.id,
            javaMajor: def.javaMajor,
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

  const unsupported = src && !src.available

  if (unsupported) {
    return (
      <div className="border-l-[3px] border-warn bg-[#1a1812] px-4 py-3">
        <p className="text-[13px] font-semibold text-[#e7dcb2]">{src?.label} 还不能自动下载</p>
        <p className="mt-1 text-[12.5px] leading-relaxed text-[#e7dcb2]">{src?.note}</p>
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
          </p>
          {loading === 'builds' ? (
            <p className="text-[12.5px] text-ink-3">正在取 {version} 的构建…</p>
          ) : builds === null || builds.length === 0 ? (
            <p className="text-[12.5px] text-ink-2">这个版本没有可下载的构建。</p>
          ) : (
            <>
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
                      {b.id > 0 ? `#${b.id}` : '官方'}
                    </span>
                    {b.channel && (
                      <span className={`chip shrink-0 ${b.channel === 'STABLE' ? 'chip-g' : 'chip-w'}`}>
                        {b.channel === 'STABLE' ? '稳定' : b.channel}
                      </span>
                    )}
                    <span className="mono-meta shrink-0">{fmtSize(b.size)}</span>
                    <span className="mono-meta shrink-0">需要 Java {b.javaMajor || '?'}</span>
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
