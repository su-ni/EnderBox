import { useEffect, useRef, useState } from 'react'
import { api } from '../api'
import { IconTypeBlock } from './Icons'

/**
 * 登录闸门。
 *
 * 整页居中是这个项目里**唯一**允许居中的场景（首屏 Hero 之外就只有它）——
 * 这是一扇门，不是内容页，居中不会破坏「标题→详情→操作」的阅读流。
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
    <div className="flex min-h-screen items-center justify-center px-4">
      <div className="w-full max-w-[380px]">
        <div className="mb-5 flex items-center gap-3">
          <IconTypeBlock type="paper" size={30} />
          <div>
            <h1 className="text-[22px] leading-tight font-bold text-ink-1">ServerBox</h1>
            <p className="mono-meta">Minecraft 服务器管理面板</p>
          </div>
        </div>

        <div className="px-card p-5">
          <p className="text-[13px] leading-relaxed text-ink-2">
            这个面板开了访问保护，需要密码才能进。
            {hint && <span className="mt-1.5 block text-[12px] text-ink-3">{hint}</span>}
          </p>

          <input
            ref={box}
            type="password"
            className="px-input mt-4 w-full"
            placeholder="面板密码"
            value={pw}
            autoComplete="current-password"
            onChange={(e) => setPw(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') submit()
            }}
          />

          {err && (
            <p className="mt-3 border-l-[3px] border-danger bg-[#1c1314] px-3.5 py-2.5 text-[12.5px] text-[#ffc2c2]">
              {err}
            </p>
          )}

          <button
            type="button"
            className="btn btn-grass mt-4 w-full"
            disabled={!pw || busy}
            onClick={submit}
          >
            {busy ? '验证中…' : '进入'}
          </button>
        </div>

        <p className="mt-4 text-center text-[12px] leading-relaxed text-ink-3">
          忘了密码就去这台机器上直接打开面板（本机访问默认免密码），
          在「设置 → 访问安全」里重设。
        </p>
      </div>
    </div>
  )
}
