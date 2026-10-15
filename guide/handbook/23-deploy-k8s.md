# 23 部署与 Kubernetes：你描述要的状态，控制器负责逼近

> 详细程度：速通 · 约 15 分钟
> 材料：W03-1 W03-2、C:go_frame/k8s、K:krm-backend-第八章 K:krm-backend K:krm-frontend · 旧长版：`notes/p26-deploy.md`
> 前置：22 容器与 Linux 运行时
> 说明：本节不启动集群，YAML 与 kubectl 命令都出自材料，没有在本机执行；能在本机验证的只有 Go 代码

## 先看一个让人愣住的现象

删掉一个正在运行的 Pod，过几秒去看，又出现了一个新的。没人手动重启它。下面这个纯 Go 的模型不带任何 K8s 组件，只留这套行为的骨架。先读代码猜输出，再往下看。

```go
package main

import "fmt"

var desired int // 期望副本数：来自你提交的 YAML
var pods = map[string]bool{}
var next int

func reconcile() {
	for len(pods) < desired { // 少了补
		next++
		pods[fmt.Sprintf("pod-%d", next)] = true
	}
	for len(pods) > desired { // 多了删
		for name := range pods {
			delete(pods, name)
			break
		}
	}
}

func names() []string {
	ns := make([]string, 0, len(pods))
	for n := range pods {
		ns = append(ns, n)
	}
	for i := 1; i < len(ns); i++ { // 排序只为输出稳定
		for j := i; j > 0 && ns[j-1] > ns[j]; j-- {
			ns[j-1], ns[j] = ns[j], ns[j-1]
		}
	}
	return ns
}

func main() {
	desired = 3
	reconcile()
	fmt.Println(names())
	delete(pods, "pod-2") // 模拟 Pod 崩溃
	fmt.Println(names())
	reconcile() // 模拟控制器再跑一轮
	fmt.Println(names())
	desired = 5
	reconcile()
	fmt.Println(names())
}
```

```text
[pod-1 pod-2 pod-3]
[pod-1 pod-3]
[pod-1 pod-3 pod-4]
[pod-1 pod-3 pod-4 pod-5 pod-6]
```

`reconcile` 只干一件事：数一数现状和期望差多少，少了补，多了删。Pod 崩溃是"现状变了"，调大 `desired` 是"期望变了"，无论哪种，同一轮循环都会把差距抹平。真实的 K8s 就是这个模型的工程化：期望来自你提交的 YAML，`kubectl apply` 让实际状态向配置收敛，多了删，少了加。〔W03-2 p24〕材料把 K8s 概括成配置驱动、元数据驱动的声明式框架：用户只提供配置，K8s 按配置执行动作。〔W03-2 p18〕这就是**声明式**：你不描述步骤，只描述结果。删除 Pod 会被自动补上，是因为控制器的逻辑是比较两份状态的差值，而不是"收到删除事件就做某件事"：即使事件丢了，下一轮对账照样发现少一个。（"现状来自 API server"、控制器靠 watch 加对账驱动，这些内部机制材料没讲，本节未在本机验证，上面的 Go 模型只保留差值收敛这一点。）这一节看完，Deployment、Service、Ingress、HPA 你都会认出这是同一个模式。

## 和你已知语言的差异

| 问题 | compose | K8s |
|---|---|---|
| 部署单位 | service，单机 | Pod（跑在哪个节点由调度器定） |
| 谁保证数量 | 没人，崩了就没了 | Deployment 控制循环补齐 |
| 服务发现 | 同网络里的服务名 | 同理：Service 名当主机名（材料的连接串就这么写） |
| 依赖顺序 | `depends_on` 加 `healthcheck` | readinessProbe，或 InitContainer（材料没讲，未在本机验证） |
| 资源限额 | `cpus`、`mem_limit` | `resources.limits`（底层同样是 cgroup，见 22 节） |
| 配置 | `environment` 写死在文件里 | ConfigMap 与 Secret（材料没细讲）；配置和镜像分离是共同目标 |

第二行是本质差异。compose 把你的声明翻译成一次性命令，执行完就退场；K8s 的控制器常驻，每轮循环都在对账。第四五行是同一概念的换皮（compose 的细节见旧章 `notes/p26-deploy.md`，22 节没有展开）。

## 一、三个最小概念：Pod、Deployment、Service

材料给的最小集：Pod 是实例，Service 是逻辑上的服务，Deployment 管 Pod，少了就补、多了就删。一个 Web 应用部署三个实例，就是一个 Service 对应三个 Pod。〔W03-2 p6〕

三个概念怎么组合成一份能跑的配置？以材料里部署 webook 的例子看 Deployment 的骨架。〔W03-2 p16-17〕

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: webook
spec:
  replicas: 3
  selector:
    matchLabels:
      app: webook
  template:
    metadata:
      labels:
        app: webook
    spec:
      containers:
        - name: webook
          image: flycash/webook:v0.0.1
          ports:
            - containerPort: 8080
```

`spec` 三要素对应三个问题：〔W03-2 p19-21〕

1. **`replicas`**：要几个。这就是开头 `desired` 的 YAML 写法。
2. **`selector`**：我怎么在一大堆 Pod 里认出哪些是我的。按标签筛，这里认 `app=webook`。
3. **`template`**：每个 Pod 长什么样。Pod 里面跑的就是容器，可以跑多个。

注意 selector 和 template 的 labels 写了同一个键值。这不是巧合：Deployment 靠标签认领自己创建的 Pod，两边不一致，它创建出来的 Pod 自己都找不到，也就谈不上收敛。另外 `apiVersion` 决定 K8s 怎么解读这份配置，写错了 K8s 就不知道你是谁。〔W03-2 p18〕

对比 compose：它把 YAML 翻译成一次启动动作，起好之后没有常驻的控制器按副本数对账；K8s 里 `replicas` 是控制器每一轮都在比较的期望值。

## 二、Service：稳定的门牌，和三个端口

Pod 会死会重生，重生出来的是新实例（上面模型里 `pod-2` 没了，补的是 `pod-4`），直接连某个 Pod 是不可维护的。材料的解法是把 Pod 封装成逻辑上的服务，即 Service，只有 Deployment 的话从外面访问不了。〔W03-2 p23〕Service 在逻辑上对应业务里的一个服务，背后可以是多个 Pod。〔W03-2 p6〕

材料里部署 Redis 时用到了 Service 的三种端口，这是新手最容易混的地方：〔W03-2 p41〕

- **`port`**：Service 自己的端口。集群内其他 Pod 连 `demo-redis-service:6379`，用的是它。
- **`targetPort`**：转发目的地，Pod 容器上监听的端口。
- **`nodePort`**：从集群**外**访问的端口，必须在 30000 到 32767 之间，比如 `redis-cli -p 31379`。〔W03-2 p40-42〕

流量路径：外部请求先进 nodePort，转给 Service 的 port，再落到某个 Pod 的 targetPort。材料用 `type: NodePort` 把 Redis 暴露到集群外。〔W03-2 p39〕如果只给集群内部用，不需要 nodePort，通过 Service 的名字和 port 访问即可（K8s 默认的 Service 类型 `ClusterIP` 就是干这个的，这一点材料没讲，未在本机验证）。

顺带回答一个老问题：K8s 说不再内置 Docker 了，那我 Docker 构建的镜像还能用吗？能。Docker 实现的是 OCI 标准，镜像归标准管，不归 Docker 管。〔W03-2 p22〕

## 三、Ingress、PV 与 PVC：规则与实现的分离

**Ingress**：七层的路由规则，按域名或路径决定外部请求转给哪个 Service。和 Service 的 LoadBalancer 对比：Service 强调把流量转发到 Pod 上，Ingress 强调转发到不同的 Service 上。〔W03-2 p45〕Ingress 是七层规则（按域名或路径）是通用说法，材料的图只画了"路由规则"。Ingress 本身只是一份规则，真正执行的是 ingress controller。K8s 作为设计者只关心规则，谁来执行、怎么执行一概不管。〔W03-2 p46〕

C 项目的实际清单就是这个形状：`/blog` 转给 `svc-blog`，`/lottery` 转给 `svc-lottery`，一个入口后面挂多个服务。〔C:go_frame/k8s/deploy/ingress-nginx.yaml:8-24〕

**PV 与 PVC** 是同一思想的存储版。MySQL 要落盘，存储空间被抽象为 PersistentVolume。为什么拆成两个对象？因为作为 K8s 设计者，你既不知道容器里跑的是什么、需要怎么存，也管不了现实中有多少种硬盘和文件协议，只能提供一个抽象让具体实现去管。〔W03-2 p28〕于是：PersistentVolume 是**存储侧**声明我有什么特性，PersistentVolumeClaim 是**使用侧**声明我需要什么特性，K8s 负责撮合两者。〔W03-2 p31-32〕材料里叫 `mysql-pv-claim` 的对象声明了 `storage: 1Gi` 和 `accessModes: ReadWriteOnce`，一个声明方管理挂载点，另一个声明究竟挂的是什么。〔W03-2 p29-31〕这就是第 04 节"接口与实现分离"在基础设施层的翻版。`accessModes` 有三个取值：`ReadWriteOnce` 只能被单个 Pod 读写，`ReadOnlyMany` 多 Pod 只读，`ReadWriteMany` 多 Pod 读写。〔W03-2 p35〕

**HPA**（自动伸缩）同样是规则与数据分离：材料里的规则是 `targetCPUUtilizationPercentage: 50`，副本数在 2 到 5 之间，目标是 `dep-blog`。〔C:go_frame/k8s/deploy/hpa-blog.yaml:1-14〕利用率是相对"分配给它的 CPU"算的百分比，所以被伸缩的 Deployment 要设 `resources.requests.cpu`（材料的 `dep-blog.yaml` 设了 `10m`）；不设的话百分比没有分母，HPA 无法工作，这是 K8s 通用行为，材料只演示了设置的写法，未在本机验证。数据由 metrics-server 提供，材料的操作手册里是单独安装它的。

| 对象 | 回答的问题 | 谁执行 |
|---|---|---|
| Deployment | 要几个、用哪个镜像 | 控制循环 |
| Service | 集群内怎么找到它 | 集群网络组件（材料没展开） |
| Ingress | 按域名/路径转给哪个 Service | ingress controller |
| PV 与 PVC | 存储怎么撮合 | 存储实现 |
| HPA | 什么时候加副本 | metrics-server 供数 |

## 四、应用代码要配合的几件事

编排器再聪明，代码不配合也白搭。三件事：

1. **配置按环境切换**。材料用的手法是 Go 编译标签：`dev.go` 和 `k8s.go` 两个文件初始化同一个配置结构体，k8s.go 上写 `//go:build k8s`，`go build -tags=k8s` 选择；连接串里的主机名直接写 Service 名（`webook-mysql:3308`），不是 IP。〔W03-2 p51-52〕更常见的做法是环境变量加配置文件，思路相同：配置与镜像分离，镜像一次构建，处处运行。
2. **服务名当主机名**。compose 里的服务名和 K8s 的 Service 名都是集群内 DNS 名。容器里的 `127.0.0.1` 是容器自己，把依赖写成宿主机地址，是第一次上容器最常见的失败现场（旧章 `notes/p26-deploy.md`）。
3. **探针与限额**。22 节讲过探针（docker 的健康检查）和 cgroup 限额：K8s 里探针由 `readinessProbe` 之类的字段承担（具体语义材料没讲，未在本机验证），`resources.limits` 底下同样是 cgroup，HPA 又依赖 `requests.cpu`。三处是同一份约束在不同层的回声。

## 五、client-go 多集群后端：krm 项目

K8s 自身的 API 是 HTTP，client-go 是它的 Go SDK。资料里的 krm 是一个 K8s 资源管理平台，后端 Gin 加 client-go，前端 Vue3。它演示了"用 K8s 管理 K8s"，几个结构值得记：〔K:krm-backend-第八章/controllers/cluster/addOrUpdate.go:1-60〕

- **集群即 Secret**。新增集群时先用 kubeconfig 探活（`getClusterStatus` 里调 `Discovery().ServerVersion()`，取得到就是活的），再把 kubeconfig 存进 K8s 自己的 Secret，状态放 annotations，同时同步内存 map。用 K8s 存元数据，免了自己再配一个数据库。
- **统一请求结构**。`ClusterId`、`Namespace`、`Name`、`Item` 四元组构成 `BasicInfo`，通用 CRUD 由第三方库 `kubeutils` 的 `KubeUtilser` 接口承接（接口不在项目里，各资源 controller 里都是 `var kubeUtilser kubeutils.KubeUtilser`）；十几类资源的 controller 同构，只是类型不同。〔K:krm-backend-第八章/controllers/controllers.go:17-31〕
- **每请求现建 clientset**。按 `ClusterId` 查 kubeconfig，`RESTConfigFromKubeConfig` 再建 clientset。简单，但没有缓存连接，是它最明显的可改进处。〔K:krm-backend-第八章/controllers/controllers.go:145-178〕
- **薄分层**。路由、controller、kubeutils，没有 service 层，业务逻辑很薄。适合"透传 K8s API"的管理平台，不适合复杂业务。
- **技术债**。登出是空实现，服务端没有 token 黑名单；登录是直接对配置值做字符串比对（环境变量里存 md5 十六进制串，代码没有再做哈希或加盐）。〔K:krm-backend-第八章/controllers/auth/auth.go:30-57〕

前端两个版本：`krm-frontend` 是基础版（集群、命名空间、节点、用户），`krm-backend` 目录里装的其实是完整 Vue3 前端（加了资源创建表单、YAML 预览）。分工很清楚：日常操作**前端只传 `clusterId`**，由后端按 id 取 kubeconfig。〔K:krm-backend/src/view/components/ClusterNamespaceSelector.vue:40-60〕唯一的例外是"添加集群"表单：用户把 kubeconfig 粘贴进去提交给后端一次，此后凭据留在后端的 Secret 和内存 map 里。〔K:krm-backend/src/view/cluster/Add.vue:124-127〕kubeconfig 通常等同于集群的管理凭据，所以不能让它在浏览器里常驻或回传，这是设计取舍，不是材料的原话。

本地想体验集群又不想买机器，材料用 kind：一个 Docker 容器模拟一个节点，`kind create cluster --name dqq --config deploy/cluster-dqq.yaml` 起 1 控制面加 3 worker，本地构建的镜像用 `kind load docker-image blog:v1.0.0 --name dqq` 灌进节点。〔C:go_frame/k8s/deploy/cluster-dqq.yaml:1-15〕〔C:go_frame/k8s/deploy/操作手册.md:23-53〕这些命令只是读了材料，没有在本机执行。

## 小结

1. K8s 是声明式系统：你提交期望，控制循环让现状向期望收敛，少了补，多了删。
2. Pod 是实例，Deployment 管数量，靠 selector 认领；selector 与模板标签必须一致。
3. Service 是稳定入口；`port` 是 Service 的，`targetPort` 是 Pod 的，`nodePort` 是集群外的。
4. Ingress 只是一份路由规则，执行靠 ingress controller；PV 与 PVC 是存储侧与使用侧的分离声明。
5. 应用代码配合三件事：配置与镜像分离、服务名当主机名、探针与限额。
6. client-go 的套路：kubeconfig 建 clientset，API 都按（集群，命名空间，名字）定位资源。

## 常见坑

1. `selector` 与 Pod 模板标签写得不一致，Deployment 找不到自己的 Pod，收敛无从谈起。
2. 把 `port`、`targetPort`、`nodePort` 混为一谈；外部连不上先问自己走的是哪条路径。
3. 镜像依赖本地构建，节点上没有，`imagePullPolicy` 又拉不下来；kind 场景要 `kind load` 先灌镜像。
4. HPA 没设 `resources.requests.cpu`，永远不会伸缩。
5. 容器里把依赖写成 `127.0.0.1`，连的是容器自己。
6. 前端拿到 kubeconfig；等价于交出集群管理权。

## 判断题与答案

先自己答，再看后面。

1. 删掉一个 Pod，Deployment 会发生什么？为什么这叫声明式？
2. Ingress 和 Service 的 LoadBalancer 分工有什么不同？
3. 前端为什么不应该拿到 kubeconfig？

**答案**

1. 控制循环发现实际副本比 `replicas` 少一个，按模板补一个新的。声明式指系统维护的是"期望状态"这个事实，任何偏离都会被下一轮循环修正，靠的是比较差值，而不是针对"删除"这一个动作写处理逻辑。
2. LoadBalancer 关注把流量从集群外引到一个 Service 再分给 Pod；Ingress 关注在多个 Service 之间按域名和路径做七层分发。层次不同：前者管把外部流量引到 Service，后者管在多个 Service 之间分发（通常说 Ingress 是七层路由，LoadBalancer 偏四层，材料没有展开）。
3. kubeconfig 通常包含访问集群的管理凭据，放到浏览器里就能被页面上的任何代码读取；krm 的做法是日常只传 `clusterId`，由后端持有凭据并代理请求。

## 在项目里

这里没有现成例子：`dnsproxi-go` 目前没有 K8s 清单，只有旧章里 `labs/compose` 的编排（`dnsproxi` 依赖 `httpkv` 的 healthcheck，`DNS_UPSTREAM: "upstream:15300"` 用服务名做主机名）。要搬上 K8s，按上面差异表的对应关系逐条换：`DNS_UPSTREAM` 这类环境变量放进 ConfigMap；UDP 监听端口用 Service 的 `protocol: UDP` 暴露；compose 的 `condition: service_healthy` 在 K8s 里大致对应探针与 InitContainer（具体写法材料没讲，未在本机验证）。

## 想深入去哪查

- 原材料：W03-2（K8s 入门、PV 与 PVC、Ingress）、C:go_frame/k8s（kind 编排、HPA、节点标签）、K:krm-backend-第八章（client-go 后端）、K:krm-backend 与 K:krm-frontend（前端）。
- 旧长版：`notes/p26-deploy.md`（compose 四个面的实测）。
- W03-1 的多实例 Session 与限流，属于部署引发的代码问题，在 13 与 16 节展开。
- kind 集群与 HPA 压测的材料只做了静态阅读，没有在本机执行。
