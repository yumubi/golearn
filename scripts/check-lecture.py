"""校验讲课体手册章节：可运行代码块必须真的运行并与紧随其后的输出块一致。

    python3 -I scripts/check-lecture.py guide/handbook/04-interfaces.md [--min-lines 250 --max-lines 480 --min-examples 3]

约定：
  ```go          以 `package main` 开头：用 go run 运行，stdout 必须与紧随其后的 ```text 块一致
  ```go fail     以 `package main` 开头：必须编译失败，紧随其后的 ```text 块内容必须是编译器输出的子串
  其他 go 块     被视为片段，不运行
退出码 0 表示通过。
"""
import re, subprocess, sys, tempfile
from pathlib import Path

BLOCK = re.compile(r"^```(\w+)?[ \t]*(\w+)?[ \t]*\n(.*?)^```[ \t]*$", re.M | re.S)
REQUIRED = ["和你已知语言的差异", "小结", "常见坑", "判断题与答案", "在项目里", "想深入去哪查"]


def norm(s):
    return "\n".join(l.rstrip() for l in s.strip().splitlines())


def main():
    args = sys.argv[1:]
    if not args:
        raise SystemExit(__doc__)
    path = Path(args[0])
    minex = int(args[args.index("--min-examples") + 1]) if "--min-examples" in args else 3
    lo = int(args[args.index("--min-lines") + 1]) if "--min-lines" in args else 0
    hi = int(args[args.index("--max-lines") + 1]) if "--max-lines" in args else 10**9
    text = path.read_text()
    errs = []
    n = len(text.splitlines())
    if not lo <= n <= hi:
        errs.append(f"行数 {n} 不在 [{lo}, {hi}]")
    heads = re.findall(r"^## (.+)$", text, re.M)
    for r in REQUIRED:
        if not any(r in h for h in heads):
            errs.append(f"缺小节（标题需含）: {r}")
    blocks = list(BLOCK.finditer(text))
    ran = 0
    for i, b in enumerate(blocks):
        lang, flag, body = b.group(1), b.group(2), b.group(3)
        if lang != "go" or not body.lstrip().startswith("package main"):
            continue
        line = text[: b.start()].count("\n") + 1
        nxt = blocks[i + 1] if i + 1 < len(blocks) else None
        if not nxt or nxt.group(1) != "text":
            errs.append(f"第 {line} 行的可运行代码块后面必须紧跟 ```text 输出块")
            continue
        want = norm(nxt.group(3))
        with tempfile.TemporaryDirectory() as d:
            f = Path(d) / "main.go"
            f.write_text(body)
            p = subprocess.run(["go", "run", str(f)], capture_output=True, text=True, timeout=120)
        ran += 1
        if flag == "fail":
            if p.returncode == 0:
                errs.append(f"第 {line} 行标了 fail 却编译运行成功")
            elif want not in (p.stdout + p.stderr):
                errs.append(f"第 {line} 行编译器输出里找不到期望文本: {want[:60]!r}\n    实际: {(p.stderr or p.stdout)[:200]!r}")
        else:
            if p.returncode != 0:
                errs.append(f"第 {line} 行运行失败: {p.stderr[:200]!r}")
            elif norm(p.stdout) != want:
                errs.append(f"第 {line} 行输出不一致\n    文中: {want[:120]!r}\n    实际: {norm(p.stdout)[:120]!r}")
    if ran < minex:
        errs.append(f"可运行示例只有 {ran} 个，本章至少 {minex} 个")
    if heads and "建立的认知" in heads[0]:
        errs.append("开头不能是「这一节要建立的认知」，要用一个具体现象或问题开场")
    q = subprocess.run([sys.executable, "-I", "scripts/check-quotes.py", str(path), "--max-ratio", "0.5"], capture_output=True, text=True)
    if q.returncode != 0:
        errs.append("check-quotes 失败:\n" + "\n".join("    " + l for l in (q.stdout + q.stderr).strip().splitlines()[-10:]))
    if chr(0x2014) in text:
        errs.append("含 em-dash")
    if "downloads/" in text:
        errs.append("正文含 downloads/ 路径")
    if errs:
        print(f"FAIL {path}")
        for e in errs:
            print("  -", e)
        sys.exit(1)
    print(f"OK   {path} ({n} 行, 运行了 {ran} 个示例)")


main()
