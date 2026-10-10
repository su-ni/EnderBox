import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { Icon } from './design'

/**
 * 登录闸门。
 *
 * 整页居中是这个项目里**唯一**允许居中的场景（首屏 Hero 之外就只有它）——
 * 这是一扇门，不是内容页，居中不会破坏「标题→详情→操作」的阅读流。
 *
 * 版式照 `docs/design/pixel-refresh/login.html` 的①默认态；
 * ②「验证中…」（pulse-dot + 按钮禁用）与③「密码错误」（输入框边框变红 + login-err 红块）
 * 这两态也按同一页投影 —— 那页就是拿它们的差异当设计说明写的。
 */
export function LoginScreen({ hint, onDone }: { hint: string; onDone: () => void }) {
  const [pw, setPw] = useState('')
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  const box = useRef<HTMLInputElement>(null)

  useEffect(() => {
    box.current?.focus()
  }, [])

  const submit = async () => {
    if (!pw || busy) return
    setBusy(true)
    setErr('')
    try {
      await api.authLogin(pw)
      setPw('')
      onDone()
    } catch (e) {
      setErr((e as Error).message)
      setPw('')
      box.current?.focus()
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <div className="atmos" aria-hidden="true">
        <div className="grid" />
        <div className="glow-a" />
        <div className="glow-b" />
        <div className="vignette" />
      </div>

      <div className="login-wrap">
        <div className="login-card">
          <div className="login-brand">
            <svg className="mk" viewBox="0 0 16 16" aria-hidden="true">
              <use href="#i-grass" />
            </svg>
            <div>
              <h1 style={{ fontSize: 20, lineHeight: 1.2, fontWeight: 700 }}>EnderBox</h1>
              <p
                style={{
                  fontFamily: 'var(--f-pix)',
                  fontSize: 10.5,
                  letterSpacing: '.1em',
                  color: 'var(--ink-3)',
                  marginTop: 3,
                }}
              >
                MC 服务器管理面板
              </p>
            </div>
          </div>

          <div className="panel" style={{ marginBottom: 0 }}>
            <i className="corner" />
            <div className="box">
              <p className="say">
                这个面板开了访问保护，需要密码才能进。
                <span>{hint || '密码是在「设置 → 访问安全」里设的。'}</span>
              </p>

              {/*
                出错时输入框边框跟着变红（设计稿第③张卡）——
                光在下面写一行红字容易被扫过去，框本身变色才是「就是这里没对上」。
              */}
              <input
                ref={box}
                type="password"
                className="inp"
                style={{
                  marginTop: 14,
                  width: '100%',
                  ...(err ? { borderRightColor: 'var(--bad)', borderBottomColor: 'var(--bad)' } : {}),
                }}
                placeholder="面板密码"
                value={pw}
                autoComplete="current-password"
                onChange={(e) => setPw(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') submit()
                }}
              />

              {err && (
                <div className="login-err">
                  <Icon name="ban" size={13} />
                  {err}
                </div>
              )}

              <button
                type="button"
                className="btn btn-primary"
                style={{ width: '100%', justifyContent: 'center', marginTop: 14 }}
                disabled={!pw || busy}
                onClick={submit}
              >
                {/*
                  「验证中…」不给第二次点击的机会（按钮已 disabled）——
                  连点会发一串请求，最后停在哪条看返回顺序。
                */}
                {busy ? (
                  <>
                    <span className="pulse-dot" style={{ width: 7, height: 7 }} />
                    验证中…
                  </>
                ) : (
                  <>
                    <Icon name="lock" />
                    进入
                  </>
                )}
              </button>
            </div>
          </div>

          <p className="login-foot">
            忘了密码就去这台机器上直接打开面板（本机访问默认免密码），在「设置 → 访问安全」里重设。
          </p>
        </div>
      </div>
    </>
  )
}
