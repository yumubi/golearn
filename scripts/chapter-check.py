"""一键自检一章（PLAN-OMP-4）。omp 不得修改本脚本。

    python3 -I scripts/chapter-check.py p35          # 单章：引文、覆盖、行数、练习包
    python3 -I scripts/chapter-check.py p35 --gate   # 另跑完整 gate（慢）
    选项（PLAN-OMP-5 速学版）：--max-lines N（默认 450）、--max-chars N（正文去代码块后的字数，默认不限）、
    --max-ratio F（摘录占比，默认 0.30）、--no-coverage（不查 dispo 覆盖账）

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
    argv = sys.argv[1:]

    def opt(name, default, cast):
        if name in argv:
            i = argv.index(name)
            v = cast(argv[i + 1])
            del argv[i : i + 2]
            return v
        return default

    max_lines = opt("--max-lines", 450, int)
    max_chars = opt("--max-chars", 0, int)
    max_ratio = opt("--max-ratio", 0.30, float)
    a = [x for x in argv if not x.startswith("--")]
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
        if not ok or "-v" in argv:
            print("   " + "\n   ".join(out.splitlines()[-12:]))

    n = len(note.read_text().splitlines())
    report(f"行数 {n} <= {max_lines}", n <= max_lines)
    if max_chars:
        body = re.sub(r"```.*?```", "", note.read_text(), flags=re.S)
        nc = len(re.sub(r"\s+", "", body))
        report(f"正文字数 {nc} <= {max_chars}", nc <= max_chars)
    rc, out = sh(["python3", "-I", "scripts/check-quotes.py", str(rel), "--max-ratio", str(max_ratio)])
    report("check-quotes", rc == 0, out)
    if "--no-coverage" not in argv:
        rc, out = sh(["python3", "-I", "scripts/check-coverage.py", "--chapter", str(rel)])
        report("check-coverage --chapter", rc == 0, out)
    mirror = ROOT / "docs" / note.name
    report("docs 镜像存在", mirror.exists())
    text = note.read_text()
    report("无 downloads/ 路径", "downloads/" not in text)
    report("无 em-dash", chr(0x2014) not in text)
    for d in sorted(x for x in glob.glob(str(ROOT / "exercises" / f"{ch}*")) if re.match(rf"{ch}(?![0-9_])", Path(x).name)):
        dd = Path(d)
        rc, out = sh(["go", "vet", "./..."], cwd=dd)
        report(f"go vet {dd.name}", rc == 0, out)
        rc, out = sh(["go", "test", "-race", "-count=1", "./..."], cwd=dd)
        report(f"go test -race {dd.name}", rc == 0, out)
        names = set(re.findall(r"func (Test\w+|Benchmark\w+)\(", "".join(p.read_text() for p in dd.rglob("*_test.go"))))
        missing = [t for t in re.findall(r"`((?:Test|Benchmark)\w+)`", text) if t not in names]
        report(f"正文提到的测试名都存在 {dd.name}", not missing, "缺: " + ",".join(missing))
    if "--gate" in argv:
        rc, out = sh(["python3", "-I", "scripts/gate.py"], timeout=3000)
        report("gate.py 完整", rc == 0 and "GATE: PASS" in out, out)
    print("CHAPTER-CHECK: " + ("PASS" if not bad else f"FAIL({bad})"))
    sys.exit(1 if bad else 0)


main()
