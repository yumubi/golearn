// Package cache 是 go 版 dnsproxi 的 D2 应答缓存。
// 对照你 Rust 版 dnsproxi/src/main.rs 的 Entry/CacheStore（D2 档：无逐出、无准入）。
//
// 语义对齐（D2 验收场景）：
//   - 键 = (qname 小写, qtype)
//   - 值 = 原始应答字节 + 各 RR 的 (TTL偏移, 原TTL) + min TTL
//   - 命中 = 未过期（age < minTTL）；回包时 ID 替换 + 每条 TTL 原地减 age
//   - 只缓存普通 RD=1 单问题 IN 查询（EDNS/多问题走转发）
package cache

import (
	"sync"
	"time"
)

// TTLField 记录应答报文里一条 RR 的 TTL 位置与原值，倒计时靠它原地改写。
// 对照 Rust 的 ttls: Vec<(usize, u32)>。
type TTLField struct {
	Off int
	TTL uint32
}

// Entry 一条缓存应答。对照 Rust struct Entry（去掉 last_access/slot，那是 D3/D4 的事）。
type Entry struct {
	Resp   []byte
	At     time.Time
	TTLs   []TTLField
	MinTTL uint32 // 整条的过期窗口 = 各 RR TTL 的最小值（秒）
}

// AgeSecs 已缓存的秒数。过期判据唯一来源（Rust 版 age_secs/expired 同款收敛）。
func (e *Entry) AgeSecs() uint32 {
	return uint32(time.Since(e.At).Seconds())
}

// Expired 是否过期。
func (e *Entry) Expired() bool { return e.AgeSecs() >= e.MinTTL }

// Key 缓存键。
type Key struct {
	Name string // 已转小写
	Type uint16
}

// Cache 应答缓存。读多写少 → sync.RWMutex（对照你 Rust 版 RwLock，L3 决策树同款）。
//
// G3 字段：
//   - keys/slot：slot 反查表，swap_remove 逐出的 O(1) 随机索引（Rust 版 keys+Entry.slot）
//   - access：key → 最后访问 ms（近似 LRU 的排序键；Rust 版是 Entry.last_access AtomicU64，
//     Go 版锁内访问无需原子）
//   - max/SampleK/Seed：容量上限、样本数（0=全表）、RNG 种子（可复跑）
type Cache struct {
	mu sync.RWMutex
	m  map[Key]*Entry

	keys   []Key
	slot   map[Key]int
	access map[Key]uint64

	max     int    // 0 = 无界
	SampleK int    // 0 = 全表扫描
	Seed    uint64 // xorshift64* 种子；0 → 固定非零
	rng     uint64

	// G4：TinyLFU 准入。nil = off（D3/D4 来者不拒）。
	Sketch *FreqSketch

	// 决策账（对照 Rust evictions/examined/admits/rejects）。
	Evictions uint64
	Examined  uint64
	Admits    uint64
	Rejects   uint64

	// G5 主动老化账（对照 Rust expired_active/expire_*）。
	ExpiredActive uint64 // 主动老化删掉的尸体数
	ExpireChecked uint64 // 累计检查数
	ExpireCycles  uint64 // 周期数
	ExpireRounds  uint64 // 轮数
	ExpireUs      uint64 // 累计耗时（微秒）
}

// NowMs 近似 LRU 的逻辑时钟。默认墙钟；测试可替换成假时钟。
var NowMs = func() uint64 { return uint64(time.Now().UnixMilli()) }

// New 建空缓存（无界，D2 语义）。
func New() *Cache { return NewCap(0, 0, 0) }

// NewCap 建带容量/采样配置的缓存。max=0 无界；sampleK=0 全表；seed=0 固定种子。
func NewCap(max, sampleK int, seed uint64) *Cache {
	if seed == 0 {
		seed = 0x9E3779B97F4A7C15 // xorshift 零状态出不来（Rust 版同款映射）
	}
	return &Cache{
		m:       map[Key]*Entry{},
		slot:    map[Key]int{},
		access:  map[Key]uint64{},
		max:     max,
		SampleK: sampleK,
		Seed:    seed,
		rng:     seed,
	}
}

// Get 返回未过期条目；过期条目视同不存在（惰性过期，D2 档不主动删）。
// 命中时更新 last_access（近似 LRU）：Rust 版在读锁下 fetch_max 原子写；
// Go 的 map 无原子，命中路径升级写锁。代价：热路径从 RLock 变 Lock——
// 读多写少的优势打了个折，这正是你 Rust 课文里"严格 LRU 要每读更新顺序
// → 读变写"难题的 Go 实相：没有原子可借时，只能付这个代价。
func (c *Cache) Get(k Key) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || e.Expired() {
		return nil, false
	}
	c.access[k] = NowMs()
	return e, true
}

// Put 插入/刷新（无界，D2 语义，测试用）。
func (c *Cache) Put(k Key, e *Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.m[k]; !exists {
		c.keys = append(c.keys, k)
		c.slot[k] = len(c.keys) - 1
	}
	c.m[k] = e
	c.access[k] = NowMs()
}

// Len 当前条目数（含已过期但未清除的——"惰性静置后全是尸体"的量纲，D6 会用到）。
func (c *Cache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}
