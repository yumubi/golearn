// auth 包测试：哈希往返、token 签发与校验、过期、篡改。
// 表驱动三段对应 W04-4 K4。
package auth

import (
	"strings"
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	// 同密码两次哈希盐不同（防彩虹表的最低要求）
	h1, s1, err := Hash("hunter2secret")
	if err != nil {
		t.Fatal(err)
	}
	h2, s2, err := Hash("hunter2secret")
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 || h1 == h2 {
		t.Fatalf("salt/hash should differ per call: salt %q vs %q", s1, s2)
	}
	if !Verify("hunter2secret", h1, s1) {
		t.Fatal("verify should pass with right password")
	}
	if Verify("hunter2secretX", h1, s1) {
		t.Fatal("verify should fail with wrong password")
	}
}

func TestTokenRoundTrip(t *testing.T) {
	key := []byte("test-key-32-bytes-long-aaaaaaaaaaaa")
	tok, err := SignToken(42, time.Hour, key)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt shape: want 3 parts, got %d", len(parts))
	}
	uid, err := ParseToken(tok, key)
	if err != nil || uid != 42 {
		t.Fatalf("parse: uid=%d err=%v", uid, err)
	}
	// 错 key 拒绝
	if _, err := ParseToken(tok, []byte("other-key")); err != ErrTokenInvalid {
		t.Fatalf("wrong key: want ErrTokenInvalid, got %v", err)
	}
	// 篡改 payload 拒绝（签名对不上）
	tampered := parts[0] + "." + tamperB64(parts[1]) + "." + parts[2]
	if _, err := ParseToken(tampered, key); err != ErrTokenInvalid {
		t.Fatalf("tampered: want ErrTokenInvalid, got %v", err)
	}
}

func TestTokenExpiry(t *testing.T) {
	key := []byte("test-key-32-bytes-long-aaaaaaaaaaaa")
	tok, err := SignToken(7, -time.Minute, key) // 已过期
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseToken(tok, key); err != ErrTokenExpired {
		t.Fatalf("want ErrTokenExpired, got %v", err)
	}
}

func tamperB64(s string) string {
	// 翻转负载首字符，制造合法 base64 但内容不同的负载
	if s[0] == 'A' {
		return "B" + s[1:]
	}
	return "A" + s[1:]
}
