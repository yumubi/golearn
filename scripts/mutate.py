#!/usr/bin/env python3
"""变异测试执行器：证明测试真的在守护行为，而不是恒绿。

读 scripts/mutations.json，逐条：备份文件 → 应用 find/replace → 跑 run → 必须失败，
且输出含 expect → 无论成败都还原文件。find 必须恰好命中 1 处（命中 0 或多处算脚本失败，
防止"替换没生效却报告失败测试"这种假阳性）。
退出码 = 存活变异体(测试仍绿)或脚本失败条数。
用法: python3 -I scripts/mutate.py [id ...]
"""
import json
import os
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.chdir(ROOT)


def run_one(m):
    path = m['file']
    orig = open(path, encoding='utf-8').read()
    n = orig.count(m['find'])
    if n != 1:
        return f"BROKEN  find 命中 {n} 处（必须恰好 1 处）"
    try:
        open(path, 'w', encoding='utf-8').write(orig.replace(m['find'], m['replace']))
        r = subprocess.run(m['run'], capture_output=True, text=True, timeout=300)
    finally:
        open(path, 'w', encoding='utf-8').write(orig)
    out = r.stdout + r.stderr
    if r.returncode == 0:
        # 并发时序测试在机器负载高时可能偶发"该失败却通过";重跑一次,仍通过才判幸存
        try:
            open(path, 'w', encoding='utf-8').write(orig.replace(m['find'], m['replace']))
            r2 = subprocess.run(m['run'], capture_output=True, text=True, timeout=300)
        finally:
            open(path, 'w', encoding='utf-8').write(orig)
        if r2.returncode != 0:
            return 'KILLED  首跑侥幸通过,复跑失败(时序偶发)'
        return 'SURVIVED  变异后测试仍然通过：测试没守住这个行为'
    if m.get('expect') and m['expect'] not in out:
        return f"WRONG-FAIL  失败了但输出不含 {m['expect']!r}（可能死于别的原因）"
    return 'KILLED'


def main():
    muts = json.load(open('scripts/mutations.json', encoding='utf-8'))
    want = set(sys.argv[1:])
    bad = 0
    for m in muts:
        if want and m['id'] not in want:
            continue
        res = run_one(m)
        bad += res != 'KILLED'
        print(f"{m['id']:28s} {res}")
    print(f'--- {len(muts)} 条, 未被杀死 {bad}')
    sys.exit(1 if bad else 0)


main()
