# 23 部署与 Kubernetes

> 详细程度：速通 · 约 15 分钟
> 材料：W03-1 W03-2、C:go_frame/k8s、K:krm-backend-第八章 K:krm-backend K:krm-frontend、旧章 `notes/p26-deploy.md`
> 前置：22 容器与 Linux 运行时
> 说明：本节不启动集群，所有 YAML 与 kubectl 命令都没有在本机执行；能在本机验证的只有 Go 代码部分

## 这一节要建立的认知

从 compose 到 Kubernetes，概念是同一套：组网、服务发现、启动顺序、资源限额。K8s 多出来的是**声明式**：你不写"启动三个容器"，只写"我要三个副本"，由控制器循环不断让现状向期望收敛。理解这一点，K8s 的所有对象（Deployment、Service、Ingress、HPA）都是同一个模式的变体。

## 一、声明式与收敛

```go
func (c *Cluster) reconcile() {
	for len(c.pods) < c.desired.Replicas { // 少了补
		c.next++
		c.pods[fmt.Sprintf("pod-%d", c.next)] = true
	}
	for len(c.pods) > c.desired.Replicas { // 多了删
		for name := range c.pods {
			delete(c.pods, name)
			break
		}
	}
}
```

我跑了一个纯 Go 的模型验证行为：期望 3 个副本时有 `pod-1 pod-2 pod-3`；`pod-2` 崩溃后只剩两个；再执行一次 reconcile，补出 `pod-4`；把期望改成 5，补到 5 个。真实的控制器同理，只是现状来自 API server，期望来自你提交的 YAML。资料里对应的一句话是：`kubectl apply` 让实际状态向配置收敛，多了删，少了加。〔W03-2 p23-24〕

## 二、核心对象的关系

三个最小概念：Pod 是实例，Deployment 管 Pod 的数量（少了补多了删），Service 是逻辑服务入口（三个 Pod 对应一个 Service）。〔W03-2 p6〕Deployment 的三要素：`replicas`、`selector.matchLabels`（按标签筛 Pod）、`template`（Pod 模板）。`selector` 和 `template.metadata.labels` 必须一致，否则管不到自己创建的 Pod。〔W03-2 p19-21〕

| 对象 | 回答的问题 | 备注 |
|---|---|---|
| Deployment | 要几个、用哪个镜像 | 不要直接创建 Pod |
| Service | 集群内怎么找到它 | `port` 是 Service 自己的端口，`targetPort` 是 Pod 上的，`nodePort` 是集群外访问端口〔W03-2 p41〕 |
| Ingress | 外部请求按路径或域名转发到哪个 Service | 只是规则，真正执行的是 ingress controller〔W03-2 p45-46〕 |
| PV 与 PVC | 存储怎么撮合 | 存储侧声明有什么，容器侧声明要什么，K8s 撮合两者，典型的接口与实现分离〔W03-2 p28-32〕 |
| HPA | 按 CPU 自动伸缩 | 前提是 Deployment 设了 `resources.requests.cpu`，并且集群装了 metrics-server〔C:go_frame/k8s/deploy/hpa-blog.yaml〕 |

K8s 不再内置 Docker 之后镜像依然可用，因为 Docker 构建的是 OCI 标准镜像。〔W03-2 p22〕

## 三、应用代码要配合的几件事

1. **配置按环境切换**：资料用 Go 的编译标签，`dev.go` 与 `k8s.go` 两个文件初始化同一个配置结构，`go build -tags=k8s` 选择；连接串里的主机名直接写 Service 名。〔W03-2 p50-52〕更常见的做法是环境变量加配置文件，思路相同：配置与镜像分离。
2. **服务名当主机名**：compose 里的服务名和 K8s 的 Service 名都是集群内 DNS 名。容器里的 `127.0.0.1` 是它自己，把上游写成宿主机地址是第一次上容器时的常见失败。（旧章 `notes/p26-deploy.md` 的实测）
3. **探针与限额**：22 节讲过，`readinessProbe` 决定要不要给它流量，`resources.limits` 就是 cgroup。

compose 与 K8s 的对应关系：

| compose | K8s |
|---|---|
| service | Deployment 加 Service |
| `depends_on` 加 `healthcheck` | readinessProbe 加 InitContainer |
| `cpus` 与 `mem_limit` | `resources.limits` |
| `ports` | Service 或 Ingress |
| `environment` | ConfigMap 与 Secret |

学习顺序建议：先把 compose 的四个面（组网、发现、顺序、限额）玩透，再背 API。（旧章 `notes/p26-deploy.md`，compose 实测未在本机复验）

## 四、client-go 多集群后端：krm 项目

资料里的 krm 是一个 K8s 资源管理平台，后端 Gin 加 client-go，前端 Vue3。后端的结构值得记：

- **集群即 Secret**：新增集群时先用 kubeconfig 探活（能取到 `ServerVersion` 就是活的），再把 kubeconfig 存进 K8s 自己的 Secret，状态放 annotations，同时同步内存 map。用 K8s 存元数据，免了自己再配数据库。〔K:krm-backend-第八章/controllers/cluster/addOrUpdate.go:1-60〕
- **统一请求结构**：`ClusterId`、`Namespace`、`Name`、`Item` 四元组，通用 CRUD 全部收口到一个 `KubeUtilser` 接口；十几类资源的 controller 同构，只是类型不同。〔K:krm-backend-第八章/controllers/controllers.go:20-100〕
- **每请求现建 clientset**：按 `ClusterId` 查 kubeconfig，再创建 clientset。简单，但没有缓存连接，是它的明显可改进处。〔K:krm-backend-第八章/controllers/controllers.go:153-182〕
- **薄分层**：路由、controller、kubeutils，没有 service 层；业务逻辑很薄。适合"透传 K8s API"的管理平台，不适合复杂业务。
- 技术债：登出是空实现，服务端没有 token 黑名单；登录是直接对配置里的值做字符串比对（环境变量里存的是 md5 十六进制串，代码没有再做哈希或加盐）。〔K:krm-backend-第八章/controllers/auth/auth.go:15-69〕

前端有两个版本：`krm-frontend` 是基础版（集群、命名空间、节点、用户），`krm-backend` 目录里装的其实是完整的 Vue3 前端（加了资源创建表单、YAML 预览）。分工很清楚：**前端只传 `clusterId`，从不接触 kubeconfig**，所有集群凭据都留在后端。〔K:krm-backend/src/view/components/ClusterNamespaceSelector.vue:38-60〕

## 常见坑

1. `selector` 与 Pod 模板标签写得不一致，Deployment 找不到自己的 Pod。
2. 把 `port`、`targetPort`、`nodePort` 混为一谈。
3. 镜像依赖本地构建，节点上没有，`imagePullPolicy` 又拉不下来。
4. HPA 没设 `requests.cpu`，永远不会伸缩。
5. 容器里把依赖写成 `127.0.0.1`。

## 判断题

1. 删掉一个 Pod，Deployment 会发生什么？为什么这叫声明式？
2. Ingress 和 Service 的 LoadBalancer 分工有什么不同？
3. 前端为什么不应该拿到 kubeconfig？

## 在项目里

这里没有现成例子：`dnsproxi-go` 目前没有 K8s 清单，只有旧章里的 `labs/compose` 编排。要把它搬上 K8s，对应关系见上面的映射表，比如 `DNS_UPSTREAM` 这类环境变量放进 ConfigMap，UDP 端口通过 Service 的 `protocol: UDP` 暴露。

## 想深入去哪查

- 原材料：W03-2（K8s 入门、PV 与 PVC、Ingress）、`C:go_frame/k8s`（kind 编排、HPA、节点标签）、`K:krm-backend-第八章`（client-go 后端）、`K:krm-backend` 与 `K:krm-frontend`（前端）。
- 旧长版：`notes/p26-deploy.md`。
- 资料里的 kind 集群与 HPA 压测，当时的环境联网受限，没有真正跑起来；这部分只有静态阅读。
