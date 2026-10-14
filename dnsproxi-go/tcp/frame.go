// Package tcp 实装 RFC 1035 §4.2.2 的 DNS over TCP：2 字节大端长度前缀成帧。
// P34 扩展项（PLAN-OMP-3 S4）：全部新增文件，默认关闭，不碰 UDP 路径。
package tcp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// MaxMsgLen 是长度前缀的硬上限。前缀是 uint16，天然封顶 65535；
// RFC 1035 §4.2.2 用两字节前缀表达同一条上限。
const MaxMsgLen = 0xFFFF

// ErrTooLarge 超过单帧上限（发侧拒绝编码；收侧当作断连理由）。
var ErrTooLarge = errors.New("tcp frame: message exceeds 65535 bytes")

// EncodeFrame 加 2 字节大端长度前缀。msg 长度超 65535 返回 ErrTooLarge。
// 返回值是新分配的帧（msg 不被改写），调用方一次 Write 到位。
func EncodeFrame(msg []byte) ([]byte, error) {
	if len(msg) > MaxMsgLen {
		return nil, fmt.Errorf("%w: got %d bytes", ErrTooLarge, len(msg))
	}
	out := make([]byte, 2+len(msg))
	binary.BigEndian.PutUint16(out[:2], uint16(len(msg)))
	copy(out[2:], msg)
	return out, nil
}

// DecodeFrame 从 r 读一帧：先读 2 字节前缀，再按前缀读满正文。
//
// 防御（fuzz 目标函数，非法输入一律 error 不 panic）：
//   - 前缀读不满（对端半开/EOF）→ io.EOF 或 io.ErrUnexpectedEOF 原样上抛；
//   - 正文读不满（前缀大于实际内容）→ io.ErrUnexpectedEOF；
//   - 前缀 0：RFC 未禁止空查询，按协议原样接受（返回 0 字节、nil 错误），
//     业务层拿到的 query 过 12 字节头校验自然丢弃（handleQuery 的 short 分支）。
//
// 返回的 buf 是新分配的，长度恰为帧正文长度。
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
	n := binary.BigEndian.Uint16(prefix[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		// n==0 时 ReadFull 不读直接 nil，天然通过。
		return nil, fmt.Errorf("tcp frame: read %d-byte body: %w", n, err)
	}
	return buf, nil
}
