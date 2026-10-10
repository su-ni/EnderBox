# -*- coding: utf-8 -*-
"""生成设计稿任意一屏的「对比沙盘」，供和真实前端并排比。

用法：python _cmp.py <page-key>      # 如 plugins / config / backup / tasks / settings
      python _cmp.py players         # 默认

做的事：把该 <section data-page=X> 设为 on、别的都关掉，隐藏设计稿自己的 .draftbar、
让 .app 铺满、禁掉入场动画 —— 这样它和真实面板的容器一致，可直接并排看。

顺带注入通用几何探针（写进 title）：量该屏里所有「重复行」的列左缘去重集合、
行高一致性。**类名按页给**（玩家页是 .prow，插件页是 .rrow…）。

⚠ 只生成临时副本 _cmp-<key>.html，**不动设计稿本身**；看完删掉。
"""
import io, os, sys

D = os.path.dirname(os.path.abspath(__file__))
SRC = os.path.join(D, 'index.html')

key = sys.argv[1] if len(sys.argv) > 1 else 'players'
OUT = os.path.join(D, '_cmp-%s.html' % key)

# 每屏的「重复行」类名 —— 用来量列对齐 / 行高
ROWS = {
    'players': ['.prow', '.prow .pstat', '.prow .pcoords', '.prow .pact'],
    'plugins': ['.rrow', '.rrow .rmeta', '.rrow .ract'],
}

s = io.open(SRC, encoding='utf-8').read()
# 原来标着 on 的那屏（overview）先关掉
import re
s = re.sub(r'<section class="page on" data-page="([\w-]+)">',
           r'<section class="page" data-page="\1">', s, count=1)
# 目标屏打开
old = '<section class="page" data-page="%s">' % key
assert old in s, '找不到 data-page=%s' % key
s = s.replace(old, '<section class="page on" data-page="%s">' % key, 1)

rows_js = '[' + ','.join("'%s'" % r for r in ROWS.get(key, ['.rrow'])) + ']'
inject = '''<style>
.draftbar{display:none!important}
.app{height:100vh!important;max-width:none!important;margin:0!important}
/* 对比用：禁掉入场动画 —— 否则无头截图逮到淡入中途，比不出排版 */
.reveal{opacity:1!important;transform:none!important;animation:none!important}
</style>
<script>
(function () {
  function L(sel) {
    var e = document.querySelectorAll(sel), seen = {}
    for (var i = 0; i < e.length; i++) seen[Math.round(e[i].getBoundingClientRect().left)] = 1
    return Object.keys(seen).join(',')
  }
  function go() {
    /* 静态替换已把目标屏设成 .page.on —— 不要再点 tab：
       app.js 的 show() 会重算一遍，且 querySelector('.sec-head') 会取到别屏的那个。 */
    setTimeout(function () {
      var sec = document.querySelector('section.page.on[data-page="''' + key + '''"]')
      var t = 'onPage=' + (sec ? sec.getAttribute('data-page') : 'NONE')
      t += ' headH=' + (sec ? Math.round(sec.querySelector('.sec-head').getBoundingClientRect().height) : 'NA')
      var rows = ''' + rows_js + '''
      for (var i = 0; i < rows.length; i++) {
        var sel = rows[i], n = document.querySelectorAll(sel).length
        if (n) t += ' ' + sel.split(' ').pop().replace('.', '') + 'L=' + L(sel) + '(n' + n + ')'
      }
      var hs = []
      var first = document.querySelectorAll(rows[0])
      for (var j = 0; j < first.length; j++) hs.push(Math.round(first[j].getBoundingClientRect().height))
      t += ' rowH=' + hs.join('/')
      document.title = t
    }, 600)
  }
  setTimeout(go, 60)
})();
</script>
</body>'''
s = s.replace('</body>', inject, 1)
io.open(OUT, 'w', encoding='utf-8', newline='\n').write(s)
print('已生成对比沙盘 %s (%d bytes) · 页面=%s · 行选择器=%s' % (
    os.path.basename(OUT), len(s), key, ' '.join(ROWS.get(key, []))))
