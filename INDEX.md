# golearn 课程总索引

> Go for Backend · 以练代学。类比锚点：Java/Rust（后端视角）。
> 配方仿照 `rs/rust-for-frontend`；实测规矩沿用 `_rust_learning`。

**读什么、按什么顺序，以 [README.md](README.md) 为准**：先读手册，再按主题查阅细节库和项目。
本文件不再复制课程表，避免两处漂移。

| 你现在的状态 | 去哪 |
|---|---|
| 第一次系统学 Go | [`guide/handbook/`](guide/handbook/README.md)，从 [26 七天路线](guide/handbook/26-route.md) 开始 |
| 有底子，快速过一遍 | README 的「读者路径」三条弧（① 语法核心 → ② 服务搭建 → ③ 可观测性） |
| 查某个具体主题 | README 课程表，或在线站 https://yumubi.github.io/golearn/ 搜索 |

## 用法

```sh
# 全仓回归（golearn 与 dnsproxi 两个 module）
go vet ./... && go test -race -count=1 ./...
cd dnsproxi-go && go vet ./... && go test -race -count=1 ./...
```

测试通过只能证明本次运行覆盖的检查通过；还要能解释机制并独立修改代码。P1-P5 练习采用参考实现模式：每道题的参考实现写在
练习文件里，附「Rust 对照」与考点注释；P5 迷你 DI 的两次翻车
（key 撞车死锁、Once 闭包单例假象）记录在 `exercises/p5deep/deep.go` 注释里。
P3/P5 记得加 `-race`：

```sh
go test -race -timeout 30s ./exercises/p3conc/...
```

## 两条主线（贯穿）

1. **Go vs Rust 心智模型**：error vs Result、interface 隐式实现 vs trait、
   goroutine vs tokio task、GC vs 所有权、race detector vs Send/Sync。
2. **工程即目录**：internal/ 编译器强制、包级可见性、interface 定义在使用方。
   精读 ejdb（库怎么组织）和 deepsearch（服务怎么组织）两个真实载体。

## 载体

- `vendored/ejdb/`：嵌入式 JSON 库（P4，快照自 forgejo.gulugulu.moe/clark/ejdb）
- `vendored/deepsearch/`：LLM 搜索服务（P5，快照自 github.com/ClarkQAQ/deepsearch）
- `dnsproxi-go/`：毕业项目本体（Rust 对照版见 P6 正文）
