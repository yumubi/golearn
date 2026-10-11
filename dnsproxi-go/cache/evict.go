package cache

// G3：容量上限 + sample-K 逐出（对照你 Rust 版 CacheStore 的 keys+slots）。
//
// 设计对齐：
//   - keys []Key + slot map[Key]int —— Rust 用 Entry.slot 字段，Go 用反向 map，
//     语义相同：swap_remove 后 O(1) 修正被换位条目的 slot
//   - xorshift64* 与 Rust 版同款常数，Seed 固定 → 实验可复跑
//   - SampleK=0：全表扫最冷者（D3 对照）；K>0：随机抽 K 条取最冷者（D4）
//   - 排序键 lastAccess（ms 逻辑时钟），D6 的 expired_first 留到 D6

// Eviction 一次逐出的记录（日志/测试断言用）。
type Eviction struct {
	Key     Key
	Checked int    // 这次决策检查了几条（成本账）
	Policy  string // full-lru / sample-lru / expired
}

// randomBelow xorshift64*，与 Rust 版同款常数。零状态出不来：0 映射到固定种子。
func (c *Cache) randomBelow(upper int) int {
	x := c.rng
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	c.rng = x
	return int((x * 0x2545F4914F6CDD1D) % uint64(upper))
}

// sampleIndices Floyd 抽样：[0,len) 均匀抽 k 个不重复下标（Rust 版同款）。
func (c *Cache) sampleIndices(k int) []int {
	n := len(c.keys)
	k = min(k, n)
	out := make([]int, 0, k)
	for j := n - k; j < n; j++ {
		cand := c.randomBelow(j + 1)
		dup := false
		for _, v := range out {
			if v == cand {
				dup = true
				break
			}
		}
		if dup {
			out = append(out, j)
		} else {
			out = append(out, cand)
		}
	}
	return out
}

// removeSlot keys.swap_remove + slot 反查修正（Rust 版 remove_slot 同款）。
// 三张表（keys/slot/m）一起动：漏一张就是静默漂移——第一版就漏了 m，
// len=4 keys=3 m=4，容量上限直接失效（被测试当场抓住）。
func (c *Cache) removeSlot(slot int) Key {
	victim := c.keys[slot]
	last := len(c.keys) - 1
	c.keys[slot] = c.keys[last]
	c.keys = c.keys[:last]
	delete(c.slot, victim)
	delete(c.m, victim)
	delete(c.access, victim)
	if slot < len(c.keys) {
		c.slot[c.keys[slot]] = slot
	}
	return victim
}

// pickVictim 只挑不删（D5 要先比频率，接口从现在就拆开）。
// 返回 (slot, checked, policy)；空表返回 slot=-1。
// 挑中者是尸体（已过期）时 policy 标 "expired"——主路径据此让它在
// 准入里无条件输（不能让死人替在位者守门，课文 §5）。
// 你 Rust 版用 DNS_CACHE_VICTIM_EXPIRED_FIRST 旋钮控制"尸体优先"，
// 这里简化成始终检查挑中者死活（排序仍纯 last_access，D6 再上字典序）。
func (c *Cache) pickVictim() (int, int, string) {
	if len(c.keys) == 0 {
		return -1, 0, ""
	}
	// 统一成"候选下标集合"：全表 or 一次抽样。只调一次 sampleIndices——
	// 两次调用会抽到两个不同样本（RNG 前进），下标换算全乱。第一版就栽在这。
	idxs := make([]int, len(c.keys))
	for i := range idxs {
		idxs[i] = i
	}
	policy := "full-lru"
	if c.SampleK != 0 && c.SampleK < len(c.keys) {
		idxs = c.sampleIndices(c.SampleK)
		policy = "sample-lru"
	}
	best, bestAt := -1, uint64(0)
	for _, i := range idxs {
		at := c.access[c.keys[i]]
		if best == -1 || at < bestAt {
			best, bestAt = i, at
		}
	}
	if best == -1 {
		return -1, 0, ""
	}
	if c.m[c.keys[best]].Expired() {
		return best, len(idxs), "expired"
	}
	return best, len(idxs), policy
}

// Rejected 被准入挡在门外的记录（缓存内容一字未动）。
type Rejected struct {
	Key        Key
	Cand, Vict uint32
}

// PutFull 带容量上限 + 准入的插入。满仓 + 新键才决策；在位键刷新绝不逐出
// （对照 Rust :1150 注释：刷新的键本来就是在位者，不能被准入挡掉）。
// 返回 (逐出记录, 拒绝记录)，都 nil = 直接插入。
//
// D5 判决顺序（对照 Rust :1160-1192）：
//  1. sketch == nil → 来者不拒（D3/D4 行为）
//  2. 受害者是尸体（policy=="expired"）→ 不比频率直接让位
//     （否则"死人靠生前热度守门"，课文 §5）
//  3. 比频率：cand > vict 进；平/输但 cand>=6 → 1/128 随机放行
//  4. 拒了 → 缓存一字未动，rejects++（examined 也记：拒绝也花了 checked 次比较）
//
// examined 口径与 Rust 对齐：分母 = 决策次数（逐出 + 拒绝），未满插入不记账。
func (c *Cache) PutFull(k Key, e *Entry) (*Eviction, *Rejected) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.m[k]; exists {
		// 在位刷新：替换内容 + 更新 last_access，keys 不重复登记。
		c.m[k] = e
		c.access[k] = NowMs()
		return nil, nil
	}

	// 新键：满仓才需要决策。
	if c.max > 0 && len(c.keys) >= c.max {
		slot, checked, policy := c.pickVictim()
		if slot < 0 {
			// 理论不可达（len>=max>0 则表非空），防御。
			c.Rejects++
			return nil, nil
		}
		victimKey := c.keys[slot]
		admit := true
		var candF, victF uint32
		if c.Sketch != nil && policy != "expired" {
			candF = c.Sketch.Frequency(k)
			victF = c.Sketch.Frequency(victimKey)
			// 随机放行用缓存自己的 xorshift，种子可固定 → 实验可复跑（Rust :1178 同款）。
			roll := uint64(c.randomBelow(1 << 16))
			admit = Admit(candF, victF, roll)
		}
		if !admit {
			c.Rejects++
			c.Examined += uint64(checked)
			// 被拒不影响应答：客户端照样拿到上游的答案，只是不留档。
			return nil, &Rejected{Key: k, Cand: candF, Vict: victF}
		}
		c.removeSlot(slot)
		c.Evictions++
		c.Examined += uint64(checked)
		c.Admits++
		c.insert(k, e)
		return &Eviction{Key: victimKey, Checked: checked, Policy: policy}, nil
	}

	c.insert(k, e)
	return nil, nil
}

func (c *Cache) insert(k Key, e *Entry) {
	c.keys = append(c.keys, k)
	c.slot[k] = len(c.keys) - 1
	c.m[k] = e
	c.access[k] = NowMs()
}
