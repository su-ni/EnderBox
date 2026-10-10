/* ServerBox 像素重制稿 v2 · 交互与动效
   纯 vanilla，无依赖。切屏、火花线、日志筛选、玩家排序/展开/批量、配置切组、实时感。 */
(function () {
  'use strict'

  var tabs = Array.prototype.slice.call(document.querySelectorAll('.tab'))
  var pages = Array.prototype.slice.call(document.querySelectorAll('.page'))
  var animOn = true
  var rndSeed = 20261007
  function rnd() { rndSeed = (rndSeed * 1103515245 + 12345) & 0x7fffffff; return rndSeed / 0x7fffffff }
  function clamp(v, a, b) { return Math.max(a, Math.min(b, v)) }

  /* ── 未画的页签：同一语言、待确认说明页 ── */
  var placeholder = null
  function ensurePlaceholder() {
    if (placeholder) return placeholder
    placeholder = document.createElement('section')
    placeholder.className = 'page'
    placeholder.dataset.page = '__placeholder'
    placeholder.innerHTML =
      '<div class="panel"><i class="corner"></i>' +
      '<div style="padding:40px 28px;text-align:left">' +
      '<div class="eyebrow" style="color:var(--cyan-d)">Coming next</div>' +
      '<h2 style="font-size:20px;font-weight:700;margin:10px 0 8px">这一屏还没画</h2>' +
      '<p style="max-width:58ch;font-size:13.5px;line-height:1.7;color:var(--ink-2)">' +
      '设计稿已覆盖面板的全部九屏：概览 / 玩家 / 配置 / 插件与模组 / 整合包 / ' +
      '文件 / 备份与还原 / 计划任务 / 设置，共用同一套 token。' +
      '左侧「设计参考」里的控制台整页与异常状态只作实现对照，不是面板页签。' +
      '</p><div style="display:flex;gap:8px;margin-top:18px;flex-wrap:wrap">' +
      '<button class="btn btn-primary" id="phBack" type="button">回到概览</button>' +
      '<a class="btn" href="spec.html" target="_blank">看设计规范</a></div></div></div>'
    document.querySelector('.content').appendChild(placeholder)
    placeholder.querySelector('#phBack').addEventListener('click', function () { show('overview') })
    pages.push(placeholder)
    return placeholder
  }

  function replay(root) {
    if (!animOn || !root) return
    Array.prototype.forEach.call(root.querySelectorAll('.reveal'), function (el) {
      el.style.animation = 'none'; void el.offsetWidth; el.style.animation = ''
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
    if (!target) { target = ensurePlaceholder(); placeholder.classList.add('on') }
    replay(target)
    var m = document.querySelector('.main')
    if (m) m.scrollTop = 0
  }
  tabs.forEach(function (t) { t.addEventListener('click', function () { show(t.dataset.view) }) })

  /* ── 方块刻度条：逐格点亮 ── */
  function buildMeters(scope) {
    Array.prototype.forEach.call((scope || document).querySelectorAll('.meter:not([data-built])'), function (m) {
      m.dataset.built = '1'
      var pct = clamp(Number(m.dataset.pct) || 0, 0, 100)
      var tone = m.dataset.tone || 'on'
      var filled = Math.round((pct / 100) * 10)
      for (var i = 0; i < 10; i++) {
        var b = document.createElement('i'); m.appendChild(b)
        if (i < filled) (function (b, k) { setTimeout(function () { b.classList.add('f', tone) }, 140 + k * 46) })(b, i)
      }
    })
  }
  buildMeters(document)

  /* ── 火花线：用伪随机走一条像资源曲线的折线 + 峰值虚线 ── */
  function drawSpark(svg) {
    if (svg.dataset.drawn) return
    svg.dataset.drawn = '1'
    var base = Number(svg.dataset.seed) || 40
    var n = 40, W = 120, H = 30, pts = [], v = base
    for (var i = 0; i < n; i++) {
      v += (rnd() - 0.46) * 14
      v = clamp(v, base * 0.45 + 4, base * 1.45 + 8)
      pts.push([i / (n - 1) * W, H - 2 - clamp(v / (base * 1.55 + 12), 0, 1) * (H - 5)])
    }
    var d = pts.map(function (p, i) { return (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1) }).join(' ')
    var area = d + ' L' + W + ' ' + H + ' L0 ' + H + ' Z'
    var peakY = Math.min.apply(null, pts.map(function (p) { return p[1] }))
    var peak = pts.filter(function (p) { return p[1] === peakY })[0]
    var NS = 'http://www.w3.org/2000/svg'
    function el(tag, attrs, cls) {
      var e = document.createElementNS(NS, tag)
      for (var k in attrs) e.setAttribute(k, attrs[k])
      if (cls) e.setAttribute('class', cls)
      return e
    }
    svg.appendChild(el('path', { d: area }, 'area'))
    svg.appendChild(el('line', { x1: 0, y1: peakY, x2: W, y2: peakY }, 'peakline'))
    svg.appendChild(el('path', { d: d }, 'line'))
    svg.appendChild(el('rect', { x: peak[0] - 1.5, y: peak[1] - 1.5, width: 3, height: 3 }, 'pk'))
  }
  Array.prototype.forEach.call(document.querySelectorAll('.spark'), drawSpark)

  /* ── 数字滚动 ── */
  function countUp(el) {
    var target = Number(el.dataset.count)
    if (!animOn) { el.textContent = String(target); return }
    var dur = 880, t0 = performance.now()
    function step(t) {
      var k = Math.min(1, (t - t0) / dur)
      el.textContent = String(Math.round(target * (1 - Math.pow(1 - k, 3))))
      if (k < 1) requestAnimationFrame(step)
    }
    requestAnimationFrame(step)
  }
  Array.prototype.forEach.call(document.querySelectorAll('[data-count]'), countUp)

  /* ── 爱心依次弹出 ── */
  Array.prototype.forEach.call(document.querySelectorAll('.hearts'), function (box, bi) {
    Array.prototype.forEach.call(box.querySelectorAll('.heart'), function (h, i) {
      h.style.animationDelay = (0.35 + bi * 0.1 + i * 0.07).toFixed(2) + 's'
    })
  })

  /* ── 底部 HUD 经验条 + 数字跳变（模拟采集刷新）── */
  var xp = document.getElementById('hudXp'), lv = document.getElementById('hudLv')
  if (xp) setTimeout(function () { xp.style.width = '64%' }, 320)
  setInterval(function () {
    if (!animOn) return
    if (lv) lv.textContent = (19.8 + rnd() * 0.2).toFixed(1)
    if (xp && xp.style.width) xp.style.width = (58 + rnd() * 12).toFixed(0) + '%'
    // 指标大数轻微跳动，并给读数卡一个「刚刷新」的闪光
    Array.prototype.forEach.call(document.querySelectorAll('.metric[data-jitter]'), function (card) {
      var j = Number(card.dataset.jitter); if (!j) return
      var b = card.querySelector('.val b'); if (!b) return
      b.textContent = String(Math.max(0, Math.round(j * (0.93 + rnd() * 0.14))))
      card.classList.remove('bump'); void card.offsetWidth; card.classList.add('bump')
    })
  }, 3000)

  /* ── 日志实时滚动 ── */
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
    var now = new Date(), p2 = function (n) { return String(n).padStart(2, '0') }
    var ts = p2(now.getHours()) + ':' + p2(now.getMinutes()) + ':' + p2(now.getSeconds())
    var color = line[0] === 'WARN' ? '#f2c14e' : '#4aedd9'
    Array.prototype.forEach.call(bodies, function (body) {
      var row = document.createElement('div')
      row.className = 'log-line fresh'
      row.dataset.lv = line[0]
      row.innerHTML = '<span class="t">' + ts + '</span><span class="lv" style="color:' + color + '">' + line[0] + '</span><span class="m">' + line[1] + '</span>'
      body.appendChild(row)
      while (body.children.length > 80) body.removeChild(body.firstChild)
      body.scrollTop = body.scrollHeight
    })
  }, 2600)

  /* ── 控制台级别筛选 ── */
  var logFilter = document.getElementById('logFilter')
  var conBody = document.getElementById('conBody')
  if (logFilter && conBody) {
    logFilter.addEventListener('click', function (e) {
      var b = e.target.closest('.chipf'); if (!b) return
      Array.prototype.forEach.call(logFilter.querySelectorAll('.chipf'), function (x) { x.classList.remove('on') })
      b.classList.add('on')
      var f = b.dataset.f
      conBody.classList.toggle('hide-info', f === 'warn')
      conBody.classList.toggle('only-warn', f === 'warn')
      conBody.classList.toggle('only-err', f === 'err')
    })
    // 计数按 DOM 实际算，别写死 —— 日志是活的，写死的数字迟早对不上
    function recount() {
      var n = { all: 0, warn: 0, err: 0 }
      Array.prototype.forEach.call(conBody.querySelectorAll('.log-line'), function (l) {
        // 折行/其明细/启动横幅都不算「一条带级别的日志」，不进计数
        if (l.classList.contains('fold') || l.classList.contains('fold-detail') || l.classList.contains('art')) return
        var lv = l.dataset.lv; if (!lv) return
        n.all++
        if (lv === 'WARN') n.warn++
        if (lv === 'ERROR') n.err++
      })
      var set = function (f, v) {
        var c = logFilter.querySelector('.chipf[data-f="' + f + '"] .n')
        if (c) c.textContent = String(v)
      }
      set('all', n.all); set('warn', n.warn); set('err', n.err)
    }
    recount()
    // 报错聚合：点「展开」把同类明细插到折行下面，再点收起
    conBody.addEventListener('click', function (e) {
      var exp = e.target.closest('.log-line.fold .exp')
      if (!exp) return
      var fold = exp.closest('.log-line.fold')
      var open = fold.classList.toggle('open')
      exp.textContent = open ? '收起 ▴' : '展开 ▾'
      if (open) {
        var frag = document.createDocumentFragment()
        for (var i = 0; i < 4; i++) {
          var d = document.createElement('div')
          d.className = 'log-line fold-detail'
          d.dataset.lv = 'ERROR'
          d.innerHTML = '<span class="t">03:16:' + (12 + i * 7) + '</span>' +
            '<span class="lv" style="color:#ff6b6b">ERROR</span>' +
            '<span class="m">Failed to load plugin \'LegacyGuard\': Unsupported API version 1.13</span>'
          frag.appendChild(d)
        }
        fold.parentNode.insertBefore(frag, fold.nextSibling)
      } else {
        var n2 = fold.nextElementSibling
        while (n2 && n2.classList.contains('fold-detail')) { var t2 = n2.nextElementSibling; n2.remove(); n2 = t2 }
      }
    })
  }

  /* ── 玩家：排序 / 展开详情 / 批量选择 ── */
  var plist = document.getElementById('plist')
  if (plist) {
    var rows = Array.prototype.slice.call(plist.querySelectorAll('.prow'))
    // 勾选 → 批量条
    var batchbar = document.getElementById('batchbar')
    var batchN = document.getElementById('batchN')
    var chkAll = document.getElementById('chkAll')
    function syncBatch() {
      var n = plist.querySelectorAll('.prow .chk.on').length
      if (batchN) batchN.textContent = String(n)
      if (batchbar) batchbar.classList.toggle('show', n > 0)
      if (chkAll) chkAll.classList.toggle('on', n > 0 && n === rows.length)
    }
    plist.addEventListener('click', function (e) {
      var chk = e.target.closest('.chk')
      if (chk) { chk.classList.toggle('on'); e.stopPropagation(); syncBatch(); return }
      var act = e.target.closest('.pact button')
      if (act) { e.stopPropagation(); return }
      // 点行本身 → 展开/收起详情
      var row = e.target.closest('.prow'); if (!row) return
      var det = row.nextElementSibling
      if (det && det.classList.contains('pdetail')) det.classList.toggle('open')
    })
    if (chkAll) chkAll.addEventListener('click', function () {
      var on = !(plist.querySelectorAll('.prow .chk.on').length === rows.length)
      rows.forEach(function (r) { r.querySelector('.chk').classList.toggle('on', on) })
      syncBatch()
    })
    var bclr = document.getElementById('batchClear')
    if (bclr) bclr.addEventListener('click', function () {
      plist.querySelectorAll('.chk').forEach(function (c) { c.classList.remove('on') }); syncBatch()
    })
    // 排序
    Array.prototype.forEach.call(document.querySelectorAll('.phead button[data-sort]'), function (btn) {
      btn.addEventListener('click', function () {
        var key = btn.dataset.sort
        var dir = btn.classList.contains('asc') ? 'desc' : 'asc'
        Array.prototype.forEach.call(document.querySelectorAll('.phead button[data-sort]'), function (b) {
          b.classList.remove('asc', 'desc')
        })
        btn.classList.add(dir)
        var pairs = rows.map(function (r) { return { row: r, det: r.nextElementSibling } })
        pairs.sort(function (a, b) {
          var va = a.row.dataset[key], vb = b.row.dataset[key]
          var na = parseFloat(va), nb = parseFloat(vb)
          var cmp = (!isNaN(na) && !isNaN(nb)) ? na - nb : String(va).localeCompare(String(vb), 'zh')
          return dir === 'asc' ? cmp : -cmp
        })
        pairs.forEach(function (p) { plist.appendChild(p.row); if (p.det) plist.appendChild(p.det) })
      })
    })
  }

  /* ── 配置：切组联动 + 上一组/下一组 ── */
  var cfgNav = document.getElementById('cfgNav')
  var ORDER = ['inst', 'net', 'play', 'world', 'pm', 'remote']
  var META = {
    inst: ['实例设置', '6 项 · 我们自己管的配置，改完立刻生效'],
    net: ['网络与身份', '5 项 · 对应 server.properties 里的端口/验证/MOTD'],
    play: ['玩法', '7 项 · 游戏模式、难度、PvP 这类规则'],
    world: ['世界生成', '5 项 · 种子、视距、结构生成'],
    pm: ['玩家管理', '3 项 · 白名单、挂机踢出、出生点保护'],
    remote: ['远程控制', '3 项 · RCON 相关，日常不用开']
  }
  // ⚠ 作用域必须限定在自己的 cfg-wrap：设置页也用 .cfg-pane，
  //   全局查会把那边的面板一起关掉（切完设置页再回配置页，右栏会是空的）。
  var panesRoot = cfgNav ? cfgNav.closest('.cfg-wrap') : document
  function pickPane(key) {
    var i = ORDER.indexOf(key); if (i < 0) return
    Array.prototype.forEach.call(document.querySelectorAll('.cfg-nav button'), function (b) {
      b.classList.toggle('on', b.dataset.pane === key)
    })
    Array.prototype.forEach.call(panesRoot.querySelectorAll('.cfg-pane'), function (p) {
      p.classList.toggle('on', p.dataset.pane === key)
    })
    var t = document.getElementById('cfgTitle'), s = document.getElementById('cfgSub'), idx = document.getElementById('cfgIdx')
    if (t) t.textContent = META[key][0]
    if (s) s.textContent = META[key][1]
    if (idx) idx.textContent = String(i + 1)
  }
  if (cfgNav) {
    cfgNav.addEventListener('click', function (e) {
      var b = e.target.closest('button'); if (!b) return
      pickPane(b.dataset.pane)
    })
  }
  var curIdx = function () {
    var on = document.querySelector('.cfg-nav button.on')
    return on ? Math.max(0, ORDER.indexOf(on.dataset.pane)) : 0
  }
  var prev = document.getElementById('cfgPrev'), next = document.getElementById('cfgNext')
  if (prev) prev.addEventListener('click', function () { pickPane(ORDER[(curIdx() + ORDER.length - 1) % ORDER.length]) })
  if (next) next.addEventListener('click', function () { pickPane(ORDER[(curIdx() + 1) % ORDER.length]) })

  /* ── 通用：筛选芯片 / 开关 ── */
  Array.prototype.forEach.call(document.querySelectorAll('.filters'), function (g) {
    g.addEventListener('click', function (e) {
      var b = e.target.closest('.chipf'); if (!b) return
      // multi 组允许一个都不选（备份的「触发方式」两种能一起开）
      if (g.hasAttribute('data-multi')) { b.classList.toggle('on'); return }
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


  /* ── 设置：分组导航（同款视觉，作用域限定在自己的 cfg-wrap 内）── */
  var setNav = document.getElementById('setNav')
  var SET_META = {
    access: ['访问安全', '谁可以打开这个面板'],
    java: ['Java 运行时', '检测到几个、各世界该用哪个'],
    source: ['下载源与市场', '默认走哪个源、从哪搜模组'],
    daemon: ['守护与启动', '这一页只管全局行为'],
    about: ['关于', '运行时信息直接来自守护进程']
  }
  if (setNav) {
    var setRoot = setNav.closest('.cfg-wrap') || document
    setNav.addEventListener('click', function (e) {
      var b = e.target.closest('button[data-pane]'); if (!b) return
      var key = b.dataset.pane
      Array.prototype.forEach.call(setNav.querySelectorAll('button'), function (x) {
        x.classList.toggle('on', x.dataset.pane === key)
      })
      Array.prototype.forEach.call(setRoot.querySelectorAll('.cfg-pane'), function (p) {
        p.classList.toggle('on', p.dataset.pane === key)
      })
      var t = document.getElementById('setTitle'), sb = document.getElementById('setSub')
      if (t) t.textContent = SET_META[key][0]
      if (sb) sb.textContent = SET_META[key][1]
    })
  }

  /* ── 动效总开关 ── */
  var toggle = document.getElementById('animToggle')
  if (toggle) toggle.addEventListener('click', function () {
    animOn = !animOn
    document.body.classList.toggle('no-anim', !animOn)
    toggle.textContent = animOn ? '暂停动效' : '播放动效'
  })

  /* ── 移动端抽屉 ── */
  var drawerBtn = document.getElementById('drawerBtn')
  if (drawerBtn) {
    drawerBtn.addEventListener('click', function (e) { e.stopPropagation(); document.body.classList.toggle('drawer') })
    document.addEventListener('click', function (e) {
      if (!document.body.classList.contains('drawer')) return
      if (e.target.closest('.side') || e.target.closest('#drawerBtn')) return
      document.body.classList.remove('drawer')
    })
  }
})()