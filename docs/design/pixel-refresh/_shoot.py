# -*- coding: utf-8 -*-
"""九屏逐屏截图 + 溢出/缺失token 检查。
做法：每屏生成一份临时 HTML（只把目标 section 置为 .page.on），
用 headless Chrome 截图；再独立跑一遍「把测量结果写进 title」的变体，用 --dump-dom 取回。"""
import io, os, re, subprocess, sys, shutil, time

BASE = os.path.dirname(os.path.abspath(__file__))
SHOTS = os.path.join(os.path.dirname(BASE), '..', 'shots', 'pixel-refresh')
SHOTS = os.path.normpath(SHOTS)
CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
IDX = os.path.join(BASE, 'index.html')
html = io.open(IDX, encoding='utf-8').read()

PAGES = [
    ('overview', '概览'), ('console', '控制台'), ('players', '玩家'),
    ('config', '配置'), ('plugins', '插件模组'), ('backup', '备份还原'),
    ('tasks', '计划任务'), ('settings', '设置'), ('states', '状态速览'),
]

os.makedirs(SHOTS, exist_ok=True)
# 临时文件必须与 styles.css 同目录，否则相对路径 404（夹具会骗人）


def variant(key):
    """返回只显示 key 这一屏的 HTML。
    ⚠ 额外注入一段「直达终态」的样式：headless 的虚拟时钟不推进 CSS 动画，
       .reveal 会停在 opacity:0，截出来是一片空白（夹具问题，不是设计稿问题）。
       真实浏览器里动画正常，所以只在这里绕开。"""
    out = html
    out = re.sub(r'<section class="page on" data-page="', '<section class="page" data-page="', out)
    out = out.replace('<section class="page" data-page="%s">' % key,
                      '<section class="page on" data-page="%s">' % key, 1)
    out = out.replace('</head>',
                      '<style>.reveal{opacity:1!important;transform:none!important;'
                      'animation:none!important}</style></head>', 1)
    return out


PROBE = """
<script>
(function(){
  function probe(){
    var d=document, out={};
    out.sw = d.documentElement.scrollWidth;
    out.cw = d.documentElement.clientWidth;
    // 横向溢出的具体元素（只看主的滚动容器 .main）
    var bad=[];
    document.querySelectorAll('.page.on *').forEach(function(el){
      var r=el.getBoundingClientRect();
      if(r.width>0 && (r.right > out.cw + 1 || r.left < -1)){
        bad.push((el.className&&el.className.baseVal===undefined?String(el.className).slice(0,40):'?')+' @'+Math.round(r.left)+'..'+Math.round(r.right));
      }
    });
    out.overflow = bad.slice(0,8);
    out.pages = document.querySelectorAll('.page').length;
    out.on = (document.querySelector('.page.on')||{}).dataset ? document.querySelector('.page.on').dataset.page : 'NONE';
    // 会不会有含中文却走了像素字体（像素字体没汉字，会出现豆腐块）
    out.font Mischief = 0;
    return out;
  }
})();
</script>
"""


def probe_html(key):
    v = variant(key)
    js = """
<script>
window.addEventListener('load',function(){
  setTimeout(function(){
    var cw = document.documentElement.clientWidth;
    var bad = [];
    document.querySelectorAll('.page.on *').forEach(function(el){
      var r = el.getBoundingClientRect();
      if (r.width > 0 && (r.right > cw + 1 || r.left < -1)) {
        var cn = typeof el.className === 'string' ? el.className : '(svg)';
        bad.push(cn.slice(0,36) + ' ' + Math.round(r.left) + '..' + Math.round(r.right));
      }
    });
    document.title = 'PR' + JSON.stringify({
      sw: document.documentElement.scrollWidth, cw: cw,
      page: (document.querySelector('.page.on')||{dataset:{}}).dataset.page,
      rows: document.querySelectorAll('.page.on .rrow').length,
      ovf: bad.slice(0,6)
    });
  }, 1200);
});
</script>
"""
    return v.replace('</body>', js + '</body>')


def run(args):
    return subprocess.run(args, capture_output=True, text=True, encoding='utf-8', errors='replace', timeout=90)


fails = []
for i, (key, cn) in enumerate(PAGES, 1):
    tmp = os.path.join(BASE, '_shot_%02d_%s.html' % (i, key))
    io.open(tmp, 'w', encoding='utf-8', newline='\n').write(variant(key))
    png = os.path.join(SHOTS, 'v3-%02d-%s.png' % (i, key))
    for w, tag in ((1440, '1440'), (520, '520')):
        h = 1400 if tag == '1440' else 2000
        out = os.path.join(SHOTS, 'v3-%02d-%s-%s.png' % (i, key, tag))
        r = run([CHROME, '--headless=new', '--disable-gpu', '--hide-scrollbars',
                 '--no-sandbox', '--allow-file-access-from-files',
                 '--window-size=%d,%d' % (w, h), '--virtual-time-budget=2600',
                 '--screenshot=' + out, 'file:///' + tmp.replace('\\', '/')])
        ok = os.path.exists(out) and os.path.getsize(out) > 9000
        print('%-10s %-5s %s %s' % (key, tag, 'OK' if ok else 'FAIL', '' if ok else r.stderr[-160:]))
        if not ok:
            fails.append((key, tag))

# 溢出测量（只测新增的四屏 + 两档）
print('\n=== 溢出/结构测量 ===')
for key in ('plugins', 'backup', 'tasks', 'settings'):
    v = probe_html(key)
    for w in (1440, 520, 1024, 768):
        tmp = os.path.join(BASE, '_shot_probe.html')
        io.open(tmp, 'w', encoding='utf-8', newline='\n').write(v)
        r = run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox',
                 '--allow-file-access-from-files', '--window-size=%d,1200' % w,
                 '--virtual-time-budget=2600', '--dump-dom',
                 'file:///' + tmp.replace('\\', '/')])
        m = re.search(r'<title>PR(\{.*?\})</title>', r.stdout)
        if not m:
            print('%-9s %-5s !! 探针没拿到结果 (title=%s)' % (key, w, (re.search(r'<title>([^<]*)</title>', r.stdout) or ['', '?'])[1][:40]))
            continue
        import json
        d = json.loads(m.group(1))
        flag = 'OK' if d['sw'] <= d['cw'] + 1 else '溢出'
        print('%-9s %-5s SW=%-5d CW=%-5d rows=%-3d %s %s' % (key, w, d['sw'], d['cw'], d['rows'], flag,
                                                             ('| ' + '; '.join(d['ovf'])) if d['ovf'] else ''))

pass
print('\n截图目录:', SHOTS)
print('失败:', fails if fails else '无')
