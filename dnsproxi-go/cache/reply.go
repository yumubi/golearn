package cache

import (
	"encoding/binary"
	"sync"
)

// 关键语义：
//   - age >= MinTTL → 过期，返回 nil（Rust 版返回 None 同款）
//   - 应答里的 question 段必须和当前 query 的一样长（指针偏移不变的前提），
//     不然 TTL 偏移全体错位 → 直接判不可用
//   - 每条 TTL 原地减 age（saturating：不为负）
func (e *Entry) Reply(query []byte) []byte {
	age := e.AgeSecs()
	if age >= e.MinTTL {
		return nil
	}
	q, err := ParseQuestion(query)
	if err != nil {
		return nil
	}
	rq, err := ParseQuestion(e.Resp)
	if err != nil {
		return nil
	}
	// question 段长度不同 = 压缩指针偏移失效（缓存里的名字编码和查询不一致）。
	if q.End != rq.End {
		return nil
	}

	out := make([]byte, len(e.Resp))
	copy(out, e.Resp)
	out[0] = query[0]
	out[1] = query[1]
	copy(out[12:q.End], query[12:q.End])
	for _, t := range e.TTLs {
		rest := t.TTL - age
		if t.TTL < age {
			rest = 0
		}
		binary.BigEndian.PutUint32(out[t.Off:t.Off+4], rest)
	}
	return out
}

// BufPool 是 ReplyPool 用的 4KB 应答缓冲池（labs 实验 4 的 B 形态）。
// 并发契约：Put 回池前 WriteTo 必须已完成——handleQuery 同 goroutine
// 用完即还，天然安全；跨 goroutine 复用会 TTL 撕裂。
var BufPool = sync.Pool{
	New: func() any { b := make([]byte, 4096); return &b },
}

// ReplyPool 是 Reply 的零分配变体（labs/experiments/hitpath-ab 的 B 形态）：
// 语义与 Reply 逐行一致，但写进池化 buffer，命中路径不再有堆分配。
// 用法：bufp := BufPool.Get().(*[]byte); resp := e.ReplyPool(query, *bufp);
// WriteTo 后 BufPool.Put(bufp)。
func (e *Entry) ReplyPool(query, out []byte) []byte {
	age := e.AgeSecs()
	if age >= e.MinTTL {
		return nil
	}
	q, err := ParseQuestion(query)
	if err != nil {
		return nil
	}
	rq, err := ParseQuestion(e.Resp)
	if err != nil {
		return nil
	}
	if q.End != rq.End {
		return nil
	}
	if len(out) < len(e.Resp) {
		return nil
	}
	out = out[:len(e.Resp)]
	copy(out, e.Resp)
	out[0] = query[0]
	out[1] = query[1]
	copy(out[12:q.End], query[12:q.End])
	for _, t := range e.TTLs {
		rest := t.TTL - age
		if t.TTL < age {
			rest = 0
		}
		binary.BigEndian.PutUint32(out[t.Off:t.Off+4], rest)
	}
	return out
}
