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
      { text: '速通手册', link: '/handbook/26-route' },
      { text: '课程表', link: '/table' },
      { text: '毕业项目', link: '/dnsproxi' },
      { text: '仓库', link: `https://github.com/${repo}` },
    ],
    sidebar: [
      {
        text: '开始',
        collapsed: true,
        items: [
          { text: '怎么学（两层阅读）', link: '/' },
          { text: '课程表（细节库）', link: '/table' },
        ],
      },
      {
        text: '速通手册（18.5 小时，先读）',
        collapsed: false,
        items: [
          { text: '手册总览与目录', link: '/handbook/README' },
          { text: '26 七天学习路线', link: '/handbook/26-route' },
          {
            text: '心智模型',
            collapsed: false,
            items: [
              { text: '01 世界观与差异总表', link: '/handbook/01-worldview' },
              { text: '02 工程骨架', link: '/handbook/02-skeleton' },
              { text: '03 类型与数据', link: '/handbook/03-types-data' },
            ],
          },
          {
            text: '核心范式',
            collapsed: false,
            items: [
              { text: '04 接口与组合', link: '/handbook/04-interfaces' },
              { text: '05 错误与资源', link: '/handbook/05-errors' },
              { text: '06 并发一', link: '/handbook/06-concurrency-basics' },
              { text: '07 并发二', link: '/handbook/07-concurrency-engineering' },
              { text: '08 运行时', link: '/handbook/08-runtime' },
              { text: '09 泛型', link: '/handbook/09-generics' },
            ],
          },
          {
            text: '服务工程',
            collapsed: false,
            items: [
              { text: '10 分层与依赖注入', link: '/handbook/10-layering-di' },
              { text: '11 HTTP 与 gRPC', link: '/handbook/11-http-grpc' },
              { text: '12 数据库', link: '/handbook/12-database' },
              { text: '13 缓存、消息与幂等', link: '/handbook/13-cache-mq-idempotency' },
              { text: '14 测试', link: '/handbook/14-testing' },
              { text: '15 可观测', link: '/handbook/15-observability' },
              { text: '16 稳定性', link: '/handbook/16-stability' },
            ],
          },
          {
            text: '规模与演进',
            collapsed: false,
            items: [
              { text: '17 数据迁移', link: '/handbook/17-migration' },
              { text: '18 分布式一致性', link: '/handbook/18-consistency' },
              { text: '19 微服务', link: '/handbook/19-microservices' },
              { text: '20 注册发现与负载均衡', link: '/handbook/20-discovery-lb' },
              { text: '21 搜索、Feed 与 IM', link: '/handbook/21-search-feed-im' },
            ],
          },
          {
            text: '交付与运行',
            collapsed: false,
            items: [
              { text: '22 容器与 Linux', link: '/handbook/22-containers-linux' },
              { text: '23 部署与 Kubernetes', link: '/handbook/23-deploy-k8s' },
            ],
          },
          {
            text: '项目线',
            collapsed: false,
            items: [
              { text: '24 项目一：dnsproxi-go', link: '/handbook/24-dnsproxi' },
              { text: '25 项目二：minifeed 蓝图', link: '/handbook/25-business-project' },
            ],
          },
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
          { text: 'P34 TCP 服务器与 DNS over TCP', link: '/p34-tcp-server-dns-over-tcp' },
          { text: 'P35 限流、熔断与容错', link: '/p35-ratelimit-breaker' },
          { text: 'P36 用户、鉴权与分层测试', link: '/p36-user-auth-layering' },
          { text: 'P37 缓存、计数与榜单', link: '/p37-cache-counter-rank' },
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
