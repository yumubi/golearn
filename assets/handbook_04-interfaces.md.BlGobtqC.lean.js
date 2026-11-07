import{_ as h,C as k,o as p,c as l,a2 as a,E as n,j as t}from"./chunks/framework.yO0biSGH.js";const o=JSON.parse('{"title":"04 方法、接口与组合：从调用行为到运行机制","description":"","frontmatter":{"title":"04 方法、接口与组合：从调用行为到运行机制"},"headers":[],"relativePath":"handbook/04-interfaces.md","filePath":"handbook/04-interfaces.md","lastUpdated":1794051066000}'),e={name:"handbook/04-interfaces.md"};function E(r,s,d,g,y,F){const i=k("vitepress-mermaid");return p(),l("div",null,[s[0]||(s[0]=a("",204)),n(i,{value:`flowchart LR
    W["Welcome(sender Sender)"] --> I["接口值 sender"]
    I --> TAB["tab：Sender × Email 的 itab"]
    I --> DATA["data：当前具体值的数据表示"]
    TAB --> IT["Inter：Sender 的类型与方法要求"]
    TAB --> CT["Type：Email 的类型信息"]
    TAB --> FUN["Fun：Send 对应的调用入口"]
    FUN --> SEND["Email.Send 或衔接它的包装函数"]
    DATA --> SEND`}),s[1]||(s[1]=a("",8)),n(i,{value:`flowchart TB
    P["p：*MyError，指针值 nil"] --> R["return p：转换为 error 接口"]
    R --> E["err：iface"]
    E --> T["tab 非 nil：error × *MyError"]
    E --> D["data = nil：保存的具体指针为空"]
    N["nil 接口：没有动态类型，也没有动态值"] --> NT["tab = nil"]
    N --> ND["data = nil"]
    T --> C["类型信息不同，所以 err != nil"]
    NT --> C`}),s[2]||(s[2]=a("",13)),n(i,{value:`flowchart LR
    EI["ei：eface"] --> ET["_type → T 的类型信息"]
    EI --> ED["data → T 值的存储"]
    ED --> EV["n = 17，s = hello, interface"]`}),s[3]||(s[3]=t("p",null,'如果另取 t := T{n: 18, s: "hello, interface"}，令 var i NonEmptyInterface = t，数据部分仍承载 T 值，但第一字换成这一接口与 T 的 itab。表中列出 M1、M2 的入口，以及接口本身的方法要求。〔TB29 p8–9〕',-1)),n(i,{value:`flowchart LR
    I["i：iface"] --> TAB["tab → NonEmptyInterface × T"]
    I --> ID["data → T 值的存储：n = 18"]
    TAB --> INTER["Inter：接口类型、包路径、M1/M2"]
    TAB --> TYPE["Type：T 的类型信息"]
    TAB --> HASH["Hash：具体类型哈希副本"]
    TAB --> FUN["Fun：M1、M2 的调用入口"]`}),s[4]||(s[4]=a("",22)),n(i,{value:`flowchart LR
    R["Java Sender 引用"] --> O["当前接收者对象"]
    O --> K["实际类的元数据"]
    K --> T["HotSpot 接口分派相关信息：itable"]
    T --> M["对应的具体方法"]
    B["invokeinterface 的符号引用"] --> S["接口与方法要求"]
    S -."与实际接收者共同确定目标".-> T`}),s[5]||(s[5]=a("",72)),n(i,{value:`flowchart LR
    A["调用 s.Read(buf)"] --> B["选择器解析到嵌入字段 Reader"]
    B --> C["调用 s.Reader.Read(buf)"]
    C --> D["接口字段中的具体 Reader 实现"]`}),s[6]||(s[6]=a("",114))])}const C=h(e,[["render",E]]);export{o as __pageData,C as default};
