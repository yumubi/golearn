import{_ as a,C as t,o as h,c as n,a2 as e,E as l}from"./chunks/framework.yO0biSGH.js";const c=JSON.parse('{"title":"第 13 章 · metrics/observability：给服务装仪表盘","description":"","frontmatter":{"title":"第 13 章 · metrics/observability：给服务装仪表盘"},"headers":[],"relativePath":"p13-metrics.md","filePath":"p13-metrics.md","lastUpdated":1793119197000}'),k={name:"p13-metrics.md"};function p(d,s,r,o,F,g){const i=t("vitepress-mermaid");return h(),n("div",null,[s[0]||(s[0]=e("",113)),l(i,{value:`flowchart LR
    Req[请求] --> M[Metrics 出事了吗] --> L[Logs 发生了什么] --> T[Traces 坏在哪一跳]
    P[Profiling pprof] -.资源视角.-> M
    M & L & T --- OTel[OpenTelemetry 统一采集 OTLP]`})])}const y=a(k,[["render",p]]);export{c as __pageData,y as default};
