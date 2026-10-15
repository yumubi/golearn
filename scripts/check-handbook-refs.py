"""手册内部引用与路径检查。

    python3 -I scripts/check-handbook-refs.py [文件...]      默认检查 guide/handbook/*.md

检查：
  1. 形如 `NN-xxx.md` 的链接指向的文件存在
  2. 「NN 节」「第 NN 节」的编号在 01 到 26 之间，且后面紧跟的标题词（如「NN 节（泛型）」暂不检查）
  3. 反引号里以 dnsproxi-go/ exercises/ notes/ guide/ labs/ scripts/ 开头的路径真实存在（支持 * 通配和 :行号 后缀）
  4. 各节头部「约 N 分钟」应约等于行数/9（±25%）；各节之和，与 README.md 和 26-route.md 里写的总时长（小时）相符（±1 小时）
退出码 0 表示通过。
"""
import glob, re, sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
HB = ROOT / "guide/handbook"
files = [Path(a) for a in sys.argv[1:]] or sorted(HB.glob("*.md"))
errs = []
nums = {p.name[:2] for p in HB.glob("[0-9][0-9]-*.md")}
for f in files:
    f = f if f.is_absolute() else ROOT / f
    text = f.read_text()
    # 去掉围栏代码块，避免示例里的路径干扰
    prose = re.sub(r"```.*?```", "", text, flags=re.S)
    for m in re.finditer(r"\b(\d\d-[a-z0-9-]+\.md)\b", prose):
        if not (HB / m.group(1)).exists() and not (ROOT / "notes" / m.group(1)).exists():
            errs.append(f"{f.name}: 链接目标不存在 {m.group(1)}")
    for m in re.finditer(r"第?\s?(\d\d)\s?节", prose):
        if m.group(1) not in nums:
            errs.append(f"{f.name}: 引用了不存在的节 {m.group(1)}")
    for m in re.finditer(r"`((?:dnsproxi-go|exercises|notes|guide|labs|scripts)/[^`\s]+)`", prose):
        p = re.sub(r":\d+(-\d+)?$", "", m.group(1)).rstrip("/，。、；")
        if not glob.glob(str(ROOT / p)):
            errs.append(f"{f.name}: 路径不存在 {p}")

full = not sys.argv[1:]
allp = sorted(HB.glob("[0-9][0-9]-*.md"))
mins = 0
for p in allp:
    lines = p.read_text().splitlines()
    m = re.search(r"约\s*(\d+)\s*分钟", "\n".join(lines[:6]))
    if m:
        mins += int(m.group(1))
        want = len(lines) / 9
        check_this = full or any(Path(a).name == p.name for a in sys.argv[1:])
        if check_this and not 0.75 * want <= int(m.group(1)) <= 1.25 * want:
            errs.append(f"{p.name}: 头部「约 {m.group(1)} 分钟」与篇幅不符；{len(lines)} 行应约 {round(want / 5) * 5} 分钟（行数除以 9，允许上下 25%）")
    elif full or any(Path(a).name == p.name for a in sys.argv[1:]):
        if p.name[:2] <= "25":
            errs.append(f"{p.name}: 头部前 6 行里没有「约 N 分钟」")
hours = mins / 60
if full:
    for name in ("README.md", "26-route.md"):
        t = (HB / name).read_text()
        hs = [float(x) for x in re.findall(r"约\s*(\d+(?:\.\d+)?)\s*小时", t)]
        if not any(abs(h - hours) <= 1 for h in hs):
            errs.append(f"{name}: 总阅读时间应约 {hours:.1f} 小时（各节头部「约 N 分钟」之和 {mins} 分钟），文中写的是 {hs or '没有'}")

# 项目节里写成 `Func()` 的函数名必须真的在源码中出现
src = ""
for d in ("dnsproxi-go", "minifeed"):
    for g in (ROOT / d).rglob("*.go"):
        src += g.read_text(errors="ignore")
for f in files:
    f = f if f.is_absolute() else ROOT / f
    if f.name[:2] in ("24", "25"):
        prose = re.sub(r"```.*?```", "", f.read_text(), flags=re.S)
        for m in re.finditer(r"`(?:[A-Za-z_]\w*\.)?([A-Za-z_]\w*)\(\)`", prose):
            if not re.search(rf"\b{m.group(1)}\b", src):
                errs.append(f"{f.name}: 函数 {m.group(1)}() 在 dnsproxi-go 和 minifeed 源码里找不到")
if errs:
    print("FAIL")
    for e in dict.fromkeys(errs):
        print("  -", e)
    sys.exit(1)
print(f"OK 引用与路径一致" + (f"，总阅读约 {hours:.1f} 小时" if full else ""))
