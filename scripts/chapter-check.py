"""一键自检一章（PLAN-OMP-4）。omp 不得修改本脚本。

    python3 -I scripts/chapter-check.py p35          # 单章：引文、覆盖、行数、练习包
    python3 -I scripts/chapter-check.py p35 --gate   # 另跑完整 gate（慢）

退出码 0 才算这章可以提交。输出每项 PASS/FAIL 和原始尾部，直接贴进 work/STATUS.md。
"""
import glob
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def sh(cmd, cwd=ROOT, timeout=900):
    r = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    return r.returncode, (r.stdout + r.stderr).strip()


def main():
    a = [x for x in sys.argv[1:] if not x.startswith("--")]
    if not a:
        raise SystemExit(__doc__)
    ch = a[0].lower()
    notes = glob.glob(str(ROOT / "notes" / f"{ch}-*.md"))
    if len(notes) != 1:
        raise SystemExit(f"FAIL 找不到唯一的 notes/{ch}-*.md: {notes}")
    note = Path(notes[0])
    rel = note.relative_to(ROOT)
    bad = 0

    def report(name, ok, out=""):
        nonlocal bad
        bad += (not ok)
        print(f"[{'PASS' if ok else 'FAIL'}] {name}")
        if not ok or "-v" in sys.argv:
            print("   " + "\n   ".join(out.splitlines()[-12:]))

    n = len(note.read_text().splitlines())
    report(f"行数 {n} <= 450", n <= 450)
    rc, out = sh(["python3", "-I", "scripts/check-quotes.py", str(rel)])
    report("check-quotes", rc == 0, out)
    rc, out = sh(["python3", "-I", "scripts/check-coverage.py", "--chapter", str(rel)])
    report("check-coverage --chapter", rc == 0, out)
    mirror = ROOT / "docs" / note.name
    report("docs 镜像存在", mirror.exists())
    text = note.read_text()
    report("无 downloads/ 路径", "downloads/" not in text)
    report("无 em-dash", chr(0x2014) not in text)
    for d in sorted(glob.glob(str(ROOT / "exercises" / f"{ch}*"))):
        dd = Path(d)
        rc, out = sh(["go", "vet", "./..."], cwd=dd)
        report(f"go vet {dd.name}", rc == 0, out)
        rc, out = sh(["go", "test", "-race", "-count=1", "./..."], cwd=dd)
        report(f"go test -race {dd.name}", rc == 0, out)
        names = set(re.findall(r"func (Test\w+|Benchmark\w+)\(", "".join(p.read_text() for p in dd.rglob("*_test.go"))))
        missing = [t for t in re.findall(r"`((?:Test|Benchmark)\w+)`", text) if t not in names]
        report(f"正文提到的测试名都存在 {dd.name}", not missing, "缺: " + ",".join(missing))
    if "--gate" in sys.argv:
        rc, out = sh(["python3", "-I", "scripts/gate.py"], timeout=3000)
        report("gate.py 完整", rc == 0 and "GATE: PASS" in out, out)
    print("CHAPTER-CHECK: " + ("PASS" if not bad else f"FAIL({bad})"))
    sys.exit(1 if bad else 0)


main()
