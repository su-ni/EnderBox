# -*- coding: utf-8 -*-
"""定位 520 档横向溢出的具体元素（溢出对所有页一致，说明在 App Shell 里，不在页面内容里）"""
import io, os, re, subprocess, json

BASE = os.path.dirname(os.path.abspath(__file__))
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
html = io.open(os.path.join(BASE, 'index.html'), encoding='utf-8').read()

JS = """
<script>window.addEventListener('load',function(){setTimeout(function(){
var cw=document.documentElement.clientWidth, list=[];
Array.prototype.forEach.call(document.querySelectorAll('body *'),function(el){
  var r=el.getBoundingClientRect();
  if(r.width>0 && r.right>cw+1){
    var cs=getComputedStyle(el);
    list.push({
      tag:el.tagName.toLowerCase(),
      cls:(typeof el.className==='string'?el.className:'(svg)').slice(0,34),
      L:Math.round(r.left), R:Math.round(r.right), W:Math.round(r.width),
      ws:cs.whiteSpace, ovx:cs.overflowX, fx:cs.flexWrap, disp:cs.display, pos:cs.position,
      txt:(el.textContent||'').trim().slice(0,26)
    });
  }
});
// 按「谁最靠右」排序，最夸张的排前面
list.sort(function(a,b){return b.R-a.R});
document.title='PR'+JSON.stringify({cw:cw, sw:document.documentElement.scrollWidth, n:list.length, top:list.slice(0,8)});
},1200);});</script>
"""

# 临时文件必须与 styles.css 同目录，否则相对路径 404（夹具会骗人）
tmp = os.path.join(BASE, '_shot_who.html')
io.open(tmp, 'w', encoding='utf-8', newline='\n').write(html.replace('</body>', JS + '</body>'))
url = 'file:///' + tmp.replace(os.sep, '/')
r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
                    '--allow-file-access-from-files', '--window-size=520,1200',
                    '--virtual-time-budget=2600', '--dump-dom', url],
                   capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
m = re.search(r'<title>PR(\{.*?\})</title>', r.stdout)
if not m:
    print('探针失败'); print(r.stdout[:400]); raise SystemExit(1)
d = json.loads(m.group(1))
print('clientWidth=%s  scrollWidth=%s  超宽元素=%d' % (d['cw'], d['sw'], d['n']))
print('-' * 100)
for x in d['top']:
    print('%-12s R=%-5d L=%-5d W=%-5d ws=%-8s ovx=%-7s %s' % (
        x['cls'][:12], x['R'], x['L'], x['W'], x['ws'], x['ovx'], repr(x['txt'])))
    print('             tag=%s pos=%s disp=%s wrap=%s' % (x['tag'], x['pos'], x['disp'], x['fx']))
