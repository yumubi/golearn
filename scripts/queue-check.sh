#!/usr/bin/env bash
# 读 work/QUEUE.md，逐项运行 check 块，按结果自动勾选，并打印下一步。
#   bash scripts/queue-check.sh          检查全部
#   bash scripts/queue-check.sh Q2       只检查某一项
set -u
cd "$(dirname "$0")/.."
exec python3 -I - "$@" <<'PY'
import re, subprocess, sys
from pathlib import Path

Q = Path("work/QUEUE.md")
BLOCKED = Path("work/BLOCKED.md")
only = sys.argv[1] if len(sys.argv) > 1 else None
text = Q.read_text()
parts = re.split(r"(?m)^(?=## Q\d+ )", text)
head, tasks = parts[0], parts[1:]
blocked = BLOCKED.read_text() if BLOCKED.exists() else ""

def check_of(t):
    m = re.search(r"```check\n(.*?)```", t, re.S)
    return m.group(1) if m else None

out, nxt, results = [], None, []
for i, t in enumerate(tasks):
    qid = re.match(r"## (Q\d+)", t).group(1)
    title = t.splitlines()[0][3:]
    cmd = check_of(t)
    if cmd is None:
        results.append((qid, "NOCHECK", "")); continue
    if only and only != qid:
        results.append((qid, "SKIP", "")); continue
    p = subprocess.run(["bash", "-e", "-c", cmd], capture_output=True, text=True, timeout=900)
    ok = p.returncode == 0
    tasks[i] = re.sub(r"- \[[ x]\]", "- [x]" if ok else "- [ ]", t, count=1)
    tail = "" if ok else "\n".join((p.stdout + p.stderr).strip().splitlines()[-25:])
    results.append((qid, "PASS" if ok else "FAIL", tail))
    if not ok and nxt is None and qid not in blocked:
        nxt = qid

Q.write_text(head + "".join(tasks))
for qid, st, tail in results:
    print(f"{st:7} {qid}")
    if tail:
        print("\n".join("    | " + l for l in tail.splitlines()))
left = [q for q, st, _ in results if st == "FAIL"]
if not left:
    print("\nALL-DONE")
elif nxt:
    print(f"\nNEXT: {nxt}（{len(left)} 项未过：{', '.join(left)}）")
else:
    print(f"\nSTOP: 未过的项都已记入 work/BLOCKED.md：{', '.join(left)}")
sys.exit(0 if not left else 1)
PY
