# -*- coding: utf-8 -*-
"""纵向体检：找出「被父容器裁掉/压扁」的元素。
判据 scrollHeight > clientHeight + 2 且 overflow 不是 visible 的祖先在裁它。
同时自检 CSS 是否真的加载（否则数据作废）。"""
import io, os, re, subprocess, json

BASE = os.path.dirname(os.path.abspath(__file__))
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
html = io.open(os.path.join(BASE, 'index.html'), encoding='utf-8').read()

JS = r"""
<script>window.addEventListener('load',function(){setTimeout(function(){
var out={};
out.cssok = getComputedStyle(document.body).backgroundColor;
out.clip=[];
document.querySelectorAll('.page.on *, .main').forEach(function(el){
  var cs=getComputedStyle(el);
  if(cs.overflowY==='visible') return;
  var over = el.scrollHeight - el.clientHeight;
  if(over > 4){
    var cn = typeof el.className==='string'? el.className : '(svg)';
    out.clip.push({cls:cn.slice(0,42), sh:el.scrollHeight, ch:el.clientHeight, over:over,
                   ov:cs.overflowY});
  }
});
// 关键元素实际高度
function h(sel){var e=document.querySelector(sel); if(!e) return null;
  var r=e.getBoundingClientRect();
  return {h:Math.round(r.height), sh:e.scrollHeight, ch:e.clientHeight,
          top:Math.round(r.top), bot:Math.round(r.bottom)};}
out.h={};
['.main','.content','.page.on','.page.on .grid-2','.page.on .grid-2 > .panel',
 '.page.on .grid-2 > .panel > .con-body','.metrics'].forEach(function(s){out.h[s]=h(s);});
// 内容自然高度 vs 可用高度
var page=document.querySelector('.page.on');
out.natural = page ? page.scrollHeight : null;
out.avail = document.querySelector('.content') ? document.querySelector('.content').clientHeight : null;
document.title='PR'+JSON.stringify(out);
},1100);});</script>
"""

tmp = os.path.join(BASE, '_shot_vfit.html')
io.open(tmp, 'w', encoding='utf-8', newline='\n').write(html.replace('</body>', JS + '</body>'))
url = 'file:///' + tmp.replace(os.sep, '/')

for w, h in ((1440, 900), (1440, 1200)):
    r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
                        '--allow-file-access-from-files', '--window-size=%d,%d' % (w, h),
                        '--virtual-time-budget=2400', '--dump-dom', url],
                       capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
    m = re.search(r'<title>PR(\{.*?\})</title>', r.stdout)
    print('=' * 90)
    if not m:
        print('探针失败', r.stdout[:200]); continue
    d = json.loads(m.group(1))
    print('视口 %dx%d  CSS=%s  %s' % (w, h, d['cssok'],
          '!! CSS 未加载，数据作废' if d['cssok'] in ('rgba(0, 0, 0, 0)', 'transparent') else 'CSS-OK'))
    print('内容自然高=%s  可用高=%s  差=%s' % (d['natural'], d['avail'],
          (d['natural'] - d['avail']) if (d['natural'] and d['avail']) else '?'))
    for k, v in d['h'].items():
        print('  %-42s %s' % (k, v))
    if d['clip']:
        print('  被裁元素:')
        for c in d['clip'][:10]:
            print('    %-44s scrollH=%-6d clientH=%-6d 溢出=%-5d overflow-y=%s' %
                  (c['cls'], c['sh'], c['ch'], c['over'], c['ov']))
    else:
        print('  无纵向裁切')

os.remove(tmp)
