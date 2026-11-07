import{_ as t,C as h,o as k,c as n,a2 as i,E as p}from"./chunks/framework.yO0biSGH.js";const C=JSON.parse('{"title":"第 17 章 · 毕业项目指标落地：dnsproxi 的 RED","description":"","frontmatter":{"title":"第 17 章 · 毕业项目指标落地：dnsproxi 的 RED"},"headers":[],"relativePath":"p17-dnsproxi-metrics.md","filePath":"p17-dnsproxi-metrics.md","lastUpdated":1793119197000}'),l={name:"p17-dnsproxi-metrics.md"};function e(d,s,F,r,o,g){const a=h("vitepress-mermaid");return k(),n("div",null,[s[0]||(s[0]=i("",68)),p(a,{value:`flowchart LR
    Q[查询] --> H{缓存判定}
    H -->|hit| C1[queries hit+1]
    H -->|miss| C2[queries miss+1] --> F[转发上游]
    F -->|成功| C3[forwarded+1<br/>histogram 记时延] --> P[PutFull] --> G[cache_entries=Len]
    F -->|失败| C4[upstream_error+1]
    Q -->|前缀命中| C5[sinkhole+1]
    E[expirer 每轮] --> G`}),s[1]||(s[1]=i("",44))])}const E=t(l,[["render",e]]);export{C as __pageData,E as default};
