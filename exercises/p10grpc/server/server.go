// Package server 是第 10 章的"读代码"部分：KV 服务端，四种流全实现。
// 每个方法前的注释 = 教程正文对应小节，先读注释再读代码。
package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"golearn/exercises/p10grpc/kvpb"
)

// KVServer 实现 kvpb.KVServer（生成接口）。
// Go 的重要约定：**不内嵌 UnimplementedKVServer**——那是给向前兼容用的
// （老二进制配新 proto 不 panic），练习里内嵌它会把"方法漏实现"从编译错误
// 变成运行时 codes.Unimplemented，掩盖错误。教程项目要编译期暴露问题。
type KVServer struct {
	kvpb.UnimplementedKVServer // 见上：理解了再决定留不留；生产常保留

	mu sync.RWMutex
	m  map[string]val

	// watch 订阅者表：每人一个 buffered chan，广播时非阻塞投递（慢消费者丢事件）
	subsMu sync.Mutex
	subs   map[chan *kvpb.WatchEvent]struct{}
	// version 单调递增，Put 时 +1，Watch 事件带上
	version int64
}

type val struct {
	s   string
	ver int64
}

func New() *KVServer {
	return &KVServer{
		m:    map[string]val{},
		subs: map[chan *kvpb.WatchEvent]struct{}{},
	}
}

// ---- 一元：Put / Get ----

// Put：一元调用。签名三件套 (ctx, req) -> (resp, error)。
// 对照 tonic: async fn put(&self, req: Request<PutRequest>) -> Result<Response<PutReply>, Status>
// Go 没有 Result：错误就是 error；gRPC 状态错误用 status.Errorf 包。
func (s *KVServer) Put(ctx context.Context, req *kvpb.PutRequest) (*kvpb.PutReply, error) {
	if req.Key == "" {
		// ≈ Err(Status::invalid_argument("key required"))
		return nil, status.Errorf(codes.InvalidArgument, "key required")
	}
	s.mu.Lock()
	s.version++
	v := val{s: req.Value, ver: s.version}
	s.m[req.Key] = v
	ver := v.ver
	s.mu.Unlock()

	s.broadcast(&kvpb.WatchEvent{Key: req.Key, Value: req.Value, Version: ver})
	return &kvpb.PutReply{Version: ver}, nil
}

// Get：注意 found 字段——Go 没有 Option，协议层面表达"存在性"。
func (s *KVServer) Get(ctx context.Context, req *kvpb.GetRequest) (*kvpb.GetReply, error) {
	s.mu.RLock()
	v, ok := s.m[req.Key]
	s.mu.RUnlock()
	return &kvpb.GetReply{Value: v.s, Found: ok}, nil
}

// ---- 服务端流：Watch ----

// Watch：服务端流。req 进，stream 出——在 stream 上循环 Send 直到 ctx 取消。
// 对照 tonic: type WatchStream = ResponseStream<WatchEvent>
// Go 的关键点：**stream.Send 返回错误必须检查**，客户端断开时 Send 报
// codes.Canceled/Unavailable，此时 return 让 goroutine 退出。
func (s *KVServer) Watch(req *kvpb.WatchRequest, stream kvpb.KV_WatchServer) error {
	ch := make(chan *kvpb.WatchEvent, 16)
	s.subsMu.Lock()
	s.subs[ch] = struct{}{}
	s.subsMu.Unlock()
	defer func() {
		s.subsMu.Lock()
		delete(s.subs, ch)
		s.subsMu.Unlock()
	}()

	// 先发当前值，语义 = "订阅即快照 + 增量"
	s.mu.RLock()
	if v, ok := s.m[req.Key]; ok {
		ev := &kvpb.WatchEvent{Key: req.Key, Value: v.s, Version: v.ver}
		if err := stream.Send(ev); err != nil {
			s.mu.RUnlock()
			return err
		}
	}
	s.mu.RUnlock()

	for {
		select {
		case ev := <-ch:
			if ev.Key != req.Key { // 教程版：全局广播，这里过滤
				continue
			}
			if err := stream.Send(ev); err != nil {
				return err // 客户端断开 → 退出，defer 清订阅
			}
		case <-stream.Context().Done(): // ≈ tonic 里 drop 掉 stream 触发的取消
			return stream.Context().Err()
		}
	}
}

// ---- 客户端端流：BulkSet ----

// BulkSet：客户端流。stream 进，最后 Resp 出——循环 Recv 收完整批再回复。
// 对照 tonic: type BulkSetStream = RequestStream<PutRequest>
func (s *KVServer) BulkSet(stream kvpb.KV_BulkSetServer) error {
	count := int32(0)
	for {
		req, err := stream.Recv()
		if err == io.EOF { // 客户端 CloseSend → 正常收尾
			return stream.SendAndClose(&kvpb.BulkSetReply{Count: count})
		}
		if err != nil {
			return err
		}
		if _, err := s.Put(stream.Context(), req); err != nil {
			return err
		}
		count++
	}
}

// ---- 双向流：Chat ----

// Chat：双向流。Recv/Send 各自独立循环——Go 里通常"读循环里顺手写回"，
// 而不是 tonic 那种 split() 成两半。本实现做回声+计数前缀，演示边收边发。
func (s *KVServer) Chat(stream kvpb.KV_ChatServer) error {
	n := int64(0)
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		n++
		if err := stream.Send(&kvpb.ChatMsg{Text: fmt.Sprintf("%03d echo: %s", n, msg.Text)}); err != nil {
			return err
		}
	}
}

// broadcast 给所有订阅者非阻塞投递（慢消费者丢弃事件——教程版足够）。
func (s *KVServer) broadcast(ev *kvpb.WatchEvent) {
	s.subsMu.Lock()
	defer s.subsMu.Unlock()
	for ch := range s.subs {
		select {
		case ch <- ev:
		default: // 满了丢事件：watch 语义里"最新"比"全量"重要
		}
	}
}

// ---- 拦截器（教程第 10.6 节的配套代码）----

// LoggingUnaryInterceptor 一元拦截器：方法名 + 错误日志。
// 对照 tonic: tower::Layer/Interceptor——Go 是普通函数，签名固定。
func LoggingUnaryInterceptor(
	ctx context.Context, req any,
	info *grpc.UnaryServerInfo, handler grpc.UnaryHandler,
) (any, error) {
	resp, err := handler(ctx, req)
	log.Printf("%s err=%v", info.FullMethod, err)
	return resp, err
}
