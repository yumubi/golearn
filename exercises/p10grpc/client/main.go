package main

// Step 2：客户端。同一家生成的 stub，四种模式各自的调用姿势。
// 运行：go run ./exercises/p10grpc/client/   （先起 server：go run ./exercises/p10grpc/）

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"golearn/exercises/p10grpc/kvpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	// insecure = 明文；生产用 tls.NewCredentials()。对照 tonic: transport::Channel
	conn, err := grpc.NewClient("127.0.0.1:9090",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	kv := kvpb.NewKVClient(conn) // ≈ KvClient::new(channel)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 一元
	if _, err := kv.Put(ctx, &kvpb.PutRequest{Key: "lang", Value: "go"}); err != nil {
		log.Fatal(err)
	}
	got, _ := kv.Get(ctx, &kvpb.GetRequest{Key: "lang"})
	fmt.Printf("unary  Get(lang) = %q found=%v\n", got.Value, got.Found)

	// 服务端流：订阅，另一协程改值，主循环收 2 个事件
	wstream, err := kv.Watch(ctx, &kvpb.WatchRequest{Key: "lang"})
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		for _, v := range []string{"go1", "go2"} {
			kv.Put(ctx, &kvpb.PutRequest{Key: "lang", Value: v})
			time.Sleep(50 * time.Millisecond)
		}
	}()
	n := 0
	for n < 3 {
		ev, err := wstream.Recv()
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("stream Watch lang = %q v%d\n", ev.Value, ev.Version)
		n++
	}

	// 客户端流：发 3 条，收一个汇总
	cstream, err := kv.BulkSet(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for i := range 3 {
		cstream.Send(&kvpb.PutRequest{Key: fmt.Sprintf("k%d", i), Value: "v"})
	}
	sum, err := cstream.CloseAndRecv() // CloseSend + Recv 合一；≈ RequestStream::message 之后CompletableFuture
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("client-stream BulkSet count = %d\n", sum.Count)

	// 双向流：发一条收一条（echo）
	bstream, err := kv.Chat(ctx)
	if err != nil {
		log.Fatal(err)
	}
	bstream.Send(&kvpb.ChatMsg{Text: "hello bidi"})
	reply, err := bstream.Recv()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("bidi Chat echo = %q\n", reply.Text)
	bstream.CloseSend()
	// 读走剩余再关
	for {
		_, err := bstream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("client done")
}
