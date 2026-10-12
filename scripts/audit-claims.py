#!/usr/bin/env python3
"""notes 引用对账：把正文里可机器核对的断言全部列出并判定。

用法: python3 -I scripts/audit-claims.py [notes/pXX-*.md ...]   (缺省全部 notes)
输出每条断言的状态: OK / MISSING / UNKNOWN。退出码 = MISSING 数 > 0 ? 1 : 0。
核对类型:
  path   反引号里的仓库路径必须存在
  test   TestXxx / FuzzXxx 必须在某个 *_test.go 里有定义
  stdsym 形如 pkg.Symbol 且 pkg 是标准库包的，必须能被 `go doc` 解析
UNKNOWN = 脚本无法自动判定（外部项目路径、命令、示意代码），审读者要在台账里逐条处理。
"""
import glob
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(ROOT)

TICK = re.compile(r'`([^`\n]+)`')
PATH_PREFIX = ('exercises/', 'notes/', 'dnsproxi-go/', 'labs/', 'docs/', 'scripts/')


def std_packages():
    out = subprocess.run(['go', 'list', 'std'], capture_output=True, text=True).stdout.split()
    pub = [p for p in out if 'internal' not in p and not p.startswith(('vendor', 'cmd/'))]
    return set(pub) | {p.rsplit('/', 1)[-1] for p in pub}


def all_tests():
    names = set()
    for f in glob.glob('**/*_test.go', recursive=True):
        if 'node_modules' in f:
            continue
        names |= set(re.findall(r'^func ((?:Test|Fuzz|Benchmark)\w+)', open(f).read(), re.M))
    return names


_doc_cache = {}


def go_doc_ok(sym):
    if sym not in _doc_cache:
        r = subprocess.run(['go', 'doc', sym], capture_output=True, text=True)
        _doc_cache[sym] = r.returncode == 0
    return _doc_cache[sym]


def check(note, std, tests):
    rows = []
    text = open(note).read()
    in_fence = False
    for ln, line in enumerate(text.splitlines(), 1):
        if line.startswith('```'):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        for tok in TICK.findall(line):
            tok = tok.strip()
            if tok.startswith(PATH_PREFIX):
                p = tok.split()[0].rstrip('/,.;:)')
                ok = os.path.exists(p) or any(os.path.exists(p.replace('...', '')) for _ in [0])
                rows.append((ln, 'path', tok, 'OK' if ok else 'MISSING'))
            elif re.fullmatch(r'(Test|Fuzz)[A-Z]\w*', tok):
                rows.append((ln, 'test', tok, 'OK' if tok in tests else 'MISSING'))
            elif re.fullmatch(r'[a-z][a-z0-9/]*\.[A-Z]\w*(\.\w+)?', tok):
                pkg = tok.split('.')[0]
                if pkg in std:
                    rows.append((ln, 'stdsym', tok, 'OK' if go_doc_ok(tok) else 'UNKNOWN'))
    return rows


def unmentioned(note, text):
    """章号配对的练习目录里存在、但笔记没提到的测试（P6/P13/P17/P18 无独立目录，自动跳过）。"""
    m = re.match(r'notes/p(\d+(?:_\d+)?)-', note)
    rows = []
    for d in glob.glob('exercises/p*/'):
        dm = re.match(r'exercises/p(\d+(?:_\d+)?)(?![\d_])', d)
        if not (m and dm and dm.group(1) == m.group(1)):
            continue
        for f in glob.glob(d + '**/*_test.go', recursive=True):
            for name in re.findall(r'^func ((?:Test|Fuzz)\w+)', open(f).read(), re.M):
                if name not in text:
                    rows.append((0, 'unmentioned', f'{name} ({f})', 'MISSING'))
    return rows


def main():
    notes = sys.argv[1:] or sorted(glob.glob('notes/p*.md'))
    std, tests = std_packages(), all_tests()
    missing = 0
    for n in notes:
        rows = check(n, std, tests) + unmentioned(n, open(n).read())
        bad = [r for r in rows if r[3] == 'MISSING']
        unk = [r for r in rows if r[3] == 'UNKNOWN']
        missing += len(bad)
        print(f'{n}: {len(rows)} 条可核对断言, MISSING {len(bad)}, UNKNOWN {len(unk)}')
        for ln, kind, tok, st in bad + unk:
            print(f'  {st} L{ln} [{kind}] {tok}')
    sys.exit(1 if missing else 0)


main()
