// Command p10grpc 服务端入口：注册 KV 服务 + 挂日志拦截器，监听 :9090。
// 运行：go run ./exercises/p10grpc/   另开终端跑 client：go run ./exercises/p10grpc/client/
package main

import (
	"log"
	"net"

	"golearn/exercises/p10grpc/kvpb"
	"golearn/exercises/p10grpc/server"
	"google.golang.org/grpc"
)

func main() {
	lis, err := net.Listen("tcp", "127.0.0.1:9090")
	if err != nil {
		log.Fatal(err)
	}
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(server.LoggingUnaryInterceptor), // ≈ tower .layer()
	)
	kvpb.RegisterKVServer(srv, server.New()) // ≈ KvServer::new().serve(lis)
	log.Printf("kv grpc on :9090")
	if err := srv.Serve(lis); err != nil { // Serve 自带优雅关停接口 GracefulStop()
		log.Fatal(err)
	}
}
