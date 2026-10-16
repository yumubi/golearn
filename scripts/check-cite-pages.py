"""Handbook 出处标签抽查:〔W14-1 p5〕里的单元必须存在,页码必须出现在对应提取文本里。

进阶:若标签页在提取文本里是目录页(该页 OCR 行数极少),告警提醒人工复核:
页码存在不代表内容支持论断。
"""
import glob
import os
import re
import sys

ROOT = os.getcwd()
TEXT = os.path.join(ROOT, 'work/text')

# unit -> {page: 该页正文行数(OCR 行,排除页标记与空行)}
page_lines = {}
for f in glob.glob(os.path.join(TEXT, '*.txt')):
    key = os.path.basename(f)[:-4]
    s = open(f, encoding='utf-8', errors='ignore').read()
    pages = {}
    parts = re.split(r'\[\[p(\d+)\]\]', s)
    for i in range(1, len(parts) - 1, 2):
        page = int(parts[i])
        body = parts[i + 1]
        lines = [l for l in body.splitlines() if l.strip() and not l.startswith('[OCR]')]
        pages[page] = len(lines)
    page_lines[key] = pages

files = sorted(glob.glob(os.path.join(ROOT, 'guide/handbook/[0-2][0-9]-*.md')))
files = [f for f in files if os.path.basename(f)[:2] <= '25']
# 页码标签只有 W/TB 两种;C:/K: 标签是路径或课名引用,不带页码
pat = re.compile(r'〔(W\d+-\d+|TB\w*)\s+p(\d+)(?:-(\d+))?〕')
total, bad, thin = 0, [], []
for f in files:
    t = open(f, encoding='utf-8').read()
    for m in pat.finditer(t):
        total += 1
        unit, page, page2 = m.group(1), int(m.group(2)), m.group(3)
        if unit not in page_lines:
            bad.append(f"{os.path.basename(f)}: 单元 {unit} 无提取文本")
            continue
        pages = page_lines[unit]
        hi = int(page2) if page2 else page
        for p in range(page, hi + 1):
            if p not in pages:
                bad.append(f"{os.path.basename(f)}: {unit} p{p} 超出提取范围")
            elif pages[p] < 4:
                thin.append(f"{os.path.basename(f)}: {unit} p{p} 仅 {pages[p]} 行正文,疑似目录页")
print(f"抽查 {total} 个出处标签")
if bad:
    print("BAD:")
    for b in dict.fromkeys(bad):
        print(' -', b)
if thin:
    print("THIN(疑似目录页,需人工复核):")
    for b in dict.fromkeys(thin):
        print(' -', b)
if bad or thin:
    sys.exit(1)
print("ALL OK")
