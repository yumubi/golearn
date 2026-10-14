// Package auth：密码哈希与 token 签发。教学选型 sha256(salt||password)（生产换 bcrypt）；HS256 JWT 手写。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Hash 返回 hex 哈希与 hex 盐，盐每用户随机 16 字节（防彩虹表的最低要求）。
func Hash(password string) (string, string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", "", err
	}
	return hashHexOf(password, salt), hex.EncodeToString(salt), nil
}

// hashHexOf 计算；Verify 用它重算并恒时比较（hex 串比，省一次解码）。
func hashHexOf(password string, salt []byte) string {
	sum := sha256.Sum256(append(salt, password...))
	return hex.EncodeToString(sum[:])
}

func Verify(password, hashHex, saltHex string) bool {
	salt, err := hex.DecodeString(saltHex)
	return err == nil && hmac.Equal([]byte(hashHexOf(password, salt)), []byte(hashHex))
}

// Claims 只放 uid 与过期时间；负载是 base64 不是加密，不放敏感信息（W03-1 K15）。
type Claims struct {
	UID int64 `json:"uid"`
	Exp int64 `json:"exp"`
}

var ErrTokenInvalid = errors.New("token invalid")
var ErrTokenExpired = errors.New("token expired") // 签名对但过期：登出/续期讨论入口

const jwtHeader = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9" // {"alg":"HS256","typ":"JWT"} 的 b64url

// SignToken 签发 HS256 JWT：b64url(header).b64url(payload).b64url(mac)。
func SignToken(uid int64, ttl time.Duration, key []byte) (string, error) {
	if len(key) == 0 {
		return "", errors.New("auth: empty key")
	}
	raw, err := json.Marshal(Claims{UID: uid, Exp: time.Now().Add(ttl).Unix()})
	if err != nil {
		return "", err
	}
	payload := b64url(raw)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(jwtHeader + "." + payload))
	return jwtHeader + "." + payload + "." + b64url(mac.Sum(nil)), nil
}

// ParseToken 校验签名与过期。结构/签名错统一 ErrTokenInvalid。
func ParseToken(token string, key []byte) (int64, error) {
	h, rest, _ := strings.Cut(token, ".")
	p, s, _ := strings.Cut(rest, ".")
	sig, sigErr := unb64url(s)
	raw, rawErr := unb64url(p)
	var c Claims
	jsonErr := json.Unmarshal(raw, &c)
	m := hmac.New(sha256.New, key)
	m.Write([]byte(h + "." + p))
	if sigErr != nil || rawErr != nil || jsonErr != nil || !hmac.Equal(sig, m.Sum(nil)) {
		return 0, ErrTokenInvalid
	}
	if c.Exp <= time.Now().Unix() {
		return 0, ErrTokenExpired
	}
	return c.UID, nil
}

func b64url(b []byte) string            { return base64.RawURLEncoding.EncodeToString(b) }
func unb64url(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }
