import { defineConfig } from 'vitepress'
import { mermaidPlugin } from './theme/mermaid-plugin'

// golearn 文档站。内容 = notes/*.md 原文，导航按"阶段"组织（编号是写作顺序）。
// mermaid：markdown-it 插件把 ```mermaid fence 换成 <vitepress-mermaid> 组件
//（见 theme/mermaid-plugin.ts + MermaidDiagram.vue；vitepress-plugin-mermaid
// 会把 dayjs 塞进 optimizeDeps 导致 hydration 全站崩，弃用）。
const repo = 'yumubi/golearn'

export default defineConfig({
  // GitHub Pages 项目页：yumubi.github.io/golearn/
  base: '/golearn/',
  lang: 'zh-CN',
  title: 'golearn',
  description: 'Go for Backend：以练代学的速成课（面向 Rust/Java 后端）',
  cleanUrls: true,
  ignoreDeadLinks: true, // 指向仓库内相对路径（exercises/…）的链接在站点里不存在，放行
  markdown: {
    // 教程里全是 ```sh/```go 代码块
    lineNumbers: false,
    config: (md) => {
      md.use(mermaidPlugin)
    },
  },
  themeConfig: {
    siteTitle: 'golearn',
    nav: [
      { text: '课程表', link: '/table' },
      { text: '毕业项目', link: '/dnsproxi' },
      { text: '仓库', link: `https://github.com/${repo}` },
    ],
    sidebar: [
      {
        text: '开始',
        collapsed: true,
        items: [
          { text: '怎么学（按阶段走）', link: '/' },
          { text: '课程表', link: '/table' },
        ],
      },
      {
        text: '① 语法核心（半天到一天）',
        collapsed: true,
        items: [
          { text: 'P1.5 语法基础速通', link: '/p1_5-basics' },
          { text: 'P2 语言核心', link: '/p2-lang-core' },
          { text: 'P2.5 范式与惯用法', link: '/p2_5-idiom' },
          { text: 'P3 并发', link: '/p3-concurrency' },
          { text: 'P14 errgroup + pipeline', link: '/p14-pipelines' },
        ],
      },
      {
        text: '② 服务搭建（1 天）',
        collapsed: true,
        items: [
          { text: 'P8 标准库/HTTP', link: '/p8-fast-track' },
          { text: 'P9 数据库', link: '/p9-database' },
          { text: 'P12 sqlc + pgx', link: '/p12-sqlc-pgx' },
          { text: 'P6 毕业项目读数', link: '/p6-capstone-dnsproxi' },
        ],
      },
      {
        text: '③ 可观测性（1 天）',
        collapsed: true,
        items: [
          { text: 'P13 metrics', link: '/p13-metrics' },
          { text: 'P15 slog', link: '/p15-slog' },
          { text: 'P16 自定义 handler', link: '/p16-slog-handler' },
          { text: 'P17 落地指标', link: '/p17-dnsproxi-metrics' },
          { text: 'P18 落地日志', link: '/p18-dnsproxi-slog' },
        ],
      },
      {
        text: '④ 按需查',
        collapsed: true,
        items: [
          { text: 'P1 工具链 cheat sheet', link: '/p1-toolchain' },
          { text: 'P19 生态与常用库', link: '/p19-ecosystem' },
          { text: 'P20 写 CLI 工具', link: '/p20-cli-tool' },
          { text: 'P21 synctest + fuzz', link: '/p21-testing-concurrency' },
          { text: 'P22 工具链深潜与文档工程', link: '/p22-toolchain-docs' },
          { text: 'P23 Linux 运行时', link: '/p23-linux-runtime' },
          { text: 'P24 网络协议栈', link: '/p24-network-stack' },
          { text: 'P25 容器云原生', link: '/p25-containers-cloud' },
          { text: 'P26 部署实战（compose）', link: '/p26-deploy' },
          { text: 'P30 方法与接口', link: '/p30-methods-interfaces' },
          { text: 'P31 并发调度', link: '/p31-concurrency-scheduler' },
          { text: 'P32 内存与 GC', link: '/p32-memory-gc' },
          { text: 'P33 泛型的边界', link: '/p33-generics-boundary' },
          { text: 'P4 精读 ejdb', link: '/p4-ejdb' },
          { text: 'P5 精读 deepsearch', link: '/p5-deepsearch' },
          { text: 'P10 gRPC', link: '/p10-grpc' },
          { text: 'P11 sqlc + goose', link: '/p11-sqlc-goose' },
        ],
      },
      {
        text: '毕业项目',
        collapsed: true,
        items: [
          { text: 'dnsproxi-go 是什么', link: '/dnsproxi' },
        ],
      },
    ],
    search: { provider: 'local' },
    outline: { level: [2, 3], label: '本页目录' },
    docFooter: { prev: '上一章', next: '下一章' },
    lastUpdated: {
      text: '更新于',
    },
    returnToTopLabel: '回到顶部',
    sidebarMenuLabel: '目录',
  },
})
