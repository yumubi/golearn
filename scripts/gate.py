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
    return sorted({f for f in out if f.endswith(exts) and os.path.exists(f)
                   and 'node_modules' not in f and not f.startswith(('vendored/', '.github/'))})


# 1 全量 CI
if not FAST:
    r = sh('pnpm run ci')
    rec('pnpm run ci', r.returncode == 0, (r.stdout + r.stderr)[-600:])

# 2 notes → docs、guide/handbook → docs/handbook 镜像逐字节一致，且没有孤儿章
def mirror_bad(src_glob, dst_dir, src_dir):
    bad = []
    for n in sorted(glob.glob(src_glob)):
        d = dst_dir + '/' + os.path.basename(n)
        if not os.path.exists(d):
            bad.append(f'{d} 缺失')
            continue
        # 重建镜像应得到的完整文件（与 scripts/mirror.sh 同一格式）再整体比较
        s = open(n).read()
        # mirror.sh 格式：frontmatter、空行、h1、空行、原文去掉 h1 行
        head, body = s.split('\n', 1)
        expect = f'---\ntitle: {head[2:]}\n---\n\n{head}\n\n{body}'
        got = open(d).read()
        if got != expect:
            bad.append(f'{d} 与 {n} 不一致')
    # dnsproxi/index/table 是 docs 独有的手写页，不算孤儿
    for d in glob.glob(dst_dir + '/*.md'):
        if os.path.basename(d) in ('dnsproxi.md', 'index.md', 'table.md'):
            continue
        if not os.path.exists(src_dir + '/' + os.path.basename(d)):
            bad.append(f'{d} 无对应正本')
    return bad

bad = mirror_bad('notes/p*.md', 'docs', 'notes') + mirror_bad('guide/handbook/*.md', 'docs/handbook', 'guide/handbook')
rec('docs 镜像逐字节一致（notes + handbook）', not bad, '; '.join(bad))

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
bad = [f for f in changed('.md', '.go', '.ts', '.json', '.py', '.sh') if chr(0x2014) in open(f, encoding='utf-8').read()]
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
ban = re.compile(r'TODO|FIXME|待补|此处省略|（略）|同上|XXX|\.\.\.\s*省略|lorem')
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

# 7.5 手册讲课体：01 到 23 全部跑示例（24/25 按 QUEUE 参数；26 是路线页无 Go 示例）
LECT = {
    '22': ['--min-examples', '2'],   # 速通节，容器命令为主，正文只留 2 个可跑 Go 块
    '23': ['--min-examples', '1'],   # 速通节，头部声明不启集群，只 1 个可跑 Go 块
}
for f in sorted(glob.glob('guide/handbook/[0-2][0-9]-*.md')):
    if os.path.basename(f)[:2] == '26':
        continue  # 路线页，不是讲课体章节
    num = os.path.basename(f)[:2]
    if num in ('24', '25'):
        args = ['--min-lines', '200', '--max-lines', '450', '--min-examples', '0', '--profile', 'project']
    else:
        args = LECT.get(num, [])
    r = sh([sys.executable, '-I', 'scripts/check-lecture.py', f] + args)
    rec(f'讲课体 {os.path.basename(f)}', r.returncode == 0, (r.stdout + r.stderr).strip().splitlines()[1][:120] if r.returncode else '')

# 8 变异测试
if not FAST:
    r = sh([sys.executable, '-I', 'scripts/mutate.py'])
    rec('变异测试：全部被杀死', r.returncode == 0, r.stdout[-500:])

# 9 新章（P21 起）结构 + 入口同步
for n in sorted(glob.glob('notes/p2[1-9]-*.md')):
    t = open(n).read()
    if '速学版' in '\n'.join(t.splitlines()[:6]):
        need = {'## 先跑一下': 1, '## 要点': 1, '## 三个坑': 1, '## 在项目里': 1, '## 练习': 1, '## 想深入': 1, '预测': 1, '观察': 1, '改造': 1}
        miss = [f'{k}≥{v}(实{t.count(k)})' for k, v in need.items() if t.count(k) < v]
        if t.count('```') < 4:
            miss.append('代码块<2')
    else:
        need = {'这章学完你能干什么': 1, 'Take-away': 1, '章末参考': 1, '预测': 2, '观察': 1, '改造': 1, 'Rust': 6}
        miss = [f'{k}≥{v}(实{t.count(k)})' for k, v in need.items() if t.count(k) < v]
        if len(re.findall(r'^## ', t, re.M)) < 4:
            miss.append('"## " 正文小节<4')
        if t.count('```') < 16:
            miss.append(f'代码块<8(实{t.count("```") // 2})')
    rec(f'{n} 结构', not miss, ', '.join(miss))
    slug = re.match(r'notes/(p\d+)', n).group(1).upper()
    entries = ['README.md', 'docs/table.md', 'docs/.vitepress/config.ts']
    miss = [e for e in entries if slug not in open(e).read()]
    rec(f'{slug} 入口同步（README/table/config）', not miss, ', '.join(miss))

# 10 测试数量不得减少；新增 t.Skip 必须带 `skip-ok: 理由`
def count_tests(ref):
    if ref:
        return int(sh(f"git grep -h '^func Test' {ref} -- '*_test.go' | wc -l").stdout.strip())
    files = sh("git ls-files -co --exclude-standard -- '*_test.go'").stdout.split()
    return sum(len(re.findall(r'^func Test', open(f).read(), re.M)) for f in files if os.path.exists(f))


b, n = count_tests(BASE), count_tests(None)
rec(f'测试函数总数不减少（BASE {b} -> 现 {n}）', n >= b)
bad = []
for f in changed('.go'):
    for i, line in enumerate(open(f).read().splitlines(), 1):
        if 't.Skip' in line and 'skip-ok:' not in line:
            old = sh(f'git show {BASE}:{f}').stdout if sh(f'git cat-file -e {BASE}:{f}').returncode == 0 else ''
            if line.strip() not in old:
                bad.append(f'{f}:{i}')
rec('无未说明的新增 t.Skip', not bad, ', '.join(bad))

# 11 审读台账：范围见 scripts/audit-scope.txt（slug:最少条数），每条必须有可复核的命令/出处
scope = [l.split('#')[0].strip() for l in open('scripts/audit-scope.txt') if l.split('#')[0].strip()]
have_audit = os.path.isdir('audit')
for item in scope:
    slug, _, mn = item.partition(':')
    mn = int(mn or 20)
    f = f'audit/{slug}.md'
    if not have_audit:
        rec(f'台账 {f}', True, 'audit/ 不在发布范围（本地核对工具），跳过')
        continue
    if not os.path.exists(f):
        rec(f'台账 {f}', False, '文件不存在')
        continue
    rows = [[c.strip() for c in l.strip().strip('|').split('|')] for l in open(f).read().splitlines() if re.match(r'^\|\s*\d+\s*\|', l)]
    good = [r for r in rows if len(r) >= 6 and r[5] in ('OK', 'FIXED') and len(r[3]) >= 8 and len(r[4]) >= 3]
    fixed = sum(r[5] == 'FIXED' for r in rows if len(r) >= 6)
    probs = []
    if len(good) < mn:
        probs.append(f'合格条目 {len(good)} < {mn}（需 状态 OK/FIXED，命令/出处≥8字，结果非空）')
    if fixed == 0 and len(re.findall(r'^\s*[-*] ', open(f).read().split('## 零发现自检')[-1], re.M)) < 10 if '## 零发现自检' in open(f).read() else fixed == 0:
        probs.append('FIXED=0 时必须有 "## 零发现自检" 小节，且列出≥10 条额外核查')
    rec(f'台账 {f}（合格 {len(good)}，FIXED {fixed}）', not probs, '; '.join(probs))

ok = all(o for _, o in results)
print(f"\nGATE: {'PASS' if ok else 'FAIL'}（{sum(not o for _, o in results)} 项失败）")
sys.exit(0 if ok else 1)
