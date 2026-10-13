# P24 · 网络协议栈：从 ping 到 DNS 全链路

> 这章学完你能干什么：抓一次真实 DNS 查询逐字节读出报文结构；解释
> "dig 两次，上游只被问一次"的 TTL 账本；看懂 TCP 状态机里
> ESTABLISHED/TIME_WAIT 各是谁的遗产。教具：dnsproxi（P6 毕业项目）
> + tcpdump。读者：读完 P8（HTTP）和 P6（dnsproxi 写过一遍）。
>
> 实战说明：全部命令真跑（macOS lo0 抓包）；tcpdump 需要 sudo，
> 输出实拍自本机。

## 体系背景：分层不是比喻，是封装

OSI 七层背了没用，工程上就四层，且每一层是**字面上的封装**：
应用字节（DNS 报文）套上 UDP 头（8 字节）套上 IP 头（20 字节）套
上以太网帧头。ping 一次、dig 一次，网卡上跑的就是这串套娃。

这层对 Rust 读者不陌生：std::net 的 UDP/TCP 封装、trust-dns 的
wire 解析，都长在同一套 BSD socket API 上。抓包工具更是语言无关，
tcpdump 对两个生态一视同仁。

```
应用  DNS 查询（12B header + question）
传输  UDP 头（源端口/目的端口/长度/校验和 = 8B）
网络  IP 头（版本/IHL/TTL/协议/源IP/目的IP = 20B）
链路  以太网帧 / lo0 回环
```

抓包工具（tcpdump/Wireshark）就是把这串套娃逐层剥开给你看。
下面用 dnsproxi 真抓一次。

## UDP：无连接意味着什么

先跑通链路：dnsproxi（:15353）+ fake upstream（:15300）：

```sh
$ cd dnsproxi-go && go build -o /tmp/dnsproxi-go .
$ DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 /tmp/dnsproxi-go &
$ dig +time=2 +tries=1 @127.0.0.1 -p 15353 example.test A +noall +answer
example.test.		60	IN	A	10.0.0.1
```

四跳：dig → 代理 :15353 → 上游 :15300 → 回程。每跳都是独立 UDP
报文，**没有握手**：代理读到查询就转发，不等"连接建立"。Go 侧
就是 `ReadFromUDP` 拿到 `(数据, 对端地址)`，回包往这个地址 sendto
即可（P6 的 `handleQuery`）。

对照 TCP（httpkv）：accept 之前有三次握手，连接是内核里的一个
状态机条目（不是"管道"）。UDP 没有这个状态，所以 dnsproxi 每查询
开一个临时上游 socket（P6 压测的瓶颈就在 socket 生命周期，不在
"转发"本身）。

## 逐字节读一次 DNS 查询

tcpdump 抓真实查询（macOS lo0，真机输出）：

```sh
$ sudo tcpdump -i lo0 -nn -X port 15353 -c 2 &
$ dig +time=2 @127.0.0.1 -p 15353 tcpdump.test A +noall +answer
```

查询包（41 字节）：

```text
0x0000:  4500 0045 e0aa 0000 4011 0000 7f00 0001
0x0010:  7f00 0001 c461 3bf9 0031 fe44 108d 0120
0x0020:  0001 0000 0000 0001 0774 6370 6475 6d70
0x0030:  0474 6573 7400 0001 0001 0000 2910 0000
0x0040:  0000 0000 00
```

逐层剥：

| 层 | 字节 | 含义 |
|---|---|---|
| IP 头 | `4500 0045` | 4=IPv4、5=头长 20B；总长 0x0045=69（41+8+20） |
| | `4011` | TTL=64、协议 0x11=17（UDP；TCP 是 06） |
| | `7f00 0001`×2 | 源/目的 127.0.0.1 |
| UDP 头 | `c461 3bf9 0031 fe44` | 源端口 50273 → 目的 15353；长 0x31=49、校验和 |
| DNS header | `108d 0120 0001 0000 0000 0001` | ID=0x108d；flags=0x0100（RD=1 递归请求）；QDCOUNT=1 |
| question | `0774 6370 6475 6d70 0474 6573 7400` | len+label 编码：`7 tcpdump`、`4 test`、0 结束 |
| | `0001 0001` | QTYPE=A(1)、QCLASS=IN(1) |
| EDNS(附加) | `0000 2910 0000...` | OPT 记录（type 0x29），dig 默认带 EDNS0 |

响应包（46 字节）的关键差异：

```text
...108d 8180 0001 0001 0000 0000 0774...  c00c 0001 0001
0000 003c 0004 0a00 0001
```

- flags `8180`：QR=1（是响应）+ RD/RA=1（递归可用）。P17 冒烟
  翻车记就是 QR 位丢了导致 dig 不认。
- `c00c`：**压缩指针**，0xc0|0x0c 指回 offset 12 的 question 域，
  不重复传 qname（P6 wire/name.go 手工解析的就是它）。
- `0000 003c`：TTL=60 秒，下一节的缓存账本主角。
- `0004 0a00 0001`：RDLENGTH=4，A 记录 10.0.0.1。

**qname 的 len+label 编码**（`07 tcpdump 04 test 00`）是 DNS 一切
解析的起点，Rust 版 `parse_header` 同款逻辑：读到 0 结束，遇到
0xc0 开头是指针跳转。

## TTL 与缓存一致性

开缓存的 dnsproxi（`DNS_CACHE=1`），同一个名字连问两次：

```sh
$ DNS_CACHE=1 DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 /tmp/dnsproxi-go &
$ dig @127.0.0.1 -p 15353 c1.test A +noall +answer   # 第一次
$ dig @127.0.0.1 -p 15353 c1.test A +noall +answer   # 第二次
```

代理日志（真机实拍）：

```text
{"msg":"cache miss, forwarding","qname":"c1.test","ttl_left":60}
{"msg":"cache hit","qname":"c1.test","ttl_left":60}
```

第一次 miss 转发上游（fakeup 计数 hit=1），第二次直接命中内存，
上游没被问。`ttl_left` 从应答里的 60 起步往下倒计时（P6 G2 的
`time.Now` 单调钟），走到 0 条目过期重新回源。

（Rust 侧对照：moka/quick-cache 的 TTL 语义相同，expired 后
回源重查；Rust 没有内置单调钟 wrap 的便利，要显式
Instant::elapsed。）

**缓存键**是 (qname, qtype) 加上请求 flags 的关键位，注意坑：
默认 dig 带 EDNS0/AD 位，不同客户端的报文不同形，命中就失效
（P18 修过：默认 dig 绕过缓存）。**TTL 是上游授权的"可信时长"**，
代理把 60 秒原样透传给客户端，三级（客户端/代理/上游）靠同一个
数字对齐一致性。改短 TTL 换新鲜度，改长换负载，这是运维旋钮的
协议层含义。

## TCP 状态机：连接是内核里的条目

httpkv 是 TCP 服务。看真实状态：

```sh
$ netstat -an -p tcp | grep ESTABLISHED
tcp4  0  0  127.0.0.1.55395  127.0.0.1.55403  ESTABLISHED
tcp4  0  0  127.0.0.1.55403  127.0.0.1.55395  ESTABLISHED
```

一对 ESTABLISHED（方向各一条）= 内核里两个 TCB（控制块），不是
"两根管子"。状态机关键迁移：

```
CLOSED → SYN_SENT（客户端 connect）→ SYN_RCVD → ESTABLISHED（三次握手完成）
ESTABLISHED → FIN_WAIT_1/2 → TIME_WAIT（主动关方，2MSL 后消失）
ESTABLISHED → CLOSE_WAIT（被动关方，等应用调 close）
```

两条工程铁律：

1. **TIME_WAIT 挂在主动关方**，持续 2MSL（1-4 分钟）。高频短连接
   打代理（未开 keep-alive）会把客户端端口耗尽，解法是连接池/
   keep-alive，不是改内核参数。
2. **CLOSE_WAIT 堆积 = 你家代码漏 close**。对端先关了，你还没调
   `conn.Close()`，内核条目永远挂着。Go 里 `defer conn.Close()`+
   `srv.Shutdown`（P23）就是防它。

P8 的 slowloris 从这里看也通：每条 TCP 连接是内核要维护状态的
资源，攻击者开着握手完成的连接慢慢发字节，应用层
`ReadHeaderTimeout` 不设，内核资源就被慢慢占满。

## Take-away

1. 分层是字面封装：DNS 报文套 UDP 头套 IP 头；协议号区分 17/6。
2. UDP 无连接 = 没有握手没有内核状态，代理每查询一个临时 socket；
   TCP 连接 = 内核 TCB 状态机条目。
3. DNS 报文：12B header + len+label qname + 压缩指针 c00c；
   flags 的 QR/RA 位是响应合法性的关键（P17 翻车记）。
4. TTL 是三级缓存的一致性契约：应答里的 60 秒原样透传；
   缓存键含请求形（EDNS 位不同即 miss）。
5. TIME_WAIT 在主动关方（2MSL），CLOSE_WAIT 堆积是你漏 close；
   慢攻击占的是内核连接状态资源。

## 练习：跟着做（无练习包，抓包就是练习）

**预测**（跑之前写答案；预测错的地方才是认知边界）：

1. dig 查询包的 IP 头里协议字段是 0x11；如果换 dig +tcp 查同一
   域名，这个字节变多少？包数从 2 变几（握手）？
2. `c00c` 的 0x0c 是十进制多少？它指向响应报文的哪个区域？

**观察**（全部真跑）：

```sh
cd dnsproxi-go && go build -o /tmp/dnsproxi-go .
DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 DNS_CACHE=1 /tmp/dnsproxi-go &
dig @127.0.0.1 -p 15353 c2.test A   # ×2，对照日志 miss/hit
sudo tcpdump -i lo0 -nn -X port 15353 -c 2   # 逐字节核对本章表格
```

**改造**：给 tcpdump 输出里的 flags 字节画 16 位分解图（QR/AA/TC/
RD/RA/RCODE 各在哪位），对照 P17 的 QR 翻车记录。

## 章末参考

### 常用抓包命令

| 命令 | 用途 |
|---|---|
| `tcpdump -i lo0 -nn port 53` | 回环 DNS，不解析主机名/端口名 |
| `tcpdump -X` | 十六进制+ASCII 全包 |
| `tcpdump -w a.pcap` | 存文件，Wireshark 打开 |
| `tcpdump 'udp[10:2] & 0x8000 != 0'` | BPF 过滤：只看响应（QR=1） |

### DNS 报文速查

| 字段 | 字节 | 备注 |
|---|---|---|
| ID | 2 | 请求响应配对 |
| flags | 2 | QR/AA/TC/RD/RA/RCODE |
| QD/AN/NS/AR | 8 | 四个 count |
| question | 变长 | len+label 直到 0；qtype/qclass |
| 压缩指针 | 2 | 0xc0 前缀 + 14 位 offset |

### Rust 对照总表

| 主题 | Go | Rust | 差异要点 |
|---|---|---|---|
| UDP 收发 | ReadFromUDP/SendtoUDP | UdpSocket::recv_from | 同一套 syscall 语义 |
| TCP 状态排查 | netstat/lsof | ss（Linux） | 内核视角相同 |
| 抓包 | tcpdump（同工具） | tcpdump | 无语言差异，协议是公共的 |
| wire 解析 | 手工位移（P6 wire/） | 手工位移 | 无 borrow checker，越界要显式防 |
| DNS 库 | miekg/dns（事实标准） | trust-dns/hickory | Rust 侧更重，Go 常手写 |
| 连接池 | http.Transport 内置 | reqwest 池内置 | TIME_WAIT 都靠 keep-alive 解 |
