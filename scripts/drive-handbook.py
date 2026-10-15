"""驱动 omp 无头模式逐节把手册改写成讲课体，并验收、提交。

    python3 -I scripts/drive-handbook.py 05          只做 05 节（试点）
    python3 -I scripts/drive-handbook.py all         01 到 23 依次做（跳过已通过的）

每节最多 3 次尝试：第 1 次按任务文件写，失败则把检查输出附上再来。
通过则 git add 该文件并提交；3 次都失败写入 work/BLOCKED-H.md 并继续下一节。
日志：work/drive-log.txt
"""
import re, subprocess, sys, time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
RANGE = {"深入": (380, 560), "标准": (260, 420), "速通": (170, 300)}
MINEX = {"01": 2, "02": 1, "22": 0, "23": 0, "17": 1, "18": 1, "19": 1, "20": 1, "21": 1}
SAMPLE = "04"
LOG = ROOT / "work/drive-log.txt"


def log(msg):
    line = time.strftime("%H:%M:%S ") + msg
    print(line, flush=True)
    with LOG.open("a") as f:
        f.write(line + "\n")


def sections():
    return sorted(p.name[:2] for p in (ROOT / "work/old").glob("[0-9][0-9]-*.md") if int(p.name[:2]) <= 23)


def info(n):
    old = next((ROOT / "work/old").glob(f"{n}-*.md"))
    text = old.read_text()
    level = re.search(r"详细程度：(深入|标准|速通)", text).group(1)
    title = text.splitlines()[0].lstrip("# ").strip()
    lo, hi = RANGE[level]
    return old, title, level, lo, hi, MINEX.get(n, 3)


def check_cmd(n):
    _, _, _, lo, hi, mx = info(n)
    f = f"guide/handbook/{next((ROOT / 'work/old').glob(f'{n}-*.md')).name}"
    return f, ["python3", "-I", "scripts/check-lecture.py", f, "--min-lines", str(lo), "--max-lines", str(hi), "--min-examples", str(mx)]


def run_check(n):
    f, cmd = check_cmd(n)
    p = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True, timeout=900)
    out = p.stdout + p.stderr
    e = subprocess.run(["npx", "eslint", f], cwd=ROOT, capture_output=True, text=True, timeout=300)
    if e.returncode != 0 or e.stdout.strip():
        out += "\neslint:\n" + (e.stdout + e.stderr)[-1500:]
        return False, out
    return p.returncode == 0, out


def task_file(n, extra=""):
    old, title, level, lo, hi, mx = info(n)
    f = f"guide/handbook/{old.name}"
    body = f"""# 任务 H{n}：把 {f} 改写成讲课体

先读 `PLAN-OMP-6.md`（规格），再读样板 `guide/handbook/{SAMPLE}-interfaces.md`（写法就照它），再读旧版 `work/old/{old.name}`（要保留的事实都在里面），再读它「材料」一行列出的原文（`work/text/<单元id>.txt`；单元 id 里的冒号在文件名里怎么写，用 `ls work/text` 查）。

目标文件：`{f}`（直接覆盖）。章节：{title}。详细程度：{level}，行数 {lo} 到 {hi}，可运行示例至少 {mx} 个。

做完后自己执行验收并修到通过：

```
python3 -I scripts/check-lecture.py {f} --min-lines {lo} --max-lines {hi} --min-examples {mx}
npx eslint {f}
```

两条都通过（前者输出 OK，后者无输出）才算完成。完成后不要提问，直接结束。不要 git commit。
{extra}"""
    p = ROOT / f"work/tasks/H{n}.md"
    p.write_text(body)
    return p


def run_omp(n, extra=""):
    t = task_file(n, extra)
    cmd = ["omp", "-p", "--no-session", "--auto-approve", "--model", "focus-glm52/glm5.1", "--max-time", "45m", f"@{t.relative_to(ROOT)}", "按任务文件执行。"]
    log(f"H{n}: omp 开始")
    p = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True)
    log(f"H{n}: omp 结束 rc={p.returncode}")


def do(n):
    f, _ = check_cmd(n)
    ok, _ = run_check(n)
    if ok and subprocess.run(["git", "diff", "--quiet", "HEAD", "--", f], cwd=ROOT).returncode != 0:
        pass  # 已通过但未提交，下面统一提交
    elif ok:
        log(f"H{n}: 已通过，跳过")
        return True
    extra = ""
    for attempt in range(1, 4):
        if not ok:
            run_omp(n, extra)
            ok, out = run_check(n)
            if not ok:
                log(f"H{n}: 第 {attempt} 次未通过\n" + out[-800:])
                extra = "\n上一次检查失败，输出如下，只修这些问题，别的不要大改：\n```\n" + out[-2500:] + "\n```\n"
        if ok:
            break
    if not ok:
        with (ROOT / "work/BLOCKED-H.md").open("a") as fh:
            fh.write(f"## H{n}\n最后一次检查输出：\n```\n{out[-1500:]}\n```\n")
        log(f"H{n}: BLOCKED")
        return False
    title = info(n)[1]
    subprocess.run(["git", "add", f], cwd=ROOT, check=True)
    msg = f"H{n} {title}：改写成讲课体\n\nCo-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
    r = subprocess.run(["git", "commit", "-q", "-m", msg], cwd=ROOT, capture_output=True, text=True)
    log(f"H{n}: 通过并提交 rc={r.returncode} {r.stderr.strip()[-200:]}")
    return True


def main():
    arg = sys.argv[1] if len(sys.argv) > 1 else ""
    todo = sections() if arg == "all" else [arg.zfill(2)]
    todo = [n for n in todo if n != SAMPLE]
    for n in todo:
        do(n)


main()
