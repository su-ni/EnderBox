import { useEffect, useState } from 'react'
import { api, type DownloadTask, type JDKRelease, type JavaMajor, type JavaRuntime } from '../api'
import { useLocalJava } from '../useLocalJava'
import { Icon } from './design'
import { fmtSize, fmtSizeMaybe } from './ui'

/** 本机 Java 是从哪来的。三种来源对用户的意义完全不同，必须如实标出来。 */
const SOURCE_LABEL: Record<JavaRuntime['source'], string> = {
  managed: '自动托管',
  path: 'PATH',
  system: '系统',
}

/**
 * Java 选择器：上半挑本机已有的、下半一键下新的。
 *
 * 为什么两半都要（用户拍板）：只让选本机的话，机器上一个 Java 都没有时直接死路；
 * 只让下载的话，用户本机明明有合适的 JDK 却白下 200 MB。两个都给，谁合适用谁。
 *
 * value 是 java 可执行文件路径；**空串 = 自动挑一个够用的**（后端启动时自己扫）。
 * 空串必须是「明确的一个选项」，不能是「还没选」—— 新手多数情况下就该用自动挑。
 */
export function JavaPicker({
  value,
  onChange,
  needMajor,
}: {
  value: string
  onChange: (path: string) => void
  /** 当前服务端构建要求的 Java 主版本；用来标出本机哪些够用、哪些偏低 */
  needMajor?: number
}) {
  const { list, reload } = useLocalJava()

  const [majors, setMajors] = useState<JavaMajor[] | null>(null)
  const [majErr, setMajErr] = useState('')

  // 用户在「下载」那半选了哪个主版本，以及查到的真实发布信息
  const [pickMajor, setPickMajor] = useState(0)
  const [release, setRelease] = useState<JDKRelease | null>(null)
  const [relLoading, setRelLoading] = useState(false)
  const [relErr, setRelErr] = useState('')

  const [task, setTask] = useState<DownloadTask | null>(null)
  const [dlErr, setDlErr] = useState('')
  const [doneMsg, setDoneMsg] = useState('')

  // 可选主版本清单是静态的，零外部请求 —— 一进面板就能列出来
  useEffect(() => {
    let alive = true
    api
      .javaReleases()
      .then((d) => {
        if (alive) setMajors(d.items ?? [])
      })
      .catch((e) => {
        if (alive) setMajErr((e as Error).message)
      })
    return () => {
      alive = false
    }
  }, [])

  // 轮询下载任务。任务生命周期在后端，跟这个组件活不活着无关；
  // 结束后重新扫一遍本机 Java，让刚装好的那个立刻出现在上面的列表里。
  useEffect(() => {
    if (!task || task.state !== 'running') return
    const id = task.id
    let alive = true
    let timer: ReturnType<typeof setTimeout> | undefined

    const tick = async () => {
      try {
        const t = await api.fetchTask(id)
        if (!alive) return
        setTask(t)
        if (t.state === 'running') {
          timer = setTimeout(tick, 700)
        } else if (t.state === 'done') {
          setDoneMsg(`${t.label || `Java ${pickMajor}`} 装好了，本机 Java 列表已刷新。`)
          void reload()
        } else {
          setDlErr(t.err || '下载失败')
        }
      } catch {
        // 网络抖一下不算失败，下个周期自己会重试
        if (alive) timer = setTimeout(tick, 1200)
      }
    }
    timer = setTimeout(tick, 500)

    return () => {
      alive = false
      if (timer) clearTimeout(timer)
    }
    // pickMajor 只用于完成时的文案，不该触发轮询重建
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task?.id, task?.state, reload])

  const chooseMajor = async (major: number) => {
    setPickMajor(major)
    setRelease(null)
    setRelErr('')
    setDlErr('')
    setDoneMsg('')
    setRelLoading(true)
    try {
      setRelease(await api.javaRelease(major))
    } catch (e) {
      setRelErr((e as Error).message)
    } finally {
      setRelLoading(false)
    }
  }

  const startDownload = async () => {
    setDlErr('')
    setDoneMsg('')
    try {
      setTask(await api.javaDownload(pickMajor))
    } catch (e) {
      setDlErr((e as Error).message)
    }
  }

  const running = task?.state === 'running'
  const failed = task?.state === 'failed'
  const done = task?.state === 'done'
  const installing = task?.phase === 'install'
  const pct = task && task.total > 0 ? Math.min(100, task.percent) : 0
  const unknownTotal = !task || task.total <= 0 || installing

  return (
    <div className="min-w-0">
      {/* ── 上半：本机已有的 Java ── */}
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10, marginBottom: 10 }}>
        <span className="eyebrow">本机的 Java</span>
        <span className="mono-meta">启动时按构建要求挑版本够用的那个</span>
        <button type="button" className="btn sm" style={{ marginLeft: 'auto' }} onClick={() => void reload()}>
          <Icon name="refresh" size={12} />
          重新扫描
        </button>
      </div>

      {list === null ? (
        <p className="mono-meta">正在扫描本机 Java…</p>
      ) : (
        <div className="rlist" style={{ border: '1px solid var(--line-1)' }}>
          {/* 「自动挑」固定排第一：多数人该用这个，且它必须是可点的一个明确选项 */}
          <button
            type="button"
            aria-pressed={value === ''}
            onClick={() => onChange('')}
            className={`java-row${value === '' ? ' sel' : ''}`}
          >
            <Icon name="compass" className="fic" />
            <span className="rmain">
              <span className="rtitle">自动挑一个够用的</span>
              <span className="rsub">精确匹配优先，没有就往上取更高的</span>
            </span>
            <span className="fmeta">按构建要求</span>
            <span className="fmeta">任何来源</span>
            {value === '' ? <Icon name="check" style={{ color: 'var(--ok)' }} /> : <span />}
          </button>

          {list.length === 0 ? (
            <p style={{ padding: '12px 14px', fontSize: 12.5, lineHeight: 1.7, color: 'var(--ink-2)' }}>
              本机一个可用的 Java 都没扫到 —— 用下面的「下载 Java」装一个，装完这里就能选了。
            </p>
          ) : (
            list.map((r) => {
              const on = value === r.path
              return (
                <button
                  key={r.path}
                  type="button"
                  aria-pressed={on}
                  onClick={() => onChange(r.path)}
                  className={`java-row${on ? ' sel' : ''}`}
                >
                  <Icon name="jar" className="fic" />
                  <span className="rmain">
                    <span className="rtitle">
                      Java {r.major}
                      {needMajor ? (
                        r.major >= needMajor ? (
                          <span className="badge wl">够用</span>
                        ) : (
                          <span className="badge w">版本偏低</span>
                        )
                      ) : null}
                    </span>
                    <span className="rsub" title={r.path}>
                      {r.path}
                    </span>
                  </span>
                  <span className="fmeta">{r.vendor}</span>
                  <span className="fmeta">{SOURCE_LABEL[r.source]}</span>
                  {on ? <Icon name="check" style={{ color: 'var(--ok)' }} /> : <span />}
                </button>
              )
            })
          )}
        </div>
      )}

      {/* ── 下半：下载一个新的 JDK ── */}
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10, margin: '18px 0 10px' }}>
        <span className="eyebrow">下载 Java</span>
        <span className="mono-meta">从镜像站拉 Temurin，解压即用，不弹安装向导</span>
      </div>

      {majErr ? (
        <div className="nbox d">
          <div className="hd">
            <Icon name="ban" />
            没拿到可下载的 Java 版本
          </div>
          {majErr}
        </div>
      ) : majors === null ? (
        <p className="mono-meta">正在取可下载的 Java 版本…</p>
      ) : (
        <div className="rlist" style={{ border: '1px solid var(--line-1)' }}>
          {majors.map((m) => {
            const on = pickMajor === m.major
            return (
              <button
                key={m.major}
                type="button"
                aria-pressed={on}
                onClick={() => void chooseMajor(m.major)}
                className={`java-row${on ? ' sel' : ''}`}
              >
                <Icon name="jar" className="fic" />
                <span className="rmain">
                  <span className="rtitle">
                    Java {m.major}
                    {m.lts && <span className="badge wl">LTS</span>}
                  </span>
                  <span className="rsub">{m.forMc}</span>
                </span>
                <span className="fmeta" />
                <span className="fmeta">{on ? '已选' : '选择'}</span>
                {on ? <Icon name="check" style={{ color: 'var(--ok)' }} /> : <span />}
              </button>
            )
          })}
        </div>
      )}

      {/* 选中主版本后：查真实版本 / 大小 / 校验和，再决定下载 */}
      {pickMajor > 0 && (
        <div style={{ marginTop: 12 }}>
          {relLoading ? (
            <p className="mono-meta">正在查 Java {pickMajor} 目前最新是哪一份…</p>
          ) : relErr ? (
            <div className="nbox d">
              <div className="hd">
                <Icon name="ban" />
                没查到可下载的版本
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 10 }}>
                <p style={{ minWidth: 0, flex: 1 }}>{relErr}</p>
                <button type="button" className="btn sm" onClick={() => void chooseMajor(pickMajor)}>
                  <Icon name="refresh" size={12} />
                  重试
                </button>
              </div>
            </div>
          ) : release ? (
            <div className="nbox live">
              <div className="hd">
                <Icon name="dl" />
                Java {release.major} · Temurin {release.version}
                <span className="mono-meta" style={{ marginLeft: 4 }}>
                  {fmtSizeMaybe(release.size)} · 来自 {release.mirrorLabel}
                </span>
              </div>
              <p style={{ fontSize: 12, lineHeight: 1.7, color: 'var(--ink-2)' }}>
                {release.sha
                  ? '源站提供了 SHA-256，下完会校验一遍。'
                  : `${release.mirrorLabel}不提供 sha256，但解压时会校验压缩包自带的 CRC —— 下坏了同样会被拦下。`}
                解压后放进 runtimes/ 目录，属于「自动托管」，不往系统里装任何东西；装完在上面的本机列表里就能选到。
              </p>
              <p className="mono-meta" style={{ marginTop: 6 }} title={release.name}>
                {release.name}
              </p>
              <div style={{ marginTop: 10 }}>
                <button type="button" className="btn btn-primary" disabled={running} onClick={() => void startDownload()}>
                  <Icon name="dl" size={12} />
                  {running ? '下载中…' : `下载 Java ${release.major}`}
                </button>
              </div>
            </div>
          ) : null}
        </div>
      )}

      {/* 下载与安装的进度 */}
      {task && (
        <div className={`nbox ${failed ? 'd' : done ? 'ok' : 'live'}`} style={{ marginTop: 12 }}>
          <div className="hd">
            <Icon name={failed ? 'ban' : done ? 'check' : 'dl'} />
            {failed ? '安装失败' : done ? 'Java 已装好' : task.step}
            {!done && !failed && (task.threads ?? 0) > 1 && <span className="badge">{task.threads} 线程</span>}
            {done && <span className="badge wl">已加入本机列表</span>}
            {!failed && (
              <span className="mono-meta" style={{ marginLeft: 'auto' }}>
                {installing
                  ? '正在解压安装'
                  : `已下载 ${fmtSize(task.done)}${unknownTotal ? '' : ` / ${fmtSize(task.total)}`}`}
              </span>
            )}
          </div>
          {!failed && (
            <div className="prog" style={{ marginTop: 10 }}>
              <div className="fill" style={{ width: unknownTotal ? '100%' : `${pct}%` }} />
              {unknownTotal && <div className="lb2">解压 / 校验中</div>}
            </div>
          )}
          <p style={{ marginTop: 8, fontSize: 12, lineHeight: 1.7, color: 'var(--ink-2)' }}>
            {failed
              ? dlErr || '下载失败，上面的「下载」按钮可以重试。'
              : done
                ? doneMsg
                : installing
                  ? '下载完成，正在解压并校验压缩包 —— 这一步不用联网。'
                  : '下载在后台进行，可以切到别的页面，回来还能看到进度。'}
          </p>
        </div>
      )}
      {dlErr && !failed && (
        <div className="nbox d" style={{ marginTop: 10 }}>
          <div className="hd">
            <Icon name="ban" />
            下载出错
          </div>
          {dlErr}
        </div>
      )}
    </div>
  )
}
