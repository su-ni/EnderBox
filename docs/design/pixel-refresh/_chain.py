# -*- coding: utf-8 -*-
"""把溢出按钮的祖先链打出来 —— 定位「是谁把它推到 x=346」"""
import io, os, re, subprocess, json

BASE = os.path.dirname(os.path.abspath(__file__))
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
html = io.open(os.path.join(BASE, 'index.html'), encoding='utf-8').read()

JS = """
<script>window.addEventListener('load',function(){setTimeout(function(){
var cw=document.documentElement.clientWidth,res=[];
Array.prototype.forEach.call(document.querySelectorAll('.btn.sm'),function(b){
  var t=(b.textContent||'').replace(/\\s+/g,'');
  if(t.indexOf('立即备份')<0) return;
  var chain=[],el=b;
  while(el && el!==document.body){
    var r=el.getBoundingClientRect(), cs=getComputedStyle(el);
    chain.push({
      cls:(typeof el.className==='string'?el.className:'(svg)').slice(0,40)||el.tagName.toLowerCase(),
      L:Math.round(r.left), R:Math.round(r.right), W:Math.round(r.width),
      disp:cs.display, gtc:cs.gridTemplateColumns, miw:cs.minWidth, ws:cs.whiteSpace, pos:cs.position
    });
    el=el.parentElement;
  }
  res.push(chain);
});
document.title='PR'+JSON.stringify({cw:cw,chains:res});
},1200);});</script>
"""

# 临时文件必须与 styles.css 同目录，否则相对路径 404（夹具会骗人）
tmp = os.path.join(BASE, '_shot_chain.html')
io.open(tmp, 'w', encoding='utf-8', newline='\n').write(html.replace('</body>', JS + '</body>'))
url = 'file:///' + tmp.replace(os.sep, '/')
r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox', '--hide-scrollbars',
                    '--allow-file-access-from-files', '--window-size=520,1200',
                    '--virtual-time-budget=2600', '--dump-dom', url],
                   capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)
m = re.search(r'<title>PR(\{.*?\})</title>', r.stdout)
if not m:
    print('探针失败'); print(r.stdout[:300]); raise SystemExit(1)
d = json.loads(m.group(1))
print('clientWidth=%d' % d['cw'])
for chain in d['chains']:
    print('=' * 96)
    for i, c in enumerate(chain):
        print('%s%-40s L=%-5d R=%-5d W=%-5d %-14s %s' % (
            '  ' * i, c['cls'][:40], c['L'], c['R'], c['W'], c['disp'], c['gtc'][:44]))
