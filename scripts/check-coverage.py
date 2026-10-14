"""覆盖账：每个知识点必有去向，每章必须用到分给它的必读点。

    python3 -I scripts/check-coverage.py --dispo
    python3 -I scripts/check-coverage.py --outline work/outline/p30.md
    python3 -I scripts/check-coverage.py --chapter notes/p30-xxx.md

work/dispo.tsv（S2 产物）每行：  <单元>#<K编号>  TAB  P30 或 DROP  TAB  理由
  例：  TB24#K3<TAB>P30<TAB>方法表达式
        TB24#K9<TAB>DROP<TAB>只讲 GOPATH 时代写法，已过时
work/chapters.md 里用 `## P30 标题` 声明章节。
规则：
  --dispo    所有 DONE/PARTIAL digest 的每个 K 条目恰好在 dispo.tsv 出现一次；
             [必读] 不能 DROP；DROP 必须有 >=6 字理由；去向章必须已在 chapters.md 声明。
  --outline  outline 里 `K: TB24#K3, TB24#K4` 引用的条目，去向必须是该章；
             去向是该章的 [必读] 条目必须全部出现在 outline 里。
  --chapter  去向是该章的 [必读] 条目，章内必须有指向重叠位置的出处标签。
omp 不得修改本脚本。
"""
import importlib.util
import re
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location("_material", Path(__file__).with_name("_material.py"))
M = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(M)

TIERS = "讲提索"
MAX_LECTURE = 45
DISPO = M.WORK / "dispo.tsv"
CHAPTERS = M.WORK / "chapters.md"


def load_items():
    items = {}
    for p in sorted((M.WORK / "digest").glob("*.md")):
        t = p.read_text()
        m = re.match(r"# (\S+)", t)
        if not m:
            continue
        for kid, body, tag, lvl in M.digest_items(p):
            items[f"{m.group(1)}#{kid}"] = (body, tag, lvl)
    return items


def load_dispo():
    out, errs = {}, []
    if not DISPO.exists():
        return out, ["work/dispo.tsv 不存在"]
    for n, l in enumerate(DISPO.read_text().splitlines(), 1):
        if not l.strip() or l.startswith("#"):
            continue
        f = l.split("\t")
        if len(f) < 3:
            errs.append(f"dispo.tsv:{n} 需要三列（TAB 分隔）")
            continue
        if f[0] in out:
            errs.append(f"dispo.tsv:{n} 重复条目 {f[0]}")
        out[f[0]] = (f[1].strip(), f[2].strip())
    return out, errs


def declared_chapters():
    if not CHAPTERS.exists():
        return set()
    return set(re.findall(r"^## (P\d+)", CHAPTERS.read_text(), re.M))


def chapter_of(path):
    m = re.match(r"p(\d+)", Path(path).name)
    return f"P{m.group(1)}" if m else None


def main():
    a = sys.argv[1:]
    if not a:
        raise SystemExit(__doc__)
    items = load_items()
    dispo, errs = load_dispo()
    chs = declared_chapters()
    if a[0] == "--dispo":
        tally = {}
        for k, (body, tag, lvl) in items.items():
            if k not in dispo:
                errs.append(f"{k} 没有去向 〔{tag}〕 {body[:30]}")
        for k, (to, why) in dispo.items():
            if k not in items:
                errs.append(f"{k} 在 dispo.tsv 里但没有对应的 digest 条目")
                continue
            lvl = items[k][2]
            if to == "DROP":
                if lvl == "必读":
                    errs.append(f"{k} 是 [必读]，不能 DROP")
                if len(why) < 6:
                    errs.append(f"{k} DROP 需要理由")
            elif to not in chs:
                errs.append(f"{k} 去向 {to} 未在 work/chapters.md 声明（`## {to} 标题`）")
            elif why[:1] not in TIERS:
                errs.append(f"{k} 的理由必须以 讲/提/索 之一开头（讲=正文展开，提=一句话带过，索=只进 MATERIALS 索引）")
            else:
                tally[to] = tally.get(to, 0) + (why[:1] == "讲")
        for ch, n in sorted(tally.items()):
            if n > MAX_LECTURE:
                errs.append(f"{ch} 的 讲 条目 {n} 个，超过预算 {MAX_LECTURE}（一章 <= 450 行装不下）；多的降为 提 或 索")
    else:
        mode, path = a[0], a[1]
        ch = chapter_of(path)
        if ch is None:
            raise SystemExit("文件名需以 pNN 开头")
        mine = {k for k, (to, why) in dispo.items() if to == ch and why[:1] == "讲"}
        must = {k for k in mine if k in items and items[k][2] == "必读"}
        text = Path(path).read_text()
        if mode == "--outline":
            used = set()
            for m in re.finditer(r"^K:\s*(.+)$", text, re.M):
                used |= {x.strip() for x in m.group(1).split(",") if x.strip()}
            for k in used:
                if k not in dispo:
                    errs.append(f"{k} 不在 dispo.tsv")
                elif dispo[k][0] != ch:
                    errs.append(f"{k} 的去向是 {dispo[k][0]}，不是 {ch}")
            for k in sorted(must - used):
                errs.append(f"[必读] {k} 分给了 {ch} 但 outline 没用到: {items[k][0][:30]}")
        elif mode == "--chapter":
            tags = [t for t in (M.parse_tag(x) for x in M.TAG_RE.findall(text)) if t and t[0] != "meta"]
            for k in sorted(must):
                kt = M.parse_tag(items[k][1])
                if not any(M.overlap(kt, t) for t in tags):
                    errs.append(f"[必读] {k} 分给了 {ch} 但章内没有指向它的出处标签 〔{items[k][1]}〕: {items[k][0][:30]}")
        else:
            raise SystemExit(__doc__)
    if errs:
        print("FAIL")
        for e in errs[:60]:
            print("  -", e)
        if len(errs) > 60:
            print(f"  ... 共 {len(errs)} 条")
        sys.exit(1)
    print("OK")


main()
