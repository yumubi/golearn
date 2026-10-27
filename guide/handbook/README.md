# Go 后端速通手册

给有多门语言基础的后端工程师：先用各章建立 Go 的体系、思想与范式，再通过两个项目巩固。01 到 25 节的估计阅读时长合计约 21.4 小时，不含项目实践；路线与每天的目标见 [26 学习路线](26-route.md)。

| 篇 | 节 |
|---|---|
| 心智模型 | [01 世界观](01-worldview.md) · [02 工程骨架](02-skeleton.md) · [03 类型与数据](03-types-data.md) |
| 核心范式 | [04 接口与组合](04-interfaces.md) · [05 错误与资源](05-errors.md) · [06 并发一](06-concurrency-basics.md) · [07 并发二](07-concurrency-engineering.md) · [08 运行时](08-runtime.md) · [09 泛型](09-generics.md) |
| 服务工程 | [10 分层与依赖注入](10-layering-di.md) · [11 HTTP 与 gRPC](11-http-grpc.md) · [12 数据库](12-database.md) · [13 缓存消息幂等](13-cache-mq-idempotency.md) · [14 测试](14-testing.md) · [15 可观测](15-observability.md) · [16 稳定性](16-stability.md) |
| 规模与演进 | [17 数据迁移](17-migration.md) · [18 分布式一致性](18-consistency.md) · [19 微服务](19-microservices.md) · [20 注册发现与负载均衡](20-discovery-lb.md) · [21 搜索 Feed IM](21-search-feed-im.md) |
| 交付与运行 | [22 容器与 Linux](22-containers-linux.md) · [23 部署与 K8s](23-deploy-k8s.md) |
| 项目线 | [24 dnsproxi-go](24-dnsproxi.md) · [25 业务实战与 minifeed 蓝图](25-business-project.md) |
| 路线与速查 | [26 学习路线](26-route.md) · [27 场景决策对照](27-decisions.md) |

第一次读可以从 01 到 09 建立语言与并发基础，再按 10 到 23 学习服务工程与交付。想从具体代码入手，可在读过前置章节后走读 24 的 DNS 代理，再用 25 的业务项目蓝图练习分层和演进；遇到设计选型时用 27 回查各方案的条件与代价。

每节末尾的"想深入去哪查"指向原材料的单元与页码和旧长版 `notes/`，细节靠它们回查；正文中吸收不了的细节收在各节"想深入:正文之外的干货"里，一般不用再翻原材料。01 到 25 节头部时长合计 1282 分钟，约 21.4 小时；26 节是按天拆分的路线，27 节按业务场景横对照各节的设计选择。
