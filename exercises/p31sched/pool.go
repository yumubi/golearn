// Package p31sched：P31 并发调度的练习锚点。
//
// 全部测试与正文小节一一对应；Rust 对照写在各考点注释里。
// 包名 p31sched 挂根 module golearn（无独立 go.mod）。
package p31sched

import (
	"errors"
	"sync"
)

// ===== TB35：workerpool（自包含，无 replace）=====

type Task func()

var ErrWorkerPoolFreed = errors.New("workerpool freed")
var ErrNoIdleWorkerInPool = errors.New("no idle worker in pool")

type Pool struct {
	capacity int
	active   chan struct{} // 计数信号量：容量 = capacity
	tasks    chan Task     // 无缓冲：worker 全忙时 Schedule 阻塞（背压）
	wg       sync.WaitGroup
	quit     chan struct{}
	block    bool
}

type Option func(*Pool)

func WithBlock(b bool) Option { return func(p *Pool) { p.block = b } }

const defaultCapacity = 10

func New(capacity int, opts ...Option) *Pool {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	p := &Pool{
		capacity: capacity,
		active:   make(chan struct{}, capacity),
		tasks:    make(chan Task),
		quit:     make(chan struct{}),
		block:    true,
	}
	for _, opt := range opts {
		opt(p)
	}
	go p.run()
	return p
}

func (p *Pool) run() {
	for {
		select {
		case <-p.quit:
			return
		case p.active <- struct{}{}:
			p.wg.Add(1)
			go p.newWorker()
		}
	}
}

func (p *Pool) newWorker() {
	defer func() {
		if r := recover(); r != nil {
			<-p.active
			p.wg.Done()
		}
	}()
	defer func() {
		<-p.active
		p.wg.Done()
	}()
	for {
		select {
		case <-p.quit:
			return
		case t := <-p.tasks:
			t()
		}
	}
}

func (p *Pool) Schedule(t Task) error {
	select {
	case <-p.quit:
		return ErrWorkerPoolFreed
	case p.tasks <- t:
		return nil
	default:
		if !p.block {
			return ErrNoIdleWorkerInPool
		}
		p.tasks <- t
		return nil
	}
}

func (p *Pool) Free() {
	close(p.quit)
	p.wg.Wait()
}

// ===== 共用类型：信号惯用法（正文 §4）=====

type signal struct{}

func spawn(f func() error) <-chan signal {
	c := make(chan signal)
	go func() {
		_ = f()
		c <- signal(struct{}{})
	}()
	return c
}

// channel 替代锁：后台 goroutine 独占状态
type cter struct {
	c chan int
	i int
}

func NewCounter() *cter {
	c := &cter{c: make(chan int)}
	go func() {
		for range c.c {
			c.i++
			c.c <- c.i
		}
	}()
	return c
}

func (c *cter) Increase() int { return <-c.c }
