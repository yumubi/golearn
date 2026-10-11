// Package main 是 go 版 dnsproxi 的 D1 骨架：UDP 转发 + sinkhole。
// 对照你的 Rust 版 /Users/akka/Dev/learning/rs/dnsproxi/src/main.rs。
//
// 运行（对照你 Rust 版 run.sh query 场景）：
//
//	go run .                # 监听 :5353，上游 :5300
//	DNS_UPSTREAM=1.1.1.1:53 go run .   # 真上游
//
// 状态：G1（D1 等价）已实装。G2 起在此加 cache，G5 加主动老化 goroutine。
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golearn/dnsproxi-go/cache"
	"golearn/dnsproxi-go/wire"
)

// Config 启动参数（env 风格与你 Rust 版一致）。
type Config struct {
	Listen   string // DNS_LISTEN，默认 :5353
	Upstream string // DNS_UPSTREAM，默认 127.0.0.1:5300
	Sinkhole string // DNS_SINKHOLE，前缀匹配则 NXDOMAIN（你 D1 的 block 场景）
	CacheOn  bool   // DNS_CACHE=1，应答缓存（你 D2）
	CacheMax int    // DNS_CACHE_MAX，容量上限；0=无界（你 D3）
	SampleK  int    // DNS_CACHE_SAMPLE_K，满时抽样数；0=全表（你 D4）
	Seed     uint64 // DNS_CACHE_SEED，逐出 RNG 种子；0=固定默认
	Admit    string // DNS_CACHE_ADMIT：off|tinylfu（你 D5）
	Door     bool   // DNS_CACHE_DOORKEEPER，挡板开关（默认关，Caffeine 也没开）

	// G5 主动老化（你 D6）：默认全关 = D1-D5 逐位原行为——TTL 到期只是
	// 让读路径当 miss，条目本身赖着不走。
	ActiveTTL      bool // DNS_CACHE_ACTIVE_TTL=1，开后台回收
	ExpireHZ       int  // DNS_CACHE_EXPIRE_HZ，1..500，默认 10
	ExpireSample   int  // DNS_CACHE_EXPIRE_SAMPLE，默认 20；0=抽表长次
	ExpirePct      int  // DNS_CACHE_EXPIRE_PCT，默认 10（ACCEPTABLE_STALE，不是 25）
	ExpireBudgetUs int  // DNS_CACHE_EXPIRE_BUDGET_US，默认 1000
	ExpireLog      bool // DNS_CACHE_EXPIRE_LOG，每周期打 [expire]
	HitPathPool    bool // DNS_HITPATH_POOL=1，命中路径走池化 buffer（labs 实验 4 B 形态）
	WorkerPool     int  // DNS_WORKER_POOL=N，worker pool 形态（labs 实验 6）；0=goroutine-per-packet
}

// packet 是 worker pool 模式的队列元素。
type packet struct {
	client net.Addr
	query  []byte
}

func loadConfig() Config {
	return Config{
		Listen:   envOr("DNS_LISTEN", ":5353"),
		Upstream: envOr("DNS_UPSTREAM", "127.0.0.1:5300"),
		Sinkhole: os.Getenv("DNS_SINKHOLE"),
		CacheOn:  os.Getenv("DNS_CACHE") == "1",
		CacheMax: envInt("DNS_CACHE_MAX", 0),
		SampleK:  envInt("DNS_CACHE_SAMPLE_K", 0),
		Seed:     envUint("DNS_CACHE_SEED", 0xD15CA5E5),
		Admit:    envOr("DNS_CACHE_ADMIT", "off"),
		Door:     os.Getenv("DNS_CACHE_DOORKEEPER") == "1",

		ActiveTTL:      os.Getenv("DNS_CACHE_ACTIVE_TTL") == "1",
		ExpireHZ:       envIntClamp("DNS_CACHE_EXPIRE_HZ", 10, 1, 500),
		ExpireSample:   envInt("DNS_CACHE_EXPIRE_SAMPLE", 20),
		ExpirePct:      envInt("DNS_CACHE_EXPIRE_PCT", 10),
		ExpireBudgetUs: envInt("DNS_CACHE_EXPIRE_BUDGET_US", 1000),
		ExpireLog:      os.Getenv("DNS_CACHE_EXPIRE_LOG") == "1",
		HitPathPool:    os.Getenv("DNS_HITPATH_POOL") == "1",
		WorkerPool:     envInt("DNS_WORKER_POOL", 0),
	}
}

// envIntClamp 越界回退默认（你 Rust 版 :846 注释：hz>1000 时 ticker 直接炸，
// 且炸在后台任务里、横幅照常打——看起来和"刚打开还没触发"一模一样，最阴险）。
func envIntClamp(k string, def, lo, hi int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= lo && n <= hi {
			return n
		}
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envUint(k string, def uint64) uint64 {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			return n
		}
	}
	return def
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// Proxy D1 转发器 + G2 应答缓存。
type Proxy struct {
	cfg   Config
	cache *cache.Cache // CacheOn 时非 nil
}

// Run 启动 UDP 监听循环，阻塞到 ctx 取消。
//
// 对照你 Rust 版的 tokio::spawn(accept 循环)：Go 没有 async 块——
// 直接 for 循环 + 每 query 一个 goroutine。ctx 取消 = close 监听口
// （ReadFromUDP 立刻返回错误）+ WaitGroup 等在飞的 query 排空。
func (p *Proxy) Run(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", p.cfg.Listen)
	if err != nil {
		return fmt.Errorf("resolve listen %s: %w", p.cfg.Listen, err)
	}
	pc, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", p.cfg.Listen, err)
	}
	if p.cfg.CacheOn && p.cache == nil {
		// Run 前没建就这里建（测试可能直接构造 Proxy{cfg:…}）。
		p.cache = cache.NewCap(p.cfg.CacheMax, p.cfg.SampleK, p.cfg.Seed)
		if p.cfg.Admit == "tinylfu" {
			// 无界缓存配准入没意义（准入只在满仓时起作用）；仍建最小规模草图，
			// 让 [cachestats] 一直有数可看。
			cap := p.cfg.CacheMax
			if cap == 0 {
				cap = 256
			}
			p.cache.Sketch = cache.NewSketch(cap, p.cfg.Door, true, 8)
		}
	}

	var wg sync.WaitGroup

	// G5 主动老化：cache_on && active_ttl 才起后台回收（对照 Rust :931 分支）。
	if p.cfg.CacheOn && p.cfg.ActiveTTL {
		ecfg := cache.DefaultExpireCfg()
		ecfg.HZ = p.cfg.ExpireHZ
		ecfg.Sample = p.cfg.ExpireSample
		ecfg.Pct = p.cfg.ExpirePct
		ecfg.Budget = time.Duration(p.cfg.ExpireBudgetUs) * time.Microsecond
		ecfg.Exchange = p.cfg.ExpireLog
		done := make(chan struct{})
		var once sync.Once
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer once.Do(func() { close(done) })
			for st := range p.cache.RunExpirer(done, ecfg) {
				CacheEntries.Set(float64(p.cache.Len()))
				if ecfg.Exchange {
					logger.Debug("expire round", "rounds", st.Rounds, "checked", st.Checked,
						"removed", st.Removed, "us", st.Took.Microseconds())
				}
			}
		}()
		// Run 返回时（读循环退出）关掉 expirer。
		defer once.Do(func() { close(done) })
	}

	go func() { // ctx 取消 → 关监听口，ReadFromUDP 立刻断
		<-ctx.Done()
		pc.Close()
	}()

	buf := make([]byte, 4096) // DNS over UDP 上限 4096（EDNS0）——两种读循环共用
	if p.cfg.WorkerPool > 0 {
		// labs 实验 6（P9）：worker pool 形态——固定 N 个 worker 吃 channel，
		// 消掉 per-packet spawn + 上台下台（P8 trace：3 秒 48.5 万次上台、
		// 每次只摊 ~13µs 有效功）。Rust 对照：task 池 ≈ 预建 mpsc 消费者。
		qch := make(chan packet, p.cfg.WorkerPool)
		for range p.cfg.WorkerPool {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for pkt := range qch {
					func() {
						defer func() { // 同 per-packet 形态：入口 recover
							if r := recover(); r != nil {
								logger.Error("query panic", "panic", fmt.Sprint(r))
							}
						}()
						p.handleQuery(pc, pkt.client, pkt.query)
					}()
				}
			}()
		}
		for {
			n, client, err := pc.ReadFromUDP(buf)
			if err != nil {
				close(qch) // 排空 worker
				wg.Wait()
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("read: %w", err)
			}
			query := append([]byte{}, buf[:n]...)
			qch <- packet{client: client, query: query} // 满时背压：读循环阻塞
		}
	}
	for {
		n, client, err := pc.ReadFromUDP(buf)
		if err != nil {
			wg.Wait()
			if ctx.Err() != nil {
				return nil // 优雅关停：不算错误
			}
			return fmt.Errorf("read: %w", err)
		}
		query := append([]byte{}, buf[:n]...) // 每查询独立副本（buf 会复用）
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { // P3：goroutine panic 杀进程，入口必须 recover
				if r := recover(); r != nil {
					logger.Error("query panic", "panic", fmt.Sprint(r))
				}
			}()
			p.handleQuery(pc, client, query)
		}()
	}
}

// handleQuery 单个 DNS 查询：解析 → sinkhole 判断 → 转发 → 回包。
//
// 对照你 Rust 版 D1：每查询临时 socket（NAT per-source 同款直觉）。
// Go 细节：net.DialUDP 建临时源口，读应答带 deadline（防上游黑洞挂住 goroutine）。
func (p *Proxy) handleQuery(pc net.PacketConn, client net.Addr, query []byte) {
	InFlight.Inc()
	defer InFlight.Dec()

	// P18：每查询一个 id，ctx 里带到任何日志调用（P16 CtxHandler 语义）。
	ctx := withRequestID(context.Background(), newQueryID())

	if len(query) < 12 {
		QueriesTotal.WithLabelValues("OTHER", "short").Inc()
		return // 太短，连头都放不下，静默丢（和你 Rust 版一致）
	}

	// sinkhole：question 的 qname 前缀命中 → 本地 NXDOMAIN，上游不见包。
	if p.cfg.Sinkhole != "" {
		if name, _, err := wire.ParseName(query, 12); err == nil &&
			strings.HasPrefix(name, p.cfg.Sinkhole) {
			qtypeLabelN(query, name, "sinkhole")
			resp := nxdomain(query)
			if _, err := pc.WriteTo(resp, client); err != nil {
				logger.ErrorContext(ctx, "sinkhole write failed", "err", err.Error())
			}
			return
		}
	}

	// G2 缓存命中：未过期的应答直接回，不问上游（对照 Rust :1056）。
	// G4：频率要按【请求流】统计，命中也要数——increment 在查询入口，
	// 不在 miss 分支（对照 Rust :1071 注释）。
	if p.cfg.CacheOn {
		if q, err := cache.Request(query); err == nil {
			k := cache.Key{Name: q.Name, Type: q.Type}
			if p.cache.Sketch != nil {
				p.cache.Sketch.Increment(k)
			}
			if e, ok := p.cache.Get(k); ok {
				QueriesTotal.WithLabelValues(qtypeLabel(q.Type), "hit").Inc()
				// labs 实验 4：HitPathPool=1 走池化零分配形态（B），
				// 否则走原 Reply（A）。语义逐行同 cache/reply.go 两个版本。
				if p.cfg.HitPathPool {
					bufp := cache.BufPool.Get().(*[]byte)
					resp := e.ReplyPool(query, *bufp)
					if resp != nil {
						if _, err := pc.WriteTo(resp, client); err != nil {
							logger.ErrorContext(ctx, "cache hit write failed", "err", err.Error())
						}
						logger.InfoContext(ctx, "cache hit", "qname", q.Name, "qtype", qtypeLabel(q.Type), "ttl_left", e.MinTTL-e.AgeSecs())
					}
					cache.BufPool.Put(bufp) // WriteTo 完成后才还——同 goroutine 顺序保证
					if resp != nil {
						return
					}
				} else if resp := e.Reply(query); resp != nil {
					if _, err := pc.WriteTo(resp, client); err != nil {
						logger.ErrorContext(ctx, "cache hit write failed", "err", err.Error())
					}
					logger.InfoContext(ctx, "cache hit", "qname", q.Name, "qtype", qtypeLabel(q.Type), "ttl_left", e.MinTTL-e.AgeSecs())
					return
				}
			} else {
				QueriesTotal.WithLabelValues(qtypeLabel(q.Type), "miss").Inc()
			}
			logger.InfoContext(ctx, "cache miss, forwarding", "qname", q.Name, "qtype", qtypeLabel(q.Type))
		}
	}

	upStart := time.Now()
	upAddr, err := net.ResolveUDPAddr("udp", p.cfg.Upstream)
	if err != nil {
		logger.ErrorContext(ctx, "resolve upstream failed", "err", err.Error())
		qtypeLabelN(query, "", "upstream_error")
		UpstreamErrorsTotal.WithLabelValues("network").Inc() // resolve 失败不是对端拒绝
		return
	}
	up, err := net.DialUDP("udp", nil, upAddr) // 临时源口，per-query
	if err != nil {
		logger.ErrorContext(ctx, "dial upstream failed", "err", err.Error())
		qtypeLabelN(query, "", "upstream_error")
		UpstreamErrorsTotal.WithLabelValues(upstreamErrReason(err)).Inc()
		return
	}
	defer up.Close()
	if err := up.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		logger.ErrorContext(ctx, "set deadline failed", "err", err.Error())
		qtypeLabelN(query, "", "upstream_error")
		UpstreamErrorsTotal.WithLabelValues("network").Inc()
		return
	}

	if _, err := up.Write(query); err != nil {
		logger.ErrorContext(ctx, "upstream write failed", "err", err.Error())
		qtypeLabelN(query, "", "upstream_error")
		UpstreamErrorsTotal.WithLabelValues(upstreamErrReason(err)).Inc()
		return
	}
	resp := make([]byte, 4096)
	n, err := up.Read(resp)
	if err != nil {
		// 含超时；不回包=client 侧超时兜底
		logger.WarnContext(ctx, "upstream read failed", "err", err.Error())
		qtypeLabelN(query, "", "upstream_error")
		UpstreamErrorsTotal.WithLabelValues(upstreamErrReason(err)).Inc()
		return
	}
	UpstreamLatency.Observe(time.Since(upStart).Seconds())
	qtypeLabelN(query, "", "forwarded")
	// G2/G3/G4 入缓存：可缓存应答 → Plan + PutFull（对照 Rust :1121）。
	// 失败只是不缓存，不影响回包；逐出/拒绝记录带回来在锁外打日志
	// （你 Rust 版 :1194 注释的同款理由）。
	if p.cfg.CacheOn {
		if plan, err := cache.PlanResponse(resp[:n], query); err == nil {
			ev, rej := p.cache.PutFull(plan.Key, &cache.Entry{
				Resp:   append([]byte(nil), resp[:n]...),
				At:     time.Now(),
				TTLs:   plan.TTLs,
				MinTTL: plan.Lifetime,
			})
			if ev != nil {
				logger.InfoContext(ctx, "cache evict", "victim", ev.Key.Name, "victim_type", qtypeLabel(ev.Key.Type),
					"new", plan.Key.Name, "policy", ev.Policy, "checked", ev.Checked)
			}
			if rej != nil {
				// 没进缓存：频率不够
				logger.DebugContext(ctx, "cache reject", "qname", rej.Key.Name,
					"cand_freq", rej.Cand, "victim_freq", rej.Vict)
			}
		}
		CacheEntries.Set(float64(p.cache.Len()))
	}

	if _, err := pc.WriteTo(resp[:n], client); err != nil {
		logger.ErrorContext(ctx, "client write failed", "err", err.Error())
	}
}

// nxdomain 构造 NXDOMAIN 应答（QR=1, RCode=3），echo 原 header+question。
// 对照你 Rust 版：只翻转 flags，ID 和 question 原样带回。
func nxdomain(query []byte) []byte {
	h, err := wire.ParseHeader(query)
	if err != nil {
		return nil
	}
	h.QR = true
	h.RCode = 3 // NXDOMAIN
	resp := wire.EncodeHeader(h)
	// question（12 字节头之后、到 name+4 字节 type/class 为止）原样 echo。
	// D1 只处理 QDCount==1。
	if h.QDCount == 1 {
		if _, next, err := wire.ParseName(query, 12); err == nil {
			end := next + 4
			if end <= len(query) {
				return append(resp, query[12:end]...)
			}
		}
	}
	return resp
}

// qtypeLabelN 按 wire 里的 qtype 记数（name 参数保留对称，暂不用：
// qname 高基数不进 label，P13 §13.7 纪律）。
func qtypeLabelN(query []byte, _ string, outcome string) {
	if _, next, err := wire.ParseName(query, 12); err == nil && next+2 <= len(query) {
		t := uint16(query[next])<<8 | uint16(query[next+1])
		QueriesTotal.WithLabelValues(qtypeLabel(t), outcome).Inc()
		return
	}
	QueriesTotal.WithLabelValues("OTHER", outcome).Inc()
}

func main() {
	cfg := loadConfig()
	logger = newLogger(os.Stderr, logLevel())
	ctx := context.Background()
	p := &Proxy{cfg: cfg}
	// P17：指标抓取口独立端口（:9153），业务 UDP :5353 不掺 HTTP。
	go func() {
		msrv := metricsServer(envOr("DNS_METRICS_LISTEN", ":9153"))
		logger.Info("metrics endpoint up", "addr", msrv.Addr)
		if err := msrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server failed", "err", err.Error())
		}
	}()
	logger.Info("dnsproxi-go starting",
		"listen", cfg.Listen, "upstream", cfg.Upstream, "sinkhole", strings.TrimSpace(cfg.Sinkhole),
		"cache", cfg.CacheOn, "max", cfg.CacheMax, "admit", cfg.Admit,
		"active_ttl", cfg.ActiveTTL, "worker_pool", cfg.WorkerPool)
	if err := p.Run(ctx); err != nil {
		logger.Error("run failed", "err", err.Error())
		os.Exit(1)
	}
}
