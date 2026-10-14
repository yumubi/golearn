# P34 · TCP 服务器与 DNS over TCP

> 这章学完你能干什么：说清 TCP 字节流为什么需要应用层定界，并用 dnsproxi-go/tcp/ 的 300 行真实代码走完一条"问题 → 实现 → 上限 → 回退 → 优化"的服务器链路：2 字节长度前缀成帧、streamConn 把连接适配成 PacketConn、每连接一 goroutine 加空闲超时、MaxConns/MaxInFlight 两级背压、UDP TC 截断回退，最后用 pprof 四步循环定位 Syscall 瓶颈。前置：P24（TCP 协议格式与粘包现象）、P31（goroutine 与 channel）。

这章的写法与前三章不同：叙事骨架不是课件，是仓库里已提交的 dnsproxi-go/tcp/（frame.go 57 行、server.go 167 行、truncconn.go 70 行，6ddc4cb）。TB36/37/38 三讲是"怎么实现一个 TCP 服务器"的完整方法论，这里按这份代码真实遇到的问题重新排列，课件顺序不等于章节顺序。先跑一遍完整链路建立体感：

```sh
$ dig +tcp -p 15354 @127.0.0.1 example.com A | grep -E "flags:|MSG SIZE"
;; flags: qr rd ra; QUERY: 1, ANSWER: 33, AUTHORITY: 0, ADDITIONAL: 0
;; MSG SIZE  rcvd: 568
```

〔实测〕查询经 2 字节前缀成帧走 TCP 进来，应答 568 字节完整回来（fake upstream 回 33 条 A 记录，`DNS_UPSTREAM=127.0.0.1:5301 DNS_TCP_LISTEN=:15353 DNS_UDP_TC_LIMIT=512 go run .`）。这条链路每一环是本章一节。

## 1. 问题：TCP 是字节流，查询从哪帧到哪帧

DNS over UDP 是天然一问一答：一次 `ReadFromUDP` 恰好一条报文，有边界。换到 TCP，第一个问题就变了：TCP 是字节流，没有消息边界。发端两次 Write，收端可能一次 Read 全收到（粘包）、两次各收一半（半包）、或者 1 字节 1 字节地收。必须由应用层自己定界。〔TB36 p6-7〕

定界有两种模式：二进制模式（长度字段，MQTT、SMPP、DNS over TCP）与文本模式（分隔符，HTTP 的空行）。〔TB36 p6-7〕

课件代码包里有两个分隔符方案。一个是 `C:go_basic/socket` 的 MAGIC 魔数（〔C:go_basic/socket/server/tcp.go:54-120〕）：`bytes.Index` 找 `13,7,29,83,113` 五字节魔数，跨两次 Read 的半包用 `bytes.Buffer` 攒。另一个是 `C:dachang/my_rpc` 的同款（〔C:dachang/my_rpc/transport/server/tcp.go:37-52〕）。

分隔符方案的根本缺陷：正文里可能出现分隔符字节，要么转义要么赌魔数不出现。长度前缀方案正文任意字节合法，代价是头 2-4 字节。DNS over TCP 由 RFC 1035 §4.2.2 定死：2 字节大端长度前缀，最大 65535。p24 §TCP 粘包 已列过三种解法清单，这里补"为什么 DNS 选前缀"：协议是 RFC 定的，DNS 报文纯二进制，分隔符得转义，得不偿失。

定了帧格式，下一个问题是 I/O 模型。三种经典模型：阻塞 I/O（内核把数据备好拷到用户空间才返回，一个线程只能陪一个连接）；非阻塞 I/O（未就绪立即返回 EAGAIN，调用方轮询，浪费 CPU）；I/O 多路复用（一个线程把一批 socket 交给 select/poll/epoll，内核就绪通知，一线程看住成百上千连接）。〔TB36 p8-12〕 C 网络服务的主流答案是多路复用加事件框架（libevent/libev/libuv 这层抽象就为此而生），代价是回调把连续逻辑撕碎。

Go 的答案与众不同：阻塞 I/O 的表象，多路复用的本质。`conn.Read` 写起来是阻塞的，底下运行时把 socket 设为非阻塞，没数据时 goroutine 挂到 netpoller（Linux epoll、macOS kqueue、Windows iocp），数据到了唤醒 goroutine。只阻塞 goroutine，不阻塞线程。〔TB36 p11-12〕 每连接一个 goroutine 就此成为 Go 的默认形态：写法是阻塞式的（无回调、无状态机），扩展性是多路复用的。

Rust 对照：std::net 是阻塞 API，单线程陪一个连接，要 tokio 的 async/await 才有同款扩展性，且异步要显式标 `async fn`。Go 的"阻塞表象"是运行时帮你做了 tokio 做的事。Java NIO 直接暴露 Selector，Netty 的 event loop 是 libevent 式框架，到 21 的虚拟线程才追平 Go 的透明阻塞。

```go
// frame.go L42-52（帧解码的前半：先读 2 字节前缀）
func DecodeFrame(r io.Reader) ([]byte, error) {
	var prefix [2]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		// 干净的对端关闭在读前缀时表现为 io.EOF，让调用方当"连接结束"处理；
		// 半截前缀是协议错，包成 UnexpectedEOF。
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("tcp frame: read prefix: %w", err)
	}
```

dnsproxi-go/tcp/frame.go 的 DecodeFrame 是"在字节流里切帧"的最小实现：`io.ReadFull` 读满 2 字节前缀，再按前缀值读满正文。〔TB37 p6-8〕 教材版 StreamFrameCodec 是 4 字节 int32 前缀加 FramePayload 类型别名，结构相同；为什么能用 `io.ReadFull` 而不怕半包，见下节三个边界。

## 2. 定界实现：frame.go 的三处边界

先看实测行为，三个边界各有归属：

```sh
$ go test -run TestBoundaries -v .
zero-len frame: 0 bytes, err=<nil>
half prefix: err=tcp frame: read prefix: unexpected EOF
prefix 16 > body 3: err=tcp frame: read 16-byte body: unexpected EOF
```

**边界一：前缀 0（0 长度帧）**。`make([]byte, 0)` 加 `io.ReadFull` 对空 buf 不读直接 nil，DecodeFrame 返回 0 字节无错误。RFC 1035 没禁止空帧，帧层按协议放行，业务层挡：handleQuery 对长度不足 12 的输入静默丢（main.go:290-293，`QueriesTotal.WithLabelValues("OTHER", "short")` 计数）。分层的好处在这：帧层不必懂 DNS，业务层不必懂字节流。

**边界二：半包（前缀只到一半）**。对端断开在读前缀时表现为 `io.EOF`：干净关闭，serveConn 拿到 EOF 静默退出循环；前缀只来 1 字节是协议错，`io.ReadFull` 报 `ErrUnexpectedEOF`。两个错误共用一个 Read，语义由错误类型区分，调用方不用自己数字节。

**边界三：前缀大于内容**。前缀说 16 字节、正文只来 3 字节就对端闭嘴了：`io.ReadFull` 卡在等剩余 13 字节，直到 EOF 报 `ErrUnexpectedEOF`。这正是 fuzz 要堵的点：恶意客户端发个 65535 前缀就断开，服务端不能 panic 不能挂死，错误上抛、连接关闭、资源归还。〔TB37 p8〕 教材提醒的三点全部落实：字节序统一 BigEndian（`binary.BigEndian.PutUint16`）；`binary.Read/Write` 按参数宽度操作；未设 deadline 时 `io.ReadFull` 一般读满才返回，除非 EOF。

```go
// frame.go L16-19（上限是前缀宽度的天然结果）
const MaxMsgLen = 0xFFFF
var ErrTooLarge = errors.New("tcp frame: message exceeds 65535 bytes")
```

发侧对称：EncodeFrame 超 65535 返回 ErrTooLarge，前缀 uint16 天然封顶。帧层错误全部是 error 不是 panic， fuzz 目标函数守这条契约：任何输入只允许三种结局（合法帧、error、EOF 族），绝不允许 panic。〔实测〕`go test -run '^$' -fuzz FuzzDecodeFrame -fuzztime 20s ./tcp/`：20 秒 7,974,886 次执行（335,288/sec），PASS，语料 22 个种子（0 长度、1 字节、前缀大于内容是必放的边界种子）。

对照：Rust tokio-util 自带 LengthDelimitedCodec，Java Netty 有 LengthFieldBasedFrameDecoder，两者都是工业级现成帧编解码器；Go 标准库没有，要么手写（本章，60 行）要么引第三方。手写的好处是 EOF 族语义完全可控：这正是 §3 空闲超时和 ctx 取消要依赖的接口。

性能底数先测出来（后文优化循环用）：

```text
$ go test -bench . -benchmem -benchtime=2s .
BenchmarkFrameEncode-10  43581062  54.33 ns/op  576 B/op  1 allocs/op
BenchmarkFrameDecode-10  33846454  73.03 ns/op  562 B/op  3 allocs/op
```

编码 1 次分配（帧缓冲），解码 3 次（前缀 buf、正文 buf、io.ReadFull 中间态），每帧两次堆分配：§6 优化的靶子立在这。

## 3. 连接形态：每连接一 goroutine 与两个上限

```go
// server.go L86-93（accept 循环的标准形态）
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				mu.Lock()
				delete(conns, sc)
				mu.Unlock()
			}()
			serveConn(ctx, sc, h, cfg.IdleTimeout, int64(cfg.MaxInFlight))
		}()
```

服务端骨架是教材同款三件套：`net.Listen` 绑端口、循环 `Accept()`、每连接 `go serveConn`。〔TB36 p12-13〕 端口被占报 `bind: address already in use`，backlog 满时 Dial 方阻塞（macOS somaxconn 默认 128）。与 C 不同的是没有 `while(1)` 里的状态机：每个连接一个 goroutine，写法就是顺序读循环。

goroutine 便宜（P3 实测 2KB 栈）不等于免费。两个必须答的问题：连接建了不查怎么办？连接太多怎么办？dnsproxi 的答案在 ServerCfg 的三个字段（server.go L23-33）：IdleTimeout（默认 30s）、MaxConns（默认 512）、MaxInFlight（默认 64）。

**空闲超时**靠 SetReadDeadline，且是绝对时间不是时长：〔TB36 p16-18〕

```go
// server.go L104-107（每读完一帧刷新，续命式租约）
	for {
		_ = sc.conn.SetReadDeadline(time.Now().Add(idle))
		query, err := DecodeFrame(sc.conn)
		if err != nil {
			return // EOF/超时/半帧：连接到此为止，无需区分
		}
```

每次读帧前把 deadline 推到 now+30s；对端 30s 没发帧，Read 返回超时错误，连接关闭。〔C:go_basic/socket/server/tcp.go:32-51〕 的长连接同款（每轮 Read 前重设 2 分钟）。实验：连上不发数据，30.0s 后 recv 得 EOF（〔实测〕`python3 -c "import socket,time; s=socket.create_connection(('127.0.0.1',15353)); print(s.recv(1))"` 30.0s 后返回 b''）。

**MaxConns** 是全局上限：conns map 记活连接，accept 后查 `len(conns) >= cfg.MaxConns`，满员直接 `conn.Close()` 接受下一个（server.go L73-79）。防的是 SYN 洪水和"只建连不发查询"的占坑客户端。被拒的客户端读侧立刻 EOF，重试即可：不用排队，排队会把内存压力转移到服务端。

**MaxInFlight** 是单连接在飞上限：

```go
// server.go L110-119（超限丢帧，不断连接）
		// MaxInFlight：单连接在飞查询超限就丢帧（不断连接），防单连接打爆下游。
		if inflight.Add(1) > maxInFlight {
			inflight.Add(-1)
			continue
		}
		go func(q []byte) {
			defer inflight.Add(-1)
			cq := append([]byte{}, q...)
			h(sc, sc.RemoteAddr(), cq)
		}(query)
```

取舍要明说：**这是丢帧，不是排队**。一条连接在飞 64 个查询没回来时，第 65 帧直接丢，客户端收不到应答只能重传。为什么不做队列？队列只是把丢弃延迟化：单连接打满队列时排队尾部的查询一样超时，而服务端多背了队列内存和队头阻塞。丢帧把压力决策还给客户端（重传是客户端的既有逻辑），服务端保持恒定内存。代价也明说：丢的是"本来晚点能处理"的查询，突发流量下重传率上升；真要精细化，应在应用层回 SERVFAIL 而非静默丢，但那要改 handleQuery 的回包路径，超出了帧层职责。RFC 允许 pipeline（一连接连发多查询），所以同连接并发在飞是常态而非异常，这个上限防的是滥用不是并发本身。

为什么每查询 `append` 一个副本？DecodeFrame 已返回独立 buf，副本是防御性兜底：对齐 UDP 路径"查询不可变"契约，防上游某天复用 buf。每帧多一次 512 字节拷贝（54ns 量级），买语义安全。

读写的坑教材列全了，这里只点与帧层直接相关的两条。其一，部分读：`conn.Read(buf)` 有多少读多少立即返回，n 可能是 2 而不是 10（buf 10 收 "hi" 得 n=2），必须循环读满：`io.ReadFull` 就是这个循环的封装。〔TB36 p16-18〕 其二，并发写：一次 Write 写完整业务包是安全的（fdMutex 串行化单次系统调用），拆多次不保证连续性。〔TB36 p22-26〕 streamConn 的 WriteTo 正是"一次 Write 整帧"（server.go L135-148），多 goroutine 并发回包安全。

ctx 取消怎么断开阻塞在 Read 的 goroutine？Serve 起一个只等 `<-ctx.Done()` 的 goroutine 关 listener（server.go L48-51），accept 断。取消路径顺手关所有在飞连接，DecodeFrame 立刻返回 EOF，wg.Wait 才不会卡 30s 空闲超时（server.go L58-65）。

TB09 的 HTTP 优雅退出同律（signal.Notify 加 srv.Shutdown）：机制不同（ctx vs signal），要点相同。退出先断阻塞点，再等 goroutine 排空。

每连接一 goroutine 在 Rust 里要显式 `tokio::spawn` 每连接一个 task，JoinHandle 的记账、task panic 的隔离都得自己写；Go 把 spawn 做进 Accept 循环的一行，代价是 panic 语义默认杀进程（P3 已讲），serveConn 入口的 defer recover 是纪律不是语言保证。


## 4. 业务复用：PacketConn 适配器与 main 接入

TCP 服务器最难的不是帧，是不复制业务逻辑。dnsproxi 的 UDP 路径已有完整 handleQuery：解析、sinkhole、缓存、转发、指标，签名 `func(pc net.PacketConn, client net.Addr, query []byte)`。TCP 想复用，就要让一条 TCP 连接假装成一个 PacketConn：适配器模式：

```go
// server.go L125-135（streamConn：WriteTo = 成帧写给这条连接）
// WriteTo 实现 net.PacketConn：把应答加 2 字节前缀写给这条连接。
// client 参数保留（handleQuery 的签名统一），调用方传的是连接的 RemoteAddr。
func (s *streamConn) WriteTo(resp []byte, _ net.Addr) (int, error) {
	frame, err := EncodeFrame(resp)
	if err != nil {
		return 0, err
	}
	n, err := s.conn.Write(frame)
	if n >= 2 {
		n -= 2 // 前缀不计入"载荷字节数"，与 UDP WriteTo 语义对齐
	}
	return n, err
}
```

handleQuery 调 `pc.WriteTo(resp, client)` 时，UDP 形态这是单包发送，TCP 形态这是成帧写入：业务代码一行不改。〔TB37 p4-5〕 的 Frame/Packet 两层分工在这里换了形态。教材版 frame 层管定界、packet 层管业务语义。Packet 接口只有 Decode([]byte) error 与 Encode() 两个方法（〔TB37 p11〕），包级 Decode 按 commandID 分派各类型（〔TB37 p12-14〕）。

dnsproxi 的帧层在 tcp 包、业务层整个是 handleQuery，分派靠 DNS 报文自身的 qtype 而非自定义 commandID。教材用接口（HandlePacket 接口加包级 Decode 分派），dnsproxi 直接传方法值 `p.handleQuery`（Handler 类型 server.go L21 注释明确"签名刻意与 handleQuery 一致，无需包装层"）。适配器模式的两种形态：定义接口 vs 对齐既有函数签名，后者少一个抽象层。

client 参数传什么？UDP 形态传对端地址（回包地址），TCP 形态语义上是"这条连接的远端"：serveConn 传 `sc.RemoteAddr()`（server.go L120）。这是真 bug 修复：最早版本传 LocalAddr，日志/指标里客户端地址全错。ReadFrom 在 TCP 形态永远不该被调（读循环在 serveConn），实现为零值报错（server.go L152-154），接口占位防误用。

main.go 的接入只有两处，全部默认关：

```go
// main.go L146-150（UDP 截断：只包出站 WriteTo）
	var replyPC net.PacketConn = pc
	if p.cfg.UDPTruncLimit > 0 {
		replyPC = tcp.WrapTrunc(pc, p.cfg.UDPTruncLimit)
	}
```

```go
// main.go L166-183（TCP：TCPListen 空 = 完全不启动）
	if p.cfg.TCPListen != "" {
		ln, err := net.Listen("tcp", p.cfg.TCPListen)
		if err != nil {
			pc.Close()
			return fmt.Errorf("tcp listen %s: %w", p.cfg.TCPListen, err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := tcp.Serve(ctx, ln, p.handleQuery, tcp.ServerCfg{}); err != nil {
				logger.Error("tcp serve failed", "err", err.Error())
			}
		}()
	}
```

两个细节值两行注释：TCPListen 由环境变量 `DNS_TCP_LISTEN` 注入（main.go L53），空串零值即不监听，不传配置行为逐位不变；TCP 启动块放在 cache 建立之后（NewCap 在 L154，Serve 在 L179）：Serve 起的查询立刻能吃到完整缓存路径，不用等后台初始化。对照 TB09 的 main 组装（factory.New 建存储、NewBookStoreServer 注入，〔TB09 p9〕）：组装职责相同，注入手段一个是工厂模式一个是方法值。TCP 与 UDP 共用 wg 等排空、共用 ctx 取消，退出路径只有一条。

## 5. 截断与回退：TC 位的最小正确实现

UDP 单包有 512 字节的历史上限（RFC 1035），应答超限发不完整怎么办？答案不是分片，是"告知截断，客户端改走 TCP"：置 TC（Truncated）位，客户端见 TC 自己重连 TCP 重发。〔实测〕三连对比（fake upstream 回 800 字节，`DNS_UDP_TC_LIMIT=512`）：

```sh
$ dig +ignore -p 15353 @127.0.0.1 example.com A | grep -E "flags:|MSG SIZE"
;; flags: qr tc rd ra; QUERY: 1, ANSWER: 0, AUTHORITY: 0, ADDITIONAL: 0
;; MSG SIZE  rcvd: 29

$ dig +tcp -p 15354 @127.0.0.1 example.com A | grep -E "ANSWER:|MSG SIZE"
;; flags: qr rd ra; QUERY: 1, ANSWER: 33, AUTHORITY: 0, ADDITIONAL: 0
;; MSG SIZE  rcvd: 568

$ dig -p 15353 @127.0.0.1 example.com A | head -1
;; Truncated, retrying in TCP mode.
```

第一条 `+ignore` 禁 dig 自行回退，收到 29 字节截断应答：TC=1、ANSWER=0；第二条走 TCP 拿全 33 条；第三条不加开关，dig 见 TC 自动打出 retrying 并走 TCP（TCP 与 UDP 同端口 :15353 时回退直达，分端口时 dig 回退到 UDP 口的 TCP 被拒，connection refused：部署时同端口才对 dig 透明）。

截断实现在 truncconn.go 的 WriteTo（L33-62），三步：

```go
// truncconn.go L41-60（保 question、切 RR、置 TC）
	// question 长度 = QNAME（变长，遇 0 结束）+ QTYPE(2) + QCLASS(2)。
	qEnd := hdr + 4
	for i := hdr; i < len(resp); i++ {
		if resp[i] == 0 {
			qEnd = i + 1 + 4
			break
		}
	}
	keep := qEnd
	if keep > t.limit {
		keep = t.limit
	}
	out := make([]byte, keep)
	copy(out, resp[:keep])
	out[2] |= 0x02 // TC 位
	out[6], out[7] = 0, 0
	out[8], out[9] = 0, 0
	out[10], out[11] = 0, 0
```

**取舍也要明说：这不是完备 DNS 解析**。为什么保留完整 question？客户端收到截断应答后重发 TCP 时，应答里的 question 帮它配对请求（也对齐 BIND 实践）。

为什么按"第一个 0 字节"找 QNAME 末尾？QNAME 是标签序列，0 字节是终止符，扫到即得 question 边界。但 DNS 有压缩指针（0xc0 开头的 2 字节偏移）和 OPT 记录，完整解析要处理两者，这份实现不支持。遇压缩指针的应答，question 扫描可能偏长，退回复制到 limit 兜底（keep > limit 时），TC 位保证客户端仍然回退。29 字节实测 = 12 头 + question 17（`example.com` QNAME 13 + QTYPE/QCLASS 4），ANS 计数清零防客户端解析半条记录。

为什么截断在 UDP 侧（TruncConn 包 WriteTo）而不是帧层？UDP 路径不换：收包原样，只在出站时包一层 `WrapTrunc`（main.go L148-150）。TCP 侧不需要截断：65535 前缀装得下一切合法 DNS 应答（frame.go 的 MaxMsgLen 就是同一条上限的两种表达）。

UDP 无连接 WriteTo 天然带地址，TruncConn 透传全部方法只改 WriteTo。对照 my_rpc 的 UDP 形态（〔C:dachang/my_rpc/transport/server/udp.go:14-28〕，ReadFromUDP 返回远端地址直接回写）：UDP"一次 read 一条报文"的语义让截断成为单包决策，TCP 的流语义反而没有这个问题。

## 6. 优化循环：pprof 四步法看 TCP 服务器

TB38 的方法论一句话：建立基准 → 性能剖析 → 代码优化 → 与基准比较，未达标循环。〔TB38 p2-3〕 教材用它把 tcp-server 从 18.5w req/s 提到 29.2w。第一步 Prometheus+Grafana 搭观测与压测（观测设施与 rate 面板 p13 已讲，不重复）。

第二步 pprof 剖析：`_ "net/http/pprof"` 空导入加 6060 端口，`go tool pprof -http=:9090 http://host:6060/debug/pprof/profile` 采 CPU，采样原理是每 10ms SIGPROF 中断记全 goroutine 栈。第三步火焰图定位：Syscall 方框最大，向上追溯全是网络 read/write。根因是 `net.Conn` 直接当 `io.Reader` 用，每次 Read 哪怕 1 字节也是一次系统调用。

第四步 bufio.Reader/Writer 包一层（每连接单 goroutine 无竞态，缓冲安全），Syscall 频率降、吞吐 18.5w → 27w。再一轮 allocs 剖析找到 packet.Decode 的 38MB 堆分配，sync.Pool 复用 Submit 对象，27w → 29.2w。〔TB38 p14-24〕

这四步在 dnsproxi 的对应物：§2 的 bench 就是"建立基准"（54ns/1 alloc 编码、73ns/3 alloc 解码）；优化靶子也现成：DecodeFrame 每帧两次堆分配（前缀 buf 加正文 buf），前缀 buf 可用栈数组省掉一次；帧缓冲可进 sync.Pool（P32 已讲 Pool 语义，这里只是换了个用武之地）。pprof 服务器侧的接法与教材一致（`_ "net/http/pprof"` 加独立 goroutine 监听 6060），dnsproxi 未开：调试期接入，生产按需。

为什么 TCP 服务器对 syscall 敏感而 UDP 不敏感？每连接一 goroutine 时，阻塞在 Read 上的 goroutine 数等于连接数，每次唤醒是一次 epoll 交互；加 bufio 后单帧内多次小读合并成一次系统调用。UDP 路径一问一答无缓冲可加（报文边界即读边界）。这也是 bufio 优化在 TB38 值 45% 而在 dnsproxi UDP 路径无意义的原因：优化手段的收益取决于 I/O 形态。

对照：Java BufferedReader 同名同职责，Netty 的 PooledByteBufAllocator 是 sync.Pool 思想的工业级实现；Rust 无 GC，对象池只为复用缓冲不为减 GC 压力（bytes::Bytes 池化仍在）。四步循环本身语言无关，Go 的优势是 pprof 内置零依赖。

同一套"接口先行"的手法在标准库 HTTP 侧也是主旋律。最小服务三行起步：http.HandleFunc 注册、ListenAndServe 启动，处理函数原型固定为 func(w http.ResponseWriter, r *http.Request)（〔TB09 p3-4〕）。ServeMux 按最长前缀匹配路由（〔TB09 p4〕）。

复杂业务用 Store 接口解耦存储实现，使用方只依赖接口（〔TB09 p10〕）。横切逻辑用 middleware：接受 http.Handler 返回 http.Handler 的函数，嵌套包裹后串到业务 handler 之前（〔TB09 p14-16〕）。dnsproxi 的 Handler/WriteTo 与它们同构：面向接口的边界划在哪，业务就能在哪换形态（UDP 换 TCP 不动 handleQuery，就是这个边界的红利）。


## 小结

- TCP 字节流定界两种模式：分隔符（正文需转义）与长度前缀（头 2-4 字节）；DNS over TCP 由 RFC 定死 2 字节大端前缀，65535 上限。〔TB36 p6-7〕〔TB37 p2-3〕
- 帧解码三边界：0 长度放行给业务层挡、半包 EOF 族区分干净断开与协议错、前缀大于内容卡到 EOF 报 ErrUnexpectedEOF：fuzz 守"只 error 不 panic"。〔TB37 p6-8〕
- 阻塞表象加 netpoller 本质：每连接一 goroutine 写法是顺序的，扩展性是多路复用的。〔TB36 p11-12〕
- 空闲超时是绝对时间续租；MaxConns 满员拒连；MaxInFlight 超限丢帧不排队：压力决策还给客户端，代价是突发下重传率上升。
- 适配器模式复用业务：streamConn 把连接假装成 PacketConn，handleQuery 零改动；TCP 启动块放在 cache 之后。〔TB37 p4-5〕
- TC 截断保完整 question、按第一个 0 字节扫描，不是完备 DNS 解析（压缩指针不支持，退回复制兜底）；客户端见 TC 回退 TCP。
- pprof 四步循环：基准 → 剖析 → 优化 → 对比；Syscall 瓶颈是 TCP 服务器第一瓶颈，bufio 是第一刀。〔TB38 p2-3〕〔TB38 p14-24〕

## 在 dnsproxi 里看这一章

本章主线就是 dnsproxi 的代码（工作副本 6ddc4cb，行号实读）：

- 帧层：frame.go 全文件（MaxMsgLen L16、EncodeFrame L27-36、DecodeFrame L42-70，EOF 族三分支 L52-62）。
- 连接层：server.go（ServerCfg 三上限 L23-33、Serve 的 ctx 关监听 L48-51 与 MaxConns 满员关连 L73-79、serveConn 的 deadline 刷新 L104-107 与 inflight 丢帧 L110-113、streamConn 的 WriteTo L135-148 与 RemoteAddr L120）。
- 截断层：truncconn.go（qEnd 扫描 L41-48、TC 置位与三计数清零 L57-60）。
- 接入层：main.go（两环境变量 L51-54、WrapTrunc 只包出站 L146-150、TCP 块在 cache.NewCap（L154）之后 L166-183、handleQuery 的 short 分支 L290-293）。
- 业务复用的证据：handleQuery（main.go L279 起）不知晓自己跑在 UDP 还是 TCP 上，QueriesTotal/InFlight 指标两形态共用。

## Take-away

- 预测：DecodeFrame 收到前缀 0 的帧返回什么？前缀 16 正文 3 字节后对端断开，错误是什么？（§2 实测：0 字节 nil 错误；ErrUnexpectedEOF。）
- 预测：MaxInFlight=1、第 1 帧还在 handler 里时，第 2、3 帧的下场？（丢弃，连接不断，seen 计数不变。）
- 观察：`nc` 直连 TCP 口发前缀 0xFFFF 后立刻断开，服务端日志与连接数怎么变？（无 panic，DecodeFrame 报 UnexpectedEOF，连接静默回收。）
- 观察：同端口 TCP+UDP（DNS_TCP_LISTEN=:15353 + DNS_LISTEN=:15353）时 dig 默认模式自动回退全链路；分端口时回退在哪一步断？（§5：connection refused，dig 回退目标固定为 UDP 口。）
- 改造：给 DecodeFrame 的前缀读换栈数组 `var prefix [2]byte` 已是零分配，正文 buf 换 sync.Pool，bench 对比 allocs/op。
- 改造：2 字节前缀改 4 字节，MaxMsgLen、兼容性、编码 bench 三处各变什么？

## 章末参考

### Rust/Java 对照

| Go | Rust | Java |
|---|---|---|
| 阻塞表象 + netpoller | std::net 阻塞 / tokio async | NIO Selector / 21 虚拟线程 |
| 手写 60 行帧编解码 | tokio-util LengthDelimitedCodec 现成 | Netty LengthFieldBasedFrameDecoder |
| 每连接一 goroutine（默认） | 每连接一 task（tokio::spawn 显式） | 线程池或 event loop |
| sync.Pool 减 GC 压力 | 无 GC，池化只省分配 | PooledByteBufAllocator |

### 速查与深读

- 帧格式：`| len(2B, BigEndian) | msg |`，len 上限 0xFFFF；读用 io.ReadFull，EOF/ErrUnexpectedEOF 区分干净断开与协议错。
- 超时三式：SetReadDeadline（绝对时间）、SetDeadline（读写一起）、ctx 关 listener（accept 阻塞的出口）。
- 旧章交叉：p24 §TCP 粘包（三种定界清单）、§TCP 可靠性（握手挥手）、p17 §指标（rate 面板）、p32 §pprof（allocs 读法）、P3 §goroutine（2KB 栈实测）。
- 原材料单元：TB36（socket 模型与读写四三情形）、TB37（Frame/Packet 抽象与错误注入测试）、TB38（pprof 四步优化）。
- 代码素材：C:go_basic/socket（MAGIC 分隔与 UDP 形态）、C:dachang/my_rpc（魔数与序列化 bench）、C:go_basic/http（ResponseWriter 铁律）、C:go_basic/encryption（AES-CBC 与 TLS 混合加密，本章链路的安全延伸）、C:go_basic/io（缓冲收益 158ms vs 7ms，p17 已引用）。
- 深读：RFC 1035 §4.2.1（UDP 截断与 TC）、§4.2.2（TCP 前缀）；Go 源码 net/fd_posix.go 的 deadline 与 internal/poll.FD 的 fdMutex。

## 练习：p34frame

先跑全绿：

```sh
$ go test -race -count=1 ./exercises/p34frame/
ok  	golearn/exercises/p34frame	1.5s
```

9 个测试各考一件事：

- `TestZeroLenFrameBoundary`：§2 边界一，0 长度帧解码 0 字节无错误。
- `TestHalfPrefixEOF`：§2 边界二，1 字节前缀报 ErrUnexpectedEOF，干净断开报 io.EOF。
- `TestPrefixOverrun`：§2 边界三，前缀 16 正文 3 字节卡到 ErrUnexpectedEOF。
- `TestSplitWriteRoundTrip`：§1 半包，前缀与正文分两次写，帧层无缝。
- `TestPipelinedTwoFrames`：§1 粘包，一次写两帧各解各的。
- `TestDropWhenInFlightFull`：§3 丢帧语义，在飞满时后续帧丢弃、连接不断、恢复后照常。
- `TestFrameEncodePool`：§6 改造题，sync.Pool 版 Encode 与原版行为等价。
- `TestPrefix4RoundTrip`：Take-away 改造题，4 字节前缀往返，MaxMsgLen 随之变化。
- `BenchmarkFrameEncode`/`BenchmarkFrameDecode`：§2 基准，-benchmem 看分配数。

**预测**（先写答案再跑，猜错的地方才是认知边界）：
1. TestZeroLenFrameBoundary：`make([]byte, 0)` 交给 io.ReadFull 会读几次？返回的 err 是 nil 还是 io.EOF？
2. TestDropWhenInFlightFull：在飞满后发来的帧被丢弃，客户端 TCP 读侧会看到什么？连接关闭、EOF、还是只是没回包？

**观察**（跑之前写下你预期的数字，跑完对照）：
3. `go test -bench . -benchmem ./exercises/p34frame/`：Encode 与 Decode 的 allocs/op 各是多少？和正文 §2 的 1/3 对不上吗（原因：练习包的 Pool 版改变了哪一项）？
4. TestSplitWriteRoundTrip 里两次 Write 之间 sleep 50ms，总耗时有没有涨 50ms？（思考：服务端是阻塞读，第二次 Write 到达前 goroutine 停在哪。）

**改造**（两题各练一个 §6 的优化点）：
5. 把 TestFrameEncodePool 的池归还点从"编码后立即归还"改成"写入连接后归还"，池还省得到吗？（提示：归还得等下游用完，生命周期跨函数的缓冲不适合 sync.Pool 的简单用法。）
6. 给帧解码加总超时：DecodeFrame 拿到的 reader 若带 deadline，半包攻击者的"卡到 EOF"变成"卡到超时"，serveConn 要怎么把 IdleTimeout 传给帧层？（对照 server.go L104 的 deadline 刷新点。）
