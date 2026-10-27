---
title: P24 · 网络协议栈：从 ping 到 DNS 全链路
---

# P24 · 网络协议栈：从 ping 到 DNS 全链路


> 这章学完你能干什么：抓一次真实 DNS 查询逐字节读出报文结构；解释
> "dig 两次，上游只被问一次"的 TTL 账本；看懂 TCP 状态机里
> ESTABLISHED/TIME_WAIT 各是谁的遗产。教具：dnsproxi（P6 毕业项目）
> + tcpdump。读者：读完 P8（HTTP）和 P6（dnsproxi 写过一遍）。
>
> 实战说明：全部命令真跑（macOS lo0 抓包）；tcpdump 需要 sudo，
> 输出实拍自本机。

## 体系背景：分层不是比喻，是封装

工程上常用四层模型理解封装：DNS 报文可套上 UDP 头，再套 IP 头，
最后经链路层传输。以下示例的 IPv4 头为 20 字节；IPv4 选项和 IPv6
会改变头部形式，回环接口也不一定使用以太网帧。

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

## 链路观察三件套：ping/traceroute/ICMP

DNS 之前先看更底层的连通性。ICMP 是 IP 的伴生协议（协议号 1，
无端口概念），ping/traceroute 靠它探测路径（真机，企业网实测）：

```sh
$ ping -c 2 -t 3 1.1.1.1
2 packets transmitted, 0 packets received, 100.0% packet loss
↑ 没收到 echo 回复；可能被过滤，也可能存在其他网络问题

$ traceroute -m 3 -w 1 1.1.1.1
 1  10.10.54.3 (10.10.54.3)    18.158 ms   11.252 ms  14.699 ms
 2  10.10.150.230 (10.10.150.230) 15.443 ms 12.941 ms 14.301 ms
 3  * * *                      ← TTL=3 的探测包没有收到回复，原因待查
```

traceroute 的原理就是**操纵 IP 头的 TTL 字段**：发 TTL=1 的包，
第一跳路由器把 TTL 减到 0，回 ICMP Time Exceeded（源地址就是
它自己）；TTL=2 问出第二跳，以此类推。**TTL 不是秒，是跳数**
（和 DNS TTL 是两个东西，后者才是秒）。

排障时区分现象和原因：ICMP 没有回复不代表网络一定中断；TCP RST
可能来自目标主机或中间设备；握手超时可能是丢包或过滤。再结合
目标地址、抓包及服务状态判断原因。

curl 的退出码区分前两种（真跑，httpkv 没起）：

```sh
$ curl -sS --max-time 2 localhost:8091/kv/x; echo "exit=$?"
curl: (7) Failed to connect to localhost port 8091: Connection refused
exit=7                          ← RST：机器活着，端口没服务
```

握手无响应可能导致 `--max-time` 后 exit=28；超时本身不能证明
一定是防火墙丢包。refused 和 timeout 是进一步排查的两个入口。

## UDP：无连接意味着什么

先跑通链路：dnsproxi（:15353）+ fake upstream（:15300）：

```sh
$ cd dnsproxi-go && go build -o /tmp/dnsproxi-go .
$ DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 /tmp/dnsproxi-go &
$ dig +time=2 +tries=1 @127.0.0.1 -p 15353 example.test A +noall +answer
example.test.		60	IN	A	10.0.0.1
```

四跳：dig → 代理 :15353 → 上游 :15300 → 回程。每跳都是独立 UDP
报文，**没有握手**：代理读到查询就转发，不等"连接建立"。

对照 TCP（httpkv）：accept 之前有三次握手，连接是内核里的一个
状态机条目（不是"管道"）。UDP 没有这个状态，所以 dnsproxi 每查询
开一个临时上游 socket（P6 压测的瓶颈就在 socket 生命周期，不在
"转发"本身）。

Go 侧的"无连接"长这样（P6 `Run` 的主循环，简化）：

```go
for {
    n, client, err := pc.ReadFromUDP(buf) // 谁来的都能收，无握手
    if err != nil {
        return fmt.Errorf("read: %w", err)
    }
    query := append([]byte{}, buf[:n]...) // buf 复用，每查询独立副本
    go p.handleQuery(pc, client, query)   // 回包发给 client 这个地址
}
```

没有 accept，没有 conn 对象，"连接"只存在于你回包用的那个
地址值里。

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
| DNS header | `108d 0120 0001 0000 0000 0001` | ID=0x108d；flags=0x0120（RD=1 + AD=1，dig 默认带 AD）；QDCOUNT=1 |
| question | `0774 6370 6475 6d70 0474 6573 7400` | len+label 编码：`7 tcpdump`、`4 test`、0 结束 |
| | `0001 0001` | QTYPE=A(1)、QCLASS=IN(1) |
| EDNS(附加) | `0000 2910 0000...` | OPT 记录（type 0x29），dig 默认带 EDNS0 |

响应包（46 字节）的关键差异：

```text
...6212 8180 0001 0001 0000 0001 0774...  c00c 0001 0001
0000 012c 0004 0102 0304
```

- flags `8180`：QR=1（是响应）+ RD/RA=1（递归可用）。P17 冒烟
  翻车记就是 QR 位丢了导致 dig 不认。
- `c00c`：**压缩指针**，0xc0|0x0c 指回 offset 12 的 question 域，
  不重复传 qname（P6 `wire.ParseName` 手工解析的就是它）。
- `0000 012c`：TTL=300 秒（fakeup 固定 300），下一节的缓存账本主角。
- `0004 0102 0304`：RDLENGTH=4，A 记录 1.2.3.4。

**qname 的 len+label 编码**（`07 tcpdump 04 test 00`）是 DNS 一切
解析的起点，Rust 版 `parse_header` 同款逻辑：读到 0 结束，遇到
0xc0 开头是指针跳转。

解析时沿普通 label 前进，遇到压缩指针就跳到报文中的偏移；解析
结果需要保留指针之前已经读到的前缀，而返回的下一偏移应指向原位置
的指针之后。真实输入还必须检查 label 和指针是否越界、限制跳转次数
以防循环，并处理非法的 label 类型。P6 的 `wire.ParseName` 展示了
这些边界；不要把省略边界检查的伪代码直接用于不可信报文。

## TTL 与缓存一致性

开缓存的 dnsproxi（`DNS_CACHE=1`），同一个名字连问两次：

```sh
$ DNS_CACHE=1 DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 /tmp/dnsproxi-go &
$ dig +noedns +noad @127.0.0.1 -p 15353 ttl.test A +noall +answer   # 第一次
ttl.test.		300	IN	A	1.2.3.4
$ dig +noedns +noad @127.0.0.1 -p 15353 ttl.test A +noall +answer   # 第二次（隔 2 秒）
ttl.test.		298	IN	A	1.2.3.4
```

代理日志（真机实拍）：

```text
{"msg":"cache miss, forwarding","qname":"ttl.test","req_id":"q-...87"}
{"msg":"cache hit","qname":"ttl.test","ttl_left":298,"req_id":"q-...89"}
```

第一次 miss 转发上游，第二次直接命中内存：answer 的 TTL 从 300 变
298，`ttl_left` 用（P6 G2 的 `time.Now` 单调钟）算出应答年龄。
上游只被问了 1 次：metrics `forwarded=1, hit=1`（fakeup 本身不打
日志，上游计数以代理 metrics 为准），走到 0 条目过期重新回源。

**默认 dig 为什么绕过缓存**（真机翻出来，比 P18 记录更细一层）：
`dig +noedns` 仍带 **AD 位**（报文 flags 是 0x0120 不是 0x0100），
缓存键要求"无 EDNS 且 flags 纯净"（P6 `cache.Request`：
`flags1 != 1 || flags2 != 0` 拒收），metrics 只有 forwarded、
连 miss 日志都不打。`+noedns +noad` 才命中。**缓存键是"请求形"**
的实证。

## TCP 状态机：连接是内核里的条目

httpkv 是 TCP 服务。先抓握手（真机）：

```text
$ sudo tcpdump -i lo0 -nn -c 3 \
    'tcp port 8090'
15:45:46.310247 IP 127.0.0.1.53878 > 127.0.0.1.8090: Flags [S]
    seq 289217297, win 65535, options [mss 16344,nop,wscale 6,...]
15:45:46.310344 IP 127.0.0.1.8090 > 127.0.0.1.53878: Flags [S.]
    seq 776946478, ack 289217298           ← SYN+ACK：服务端确认
15:45:46.310363 IP 127.0.0.1.53878 > 127.0.0.1.8090: Flags [.]
    ack 776946479                          ← 客户端最终 ACK
```

三行 Flags `[S]`、`[S.]`、`[.]` 就是握手三步。IP 头协议字段
这次是 0x06（对照 DNS 查询的 0x11）。再看握手之后的状态：

```sh
$ netstat -an -p tcp | grep ESTABLISHED
tcp4  0  0  127.0.0.1.55395  127.0.0.1.55403  ESTABLISHED
tcp4  0  0  127.0.0.1.55403  127.0.0.1.55395  ESTABLISHED
```

一对 ESTABLISHED（方向各一条）= 内核里两个 TCB（控制块），不是
"两根管子"。状态机关键迁移：

```
客户端：CLOSED → SYN_SENT → ESTABLISHED
服务端：LISTEN → SYN_RCVD → ESTABLISHED
ESTABLISHED → FIN_WAIT_1/2 → TIME_WAIT（主动关方，2MSL 后消失）
ESTABLISHED → CLOSE_WAIT（被动关方，等应用调 close）
```

两条工程铁律：

1. **TIME_WAIT 通常在主动关闭方**；持续时间依系统实现而异。
   高频短连接可能消耗客户端临时端口，复用连接可以降低压力。
2. **CLOSE_WAIT 表示已收到对端 FIN，等待本端应用关闭**。持续
   堆积可能是连接泄漏，也可能是应用仍在处理；结合连接寿命和
   调用栈排查。`defer conn.Close()` 有助于明确所有权。

P8 的 slowloris 从这里看也通：每条 TCP 连接是内核要维护状态的
资源，攻击者开着握手完成的连接慢慢发字节，应用层
`ReadHeaderTimeout` 不设，内核资源就被慢慢占满。

预测题 1 的验证（真跑，结果反转）：

```sh
$ dig +tcp +time=2 @127.0.0.1 -p 15353 tcptest.test A
;; Connection to 127.0.0.1#15353(127.0.0.1) for tcptest.test failed:
   connection refused.
```

dnsproxi 只 `net.ResolveUDPAddr` 监听 UDP（P6 的设计决策：教具只
做 UDP 面），TCP 查询在握手前就被拒。这比"包数变 5"更有教学
价值：**协议选择是服务端声明出来的，客户端换协议只会撞墙**。
真想看 TCP 全景，可用 `dig +tcp @1.1.1.1 example.com A`，在实际
出口接口抓 `tcp port 53`；回环接口 lo0 抓不到发往外网的流量。
握手、查询、响应和关闭的包数会受复用与关闭方式影响。

## Take-away

1. 分层是字面封装：DNS 报文套 UDP 头套 IP 头；协议号区分 17/6。
2. UDP 没有 TCP 式握手与连接状态，但 socket 仍有内核状态；本代理
   每查询使用临时上游 socket。TCP 连接有自己的状态机。
3. DNS 报文：12B header + len+label qname + 压缩指针 c00c；
   flags 的 QR/RA 位是响应合法性的关键（P17 翻车记）。
4. DNS TTL 是缓存有效期；缓存命中后应答 TTL 随时间递减，本例
   的缓存还限制请求形（如 EDNS 和标志位）。
5. TIME_WAIT 通常在主动关方，持续堆积的 CLOSE_WAIT 值得排查关闭
   流程；慢攻击占用连接与应用资源。

## 练习：跟着做（无练习包，抓包就是练习）

**预测**（跑之前写答案；预测错的地方才是认知边界）：

1. dig 查询包的 IP 头里协议字段是 0x11；如果换 dig +tcp 查同一
   域名，会发生什么？（真跑过：见 TCP 一节的"结果反转"。）
2. `c00c` 的 0x0c 是十进制多少？它指向响应报文的哪个区域？
3. 开缓存后第二次 `dig` 同一域名（cache hit），tcpdump 在 15353
   端口还能抓到几个包？抓到的是查询还是响应？

**观察**（全部真跑）：

```sh
cd dnsproxi-go && go build -o /tmp/dnsproxi-go .
DNS_LISTEN=:15353 DNS_UPSTREAM=127.0.0.1:15300 DNS_CACHE=1 /tmp/dnsproxi-go &
dig +noedns +noad @127.0.0.1 -p 15353 c2.test A   # ×2，对照日志 miss/hit
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

BPF 过滤语法速记（tcpdump 的"查询语言"，按字节位置取值；udp[N]
的 N 从 UDP 头起算，DNS header 从 udp[8] 开始）：

```sh
# 只看响应：DNS flags（udp[10]）的 QR 位
sudo tcpdump -i lo0 -nn 'udp port 15353 and (udp[10] & 0x80 != 0)'

# 只看查询（QR=0），macOS 实测抓到 UDP length 38 的查询包
sudo tcpdump -i lo0 -nn -c 1 'udp port 15353 and (udp[10] & 0x80 = 0)' -X

# 按 UDP length 筛包长：EDNS 查询 41B（udp[4:2] 是 length 字段，6:2 是校验和）
sudo tcpdump -i lo0 -nn 'udp port 15353 and udp[4:2] = 0x0031'
```

存 pcap 离线分析（Wireshark 的 `dns.qry.name` 过滤器比 BPF 好
读）：

```sh
$ sudo tcpdump -i lo0 -nn -w /tmp/dns.pcap port 15353 &
$ dig @127.0.0.1 -p 15353 off.test A +noall +answer
$ tcpdump -nn -r /tmp/dns.pcap -X | head -8
reading from file /tmp/dns.pcap, link-type NULL_LOOPBACK (BSD loopback)
15:39:14.190098 IP 127.0.0.1.50273 > 127.0.0.1.15353: 40732+ [1au] A? off.test. (41)
15:39:14.190512 IP 127.0.0.1.15353 > 127.0.0.1.50273: 40732 1/0/0 A off.test. (46)
```

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
