#!/usr/bin/env python3
"""交付闸门：所有能机器判定的验收项一次跑完，只认命令输出，不认自述。

用法: BASE=<commit> python3 -I scripts/gate.py [--fast]
  BASE  本轮工作的起点提交（默认读 scripts/BASE）。"changed files" 指 git diff BASE。
  --fast 跳过 pnpm run ci 与变异测试（迭代时用；交付前必须不带 --fast 跑一次）。
退出码 = 失败项数。最后一行 `GATE: PASS` 才算过。
"""
import glob
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(ROOT)
FAST = '--fast' in sys.argv
BASE = os.environ.get('BASE') or open('scripts/BASE').read().strip()
results = []


def sh(cmd, **kw):
    return subprocess.run(cmd, capture_output=True, text=True, shell=isinstance(cmd, str), **kw)


def rec(name, ok, detail=''):
    results.append((name, ok))
    print(f"[{'PASS' if ok else 'FAIL'}] {name}" + (f'\n       {detail}' if detail and not ok else ''))


def changed(*exts):
    out = sh(f'git diff --name-only {BASE} && git ls-files --others --exclude-standard').stdout.split()
    return sorted({f for f in out if f.endswith(exts) and os.path.exists(f) and 'node_modules' not in f})


# 1 全量 CI
if not FAST:
    r = sh('pnpm run ci')
    rec('pnpm run ci', r.returncode == 0, (r.stdout + r.stderr)[-600:])

# 2 notes → docs 镜像逐字节一致，且 docs 里没有孤儿章
bad = []
for n in sorted(glob.glob('notes/p*.md')):
    d = 'docs/' + os.path.basename(n)
    if not os.path.exists(d):
        bad.append(f'{d} 缺失')
        continue
    src = open(n).read().split('\n', 1)[1]
    dst = open(d).read().split('\n', 6)[6]
    if src != dst:
        bad.append(f'{d} 与 {n} 不一致')
for d in glob.glob('docs/p*.md'):
    if not os.path.exists('notes/' + os.path.basename(d)):
        bad.append(f'{d} 无对应 notes')
rec('docs 镜像逐字节一致', not bad, '; '.join(bad))

# 3 slop-lint：改动过的 md 必须 0 violations
LINT = '/Users/akka/.agents/skills/no-slop/slop-lint.mjs'
bad = []
for f in changed('.md'):
    if f.startswith('docs/.vitepress'):
        continue
    o = sh(['node', LINT, f]).stdout
    m = re.search(r'violations=(\d+)', o)
    if not m or int(m.group(1)) != 0:
        bad.append(f'{f}: {o.strip()[-80:]}')
rec('slop-lint 0 violations（改动的 md）', not bad, '; '.join(bad))

# 4 em-dash（U+2014）：改动的文本文件里不得出现
bad = [f for f in changed('.md', '.go', '.ts', '.json', '.py', '.sh') if '—' in open(f, encoding='utf-8').read()]
rec('零 em-dash', not bad, ', '.join(bad))

# 5 Go 1.22 整数 range：改动的 .go 里 `for i := 0; i < n; i++` 必须写 range，例外行加 `// range-ok: 理由`
pat = re.compile(r'for\s+\w+\s*:=\s*0\s*;\s*\w+\s*<\s*[^;]+;\s*\w+\+\+')
bad = []
for f in changed('.go'):
    for i, line in enumerate(open(f).read().splitlines(), 1):
        if pat.search(line) and 'range-ok:' not in line:
            bad.append(f'{f}:{i}')
rec('整数 range 写法', not bad, ', '.join(bad[:10]))

# 6 禁用的敷衍标记：改动的 notes/exercises 不得含占位语
ban = re.compile(r'TODO|FIXME|待补|此处省略|同上|略\s*$|XXX|\.\.\.\s*省略|lorem')
bad = []
for f in changed('.md', '.go'):
    if f.startswith(('HANDOFF', 'PLAN')) or f.endswith('_test.go') and False:
        continue
    for i, line in enumerate(open(f).read().splitlines(), 1):
        if ban.search(line):
            bad.append(f'{f}:{i}: {line.strip()[:50]}')
rec('无占位/敷衍标记', not bad, '; '.join(bad[:6]))

# 7 引用对账
r = sh([sys.executable, '-I', 'scripts/audit-claims.py'])
tail = '\n'.join(l for l in r.stdout.splitlines() if 'MISSING' in l and 'MISSING 0' not in l or l.startswith('  MISSING'))
rec('notes 引用对账（路径/测试名/反向遗漏）无 MISSING', r.returncode == 0, tail)

# 8 变异测试
if not FAST:
    r = sh([sys.executable, '-I', 'scripts/mutate.py'])
    rec('变异测试：全部被杀死', r.returncode == 0, r.stdout[-500:])

# 9 新章（P21 起）结构 + 入口同步
for n in sorted(glob.glob('notes/p2[1-9]-*.md')):
    t = open(n).read()
    need = {'这章学完你能干什么': 1, 'Take-away': 1, '章末参考': 1, '预测': 2, '观察': 1, '改造': 1, 'Rust': 6}
    miss = [f'{k}≥{v}(实{t.count(k)})' for k, v in need.items() if t.count(k) < v]
    if len(re.findall(r'^## .*问题', t, re.M)) < 3:
        miss.append('"## ...问题" 小节<3')
    if t.count('```') < 16:
        miss.append(f'代码块<8(实{t.count("```") // 2})')
    rec(f'{n} 结构', not miss, ', '.join(miss))
    slug = re.match(r'notes/(p\d+)', n).group(1).upper()
    entries = ['INDEX.md', 'README.md', 'docs/index.md', 'docs/table.md', 'docs/.vitepress/config.ts']
    miss = [e for e in entries if slug not in open(e).read()]
    rec(f'{slug} 五处入口同步', not miss, ', '.join(miss))

ok = all(o for _, o in results)
print(f"\nGATE: {'PASS' if ok else 'FAIL'}（{sum(not o for _, o in results)} 项失败）")
sys.exit(0 if ok else 1)
