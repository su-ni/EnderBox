import { useCallback, useEffect, useState } from 'react'
import { api, type ConvertInfo, type ConvertTarget } from '../api'
import { IconTypeBlock } from './Icons'
import { NotReady, Skeleton, useConfirm } from './ui'
import { DownloadBar } from './DownloadBar'
import type { Instance } from '../types'

type Props = {
  instance: Instance
  onToast: (msg: string) => void
  /** 转换成功后让上层重新拉实例列表 —— 类型变了，整页的判据都会跟着变 */
  onDone: () => void
}

/**
 * 「普通模组服 → 对应的混合端」。
 *
 * 存在的理由：模组服开久了想加插件，唯一的办法是换成混合端（本体还是那个模组端，
 * 只是外面多了一层 Bukkit 实现）。这件事用户自己做得对不容易 —— 得知道下哪个 jar、
 * 放进哪个目录、启动文件该指谁、类型在哪里改、顺序还不能反。
 *
 * ⚠ 这里**不做**成「下拉选个类型」：类型是好几处的判据（文件怎么下、插件目录、
 * 支持判定），先改类型再下载，一旦下载失败就留下一台自称混合端、
 * 启动文件还是 Forge 的实例。所以只有「转换」这一个动作，顺序由后端保证。
 */
export function ConvertPanel({ instance, onToast, onDone }: Props) {
  const [info, setInfo] = useState<ConvertInfo | null>(null)
  const [err, setErr] = useState('')
  const [taskId, setTaskId] = useState('')
  // 只锁正在跑的那一格 —— 全页共用一个 busy 会让别的按钮也变灰，看着像坏了
  const [busy, setBusy] = useState('')
  const { dialog, confirm } = useConfirm()

  const load = useCallback(async () => {
    try {
      const d = await api.convertInfo(instance.id)
      setInfo(d)
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [instance.id])

  // 依赖 instance.type：转换完成后上层刷新实例，这里要跟着重新问一遍
  useEffect(() => {
    void load()
  }, [load, instance.type])

  const start = async (t: ConvertTarget) => {
    setBusy(t.type)
    setErr('')
    setTaskId('')
    try {
      const task = await api.convert(instance.id, { target: t.type })
      setTaskId(task.id)
      onToast(`已开始转换 → ${t.label}，下完会自动切换`)
    } catch (e) {
      setErr((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const ask = (t: ConvertTarget) => {
    confirm({
      title: `转换为 ${t.label}`,
      confirmLabel: '开始转换',
      width: 540,
      body: (
        <div className="space-y-2.5">
          <p>
            把 <b>{instance.name}</b> 的服务端从 {info?.currentLabel} 换成 {t.label}。
          </p>
          <p className="text-ink-3">
            会新下一个启动文件 <span className="mono-value">{t.newJar}</span>
            ，下好并校验通过之后才改实例；<b>现在的启动文件</b>{' '}
            <span className="mono-value">{info?.jarPath}</span> 保留不动。
          </p>
          <p className="text-ink-3">
            mods/、plugins/、world/ 和各项配置原样保留，端口内存这些一个都不改。
          </p>
          <div className="nbox w">
            混合端会复用实例里已有的 libraries/ 并把缺的补上，所以倒回去不保证干净；
            少数用 Mixin 改底层类的模组也可能跟 Bukkit 打架。<b>建议先做一次备份。</b>
          </div>
        </div>
      ),
      run: () => {
        void start(t)
      },
    })
  }

  if (err && !info) {
    return (
      <div className="mt-6 border-t border-line pt-5">
        <p className="text-[12.5px] text-danger">{err}</p>
        <button type="button" className="btn sm mt-2.5" onClick={() => void load()}>
          重试
        </button>
      </div>
    )
  }

  return (
    <>
      {dialog}
      <div className="mt-6 border-t border-line pt-5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <h4 className="text-[13.5px] font-semibold text-ink-1">转换为混合端</h4>
          {info && <span className="chip">{info.currentLabel}</span>}
          <span className="mono-meta">模组 + 插件同时跑</span>
        </div>

        {!info ? (
          <div className="mt-3.5 space-y-2" role="status" aria-busy="true" aria-label="正在读取">
            <Skeleton w="60%" />
            <Skeleton w="85%" />
          </div>
        ) : info.targets.length === 0 ? (
          /* 一个目标都没有：这一块的任务就只剩「解释为什么」。
             ⚠ 这时候**不许**再印「混合端的本体还是你这个模组端」和那份代价提示 ——
             前者对 Paper / 代理端是错的（它们本来就不是模组端），
             后者是给「真要转」的人看的，摆在一个点不了的页面上只是噪音。 */
          <>
            <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
              这个类型的实例没有对应的混合端可以转。
            </p>
            {info.refusals.map((r) => (
              <div key={r.label} className="nbox" style={{ marginTop: 14 }}>
                <div className="hd">{r.label} · 不做一键转换</div>
                {r.reason}
              </div>
            ))}
          </>
        ) : (
          <>
            <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
              混合端的本体还是你这个模组端，只是多了一层 Bukkit 实现 —— 所以 mods/ 里的模组不用重装，
              换完之后也能装插件了。
            </p>

            {info.running && (
              <div className="nbox w" style={{ marginTop: 14 }}>
                实例正在运行。转换要先停服 —— Windows 上运行中的 jar 是锁着的，覆盖不了；
                而且换完得重启才生效，跑着换等于换了也不起作用。
              </div>
            )}

            {info.warning && <NotReady>{info.warning}</NotReady>}

            <div className="panel" style={{ marginTop: 14 }}>
              <div className="rlist">
                {info.targets.map((t) => (
                  <div
                    key={t.type}
                    className="rrow"
                    style={{ gridTemplateColumns: '32px minmax(0, 1fr) auto' }}
                  >
                    <div className="ricon">
                      <IconTypeBlock type={t.type} size={18} />
                    </div>
                    <div className="rmain">
                      <div className="rtitle">
                        {t.label}
                        {!t.available && <span className="chip s w">暂不可用</span>}
                      </div>
                      <div className="rsub">{t.note}</div>
                      {t.available && (
                        <div className="rsub" style={{ fontFamily: 'var(--f-term)', fontSize: 13 }}>
                          新启动文件 {t.newJar} · 旧的不删
                        </div>
                      )}
                    </div>
                    {t.available && (
                      <div className="ract">
                        <button
                          type="button"
                          className="btn sm"
                          disabled={info.running || busy === t.type}
                          onClick={() => ask(t)}
                        >
                          {busy === t.type ? '准备中…' : '转换'}
                        </button>
                      </div>
                    )}
                  </div>
                ))}
              </div>
            </div>

            {/* 有目标、同时也有「不做」的路时，也摆出来 —— 用户可能正好在找那一条 */}
            {info.refusals.map((r) => (
              <div key={r.label} className="nbox" style={{ marginTop: 14 }}>
                <div className="hd">{r.label} · 不做一键转换</div>
                {r.reason}
              </div>
            ))}
          </>
        )}

        {taskId && (
          <div className="mt-4">
            <DownloadBar
              taskId={taskId}
              onToast={onToast}
              onSettled={(t) => {
                if (t.state === 'done') {
                  onToast('转换完成，服务端类型已切换')
                  onDone()
                  void load()
                  setTaskId('')
                } else if (t.state === 'failed') {
                  onToast('转换失败：' + (t.err ?? '未知原因') + '（实例没有被改动）')
                  setTaskId('')
                }
              }}
            />
          </div>
        )}

        {err && <p className="mt-3 text-[12.5px] leading-relaxed text-danger">{err}</p>}
      </div>
    </>
  )
}
