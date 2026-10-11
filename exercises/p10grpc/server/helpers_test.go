package server

import (
	"net"
	"sync"

	"google.golang.org/grpc/status"
)

// ioConn：bufconn.DialContext 返回 net.Conn。
type ioConn = net.Conn

type syncWG = sync.WaitGroup

func statusCode(err error) uint32 {
	if err == nil {
		return 0
	}
	return uint32(status.Code(err))
}
