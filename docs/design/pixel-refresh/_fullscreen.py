# -*- coding: utf-8 -*-
"""全屏式布局改造：内容区左右去缝、底部贴住健康条、健康条压薄。"""
import io, os, sys

BASE = os.path.dirname(os.path.abspath(__file__))
p = os.path.join(BASE, 'styles.css')
s = io.open(p, encoding='utf-8').read()
orig = s

REPL = [
    # 内容区/页签/身份头：左右 22px 的缝去掉，改成贴边
    ('.content { padding: 18px 22px 0; flex: 1; }',
     '.content { padding: 16px 0 0; flex: 1; }'),
    ('.tabbar { display: flex; gap: 4px; padding: 16px 22px 0; flex-wrap: wrap; }',
     '.tabbar { display: flex; gap: 4px; padding: 16px 0 0; flex-wrap: wrap; }'),
    ('.world-head { padding: 16px 22px 0; }',
     '.world-head { padding: 16px 0 0; }'),
    # 主区：底部让位从 78 压到 50（= 新健康条 48 + 2），不再留那 18px 空档；
    # overflow-x 必须显式裁掉 —— panel 有 4px 硬阴影，贴边后它会撑出横向滚动条
    ('.main { flex: 1; min-width: 0; display: flex; flex-direction: column; padding: 0 0 78px; overflow-y: auto; }',
     '.main { flex: 1; min-width: 0; display: flex; flex-direction: column; padding: 0 0 50px; overflow: hidden auto; }'),
    # 健康条：60 → 48，把 12px 还给内容区
    ('  height: 60px; display: flex; align-items: center; gap: 20px;',
     '  height: 48px; display: flex; align-items: center; gap: 18px;'),
    ('@media (min-width: 1081px) { .side { padding-bottom: 60px; } }',
     '@media (min-width: 1081px) { .side { padding-bottom: 50px; } }'),
]

miss = []
for a, b in REPL:
    if a in s:
        s = s.replace(a, b, 1)
    else:
        miss.append(a[:56])

if s == orig:
    print('FAIL: 没有改动'); sys.exit(1)

io.open(p, 'w', encoding='utf-8', newline='\n').write(s)

chk = io.open(p, encoding='utf-8').read()
print('未匹配到:', miss if miss else '无')
for token, label in [('padding: 16px 0 0; flex: 1', '内容区去缝'),
                     ('padding: 0 0 50px', '底部贴住'),
                     ('height: 48px', '健康条压薄'),
                     ('padding-bottom: 50px', '侧栏让位同步')]:
    print('  %-12s %s' % (label, 'OK' if token in chk else '!! 缺失'))
print('  花括号 %d/%d %s' % (chk.count('{'), chk.count('}'),
                             'OK' if chk.count('{') == chk.count('}') else '!! 不配对'))
