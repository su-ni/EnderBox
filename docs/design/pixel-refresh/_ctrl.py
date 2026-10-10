# -*- coding: utf-8 -*-
"""对照组 + 夹具自检：老五屏 vs 新四屏，同档宽度。
关键：临时 HTML 必须和 styles.css 同目录，否则相对路径 404，
量到的是「裸 HTML」而不是产品 —— 探针会骗人，所以这里带 CSS 加载自检。"""
import io, os, re, subprocess, json

BASE = os.path.dirname(os.path.abspath(__file__))
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
html = io.open(os.path.join(BASE, 'index.html'), encoding='utf-8').read()


def variant(key):
    o = re.sub(r'<section class="page on" data-page="', '<section class="page" data-page="', html)
    o = o.replace('<section class="page" data-page="' + key + '">',
                  '<section class="page on" data-page="' + key + '">', 1)
    # 动画在 headless 虚拟时钟下不推进，.reveal 会停在 translateY(12px)/opacity:0，
    # 那会让量到的位置本身就偏 —— 先直达终态再量。
    return o.replace('</head>',
                     '<style>.reveal{opacity:1!important;transform:none!important;'
                     'animation:none!important}</style></head>', 1)


JS = """
<script>window.addEventListener('load',function(){setTimeout(function(){
var cw=document.documentElement.clientWidth,bad=[];
document.querySelectorAll('.page.on *').forEach(function(el){var r=el.getBoundingClientRect();
 if(r.width>0&&(r.right>cw+1||r.left<-1)){var cn=typeof el.className==='string'?el.className:'(svg)';
 bad.push(cn.slice(0,30)+' '+Math.round(r.left)+'..'+Math.round(r.right));}});
var side=document.querySelector('.side');
document.title='PR'+JSON.stringify({
 bg:getComputedStyle(document.body).backgroundColor,
 sideW:Math.round(side?side.getBoundingClientRect().width:0),
 sw:document.documentElement.scrollWidth,
 cw:cw,
 ovf:bad.slice(0,5)});},1200);});</script>
"""

WIDTH = os.environ.get('PROBE_W', '520')
for key in ['overview', 'console', 'players', 'config', 'plugins', 'backup', 'tasks', 'settings', 'states']:
    v = variant(key).replace('</body>', JS + '</body>')
    t = os.path.join(BASE, '_shot_c.html')
    io.open(t, 'w', encoding='utf-8', newline='\n').write(v)
    url = 'file:///' + t.replace(os.sep, '/')
    r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
                        '--allow-file-access-from-files', '--window-size=%s,1200' % WIDTH,
                        '--virtual-time-budget=2600', '--dump-dom', url],
                       capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
    m = re.search(r'<title>PR(\{.*?\})</title>', r.stdout)
    if not m:
        print('%-9s 探针失败' % key)
        continue
    d = json.loads(m.group(1))
    tag = '溢出' if d['sw'] > d['cw'] + 1 else 'OK'
    bg = d.get('bg', '?')
    css = 'CSS-OK' if bg not in ('rgba(0, 0, 0, 0)', 'transparent', '?') else '!! CSS未加载 数据作废'
    print('%-9s SW=%-5d CW=%-5d %-4s %-16s bg=%s side=%s %s' % (
        key, d['sw'], d['cw'], tag, css, bg, d.get('sideW'), d['ovf'][:3]))
