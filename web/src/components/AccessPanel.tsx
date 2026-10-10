import { useCallback, useEffect, useState, type CSSProperties } from 'react'
import { api, type GlobalSettings, type MirrorOption } from '../api'
import { Icon, PaneHead } from './design'
import { SkeletonFields, Toggle, useConfirm } from './ui'

/**
 * 「访问安全」面板。
 *
 * 两条设计原则：
 *  1. **默认不挡人**：监听在本机时免密码，装完就能用。只在真的暴露到局域网
 *     且没设密码时才红着脸告警 —— 一上来就逼人设密码，多数人只会随便打一个然后忘掉。
 *  2. **敏感值只出现一次**：API 令牌的明文生成后立刻从内存里清掉，列表里只剩前缀。
 *     想再拿回来只能重新生成 —— 这是唯一诚实的做法。
 *
 * ⚠ 版式全部照设计稿 09 的 access pane：内容统一包在 `padding:14px 16px 18px` 里，
 *   小节用 PaneHead（像素字编号 + h3），**不要**再塞 SectionHead —— 那是整页级的大标题，
 *   放进 pane 会字级过大、零内边距贴面板边（2026-10-07 用户原话「乱得要死」就是它）。
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
      <div style={{ padding: '14px 16px 18px' }}>
        <div className="nbox d">读取设置失败：{err}</div>
      </div>
    )
  }
  if (!cfg) return <SkeletonFields rows={4} />

  // 只有「暴露在局域网 且 没开保护」才算危险。只开保护但没暴露，是用户自己的选择。
  const risky = cfg.runtime.exposed && !cfg.requireAuth
  const noGuard = !cfg.hasPassword && cfg.tokens.length === 0

  return (
    <div style={{ padding: '14px 16px 18px' }}>
      {risky && (
        <div className="nbox d">
          <div className="hd">
            <Icon name="ban" />
            现在局域网里任何人都能打开这个面板
          </div>
          守护进程监听在 <span className="mono">{cfg.runtime.bindingAddr}</span>
          （所有网卡），而访问保护没开。同一个 Wi-Fi 下的任何设备都能启停你的服务器、删你的存档。
          下面设一个密码并开启保护就行。
        </div>
      )}

      <div style={{ marginTop: risky ? 18 : 0 }}>
        <TogLine
          title="开启访问保护"
          desc="开启后，非本机访问需要密码或 API 令牌。"
          on={cfg.requireAuth}
          onChange={(v) => save({ requireAuth: v }, v ? '访问保护已开启' : '访问保护已关闭')}
          disabled={busy === 'save' || noGuard}
        />
        {noGuard && (
          <p style={{ fontSize: 12, color: 'var(--warn)', marginTop: 8 }}>
            还没有密码也没有令牌，开不了保护 —— 先把下面密码设上。
          </p>
        )}
        <TogLine
          title="本机访问免登录"
          desc="在这台机器上打开面板不用输密码。关掉的话自己每次也要登。"
          on={cfg.trustLoopback}
          onChange={(v) => save({ trustLoopback: v }, '已更新')}
          disabled={busy === 'save'}
          style={{ marginTop: 14 }}
        />
      </div>

      <div className="fgrid" style={{ marginTop: 18 }}>
        <div className="field col">
          <div>
            <div className="lb">登录有效期（小时）</div>
            <div className="hint">过期后需要重新登录</div>
          </div>
          <input
            className="inp"
            style={{ fontFamily: 'var(--f-term)', fontSize: 14 }}
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
        </div>

        <div className="field col">
          <div>
            <div className="lb">监听地址</div>
            <div className="hint">
              :23333 表示所有网卡都监听（手机能连）；127.0.0.1:23333 表示只本机能连
            </div>
          </div>
          <input
            className="inp"
            style={{ fontFamily: 'var(--f-term)', fontSize: 14 }}
            defaultValue={cfg.listenAddr}
            placeholder=":23333"
            onBlur={(e) => {
              const v = e.target.value.trim()
              if (v !== cfg.listenAddr) save({ listenAddr: v }, '监听地址已保存，重启守护进程后生效')
            }}
          />
        </div>
      </div>
      <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 10 }}>
        当前实际绑定{' '}
        <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-2)' }}>
          {cfg.runtime.bindingAddr}
        </span>
        {cfg.runtime.restartNeeded && (
          <span style={{ color: 'var(--warn)' }}> · 改过了，重启守护进程才生效</span>
        )}
      </p>

      {/* ── 02 面板密码 ── */}
      <div style={{ marginTop: 24, borderTop: '1px solid var(--line-1)', paddingTop: 18 }}>
        <PaneHead
          num="02"
          title="面板密码"
          note={cfg.hasPassword ? `已设置 · ${cfg.passwordSetAt ?? ''}` : '还没设置'}
        >
          {cfg.hasPassword && <span className="chip s g">已保护</span>}
        </PaneHead>

        <div className="fgrid" style={{ marginTop: 14 }}>
          {cfg.hasPassword && (
            <div className="field col">
              <div>
                <div className="lb">当前密码</div>
                <div className="hint">改密码必须先给旧的</div>
              </div>
              <input
                className="inp"
                type="password"
                value={oldPw}
                autoComplete="current-password"
                onChange={(e) => setOldPw(e.target.value)}
              />
            </div>
          )}
          <div className="field col">
            <div>
              <div className="lb">{cfg.hasPassword ? '新密码' : '设置密码'}</div>
              <div className="hint">至少 6 位</div>
            </div>
            <input
              className="inp"
              type="password"
              value={newPw}
              autoComplete="new-password"
              onChange={(e) => setNewPw(e.target.value)}
            />
          </div>
          <div className="field col">
            <div>
              <div className="lb">再输一遍</div>
            </div>
            <input
              className="inp"
              type="password"
              value={newPw2}
              autoComplete="new-password"
              onChange={(e) => setNewPw2(e.target.value)}
            />
          </div>
        </div>

        <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap', marginTop: 16 }}>
          <button
            type="button"
            className="btn btn-primary"
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
          <span style={{ fontSize: 12, color: 'var(--ink-3)' }}>
            {cfg.hasPassword ? '改完密码所有已登录的设备都会被踢下线' : '设完密码保护会自动开启'}
          </span>
        </div>
      </div>

      {/* ── 03 API 令牌 ── */}
      <div style={{ marginTop: 24, borderTop: '1px solid var(--line-1)', paddingTop: 18 }}>
        <PaneHead num="03" title="API 令牌" note="给脚本 / 外部工具用，不用把密码写进脚本里">
          <button type="button" className="btn btn-ghost sm" onClick={load}>
            <Icon name="refresh" />
            刷新
          </button>
        </PaneHead>

        {newToken && (
          <div
            className="nbox"
            style={{ marginTop: 14, borderLeftColor: 'var(--ok)', background: 'rgba(87,211,100,.06)' }}
          >
            <div className="hd" style={{ color: 'var(--ink-1)' }}>
              <Icon name="check" style={{ color: 'var(--ok)' }} />
              令牌已生成 —— 关掉这一页就再也看不到了
            </div>
            <div className="mono" style={{ color: 'var(--ok)', fontSize: 13 }}>
              {newToken}
            </div>
            <p style={{ fontSize: 12, color: 'var(--ink-2)', marginTop: 8 }}>
              用法：请求头带 <span className="mono" style={{ color: 'var(--cyan)' }}>Authorization: Bearer &lt;令牌&gt;</span>，
              或者给 EventSource 这类设不了头的场景走{' '}
              <span className="mono" style={{ color: 'var(--cyan)' }}>?token=&lt;令牌&gt;</span>。
              我们只存它的哈希，明文拿不回来。
            </p>
            <button
              type="button"
              className="btn sm"
              style={{ marginTop: 12 }}
              onClick={() => setNewToken('')}
            >
              我知道了，收起来
            </button>
          </div>
        )}

        {cfg.tokens.length > 0 ? (
          <div className="rlist" style={{ marginTop: 14 }}>
            {cfg.tokens.map((t) => (
              <div
                key={t.id}
                className="rrow"
                style={{ gridTemplateColumns: '32px minmax(0, 1fr) auto' }}
              >
                <div className="ricon">
                  <Icon name="crown" size={18} style={{ color: 'var(--cyan)' }} />
                </div>
                <div className="rmain">
                  <div className="rtitle">{t.name}</div>
                  <div className="rsub" style={{ fontSize: 13 }}>
                    {t.prefix}… · 建于 {t.createdAt}
                    {t.lastUsed ? ` · 最后用 ${t.lastUsed}` : ' · 还没用过'}
                  </div>
                </div>
                <div className="ract">
                  <button
                    type="button"
                    className="btn btn-ghost xs"
                    onClick={() => doRemoveToken(t.id, t.name)}
                    aria-label={`删除令牌 ${t.name}`}
                  >
                    <Icon name="trash" />
                  </button>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <p style={{ marginTop: 12, fontSize: 12.5, color: 'var(--ink-3)' }}>还没有令牌。</p>
        )}

        <div className="field" style={{ marginTop: 16 }}>
          <div>
            <div className="lb">新建令牌</div>
            <div className="hint">取个好认的名字，比如「手机上的脚本」</div>
          </div>
          <div style={{ display: 'flex', gap: 10, alignItems: 'center', flexWrap: 'wrap' }}>
            <input
              className="inp"
              style={{ flex: 1, minWidth: 200 }}
              value={tkName}
              maxLength={24}
              placeholder="用途备注"
              onChange={(e) => setTkName(e.target.value)}
            />
            <button type="button" className="btn" disabled={busy === 'tk'} onClick={doAddToken}>
              {busy === 'tk' ? '生成中…' : '生成令牌'}
            </button>
          </div>
        </div>
      </div>

      {dialog}
    </div>
  )
}

/** 开关行：文案在左（flex:1）、开关推到右。设计稿 09 的顺序就是这样，
    ToggleRow（开关在左）是旧版式，别在这页用。 */
function TogLine({
  title,
  desc,
  on,
  onChange,
  disabled,
  style,
}: {
  title: string
  desc: string
  on: boolean
  onChange: (v: boolean) => void
  disabled?: boolean
  style?: CSSProperties
}) {
  return (
    <div style={{ display: 'flex', alignItems: 'flex-start', gap: 16, ...style }}>
      <div style={{ flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 13, fontWeight: 600 }}>{title}</div>
        <p style={{ fontSize: 12, color: 'var(--ink-3)', marginTop: 4 }}>{desc}</p>
      </div>
      <Toggle on={on} onChange={onChange} label={title} disabled={disabled} />
    </div>
  )
}

/** 「下载源」分页。版式照设计稿 09 的 source pane。 */
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
      <div style={{ padding: '14px 16px 18px' }}>
        <div className="nbox d">读取失败：{err}</div>
      </div>
    )
  }
  if (!cfg) return <SkeletonFields rows={4} />

  return (
    <div style={{ padding: '14px 16px 18px' }}>
      <PaneHead num="03" title="下载源与市场" note="默认走哪个源、从哪搜模组" />

      <div style={{ maxWidth: 480, marginTop: 16 }}>
        <div className="field col">
          <div>
            <div className="lb">默认下载源</div>
            <div className="hint">服务端 jar 从哪下。只列出验证过能用的</div>
          </div>
          {/* 设计稿的 .sel 是演示用 span；真实实现用原生 select 套同一层皮
              （appearance:base-select 连弹层一起重做 —— 样式都在 design-overrides 的
              「原生下拉框」一节；⚠ 这里别再加内联 appearance:none，内联会压过 base-select） */}
          <div style={{ position: 'relative' }}>
            <select
              className="sel"
              style={{ width: '100%' }}
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
            {/* ⚠ 这里曾挂过一个手动的绝对定位 chev-r 箭头（第一轮 appearance:none 方案的产物）。
                base-select 的 ::picker-icon 会自己渲染箭头（紧跟文字，设计稿 .sel 的摆法），
                手动那个不删就是「文字后一个 + 最右一个」的双箭头（2026-10-09 截图抓到的）。 */}
          </div>
        </div>

        <div className="field col" style={{ marginTop: 16 }}>
          <div>
            <div className="lb">模组市场地址</div>
            <div className="hint">「插件与模组」页的在线搜索走这个地址</div>
          </div>
          <input
            className="inp"
            style={{ fontFamily: 'var(--f-term)', fontSize: 14 }}
            defaultValue={cfg.marketBase}
            placeholder="https://api.modrinth.com/v2"
            onBlur={async (e) => {
              const v = e.target.value.trim()
              if (v === cfg.marketBase) return
              try {
                setCfg(await api.saveSettings({ marketBase: v }))
                onToast('模组市场地址已保存')
              } catch (er) {
                onToast((er as Error).message)
                e.target.value = cfg.marketBase
              }
            }}
          />
        </div>
      </div>

      <p
        style={{
          fontSize: 12.5,
          lineHeight: 1.7,
          color: 'var(--ink-2)',
          marginTop: 12,
          maxWidth: '70ch',
        }}
      >
        默认是 Modrinth 官方 API —— 国内直连经常超时，换成能访问的镜像 / 反代就行（要填到{' '}
        <span style={{ fontFamily: 'var(--f-term)', fontSize: 14, color: 'var(--ink-1)' }}>/v2</span>{' '}
        这一层）。留空表示用官方地址。
      </p>

      <div className="rlist" style={{ marginTop: 16 }}>
        {opts.map((m) => (
          <div key={m.id} className="rrow" style={{ gridTemplateColumns: 'minmax(0, 1fr) auto' }}>
            <div className="rmain">
              <div className="rtitle">{m.label}</div>
              <div className="rsub">适用：{m.appliesTo.join(' / ')}</div>
            </div>
            <div className="ract">
              {m.appliesTo.length < 2 && <span className="chip s w">部分类型不可用</span>}
            </div>
          </div>
        ))}
      </div>

      <div className="nbox" style={{ marginTop: 16 }}>
        <div className="hd">
          <span className="badge ban">还没接</span>
          换镜像不是挂上就有用
        </div>
        没被镜像的路径会直接 404，选了反而更糟。所以只列出验证过能用的，并且标明它覆盖哪些服务端类型。
        Paper 目前没有可用的国内镜像，走官方直连反而更快。
      </div>
    </div>
  )
}
