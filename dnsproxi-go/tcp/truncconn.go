// P34 扩展：UDP 应答截断（RFC 1035 §4.2.1 的 TC 位）。
// 只拦出站 WriteTo：应答超过 UDP 单包上限就截到安全长度并置 TC=1，
// 客户端见到 TC 自己回退 TCP。UDP 收包路径零改动（只包 WriteTo）。
package tcp

import (
	"net"
	"time"
)

// TruncConn 包一个 UDP PacketConn，把超限应答截断并置 TC 位。
// 用法：handleQuery 收到的 pc 换成 WrapTrunc(pc, limit) 的返回值，
// 其余路径（ReadFromUDP 循环、上游 socket）原样。
type TruncConn struct {
	pc    net.PacketConn
	limit int
}

// WrapTrunc 返回截断版 PacketConn。limit 是应答字节上限（含 DNS 头）。
// 惯例取 512（RFC 1035 对 UDP 报文的历史上限）；EDNS 由客户端自己带 OPT，本代理不解析。
func WrapTrunc(pc net.PacketConn, limit int) *TruncConn {
	if limit <= 0 {
		limit = 512
	}
	return &TruncConn{pc: pc, limit: limit}
}

// WriteTo 实现 net.PacketConn：超限时截断 + 置 TC。
// 截断语义对齐 RFC 1035 §4.2.1 与 BIND 的行为：保留 12 字节头 + 完整
// question（问题节必然在应答里原样带回），question 之后的所有 RR 舍弃；
// ANCOUNT/NSCOUNT/ARCOUNT 三个计数清零，TC 位（RFC 1035 §4.1.1 flags
// 高字节 bit 1）置 1。小于 limit 原样直写，零拷贝。
func (t *TruncConn) WriteTo(resp []byte, addr net.Addr) (int, error) {
	if len(resp) <= t.limit {
		return t.pc.WriteTo(resp, addr)
	}
	hdr := 12
	if len(resp) < hdr {
		return t.pc.WriteTo(resp, addr) // 理论不可达：handleQuery 已挡 <12
	}
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
		// question 本身就超过 limit：退回复制到 limit（保 12 字节头），
		// 计数照样清零，TC 位已足够客户端回退。
		keep = t.limit
	}
	out := make([]byte, keep)
	copy(out, resp[:keep])
	out[2] |= 0x02 // TC 位
	out[6], out[7] = 0, 0
	out[8], out[9] = 0, 0
	out[10], out[11] = 0, 0
	return t.pc.WriteTo(out, addr)
}

// ReadFrom 透传：收包路径与截断无关。
func (t *TruncConn) ReadFrom(p []byte) (int, net.Addr, error) { return t.pc.ReadFrom(p) }
func (t *TruncConn) Close() error                             { return t.pc.Close() }
func (t *TruncConn) LocalAddr() net.Addr                      { return t.pc.LocalAddr() }
func (t *TruncConn) SetDeadline(d time.Time) error            { return t.pc.SetDeadline(d) }
func (t *TruncConn) SetReadDeadline(d time.Time) error        { return t.pc.SetReadDeadline(d) }
func (t *TruncConn) SetWriteDeadline(d time.Time) error       { return t.pc.SetWriteDeadline(d) }
