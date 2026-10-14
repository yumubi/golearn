// Package rank：热帖 TopN。定时算分装填，读侧 atomic.Value 零锁（W09-1 K17/K18/K21：
// 本地缓存数据只有一份不需要 key-value；读序本地→Redis→DB 的两层 minifeed 版=内存→DB；
// Redis 永不过期的等价语义=刷新失败继续用旧榜）。
package rank

import (
	"sync"
	"sync/atomic"
	"time"
)

// Scored 一条榜单项（Content 换 Abstract 不缓存全文——W09-1 K12 缓存字段纪律）。
type Scored struct {
	PostID int64
	Title  string
	Score  float64
}

// Source 榜单数据源（算分所需的原始数据：minifeed 直接从 PostStore 拉全量+计数）。
type Source interface {
	Posts() []Scored // 返回候选集（生产是分批 ListPub + GetByIds，W09-1 K9）
}

type rankCache struct {
	items []Scored
	at    int64 // 装填时间戳（UnixMilli）
}

// Rank 本地缓存榜单：读 Top 零锁，Refresh 单飞（进程内 TryLock，多副本才需要 SetNX——W09-2 K3）。
type Rank struct {
	src  Source
	ttl  int64
	data atomic.Value // rankCache
	mu   sync.Mutex   // Refresh 单飞：并发触发只有 1 个真算（K3 的单机等价物）
}

func New(src Source, ttlMillis int64) *Rank {
	return &Rank{src: src, ttl: ttlMillis}
}

// Refresh 算分并装填。已有未过期数据时直接返回（幂等）；并发调用由 mu 单飞。
// 失败保留旧榜继续服务（K19：刷新失败不回错误，降级用"过期"数据）。
func (r *Rank) Refresh() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UnixMilli()
	if c, ok := r.data.Load().(rankCache); ok && len(c.items) > 0 && now-c.at < r.ttl {
		return // 有数据且未过期：重复触发跳过（W21-1 K25）；空榜不算有效缓存，允许重装
	}
	items := r.src.Posts()
	sortScored(items)
	r.data.Store(rankCache{items: items, at: now})
}

// Top 返回当前榜单前 n 条。缓存为空返回空切片（调用方触发一次 Refresh 的责任在组装层）。
func (r *Rank) Top(n int) []Scored {
	c, _ := r.data.Load().(rankCache)
	if n > len(c.items) {
		n = len(c.items)
	}
	return c.items[:n]
}

// sortScored 按 Score 降序（小顶堆 cap=N 是 W09-1 K9 的生产形态，minifeed 候选集小直接排序）。
func sortScored(items []Scored) {
	for i := range len(items) {
		for j := i + 1; j < len(items); j++ {
			if items[j].Score > items[i].Score {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}
