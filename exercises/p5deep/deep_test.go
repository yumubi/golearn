package p5deep

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type config struct{ port int }
type db struct{ dsn string }

func TestTypeKey(t *testing.T) {
	r := NewRegistry()
	Store(r, &config{port: 8080})
	Store(r, &db{dsn: "postgres://x"})

	c, ok := Load[*config](r)
	if !ok || c.port != 8080 {
		t.Fatalf("config wrong: %v %v", c, ok)
	}
	d, ok := Load[*db](r)
	if !ok || d.dsn != "postgres://x" {
		t.Fatalf("db wrong: %v %v", d, ok)
	}
	// 未注册的类型返回 false。
	if _, ok := Load[string](r); ok {
		t.Fatal("string should not be registered")
	}
}

func TestProvideInvoke(t *testing.T) {
	d := NewMiniDI()
	Provide(d, func(ctx context.Context) (config, error) {
		return config{port: 9090}, nil
	})
	calls := 0
	Provide(d, func(ctx context.Context) (*db, error) {
		calls++
		cfg, err := Invoke[config](d, ctx)
		if err != nil {
			return nil, err
		}
		return &db{dsn: fmt.Sprintf("cfg-port-%d", cfg.port)}, nil
	})

	ctx := context.Background()
	d1, err := Invoke[*db](d, ctx)
	if err != nil {
		t.Fatalf("invoke db: %v", err)
	}
	d2, err := Invoke[*db](d, ctx)
	if err != nil {
		t.Fatalf("invoke db again: %v", err)
	}
	if d1.dsn != "cfg-port-9090" {
		t.Fatalf("dsn wrong: %q", d1.dsn)
	}
	if d1 != d2 {
		t.Fatal("singleton broken: two instances")
	}
	if calls != 1 {
		t.Fatalf("provider called %d times, want 1", calls)
	}
	// 未注册类型报错。
	if _, err := Invoke[string](d, ctx); err == nil {
		t.Fatal("want error for unregistered type")
	}
}

func TestGracefulShutdown(t *testing.T) {
	var ranA, ranB atomic.Bool

	// 正常完成。
	err := RunAll(context.Background(), []Runner{
		{Name: "a", Exec: func(ctx context.Context) error { ranA.Store(true); return nil }},
		{Name: "b", Exec: func(ctx context.Context) error { ranB.Store(true); return nil }},
	})
	if err != nil || !ranA.Load() || !ranB.Load() {
		t.Fatalf("normal run: err=%v a=%v b=%v", err, ranA.Load(), ranB.Load())
	}

	// 任一失败 → 其它被取消；Canceled 不当错误。
	boom := errors.New("boom")
	started := make(chan struct{})
	err = RunAll(context.Background(), []Runner{
		{Name: "slow", Exec: func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err() // Canceled
		}},
		{Name: "bad", Exec: func(ctx context.Context) error {
			<-started
			return boom
		}},
	})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}

	// 外部取消 → 正常返回。
	ctx, cancel := context.WithCancel(context.Background())
	sawCancel := make(chan struct{})
	go func() {
		<-time.After(50 * time.Millisecond)
		cancel()
	}()
	err = RunAll(ctx, []Runner{
		{Name: "waiter", Exec: func(ctx context.Context) error {
			<-ctx.Done()
			close(sawCancel)
			return ctx.Err()
		}},
	})
	if err != nil {
		t.Fatalf("external cancel should be normal: %v", err)
	}
}
