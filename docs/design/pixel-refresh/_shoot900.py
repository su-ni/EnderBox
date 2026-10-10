# -*- coding: utf-8 -*-
"""按真实视口高度(1440x900)截图，用来看「内容末块到健康条之间还剩多少空」。
同时把这段空的像素数写进 title 带回来。"""
import io, os, re, subprocess, json

BASE = os.path.dirname(os.path.abspath(__file__))
SHOTS = os.path.normpath(os.path.join(BASE, '..', '..', 'shots', 'pixel-refresh'))
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
html = io.open(os.path.join(BASE, 'index.html'), encoding='utf-8').read()

PAGES = [('overview','概览'),('console','控制台'),('players','玩家'),('config','配置'),
         ('plugins','插件模组'),('backup','备份还原'),('tasks','计划任务'),
         ('settings','设置'),('states','状态速览')]

JS = r"""
<script>window.addEventListener('load',function(){setTimeout(function(){
var hud=document.querySelector('.hud'), page=document.querySelector('.page.on');
var kids=page?page.querySelectorAll('.panel,.metrics,.console,.cfg-wrap,.grid-2'):[];
var last=kids[kids.length-1];
var gap=null;
if(last&&hud){gap=Math.round(hud.getBoundingClientRect().top-last.getBoundingClientRect().bottom);}
document.title='PR'+JSON.stringify({page:(page||{dataset:{}}).dataset.page,gap:gap,
  natural:page?Math.round(page.getBoundingClientRect().height):null,
  css:getComputedStyle(document.body).backgroundColor});
},1100);});</script>
"""

def variant(key):
    out = re.sub(r'<section class="page on" data-page="', '<section class="page" data-page="', html)
    out = out.replace('<section class="page" data-page="%s">' % key,
                      '<section class="page on" data-page="%s">' % key, 1)
    out = out.replace('</head>', '<style>.reveal{opacity:1!important;transform:none!important;'
                                 'animation:none!important}</style></head>', 1)
    return out

os.makedirs(SHOTS, exist_ok=True)
rows = []
for i, (key, cn) in enumerate(PAGES, 1):
    tmp = os.path.join(BASE, '_s9.html')
    io.open(tmp, 'w', encoding='utf-8', newline='\n').write(variant(key).replace('</body>', JS + '</body>'))
    png = os.path.join(SHOTS, 'v4-%02d-%s-900.png' % (i, key))
    r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-sandbox',
                        '--allow-file-access-from-files', '--window-size=1440,900',
                        '--virtual-time-budget=2600', '--screenshot=' + png,
                        'file:///' + tmp.replace('\\', '/')],
                       capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
    r2 = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
                         '--allow-file-access-from-files', '--window-size=1440,900',
                         '--virtual-time-budget=2600', '--dump-dom',
                         'file:///' + tmp.replace('\\', '/')],
                        capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
    m = re.search(r'<title>PR(\{.*?\})</title>', r2.stdout)
    d = json.loads(m.group(1)) if m else {}
    ok = os.path.exists(png) and os.path.getsize(png) > 9000
    flag = '空' if (d.get('gap') or 0) > 40 else ('贴' if (d.get('gap') or 0) >= -20 else '溢出')
    print('%-10s %-4s 截图=%s  CSS=%s  内容高=%-5s 末块到HUD=%-6s %s' %
          (key, cn, 'OK' if ok else 'FAIL', d.get('css', '?')[:16], d.get('natural'), d.get('gap'), flag))
    rows.append((key, d.get('gap')))

os.remove(tmp)
print('\n底部留空 >40px 的页面:', [k for k, g in rows if (g or 0) > 40] or '无')
