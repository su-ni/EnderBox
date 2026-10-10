import { useState } from 'react'

/**
 * 插件与模组页 —— 结构照设计稿 docs/design/pixel-refresh/index.html 的
 * <section data-page="plugins">，**原样搬过来**（class 名、层级、图标 sprite 都不动）。
 *
 * ⚠ 这一版的数据仍是设计稿里的假数据（Sodium / Create / Jade / …）。
 *    目的和前两页一样：先把视觉做到与设计稿逐像素一致，下一步再换成真实接口。
 *
 * ⚠ 图标走 index.html 里的精灵：<use href="#i-xxx"/>。不要改回自己画的图标组件。
 */
export function PluginsPage() {
  /* 开关状态：设计稿里 6 个 .tog 的初始态是 开/开/开/开/关/开。
     ⚠ 只有 .tog 受控，其余「原样搬」的 DOM 一个字都没动。 */
  const [togs, setTogs] = useState([true, true, true, true, false, true])
  const toggleTog = (i: number) =>
    setTogs((prev) => prev.map((v, k) => (k === i ? !v : v)))

  return (
    <section className="page on" data-page="plugins">
              <div className="sec-head reveal" style={{ '--rd': '.08s' } as React.CSSProperties}>
                <span className="idx">03</span>
                <h2>插件与模组</h2>
                <span className="sub">Paper 这类读 plugins/，Forge / Fabric 这类读 mods/ —— 目录按服务端类型自动切换，不让你自己挑</span>
                <div className="spacer"></div>
                <button className="btn btn-primary sm" type="button"><svg className="pic"><use href="#i-plus"/></svg>安装</button>
              </div>

          
              <div className="banner-danger reveal" style={{ '--rd': '.1s' } as React.CSSProperties}>
                <svg className="pic" style={{ color: 'var(--warn)', width: '20px', height: '20px' }}><use href="#i-info"/></svg>
                <div className="t">
                  <h3>有 2 个模组缺少必需前置</h3>
                  <p>缺必需前置的模组在服务端加载阶段就会被丢掉，压根不会进游戏。点「装前置」会先把缺的那个按你的 MC 版本挑好版本再装 —— 不会替你默认装最新版。</p>
                </div>
                <button className="btn btn-primary sm" type="button"><svg className="pic"><use href="#i-dl"/></svg>装前置（2）</button>
              </div>

              <div className="panel reveal" style={{ '--rd': '.14s' } as React.CSSProperties}>
                <i className="corner"></i>
                <div className="con-head tex-stone">
                  <svg className="pic" style={{ color: 'var(--cyan)' }}><use href="#i-table"/></svg>
                  <h2>已安装</h2>
                  <span style={{ fontSize: '12.5px', color: 'var(--ink-3)' }}>12 个 · 启用 9 · 禁用 1</span>
                  <input className="inp" style={{ maxWidth: '240px', marginLeft: '8px' }} placeholder="搜名字 / 文件名 / modId" />
                  <div className="spacer"></div>
                  <div className="filters">
                    <button className="chipf on" type="button">全部<span className="n">12</span></button>
                    <button className="chipf" type="button">已启用<span className="n">9</span></button>
                    <button className="chipf" type="button">已禁用<span className="n">1</span></button>
                    <button className="chipf" type="button">缺前置<span className="n">2</span></button>
                    <button className="chipf" type="button">读不出信息<span className="n">1</span></button>
                  </div>
                </div>

                <div className="rlist">
                  <div className="rrow">
                    <div className="ricon"><svg className="pic" style={{ width: '18px', height: '18px', color: 'var(--cyan)' }}><use href="#i-table"/></svg></div>
                    <div className="rmain">
                      <div className="rtitle">Sodium <span className="badge">FABRIC</span><span className="badge">1.21.4</span></div>
                      <div className="rsub">大幅优化渲染，几乎不掉画质 · jellysquid3</div>
                    </div>
                    <div className="rmeta">sodium-fabric-1.21.4.jar · 1.2 MB</div>
                    <div className="ract">
                      <span className={'tog' + (togs[0] ? ' on' : '')} role="switch" aria-checked={togs[0]} onClick={() => toggleTog(0)}><span className="knob"></span></span>
                      <button className="btn btn-ghost xs" type="button" aria-label="删除"><svg className="pic"><use href="#i-trash"/></svg></button>
                    </div>
                  </div>

                  <div className="rrow dep">
                    <div className="ricon"><svg className="pic" style={{ width: '18px', height: '18px', color: 'var(--copper)' }}><use href="#i-table"/></svg></div>
                    <div className="rmain">
                      <div className="rtitle">Create <span className="badge">FORGE</span><span className="badge">1.20.1</span><span className="badge ban">缺前置</span></div>
                      <div className="rsub">需要 <b style={{ color: 'var(--warn)' }}>flywheel</b> 才能加载 —— 现在它在服务端启动阶段就被丢掉了</div>
                    </div>
                    <div className="rmeta">create-1.20.1-6.0.4.jar · 14.8 MB</div>
                    <div className="ract">
                      <button className="btn btn-primary xs" type="button"><svg className="pic"><use href="#i-dl"/></svg>装前置</button>
                      <span className={'tog' + (togs[1] ? ' on' : '')} role="switch" aria-checked={togs[1]} onClick={() => toggleTog(1)}><span className="knob"></span></span>
                      <button className="btn btn-ghost xs" type="button" aria-label="删除"><svg className="pic"><use href="#i-trash"/></svg></button>
                    </div>
                  </div>

                  <div className="rrow dep">
                    <div className="ricon"><svg className="pic" style={{ width: '18px', height: '18px', color: 'var(--copper)' }}><use href="#i-table"/></svg></div>
                    <div className="rmain">
                      <div className="rtitle">Jade <span className="badge">FORGE</span><span className="badge">1.20.1</span><span className="badge ban">缺前置</span></div>
                      <div className="rsub">需要 <b style={{ color: 'var(--warn)' }}>iceberg</b></div>
                    </div>
                    <div className="rmeta">jade-1.20.1-11.9.2.jar · 402 KB</div>
                    <div className="ract">
                      <button className="btn btn-primary xs" type="button"><svg className="pic"><use href="#i-dl"/></svg>装前置</button>
                      <span className={'tog' + (togs[2] ? ' on' : '')} role="switch" aria-checked={togs[2]} onClick={() => toggleTog(2)}><span className="knob"></span></span>
                      <button className="btn btn-ghost xs" type="button" aria-label="删除"><svg className="pic"><use href="#i-trash"/></svg></button>
                    </div>
                  </div>

                  <div className="rrow">
                    <div className="ricon"><svg className="pic" style={{ width: '18px', height: '18px', color: 'var(--emerald)' }}><use href="#i-cmd"/></svg></div>
                    <div className="rmain">
                      <div className="rtitle">EnderBoxCompanion <span className="badge">BUKKIT</span><span className="badge op">v0.2.0</span></div>
                      <div className="rsub">面板的实时数据由它提供 · 我们自己写的插件</div>
                    </div>
                    <div className="rmeta">plugins/EnderBoxCompanion.jar · 46 KB</div>
                    <div className="ract">
                      <span className={'tog' + (togs[3] ? ' on' : '')} role="switch" aria-checked={togs[3]} onClick={() => toggleTog(3)}><span className="knob"></span></span>
                      <button className="btn btn-ghost xs" type="button" aria-label="删除"><svg className="pic"><use href="#i-trash"/></svg></button>
                    </div>
                  </div>

                  <div className="rrow off">
                    <div className="ricon"><svg className="pic" style={{ width: '18px', height: '18px' }}><use href="#i-table"/></svg></div>
                    <div className="rmain">
                      <div className="rtitle">LegacyGuard <span className="badge">BUKKIT</span><span className="badge">已禁用</span></div>
                      <div className="rsub">上次启动时它报了 Unsupported API version 1.13 —— 先禁用，别让它拖住整台服</div>
                    </div>
                    <div className="rmeta">LegacyGuard-1.13.jar · 88 KB</div>
                    <div className="ract">
                      <span className={'tog' + (togs[4] ? ' on' : '')} role="switch" aria-checked={togs[4]} onClick={() => toggleTog(4)}><span className="knob"></span></span>
                      <button className="btn btn-ghost xs" type="button" aria-label="删除"><svg className="pic"><use href="#i-trash"/></svg></button>
                    </div>
                  </div>

                  <div className="rrow">
                    <div className="ricon"><svg className="pic" style={{ width: '18px', height: '18px', color: 'var(--ink-3)' }}><use href="#i-ban"/></svg></div>
                    <div className="rmain">
                      <div className="rtitle">mystery-mod-7 <span className="badge">读不出信息</span></div>
                      <div className="rsub">jar 里没有认识的元数据文件 —— 名字就是文件名，其余留空，不瞎猜</div>
                    </div>
                    <div className="rmeta">mystery-mod-7.jar · 2.1 MB</div>
                    <div className="ract">
                      <span className={'tog' + (togs[5] ? ' on' : '')} role="switch" aria-checked={togs[5]} onClick={() => toggleTog(5)}><span className="knob"></span></span>
                      <button className="btn btn-ghost xs" type="button" aria-label="删除"><svg className="pic"><use href="#i-trash"/></svg></button>
                    </div>
                  </div>
                </div>

                <div className="con-foot">
                  <svg className="pic" style={{ color: 'var(--ink-3)' }}><use href="#i-info"/></svg>
                  开关改的是文件后缀（<code>.jar</code> ↔ <code>.jar.disabled</code>），下次启动才生效；删除是不可逆的，唯一的后悔药在备份页。
                </div>
              </div>
            </section>

        
  )
}
