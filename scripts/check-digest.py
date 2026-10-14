"""校验 work/digest/<id>.md 的结构和出处。

    python3 -I scripts/check-digest.py work/digest/TB24.md [更多...]

通过退出码 0，否则打印原因退出码 1。omp 不得修改本脚本。
"""
import importlib.util
import re
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location("_material", Path(__file__).with_name("_material.py"))
M = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(M)

SECTIONS = ["一句话主题", "知识点", "代码例子", "设计取舍与易错", "与 Go 1.26 冲突或过期", "可做成练习的点", "Rust/Java 对照点"]


def parse_pages(spec):
    out = set()
    for part in re.split(r"[,\s]+", spec.strip()):
        if not part:
            continue
        m = re.fullmatch(r"(\d+)(?:-(\d+))?", part)
        if not m:
            raise ValueError(f"页码写法不对: {part!r}")
        a = int(m.group(1))
        b = int(m.group(2) or a)
        out.update(range(a, b + 1))
    return out


def section(text, name):
    m = re.search(rf"^## {re.escape(name)}\s*$", text, re.M)
    if not m:
        return None
    rest = text[m.end():]
    n = re.search(r"^## ", rest, re.M)
    return rest[: n.start()] if n else rest


def bullets(block):
    return [l for l in block.splitlines() if l.startswith("- ")]


def listed(text, header):
    """`header` 行之后连续的 `- xxx` 项。"""
    m = re.search(rf"^{re.escape(header)}[:：]\s*(.*)$", text, re.M)
    if not m:
        return None, []
    items = []
    for l in text[m.end():].splitlines()[1:]:
        if l.startswith("- "):
            items.append(l[2:].strip())
        elif l.strip() == "":
            if items:
                break
        else:
            break
    return m.group(1).strip(), items


def validate(path, inv=None):
    errs = []
    text = Path(path).read_text()
    inv = inv or M.load_inv()
    m = re.match(r"# (\S+)", text)
    if not m or m.group(1) not in inv:
        return [f"首行必须是 `# <单元id> 标题`，且 id 在 inventory 里（读到 {m.group(1) if m else None}）"]
    uid = m.group(1)
    u = inv[uid]
    want = uid.replace(":", "_").replace("/", "__") + ".md"
    if Path(path).name != want:
        errs.append(f"文件名应为 {want}")

    st = re.search(r"^状态:\s*(DONE|PARTIAL\(.+\)|MISSING)\s*$", text, re.M)
    if not st:
        return errs + ["缺 `状态: DONE | PARTIAL(原因) | MISSING`"]
    status = st.group(1)
    if u["status"] == "MISSING":
        if status != "MISSING":
            errs.append("该单元在 inventory 里是 MISSING（文件缺失），状态必须是 MISSING")
        return errs
    if status == "MISSING":
        errs.append("文件存在，状态不能写 MISSING")

    unread = set()
    if u["kind"] == "pdf":
        cov = re.search(r"^覆盖页:\s*(.+)$", text, re.M)
        if not cov:
            errs.append("缺 `覆盖页:`")
            covered = set()
        else:
            try:
                covered = parse_pages(cov.group(1))
            except ValueError as e:
                errs.append(str(e))
                covered = set()
        for key in ("IMG-UNREAD", "UNREADABLE"):
            mm = re.search(rf"^{key}:\s*(.+)$", text, re.M)
            if mm:
                try:
                    unread |= parse_pages(mm.group(1))
                except ValueError as e:
                    errs.append(str(e))
        allp = set(range(1, u["pages"] + 1))
        if (covered | unread) - allp:
            errs.append(f"页码超出 1-{u['pages']}: {sorted((covered | unread) - allp)[:10]}")
        miss = allp - covered - unread
        if miss:
            errs.append(f"这些页既没覆盖也没声明读不了: {sorted(miss)[:20]}{'...' if len(miss) > 20 else ''}")
        if unread and not status.startswith("PARTIAL"):
            errs.append("有 IMG-UNREAD/UNREADABLE 页，状态必须写 PARTIAL(原因)")
    else:
        head, cov = listed(text, "覆盖文件")
        _, skip = listed(text, "跳过文件")
        allf = {f["path"] for f in u["files"]}
        skipped = {s.split(":")[0].strip() for s in skip}
        for s in skip:
            if ":" not in s or len(s.split(":", 1)[1].strip()) < 4:
                errs.append(f"跳过文件需写理由: {s!r}")
        if head == "all":
            covered_f = allf
        else:
            covered_f = {p for p in allf if any(p == c or p.startswith(c.rstrip("/") + "/") for c in cov)}
        miss = allf - covered_f - {p for p in allf if any(p == s or p.startswith(s.rstrip("/") + "/") for s in skipped)}
        if miss:
            errs.append(f"这些文件既没覆盖也没跳过: {sorted(miss)[:10]}{'...' if len(miss) > 10 else ''}")
        if skipped and not status.startswith("PARTIAL"):
            errs.append("有跳过文件，状态必须写 PARTIAL(原因)")

    for name in SECTIONS:
        if section(text, name) is None:
            errs.append(f"缺小节 `## {name}`")
    if errs:
        return errs

    ks = M.digest_items(path)
    kb = bullets(section(text, "知识点"))
    if not ks:
        errs.append("知识点至少 1 条，格式 `- K1 要点 〔出处〕 [必读|选读|低价值]`")
    if len(ks) != len(kb):
        errs.append(f"知识点有 {len(kb)} 行但只有 {len(ks)} 行格式合法（缺出处或级别）")
    seen = set()
    for kid, body, tag, lvl in ks:
        if kid in seen:
            errs.append(f"{kid} 重复")
        seen.add(kid)
        t = M.parse_tag(tag)
        if t is None or t[0] == "meta":
            errs.append(f"{kid} 出处标签不合法: 〔{tag}〕")
            continue
        e = M.tag_error(t, inv)
        if e:
            errs.append(f"{kid}: {e}")
            continue
        if t[0] == "pdf" and t[3] - t[2] >= 5:
            errs.append(f"{kid} 出处页范围过宽 p{t[2]}-{t[3]}（最多 5 页），请拆成几条或缩小范围")
        if t[0] == "code" and t[3] and t[4] - t[3] > 300:
            errs.append(f"{kid} 出处行范围过宽 {t[3]}-{t[4]}（最多 300 行）")
        if len(body) >= 30 and M.shingle_ratio(body, M.tag_text(t)) > 0.6:
            errs.append(f"{kid} 疑似抄原文（与出处文字重合超过 60%），请用自己的话")

    for l in bullets(section(text, "代码例子")):
        if l.strip() == "- 无":
            continue
        if not re.search(r"可运行:\s*(是|否|未验证)", l) or "过期:" not in l or not M.TAG_RE.search(l):
            errs.append(f"代码例子行需含 出处、`可运行: 是|否|未验证`、`过期:`: {l[:50]}")
        if re.search(r"可运行:\s*是", l):
            me = re.match(r"- (E\d+)", l)
            d = M.WORK / "run" / Path(path).stem / (me.group(1) if me else "?")
            if not (d.is_dir() and list(d.glob("*.go")) and (d / "out.txt").exists() and (d / "out.txt").stat().st_size > 0):
                errs.append(f"{me.group(1) if me else '?'} 标了可运行: 是，但缺运行证据：需要目录 work/run/{Path(path).stem}/{me.group(1) if me else 'Ek'}/，"
                            "内含你实际运行的 .go 文件和非空的 out.txt（命令输出或编译错误）。没跑过就改成 `未验证`")
    for l in bullets(section(text, "与 Go 1.26 冲突或过期")):
        if l.strip() == "- 无":
            continue
        has_basis = "依据:" in l
        if has_basis:
            if not re.search(r"依据:.*`[^`]+`", l):
                errs.append(f"`依据:` 后必须有反引号包起来的、你实际运行过的命令: {l[:50]}")
        elif not l.startswith("- 待核:"):
            errs.append(f"没有实测依据的条目必须以 `- 待核:` 开头，且只能写成待验证的疑问，不能当结论: {l[:50]}")
        for tg in M.TAG_RE.findall(l):
            t = M.parse_tag(tg)
            if t is None:
                errs.append(f"过期条目标签不合法: 〔{tg}〕")
            elif t[0] != "meta" and (e := M.tag_error(t, inv)):
                errs.append(e)
    for i, l in enumerate(text.splitlines(), 1):
        if len(l) > 300:
            errs.append(f"第 {i} 行超过 300 字符，digest 是索引不是抄录")
            break
    if chr(0x2014) in text:
        errs.append("含 em-dash (U+2014)")
    return errs


def main():
    if len(sys.argv) < 2:
        raise SystemExit(__doc__)
    bad = 0
    for p in sys.argv[1:]:
        errs = validate(p)
        if errs:
            bad += 1
            print(f"FAIL {p}")
            for e in errs:
                print("  -", e)
        else:
            print(f"OK   {p}")
    sys.exit(1 if bad else 0)


if __name__ == "__main__":
    main()
