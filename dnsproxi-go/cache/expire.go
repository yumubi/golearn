package cache

// G5：主动老化（对照你 Rust 版 D6，Redis activeExpireCycle + serverCron）。
//
// 骨架抄 Redis：定时器每 1/hz 秒醒一次，一个周期里跑若干轮，每轮随机抽
// sample 个 key 当场判当场删；本轮过期比例还高（>=Pct）就再来一轮，
// 直到墙钟预算用完。

//
// 让位对象的差异（你 Rust 课文 §8.4 的原话）：
//   Redis 是单线程，每 16 次迭代查一次时钟是否超预算，超了 break，
//   把控制权【交还事件循环】——让出的是时间片。
//   我们是多线程 + 锁，每轮结束就把写锁还回去，让排队的查询插进来
//   ——让出的是【锁】。CycleRound 的锁边界不是代码风格，是延迟设计。
//
// 【为什么必须一次抽一个，不能先攒一批下标再逐个删】
// removeSlot 内部是 keys[slot] = keys[last]：把最后一个条目搬进那个洞。
// 只要删掉第一个，先前记下的其余下标就全都指向别人了——不是 panic，
// 是【静默删活人】。这类 bug 不会有任何报错，只会让命中率莫名其妙地掉。
// （你 Rust 版 expire_round 注释原文，Go 版同罪同罚。）

import "time"

// ExpireCfg 主动老化参数（对齐你 Rust 版的 env 默认值）。
type ExpireCfg struct {
	HZ       int           // 每秒醒几次（Redis CONFIG_DEFAULT_HZ=10，合法 1..500）
	Sample   int           // 每轮抽几个（Redis KEYS_PER_LOOP=20；0 = 抽"表长"次）
	Pct      int           // 样本过期比例超过它就再来一轮
	Budget   time.Duration // 单个周期墙钟预算（Redis FAST_DURATION=1ms）
	Exchange bool          // [expire] 日志开关
}

// DefaultExpireCfg 默认值考据（你 Rust :846-874 的结论）：
//   - Pct 用 10 不用 25：25 是 ACTIVE_EXPIRE_CYCLE_SLOW_TIME_PERC（CPU 预算百分比），
//     现行 Redis 的判据是 ACCEPTABLE_STALE=10。坊间的 25% 是两个常量张冠李戴。
//   - Sample=20：KEYS_PER_LOOP 从 4.0 到今天没改过。0 = 抽表长次（不是全表扫描：
//     有放回抽 N 次只覆盖约 63% 的格子，1-1/e）。
//   - HZ 上限 500：>1000 时 1000/hz 整数除法得 0，ticker 直接炸。
func DefaultExpireCfg() ExpireCfg {
	return ExpireCfg{HZ: 10, Sample: 20, Pct: 10, Budget: time.Millisecond}
}

// ExpireRound 一轮：随机抽 n 个 slot，当场判当场删。
// 返回 (checked, removed)。写锁的边界就是这一轮——锁在这里还回去。
func (c *Cache) ExpireRound(n int) (checked, removed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n <= 0 {
		n = len(c.keys)
	}
	for i := 0; i < n; i++ {
		live := len(c.keys)
		if live == 0 {
			break // randomBelow 空表会除零，先挡住（Rust :713 同款）
		}
		slot := c.randomBelow(live)
		checked++
		if c.m[c.keys[slot]].Expired() {
			c.removeSlot(slot)
			removed++
		}
	}
	c.ExpiredActive += uint64(removed)
	c.ExpireChecked += uint64(checked)
	return checked, removed
}

// ExpireCycle 一个完整周期：多轮直到过期比例低于阈值或预算耗尽。
// 返回 (rounds, checkedTotal, removedTotal, took)。
//
// 每轮之间锁完全释放（ExpireRound 返回即释放），排队的查询能插进来。
func (c *Cache) ExpireCycle(cfg ExpireCfg) (rounds int, checkedTotal, removedTotal int, took time.Duration) {
	start := time.Now()
	for {
		checked, removed := c.ExpireRound(cfg.Sample)
		rounds++
		checkedTotal += checked
		removedTotal += removed
		if checked == 0 {
			break // 空表
		}
		// Redis 的 25%：本轮样本里过期比例还高，说明尸体还多，再来一轮。
		if removed*100 < checked*cfg.Pct {
			break
		}
		// 没有这行，一次雪崩式过期就会把写锁攥到天亮（课文 §8.4 的反例）。
		if took = time.Since(start); took >= cfg.Budget {
			break
		}
	}
	c.ExpireCycles++
	c.ExpireRounds += uint64(rounds)
	c.ExpireUs += uint64(time.Since(start).Microseconds())
	return rounds, checkedTotal, removedTotal, time.Since(start)
}

// RunExpirer 老化循环：每 1/HZ 秒一个周期，直到 ctx 取消。
// 返回给调用方的 channel 每周期发一次统计（测试/日志用）。
func (c *Cache) RunExpirer(done <-chan struct{}, cfg ExpireCfg) <-chan ExpireStat {
	stats := make(chan ExpireStat, 1)
	interval := time.Second / time.Duration(cfg.HZ)
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		defer close(stats)
		for {
			select {
			case <-done:
				return
			case <-t.C:
				rounds, checked, removed, took := c.ExpireCycle(cfg)
				select {
				case stats <- ExpireStat{Rounds: rounds, Checked: checked, Removed: removed, Took: took}:
				default: // 消费者不在就丢——老化不能被观测拖慢
				}
			}
		}
	}()
	return stats
}

// ExpireStat 一个周期的账（日志/测试断言用）。
type ExpireStat struct {
	Rounds  int
	Checked int
	Removed int
	Took    time.Duration
}

// Ghosts 当前有多少条已过期却还占着格子（全表扫的调试量表，生产别这么干）。
// 没有它，"主动老化其实没跑起来"会是一次安静的失效（你 Rust :995 注释）。
func (c *Cache) Ghosts() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n := 0
	for _, e := range c.m {
		if e.Expired() {
			n++
		}
	}
	return n
}
