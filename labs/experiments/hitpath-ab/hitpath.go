// Package hitpathab 实验 4：dnsproxi-go 命中路径省拷贝 A/B。
//
// P6 LESSON 遗留的现成优化点（LESSON.md 读数 2）：命中路径 Go 版比 Rust 版
// 慢 2×（B2 档 9.9k vs 19.4k qps，当年 keyspace 更大数字偏低），
// 怀疑是 Reply() 的 make+copy 整包拷贝。
//
// A（baseline）: Reply() 内部 make([]byte, len)+copy，返回新切片
// B（hitpath）  : caller 预取 sync.Pool 的 4096B buffer，Reply 写进去，
//
//	WriteTo 后 Put 回——单次拷贝（e.Resp→pool buf），
//	消掉每命中一次的堆分配。
//
// Rust 对照：原版命中路径是 Arc 读锁 + 内存拷贝一次——Go 版补齐后应同档。
package hitpathab

import (
	"encoding/binary"
	"sync"
)

// MaxResp 应答包上限（与 main.go 的 4096 一致）。
const MaxResp = 4096

// BufPool 命中路径的应答 buffer 池。
// ⚠️ 并发语义：Put 回去之前 WriteTo 必须已完成（本实验在 handleQuery
// 同一 goroutine 内 Put，天然安全）；跨 goroutine 复用会造成 TTL 撕裂——
// Rust 里 &mut 独占由类型系统保证，Go 这里靠"用完即还、还前写完"的纪律。
var BufPool = sync.Pool{
	New: func() any { b := make([]byte, MaxResp); return &b },
}

// BuildReplyB 是 Reply 的 pool 变体：写进 out（必须 ≥ len(e.Resp)），
// 返回切片 out[:n]；n=0 表示过期/不可用。
// 与 cache.Entry.Reply 的语义逐行对照（见 cache/reply.go:12-43）。
func BuildReplyB(
	resp []byte, atAge uint32, minTTL uint32, ttls []TTLField,
	query []byte, qEnd int, out []byte,
) []byte {
	if atAge >= minTTL {
		return nil
	}
	if len(out) < len(resp) {
		return nil
	}
	out = out[:len(resp)]
	copy(out, resp)
	if len(query) >= 2 {
		out[0] = query[0]
		out[1] = query[1]
	}
	if qEnd <= len(out) && qEnd <= len(query) {
		copy(out[12:qEnd], query[12:qEnd])
	}
	for _, t := range ttls {
		rest := t.TTL - atAge
		if t.TTL < atAge {
			rest = 0
		}
		binary.BigEndian.PutUint32(out[t.Off:t.Off+4], rest)
	}
	return out
}
