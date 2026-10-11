import { defineConfig } from 'vitepress'

// golearn 文档站。内容 = notes/*.md 原文，导航按"阶段"组织（编号是写作顺序）。
export default defineConfig({
  lang: 'zh-CN',
  title: 'golearn',
  description: 'Go for Backend：以练代学的速成课（面向 Rust/Java 后端）',
  cleanUrls: true,
  ignoreDeadLinks: true, // 指向仓库内相对路径（exercises/…）的链接在站点里不存在，放行
  markdown: {
    // 教程里全是 ```sh/```go 代码块
    lineNumbers: false,
  },
  themeConfig: {
    siteTitle: 'golearn',
    nav: [
      { text: '课程表', link: '/table' },
      { text: '毕业项目', link: '/dnsproxi' },
      { text: '仓库', link: 'https://github.com/' },
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
        text: '① 语法核心（半天）',
        collapsed: true,
        items: [
          { text: 'P2 语言核心', link: '/p2-lang-core' },
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
