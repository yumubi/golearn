// P34 扩展：DNS over TCP 监听器。复用 main.Proxy.handleQuery 的全部业务逻辑
// （sinkhole/缓存/转发/指标），本文件只做连接形态的差异：
//   - 出站回包不走 PacketConn.WriteTo 的单包语义，而是 2 字节前缀成帧；
//   - 入站查询从字节流按帧切开，一帧一个查询。
//
// 桥接点是 streamConn：把 net.Conn 适配成 net.PacketConn，
// handleQuery 只调用 WriteTo(应答, client)，适配器把它翻译成"成帧写给这条 TCP 连接"。
package tcp

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Handler 是 TCP 侧需要的最小业务接口，签名刻意与 (*main.Proxy).handleQuery 一致：
// 直接传方法值 p.handleQuery 即可，无需包装层。
type Handler func(pc net.PacketConn, client net.Addr, query []byte)

// ServerCfg TCP 监听参数。零值字段用默认。
type ServerCfg struct {
	// IdleTimeout 空闲超时：连接上超过此时长没有任何一帧往来就关。
	// 防 half-open 连接和只建连不发查询的慢速占坑。0 = 30s。
	IdleTimeout time.Duration
	// MaxConns 并发连接上限：满了之后新连接直接关（防 SYN 洪水/连接占坑）。
	// 0 = 512。
	MaxConns int
	// MaxInFlight 单连接在飞查询上限：超过后本帧被拒（连接不断，
	// 回包侧由业务层 WriteTo 自然失败）。0 = 64。
	MaxInFlight int
}

// Serve 阻塞跑 accept 循环：每连接一个 goroutine，ctx 取消即关监听口并等连接排空。
// 与 UDP 侧 Proxy.Run 同款骨架（close 监听口 → ReadFrom 返回错 → 判 ctx）。
func Serve(ctx context.Context, ln net.Listener, h Handler, cfg ServerCfg) error {
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 30 * time.Second
	}
	if cfg.MaxConns <= 0 {
		cfg.MaxConns = 512
	}
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 64
	}
	go func() {
		<-ctx.Done()
		ln.Close()
	}()
	var wg sync.WaitGroup
	var mu sync.Mutex
	conns := map[*streamConn]struct{}{}
	for {
		conn, err := ln.Accept()
		if err != nil {
			// 取消路径：关掉所有在飞连接，让 serveConn 的 DecodeFrame 立刻返回，
			// wg.Wait 才不会卡在 30s 空闲超时上。
			if ctx.Err() != nil {
				mu.Lock()
				for sc := range conns {
					_ = sc.Close()
				}
				mu.Unlock()
			}
			wg.Wait()
			if ctx.Err() != nil {
				return nil // 优雅关停：不算错误
			}
			return fmt.Errorf("accept: %w", err)
		}
		// MaxConns：满员时直接关新连接（客户端重试即可），不进 serve 队列。
		mu.Lock()
		full := len(conns) >= cfg.MaxConns
		mu.Unlock()
		if full {
			_ = conn.Close()
			continue
		}
		sc := &streamConn{conn: conn}
		mu.Lock()
		conns[sc] = struct{}{}
		mu.Unlock()
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
	}
}

// serveConn 单连接：整条连接包一个 streamConn 适配器，按帧读查询、
// 一帧一个 goroutine 交给 h（并发处理同一连接的粘包查询，RFC 允许 pipeline）。
// 空闲超时以 SetReadDeadline 落实：每读完一帧刷新。
func serveConn(ctx context.Context, sc *streamConn, h Handler, idle time.Duration, maxInFlight int64) {
	defer sc.Close()
	var inflight atomic.Int64
	for {
		_ = sc.conn.SetReadDeadline(time.Now().Add(idle))
		query, err := DecodeFrame(sc.conn)
		if err != nil {
			return // EOF/超时/半帧：连接到此为止，无需区分
		}
		// MaxInFlight：单连接在飞查询超限就丢帧（不断连接），防单连接打爆下游。
		if inflight.Add(1) > maxInFlight {
			inflight.Add(-1)
			continue
		}
		go func(q []byte) {
			defer inflight.Add(-1)
			// 每查询独立副本：对齐 UDP 路径"查询不可变"契约
			// （DecodeFrame 已返回独立 buf，副本是防御性兜底）。
			cq := append([]byte{}, q...)
			h(sc, sc.RemoteAddr(), cq)
		}(query)
	}
}

// streamConn 把一条 TCP 连接适配成 handleQuery 需要的 net.PacketConn。
// 语义映射：WriteTo(resp, addr) = 成帧写 resp（addr 忽略：TCP 无 per-包地址）；
// ReadFromUDP 侧不用；读在 serveConn 里做，ReadFrom 仅实现接口以零值报错。
type streamConn struct {
	conn net.Conn
	once sync.Once
}

// WriteTo 实现 net.PacketConn：把应答加 2 字节前缀写给这条连接。
// client 参数保留（handleQuery 的签名统一），调用方传的是连接的 RemoteAddr。
func (s *streamConn) WriteTo(resp []byte, _ net.Addr) (int, error) {
	// 超过 65535 的应答：TCP 无法成帧（前缀只有 2 字节），按 RFC 1035 §4.2.2
	// 截断语义处理。置 TC 位丢多余 RR 不现实（无法就地缩报文），
	// 这里选择丢弃整个应答并回写错误，日志层可见；DNS over TCP 的实践是
	// 上游应答本就不该超 64K（UDP 侧 TruncConn 已截断到 UDP 限内）。
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

// ReadFrom 实现接口占位：TCP 读循环在 serveConn，handleQuery 不从 pc 读。
func (s *streamConn) ReadFrom(p []byte) (int, net.Addr, error) {
	return 0, nil, fmt.Errorf("tcp streamConn: ReadFrom not used")
}

// Close 幂等关连接。
func (s *streamConn) Close() error {
	var err error
	s.once.Do(func() { err = s.conn.Close() })
	return err
}
func (s *streamConn) LocalAddr() net.Addr  { return s.conn.LocalAddr() }
func (s *streamConn) RemoteAddr() net.Addr { return s.conn.RemoteAddr() }

func (s *streamConn) SetDeadline(t time.Time) error      { return s.conn.SetDeadline(t) }
func (s *streamConn) SetReadDeadline(t time.Time) error  { return s.conn.SetReadDeadline(t) }
func (s *streamConn) SetWriteDeadline(t time.Time) error { return s.conn.SetWriteDeadline(t) }
