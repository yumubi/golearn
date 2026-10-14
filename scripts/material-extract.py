"""S0：建 inventory，抽 PDF 文字层。幂等，可重复跑。

    python3 -I scripts/material-extract.py [--force]

产物（全在 work/，已被 .gitignore）：
  work/inventory.json      所有单元
  work/text/<id>.txt       PDF 文字，每页前插 [[pN]]
  work/text/<id>.txt       代码/krm 单元：文件清单（路径 行数），不含内容
读取 downloads/ 是只读的，不执行其中任何内容。
"""
import importlib.util
import json
import re
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location("_material", Path(__file__).with_name("_material.py"))
M = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(M)

CN = {"一": 1, "二": 2, "三": 3, "四": 4, "五": 5, "六": 6, "七": 7, "八": 8, "九": 9}
TB_EXTRA = [
    ("开篇词", "TB00"), ("结束语", "TB99"), ("海纳", "TBX01"), ("聊聊Go1", "TBX02"),
    ("孔令飞", "TBX03"), ("大明", "TBX04"), ("最近大热的Go泛型", "TBX05"), ("罗杰", "TBX06"),
    ("徐祥曦", "TBX07"), ("他山之石", "TBX08"), ("叶剑峰", "TBX09"), ("曹春晖", "TBX10"),
    ("GoModule的作者", "TBX11"),
]


def cn_num(s):
    if s == "十":
        return 10
    if s.startswith("十"):
        return 10 + CN[s[1]]
    if s.endswith("十"):
        return CN[s[0]] * 10
    if "十" in s:
        a, b = s.split("十")
        return CN[a] * 10 + CN[b]
    return CN[s]


def title_of(name):
    return name.split("【")[0].strip()


def tb_id(name):
    t = title_of(name)
    m = re.match(r"^(\d+)[丨｜]", t)
    if m:
        n = int(m.group(1))
        return f"TB{n:02d}" + ("b" if "加餐" in t else "")
    for key, uid in TB_EXTRA:
        if key in t:
            return uid
    return None


def week_id(name):
    t = title_of(name)
    m = re.match(r"^第([一二三四五六七八九十]+)周\s*(?:[（(]([^）)]*)[）)])?", t)
    if m:
        k = m.group(2) or "1"
        k = {"上": "1", "下": "2"}.get(k, k)
        k = k if k.isdigit() else "1"
        return f"W{cn_num(m.group(1)):02d}-{k}"
    if "短信服务来面试" in t:
        return "WX-sms"
    return None


def uniq(uid, seen):
    base, i = uid, 0
    while uid in seen:
        i += 1
        uid = base + "bcdefgh"[i - 1]
    seen.add(uid)
    return uid


def pdf_unit(uid, kind, path, force):
    info = M.run(["pdfinfo", str(path)]).stdout
    m = re.search(r"Pages:\s+(\d+)", info)
    pages = int(m.group(1)) if m else 0
    out = M.WORK / "text" / f"{uid}.txt"
    if force or not out.exists():
        raw = M.run(["pdftotext", "-layout", str(path), "-"]).stdout
        pgs = raw.split("\f")
        if pgs and not pgs[-1].strip():
            pgs = pgs[:-1]
        out.write_text("".join(f"[[p{i}]]\n{p}\n" for i, p in enumerate(pgs, 1)))
    txt = out.read_text()
    parts = re.split(r"\[\[p(\d+)\]\]\n", txt)
    pg = {int(parts[i]): parts[i + 1] for i in range(1, len(parts) - 1, 2)}
    low = [i for i in range(1, pages + 1) if len(re.sub(r"\s", "", pg.get(i, ""))) < M.LOW_TEXT]
    chars = sum(len(re.sub(r"\s", "", v)) for v in pg.values())
    return {"id": uid, "kind": "pdf", "path": str(path.relative_to(M.ROOT)), "title": title_of(path.name),
            "pages": pages, "chars": chars, "low_text_pages": low, "status": "TODO"}


def list_files(root, rel_base):
    files = []
    for p in sorted(root.rglob("*")):
        if not p.is_file():
            continue
        rel = p.relative_to(root)
        if any(part in M.SKIP_DIRS or part.startswith(".") for part in rel.parts):
            continue
        if p.suffix.lower() not in M.CODE_EXT or M.SENSITIVE.search(p.name):
            continue
        if p.stat().st_size > M.MAX_FILE:
            continue
        n = len(p.read_text(errors="replace").splitlines())
        files.append({"path": f"{rel_base}/{rel}".replace("\\", "/"), "lines": n})
    return files


def code_unit(uid, kind, root, rel_base, root_only=False):
    if root_only:
        files = []
        for p in sorted(root.iterdir()):
            if p.is_file() and p.suffix.lower() in M.CODE_EXT and not M.SENSITIVE.search(p.name):
                files.append({"path": f"{rel_base}/{p.name}", "lines": len(p.read_text(errors='replace').splitlines())})
    else:
        files = list_files(root, rel_base)
    if not files:
        return None
    out = M.WORK / "text" / f"{uid.replace(':', '_').replace('/', '__')}.txt"
    out.write_text("".join(f"{f['path']}\t{f['lines']}\n" for f in files))
    return {"id": uid, "kind": kind, "path": str(root.relative_to(M.ROOT)), "files": files,
            "lines": sum(f["lines"] for f in files), "status": "TODO"}


def main():
    force = "--force" in sys.argv
    if not M.DL.exists():
        print("SKIP: downloads 不存在")
        return
    (M.WORK / "text").mkdir(parents=True, exist_ok=True)
    (M.WORK / "digest").mkdir(exist_ok=True)
    units, seen = [], set()

    tb = M.DL / "Tony Bai · Go语言第一课"
    for p in sorted(tb.glob("*.pdf")):
        uid = tb_id(p.name)
        if uid is None:
            print("无法识别的 TB 文件:", p.name)
            continue
        units.append(pdf_unit(uniq(uid, seen), "pdf", p, force))

    for p in sorted((M.DL / "文档").iterdir()):
        if p.suffix == ".pdf":
            uid = week_id(p.name)
            if uid is None:
                print("无法识别的周课文件:", p.name)
                continue
            units.append(pdf_unit(uniq(uid, seen), "pdf", p, force))
        elif p.name.endswith(".qkdownloading"):
            uid = week_id(p.name)
            units.append({"id": uniq(uid or "W??", seen), "kind": "pdf", "path": str(p.relative_to(M.ROOT)),
                          "title": title_of(p.name), "pages": 0, "chars": 0, "low_text_pages": [],
                          "status": "MISSING"})

    for alias, base in M.PKG.items():
        if not base.exists():
            continue
        root_u = code_unit(f"C:{alias}/_root", "code", base, alias, root_only=True)
        if root_u:
            units.append(root_u)
        for d in sorted(base.iterdir()):
            if d.is_dir() and d.name not in M.SKIP_DIRS and not d.name.startswith("."):
                u = code_unit(f"C:{alias}/{d.name}", "code", d, f"{alias}/{d.name}")
                if u:
                    units.append(u)

    k = M.krm_root()
    if k:
        for d in sorted(k.iterdir()):
            if d.is_dir():
                u = code_unit(f"K:{d.name}", "krm", d, d.name)
                if u:
                    units.append(u)

    def rank(u):
        i = u["id"]
        return 0 if i.startswith("TB") else 1 if i.startswith("C:") else 2 if i.startswith("W") else 3

    units.sort(key=lambda u: (rank(u), u["id"]))
    M.INV.write_text(json.dumps(units, ensure_ascii=False, indent=1))
    by = {}
    for u in units:
        by[u["kind"]] = by.get(u["kind"], 0) + 1
    print(f"inventory: {len(units)} 单元 {by}")
    miss = [u["id"] for u in units if u["status"] == "MISSING"]
    if miss:
        print("MISSING:", miss)


main()
