package server

// 第 10 章练习验收：四种流的集成测试（bufconn 进程内，不占端口，-race 干净）。
// 对照 tonic: tower::ServiceBuilder + tokio::spawn 测试。

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"golearn/exercises/p10grpc/kvpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1 << 20

// newTestKV 起一个 bufconn 服务端，返回已连上的客户端。
// bufconn = 内存网络，绕过 TCP——单元测试标准姿势（≈ tonic 的 in-memory channel）。
func newTestKV(t *testing.T) kvpb.KVClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	kvpb.RegisterKVServer(srv, New())
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (netConn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return kvpb.NewKVClient(conn)
}

// bufconn 拨号返回类型别名（google.golang.org/grpc/test/bufconn 拨的是 net.Conn）。
type netConn = ioConn

func TestUnary(t *testing.T) {
	kv := newTestKV(t)
	ctx := context.Background()

	rep, err := kv.Put(ctx, &kvpb.PutRequest{Key: "a", Value: "1"})
	if err != nil || rep.Version != 1 {
		t.Fatalf("Put = %v, %v", rep, err)
	}
	got, err := kv.Get(ctx, &kvpb.GetRequest{Key: "a"})
	if err != nil || !got.Found || got.Value != "1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	// 未命中：found=false 而非错误（协议设计选择，教程正文讲过）
	miss, _ := kv.Get(ctx, &kvpb.GetRequest{Key: "zz"})
	if miss.Found {
		t.Fatal("miss should not be found")
	}
	// 校验错误走 status：空 key → InvalidArgument
	_, err = kv.Put(ctx, &kvpb.PutRequest{Key: "", Value: "x"})
	if st := statusCode(err); st != 3 { // codes.InvalidArgument == 3
		t.Fatalf("want InvalidArgument(3), got %v", err)
	}
}

func TestServerStreamWatch(t *testing.T) {
	kv := newTestKV(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := kv.Watch(ctx, &kvpb.WatchRequest{Key: "w"})
	if err != nil {
		t.Fatal(err)
	}
	// 订阅快照：当前无值，第一条事件应来自下面的 Put
	go func() {
		time.Sleep(50 * time.Millisecond)
		kv.Put(ctx, &kvpb.PutRequest{Key: "w", Value: "v1"})
		kv.Put(ctx, &kvpb.PutRequest{Key: "w", Value: "v2"})
	}()

	var versions []int64
	for len(versions) < 2 {
		ev, err := stream.Recv()
		if err != nil {
			t.Fatalf("recv after %v: %v", versions, err)
		}
		versions = append(versions, ev.Version)
	}
	if versions[0] >= versions[1] {
		t.Fatalf("watch versions not monotonic: %v", versions)
	}
}

func TestClientStreamBulkSet(t *testing.T) {
	kv := newTestKV(t)
	ctx := context.Background()

	stream, err := kv.BulkSet(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := stream.Send(&kvpb.PutRequest{Key: fmt.Sprintf("b%d", i), Value: "v"}); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := stream.CloseAndRecv()
	if err != nil || rep.Count != 5 {
		t.Fatalf("BulkSet = %+v, %v", rep, err)
	}
}

func TestBidiChat(t *testing.T) {
	kv := newTestKV(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := kv.Chat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// 收发并行：3 发 3 收，校验回声顺序
	go func() {
		for i := range 3 {
			if err := stream.Send(&kvpb.ChatMsg{Text: fmt.Sprintf("m%d", i)}); err != nil {
				t.Error(err)
			}
		}
		if err := stream.CloseSend(); err != nil {
			t.Error(err)
		}
	}()
	for i := 1; i <= 3; i++ {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("%03d echo: m%d", i, i-1); msg.Text != want {
			t.Fatalf("echo %d = %q, want %q", i, msg.Text, want)
		}
	}
}

func TestConcurrentUnary(t *testing.T) {
	kv := newTestKV(t)
	ctx := context.Background()
	var wg syncWG
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := kv.Put(ctx, &kvpb.PutRequest{Key: fmt.Sprintf("c%d", i), Value: "v"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
}
