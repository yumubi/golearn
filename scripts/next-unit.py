"""打印下一个该做的单元。完成与否由 digest 校验裁定，不看任何自报状态。

    python3 -I scripts/next-unit.py          # 下一个单元
    python3 -I scripts/next-unit.py --list   # 全部进度
顺序：TB -> C -> W -> K，各自按 id。MISSING 单元跳过。omp 不得修改本脚本。
"""
import importlib.util
import sys
from pathlib import Path

here = Path(__file__).parent


def load(name, file):
    spec = importlib.util.spec_from_file_location(name, here / file)
    m = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(m)
    return m


M = load("_material", "_material.py")
CD = load("check_digest", "check-digest.py")


def digest_path(uid):
    return M.WORK / "digest" / (uid.replace(":", "_").replace("/", "__") + ".md")


def state(u, inv):
    if u["status"] == "MISSING":
        return "MISSING"
    p = digest_path(u["id"])
    if not p.exists():
        return "TODO"
    errs = CD.validate(p, inv)
    if errs:
        return "FAIL"
    return "PARTIAL" if "状态: PARTIAL" in p.read_text() else "DONE"


def main():
    inv = M.load_inv()
    rows = [(u, state(u, inv)) for u in inv.values()]
    if "--list" in sys.argv:
        for u, s in rows:
            print(f"{s:8} {u['id']}")
        cnt = {}
        for _, s in rows:
            cnt[s] = cnt.get(s, 0) + 1
        print(cnt)
        return
    for u, s in rows:
        if s in ("TODO", "FAIL"):
            print(f"NEXT {u['id']}  ({s})")
            print(f"  标题/路径: {u.get('title') or u['path']}")
            if u["kind"] == "pdf":
                print(f"  页数 {u['pages']}  字数 {u['chars']}  低文字页 {len(u['low_text_pages'])}: {u['low_text_pages'][:30]}")
                print(f"  文本: work/text/{u['id']}.txt")
                if u["pages"] > 30 or u["chars"] > 40000:
                    print("  体量大：按页段拆开读，digest 里分节，覆盖页写整体范围")
            else:
                print(f"  文件 {len(u['files'])} 个，共 {u['lines']} 行；清单: work/text/{u['id'].replace(':', '_').replace('/', '__')}.txt")
            print(f"  digest 写到: {digest_path(u['id'])}")
            if s == "FAIL":
                print("  上次的 digest 未通过校验:")
                for e in CD.validate(digest_path(u["id"]), inv):
                    print("   -", e)
            return
    print("ALL DONE（可进入 S2）")


main()
