// P34 扩展：测试/冒烟用的最小 DNS over TCP 客户端。
// 只做"TC 回退"那一跳：发一帧、收一帧，不是完整 resolver
// （不重试、不并发、不解析应答体，解析在 wire 包）。
package tcp

import (
	"fmt"
	"net"
	"time"
)

// Client 一条 TCP 连接上的最小 DNS 客户端。
type Client struct {
	conn    net.Conn
	Timeout time.Duration // 单次读写超时；0 = 2s
}

// Dial 建连。addr 形如 "127.0.0.1:5353"。
func Dial(addr string) (*Client, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, Timeout: 2 * time.Second}, nil
}

// RoundTrip 发一帧 query、收一帧应答，返回应答正文（不含前缀）。
// 半包/粘包由 DecodeFrame 处理：读侧按帧切开，多余字节留在连接里供下一次。
func (c *Client) RoundTrip(query []byte) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	_ = c.conn.SetDeadline(time.Now().Add(timeout))
	frame, err := EncodeFrame(query)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(frame); err != nil {
		return nil, fmt.Errorf("tcp client write: %w", err)
	}
	resp, err := DecodeFrame(c.conn)
	if err != nil {
		return nil, fmt.Errorf("tcp client read: %w", err)
	}
	return resp, nil
}

// Close 关连接。
func (c *Client) Close() error { return c.conn.Close() }
