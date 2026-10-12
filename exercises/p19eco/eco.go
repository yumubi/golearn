// Package p19eco：P19 生态章的练习锚点。
//
// Level 演示 stringer 的 go:generate 流程（先 //go:generate 注释，
// 再 go generate ./exercises/p19eco/，产物 level_string.go 进 git）。
// ServerOption 演示 functional options（对照 Rust builder）。
package p19eco

import (
	"context"
	"log/slog"
	"time"
)

// Level 是 io 类分级枚举（P1.5 §2 的 iota 在生态里的样子）。
//
//go:generate stringer -type=Level -linecomment
type Level int

const (
	Debug Level = iota // debug
	Info               // info
	Warn               // warn
	Error              // error
)

// ===== functional options（P19 章末参考节的可运行版）=====

// Server 是配置增长的演示对象。
type Server struct {
	addr    string
	timeout time.Duration
	logger  *slog.Logger
}

// Option 是配置函数：接收者可变，逐个应用。
type Option func(*Server)

// WithTimeout 覆盖默认超时。
func WithTimeout(d time.Duration) Option { return func(s *Server) { s.timeout = d } }

// WithLogger 注入 slog（生态库全部长在标准库接口上的示例）。
func WithLogger(l *slog.Logger) Option { return func(s *Server) { s.logger = l } }

// NewServer 默认值在先，options 覆盖在后。
func NewServer(addr string, opts ...Option) *Server {
	s := &Server{
		addr:    addr,
		timeout: 5 * time.Second,                // 默认值
		logger:  slog.Default(),                 // 默认值
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Addr 只读访问器（P2.5：不加 Get）。
func (s Server) Addr() string { return s.addr }

// Timeout 只读访问器。
func (s Server) Timeout() time.Duration { return s.timeout }

// Logger 只读访问器。
func (s Server) Logger() *slog.Logger { return s.logger }

// Ping 演示 ctx 第一参数惯例（生态契合度的信号之一）。
func (s Server) Ping(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(s.timeout):
		return nil // 演示用途：超时即成功
	}
}
