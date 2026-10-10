# -*- coding: utf-8 -*-
"""量 App Shell 的盒模型：左右缝隙到底在哪、HUD 占了多少高、内容区底部留了多少空。"""
import io, os, re, subprocess, json

BASE = os.path.dirname(os.path.abspath(__file__))
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
html = io.open(os.path.join(BASE, 'index.html'), encoding='utf-8').read()

JS = """
<script>window.addEventListener('load',function(){setTimeout(function(){
var cw=document.documentElement.clientWidth, ch=window.innerHeight, out={};
function box(sel){
  var el=document.querySelector(sel); if(!el) return {sel:sel,missing:1};
  var r=el.getBoundingClientRect(), cs=getComputedStyle(el);
  return {sel:sel, L:Math.round(r.left), R:Math.round(r.right), W:Math.round(r.width),
          T:Math.round(r.top), B:Math.round(r.bottom), H:Math.round(r.height),
          pad:[cs.paddingTop,cs.paddingRight,cs.paddingBottom,cs.paddingLeft].join(' '),
          mgn:[cs.marginTop,cs.marginRight,cs.marginBottom,cs.marginLeft].join(' '),
          bdw:cs.borderLeftWidth, ovx:cs.overflowX, pos:cs.position,
          gap:cs.gap, gtc:cs.gridTemplateColumns};
}
out.view={cw:cw,ch:ch};
out.els=['.app','.shell','aside.side','.main','.content','.page.on','.page.on .panel','.hud','.tabbar','.world-head']
         .map(box);
// 内容区最后一个 panel 的底边 到 HUD 顶边 的距离 = 用户说的「下面那截空」
var last=document.querySelector('.page.on');
var panels=last?last.querySelectorAll('.panel,.metrics'):[];
var lastEl=panels[panels.length-1];
var hud=document.querySelector('.hud');
out.tailGap = (lastEl&&hud)? Math.round(hud.getBoundingClientRect().top - lastEl.getBoundingClientRect().bottom) : null;
// 左右缝隙：内容区左边 与 第一个 panel 左边
var p0=document.querySelector('.page.on .panel');
var c0=document.querySelector('.content');
if(p0&&c0){var cs0=getComputedStyle(c0);
  out.leftGap = Math.round(p0.getBoundingClientRect().left - c0.getBoundingClientRect().left);
  out.rightGap = Math.round(c0.getBoundingClientRect().right - p0.getBoundingClientRect().right);}
document.title='PR'+JSON.stringify(out);
},1000);});</script>
"""

tmp = os.path.join(BASE, '_shot_layout.html')
io.open(tmp, 'w', encoding='utf-8', newline='\n').write(html.replace('</body>', JS + '</body>'))
url = 'file:///' + tmp.replace(os.sep, '/')

for w, h in ((1440, 900), (1440, 1200)):
    r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
                        '--allow-file-access-from-files', '--window-size=%d,%d' % (w, h),
                        '--virtual-time-budget=2400', '--dump-dom', url],
                       capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
    m = re.search(r'<title>PR(\{.*?\})</title>', r.stdout)
    if not m:
        print('探针失败'); continue
    d = json.loads(m.group(1))
    print('=' * 104)
    print('视口 %dx%d  左缝=%s  右缝=%s  末块到HUD的空=%s' % (d['view']['cw'], d['view']['ch'],
                                                             d.get('leftGap'), d.get('rightGap'), d.get('tailGap')))
    print('%-16s %5s %5s %5s %5s %5s %6s  %s' % ('元素', 'L', 'R', 'W', 'T', 'B', 'H', 'padding'))
    for e in d['els']:
        if e.get('missing'):
            print('%-16s 缺失' % e['sel']); continue
        print('%-16s %5d %5d %5d %5d %5d %6d  %s' % (e['sel'], e['L'], e['R'], e['W'], e['T'], e['B'], e['H'], e['pad']))

os.remove(tmp)
