# T5 站点实测记录

日期: 2026-10-12
机器: Apple M4, macOS（Darwin 25.x）
方法: `pnpm run docs:dev -- --port 5173 --strictPort`（vitepress 1.6.4）
起服务，Chromium headless 两个视口（1280×800 桌面 / 390×844 移动）
逐页 DOM 数值断言（截图模型读不了，全部用 scrollWidth/clientWidth
等可复核数值）。

## 断言结果

| # | 断言 | 视口 | 页面 | 实测 | 状态 |
|---|---|---|---|---|---|
| 1 | P21 页渲染，H1 正确 | 桌面 | /p21-testing-concurrency | H1="第 21 章 · testing/synctest + fuzz：时间与输入的测试武器" | OK |
| 2 | 侧边栏可见且含 P21 条目 | 桌面 | P21 | sidebarVisible=true，width=224px，sidebarHasP21=true | OK |
| 3 | 页面无横向溢出 | 桌面 | P21/P14/P1/P2/P8/P19/P11 | scrollWidth==clientWidth==1280（7 页全过） | OK |
| 4 | P21 内容量正常（非空壳） | 桌面 | P21 | contentChars=9521，tables=6，codeBlocks=8 | OK |
| 5 | mermaid 图渲染出 SVG | 桌面 | /p14-pipelines | mermaid 元素 2 个，渲染出 svg 1 个（本章实画 1 图） | OK |
| 6 | 抽 5 旧章渲染正常 | 桌面 | P1/P2/P8/P19/P11 | H1 全部正确，无溢出，代码块/表格数正常 | OK |
| 7 | 页面无横向溢出 | 390px | P21/P19/P8 | pageOverflow=false（3 页全过） | OK |
| 8 | 表格在窄屏不顶破布局 | 390px | P21 | 6 张表全部 contained（表宽 342px ≤ 容器 342px，视口 390px） | OK |
| 9 | 代码块窄屏不溢出 | 390px | P21 | codeOverflow=0/8 | OK |
| 10 | 移动端汉堡菜单工作 | 390px | P21 | hamburger 存在，点击后 sidebarVisible=true | OK |
| 11 | 课程表页 23 章 | 桌面 | /table | title="课程表：23 章速查"，tables=1，无溢出 | OK |
| 12 | 首页 P21 入口 | 桌面 | / | p21Links=1（docs/index.md 课程表区链接可达） | OK |

## 备注

- mermaid=0 于 P21 属预期：该章未画 mermaid 图，mermaid 能力用 P14
  （2 个元素、1 个 SVG 实渲染）验证。
- 390px 下 P21 有 4 张表 scrollWidth > clientWidth 的读数是
  overflow-x:auto 容器内部的滚动余量，实测表宽 342px 全部收进
  视口（contained=true），非布局缺陷。
- 测试期间 gate --fast 与压测后台任务的竞态已通过"等后台完成再跑
  gate"规避（T2 收尾时实证过一次 FAIL 后复跑 PASS）。
