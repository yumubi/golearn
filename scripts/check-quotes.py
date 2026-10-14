"""核验章节里的出处标签与摘录。

    python3 -I scripts/check-quotes.py notes/p30-xxx.md [--max-ratio 0.30]

检查：
  1. 所有 〔…〕 标签合法，指向真实位置（页码/文件/行号在范围内）。
  2. 以标签开头的引用块（> 〔TB24 p5〕 “…”）：引用文字规范化后必须是出处文本（不含 [OCR] 段）的子串，
     `…` 或 `...` 分隔的每一段分别核对；单块 <= 800 字。
  3. 紧跟在独占一行的标签 〔C:…〕 / 〔K:…〕 之后的代码块，必须是该文件对应行的子串
     （单独一行的 `...` / `// ...` / `…` 视为省略）。
  4. 首个引用块视为章头提示，可无标签；之后的引用块必须有标签。
  5. 摘录总字数占全文比例 <= --max-ratio（默认 0.30）。
  6. 〔实测〕 所在行必须有反引号命令。
downloads/ 不存在时 SKIP 退出 0。omp 不得修改本脚本。
"""
import importlib.util
import re
import sys
from pathlib import Path

_spec = importlib.util.spec_from_file_location("_material", Path(__file__).with_name("_material.py"))
M = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(M)

ELLIPSIS = re.compile(r"…|\.\.\.")
LEAD_TAG = re.compile(r"^\s*(?:[-*]\s*)?〔([^〕]+)〕")
TAG_LINE = re.compile(r"^\s*〔([CK]:[^〕]+)〕\s*$")


def check(path, max_ratio):
    errs = []
    text = Path(path).read_text()
    lines = text.splitlines()
    inv = M.load_inv()
    quote_chars = 0

    for i, l in enumerate(lines, 1):
        for tg in M.TAG_RE.findall(l):
            t = M.parse_tag(tg)
            if t is None:
                errs.append(f"{path}:{i} 不认识的标签 〔{tg}〕")
                continue
            e = M.tag_error(t, inv)
            if e:
                errs.append(f"{path}:{i} {e}")
            if t[0] == "meta" and tg.startswith("实测") and "`" not in l:
                errs.append(f"{path}:{i} 〔实测〕 同一行要附反引号命令")

    # 引用块与代码块
    in_fence = False
    i = 0
    first_quote = True
    while i < len(lines):
        l = lines[i]
        if l.lstrip().startswith("```"):
            if not in_fence:
                # 找代码块范围与前一行标签
                j = i + 1
                while j < len(lines) and not lines[j].lstrip().startswith("```"):
                    j += 1
                prev = next((lines[k] for k in range(i - 1, -1, -1) if lines[k].strip()), "")
                m = TAG_LINE.match(prev)
                if m:
                    t = M.parse_tag(m.group(1))
                    if t and t[0] == "code" and not M.tag_error(t, inv):
                        src = M.norm(M.tag_text(t))
                        body = [x for x in lines[i + 1 : j] if x.strip() not in ("...", "// ...", "…", "// …")]
                        chunks, cur = [], []
                        for x in lines[i + 1 : j]:
                            if x.strip() in ("...", "// ...", "…", "// …"):
                                chunks.append(cur)
                                cur = []
                            else:
                                cur.append(x)
                        chunks.append(cur)
                        for c in chunks:
                            n = M.norm("\n".join(c))
                            if n and n not in src:
                                errs.append(f"{path}:{i + 1} 代码块与出处 〔{m.group(1)}〕 不一致（片段开头: {c[0].strip()[:30]!r}）")
                                break
                        quote_chars += sum(len(x) for x in body)
                i = j + 1
                continue
        if l.lstrip().startswith(">"):
            j = i
            blk = []
            while j < len(lines) and lines[j].lstrip().startswith(">"):
                blk.append(lines[j].lstrip()[1:].strip())
                j += 1
            joined = " ".join(blk).strip()
            m = LEAD_TAG.match(joined)
            if m:
                t = M.parse_tag(m.group(1))
                body = joined[m.end():].strip()
                if t is None or t[0] == "meta":
                    errs.append(f"{path}:{i + 1} 引用块标签必须指向材料: 〔{m.group(1)}〕")
                elif not M.tag_error(t, inv):
                    src = M.norm(M.tag_text(t, ocr=False))
                    if len(body) > 800:
                        errs.append(f"{path}:{i + 1} 引用块超过 800 字")
                    for seg in ELLIPSIS.split(body):
                        n = M.norm(seg)
                        if len(n) >= 6 and n not in src:
                            errs.append(f"{path}:{i + 1} 引用与出处不符: 「{seg.strip()[:40]}…」不在 〔{m.group(1)}〕 里")
                            break
                    quote_chars += len(body)
            elif first_quote:
                pass
            else:
                errs.append(f"{path}:{i + 1} 引用块缺出处标签（普通提示不要用引用块）")
            first_quote = False
            i = j
            continue
        i += 1

    ratio = quote_chars / max(1, len(text))
    if ratio > max_ratio:
        errs.append(f"{path} 摘录占比 {ratio:.0%} 超过 {max_ratio:.0%}")
    if chr(0x2014) in text:
        errs.append(f"{path} 含 em-dash (U+2014)")
    return errs


def main():
    args = sys.argv[1:]
    ratio = 0.30
    if "--max-ratio" in args:
        k = args.index("--max-ratio")
        ratio = float(args[k + 1])
        del args[k : k + 2]
    if not args:
        raise SystemExit(__doc__)
    if not M.DL.exists():
        print("SKIP: downloads 不存在")
        return
    bad = False
    for p in args:
        errs = check(p, ratio)
        print(("FAIL " if errs else "OK   ") + p)
        for e in errs:
            print("  -", e)
        bad |= bool(errs)
    sys.exit(1 if bad else 0)


main()
