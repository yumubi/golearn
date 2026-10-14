package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// TestSIGTERMGracefulExit Q1：SIGTERM 触发优雅关停，进程 5 秒内退出且退出码 0。
//
// 做法：自重启（exec.Command(os.Args[0])）子进程并设环境变量
// DNS_SIGTERM_CHILD=1，子进程走真实 main()（signal.NotifyContext 生效路径），
// 在空闲端口起 Run；父进程用真实 DNS 查询探活（UDP "connect" 不校验端口，
// 会被内核直接接受，不能当就绪信号），就绪后发 SIGTERM，5 秒 deadline
// 内等退出。不固定 sleep 猜时机：就绪判定 = 查询得到应答或上游黑洞时
// 端口确实开始收包（用 sendto 的系统调用错误与进程存活联合判断）。
func TestSIGTERMGracefulExit(t *testing.T) {
	if os.Getenv("DNS_SIGTERM_CHILD") == "1" {
		// 子进程分支：走真实 main()，loadConfig 读 env。
		_ = os.Setenv("DNS_LISTEN", "127.0.0.1:"+os.Getenv("SIGTERM_TEST_PORT"))
		main()
		os.Exit(0)
	}
	upAddr, _ := fakeUpstream(t)
	port := freePort(t)
	cmd := exec.Command(os.Args[0], "-test.run=TestSIGTERMGracefulExit")
	cmd.Env = append(os.Environ(),
		"DNS_SIGTERM_CHILD=1",
		"SIGTERM_TEST_PORT="+strconv.Itoa(port),
		"DNS_UPSTREAM="+upAddr.String(), // 本地假上游：探活回包快且确定
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// 就绪判定：UDP 无连接，DialUDP 对未监听端口也成功，不能当探活。
	// 用"进程活着 + DNS 查询得到应答"做真探活：子进程 Run 起来后首个查询
	// 经假上游回包，回包到达 = 监听口在工作（upAddr 已在 cmd.Env 前取好）。
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("client listen: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	proxyAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}

	deadline := time.Now().Add(5 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		if _, err := client.WriteToUDP(dnsQuery(7, "ready.example"), proxyAddr); err != nil {
			t.Fatalf("probe write: %v", err)
		}
		_ = client.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buf := make([]byte, 512)
		if n, _, err := client.ReadFromUDP(buf); err == nil && n >= 12 {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("child never became ready (no DNS response in 5s)")
	}

	// 发 SIGTERM，等优雅退出：5 秒内、退出码 0（不是被信号杀死）。
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()
	select {
	case err := <-waitCh:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				t.Fatalf("child was killed by signal, not graceful exit: %v", ee)
			}
			if code := ee.ExitCode(); code != 0 {
				t.Fatalf("want exit 0 after SIGTERM, got %d", code)
			}
			return
		}
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("child did not exit within 5s after SIGTERM")
	}
}
