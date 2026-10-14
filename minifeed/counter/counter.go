// Package counter：互动计数（阅读/点赞/收藏同构，biz+bizId 标识任意资源）。
// 素材对照（W07-1 K5-K10）：IncrReadCnt 先 DB 后缓存；原子自增不能"读出加一写回"；
// minifeed 无 Redis，MemoryCounter 锁内 ++ 是 HIncrBy 的单机等价物；刷库 ticker 模拟批量消费。
package counter

import (
	"sync"
	"time"
)

// Counter 互动计数最小接口（biz: "post"/"comment"...，id: 资源 ID）。
// Like/Unlike 幂等语义由实现保证；Count 只读。
type Counter interface {
	Like(biz string, id int64) error
	Unlike(biz string, id int64) error
	Count(biz string, id int64) (int64, error)
}

// MemoryCounter：进程内 map 计数 + 定时刷库（模拟 W07-2 K25 的消费端凑批：
// 单条请求只动内存，ticker 到点把脏数据一次落库——批量一次提交比逐条快一个量级）。
type MemoryCounter struct {
	mu      sync.Mutex
	counts  map[string]int64 // key: biz:id
	dirty   map[string]struct{}
	flushFn func(biz string, id int64, cnt int64) error // 注入落库函数（测试换 fake）
	ticker  *time.Ticker
	done    chan struct{}
}

func NewMemoryCounter(flush func(biz string, id int64, cnt int64) error, every time.Duration) *MemoryCounter {
	m := &MemoryCounter{
		counts:  map[string]int64{},
		dirty:   map[string]struct{}{},
		flushFn: flush,
		done:    make(chan struct{}),
	}
	if flush != nil { // flush 为 nil 表示纯内存模式（不落库），不启 ticker
		m.ticker = time.NewTicker(every)
		go m.loop()
	}
	return m
}

func counterKey(biz string, id int64) string {
	return biz + ":" + itoa(id)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Like 计数 +1。锁内整数自增即原子（W07-1 K6：不能读出加一写回，这里天然没有该竞态）。
func (m *MemoryCounter) Like(biz string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := counterKey(biz, id)
	m.counts[k]++
	m.dirty[k] = struct{}{}
	return nil
}

// Unlike 计数 -1，下界 0（教学保护：正常业务 Unlike 前有"谁赞过"记录校验，W07-1 K11）。
func (m *MemoryCounter) Unlike(biz string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := counterKey(biz, id)
	if m.counts[k] > 0 {
		m.counts[k]--
		m.dirty[k] = struct{}{}
	}
	return nil
}

func (m *MemoryCounter) Count(biz string, id int64) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[counterKey(biz, id)], nil
}

// loop 刷库循环：到点把脏 key 批量交给 flushFn（W07-2 K26：事务内 for 逐条 + 一次提交，
// minifeed 单次 Exec 每条，凑批的意义在"ticker 到点才落"，不在单条 SQL 数）。
func (m *MemoryCounter) loop() {
	for {
		select {
		case <-m.ticker.C:
			m.flush()
		case <-m.done:
			return
		}
	}
}

func (m *MemoryCounter) flush() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.dirty {
		biz, id := splitKey(k)
		if err := m.flushFn(biz, id, m.counts[k]); err != nil {
			continue // 落库失败保留 dirty，下轮重试（K5：部分失败不解决，计数可容错）
		}
		delete(m.dirty, k)
	}
}

// Close 停刷库循环并做最后一次 flush（优雅关停语义，p5-deepsearch §优雅关停）。
func (m *MemoryCounter) Close() error {
	if m.ticker != nil {
		m.ticker.Stop()
		close(m.done)
		m.flush()
	}
	return nil
}

func splitKey(k string) (string, int64) {
	for i := len(k) - 1; i >= 0; i-- {
		if k[i] == ':' {
			return k[:i], atoi(k[i+1:])
		}
	}
	return k, 0
}

func atoi(s string) int64 {
	var v int64
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0
		}
		v = v*10 + int64(s[i]-'0')
	}
	return v
}
