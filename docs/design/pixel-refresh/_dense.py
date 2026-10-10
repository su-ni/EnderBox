# -*- coding: utf-8 -*-
"""提高信息密度（治「偏松」）。幂等：每处替换都断言命中，命中过就跳过。"""
import io, os

BASE = os.path.dirname(os.path.abspath(__file__))
p = os.path.join(BASE, 'styles.css')
s = io.open(p, encoding='utf-8').read()

RULES = [
    # ── 侧栏：行高 50 → 42 左右
    ('.side-cap {\n  display: flex; align-items: baseline; gap: 8px;\n  padding: 14px 14px 8px;\n}',
     '.side-cap {\n  display: flex; align-items: baseline; gap: 8px;\n  padding: 10px 12px 6px;\n}'),
    ('.worlds { flex: 1; overflow-y: auto; padding: 4px 8px 8px; display: flex; flex-direction: column; gap: 3px; }',
     '.worlds { flex: 1; overflow-y: auto; padding: 2px 6px 6px; display: flex; flex-direction: column; gap: 2px; }'),
    ('  padding: 8px 9px; cursor: pointer;\n  border-left: 3px solid transparent;',
     '  padding: 6px 9px; cursor: pointer;\n  border-left: 3px solid transparent;'),

    # ── 世界头 / 页签条 / 内容区 上间距
    ('.world-head { padding: 16px 0 0; }', '.world-head { padding: 10px 0 0; }'),
    ('.tabbar { display: flex; gap: 4px; padding: 16px 0 0; flex-wrap: wrap; }',
     '.tabbar { display: flex; gap: 4px; padding: 10px 0 0; flex-wrap: wrap; }'),
    ('.content { padding: 16px 0 0; flex: 1 0 auto; display: flex; flex-direction: column; }',
     '.content { padding: 12px 0 0; flex: 1 0 auto; display: flex; flex-direction: column; }'),

    # ── 分栏间距
    ('.grid-2 { display: grid; grid-template-columns: 1.5fr 1fr; gap: 16px; margin-top: 16px; }',
     '.grid-2 { display: grid; grid-template-columns: 1.5fr 1fr; gap: 12px; margin-top: 12px; }'),

    # ── 面板底距
    ('  box-shadow: 0 0 0 1px #000, var(--sh-hard);\n  margin-bottom: 16px;',
     '  box-shadow: 0 0 0 1px #000, var(--sh-hard);\n  margin-bottom: 12px;'),

    # ── 指标带：卡片更矮、大数字收小
    ('.metrics { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }',
     '.metrics { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px; }'),
    ('  padding: 13px 14px 14px;\n  transition: border-color var(--dur), transform var(--dur) var(--ease);',
     '  padding: 10px 12px 11px;\n  transition: border-color var(--dur), transform var(--dur) var(--ease);'),
    ('.metric .val { display: flex; align-items: baseline; gap: 6px; margin: 10px 0 11px; }',
     '.metric .val { display: flex; align-items: baseline; gap: 6px; margin: 7px 0 8px; }'),
    ('.metric .val b { font-family: var(--f-pix); font-size: 30px; line-height: 1; color: var(--ink-1); font-weight: 400; }',
     '.metric .val b { font-family: var(--f-pix); font-size: 26px; line-height: 1; color: var(--ink-1); font-weight: 400; }'),
    ('.metric .note { margin-top: 9px;', '.metric .note { margin-top: 7px;'),

    # ── 控制台：行距 25.6 → 21.8，最小高度 320 → 220
    ('  position: relative; flex: 1; min-height: 320px; overflow-y: auto;\n'
     '  padding: 10px 14px 14px; background: #070a0c;\n'
     '  font-family: var(--f-term); font-size: 15.5px; line-height: 1.65;',
     '  position: relative; flex: 1; min-height: 220px; overflow-y: auto;\n'
     '  padding: 8px 12px 10px; background: #070a0c;\n'
     '  font-family: var(--f-term); font-size: 14.5px; line-height: 1.5;'),
]

applied, skipped = [], []
for old, new in RULES:
    if old in s:
        s = s.replace(old, new, 1)
        applied.append(old.strip().split('\n')[0][:56])
    elif new in s:
        skipped.append(old.strip().split('\n')[0][:56] + '  (已是新值)')
    else:
        skipped.append('!! 未命中: ' + old.strip().split('\n')[0][:56])

io.open(p, 'w', encoding='utf-8', newline='\n').write(s)
print('已应用 %d 处:' % len(applied))
for a in applied:
    print('  +', a)
if skipped:
    print('跳过 %d 处:' % len(skipped))
    for k in skipped:
        print('  -', k)
