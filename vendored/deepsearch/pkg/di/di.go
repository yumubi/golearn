package di

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
)

var (
	ErrContainerMissing   = errors.New("container not found in context")
	ErrServiceNotFound    = errors.New("service not found")
	ErrCircularDependency = errors.New("circular dependency detected")
)

// ServiceLifetime determines when a service is instantiated.
type ServiceLifetime int

const (
	Singleton ServiceLifetime = iota
	Transient
)

// Provider creates a service instancentry.
type Provider[T any] func(ctx context.Context) (T, error)

// Option configures a service entry.
type Option func(*serviceEntry)

type (
	ctxKeyContainer struct{}
	ctxKeyResolving struct{}
)

var (
	ctxKeyContainerKey = ctxKeyContainer{}
	ctxKeyResolvingKey = ctxKeyResolving{}
)

// serviceEntry holds the state and configuration of a servicentry.
type serviceEntry struct {
	provider       func(context.Context) (any, error)
	cleanup        func(context.Context, any) error
	lifetime       ServiceLifetime
	cleanRecursive bool

	mu       sync.Mutex
	instance any
	built    bool
}

// containerImpl is the concurrent-safe DI container.
type containerImpl struct {
	services sync.Map // map[reflect.Type]*serviceEntry
	graph    sync.Map // map[reflect.Type]*sync.Map (Set of dependents)
}

// New returns a context with a new DI container attached.
func New(ctx context.Context) context.Context {
	return context.WithValue(ctx, ctxKeyContainerKey, &containerImpl{})
}

func Shutdown(ctx context.Context) error {
	c, ok := ctx.Value(ctxKeyContainerKey).(*containerImpl)
	if !ok {
		return ErrContainerMissing
	}

	errs, visited := error(nil), make(map[reflect.Type]bool)

	c.services.Range(func(k, _ any) bool {
		if e := c.cleanRecursive(ctx, k.(reflect.Type), visited); e != nil {
			errs = errors.Join(errs, e)
		}
		return true
	})

	return errs
}

// WithLifetime sets the lifecycle of the servicentry. Default is Singleton.
func WithLifetime(t ServiceLifetime) Option {
	return func(entry *serviceEntry) { entry.lifetime = t }
}

// WithCleanup registers a function to run when the service is cleaned.
func WithCleanup[T any](fn func(context.Context, T) error) Option {
	return func(entry *serviceEntry) {
		entry.cleanup = func(ctx context.Context, v any) error {
			if cast, ok := v.(T); ok {
				return fn(ctx, cast)
			}

			return nil
		}
	}
}

// WithAutoClean controls if this service should be cleaned when its dependencies are cleaned.
// Default is truentry. Set to false to retain state even if dependencies reset.
func WithCleanRecursive(enable bool) Option {
	return func(entry *serviceEntry) { entry.cleanRecursive = enable }
}

// Provide registers a service provider with options.
func Provide[T any](ctx context.Context, provider Provider[T], opts ...Option) error {
	c, ok := ctx.Value(ctxKeyContainerKey).(*containerImpl)
	if !ok {
		return ErrContainerMissing
	}

	e := &serviceEntry{
		provider: func(ctx context.Context) (any, error) { return provider(ctx) },
		lifetime: Singleton,
	}
	for _, opt := range opts {
		opt(e)
	}

	c.services.Store(reflect.TypeFor[T](), e)
	return nil
}

// ProvideValue registers a value provider with options.
func ProvideValue[T any](ctx context.Context, value T) error {
	return Provide(ctx,
		func(context.Context) (T, error) { return value, nil },
		WithLifetime(Singleton),
		WithCleanRecursive(false),
	)
}

// Invoke retrieves a service instance, instantiating it if necessary.
func Invoke[T any](ctx context.Context) (T, error) {
	c, ok := ctx.Value(ctxKeyContainerKey).(*containerImpl)
	if !ok {
		var zero T
		return zero, ErrContainerMissing
	}

	val, err := c.resolve(ctx, reflect.TypeFor[T]())
	if err != nil {
		var zero T
		return zero, err
	}
	return val.(T), nil
}

func MustInvoke[T any](ctx context.Context) T {
	val, e := Invoke[T](ctx)
	if e != nil {
		var zero T
		panic(fmt.Errorf("failed to invoke %T: %w", zero, e))
	}

	return val
}

// Clean resets the state of the specified service and executes its cleanup function.
// It recursively cleans dependent services if they have AutoClean enabled.
func Clean[T any](ctx context.Context) error {
	c, ok := ctx.Value(ctxKeyContainerKey).(*containerImpl)
	if !ok {
		return ErrContainerMissing
	}
	return c.cleanRecursive(ctx, reflect.TypeFor[T](), make(map[reflect.Type]bool))
}

func (c *containerImpl) resolve(ctx context.Context, key reflect.Type) (any, error) {
	val, ok := c.services.Load(key)
	if !ok {
		return nil, fmt.Errorf("%w: %v", ErrServiceNotFound, key)
	}

	entry := val.(*serviceEntry)

	// Cycle detection and graph building
	chain, _ := ctx.Value(ctxKeyResolvingKey).([]reflect.Type)
	if slices.Contains(chain, key) {
		return nil, fmt.Errorf("%w: %v -> %v", ErrCircularDependency, formatChain(chain), key)
	}

	if len(chain) > 0 {
		parent := chain[len(chain)-1]
		m, _ := c.graph.LoadOrStore(key, &sync.Map{})
		m.(*sync.Map).Store(parent, true)
	}

	// Singleton optimization
	if entry.lifetime == Singleton {
		entry.mu.Lock()
		defer entry.mu.Unlock()

		if entry.built {
			return entry.instance, nil
		}
	}

	// Instantiate
	nextCtx := context.WithValue(ctx, ctxKeyResolvingKey, append(chain, key))
	instance, e := entry.provider(nextCtx)
	if e != nil {
		return nil, e
	}

	if entry.lifetime == Singleton {
		entry.instance = instance
		entry.built = true
	}

	return instance, nil
}

func (c *containerImpl) cleanRecursive(ctx context.Context, key reflect.Type, visited map[reflect.Type]bool) error {
	if visited[key] {
		return nil
	}

	visited[key] = true

	val, ok := c.services.Load(key)
	if !ok {
		return nil
	}
	entry := val.(*serviceEntry)

	// Clean current
	entry.mu.Lock()
	if entry.built {
		if entry.cleanup != nil {
			if e := entry.cleanup(ctx, entry.instance); e != nil {
				return fmt.Errorf("%s: %w", key, e)
			}
		}
		entry.instance = nil
		entry.built = false
	}
	entry.mu.Unlock()

	dependents, ok := c.graph.Load(key)
	if !ok {
		return nil
	}

	errs := error(nil)

	dependents.(*sync.Map).Range(func(k, _ any) bool {
		depKey := k.(reflect.Type)

		if depVal, ok := c.services.Load(depKey); !ok ||
			!depVal.(*serviceEntry).cleanRecursive {
			return true
		}

		if e := c.cleanRecursive(ctx, depKey, visited); e != nil {
			errs = errors.Join(errs, fmt.Errorf("%s: %w", depKey, e))
		}

		return true
	})

	return errs
}

func formatChain(chain []reflect.Type) string {
	var sb strings.Builder
	for i, t := range chain {
		if i > 0 {
			sb.WriteString(" -> ")
		}
		sb.WriteString(t.String())
	}
	return sb.String()
}
