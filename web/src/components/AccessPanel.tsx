import { useCallback, useEffect, useState } from 'react'
import { api, type GlobalSettings, type MirrorOption } from '../api'
import { IconCheck, IconRefresh, IconShield, IconTrash, IconWarn } from './Icons'
import { Field, NotReady, SectionHead, SkeletonFields, ToggleRow, useConfirm } from './ui'

/**
 * 「访问安全」面板。
 *
 * 两条设计原则：
 *  1. **默认不挡人**：监听在本机时免密码，装完就能用。只在真的暴露到局域网
 *     且没设密码时才红着脸告警 —— 一上来就逼人设密码，多数人只会随便打一个然后忘掉。
 *  2. **敏感值只出现一次**：API 令牌的明文生成后立刻从内存里清掉，列表里只剩前缀。
 *     想再拿回来只能重新生成 —— 这是唯一诚实的做法。
 */
export function AccessPanel({ onToast }: { onToast: (m: string) => void }) {
  const [cfg, setCfg] = useState<GlobalSettings | null>(null)
  const [err, setErr] = useState('')
  const [busy, setBusy] = useState('')

  // 密码表单
  const [oldPw, setOldPw] = useState('')
  const [newPw, setNewPw] = useState('')
  const [newPw2, setNewPw2] = useState('')

  // 令牌：明文只在刚生成后存在 NewToken 里
  const [tkName, setTkName] = useState('')
  const [newToken, setNewToken] = useState('')

  const { dialog, confirm } = useConfirm()

  const load = useCallback(async () => {
    try {
      setCfg(await api.settings())
      setErr('')
    } catch (e) {
      setErr((e as Error).message)
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const save = async (patch: Parameters<typeof api.saveSettings>[0], okMsg: string) => {
    setBusy('save')
    try {
      setCfg(await api.saveSettings(patch))
      onToast(okMsg)
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const doSetPassword = async (enable: boolean) => {
    if (newPw.length < 6) {
      onToast('密码至少 6 位')
      return
    }
    if (newPw !== newPw2) {
      onToast('两次输入的密码不一样')
      return
    }
    setBusy('pw')
    try {
      setCfg(await api.setPassword({ old: oldPw, new: newPw, enable }))
      setOldPw('')
      setNewPw('')
      setNewPw2('')
      onToast(enable ? '密码已设置，访问保护已开启' : '密码已更新')
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const doClearPasswordNow = async () => {
    setBusy('pw')
    try {
      setCfg(await api.setPassword({ old: oldPw, disable: true }))
      setOldPw('')
      onToast('密码已清除，访问保护已关闭')
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const doClearPassword = () => {
    confirm({
      title: '清除密码',
      danger: true,
      confirmLabel: '清除并关闭保护',
      run: doClearPasswordNow,
      body: (
        <>
          清除密码会<b className="text-ink-1">同时关掉访问保护</b>。
          <br />
          <br />
          那之后，同一个局域网里任何人都能打开这个面板、启停你的服务器，改动配置。
          如果这台机器接的是公共网络，别这么做。
        </>
      ),
    })
  }

  const doAddToken = async () => {
    setBusy('tk')
    try {
      const t = await api.addToken(tkName.trim())
      setNewToken(t.token)
      setTkName('')
      await load()
    } catch (e) {
      onToast((e as Error).message)
    } finally {
      setBusy('')
    }
  }

  const doRemoveTokenNow = async (id: string) => {
    try {
      await api.removeToken(id)
      await load()
      onToast('令牌已删除')
    } catch (e) {
      onToast((e as Error).message)
    }
  }

  const doRemoveToken = (id: string, name: string) =>
    confirm({
      title: `删除令牌「${name}」`,
      danger: true,
      confirmLabel: '删除令牌',
      run: () => doRemoveTokenNow(id),
      body: (
        <>
          用了这个令牌的脚本、定时任务、第三方工具会<b className="text-ink-1">立刻失效</b>。
          <br />
          <br />
          想恢复只能重新生成一个，新令牌和旧的不是同一个。
        </>
      ),
    })

  if (err) {
    return (
      <p className="border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
        读取设置失败：{err}
      </p>
    )
  }
  if (!cfg) return <SkeletonFields rows={4} />

  // 只有「暴露在局域网 且 没开保护」才算危险。只开保护但没暴露，是用户自己的选择。
  const risky = cfg.runtime.exposed && !cfg.requireAuth

  return (
    <div>
      <SectionHead idx="01" title="访问安全" note="谁可以打开这个面板" />

      {risky && (
        <div className="mb-5 border-l-[3px] border-danger bg-[#1c1314] px-4 py-3.5">
          <p className="flex items-center gap-2 text-[13.5px] font-bold text-[#ffc2c2]">
            <IconWarn size={13} />
            现在局域网里任何人都能打开这个面板
          </p>
          <p className="mt-1.5 max-w-[76ch] text-[12.5px] leading-[1.7] text-[#e8cfcf]">
            守护进程监听在 <b className="mono-value text-[#ffd9d9]">{cfg.runtime.bindingAddr}</b>
            （所有网卡），而访问保护没开。同一个 Wi-Fi 下的任何设备都能启停你的服务器、删你的存档。
            下面设一个密码并开启保护就行。
          </p>
        </div>
      )}

      <div className="space-y-4">
        <ToggleRow
          on={cfg.requireAuth}
          onChange={(v) => save({ requireAuth: v }, v ? '访问保护已开启' : '访问保护已关闭')}
          title="开启访问保护"
          desc="开启后，非本机访问需要密码或 API 令牌。"
          disabled={busy === 'save' || (!cfg.hasPassword && cfg.tokens.length === 0)}
        />
        {!cfg.hasPassword && cfg.tokens.length === 0 && (
          <p className="-mt-2 pl-3 text-[12px] text-warn">
            还没有密码也没有令牌，开不了保护 —— 先把下面密码设上。
          </p>
        )}

        <ToggleRow
          on={cfg.trustLoopback}
          onChange={(v) => save({ trustLoopback: v }, '已更新')}
          title="本机访问免登录"
          desc="在这台机器上打开面板不用输密码。关掉的话自己每次也要登。"
          disabled={busy === 'save'}
        />

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div className="max-w-[220px]">
            <Field label="登录有效期（小时）" hint="过期后需要重新登录">
              <input
                className="px-input px-input-mono w-full"
                type="text"
                inputMode="numeric"
                defaultValue={String(cfg.sessionHours)}
                onBlur={(e) => {
                  const n = Number(e.target.value)
                  if (!Number.isFinite(n) || n < 1 || n > 720) {
                    onToast('有效期要在 1 到 720 小时之间')
                    e.target.value = String(cfg.sessionHours)
                    return
                  }
                  if (n !== cfg.sessionHours) save({ sessionHours: n }, '已更新有效期')
                }}
              />
            </Field>
          </div>

          <div>
            <Field
              label="监听地址"
              hint=":23333 表示所有网卡都监听（手机能连）；127.0.0.1:23333 表示只本机能连"
            >
              <input
                className="px-input px-input-mono w-full"
                defaultValue={cfg.listenAddr}
                placeholder=":23333"
                onBlur={(e) => {
                  const v = e.target.value.trim()
                  if (v !== cfg.listenAddr) save({ listenAddr: v }, '监听地址已保存，重启守护进程后生效')
                }}
              />
            </Field>
            <p className="mt-2 text-[12px] leading-relaxed text-ink-3">
              当前实际绑定 <b className="mono-value">{cfg.runtime.bindingAddr}</b>
              {cfg.runtime.restartNeeded && (
                <span className="text-warn"> · 改过了，重启守护进程才生效</span>
              )}
            </p>
          </div>
        </div>
      </div>

      {/* ── 密码 ── */}
      <div className="mt-8">
        <SectionHead
          idx="02"
          title="面板密码"
          note={cfg.hasPassword ? `已设置 · ${cfg.passwordSetAt ?? ''}` : '还没设置'}
          right={
            <span
              className={`chip ${cfg.hasPassword ? 'chip-g' : ''}`}
              title={cfg.hasPassword ? '已设置密码' : '未设置'}
            >
              {cfg.hasPassword ? '已保护' : '未设置'}
            </span>
          }
        />
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          {cfg.hasPassword && (
            <Field label="当前密码" hint="改密码必须先给旧的">
              <input
                className="px-input w-full"
                type="password"
                value={oldPw}
                autoComplete="current-password"
                onChange={(e) => setOldPw(e.target.value)}
              />
            </Field>
          )}
          <Field label={cfg.hasPassword ? '新密码' : '设置密码'} hint="至少 6 位">
            <input
              className="px-input w-full"
              type="password"
              value={newPw}
              autoComplete="new-password"
              onChange={(e) => setNewPw(e.target.value)}
            />
          </Field>
          <Field label="再输一遍">
            <input
              className="px-input w-full"
              type="password"
              value={newPw2}
              autoComplete="new-password"
              onChange={(e) => setNewPw2(e.target.value)}
            />
          </Field>
        </div>

        <div className="mt-4 flex flex-wrap items-center gap-x-3 gap-y-2">
          <button
            type="button"
            className="btn btn-grass"
            disabled={busy === 'pw' || !newPw}
            onClick={() => doSetPassword(!cfg.requireAuth)}
          >
            {busy === 'pw' ? '处理中…' : cfg.hasPassword ? '改密码' : '设密码并开启保护'}
          </button>
          {cfg.hasPassword && (
            <button
              type="button"
              className="btn btn-danger"
              disabled={busy === 'pw'}
              onClick={doClearPassword}
            >
              清除密码
            </button>
          )}
          <span className="text-[12px] text-ink-3">
            {cfg.hasPassword ? '改完密码所有已登录的设备都会被踢下线' : '设完密码保护会自动开启'}
          </span>
        </div>
      </div>

      {/* ── API 令牌 ── */}
      <div className="mt-8">
        <SectionHead
          idx="03"
          title="API 令牌"
          note="给脚本 / 外部工具用，不用把密码写进脚本里"
          right={
            <button type="button" className="btn btn-ghost btn-sm" onClick={load}>
              <IconRefresh size={12} />
              刷新
            </button>
          }
        />

        {newToken && (
          <div className="mb-4 border-l-[3px] border-accent-deep bg-[#141a13] px-4 py-3.5">
            <p className="flex items-center gap-2 text-[13px] font-semibold text-ink-1">
              <IconCheck size={12} />
              令牌已生成 —— 关掉这一页就再也看不到了
            </p>
            <p className="mt-2.5 font-mono text-[13px] break-all text-accent">{newToken}</p>
            <p className="mt-2.5 text-[12px] leading-relaxed text-ink-2">
              用法：请求头带 <code className="font-mono text-cyan">Authorization: Bearer &lt;令牌&gt;</code>，
              或者给 EventSource 这类设不了头的场景走{' '}
              <code className="font-mono text-cyan">?token=&lt;令牌&gt;</code>。
              我们只存它的哈希，明文拿不回来。
            </p>
            <button type="button" className="btn btn-sm mt-3" onClick={() => setNewToken('')}>
              我知道了，收起来
            </button>
          </div>
        )}

        {cfg.tokens.length === 0 ? (
          <p className="text-[12.5px] text-ink-3">还没有令牌。</p>
        ) : (
          <div className="stagger px-card">
            {cfg.tokens.map((t) => (
              <div key={t.id} className="row-b flex flex-wrap items-center gap-x-3.5 gap-y-1.5 px-4 py-3">
                <IconShield size={16} />
                <div className="min-w-0 flex-1">
                  <div className="truncate text-[14px] leading-tight font-semibold text-ink-1">
                    {t.name}
                  </div>
                  <div className="mono-meta mt-0.5 truncate">
                    {t.prefix}… · 建于 {t.createdAt}
                    {t.lastUsed ? ` · 最后用 ${t.lastUsed}` : ' · 还没用过'}
                  </div>
                </div>
                <button
                  type="button"
                  className="btn btn-ghost btn-sm shrink-0"
                  onClick={() => doRemoveToken(t.id, t.name)}
                  aria-label={`删除令牌 ${t.name}`}
                >
                  <IconTrash size={12} />
                </button>
              </div>
            ))}
          </div>
        )}

        <div className="mt-4 flex flex-wrap items-end gap-3">
          <div className="w-full max-w-[300px]">
            <Field label="新建令牌" hint="取个好认的名字，比如「手机上的脚本」">
              <input
                className="px-input w-full"
                value={tkName}
                maxLength={24}
                placeholder="用途备注"
                onChange={(e) => setTkName(e.target.value)}
              />
            </Field>
          </div>
          <button type="button" className="btn" disabled={busy === 'tk'} onClick={doAddToken}>
            {busy === 'tk' ? '生成中…' : '生成令牌'}
          </button>
        </div>
      </div>

      {dialog}
    </div>
  )
}

/** 「下载源」分页 */
export function SourcePanel({ onToast }: { onToast: (m: string) => void }) {
  const [cfg, setCfg] = useState<GlobalSettings | null>(null)
  const [opts, setOpts] = useState<MirrorOption[]>([])
  const [err, setErr] = useState('')

  useEffect(() => {
    api
      .settings()
      .then(setCfg)
      .catch((e) => setErr((e as Error).message))
    api
      .mirrorOptions()
      .then((d) => setOpts(d.items ?? []))
      .catch(() => {})
  }, [])

  if (err) {
    return (
      <p className="border-l-[3px] border-danger bg-[#1c1314] px-4 py-2.5 text-[12.5px] text-[#ffc2c2]">
        读取失败：{err}
      </p>
    )
  }
  if (!cfg) return <SkeletonFields rows={4} />

  return (
    <div>
      <SectionHead idx="03" title="下载源" note="新建实例、换服务端时默认走哪个源" />
      <div className="max-w-[420px]">
        <Field label="默认下载源">
          <select
            className="px-input w-full"
            value={cfg.defaultMirror}
            onChange={async (e) => {
              try {
                setCfg(await api.saveSettings({ defaultMirror: e.target.value }))
                onToast('默认下载源已更新')
              } catch (er) {
                onToast((er as Error).message)
              }
            }}
          >
            {opts.map((m) => (
              <option key={m.id} value={m.id}>
                {m.label}
              </option>
            ))}
          </select>
        </Field>
      </div>

      <div className="mt-5 px-card">
        {opts.map((m) => (
          <div key={m.id} className="row-b flex flex-wrap items-center gap-x-3.5 gap-y-1.5 px-4 py-3">
            <div className="min-w-0 flex-1">
              <div className="text-[14px] leading-tight font-semibold text-ink-1">{m.label}</div>
              <div className="mono-meta mt-0.5">适用：{m.appliesTo.join(' / ')}</div>
            </div>
            {m.appliesTo.length < 2 && (
              <span className="chip chip-w shrink-0" title="这个镜像只覆盖了部分服务端类型">
                部分类型不可用
              </span>
            )}
          </div>
        ))}
      </div>

      <div className="mt-5">
        <NotReady>
          镜像不是挂上就有用：没被镜像的路径会直接 404，选了反而更糟。所以这里只列出实测能用的，
          并且标明它覆盖哪些服务端类型。Paper 目前没有可用的国内镜像，走官方直连反而更快。
        </NotReady>
      </div>
    </div>
  )
}
