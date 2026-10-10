/* ServerBox 像素重制稿 · 交互与动效
   纯 vanilla，无依赖。只做设计稿需要的东西：切屏、填充动画、实时感。 */
(function () {
  'use strict'

  var tabs = Array.prototype.slice.call(document.querySelectorAll('.tab'))
  var pages = Array.prototype.slice.call(document.querySelectorAll('.page'))
  var animOn = true

  function clampPct(v) { return Math.max(0, Math.min(100, Number(v) || 0)) }

  /* ── 占位屏：本次只出 4 屏，其余页签给一张「同一语言、待确认」的说明页 ── */
  var placeholder = null
  function ensurePlaceholder() {
    if (placeholder) return placeholder
    placeholder = document.createElement('section')
    placeholder.className = 'page'
    placeholder.dataset.page = '__placeholder'
    placeholder.innerHTML =
      '<div class="panel"><i class="corner"></i>' +
      '<div style="padding:40px 28px;text-align:left">' +
      '<div class="eyebrow" style="color:var(--cyan-d)">Coming in v2</div>' +
      '<h2 style="font-size:20px;font-weight:700;margin:10px 0 8px">这一屏还没画</h2>' +
      '<p style="max-width:58ch;font-size:13.5px;line-height:1.7;color:var(--ink-2)">' +
      '本次设计稿聚焦 <b style="color:var(--ink-1)">概览 / 控制台 / 玩家 / 配置</b> 四屏，' +
      '用来先敲定整体设计语言（色板 · 字阶 · 像素浮雕 · 动效）。' +
      '确认风格之后，插件模组 / 备份还原 / 计划任务 / 设置 会沿用同一套 token 铺开，' +
      '不会出现两套视觉。</p>' +
      '<div style="display:flex;gap:8px;margin-top:18px;flex-wrap:wrap">' +
      '<button class="btn btn-primary" id="phBack" type="button">回到概览</button>' +
      '<span class="badge wl" style="align-self:center">同一 token 体系</span>' +
      '</div></div></div>'
    document.querySelector('.content').appendChild(placeholder)
    placeholder.querySelector('#phBack').addEventListener('click', function () { show('overview') })
    pages.push(placeholder)
    return placeholder
  }

  function replay(root) {
    if (!animOn || !root) return
    Array.prototype.forEach.call(root.querySelectorAll('.reveal'), function (el) {
      el.style.animation = 'none'
      void el.offsetWidth
      el.style.animation = ''
    })
  }

  function show(view) {
    var target = null
    tabs.forEach(function (t) { t.classList.toggle('on', t.dataset.view === view) })
    pages.forEach(function (p) {
      var on = p.dataset.page === view
      p.classList.toggle('on', on)
      if (on) target = p
    })
    if (!target) target = ensurePlaceholder(), placeholder.classList.add('on')
    replay(target)
  }

  tabs.forEach(function (t) {
    t.addEventListener('click', function () { show(t.dataset.view) })
  })

  /* ── 方块刻度条：逐格点亮 ── */
  Array.prototype.forEach.call(document.querySelectorAll('.meter'), function (m) {
    var pct = clampPct(m.dataset.pct)
    var tone = m.dataset.tone || 'on'
    var count = 10
    var filled = Math.round((pct / 100) * count)
    var bars = []
    for (var i = 0; i < count; i++) { var s = document.createElement('i'); m.appendChild(s); bars.push(s) }
    bars.forEach(function (b, k) {
      if (k < filled) setTimeout(function () { b.classList.add('f', tone) }, 140 + k * 46)
    })
  })

  /* ── 数字滚动 ── */
  Array.prototype.forEach.call(document.querySelectorAll('[data-count]'), function (el) {
    var target = Number(el.dataset.count)
    if (!animOn) { el.textContent = String(target); return }
    var dur = 880, t0 = performance.now()
    function step(t) {
      var k = Math.min(1, (t - t0) / dur)
      var e = 1 - Math.pow(1 - k, 3)
      el.textContent = String(Math.round(target * e))
      if (k < 1) requestAnimationFrame(step)
    }
    requestAnimationFrame(step)
  })

  /* ── 爱心依次弹出 ── */
  Array.prototype.forEach.call(document.querySelectorAll('.hearts'), function (box, bi) {
    Array.prototype.forEach.call(box.querySelectorAll('.heart'), function (h, i) {
      h.style.animationDelay = (0.35 + bi * 0.1 + i * 0.07).toFixed(2) + 's'
    })
  })

  /* ── 底部 HUD：活跃度经验条 + TPS 实时抖动 ── */
  var xp = document.getElementById('hudXp')
  var lv = document.getElementById('hudLv')
  if (xp) setTimeout(function () { xp.style.width = '64%' }, 320)
  setInterval(function () {
    if (!animOn) return
    if (lv) lv.textContent = (19.8 + Math.random() * 0.2).toFixed(1)
    if (xp && xp.style.width) xp.style.width = (58 + Math.random() * 12).toFixed(0) + '%'
  }, 2400)

  /* ── 日志实时滚动：给「面板活着」的观感 ── */
  var pool = [
    ['INFO', 'Tick 450 | uptime 5403s | heap 162MB | players 3'],
    ['INFO', 'Saving chunks for level \'world\'/minecraft:overworld'],
    ['INFO', 'Steve joined the game'],
    ['WARN', 'Can\'t keep up! Running 2050ms or 41 ticks behind'],
    ['INFO', 'Savinaa left the game'],
    ['INFO', 'Tick 500 | uptime 6003s | heap 168MB | players 2']
  ]
  var pi = 0
  setInterval(function () {
    if (!animOn) return
    var bodies = document.querySelectorAll('.page.on .con-body')
    if (!bodies.length) return
    var line = pool[pi++ % pool.length]
    var now = new Date()
    var hh = String(now.getHours()).padStart(2, '0')
    var mm = String(now.getMinutes()).padStart(2, '0')
    var ss = String(now.getSeconds()).padStart(2, '0')
    var color = line[0] === 'WARN' ? '#f2c14e' : '#4aedd9'
    Array.prototype.forEach.call(bodies, function (body) {
      var row = document.createElement('div')
      row.className = 'log-line fresh'
      row.innerHTML = '<span class="t">' + hh + ':' + mm + ':' + ss + '</span>' +
        '<span class="lv" style="color:' + color + '">' + line[0] + '</span>' +
        '<span class="m">' + line[1] + '</span>'
      body.appendChild(row)
      while (body.children.length > 60) body.removeChild(body.firstChild)
      body.scrollTop = body.scrollHeight
    })
  }, 2600)

  /* ── 交互：筛选芯片 / 开关 / 配置分组 ── */
  Array.prototype.forEach.call(document.querySelectorAll('.filters'), function (g) {
    g.addEventListener('click', function (e) {
      var b = e.target.closest('.chipf')
      if (!b) return
      Array.prototype.forEach.call(g.querySelectorAll('.chipf'), function (x) { x.classList.remove('on') })
      b.classList.add('on')
    })
  })
  Array.prototype.forEach.call(document.querySelectorAll('.tog'), function (t) {
    t.addEventListener('click', function () {
      t.classList.toggle('on')
      t.setAttribute('aria-checked', t.classList.contains('on') ? 'true' : 'false')
    })
  })
  var cfgNav = document.getElementById('cfgNav')
  if (cfgNav) {
    cfgNav.addEventListener('click', function (e) {
      var b = e.target.closest('button')
      if (!b) return
      Array.prototype.forEach.call(cfgNav.querySelectorAll('button'), function (x) { x.classList.remove('on') })
      b.classList.add('on')
    })
  }

  /* ── 动效总开关 ── */
  var toggle = document.getElementById('animToggle')
  if (toggle) {
    toggle.addEventListener('click', function () {
      animOn = !animOn
      document.body.classList.toggle('no-anim', !animOn)
      toggle.textContent = animOn ? '暂停动效' : '播放动效'
    })
  }

  /* ── 移动端抽屉 ── */
  var drawerBtn = document.getElementById('drawerBtn')
  if (drawerBtn) {
    drawerBtn.addEventListener('click', function () { document.body.classList.toggle('drawer') })
    document.addEventListener('click', function (e) {
      if (!document.body.classList.contains('drawer')) return
      if (e.target.closest('.side') || e.target.closest('#drawerBtn')) return
      document.body.classList.remove('drawer')
    })
  }
})()