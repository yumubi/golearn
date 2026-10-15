"""文档站真实验证：headless 浏览器全站爬取。

    python3 -I scripts/verify-site.py [--dist docs/.vitepress/dist]

前置：pnpm docs:build 已跑过（本脚本不重新构建）；本机装有 playwright
（pip install --user --break-system-packages playwright，浏览器用
~/Library/Caches/ms-playwright 下的 chrome-headless-shell，缺了会提示）。

验证（模拟 GitHub Pages 行为：/path → /path.html 回退）：
  1. 站内链接全部 200（从首页出发，同源 <a href> 全量爬取，去重）
  2. 每页子资源（js/css/图/字体）无 4xx/5xx
  3. 含 mermaid 的页面 hydration 后渲染出 svg
  4. 本地搜索索引 hashmap.json 覆盖所有镜像页
退出码 = 失败数；全过打印 SITE: PASS。
"""
import glob
import json
import os
import re
import sys
import tempfile
import threading
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DIST = os.path.join(ROOT, "docs/.vitepress/dist")
BASE = "/golearn/"
args = sys.argv[1:]
if "--dist" in args:
    DIST = os.path.abspath(args[args.index("--dist") + 1])

if not os.path.isdir(DIST):
    raise SystemExit(f"dist 不存在：{DIST}\n先跑 pnpm docs:build")

try:
    from playwright.async_api import async_playwright
except ImportError:
    raise SystemExit("缺 playwright：python3 -m pip install --user --break-system-packages playwright")

EXTRA = {"dnsproxi.md", "index.md", "table.md"}


class GHPages(SimpleHTTPRequestHandler):
    """dist 静态服务 + GitHub Pages 式的 .html 回退，日志静默。"""

    def translate_path(self, path):
        p = super().translate_path(path)
        if os.path.isdir(p):
            p = os.path.join(p, "index.html")
        elif not os.path.exists(p) and os.path.exists(p + ".html"):
            p += ".html"
        return p

    def send_head(self):
        # GitHub Pages：/path（目录）→ 301 /path/
        p = super().translate_path(self.path)
        if os.path.isdir(p) and not self.path.endswith("/"):
            self.send_response(301)
            self.send_header("Location", self.path + "/")
            self.end_headers()
            return None
        return super().send_head()

    def log_message(self, *a):
        pass


def serve():
    # 站点挂在 /golearn/ 下：临时目录里做 golearn → dist 的符号链接，服务临时目录
    tmp = tempfile.mkdtemp(prefix="verify-site-")
    os.symlink(DIST, os.path.join(tmp, "golearn"))
    os.chdir(tmp)
    srv = ThreadingHTTPServer(("127.0.0.1", 0), GHPages)
    port = srv.server_address[1]
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv, f"http://127.0.0.1:{port}{BASE}"


def search_index_gaps():
    """hashmap.json 必须覆盖 notes 镜像与 handbook 镜像（key 形如 p1-toolchain.md、handbook_01-worldview.md）。期望集合从正本目录推导。"""
    m = json.load(open(os.path.join(DIST, "hashmap.json")))
    want = {f"{os.path.basename(f)}" for f in glob.glob(os.path.join(ROOT, "notes/p*.md"))}
    want |= {f"handbook_{os.path.basename(f)}" for f in glob.glob(os.path.join(ROOT, "guide/handbook/*.md"))
             if not f.endswith("README.md")}
    want |= EXTRA
    return sorted(w for w in want if w not in m)


async def crawl(page, origin):
    """从首页广度优先爬同源链接。返回 (访问页数, [(url, 问题)])。"""
    seen, queue, fails = set(), [origin + ""], []
    sub_bad, page_url = [], None
    page.on("response", lambda r: sub_bad.append((r.url, r.status))
            if r.status >= 400 and page_url and r.url.startswith(origin) else None)
    visited = 0
    while queue:
        url = queue.pop(0).split("#")[0].rstrip("?")
        key = url if url.endswith("/") or url.endswith(".html") else url
        if key.rstrip("/") in seen:
            continue
        seen.add(key.rstrip("/"))
        page_url = key
        sub_bad.clear()
        try:
            resp = await page.goto(key, wait_until="networkidle", timeout=30000)
        except Exception as e:
            fails.append((key, f"打开失败 {type(e).__name__}"))
            continue
        if not resp or resp.status != 200:
            fails.append((key, f"HTTP {resp.status if resp else '无响应'}"))
            continue
        visited += 1
        await page.wait_for_timeout(600)
        if sub_bad:
            fails.append((key, f"子资源失败 {sub_bad[:2]}"))
        n_mermaid = await page.evaluate(
            "() => { const d=[...document.querySelectorAll('.mermaid-diagram')];"
            " return {t: d.length, s: d.filter(x=>x.querySelector('svg')).length} }")
        if n_mermaid["t"] and n_mermaid["s"] < n_mermaid["t"]:
            fails.append((key, f"mermaid 未渲染 {n_mermaid['s']}/{n_mermaid['t']}"))
        hrefs = await page.evaluate(
            """() => [...document.querySelectorAll('a[href]')]
                   .map(a => a.href.split('#')[0])
                   .filter(h => h.startsWith(location.origin + '%s'))""" % BASE.rstrip("/"))
        queue.extend(hrefs)
    return visited, fails


async def main():
    srv, origin = serve()
    try:
        pw = await async_playwright().start()
        browser = await pw.chromium.launch(headless=True)
        page = await browser.new_page(viewport={"width": 1440, "height": 900})
        visited, fails = await crawl(page, origin)
        await browser.close()
        gaps = search_index_gaps()
        print(f"爬取页面 {visited} 个")
        for url, why in fails:
            print(f"  FAIL {url} {why}")
        if gaps:
            print(f"  FAIL 搜索索引缺 {len(gaps)} 页: {gaps[:5]}")
        n_fail = len(fails) + (1 if gaps else 0)
        print("SITE: PASS" if n_fail == 0 else f"SITE: FAIL（{n_fail} 项）")
        sys.exit(n_fail)
    finally:
        srv.shutdown()


import asyncio
asyncio.run(main())
