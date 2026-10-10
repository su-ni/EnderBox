# -*- coding: utf-8 -*-
"""把 4 屏新页面插入主设计稿 index.html —— 幂等：重复跑不会重复插入。"""
import io, os, sys, re

base = os.path.dirname(os.path.abspath(__file__))
idx = os.path.join(base, 'index.html')
frag = os.path.join(base, '_newpages.html')

html = io.open(idx, encoding='utf-8').read()
pages = io.open(frag, encoding='utf-8').read()

# 幂等守卫：已经插过就直接退出
if 'data-page="settings"' in html:
    print('skip: 已包含 settings 屏，未改动')
    sys.exit(0)

anchor = '        <!-- ════════ 屏 5 · 状态速览 ════════ -->'
if anchor not in html:
    # 锚点可能因为重写变了行尾/缩进，退而求其次按 data-page 找
    m = re.search(r'(?m)^(\s*)<!--[^\n]*屏 5[^\n]*-->\s*$', html)
    if not m:
        print('FAIL: 找不到插入锚点'); sys.exit(1)
    anchor = m.group(0)

html = html.replace(anchor, pages + anchor, 1)
io.open(idx, 'w', encoding='utf-8', newline='\n').write(html)

# 回验：不是看我自己有没有写进去，而是看产物对不对
check = io.open(idx, encoding='utf-8', newline='\n').read()
want = ['data-page="plugins"', 'data-page="backup"', 'data-page="tasks"', 'data-page="settings"']
lost = [w for w in want if w not in check]
print('插入后行数: %d' % check.count('\n'))
print('缺失的屏: %s' % (lost if lost else '无'))
# 标签配对粗查（插碎片最容易留 unbalanced tag）
for tag in ('section', 'div'):
    o = len(re.findall(r'<%s[\s>]' % tag, check))
    c = len(re.findall(r'</%s>' % tag, check))
    print('%-8s open=%d close=%d %s' % (tag, o, c, 'OK' if o == c else '!! 不配对'))
sys.exit(1 if lost else 0)
