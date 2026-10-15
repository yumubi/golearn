# 20 注册发现与负载均衡

> 详细程度:标准 · 约 45 分钟
> 材料:W12-1、W12-2、W13-1、W13-2、C:dachang/etcd · 旧长版:`notes/p10-grpc.md`(gRPC 基础)
> 前置:16 稳定性、19 微服务拆分与治理

## 先看一个让人愣住的现象

把同一个服务部署了两个节点,分别监听 8090 和 8091,客户端连打 4 次 RPC,你预期流量怎么分?大多数人会猜"对半分"。但用 gRPC 默认配置跑出来的结果是:4 次全落在 8090,8091 一次都没被用到。〔W13-2 p4〕

这不是 bug,是 gRPC 默认的负载均衡策略 `pick_first` 在忠实执行自己的语义:所有地址都试一遍,选第一个连得上的,然后**所有请求都固定发往这一个节点**。其余节点纯粹是"备胎",只在主节点连不上时才顶上。下面用最小的代码把这个行为演出来(`Picker` 和 `SubConn` 的名字都对应 gRPC 里同名概念):

```go
package main

import (
	"fmt"
	"sync/atomic"
)

// SubConn 对应 gRPC 的 balancer.SubConn:名字里是 Conn,实际代表"连到某个节点的连接池"。
type SubConn string

// Picker 对应 balancer.Picker:每次 RPC 调用它挑一个连接。
type Picker interface {
	Pick() SubConn
}

// pickFirst 是 gRPC 的默认策略:永远返回解析结果里的第一个。
type pickFirst struct{ subConns []SubConn }

func (p *pickFirst) Pick() SubConn { return p.subConns[0] }

// rrPicker 是轮询:原子递增下标,对节点数取余。
type rrPicker struct {
	subConns []SubConn
	next     uint32
}

func (p *rrPicker) Pick() SubConn {
	i := atomic.AddUint32(&p.next, 1) % uint32(len(p.subConns))
	return p.subConns[i]
}

func main() {
	scs := []SubConn{"127.0.0.1:8090", "127.0.0.1:8091"}
	pf := &pickFirst{subConns: scs}
	fmt.Print("pick_first 连打 4 次: ")
	for i := 0; i < 4; i++ {
		fmt.Print(pf.Pick(), " ")
	}
	fmt.Println()
	rr := &rrPicker{subConns: scs}
	fmt.Print("round_robin 连打 4 次: ")
	for i := 0; i < 4; i++ {
		fmt.Print(rr.Pick(), " ")
	}
	fmt.Println()
}
```

```text
pick_first 连打 4 次: 127.0.0.1:8090 127.0.0.1:8090 127.0.0.1:8090 127.0.0.1:8090
round_robin 连打 4 次: 127.0.0.1:8091 127.0.0.1:8090 127.0.0.1:8091 127.0.0.1:8090
```

同一个列表,`pick_first` 死磕一个节点,`round_robin` 才开始分流。所以这一节要回答的其实是三个连环问题:服务实例列表从哪来(服务注册与发现);拿到列表之后这一次调谁(负载均衡);gRPC 把这两件事各自抽象成了什么接口,我们又该怎么接进去。

## 和你已知语言的差异

| 主题 | Java/Kotlin 里常见 | Go(本节) |
|---|---|---|
| 服务发现 | Spring Cloud 全家桶,SDK 深度整合 | 核心就是 gRPC 的 `Resolver` 接口,自己接注册中心〔W12-2 p26〕 |
| 负载均衡 | Ribbon/Spring Cloud LoadBalancer,注解切换 | gRPC 的 `Balancer`/`Picker` 接口,JSON 配置切换〔W13-2 p5〕 |
| 心跳保活 | 常由框架代劳 | etcd 的 Lease 加 KeepAlive,谁来续约要自己选〔W12-2 p11〕 |
| 注册自己的地址 | 容器平台注入 | 常见做法是 UDP 拨号探测出口 IP 或枚举网卡〔W12-2 p16〕 |
| 生态默认值 | 常见的是 Eureka/Nacos 这类注册中心 | etcd 常用作注册中心〔W12-2 p36〕;K8s 里材料作者推荐直接用 Service 名字(DNS)〔W12-2 p18〕 |

## 一、服务发现:从写死 IP 到注册中心

服务拆成多个实例之后,调用方要回答两个问题:**这个服务现在有哪些实例**(发现),**这一次调哪一个**(负载均衡)。两个答案都会随时间变,所以不能写死。解决"列表从哪来"有三代方案,演进主线就是"缓存与新鲜度"的权衡。

**第一代:IP 直连。** 配置文件里写死 `10.0.0.1:8090`,没有注册也没有发现。IP 会变、难维护,但联调时直接调同事电脑的 IP 方便对方本地 DEBUG,排查只出现在个别实例上的 bug 时也会定向发请求过去。〔W12-1 p5〕

**第二代:域名 + DNS。** 解析域名有开销,客户端一般会缓存解析结果,于是两难:不缓存,每次调用多一跳查询;缓存了,节点上下线无法及时反映到客户端列表里。〔W12-1 p6〕

**第三代:注册中心。** 用"主动通知"替代"过期重拉":服务端启动时注册;客户端首次调用前查询并缓存;节点变动时注册中心主动推给客户端。没有明显短板,硬要说就是大规模集群下注册中心本身可能成为瓶颈。〔W12-1 p7〕


数据量大了还有一步优化:**服务自省**(Dubbo 提出)。注册中心只存最小的定位数据,其余元数据变化少,放到独立的元数据服务里查一次就够。〔W12-1 p8〕

## 二、注册的完整生命周期

### 注册什么、什么时候注册

注册数据里关键的是定位信息(IP 加端口),其余是治理信息,比如分组、权重。〔W12-1 p13〕权重这条伏笔在第三节展开。

启动时机上,服务端连不上注册中心就应该直接失败退出:此时服务还没对外提供流量,失败代价最小,拖着带病上线反而危险。〔W12-1 p12〕还有个容易忽略的顺序细节:要**先确保端口打开,再注册**,否则客户端拿着列表打过来,连接被拒。〔W12-2 p14〕

注册自己的地址有个小坑:不能用 `127.0.0.1`,得注册和客户端同处一个局域网的地址。从配置文件读不可靠,常见办法是枚举网卡取第一个非环回的 IPv4(`net.InterfaceAddrs` 遍历,跳过 loopback 和 IPv6),平台注入环境变量或 UDP 拨号探测出口 IP 也是可行做法。课件里的 `GetLocalIP` 函数就是枚举网卡这路,找不到时返回 `ERR_NO_LOCAL_IP_FOUND`。〔C:dachang/etcd/service_registration_center/common.go:25-51〕〔W12-2 p16〕

### 心跳就是租约续租

注册成功不等于一劳永逸。注册机制的本质是**租约**:数据带过期时间,心跳就是续租。心跳有两种模式:服务端主动(只要没收到网络错误就当连接正常)或注册中心主动探测;心跳失败,注册中心就判定节点崩溃并通知客户端。〔W12-1 p14〕"失败"可以是一次,也可以是连续多次,这两个参数都是权衡:间隔越短压力越大;判定次数越少发现越快但越怕偶发抖动误踢,越多越稳但发现越慢。〔W12-1 p15〕

etcd 把这套机制做成 Lease 加 KeepAlive:先 `Grant` 创建带 TTL 的租约,`Put` 时用 `WithLease` 把 key 挂上租约,再 `KeepAlive` 自动续约;节点崩溃没人续租,key 到期自动消失。etcd 的 KeepAlive 默认每隔 ttl/3 续约一次,选项很少。大多数微服务框架直接用它的租约机制当心跳,实践中没什么问题,只是没有自定义重试和判定策略的空间。〔W12-2 p11-12〕课件里 `etcd.go` 的 `keepAlive` 函数演示了 Grant、Put 带租约、KeepAlive 的基本用法。〔C:dachang/etcd/etcd.go:59-85〕

### 优雅下线五步

收到退出信号能不能立刻结束进程?不能:进程里可能还有读了一半的请求,客户端可能正发着下一个请求。完整流程是五步:通知注册中心自己要下线;不再接受新请求,网络上读了一半的请求读完即拒,不进业务代码;等在处理的请求和定时任务结束;结束运行;整个退出过程有超时控制,超时强退。〔W12-1 p18-19〕课件里的服务端收 `SIGINT`/`SIGTERM` 后先主动注销再退出,即使没来得及注销,最晚一个 TTL 之后注册中心也会把这个节点踢掉。〔C:dachang/etcd/service_registration_center/server/server.go:64-75〕

### 客户端发现的两个时机

客户端做服务发现有两个时机:启动时发现,从配置或注册接口拿到全部要用的服务,初始化时就查好,失败就不启动;或者惰性发现,第一次真正调用某个服务时才去查。后者冷启动快,但首次调用会多一次查询延迟。另外客户端和注册中心之间也要保持连接(心跳),这样节点变动才能推得过来。〔W12-1 p20〕

## 三、拿 etcd 实现一遍

etcd 是 Go 写的、基于 Raft 的强一致键值存储。用做注册中心的套路:服务端把 `服务名/实例地址` 当 key 写进去,挂上租约;客户端启动后把某服务名前缀下的 key 拉一遍建本地缓存,再装一个 Watcher,etcd 数据一变就更新缓存,平时调用根本不碰 etcd。

服务端这边,注册逻辑值得细看。第一次注册是 `Grant` 创建租约、`Put` 把 key 挂上租约;后续周期性触发时,同一个调用走的是 `KeepAliveOnce` 续约分支:

〔C:dachang/etcd/service_registration_center/server/service_hub.go:54-84〕

```go
func (hub *ServiceHub) Regist(service string, endpoint string, leaseID etcdv3.LeaseID) (etcdv3.LeaseID, error) {
	ctx := context.Background()
	if leaseID <= 0 {
		// 创建一个租约，有效期为heartbeatFrequency秒
		if lease, err := hub.client.Grant(ctx, hub.heartbeatFrequency); err != nil {
			log.Printf("创建租约失败：%v", err)
			return 0, err
		} else {
			key := strings.TrimRight(service_registration_center.SERVICE_ROOT_PATH, "/") + "/" + service + "/" + endpoint
			// 服务注册
			if _, err = hub.client.Put(ctx, key, "", etcdv3.WithLease(lease.ID)); err != nil { //只需要key，不需要value
				log.Printf("写入服务%s对应的节点%s失败：%v", service, endpoint, err)
				return lease.ID, err
			} else {
				return lease.ID, nil
			}
		}
	} else {
		//续租
		if _, err := hub.client.KeepAliveOnce(ctx, leaseID); err == rpctypes.ErrLeaseNotFound { //续约一次，到期后还得再续约
			return hub.Regist(service, endpoint, 0) //找不到租约，走注册流程(把leaseID置为0)
		} else if err != nil {
			log.Printf("续约失败:%v", err)
			return 0, err
		} else {
			// log.Printf("服务%s对应的节点%s续约成功", service, endpoint)
			return leaseID, nil
		}
	}
}
```

注意 `ErrLeaseNotFound` 这个分支:租约一旦过期被回收,续约就再也续不上,只能整个重新注册。优雅下线对应的注销就是一个 `Delete`(`UnRegist` 函数,把 key 从 etcd 删掉),客户端这边则简单得多:SDK 把查询、缓存、监听全包了。〔C:dachang/etcd/service_registration_center/server/service_hub.go:85-96〕

〔C:dachang/etcd/service_registration_center/client/service_hub.go:96-108〕

```go
// 服务发现。把第一次查询etcd的结果缓存起来，然后安装一个Watcher，仅etcd数据变化时更新本地缓存。这样可以降低etcd的访问压力
func (hub *ServiceHub) GetServiceEndpointsWithCache(service string) []string {
	hub.watchEndpointsOfService(service) //监听etcd的数据变化，及时更新本地缓存
	if endpoints, exists := hub.endpointCache.Load(service); exists {
		return endpoints.([]string)
	} else {
		endpoints := hub.getServiceEndpoints(service)
		if len(endpoints) > 0 {
			hub.endpointCache.Store(service, endpoints) //查询etcd的结果放入本地缓存
		}
		return endpoints
	}
}
```

最后在客户端侧做负载均衡:从缓存拿到实例列表,随便选一个算法挑节点,再按地址复用连接。

〔C:dachang/etcd/service_registration_center/client/client.go:19-46〕

```go
func getClient() idl.HelloServiceClient {
	hub := GetServiceHub(service_registration_center.ETCD_CLUSTER)
	//根据service name获取所有可用的servers
	servers := hub.GetServiceEndpointsWithCache(service_registration_center.HELLO_SERVICE)
	if len(servers) == 0 {
		log.Printf("服务%s没有对应的实例", service_registration_center.HELLO_SERVICE)
		return nil
	} else {
		//采用某种负载均衡算法，选择一个server
		idx := rand.Intn(len(servers)) //这里采用随机法
		server := servers[idx]
		...
		if client, exists := serverConn.Load(server); exists {
			return client.(idl.HelloServiceClient)
		} else {
			// 连接到GRPC服务端
			conn, err := grpc.Dial(server, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				log.Printf("连接GRPC服务端失败 %v\n", err)
				return nil
			}
			...
		}
	}
}
```

这段代码的演化路径和第一节的三代方案完全同构:把 `rand.Intn` 换成别的策略,就是第三节要展开的算法问题;把手工缓存换成 gRPC 的 Resolver 抽象,就是第四节要展开的接入问题。

## 四、etcd 注册中心崩了怎么办

选型离不开 CAP:网络分区不可避免时,一致性和可用性只能保一个。ZooKeeper 是 CP,Eureka 是 AP,Nacos 两种模式都支持。面试标准答案是注册中心选 AP,务实的说法是小规模随便选,大规模优先 AP:拿到稍微过期的节点列表,通常好过完全拿不到列表。〔W12-2 p29-31〕ZooKeeper 是主从结构,缺点里有主节点选举期间集群不可用,材料据此判它是 CP;Eureka 是对等集群、选 AP;Nacos 同时支持两种。〔W12-2 p33-35〕

etcd 在 CAP 里应当归为 **CP**:它用 Raft 保证强一致,〔W12-2 p36〕发生网络分区时,拿不到多数派的那一侧不再服务,选择的是一致性而放弃了可用性。材料 W12-2 p30 把 etcd 说成"AC 模型,没有选择 P",这个说法不对:分布式系统里分区容错不是可以不选的选项,所谓"集群之间不能通信就不可用"恰恰是 CP 的表现。后面引用这一页时,以这里的说法为准。强一致对注册中心的实际意义是:你读到的列表来自多数派确认过的状态,第二节的代码因此可以写得简单。

注册中心崩溃期间,各方该怎么办值得单独想清楚,因为答案不是"全体重试":

- **服务端**:已注册的老节点继续服务,反正客户端还在用本地缓存;新节点注册失败就直接退出,和第二节"启动阶段连不上就退出"是同一条原则。〔W12-2 p43〕
- **客户端**:用本地缓存继续调用,发现连不上的节点就移出列表;全新服务没有任何缓存,只能返回错误,让调用方自己决定怎么办。〔W12-2 p42〕
- **注册中心与服务端断连**:注册中心会把活着的节点误判为崩溃,客户端在收到错误判定前照常能调用;服务端发现自己长时间续不上约,要告警并考虑是否退出。〔W12-2 p44〕

提高可用性的手段从轻到重:注册中心上集群或多活;双注册中心互为备份;按业务拆分注册中心,核心业务和别的业务隔离。〔W12-2 p41〕顺带一提 K8s:IP 直连、域名、注册中心三种方式在 K8s 里都能用,但注册的必须是 Pod 之间真实通信的 IP,且 Pod 重启后 IP 可能漂移,所以材料作者不太推荐在 K8s 里再跑注册中心,推荐直接用 Service 的名字(DNS)通信。〔W12-2 p18〕

## 五、负载均衡算法全景

列表到手,这次请求发给谁?先摆正目的:负载均衡是手段不是目的,目的是把请求交给"最适合"处理的节点,内存密集的给内存多的,CPU 密集的给 CPU 空闲的。〔W13-1 p4〕

**轮询与随机。** 轮询是逐个轮流,应用最广,绝大多数情况下直接用不会出问题。它隐含两个假设:所有服务器处理能力一样,所有请求消耗资源一样。〔W13-1 p7〕随机更简单,生成随机数对节点数取余,假设与轮询相同。〔W13-1 p18〕

**加权版本。** 现实是机器有新旧、配置有高低,于是给每个节点一个权重,代表处理能力、重要性或优先级。加权轮询按权重分配请求次数;〔W13-1 p8〕加权随机计算总权重,随机数落在哪个区间就选哪个节点(这是对"随机数加权重"的常规做法,材料未展开);哈希类算法用业务特征(业务 ID、外键、唯一索引)算哈希值代替随机数。权重从哪来?注册中心模型下由服务端注册时写入,随服务发现下发给客户端。〔W13-1 p9〕

**一致性哈希。** 普通哈希在节点数变化时几乎全部请求要重新映射;把哈希空间组织成环,哈希值落在某个区间就由负责该区间的节点处理,增减节点只有部分请求换节点。这就是它对有状态场景(缓存、分片)的价值。〔W13-1 p22〕

**动态算法。** 前面都是静态的,动态算法尝试实时估算节点负载,常见三种:最少连接数,挑当前连接数最少的,但在连接多路复用下会失真,一个连接可能被上百个请求复用;最少活跃数,数的是正在处理中的请求数,比连接数准;最快响应时间,挑历史上响应最快的,通常取平均或 99 线。〔W13-1 p25-27〕注意这三种的视角都只在客户端:客户端只知道自己发出去的连接、未归还会话和响应时间,看不到服务端的真实水位,这是框架级负载均衡的固有局限。〔W13-1 p30〕

设计自己的算法时回答四个问题就够了:要不要区分处理能力(加权与否);选什么指标表达负载(不选指标就靠轮询这种统计意义上的均衡);要不要区分请求消耗(大请求会打爆个别实例);最后选哪个算法。实践口诀:遇事不决用轮询,出了问题再说。〔W13-1 p28〕

### 平滑加权轮询:权重 10:20:30 是怎么变成 C B A C B C 的

普通加权轮询有个观感很差的缺陷:权重高的节点会连续收到请求,比如 B 连收两个、C 连收三个,瞬时压力集中。〔W13-1 p10〕平滑加权轮询(SWRR)解决的就是这个,它给每个节点引入"当前权重",流程四步:算总权重;每个节点当前权重加自己的初始权重;选当前权重最大的;选中的减去总权重。〔W13-1 p11〕

初始权重 10、20、30(总 60)时,前两次的推演:第一次三家加权重后是 10、20、30,C 最大被选中,C 减 60 变 -30;第二次加完是 20、40、0,B 最大被选中,B 减 60 变 -20。完整序列是 C、B、A、C、B、C,三个节点交替出现,没有谁连续挨打。直接跑一遍:

```go
package main

import "fmt"

// 初始权重就是材料图里的:10、20、30,总权重 60
type node struct {
	name  string
	initW int // 初始权重(有效权重)
	curW  int // 当前权重
}

func pick(nodes []*node) *node {
	total := 0
	for _, n := range nodes {
		n.curW += n.initW // 1. 每个节点当前权重加上初始权重
		total += n.initW
	}
	best := nodes[0]
	for _, n := range nodes[1:] { // 2. 选当前权重最大的
		if n.curW > best.curW {
			best = n
		}
	}
	best.curW -= total // 3. 选中者减去总权重
	return best
}

func main() {
	nodes := []*node{{"A", 10, 0}, {"B", 20, 0}, {"C", 30, 0}}
	for i := 1; i <= 6; i++ {
		n := pick(nodes)
		fmt.Printf("第%d次:%s  ", i, n.name)
	}
	fmt.Println()
}
```

```text
第1次:C  第2次:B  第3次:A  第4次:C  第5次:B  第6次:C
```

减总权重这一步是算法的灵魂。不减的话,C 的当前权重永远比 B、A 高出一大截,每次都是 C,权重失效;减了 60 之后,选中的节点"欠"了一次,下次轮到别人,6 次一轮恰好 C 出现 3 次、B 出现 2 次、A 出现 1 次,比例精确等于权重比,且分布是打散的。第四个请求选完后,A、B、C 的当前权重是 -20、20、0;材料的第五个请求图里,选择之前(各加一次初始权重后)是 -10、40、30,与此一致。〔W13-1 p15-16〕理解了 SWRR,再看"结合调用结果动态调权"就顺理成章了:把返回错误的节点降权、熔断期间把权重压到极低,第七节把它加进上面的代码。〔W13-2 p31〕〔W13-2 p35〕

## 六、gRPC 怎么接负载均衡

### 默认行为与内置算法

回到开头的现象。gRPC 内置的算法都在 `balancer` 子包里:grpclb 是给集中式 LB 用的集成点;leastrequest 和 rls 是实验特性;round_robin 是轮询;weightedroundrobin 是加权轮询。除了 round_robin,其他一般用不上。〔W13-2 p10〕`pick_first` 虽然没列在这个清单里,但它是默认策略,这就是开头"4 次全打 8090"的原因。

换算法的方式很特别:传一段 JSON。这是 gRPC 一个容易写错的地方,`round_robin` 没有配置项,花括号里就是空的:〔W13-2 p5〕

```go
grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`)
```

带上这段配置重跑开头的实验,4 次调用就会轮流落在 8090 和 8091 上,和第一节 `rrPicker` 的行为一致。〔W13-2 p5〕

### 自己写一个 Picker 有多难

gRPC 把负载均衡拆成两个接口:`Balancer` 负责维护连接状态、创建 `Picker`;`Picker` 只做一件事,每次 RPC 被调时返回一个连接。round_robin 的实现很简单,核心是用原子操作更新候选节点的下标,第一节我们照着写的 `rrPicker` 就是这个思路。〔W13-2 p6〕注意版本差异:我对照了本机模块缓存里的 gRPC v1.67.0 源码,`roundrobin.go` 里确实是原子递增下标,而且起始下标是随机的(所以第一节的 `rrPicker` 从下标 1 开始只是演示);本项目 `go.mod` 里的 v1.84.0,`round_robin` 已经改成建立在 `endpointsharding` 之上,内部结构不同,对外语义(轮流挑选)相同,这部分我没有逐行核对。注意 `balancer.SubConn` 名不副实:明面上是 Conn,实际代表"连到某个节点",背后是这个节点的连接池。〔W13-2 p6〕

自定义加权轮询的接入是三步:实现 `PickerBuilder` 接口(它唯一的 `Build` 返回一个 Picker);把 PickerBuilder 包装成 `balancer.Builder` 注册进 gRPC 的全局表;Dial 时用上面那段 JSON 指定名字。注册靠算法包的 `init` 函数,业务代码匿名引入触发 init,之后按名字查找,这是 Go 生态的标准插件模式。〔W13-2 p16〕〔W13-2 p8〕权重数据则走服务发现返回的元数据:Resolver 返回的每个地址携带 attributes,注册中心里 `metadata=weight=123` 这类数据放进去,PickerBuilder 建立时取出;元数据在地址生命周期里不变,权重变了要靠 Resolver 触发地址更新来传播。〔W13-2 p13〕〔W13-2 p14〕

总结成一句话:绕不开 `PickerBuilder` 和 `Picker` 两个接口,接入方式基本都靠 `WithDefaultServiceConfig` 指定;现有的轮询和加权轮询已能覆盖绝大多数需求,真要自定义时才走这条路。以 gRPC 为底座的微服务框架(例如 go-zero)在服务注册发现上高度同构:必然实现 gRPC 的 `Resolver` 接口;必然有注册过程;必然有续约(自己写或靠 etcd 客户端);想支持多种实现就会抽一层 Registry/Discovery 接口,学新框架沿这四点找源码很快能定位。〔W13-2 p25〕〔W12-2 p26〕

## 七、把动态调权加进 SWRR

第五节的 `pick` 里,选中的减总权重、出错的怎么办?让"有效权重"可变就行:某个节点连续失败,就把它的初始权重减半(下限 1,永不让权重归零,否则它永远选不中,也永远没有恢复的机会);熔断恢复后再逐步升回来。〔W13-2 p31〕〔W13-2 p35〕〔W13-1 p29〕

```go
package main

import (
	"errors"
	"fmt"
)

var errNoInstance = errors.New("no instance")

type picker struct {
	names []string
	w     []int // 各节点的动态权重(有效权重)
	cur   []int
}

func (p *picker) pick() (string, error) {
	if len(p.names) == 0 || len(p.w) != len(p.names) {
		return "", errNoInstance
	}
	total := 0
	for i, w := range p.w {
		p.cur[i] += w
		total += w
	}
	if total <= 0 {
		return "", errNoInstance
	}
	best := 0
	for i := range p.cur {
		if p.cur[i] > p.cur[best] {
			best = i
		}
	}
	p.cur[best] -= total
	return p.names[best], nil
}

// 熔断降权:连续失败就把有效权重减半,下限 1,永不归零
func (p *picker) penalize(i int) {
	if p.w[i] > 1 {
		p.w[i] /= 2
	}
	p.cur[i] = 0
}

func main() {
	p := &picker{names: []string{"A", "B"}, w: []int{30, 10}, cur: make([]int, 2)}
	for i := 0; i < 4; i++ {
		n, _ := p.pick()
		fmt.Print(n, " ")
	}
	fmt.Println()
	p.penalize(0) // A 连续出错,降权
	for i := 0; i < 4; i++ {
		n, _ := p.pick()
		fmt.Print(n, " ")
	}
	fmt.Println("A 的有效权重现在是", p.w[0])
}
```

```text
A A B A
A B A B A 的有效权重现在是 15
```

降权前 A 拿走四分之三的流量;减到 15 后 A:B 变成 3:2,分流的正是从 A 挪走的那部分。`total <= 0` 的守卫对应的正是"权重全被调成 0"这种事故:不拦住,全零权重下依然会选出一个节点,流量被打给本该剔除的实例。〔W13-1 p29〕

## 小结

- 服务发现三代方案:IP 直连、DNS、注册中心,演进主线是"客户端缓存 + 新鲜度"的权衡,注册中心用主动通知打破 DNS 两难。〔W12-1 p5-7〕
- 注册生命周期:启动先开端口再注册、失败即退出;租约加续约当心跳;下线走五步,全程有超时。〔W12-1 p12-19〕〔W12-2 p14〕
- etcd 实现三件套:`Grant` 建租约,`KeepAlive`(课件服务端用的是周期性 `KeepAliveOnce`)续约,`Delete` 注销;客户端查询建缓存再挂 Watcher。〔C:dachang/etcd/service_registration_center/server/service_hub.go:54-84〕〔C:dachang/etcd/service_registration_center/client/service_hub.go:96-106〕
- 注册中心崩溃时的分工:老节点续服务、新节点注册失败即退出、客户端用缓存加 failover,小规模选型随意、大规模优先 AP。〔W12-2 p29-31〕〔W12-2 p42-44〕
- 负载均衡算法按"是否考虑处理能力、用什么指标、是否区分请求消耗"三问选择,默认轮询;SWRR 用"加初始权重、选最大、减总权重"把比例分配打散,动态权重必须设上下限。〔W13-1 p28〕〔W13-1 p11〕〔W13-1 p29〕
- gRPC 侧:默认 `pick_first` 会把流量全压到第一个地址;换算法传 JSON;自定义算法实现 `PickerBuilder` 加 `Picker`,匿名引入注册,权重走 Resolver 元数据。〔W13-2 p4〕〔W13-2 p25〕

## 常见坑

1. **忘了换默认负载均衡**:两个节点只有一个收到流量,一查是 `pick_first`。多副本部署却没分流,先查这个。〔W13-2 p4〕
2. **注册了 `127.0.0.1` 或容器内网 IP**:客户端拿到地址连不上。注册地址必须是客户端可达的网卡 IP,不能从配置文件死读。〔W12-2 p16〕〔W12-2 p18〕
3. **先注册后开端口**:客户端拿着列表打过来连接被拒。要确保端口打开之后再注册。〔W12-2 p14〕
4. **心跳参数拍脑袋**:间隔、判定次数是"发现速度换系统压力"的定价,不是随手写的常数。〔W12-1 p15〕
5. **动态权重没有上下限**:权重滚雪球或归零,节点要么一直被选中要么永远没流量,降权要有下限。〔W13-1 p29〕
6. **租约过期后还傻续**:续约报 `ErrLeaseNotFound` 时必须整个重新注册,续是续不上的。〔C:dachang/etcd/service_registration_center/server/service_hub.go:54-84〕

## 判断题与答案

1. 租约 TTL 为 5 秒,服务端被 `kill -9`,客户端最早什么时候能把流量切走?有哪几段延迟?
   答案:`kill -9` 没机会优雅注销,只能等租约到期,加上注册中心判定和通知,客户端通常在 TTL 之后不久感知;具体延迟由心跳间隔和失败判定次数决定,间隔越短、判定越严,切换越快但系统压力越大。〔W12-1 p15〕
2. 平滑加权轮询为什么要在选中后减去总权重?
   答案:不减,当前权重最高的节点会一直最高,流量全压给它;减掉总权重相当于"记账",让别的节点有机会反超,最终选中比例严格等于权重比,且分布平滑。
3. 注册中心崩溃期间上线的新服务,注册失败就退出,这个设计的理由是什么?
   答案:此时客户端没有任何缓存可用,注册不成功就永远没人调它;而启动阶段失败代价最小,退出去靠重试或告警比带病挂着好。〔W12-2 p43〕
4. 什么场景下哈希负载均衡比轮询更合适?
   答案:同一请求需要落到同一节点的场合,比如本地缓存复用、有状态会话;用业务 ID 等特征做哈希,节点数稳定时映射关系不漂移,节点增减时用一致性哈希减少重映射。〔W13-1 p20〕〔W13-1 p22〕

## 在项目里

这里没有现成例子:`dnsproxi-go` 的上游由环境变量 `DNS_UPSTREAM` 指定单个地址(见 `main.go` 里的 `Config.Upstream`),没有服务发现,也没有负载均衡。读代码时可以把它当"最小形态":如果上游要变成一组,下一步就是在 `Proxy` 里把单个地址换成带健康检查的列表,再用本节的某种算法挑选。

## 想深入去哪查

- 原材料:W12-1(注册发现理论、心跳、优雅下线)、W12-2(etcd 与 gRPC 接入、CAP 与选型、高可用)、W13-1(负载均衡算法全景)、W13-2(gRPC balancer、failover、哈希负载均衡)、C:dachang/etcd(租约、锁、配置中心、服务注册完整示例)。
- 旧章:`notes/p10-grpc.md`。
- 平滑加权轮询的代码在 `/tmp` 的独立模块里用 `go test -race` 跑过。
