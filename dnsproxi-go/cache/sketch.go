package cache

// G4：TinyLFU 准入的频率草图（对照你 Rust 版 FreqSketch，Caffeine 同款）。
//
// count-min sketch：SKETCH_ROWS 行 × width 个 4-bit 计数器，16 格打包进一个
// uint64。每行用不同的哈希把 key 打到一个格子，估计值 = 各行取 min。
// 碰撞只会把某一行抬高 → 取 min 之后仍可能高估，但【永不低估】——
// 宁可把冷名字误当热的放进来，也不能把真热点挡在门外。
//
// 为什么 4 bit 就够：准入只要"谁比谁热"的序，不要精确次数。15 封顶 +
// 定期整体减半 = 带老化的频率估计（没有老化就会被历史锁死）。

import (
	"hash/maphash"
	"sync/atomic"
)

const (
	SketchRows       = 4
	MinSketchSize    = 256
	AdmitHashdosThld = 6 // Caffeine 的 ADMIT_HASHDOS_THRESHOLD（真源码里就是 6）
)

// Caffeine 同款的减半/计数掩码。
const (
	nibbleHalveMask uint64 = 0x7777777777777777
	nibbleOneMask   uint64 = 0x1111111111111111
)

// sketchSeeds 每行混不同的种子再雪崩（Rust 版同款常数）。
var sketchSeeds = [SketchRows]uint64{
	0x9E3779B97F4A7C15,
	0xC2B2AE3D27D4EB4F,
	0x165667B19E3779F9,
	0x27D4EB2F165667C5,
}

// spread 拿同一个哈希混不同的种子再雪崩（Rust 版 spread 同款常数）。
func spread(h, seed uint64) uint64 {
	x := h ^ seed
	x *= 0xFF51AFD7ED558CCD
	x ^= x >> 33
	x *= 0xC4CEB9FE1A85EC53
	x ^= x >> 33
	return x
}

// keyHash 键哈希。maphash 随机进程种子——准入只看相对序，不需要跨进程稳定。
var hashSeed = maphash.MakeSeed()

func keyHash(k Key) uint64 {
	// 手写混入：maphash.String 返回的 h 已够散，再过一遍 spread 确保雪崩。
	return spread(maphash.String(hashSeed, k.Name)^uint64(k.Type), 0x9E3779B97F4A7C15)
}

// FreqSketch 频率草图 + 挡板。字段全是原子——增量发生在读路径热区，
// 和你 Rust 版"用原子把读变写挡在锁外"同款手法。
type FreqSketch struct {
	table     []atomic.Uint64
	rowWords  int
	widthMask uint64
	door      []atomic.Uint64
	doorMask  uint64
	size      atomic.Uint64
	sample    uint64
	aging     bool
	resets    atomic.Uint64
}

// NewSketch 建草图。尺寸按 Caffeine 折算：ceilPow2(max(cap,256)) 个 u64 =
// 16 倍容量的格子，深度 4 行。doorBPI = 挡板每项 bit 数（0 = 关挡板）。
// 关老化 = 频率变历史总账，演示"准入被历史锁死"用。
func NewSketch(capacity int, doorkeeper, aging bool, doorBPI int) *FreqSketch {
	base := capacity
	if base < MinSketchSize {
		base = MinSketchSize
	}
	// next power of two
	p := 1
	for p < base {
		p <<= 1
	}
	width := p * 4
	rowWords := width / 16
	sample := uint64(p) * 10 // Caffeine：sampleSize = 10 × maximum，累计到就整体减半
	doorBits := 0
	if doorkeeper {
		bpi := doorBPI
		if bpi < 1 {
			bpi = 1
		}
		d := sample * uint64(bpi)
		db := 1
		for db < int(d) {
			db <<= 1
		}
		doorBits = db
	}
	doorMask := uint64(0)
	if doorBits > 0 {
		doorMask = uint64(doorBits - 1)
	}
	return &FreqSketch{
		table:     make([]atomic.Uint64, rowWords*SketchRows),
		rowWords:  rowWords,
		widthMask: uint64(width) - 1,
		door:      make([]atomic.Uint64, doorBits/64),
		doorMask:  doorMask,
		sample:    sample,
		aging:     aging,
	}
}

func (s *FreqSketch) cell(row int, h uint64) (int, uint32) {
	idx := int(spread(h, sketchSeeds[row]) & s.widthMask)
	return row*s.rowWords + idx/16, uint32(idx%16) * 4
}

// doorMark 在挡板上打两个位；返回"之前就已经全置上了"（= 不是第一次见）。
// 没开挡板：所有增量直通草图。
func (s *FreqSketch) doorMark(h uint64) bool {
	if len(s.door) == 0 {
		return true
	}
	seen := true
	for _, seed := range [2]uint64{sketchSeeds[0], sketchSeeds[3]} {
		bit := int(spread(h, seed) & s.doorMask)
		word := &s.door[bit/64]
		mask := uint64(1) << (bit % 64)
		if word.Or(mask)&mask == 0 {
			seen = false
		}
	}
	return seen
}

func (s *FreqSketch) doorContains(h uint64) bool {
	if len(s.door) == 0 {
		return false
	}
	for _, seed := range [2]uint64{sketchSeeds[0], sketchSeeds[3]} {
		bit := int(spread(h, seed) & s.doorMask)
		if s.door[bit/64].Load()&(uint64(1)<<(bit%64)) == 0 {
			return false
		}
	}
	return true
}

// Increment 按请求流计数：命中也要数，调用点在查询入口不在 miss 分支。
// 第一次见到的名字只在挡板上留印子（DNS 流量最多就是一次性名字，
// 让它们止步于此，草图计数器留给真有复访的）。
func (s *FreqSketch) Increment(k Key) {
	h := keyHash(k)
	if !s.doorMark(h) {
		return
	}
	bumped := false
	for row := range SketchRows {
		w, shift := s.cell(row, h)
		word := &s.table[w]
		for {
			cur := word.Load()
			if (cur>>shift)&0xF == 0xF {
				break // 饱和
			}
			if word.CompareAndSwap(cur, cur+(uint64(1)<<shift)) {
				bumped = true
				break
			}
		}
	}
	if bumped && s.aging {
		n := s.size.Add(1)
		if n >= s.sample {
			s.Reset()
		}
	}
}

// Frequency 频率估计 = 各行取 min + 挡板见过的 +1。
func (s *FreqSketch) Frequency(k Key) uint32 {
	h := keyHash(k)
	mn := ^uint32(0)
	for row := range SketchRows {
		w, shift := s.cell(row, h)
		v := uint32((s.table[w].Load() >> shift) & 0xF)
		if v < mn {
			mn = v
		}
	}
	if s.doorContains(h) {
		mn++
	}
	return mn
}

// Reset 整体减半 = 老化。每个 4-bit 格子右移一位，0x77… 掩掉高位邻格借位；
// 顺带数奇数格修正 size（减半丢的是每个奇数格的最低位）。
func (s *FreqSketch) Reset() {
	var odd uint64
	for i := range s.table {
		prev := s.table[i].Swap((s.table[i].Load() >> 1) & nibbleHalveMask)
		odd += popcount64(prev & nibbleOneMask)
	}
	for i := range s.door {
		s.door[i].Store(0)
	}
	cur := s.size.Load()
	s.size.Store((cur - min64(cur, odd>>2)) >> 1)
	s.resets.Add(1)
}

// Occupancy 非零格子占比（可观测性：一次性名字有没有在啃计数器，看它）。
func (s *FreqSketch) Occupancy() (used, total int) {
	for i := range s.table {
		v := s.table[i].Load()
		for j := 0; j < 16; j++ {
			if (v>>(uint(j)*4))&0xF != 0 {
				used++
			}
		}
	}
	return used, len(s.table) * 16
}

// Resets 老化次数。
func (s *FreqSketch) Resets() uint64 { return s.resets.Load() }

// Bytes 草图+挡板占用字节。
func (s *FreqSketch) Bytes() int { return (len(s.table) + len(s.door)) * 8 }

// Admit Caffeine BoundedLocalCache.admit 的同款判据：
// 赢了就进；平/输但自身够热（>=6）时 1/128 随机放行——否则攻击者只要
// 把在位者的估计值抬高，就能让整个缓存再也进不去任何新东西。
func Admit(candidate, victim uint32, roll uint64) bool {
	if candidate > victim {
		return true
	}
	if candidate >= AdmitHashdosThld {
		return roll&127 == 0
	}
	return false
}

func popcount64(x uint64) uint64 {
	n := uint64(0)
	for x != 0 {
		x &= x - 1
		n++
	}
	return n
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
