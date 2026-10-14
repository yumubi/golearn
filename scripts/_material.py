"""材料流水线的共用函数（PLAN-OMP-3）。只用标准库。被 material-extract / check-* / next-unit 加载。

约定：
  单元 id    TB24 / TB35b / TBX10 / W12-1 / WX-sms / C:dachang/raft / K:krm-backend-第八章
  出处标签   〔TB24 p5〕 〔TB24 p5-7〕 〔W12-1 p31〕 〔C:dachang/raft/raft.go:120-180〕 〔K:krm-backend/x.go:3-9〕
  元标签     〔实测〕 〔自〕 〔1.26 修正〕（不指向材料，由其它检查要求附命令）
"""
import glob
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DL = ROOT / "downloads"
WORK = ROOT / "work"
INV = WORK / "inventory.json"

PKG = {
    "dachang": DL / "代码课件" / "3-go大厂冲刺--代码",
    "go_basic": DL / "代码课件" / "go_basic",
    "go_frame": DL / "代码课件" / "go_frame",
}
SKIP_DIRS = {".git", "node_modules", "img", "doc", "dist", ".idea", "vendor", "__MACOSX"}
CODE_EXT = {".go", ".mod", ".proto", ".yaml", ".yml", ".md", ".sql", ".json", ".vue", ".js", ".ts", ".toml", ".sh"}
SENSITIVE = re.compile(r"(\.key$|\.pem$|\.priv$|kubeconfig|\.log$|\.p12$|\.crt$|id_rsa|\.env$|\.DS_Store$|go\.sum$)", re.I)
MAX_FILE = 400 * 1024
LOW_TEXT = 60
OCR_BELOW = 200  # 文字层少于这个字数的页做 OCR
OCR_BIN = WORK / "bin" / "ocr"
OCR_DIR = WORK / "ocr"

TAG_RE = re.compile(r"〔([^〕]+)〕")
PDF_TAG = re.compile(r"((?:TB\w+)|(?:W\d+-\d+)|(?:WX-\w+))\s+p(\d+)(?:-(\d+))?")
CODE_TAG = re.compile(r"([CK]):([^:]+?)(?::(\d+)(?:-(\d+))?)?")
META_PREFIX = ("实测", "自", "1.26 修正")


def krm_root():
    g = glob.glob(str(DL / "课件资料*"))
    return Path(g[0]) if g else None


def load_inv():
    if not INV.exists():
        raise SystemExit("work/inventory.json 不存在，先跑 python3 -I scripts/material-extract.py")
    return {u["id"]: u for u in json.loads(INV.read_text())}


def code_path(kind, path):
    """tag 里的路径 -> 磁盘路径。C: alias/rest；K: dirname/rest。"""
    first, _, rest = path.partition("/")
    if kind == "C":
        base = PKG.get(first)
    else:
        k = krm_root()
        base = (k / first) if k else None
    return (base / rest) if base is not None else None


def parse_tag(inner):
    """返回 ('pdf', id, a, b) | ('code', kind, path, a, b) | ('meta',) | None"""
    s = inner.strip()
    m = PDF_TAG.fullmatch(s)
    if m:
        a = int(m.group(2))
        return ("pdf", m.group(1), a, int(m.group(3) or a))
    m = CODE_TAG.fullmatch(s)
    if m:
        a = int(m.group(3)) if m.group(3) else None
        b = int(m.group(4)) if m.group(4) else a
        return ("code", m.group(1), m.group(2), a, b)
    if s.startswith(META_PREFIX):
        return ("meta",)
    return None


_page_cache = {}


def pdf_pages(uid):
    """返回 {页号: 文本}。文本文件由 material-extract 生成。"""
    if uid in _page_cache:
        return _page_cache[uid]
    p = WORK / "text" / f"{uid}.txt"
    if not p.exists():
        raise SystemExit(f"{p} 不存在，先跑 material-extract.py")
    parts = re.split(r"\[\[p(\d+)\]\]\n", p.read_text())
    pages = {int(parts[i]): parts[i + 1] for i in range(1, len(parts) - 1, 2)}
    _page_cache[uid] = pages
    return pages


def tag_error(tag, inv):
    """校验标签是否指向真实位置；返回错误字符串或 None。"""
    if tag[0] == "meta":
        return None
    if tag[0] == "pdf":
        _, uid, a, b = tag
        u = inv.get(uid)
        if not u or u["kind"] != "pdf":
            return f"未知 PDF 单元 {uid}"
        if a < 1 or b < a or b > u["pages"]:
            return f"{uid} 页码越界 p{a}-{b}（共 {u['pages']} 页）"
        return None
    _, kind, path, a, b = tag
    f = code_path(kind, path)
    if f is None or not f.is_file():
        return f"文件不存在 {kind}:{path}"
    if a is not None:
        n = len(f.read_text(errors="replace").splitlines())
        if a < 1 or b < a or b > n:
            return f"{path} 行号越界 {a}-{b}（共 {n} 行）"
    return None


def tag_text(tag, ocr=True):
    """标签指向的原文文本（页范围 / 行范围 / 整个文件）。ocr=False 时去掉 [OCR] 段（OCR 有识别错误，不能当引文依据）。"""
    if tag[0] == "pdf":
        pages = pdf_pages(tag[1])
        parts = [pages.get(i, "") for i in range(tag[2], tag[3] + 1)]
        if not ocr:
            parts = [x.split("\n[OCR]\n")[0] for x in parts]
        return "\n".join(parts)
    if tag[0] == "code":
        f = code_path(tag[1], tag[2])
        lines = f.read_text(errors="replace").splitlines()
        if tag[3] is None:
            return "\n".join(lines)
        return "\n".join(lines[tag[3] - 1 : tag[4]])
    return ""


_QUOTES = "“”\"'‘’「」『』"
_STRIP = re.compile(r"\[\[p\d+\]\]|\s+")


def norm(s):
    s = _STRIP.sub("", s)
    return "".join(c for c in s if c not in _QUOTES)


def overlap(t1, t2):
    """两个标签是否指向重叠位置。"""
    if t1[0] != t2[0]:
        return False
    if t1[0] == "pdf":
        return t1[1] == t2[1] and t1[2] <= t2[3] and t2[2] <= t1[3]
    if t1[0] == "code":
        if (t1[1], t1[2]) != (t2[1], t2[2]):
            return False
        if t1[3] is None or t2[3] is None:
            return True
        return t1[3] <= t2[4] and t2[3] <= t1[4]
    return False


K_RE = re.compile(r"^- (K\d+)\s+(.*?)〔([^〕]+)〕\s*\[(必读|选读|低价值)\]", re.M)


def digest_items(path):
    """解析 digest 的知识点 -> [(Kid, 文字, 标签inner, 级别)]"""
    return [(m.group(1), m.group(2), m.group(3), m.group(4)) for m in K_RE.finditer(Path(path).read_text())]


def shingle_ratio(text, source, n=10):
    t, s = norm(text), norm(source)
    if len(t) < max(30, n):
        return 0.0
    grams = [t[i : i + n] for i in range(len(t) - n + 1)]
    hit = sum(1 for g in grams if g in s)
    return hit / len(grams)


def run(cmd):
    return subprocess.run(cmd, capture_output=True, text=True)
