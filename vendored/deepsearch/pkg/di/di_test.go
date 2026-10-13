package di

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// Helper types for testing
type ServiceA struct {
	ID string
}
type ServiceB struct {
	A *ServiceA
}
type ServiceC struct {
	B *ServiceB
}

func TestBasicLifecycle(t *testing.T) {
	ctx := New(context.Background())

	// Test Singleton (Default)
	var created int
	err := Provide(ctx, func(ctx context.Context) (*ServiceA, error) {
		created++
		return &ServiceA{ID: "singleton"}, nil
	})
	if err != nil {
		t.Fatalf("Provide failed: %v", err)
	}

	s1, err := Invoke[*ServiceA](ctx)
	if err != nil {
		t.Fatalf("Invoke 1 failed: %v", err)
	}
	s2, err := Invoke[*ServiceA](ctx)
	if err != nil {
		t.Fatalf("Invoke 2 failed: %v", err)
	}

	if s1 != s2 {
		t.Error("Singleton expects same instance, got different pointers")
	}
	if created != 1 {
		t.Errorf("Singleton factory called %d times, expected 1", created)
	}

	// Test Transient
	err = Provide(ctx, func(ctx context.Context) (*ServiceB, error) {
		return &ServiceB{}, nil
	}, WithLifetime(Transient))
	if err != nil {
		t.Fatal(err)
	}

	b1, _ := Invoke[*ServiceB](ctx)
	b2, _ := Invoke[*ServiceB](ctx)
	if b1 == b2 {
		t.Error("Transient expects different instances, got same pointer")
	}
}

func TestDependencies(t *testing.T) {
	ctx := New(context.Background())

	_ = Provide(ctx, func(ctx context.Context) (*ServiceA, error) {
		return &ServiceA{ID: "root"}, nil
	})

	_ = Provide(ctx, func(ctx context.Context) (*ServiceB, error) {
		a, err := Invoke[*ServiceA](ctx)
		if err != nil {
			return nil, err
		}
		return &ServiceB{A: a}, nil
	})

	b, err := Invoke[*ServiceB](ctx)
	if err != nil {
		t.Fatalf("Failed to resolve dependency: %v", err)
	}
	if b.A == nil || b.A.ID != "root" {
		t.Error("Dependency injection failed")
	}
}

func TestCircularDependency(t *testing.T) {
	ctx := New(context.Background())

	// A -> B -> A
	_ = Provide(ctx, func(ctx context.Context) (*ServiceA, error) {
		_, err := Invoke[*ServiceB](ctx)
		return &ServiceA{}, err
	})
	_ = Provide(ctx, func(ctx context.Context) (*ServiceB, error) {
		_, err := Invoke[*ServiceA](ctx)
		return &ServiceB{}, err
	})

	_, err := Invoke[*ServiceA](ctx)
	if !errors.Is(err, ErrCircularDependency) {
		t.Errorf("Expected ErrCircularDependency, got %v", err)
	}
}

func TestErrors(t *testing.T) {
	// 1. Missing Container
	_, err := Invoke[int](context.Background())
	if !errors.Is(err, ErrContainerMissing) {
		t.Errorf("Expected ErrContainerMissing, got %v", err)
	}

	// 2. Service Not Found
	ctx := New(context.Background())
	_, err = Invoke[string](ctx)
	if !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("Expected ErrServiceNotFound, got %v", err)
	}

	// 3. Provider Error propagation
	mockErr := errors.New("boom")
	_ = Provide(ctx, func(ctx context.Context) (int, error) {
		return 0, mockErr
	})
	_, err = Invoke[int](ctx)
	if !errors.Is(err, mockErr) {
		t.Errorf("Expected provider error, got %v", err)
	}
}

func TestCleanupAndCascade(t *testing.T) {
	ctx := New(context.Background())
	var cleanedA, cleanedB, cleanedC int32

	// Topology: C depends on B, B depends on A
	// Cleaning A should cascade to B and C

	_ = Provide(ctx, func(ctx context.Context) (*ServiceA, error) { return &ServiceA{}, nil },
		WithCleanup(func(_ context.Context, s *ServiceA) error {
			atomic.AddInt32(&cleanedA, 1)
			return nil
		}))

	_ = Provide(
		ctx, func(ctx context.Context) (*ServiceB, error) {
			a, _ := Invoke[*ServiceA](ctx)
			return &ServiceB{A: a}, nil
		}, WithCleanRecursive(true),
		WithCleanup(func(_ context.Context, s *ServiceB) error {
			atomic.AddInt32(&cleanedB, 1)
			return nil
		}),
	)

	_ = Provide(
		ctx, func(ctx context.Context) (*ServiceC, error) {
			b, _ := Invoke[*ServiceB](ctx)
			return &ServiceC{B: b}, nil
		}, WithCleanRecursive(true),
		WithCleanup(func(_ context.Context, s *ServiceC) error {
			atomic.AddInt32(&cleanedC, 1)
			return nil
		}),
	)

	// Must Invoke to build the dependency graph
	_, err := Invoke[*ServiceC](ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Clean A (Root dependency)
	err = Clean[*ServiceA](ctx)
	if err != nil {
		t.Fatal(err)
	}

	if atomic.LoadInt32(&cleanedA) != 1 {
		t.Error("Service A not cleaned")
	}
	if atomic.LoadInt32(&cleanedB) != 1 {
		t.Error("Service B not cleaned via cascade")
	}
	if atomic.LoadInt32(&cleanedC) != 1 {
		t.Error("Service C not cleaned via cascade")
	}

	// Verify instances are recreated
	a2, _ := Invoke[*ServiceA](ctx)
	b2, _ := Invoke[*ServiceB](ctx)
	if a2 == nil || b2 == nil {
		t.Error("Services should be recreatable after clean")
	}
}

func TestWithCleanBlocking(t *testing.T) {
	ctx := New(context.Background())
	var cleanedB int32

	_ = Provide(ctx, func(_ context.Context) (string, error) { return "A", nil })

	// B depends on A, but opts out of auto-clean
	_ = Provide(
		ctx, func(ctx context.Context) (*ServiceB, error) {
			_, _ = Invoke[string](ctx)
			return &ServiceB{}, nil
		}, WithCleanRecursive(false),
		WithCleanup(func(_ context.Context, s *ServiceB) error {
			atomic.AddInt32(&cleanedB, 1)
			return nil
		}),
	)

	_, _ = Invoke[*ServiceB](ctx)

	// Clean A
	_ = Clean[string](ctx)

	if atomic.LoadInt32(&cleanedB) != 0 {
		t.Error("Service B should not have been cleaned due to WithClean(false)")
	}
}

func TestConcurrency(t *testing.T) {
	ctx := New(context.Background())

	// Simulate a heavy constructor
	_ = Provide(ctx, func(_ context.Context) (*ServiceA, error) {
		return &ServiceA{}, nil
	})

	const workers = 100
	var wg sync.WaitGroup
	wg.Add(workers)

	results := make([]*ServiceA, workers)

	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()
			val, err := Invoke[*ServiceA](ctx)
			if err != nil {
				t.Errorf("Concurrent invocation failed: %v", err)
			}
			results[idx] = val
		}(i)
	}
	wg.Wait()

	first := results[0]
	for i := 1; i < workers; i++ {
		if results[i] != first {
			t.Error("Concurrent Singleton resolution returned different instances")
		}
	}
}

func BenchmarkInvokeSingleton(b *testing.B) {
	ctx := New(context.Background())
	_ = Provide(ctx, func(ctx context.Context) (int, error) {
		return 100, nil
	})

	// Warmup
	_, _ = Invoke[int](ctx)

	b.ReportAllocs()
	for b.Loop() {
		_, _ = Invoke[int](ctx)
	}
}

func BenchmarkInvokeSingletonParallel(b *testing.B) {
	ctx := New(context.Background())
	_ = Provide(ctx, func(_ context.Context) (*ServiceA, error) { return &ServiceA{}, nil })
	_, _ = Invoke[*ServiceA](ctx) // Warmup

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Invoke[*ServiceA](ctx)
		}
	})
}

func BenchmarkInvokeTransient(b *testing.B) {
	ctx := New(context.Background())
	_ = Provide(ctx, func(ctx context.Context) (int, error) {
		return 100, nil
	}, WithLifetime(Transient))

	b.ReportAllocs()
	for b.Loop() {
		_, _ = Invoke[int](ctx)
	}
}

func BenchmarkInvokeTransientParallel(b *testing.B) {
	ctx := New(context.Background())
	_ = Provide(ctx, func(_ context.Context) (*ServiceA, error) { return &ServiceA{}, nil }, WithLifetime(Transient))
	_, _ = Invoke[*ServiceA](ctx) // Warmup

	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = Invoke[*ServiceA](ctx)
		}
	})
}
