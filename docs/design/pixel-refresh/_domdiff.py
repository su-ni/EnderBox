# -*- coding: utf-8 -*-
"""抓设计稿沙盘与真实前端的第一条 .prow，逐字符 diff —— 版式查不出差异时，
最后就靠它：DOM 只要有一个字不同，就是元凶。"""
import re, subprocess, difflib

CHROME = r'C:\Program Files\Google\Chrome\Application\chrome.exe'
DESIGN = 'file:///F:/项目/开服器/docs/design/pixel-refresh/_cmp-players.html'
REAL = 'http://127.0.0.1:5173/?nosse=1&page=players'


def dump(url):
    r = subprocess.run([CHROME, '--headless=new', '--disable-gpu', '--no-sandbox',
                        '--virtual-time-budget=9000', '--dump-dom', url],
                       capture_output=True, text=True, encoding='utf-8',
                       errors='replace', timeout=120)
    return r.stdout


def first_prow(html):
    """从 class="prow" 处往回找到 '<'，再按 div 平衡截出整个元素。"""
    i = html.find('class="prow"')
    if i < 0:
        return ''
    a = html.rfind('<', 0, i)
    depth = 0
    j = a
    tag = re.compile(r'</?div\b')
    while True:
        m = tag.search(html, j)
        if not m:
            break
        if html[m.start():m.start() + 2] == '</':
            depth -= 1
            j = m.end()
            if depth == 0:
                return html[a:html.find('>', m.start()) + 1]
        else:
            depth += 1
            j = m.end()
    return html[a:a + 4000]


d = first_prow(dump(DESIGN))
r = first_prow(dump(REAL))
print('设计稿 .prow 长度 %d · 真实前端 %d' % (len(d), len(r)))
print('=' * 70)
# 把标签之间的空白规整掉再比，避免缩进噪音
norm = lambda s: re.sub(r'>\s+<', '><', s)
dl, rl = norm(d), norm(r)
if dl == rl:
    print('✅ 两条 .prow 的 DOM 完全一致（规整空白后）')
else:
    sm = difflib.SequenceMatcher(None, dl, rl)
    for op, i1, i2, j1, j2 in sm.get_opcodes():
        if op == 'equal':
            continue
        print('[%s] 设计稿=%r  真实前端=%r' % (op, dl[i1:i2][:200], rl[j1:j2][:200]))
