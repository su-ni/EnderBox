# -*- coding: utf-8 -*-
"""给新 4 屏接交互：设置分组导航、chipf 多选、更新占位页文案。
顺带修一处因插入新页而引入的 bug：全局 querySelectorAll('.cfg-pane')
会让「配置」和「设置」两组面板互相清空 —— 两边都改成按各自容器作用域查询。"""
import io, os, sys

base = os.path.dirname(os.path.abspath(__file__))
p = os.path.join(base, 'app.js')
s = io.open(p, encoding='utf-8').read()
orig = s

# ── 1) 配置页：把 pane 查询限定在自己的 cfg-wrap 内 ──
old_cfg = """  function pickPane(key) {
    var i = ORDER.indexOf(key); if (i < 0) return
    Array.prototype.forEach.call(document.querySelectorAll('.cfg-nav button'), function (b) {
      b.classList.toggle('on', b.dataset.pane === key)
    })
    Array.prototype.forEach.call(document.querySelectorAll('.cfg-pane'), function (p) {
      p.classList.toggle('on', p.dataset.pane === key)
    })"""
new_cfg = """  // ⚠ 作用域必须限定在自己的 cfg-wrap：设置页也用 .cfg-pane，
  //   全局查会把那边的面板一起关掉（切完设置页再回配置页，右栏会是空的）。
  var panesRoot = cfgNav ? cfgNav.closest('.cfg-wrap') : document
  function pickPane(key) {
    var i = ORDER.indexOf(key); if (i < 0) return
    Array.prototype.forEach.call(document.querySelectorAll('.cfg-nav button'), function (b) {
      b.classList.toggle('on', b.dataset.pane === key)
    })
    Array.prototype.forEach.call(panesRoot.querySelectorAll('.cfg-pane'), function (p) {
      p.classList.toggle('on', p.dataset.pane === key)
    })"""
if old_cfg in s:
    s = s.replace(old_cfg, new_cfg, 1)
    print('OK  配置页 pane 作用域已限定')
else:
    print('!!  没找到配置页 pickPane 片段')

# ── 2) filters：支持 data-multi（如「触发方式」两种可以一起开）──
old_f = """    var b = e.target.closest('.chipf'); if (!b) return
      Array.prototype.forEach.call(g.querySelectorAll('.chipf'), function (x) { x.classList.remove('on') })
      b.classList.add('on')"""
new_f = """    var b = e.target.closest('.chipf'); if (!b) return
      // multi 组允许一个都不选（备份的「触发方式」两种能一起开）
      if (g.hasAttribute('data-multi')) { b.classList.toggle('on'); return }
      Array.prototype.forEach.call(g.querySelectorAll('.chipf'), function (x) { x.classList.remove('on') })
      b.classList.add('on')"""
if old_f in s:
    s = s.replace(old_f, new_f, 1)
    print('OK  chipf 支持多选组')
else:
    print('!!  没找到 filters 片段')

# ── 3) 占位页文案：九屏已全画完 ──
old_ph = "'本次设计稿覆盖 <b style=\"color:var(--ink-1)\">概览 / 控制台 / 玩家 / 配置 / 状态速览</b> 五屏，外加一页设计规范，' +\n      '用来先把整体设计语言敲定。确认之后，插件模组 / 备份还原 / 计划任务 / 设置 会沿用同一套 token 铺开。' +"
new_ph = "'本次设计稿已覆盖全部 <b style=\"color:var(--ink-1)\">九屏</b>：概览 / 控制台 / 玩家 / 配置 / ' +\n      '插件模组 / 备份还原 / 计划任务 / 设置 / 状态速览，共用同一套 token。' +\n      '真面板里还有「文件管理」和「整合包市场」两屏，等它们也补进来再撤掉这里。' +"
if old_ph in s:
    s = s.replace(old_ph, new_ph, 1)
    print('OK  占位页文案已更新')
else:
    print('!!  没找到占位页文案片段')

# ── 4) 设置页分组导航（作用域限定，不碰配置页）──
set_nav = """
  /* ── 设置：分组导航（同款视觉，作用域限定在自己的 cfg-wrap 内）── */
  var setNav = document.getElementById('setNav')
  var SET_META = {
    access: ['访问与安全', '默认不挡人 —— 改了这个家以外的地方访问就会被问密码'],
    daemon: ['守护与启动', '面板自己怎么起来、监听在哪'],
    mirror: ['下载与市场', '服务端 jar 和模组市场从哪拿'],
    data: ['数据与日志', '东西存在哪、留多久'],
    about: ['关于', '版本与构建信息']
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
"""
anchor = "  /* ── 动效总开关 ── */"
if 'setNav' not in s and anchor in s:
    s = s.replace(anchor, set_nav + "\n" + anchor, 1)
    print('OK  设置页导航已接入')
else:
    print('!!  设置页导航：已存在或找不到锚点')

if s == orig:
    print('FAIL: 没有任何改动')
    sys.exit(1)

io.open(p, 'w', encoding='utf-8', newline='\n').write(s)

# 回验——看产物，不看脚本的自我感觉
chk = io.open(p, encoding='utf-8').read()
for token, label in [('setNav', '设置页导航'), ('data-multi', '多选支持'), ('九屏', '占位页文案'), ('panesRoot', '作用域限定')]:
    print('  %-8s %s' % (label, 'OK' if token in chk else '!! 缺失'))
# JS 括号粗查
print('  花括号 %d/%d %s' % (chk.count('{'), chk.count('}'), 'OK' if chk.count('{') == chk.count('}') else '!! 不配对'))
print('  圆括号 %d/%d %s' % (chk.count('('), chk.count(')'), 'OK' if chk.count('(') == chk.count(')') else '!! 不配对'))
