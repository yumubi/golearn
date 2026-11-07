import{_ as h,C as n,o as t,c as k,a2 as i,E as l}from"./chunks/framework.yO0biSGH.js";const y=JSON.parse('{"title":"P4 · 项目精读 A：ejdb，读别人的 Go 存储引擎从哪下手？","description":"","frontmatter":{"title":"P4 · 项目精读 A：ejdb，读别人的 Go 存储引擎从哪下手？"},"headers":[],"relativePath":"p4-ejdb.md","filePath":"p4-ejdb.md","lastUpdated":1793119197000}'),p={name:"p4-ejdb.md"};function e(d,s,F,r,E,g){const a=n("vitepress-mermaid");return t(),k("div",null,[s[0]||(s[0]=i("",33)),l(a,{value:`flowchart LR
    Q[Query builder<br/>query_builder.go] --> X[executeQuery<br/>query_exec.go:41]
    X --> S[selectIndexes 选索引<br/>query_selection.go:9]
    S -->|无索引可用| FS[fullScanner 全表扫<br/>query_scanner.go:8]
    S -->|唯一索引| US[uniq scanner<br/>query_scanner_uniq.go]
    S -->|非唯一索引| DS[dup scanner<br/>query_scanner_dup.go]
    FS --> C[docConsumer 闭包<br/>query_exec.go:76]
    US --> C
    DS --> C
    C --> P[match + project + sort]
    subgraph storage 层
        KV[KVStore interface<br/>storage/storage.go:58]
        KV --> B[badger.go]
        KV --> PB[pebble.go]
    end
    FS --> KV`}),s[1]||(s[1]=i("",101))])}const c=h(p,[["render",e]]);export{y as __pageData,c as default};
