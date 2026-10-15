---
title: 17 数据迁移与双写：不停机换库的套路
---

# 17 数据迁移与双写：不停机换库的套路


> 详细程度：深入 · 约 60 分钟
> 材料：W11-1、W11-2、W18-2 · 旧长版：无整章对应（`notes/p11-sqlc-goose.md` 讲的是 schema 迁移，不是本节的数据迁移）
> 前置：04 接口与组合、12 数据库

## 先看一个怎么也追不上的程序

要给一个大表换库，最直接的想法是写个程序把数据搬过去。下面的程序就是这么干的：源表 100 行，分 5 轮搬，每轮搬 20 行。业务没有停，每搬完一轮，它就改一条已搬过的数据，再插一条新数据。搬完之后，把两边逐行比一遍：

```go
package main

import "fmt"

// Table 是一个极简的内存"表"，id 是主键。
type Table map[int64]string

func (t Table) Put(id int64, v string) { t[id] = v }
func (t Table) Delete(id int64)        { delete(t, id) }

// Copy 把 src 里 id 在 from 到 to 之间的行搬过去，模拟"批量导出导入"。
func Copy(src, dst Table, from, to int64) int {
	n := 0
	for id := from; id <= to; id++ {
		if v, ok := src[id]; ok {
			dst[id] = v
			n++
		}
	}
	return n
}

func main() {
	src := Table{}
	dst := Table{}
	for i := int64(1); i <= 100; i++ {
		src.Put(i, fmt.Sprintf("v%d", i))
	}

	// 分 5 轮搬完，业务在每轮之间持续写源表。
	from := int64(1)
	for from <= 100 {
		to := from + 19
		Copy(src, dst, from, to)
		src.Put(from, "biz-update")     // 更新一条刚搬过的行
		src.Put(1000+from, "biz-insert") // 插入一条新行
		from = to + 1
	}

	diff := 0
	for id, v := range src {
		if dst[id] != v {
			diff++
		}
	}
	for id := range dst {
		if _, ok := src[id]; !ok {
			diff++
		}
	}
	fmt.Println("搬完轮次:", 5, "两边不一致行数:", diff)
}
```

```text
搬完轮次: 5 两边不一致行数: 10
```

程序规规矩矩搬完了每一行，结果仍然有 10 行对不上。原因不是搬得慢，而是**数据一直在变**：t1 时刻两边校平了，应用一写，目标库立刻又落后了。资料把这个困境比作蜗牛悖论，每当你追近一段距离，蜗牛又往前爬了一段，所以你永远追不上。〔W11-1 p9〕

追不上，就换思路。整个不停机迁移方案由两半组成：

1. **双写**：让每一次变化同时落到两边。业务写源库的同时也写目标库，不再依赖"追"。
2. **校验加修复**：双写做不到完美（总有一边偶尔失败），所以用一个独立程序不断比对两边，发现差异就修复，让两边**收敛**到一致。

它的哲学可以一句话说清：**失败了就失败，靠校验修复兜底；用监控保证双写失败只是偶发**。〔W11-2 p13〕这一节按"流程、双写、校验修复、增量来源、并发、保护数据库"的顺序把这套方案拆开。

## 和你已知语言的差异

这里的"已知语言"指你用过的迁移手段。做过多库写入或 ETL 的话，下面几行最值得对照：

| 问题 | 常见直觉 | 这套方案的做法 |
|---|---|---|
| 换库怎么做 | 停应用，导出，导入 | 双写加校验修复，分四个阶段切流量 |
| 一致性怎么保证 | 搬完就对齐了 | 永远有窗口期，靠反复校验修复收敛 |
| 双写失败怎么办 | 回滚或重试到成功 | 权威侧失败才失败，非权威侧记下来等修复 |
| 数据库事务 | 一个事务包住所有写入 | 跨库没有事务，两个库各开各的事务 |
| 修复和业务写冲突 | 加锁串行 | 不加锁，接受覆盖，靠下一轮校验收敛 |

第一行是方案选择，中间三行是这套方案里最容易想错的地方，最后一行是它的底线：这是最终一致，不是强一致，下面第五部分专门讲。

## 一、四个阶段、八个步骤

先把问题本身说清楚。停机迁移的做法是停下应用、从老库导出数据、导入新库。中小型应用或不重要的业务常用它，缺陷也直白：应用停下来了。数据量越大，导出和导入越慢，停机越久。〔W11-1 p6〕现实中核心应用不能停机，于是多出两条硬约束：一边迁移，一边有新数据产生、老数据被更新；迁移本身不能给数据库太大压力，否则影响应用的正常运行。〔W11-1 p8〕

还有一类**异构迁移**：源表和目标表结构不一样，或者干脆跨库，比如 MySQL 迁到 MongoDB。它更复杂，难在数据转换和完整性校验：类型转换可能不兼容，数字有精度和溢出问题；外键约束和业务关系很难验证。〔W11-1 p5〕下面讲的校验方案只适用于同构迁移，异构场景见第三部分末尾。

回到主线。不停机迁移分四个阶段，用双写模式命名最清楚：

| 阶段 | 模式 | 读 | 写 | 不一致时以谁为准 |
|---|---|---|---|---|
| 一 | `SrcOnly` | 源 | 只写源 | 源 |
| 二 | `SrcFirst` | 源 | 先写源，成功再写目标 | 源 |
| 三 | `DstFirst` | 目标 | 先写目标，成功再写源 | 目标 |
| 四 | `DstOnly` | 目标 | 只写目标 | 目标 |

〔W11-1 p10〕〔W11-2 p5〕展开成八个步骤：创建目标表；用源表数据初始化目标表；执行一次校验并修复（可选）；业务代码开启双写，读源表、先写源表；开启增量校验和数据修复，保持一段时间；切换双写顺序，读目标表、先写目标表；继续保持增量校验；切换为目标表单写，读写都只操作目标表。〔W11-1 p11〕

注意**读流量要和写模式一起调度**。资料把四个模式叫"双写加流量切换"，强调方案里必须一并考虑读哪个数据源。〔W11-2 p5〕推论（资料未写）：如果双写阶段以源为准，读流量却先切到目标表，读到的可能是还没追上的旧数据，等于把迁移做成了事故。

**回滚怎么设计**（推论，资料未写）：每次切换前，两边数据都完整，所以一、二、三阶段之间任何一步都能退回上一步，前提是增量校验已经把非权威侧漏写的差异修平，否则退回去的那一侧还是旧的。切到 `DstOnly` 之后源库不再更新，退路就没了。所以在最后一步之前，要在 `DstFirst` 停留足够久，让增量校验持续无差异再切。资料没有单独列回滚策略，这一段是我对流程的推导。

## 二、双写怎么实现

双写指写源库的时候，同时写目标库。难点一上来就摆在那里：涉及两个库，**数据库事务管不到两边**。〔W11-2 p4〕实现位置有三种思路：

1. **DAO 层双写**：每个方法里 `switch` 模式，按模式调用源库或目标库。直观，缺陷是侵入式修改，改动量大，容易引入 bug、容易遗漏。〔W11-2 p6-7〕
2. **ORM 的 Hook 或 Callback**：不可行。Hook 要定义在结构体上，一个个定义过去太麻烦；Callback 拿到的 `*gorm.DB` 是固定的，只能是源或目标其中一个，没法动态切换模式。要切到 `DstOnly`？完全没办法。〔W11-2 p8〕
3. **连接池层双写**：GORM 底层有一个和数据库打交道的接口 `ConnPool`（`PrepareContext`、`ExecContext`、`QueryContext`、`QueryRowContext`），实现一个双写版本，按模式分发。事务也有对应接口：用 `ConnPoolBeginner` 开事务，双写模式下源库和目标库各开一个，再实现 `TxCommitter` 的 `Commit` 和 `Rollback`。〔W11-2 p9-12〕提交时先提交权威侧再提交非权威侧，是推论，资料未写。

第三种对业务零侵入，是通用方案。下面的内存版演示它的核心：模式分发和失败语义。`Table` 是内存里的"表"，`failID` 用来注入写失败；业务代码只面对 `DoubleWrite`，感知不到背后有两个"库"：

```go
package main

import (
	"fmt"
	"sync/atomic"
)

// Pattern 四个阶段各对应一个模式。
type Pattern int

const (
	SrcOnly Pattern = iota // 阶段一：只写源
	SrcFirst               // 阶段二：先写源，成功再写目标，源为准
	DstFirst               // 阶段三：先写目标，成功再写源，目标为准
	DstOnly                // 阶段四：只写目标
)

// Table 是内存里的"表"，failID 非零时写这个 id 会失败，用来注入故障。
type Table struct {
	name   string
	rows   map[int64]string
	failID atomic.Int64
}

func NewTable(name string) *Table {
	return &Table{name: name, rows: map[int64]string{}}
}

func (t *Table) Put(id int64, v string) error {
	if t.failID.CompareAndSwap(id, 0) {
		return fmt.Errorf("%s write %d failed", t.name, id)
	}
	t.rows[id] = v
	return nil
}

func (t *Table) Get(id int64) (string, bool) {
	v, ok := t.rows[id]
	return v, ok
}

// DoubleWrite 对应 GORM 的 ConnPool：业务只调用它，不感知两个库。
type DoubleWrite struct {
	Src, Dst       *Table
	pattern        atomic.Int64
	SecondaryFails atomic.Int64 // 非权威侧写失败次数，交给校验修复
	AuthorityFails atomic.Int64 // 权威侧写失败次数，业务直接失败
}

func (d *DoubleWrite) SetPattern(p Pattern) { d.pattern.Store(int64(p)) }

func (d *DoubleWrite) Put(id int64, v string) error {
	first, second := d.Src, d.Dst
	switch Pattern(d.pattern.Load()) {
	case SrcOnly:
		return d.Src.Put(id, v)
	case DstOnly:
		return d.Dst.Put(id, v)
	case DstFirst:
		first, second = d.Dst, d.Src
	}
	// SrcFirst 和 DstFirst 走到这里：先写权威侧，成功再写非权威侧。
	if err := first.Put(id, v); err != nil {
		d.AuthorityFails.Add(1)
		return err // 权威侧失败：整体失败
	}
	if err := second.Put(id, v); err != nil {
		d.SecondaryFails.Add(1) // 非权威侧失败：记下来，靠校验修复
	}
	return nil
}

func main() {
	src := NewTable("src")
	dst := NewTable("dst")
	d := &DoubleWrite{Src: src, Dst: dst}

	d.SetPattern(SrcFirst)
	d.Put(3, "v3")
	dst.failID.Store(3) // 注入：目标库写 3 号会失败
	fmt.Println(d.Put(3, "v3-src-first") == nil, d.SecondaryFails.Load())
	dstv, _ := dst.Get(3)
	fmt.Println(dstv)
	// 权威侧失败：源写不进去，业务拿到错误。
	src.failID.Store(9)
	fmt.Println(d.Put(9, "v9") != nil, d.AuthorityFails.Load())

	d.SetPattern(DstFirst)
	d.Put(4, "v4-dst-first")
	sv, _ := src.Get(4)
	dv, _ := dst.Get(4)
	fmt.Println(sv, dv)
}
```

```text
true 1
v3
true 1
v4-dst-first v4-dst-first
```

逐行读输出。第一行：`SrcFirst` 模式下目标库写 3 号失败，业务调用仍返回 true（没出错），`SecondaryFails` 记了一次。第二行：目标库里 3 号还是旧值 `v3`，差异留着，等校验来发现。第三行：换成源库失败，业务直接拿到错误，`AuthorityFails` 记了一次，这就是"权威侧失败才失败"。第四行：切到 `DstFirst` 后，先写目标再写源，两边都是新值。

失败语义完整地列出来有三种：源写成功、目标写失败，等校验修复；源事务开启成功、目标事务开启失败，回滚源事务；源提交成功、目标提交失败，因为没有记录事务内部的语句，没法直接修复，也只能等校验。〔W11-2 p13〕注意第三种：所以双写路径上要监控 `SecondaryFails` 这类计数，保证失败是偶发而不是常态，常态化的失败说明目标库出了问题，要停下来处理，而不是靠校验硬扛。

## 三、校验与修复：泛型 Validator

校验程序要回答一个问题：两边一致吗？资料先列了三个坑：数据库类型能不能都转成 Go 类型；转出来的类型可不可比；浮点数读出来精度有没有损失、还能不能比。这些都指向同一个问题：怎么查、用什么类型接收、怎么比。〔W11-1 p23〕

方案上先做减法。给每张表写一套 DAO 加比较方法，表一多就要命；用最底层的 `[]byte` 接收一切再比 `[]byte`，在拿不到实体定义时才考虑。能拿到实体定义，就走泛型：**查询通用，比较逻辑交给实体自己实现**。〔W11-1 p24〕

```go
type Entity[T any] interface {
	ID() int64
	Utime() int64
	CompareTo(T) bool
	TableName() string
	Columns() []string
}

type Validator[T Entity[T]] struct {
	Base      *gorm.DB // 权威侧：第二阶段是源表，第三阶段是目标表
	Target    *gorm.DB
	direction string
	batchSize int
}
```

这个接口每个方法都有明确分工。〔W11-1 p25-26〕`ID` 是整个校验体系的围绕点，一切定位靠主键。`CompareTo` 决定字段怎么比、忽略哪些列、要不要业务比较，实体实现者最清楚。`TableName` 和 `Columns` 在修复时用：修复程序要知道往哪张表、写哪些列。`Utime` 不在上面这段资料代码里，但第四部分的增量校验靠它圈范围。`Base` 和 `Target` 加上 `direction`，同一个 `Validator` 就能服务第二阶段（以源为准）和第三阶段（以目标为准）。

**正向校验**：从 `Base` 按 id 顺序分批查出来，逐条拿主键去 `Target` 查。查不到，事件是 `target_missing`；查到了但 `CompareTo` 为 false，事件是 `neq`；查询出错只能记日志、监控告警，等人工介入。〔W11-1 p27-28〕

**反向校验**：以源为准的阶段，理论上不会出现"源没有而目标有"，因为目标数据本来就是从源初始化的。唯一的例外是硬删除：同步之后源表把这行删了，目标表就多出一条。软删除本质是 UPDATE，不会有这个问题。所以要有一个反向过程：从 `Target` 取一批 id，去 `Base` 里找，差集就是已经删掉的，事件类型 `base_missing`。〔W11-1 p29-31〕

**异构场景要另想办法**：上面这套校验默认能用主键在目标表找到对应行，所以只适用同构迁移。异构时从源表查出数据后没法直接拿主键去目标表找，要改用唯一索引、外键等去找；源表字段被丢弃，或目标表有根据源表数据计算出来的新字段时，只能定制校验逻辑，没有通用版本。〔W11-1 p32〕

**修复**：三种事件三种动作，`target_missing` 插入，`neq` 用源覆盖目标，`base_missing` 删掉目标多出来的。资料随后给了个更省事的版本：修复时根本不管事件类型，直接拿 id 去 `Base` 查最新值，查到就 upsert 到 `Target`，查不到就删 `Target`。〔W11-1 p37〕〔W11-1 p39〕

为什么必须是 upsert：从校验到修复之间数据会被改。双写阶段目标表也在被业务写，校验时目标没有这条，修复时业务可能刚好写进去了。只 insert 会主键冲突，只 update 会漏插，upsert 幂等，把这种并发吸收掉。〔W11-1 p40〕

把全量校验、失败注入、增量校验、反向校验串成一条链路跑一遍。实体 `Row` 实现 `Entity` 接口，`Table` 是内存表：

```go
package main

import (
	"fmt"
	"sort"
)

// Entity 是资料里 Entity 接口的内存版：ID 定位，Utime 圈定增量，CompareTo 交给实体比较。
type Entity interface {
	ID() int64
	Utime() int64
	CompareTo(Entity) bool
}

// Row 是一条业务数据，比如用户表的一行。
type Row struct {
	id    int64
	name  string
	score int
	utime int64
}

func (r Row) ID() int64    { return r.id }
func (r Row) Utime() int64 { return r.utime }
func (r Row) CompareTo(o Entity) bool {
	b := o.(Row)
	return r.name == b.name && r.score == b.score
}

// Table 是内存"表"：fail 里的 id 写入会失败，用来注入目标库故障。
type Table struct {
	rows map[int64]Entity
	fail map[int64]bool
}

func NewTable() *Table { return &Table{rows: map[int64]Entity{}, fail: map[int64]bool{}} }

func (t *Table) Put(e Entity) error {
	if t.fail[e.ID()] {
		return fmt.Errorf("write %d failed", e.ID())
	}
	t.rows[e.ID()] = e
	return nil
}

func (t *Table) Get(id int64) (Entity, bool) { e, ok := t.rows[id]; return e, ok }
func (t *Table) Delete(id int64)             { delete(t.rows, id) }

func (t *Table) IDsAtLeast(utime int64) []int64 {
	var ids []int64
	for id, e := range t.rows {
		if e.Utime() >= utime {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

type Event struct {
	ID   int64
	Type string // target_missing / neq / base_missing
}

// Validator 持有 Base（权威侧）和 Target（跟进侧），同一段代码服务第二、三阶段。
type Validator struct {
	Base, Target *Table
}

// ValidateBaseToTarget 正向校验：以 Base 为准逐条比对 Target。
func (v *Validator) ValidateBaseToTarget(sinceUtime int64) []Event {
	var evs []Event
	for _, id := range v.Base.IDsAtLeast(sinceUtime) {
		b, _ := v.Base.Get(id)
		t, ok := v.Target.Get(id)
		switch {
		case !ok:
			evs = append(evs, Event{id, "target_missing"})
		case !b.CompareTo(t):
			evs = append(evs, Event{id, "neq"})
		}
	}
	return evs
}

// ValidateTargetToBase 反向校验：找出 Target 里 Base 没有的行（硬删除造成的）。
func (v *Validator) ValidateTargetToBase() []Event {
	var evs []Event
	for _, id := range v.Target.IDsAtLeast(0) {
		if _, ok := v.Base.Get(id); !ok {
			evs = append(evs, Event{id, "base_missing"})
		}
	}
	return evs
}

// Fix 修复不区分事件类型：Base 还有就 upsert 过去，没有了就删 Target。
func (v *Validator) Fix(e Event) {
	if b, ok := v.Base.Get(e.ID); ok {
		t, exists := v.Target.Get(e.ID)
		if exists && b.CompareTo(t) {
			return // 已经一致，不用动
		}
		_ = v.Target.Put(b)
		return
	}
	v.Target.Delete(e.ID)
}

func fmtEvents(evs []Event) string {
	s := "["
	for i, e := range evs {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d:%s", e.ID, e.Type)
	}
	return s + "]"
}

func main() {
	src := NewTable()
	dst := NewTable()
	v := &Validator{Base: src, Target: dst}
	clock := int64(100)
	tick := func() int64 { clock += 10; return clock }

	// 造 5 行源数据，目标表为空。
	for i := int64(1); i <= 5; i++ {
		src.Put(Row{id: i, name: fmt.Sprintf("u%d", i), score: 60, utime: tick()})
	}

	// 1 全量校验：目标表全缺。
	evs := v.ValidateBaseToTarget(0)
	fmt.Println("1 全量校验:", fmtEvents(evs))
	for _, e := range evs {
		v.Fix(e)
	}
	fmt.Println("  修复后:", fmtEvents(v.ValidateBaseToTarget(0)))

	// 2 开双写（SrcFirst）：先写源，成功再写目标。注入目标库写 3 号失败。
	// 源写成功，业务拿到 nil；目标写失败，差异留给校验修复。
	dst.fail[3] = true
	row := Row{id: 3, name: "u3", score: 90, utime: tick()}
	fmt.Println("2 源写成功:", src.Put(row) == nil, "目标写失败:", dst.Put(row) != nil)
	evs = v.ValidateBaseToTarget(row.utime)
	fmt.Println("  增量校验:", fmtEvents(evs))
	delete(dst.fail, 3) // 目标库故障排除，修复才写得进去
	for _, e := range evs {
		v.Fix(e)
	}
	fmt.Println("  修复后:", fmtEvents(v.ValidateBaseToTarget(0)))

	// 3 源表硬删 5 号：正向校验看不见它，反向校验才能发现。
	src.Delete(5)
	fmt.Println("3 正向校验:", fmtEvents(v.ValidateBaseToTarget(0)), "反向校验:", fmtEvents(v.ValidateTargetToBase()))
	for _, e := range v.ValidateTargetToBase() {
		v.Fix(e)
	}
	fmt.Println("  修复后:", fmtEvents(v.ValidateTargetToBase()), "两边行数:", len(src.rows), len(dst.rows))
}
```

```text
1 全量校验: [1:target_missing 2:target_missing 3:target_missing 4:target_missing 5:target_missing]
  修复后: []
2 源写成功: true 目标写失败: true
  增量校验: [3:neq]
  修复后: []
3 正向校验: [] 反向校验: [5:base_missing]
  修复后: [] 两边行数: 4 4
```

第 1 步是开双写前的全量校验：5 行全缺，修一次补齐。第 2 步模拟双写失败：同一行数据，源库写入成功，目标库写入失败，业务拿到 true，增量校验只扫 `utime` 达标的行，精准发现 `3:neq`；故障排除后修复把目标行盖成源的新值（代码里用 `delete(dst.fail, 3)` 模拟排除，故障还在的话修复也会写失败，这条差异会留到下一轮校验）。第 3 步在源表硬删 5 号，目标表还留着这一行：正向校验以源为准逐行查，源里已经没有 5 号，所以什么也发现不了；只有反向校验从目标表取 id 去源表找，才抓到 `5:base_missing`，修复把它从目标表删掉，两边都剩 4 行。

全量校验和修复本身就构成一个"导出导入"程序，资料特别提到：初始化目标表之后那次可选的全量校验修复，用的就是这套代码。〔W11-1 p19〕校验发现不一致后怎么送去修复，形态上有三种：同一 goroutine 里立刻修；交给另一个 goroutine 修；发到消息队列，由消费者修。资料选 Kafka，为的是解耦削峰，控制住消费者的消费速率，就等于控制住了目标表的写入速率。〔W11-1 p21-22〕

## 四、增量校验怎么知道"改了什么"

全量校验只在开双写前跑一次（可选），双写期间要持续做**增量校验**，只看新修改的数据，防止双写中间出问题。它第二、第三阶段都可以运行。〔W11-2 p15〕问题是：我怎么知道哪些数据是新的？资料给了三条路：

1. **在连接池层拦截**：很难。`UPDATE xx SET xxx WHERE a = 1` 影响了哪些行，连接池不知道；理论上的做法是解析 SQL 语法树，把 WHERE 部分转成 SELECT 再查一遍受影响的行。〔W11-2 p16-18〕
2. **`utime` 轮询**：大部分公司的表规范都要求有更新时间列，webook 里就是 `utime`。校验时加上 `WHERE utime >= ?` 轮询近期数据，只需要在原有 Validator 上加一个条件。前提是 `utime` 上有**独立索引**，否则查询会特别慢。〔W11-2 p18〕
3. **binlog / CDC**：实时。Canal 是一个典型的 CDC 工具，伪装成 MySQL 从库连上主库，主库把 binlog 发给它，它解析后转发到 Kafka。〔W18-2 p4〕〔W18-2 p8〕binlog 有三种格式：Row 记每行数据的变更，信息最全但日志量大；Statement 记 SQL 语句，日志量小但可能漏掉复杂变更；Mixed 混合两者。资料推荐 Row，用起来方便。〔W18-2 p9〕

用 Canal 做增量校验时，消费者拿到消息，完全不理会是 INSERT、UPDATE 还是 DELETE，只取 id，去两边各查一次再比较。〔W18-2 p24-25〕有三个细节必须知道：

- **分区保序**：同一条数据的消息必须进同一个 Kafka 分区才能保序，配置 `canal.mq.partitionHash` 按 id 哈希。乱序会导致旧消息后到，把新值比对成不一致。〔W18-2 p21〕
- **删除消息的反直觉字段**：DELETE 消息里，被删的数据在 `data` 字段，`old` 反而是 null。资料作者说这种设计让他写过一堆垃圾代码。〔W18-2 p17〕
- **切换窗口漏检**：以源为准期间产生的 binlog，到达 Canal 时可能已经切到以目标为准，消费者一看是源库的消息就忽略，这批差异没人发现。资料的做法是切换后手动校验窗口期内改过的数据。〔W18-2 p26〕

`utime` 和 `sleepInterval` 组合起来还能一套代码两用：`utime = 0` 且 `sleepInterval <= 0` 是全量校验，跑完退出；`utime = 0` 且 `sleepInterval > 0` 是全量校验之后接着增量；`utime` 设成近期时间点且 `sleepInterval > 0` 就是纯增量校验，循环轮询。〔W11-2 p19〕

## 五、并发与一致性窗口

增量校验开启后，目标库有两个过程在写：业务写、修复写。于是可能出现修复把业务的写入覆盖掉：双写第二次更新 a=3 成功，修复程序却拿着第一次读到的 a=2 把目标表盖回去。怎么办？资料的态度是**不用办**：等这条数据再次被更新，或者再跑一轮校验，就收敛了。〔W11-2 p21〕

把这个态度推广开：全量校验和增量校验可以并行跑；修复和业务写并发也没有锁。所有这些并发问题，答案都是同一句话：**反复校验和修复，最终总会一致**。这是最终一致，不是强一致，接受短窗口内的不一致，是这套方案能做得简单的前提。〔W11-2 p22〕

主从库带来三个差别。校验要最新数据就读主库，从库有延迟；主库写请求已经把负载打高了，校验读再压上去是雪上加霜，所以先读从库，不一致再读主库复核一遍；修复没有商量，只能走主库。前面修复走 Kafka 已经削了峰，要进一步保护主库就再接数据库限流。〔W11-2 p32〕

## 六、别把线上库打垮

迁移程序跑得再对，把源库或目标库压垮了，业务就出了事故。保护手段分两侧：

- **程序侧**：按表开 goroutine，一张表一个；表太大就按 id 尾号再分片，比如 10 个 goroutine 各负责尾号 0 到 9。瓶颈通常在数据库，不在程序。〔W11-2 p30〕
- **量化**：大厂核心业务一个逻辑表（多半已分库分表）可能有数十亿条，想在一天内校验完，QPS 要过万。答案是尽量提高并发度，同时保护住源表和目标表。〔W11-1 p20〕
- **数据库侧**：业务低峰期多开 goroutine，高峰期少开甚至停掉；专门为迁移准备一个从库代价太高，除非核心中的核心，一般不用。〔W11-2 p31〕
- **修复走 Kafka**：解耦削峰，控制消费速率等于控制目标表写入速率，还能给修复过程加限流。〔W11-1 p21-22〕
- **初始化目标表**：批量读源库，转成 SQL 导入或直接用 ORM 插入，SQL 导入更快。并发手段是表内拆分（如按 `ID % 3` 分给三个 goroutine）加每表一个 goroutine，混合使用，控制数量，别影响线上。〔W11-1 p16〕
- **调度时机**：全量校验会跑很久，可以定时到业务低峰期运行，或者动态判定负载，高了就挂起，低了再继续。〔W11-1 p33〕

## 小结

1. 追平思路不可行：数据一直在变，搬完就落后，像蜗牛悖论。解法是双写加校验修复。
2. 四个阶段对应四个模式 `SrcOnly`、`SrcFirst`、`DstFirst`、`DstOnly`，读流量和写模式一起切换。
3. 双写实现在连接池层（GORM 的 `ConnPool`）最通用，DAO 层太侵入，Callback 拿到固定的 DB 做不了切换。
4. 失败语义：权威侧失败业务才失败，非权威侧失败记下来等修复；跨库没有事务，各开各的。
5. 校验分正向（缺、不等）和反向（硬删除导致的 `base_missing`）；修复不分事件类型，查 Base 有就 upsert、没有就删。
6. 增量校验的改动来源：连接池拦截难，`utime` 轮询简单，binlog/CDC 实时；Canal 要按 id 哈希分区保序。
7. 一切并发问题靠反复校验修复收敛，这是最终一致；全程控制并发度，保护源库和目标库。

## 常见坑

1. **只做全量复制不做双写**：复制期间的变化永远追不上，搬完就对齐只是错觉。
2. **双写失败就让业务失败**：非权威侧失败应记录并告警，权威侧失败才失败；否则迁移本身变成线上故障源。
3. **校验用 offset 分页**：校验期间数据在变，按偏移量翻页会漏检或重复；用 id 或 utime 游标更稳（资料作业的思考方向，待核）。
4. **`utime` 没建独立索引**：增量轮询变成全表扫描，拖垮数据库。〔W11-2 p18〕
5. **只写正向校验**：硬删除产生的目标表多余数据永远发现不了，要配反向校验。〔W11-1 p29-31〕
6. **切换窗口没补校验**：Canal 方案里，模式切换窗口期的 binlog 被忽略，差异会永远留着。〔W18-2 p26〕
7. **没有演练失败注入**：双写非权威侧失败、目标事务开启失败这些路径，不主动模拟就等于没测过；资料演示时就是靠直接改数据库来模拟双写失败的。〔W11-2 p26〕

## 判断题与答案

先自己答，再看后面。

1. 双写阶段以源为准，但读流量先切到目标表，会发生什么？
2. 为什么修复要用 upsert，而不是只 insert 或只 update？
3. 以源为准的阶段，什么情况下目标表会比源表多出一条？用什么发现？
4. 切到 `DstOnly` 之前，为什么要在 `DstFirst` 停留一段时间？

**答案**

1. 读到目标库里还没被修复的旧数据，把迁移做成线上事故。所以读流量要跟着写模式一起调度，`SrcFirst` 期间读源。〔W11-2 p5〕
2. 从校验到修复之间目标表还在被业务写：校验时缺的行，修复时可能已经有了。只 insert 会主键冲突，只 update 会漏插，upsert 幂等，吸收这段并发。〔W11-1 p40〕
3. 源表硬删了一行，而目标表还没删。软删除本质是 UPDATE，不会这样。正向校验两边都没有这行，发现不了，要用反向校验：取目标表 id 去源表找，差集就是 `base_missing`。〔W11-1 p29-31〕
4. 切到 `DstOnly` 后源库不再更新，回滚就没有退路了。在 `DstFirst` 停留足够久、让增量校验持续无差异，是把最后一步的风险压到最低（推论，资料未写）。

## 在项目里

这里没有现成例子：`dnsproxi-go` 没有持久化数据，自然没有迁移问题。思路最接近的是 `dnsproxi-go/cache/expire.go` 里的 `ExpireRound`：后台每轮随机抽样判断过期并删除，同样是"一个后台程序不断把状态收敛到目标"的模式，只是对象是内存缓存而不是两个数据库。完整的数据迁移演练放在 minifeed 项目线里做。

## 想深入去哪查

- 原材料：W11-1（四阶段、八步、Validator、修复与 upsert）、W11-2（双写四模式、ConnPool 实现、增量校验、并发与保护数据库）、W18-2（Canal、binlog 三格式、增量校验案例与切换窗口）。
- 旧章：`notes/p11-sqlc-goose.md` 讲 schema 迁移（goose 的 up 与 down），管的是表结构怎么变，本节管的是表里已有的数据怎么搬，两件事互补。
- 本节示例里的 `Table`、`DoubleWrite`、`Validator` 都是内存版，真实实现里它们对应数据库连接、GORM 的 `ConnPool` 和 Kafka 消费者。资料里完整的 Scheduler（把四个模式和全量、增量的启停暴露成 HTTP 接口）见 W11-2 p24。
