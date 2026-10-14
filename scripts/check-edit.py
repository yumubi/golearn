"""核验速学版章节是否严格按编辑单（rewrite/edit/<章>.md）组装。omp 不得修改本脚本。

    python3 -I scripts/check-edit.py rewrite/edit/p2-lang-core.md

检查：
  1. 章文件必须有章头标记行（含"速学版"与"读完约 N 分钟"）和六个固定小节：
     先跑一下 / 要点 / 三个坑 / 在项目里 / 练习 / 想深入
  2. “要点”下的三级标题必须是 `### N. 标题`，数量、顺序、标题都与编辑单一致，不得增删。
  3. 每个要点小节里必须出现该要点编辑单里至少一个出处标签。
  4. “三个坑”的条数与编辑单一致，每条至少带一个出处标签。
  5. “想深入”必须提到编辑单 deep 里列的每个原材料单元。
  6. “在项目里”必须含至少一个反引号路径，或明说“这里没有现成例子”。
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SECTIONS = ["先跑一下", "要点", "三个坑", "在项目里", "练习", "想深入"]


def parse(path):
    d = {"points": [], "pitfalls": [], "deep": []}
    cur = None
    for raw in Path(path).read_text().splitlines():
        line = raw.rstrip()
        m = re.match(r"^(\w+):\s*(.*)$", line)
        if m and not line.startswith(" "):
            cur = m.group(1)
            if cur in ("points", "pitfalls", "lookup", "dropped"):
                continue
            d[cur] = m.group(2)
            continue
        if cur in ("points", "pitfalls") and re.match(r"^\d+ \|", line):
            parts = [x.strip() for x in line.split("|")]
            n = int(parts[0])
            tags = re.findall(r"〔[^〕]+〕", parts[2] if len(parts) > 2 else "")
            if cur == "points":
                d["points"].append((n, parts[1], tags))
            else:
                d["pitfalls"].append((n, parts[1], tags))
    d["deep_units"] = re.findall(r"\b(?:TB\w+|W\d+-\d+\w*|WX-\w+|C:[\w/_]+|K:[\w\-]+)\b", d.get("deep", "").split("；")[0].split(";")[0])
    return d


def main():
    if len(sys.argv) < 2:
        raise SystemExit(__doc__)
    e = parse(sys.argv[1])
    note = ROOT / "notes" / (e["chapter"] + ".md")
    errs = []
    if not note.exists():
        raise SystemExit(f"FAIL {note} 不存在")
    text = note.read_text()
    head = "\n".join(text.splitlines()[:6])
    if "速学版" not in head or not re.search(r"读完约\s*\d+\s*分钟", head):
        errs.append("章头 6 行内要有“速学版”与“读完约 N 分钟”标记")
    secs = re.split(r"^## ", text, flags=re.M)[1:]
    body = {s.split("\n", 1)[0].strip(): s for s in secs}
    for name in SECTIONS:
        if not any(k.startswith(name) for k in body):
            errs.append(f"缺小节 ## {name}")
    get = lambda name: next((v for k, v in body.items() if k.startswith(name)), "")
    pts = re.split(r"^### ", get("要点"), flags=re.M)[1:]
    if len(pts) != len(e["points"]):
        errs.append(f"要点数 {len(pts)} 与编辑单 {len(e['points'])} 不一致（不得增删）")
    for (n, title, tags), sec in zip(e["points"], pts):
        h = sec.split("\n", 1)[0].strip()
        if not h.startswith(f"{n}."):
            errs.append(f"要点 {n} 的标题应以 `### {n}.` 开头，实为 {h[:20]!r}")
        key = re.sub(r"[：:，,（）() ]", "", title)[:8]
        if key not in re.sub(r"[：:，,（）() ]", "", h):
            errs.append(f"要点 {n} 标题与编辑单不符，应含“{title[:12]}…”")
        if tags and not any(t in sec for t in tags):
            errs.append(f"要点 {n} 缺出处标签（编辑单给的是 {''.join(tags)}）")
    pit = [l for l in get("三个坑").splitlines() if re.match(r"^\s*(?:[-*]|\d+\.|\|(?!\s*-))", l) and "出处" not in l and not re.match(r"^\|\s*坑", l)]
    if len(pit) != len(e["pitfalls"]):
        errs.append(f"坑的条数 {len(pit)} 与编辑单 {len(e['pitfalls'])} 不一致")
    for l in pit:
        if "〔" not in l:
            errs.append(f"坑缺出处标签: {l[:30]}")
    deep = get("想深入")
    for u in e["deep_units"]:
        if u not in deep:
            errs.append(f"想深入 没提到原材料单元 {u}")
    proj = get("在项目里")
    if "`" not in proj and "这里没有现成例子" not in proj:
        errs.append("在项目里 要有反引号路径，或明说“这里没有现成例子”")
    print(("FAIL " if errs else "OK   ") + str(note.relative_to(ROOT)))
    for x in errs:
        print("  -", x)
    sys.exit(1 if errs else 0)


main()
