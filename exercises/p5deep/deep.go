// Package p5deep 练习：复刻 deepsearch 的工程套路。
package p5deep

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"golang.org/x/sync/errgroup"
)

// ============ 练习 1：TypeFor 当 map key（di.go:123 的套路）============

// TypedRegistry 按 T 注册/取回值。
type TypedRegistry struct {
	mu sync.RWMutex
	m  map[reflect.Type]any
}

// NewRegistry 创建空注册表。
func NewRegistry() *TypedRegistry {
	return &TypedRegistry{m: map[reflect.Type]any{}}
}

// Store 存一个按类型的值（Go 方法不能带类型参数 → 包级泛型函数，di.go 同款）。
// 参考实现：reflect.TypeFor[T]() 是 1.22+ 的零成本类型字面量
// （老写法 reflect.TypeOf((*T)(nil)).Elem() 多一次分配+解引用）。
func Store[T any](r *TypedRegistry, v T) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[reflect.TypeFor[T]()] = v
}

// Load 按 T 取值；不存在返回 false。
// 参考实现：any → T 的断言。map[reflect.Type]any 的 any 是"擦除"，
// 断言 v.(T) 是"还原"——Rust 里这层由编译期单态化免费完成，Go 走运行时。
func Load[T any](r *TypedRegistry) (T, bool) {
	var zero T
	r.mu.RLock()
	v, ok := r.m[reflect.TypeFor[T]()]
	r.mu.RUnlock()
	if !ok {
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// ============ 练习 2：迷你 DI ============
// 20 行版 Invoke：泛型 + 已注册 provider + sync.Once 语义。

type MiniDI struct {
	reg   *TypedRegistry
	once  map[reflect.Type]*sync.Once
	onceM *sync.Mutex
}

// NewMiniDI 创建容器。
func NewMiniDI() *MiniDI {
	return &MiniDI{reg: NewRegistry(), once: map[reflect.Type]*sync.Once{}, onceM: &sync.Mutex{}}
}

// Provide 注册 provider（登记闭包，不执行）。
// 参考实现：providerOf[T] 是 T 专属 key 类型——TypeFor[providerOf[T]]()
// 对每个 T 都是不同的 reflect.Type，provider 与结果天然分 namespace。
// ⚠️ 教训（第一版参考实现翻车点）：若所有 T 的 provider 都直接存一个
// 具体类型（如 providerFn），TypeFor 是同一个 key——后注册覆盖先注册，
// 且所有类型共享同一个 *sync.Once → Invoke[*db] 的 Do 里 provider 调
// Invoke[config] → 同一个 Once 重入 → 自己等自己，死锁。
// Rust 的 HashMap<TypeId, Box<dyn Any>> 同样会撞 key；教训是"类型当 key
// 的表，注册项本身也要按类型隔离"。
type providerOf[T any] struct {
	fn func(ctx context.Context) (T, error)
}

func Provide[T any](d *MiniDI, p func(context.Context) (T, error)) {
	Store(d.reg, providerOf[T]{fn: p})
}

// Invoke 解析：第一次调用 provider 并缓存（Singleton 语义）。
// 参考实现：once 表按类型取 Once（首次登记要加锁），Do 包住 provider 调用。
// 缓存也走 TypedRegistry（类型即 key）——结果和 Once 是两张表，独立演化。
// Rust 对照：OnceLock<T> 一步到位；Go 把"懒初始化"拆成 once 表 + 缓存表两笔账。
func Invoke[T any](d *MiniDI, ctx context.Context) (T, error) {
	var zero T
	p, ok := Load[providerOf[T]](d.reg)
	if !ok {
		return zero, fmt.Errorf("no provider for %T", zero)
	}
	// 并发首调：同类型的 Once 必须是同一个实例（onceM 保护登记过程）。
	d.onceM.Lock()
	o, ok := d.once[reflect.TypeFor[T]()]
	if !ok {
		o = &sync.Once{}
		d.once[reflect.TypeFor[T]()] = o
	}
	d.onceM.Unlock()

	var (
		cached T
		err    error
	)
	o.Do(func() {
		cached, err = p.fn(ctx)
		if err == nil {
			Store(d.reg, cached) // 结果缓存：T 类型 key，与 providerOf[T] 不撞
		}
	})
	if err != nil {
		return zero, err
	}
	// ⚠️ 不能 return cached：o.Do 只在第一个调用者里跑闭包，
	// 后续调用者的 cached 是零值。单例必须从缓存表回读。
	v, ok := Load[T](d.reg)
	if !ok {
		return zero, fmt.Errorf("invoke %T: cache miss after once", zero)
	}
	return v, nil
}

// ============ 练习 3：优雅关停 ============
// mini server.go：两个 runner，任一错误或 ctx 取消时全停。

// Runner 命名任务。
type Runner struct {
	Name string
	Exec func(ctx context.Context) error
}

// RunAll 并行跑 runners：
//   - 全部正常返回 → 返回 nil
//   - 任一返回错误 → cancel 其它，返回第一个非 Canceled 错误
//   - ctx 先被取消 → runner 收到 Canceled → 视为正常
//
// 对照 cmd/server.go:90-140（你读过的），Rust 对照：try_join! + CancellationToken
func RunAll(ctx context.Context, runners []Runner) error {
	// 参考实现：errgroup = try_join! + CancellationToken 合体（P3 ⑧ 同款）。
	// 两个语义细节：
	//   1. runner 返回 context.Canceled 不算错——它只说明"有人喊停了"
	//      （外部 cancel 或兄弟失败），不是业务失败。
	//   2. g.Wait() 若返回 Canceled（全部 runner 都被取消时 errgroup
	//      把 ctx.Err() 顶出来），同样过滤成 nil。
	g, gctx := errgroup.WithContext(ctx)
	for _, r := range runners {
		r := r // 显式 shadow（同 P3 ⑧ 的保险）
		g.Go(func() error {
			err := r.Exec(gctx)
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		})
	}
	err := g.Wait()
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
