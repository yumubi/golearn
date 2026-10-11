#!/usr/bin/env bash
# dnsproxi-go D1 冒烟：对照你 Rust 版 lessonD1-dns/run.sh 的 query/block 场景。
# 用法：./run.sh [query|block|stop]
set -u
cd "$(dirname "$0")"

UPSTREAM_PORT=15300
PROXY_PORT=15353
PID_FILE=/tmp/dnsproxi-go.pid

start_upstream() {
  # 笨办法：Python 最小 DNS 应答器（固定 A 记录）。G2 加了请求计数：
  # 每收到一个查询打一行 [upstream] qname hit=N，缓存场景靠它断言“上游只答 2 次”。
  python3 - "$UPSTREAM_PORT" <<'PY' &
import socket, struct, sys, collections
port = int(sys.argv[1])
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(("127.0.0.1", port))
print(f"fake upstream on :{port}", flush=True)
counts = collections.Counter()
while True:
    data, addr = s.recvfrom(4096)
    if len(data) < 12: continue
    tid = data[:2]
    off = 12
    while off < len(data) and data[off] != 0:
        if data[off] & 0xC0:
            off += 2; break
        off += 1 + data[off]
    off += 1
    qtype = struct.unpack("!H", data[off:off+2])[0]
    off += 4
    question = data[12:off]
    # 可读 qname：从 question 段解出（len+label 序列）
    parts, p = [], 12
    while p < len(data) and data[p] != 0:
        n = data[p]; parts.append(data[p+1:p+1+n].decode()); p += 1 + n
    qname = ".".join(parts) or "(root)"
    counts[(qname, qtype)] += 1
    print(f"[upstream] {qname} type={qtype} hit={counts[(qname, qtype)]}", flush=True)
    flags = bytearray(data[2:4]); flags[0] |= 0x80  # QR=1
    resp = tid + bytes(flags) + struct.pack("!HHHH", 1, 1, 0, 0) + question
    resp += b"\xc0\x0c" + b"\x00\x01\x00\x01" + struct.pack("!I", 60) + b"\x00\x04" + bytes([10,0,0,1])
    s.sendto(resp, addr)
PY
  echo $! > /tmp/dnsproxi-upstream.pid
}

stop_all() {
  [ -f $PID_FILE ] && kill "$(cat $PID_FILE)" 2>/dev/null && rm $PID_FILE
  [ -f /tmp/dnsproxi-upstream.pid ] && kill "$(cat /tmp/dnsproxi-upstream.pid)" 2>/dev/null && rm /tmp/dnsproxi-upstream.pid
}

case "${1:-query}" in
  start)
    stop_all
    start_upstream
    DNS_LISTEN=:$PROXY_PORT DNS_UPSTREAM=127.0.0.1:$UPSTREAM_PORT go run . &
    echo $! > $PID_FILE
    sleep 2
    echo "proxy on :$PROXY_PORT → upstream :$UPSTREAM_PORT"
    ;;
  query)
    echo "== 正常解析 example.test（期望 NOERROR + A 10.0.0.1，上游见包）=="
    dig +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT example.test A +noall +answer +comments
    ;;
  block)
    echo "== sinkhole：DNS_SINKHOLE=blocked. 挡 blocked.test（期望 NXDOMAIN）=="
    stop_all
    pkill -f "dnsproxi-go" 2>/dev/null   # 杀掉 start 场景残留的旧实例（端口独占）
    sleep 1
    start_upstream
    DNS_LISTEN=:$PROXY_PORT DNS_UPSTREAM=127.0.0.1:$UPSTREAM_PORT DNS_SINKHOLE=blocked. go run . &
    echo $! > $PID_FILE
    sleep 3
    dig +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT blocked.test A +noall +comments | grep -E "status|NXDOMAIN"
    echo "== 对照组 open.test 照常通过 =="
    dig +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT open.test A +noall +answer
    ;;
  cache)
    echo "== G2 缓存：DNS_CACHE=1，4 查同名，上游只应见 2 次（miss + 过期重取）=="
    stop_all
    pkill -f "dnsproxi-go" 2>/dev/null
    sleep 1
    start_upstream
    DNS_LISTEN=:$PROXY_PORT DNS_UPSTREAM=127.0.0.1:$UPSTREAM_PORT DNS_CACHE=1 go run . &
    echo $! > $PID_FILE
    sleep 3
    # dig 默认带 EDNS OPT（ARCOUNT=1）→ cache.Request 拒收（EDNS 留给后续课，
    # 你 Rust 版 cache_request 同款）；macOS dig 9.10.6 还默认置 AD 位（byte3=0x20，
    # flags2!=0 同样拒，+cd 消不掉 AD，得 +noad）。+noedns +noad 才是“纯 RD=1 查询”。
    echo "-- 第 1 查：miss，上游应答（看 [upstream] hit=1）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT cache.test A +noall +answer
    sleep 1
    echo "-- 第 2 查（1s 后）：命中，上游无包，TTL 倒计时 60→59 --"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT cache.test A +noall +answer
    sleep 62
    echo "-- 第 3 查（62s 后）：已过期，重新问上游（hit=2），TTL 回到 60 --"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT cache.test A +noall +answer
    echo "== 上游视角（应只有 hit=1 和 hit=2 两条 cache.test）=="
    ;;
  evict)
    echo "== G3 逐出：DNS_CACHE_MAX=2 DNS_CACHE_SAMPLE_K=3，查 3 个名字看逐出 =="
    stop_all
    pkill -f "dnsproxi-go" 2>/dev/null
    sleep 1
    start_upstream
    DNS_LISTEN=:$PROXY_PORT DNS_UPSTREAM=127.0.0.1:$UPSTREAM_PORT \
      DNS_CACHE=1 DNS_CACHE_MAX=2 DNS_CACHE_SAMPLE_K=3 DNS_CACHE_EVICT_LOG=1 go run . &
    echo $! > $PID_FILE
    sleep 3
    echo "-- 查 ev1（miss，表 1/2）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ev1.test A +noall +answer
    echo "-- 查 ev2（miss，表 2/2 满）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ev2.test A +noall +answer
    sleep 1
    echo "-- 再查 ev2（命中，变热）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ev2.test A +noall +answer
    echo "-- 查 ev3（满仓 → 逐出最冷的 ev1，看代理日志 [evict]）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ev3.test A +noall +answer
    echo "-- 查 ev1（已不在缓存 → 重新问上游；满仓挤掉 ev2：ev2 命中时间戳早于 ev3 插入）--"
    sleep 1
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ev1.test A +noall +answer
    echo "-- 查 ev2（同上，重新问上游，挤掉 ev3）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ev2.test A +noall +answer
    echo "== 上游视角：ev1×2、ev2×2、ev3×1；逐出链 ev1→ev2→ev3 =="
    ;;
  admit)
    echo "== G4 准入：DNS_CACHE_MAX=2 DNS_CACHE_ADMIT=tinylfu，热名字活、冷名字被拒 =="
    stop_all
    pkill -f "dnsproxi-go" 2>/dev/null
    sleep 1
    start_upstream
    DNS_LISTEN=:$PROXY_PORT DNS_UPSTREAM=127.0.0.1:$UPSTREAM_PORT \
      DNS_CACHE=1 DNS_CACHE_MAX=2 DNS_CACHE_SAMPLE_K=2 DNS_CACHE_ADMIT=tinylfu go run . &
    echo $! > $PID_FILE
    sleep 3
    echo "-- 养热 hot1/hot2：各查 5 次（miss 后 hit）--"
    for n in 1 2 3 4 5; do
      dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT hot1.test A +noall +short
      dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT hot2.test A +noall +short
      sleep 0.2
    done
    echo "-- 冷名字 cold.test 来了：草图里零频，打不过在位者 → [reject] --"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT cold.test A +noall +short
    echo "-- 验证：hot1/hot2 还在缓存（再查应 hit），cold 不在（再查应 miss 上游）--"
    sleep 1
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT hot1.test A +noall +short
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT cold.test A +noall +short
    ;;
  expire)
    stop_all
    pkill -f "dnsproxi-go" 2>/dev/null
    sleep 1
    start_upstream
    DNS_LISTEN=:$PROXY_PORT DNS_UPSTREAM=127.0.0.1:$UPSTREAM_PORT \
      DNS_CACHE=1 DNS_CACHE_ACTIVE_TTL=1 DNS_CACHE_EXPIRE_HZ=20 \
      DNS_CACHE_EXPIRE_SAMPLE=10 DNS_CACHE_EXPIRE_LOG=1 go run . &
    echo $! > $PID_FILE
    sleep 3
    echo "-- 查 ghost.test 一次（进缓存，TTL 60s）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ghost.test A +noall +short
    echo "-- 等 62s：TTL 到期（读路径 miss）+ 主动老化把尸体删掉 --"
    sleep 62
    echo "-- 再查：应 miss → 上游（[expire] 日志应显示 removed>=1）--"
    dig +noedns +noad +time=2 +tries=1 @127.0.0.1 -p $PROXY_PORT ghost.test A +noall +short
    echo "== 上游视角：ghost.test 应 hit=2（第一次 + 老化后重取）=="
    ;;
  stop)
    stop_all
    echo stopped
    ;;
  *) echo "usage: $0 [start|query|block|cache|evict|admit|expire|stop]"; exit 1 ;;
esac
