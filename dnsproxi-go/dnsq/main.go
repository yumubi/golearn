// dnsq.go —— G6 压测发包器。
// 用法: go run dnsq.go -target 127.0.0.1:15353 -dur 10s -conc 64 -rate 0 -names 1000
//
//	-rate 0 = 不限速（打满）
//
// 输出: 发送数/应答数/丢失数/qps/延迟分位（P50/P90/P99/max）。
// 每个 goroutine 独立 UDP socket，题目从 names 个名字里轮转（keyspace 固定
// 才能测出缓存的命中路径，随机大空间测的是准入+逐出的路径——用 -spread 切换）。
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"sort"
	"sync"
	"time"
)

func buildQuery(id uint16, name string) []byte {
	q := make([]byte, 12, 512)
	binary.BigEndian.PutUint16(q[0:], id)
	q[2] = 0x01 // RD
	q[5] = 1    // QDCount=1
	for _, label := range splitName(name) {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0)
	q = binary.BigEndian.AppendUint16(q, 1) // A
	q = binary.BigEndian.AppendUint16(q, 1) // IN
	return q
}

func splitName(name string) []string {
	var out []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			out = append(out, name[start:i])
			start = i + 1
		}
	}
	if start < len(name) {
		out = append(out, name[start:])
	}
	return out
}

func main() {
	target := flag.String("target", "127.0.0.1:15353", "proxy address")
	dur := flag.Duration("dur", 10*time.Second, "test duration")
	conc := flag.Int("conc", 64, "concurrent workers")
	rate := flag.Int("rate", 0, "global rate limit qps (0=unlimited)")
	names := flag.Int("names", 1000, "keyspace size")
	spread := flag.Bool("spread", false, "uniform random keyspace (cache-hostile)")
	flag.Parse()

	// 预生成题目（每 worker 独立 id 空间，避免应答错配）。
	queries := make([][]byte, *names)
	for i := range queries {
		queries[i] = buildQuery(0x1234, fmt.Sprintf("n%05d.test", i))
	}

	stop := time.Now().Add(*dur)
	var sent, recv, lost uint64
	var lat []time.Duration
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 每 worker 的发送间隔 = conc / rate：并发 conc 个 worker，
	// 每个睡 conc/rate 秒 → 总速率 ≈ rate。
	perTick := time.Duration(0)
	if *rate > 0 {
		perTick = time.Duration(int64(time.Second) * int64(*conc) / int64(*rate))
	}

	start := time.Now()
	for w := range *conc {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			conn, err := net.DialUDP("udp", nil, mustAddr(*target))
			if err != nil {
				fmt.Fprintln(os.Stderr, "dial:", err)
				return
			}
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed)<<32))
			buf := make([]byte, 512)
			localLat := make([]time.Duration, 0, 1024)
			var localSent, localRecv, localLost uint64
			seq := uint64(0)
			for time.Now().Before(stop) {
				var q []byte
				if *spread {
					q = queries[rng.IntN(len(queries))]
				} else {
					q = queries[(seq+uint64(w)*uint64(*names)/uint64(*conc))%uint64(len(queries))]
				}
				id := uint16(seq)
				binary.BigEndian.PutUint16(q[0:], id)
				t0 := time.Now()
				if _, err := conn.Write(q); err != nil {
					localLost++
					continue
				}
				localSent++
				conn.SetReadDeadline(time.Now().Add(time.Second)) // 每包刷新：否则第 2 包起 deadline 已过期
				_, err = conn.Read(buf)
				if err != nil {
					localLost++
				} else if binary.BigEndian.Uint16(buf[0:]) != id {
					// 应答错配（旧包迟到），按应答计但不计延迟
					localRecv++
				} else {
					localRecv++
					localLat = append(localLat, time.Since(t0))
				}
				seq++
				if perTick > 0 {
					time.Sleep(perTick)
				}
			}
			mu.Lock()
			sent += localSent
			recv += localRecv
			lost += localLost
			lat = append(lat, localLat...)
			mu.Unlock()
		}(int64(w))
	}
	wg.Wait()
	elapsed := time.Since(start)

	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)]
	}
	fmt.Printf("sent=%d recv=%d lost=%d (%.2f%%)\n", sent, recv, lost, float64(lost)*100/float64(max(sent, 1)))
	fmt.Printf("elapsed=%s qps=%.0f\n", elapsed.Round(time.Millisecond), float64(recv)/elapsed.Seconds())
	fmt.Printf("latency p50=%s p90=%s p99=%s max=%s n=%d\n",
		pct(0.50).Round(time.Microsecond), pct(0.90).Round(time.Microsecond),
		pct(0.99).Round(time.Microsecond), pct(1).Round(time.Microsecond), len(lat))
}

func mustAddr(s string) *net.UDPAddr {
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		panic(err)
	}
	return a
}

func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
