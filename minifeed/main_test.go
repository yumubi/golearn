// server 层测试：httptest 直打 mux（P36 正章的 handler 测试形态）。
// 内存 store + 固定 token key，不碰网络端口。
package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"minifeed/user"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	return &server{
		store:    user.NewMemoryStore(),
		tokenKey: []byte("test-key-32-bytes-long-aaaaaaaaaaaa"),
		log:      discardLogger(),
	}
}

func doJSON(t *testing.T, s *server, method, path, body, token string) (*http.Response, map[string]any) {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	newMux(s).ServeHTTP(rec, req)
	res := rec.Result()
	var out map[string]any
	if res.Header.Get("Content-Type") == "application/json" {
		_ = json.NewDecoder(res.Body).Decode(&out)
	}
	return res, out
}

// discardLogger 静音 slog；测试不看日志。
func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestSignupLoginProfile(t *testing.T) {
	s := newTestServer(t)

	// 注册成功 201
	res, out := doJSON(t, s, "POST", "/users", `{"email":"a@b.c","nick":"alice","password":"hunter2secret"}`, "")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("signup: want 201, got %d (%v)", res.StatusCode, out)
	}

	// 重复注册 409
	if res, _ := doJSON(t, s, "POST", "/users", `{"email":"a@b.c","nick":"x","password":"hunter2secret"}`, ""); res.StatusCode != http.StatusConflict {
		t.Fatalf("dup signup: want 409, got %d", res.StatusCode)
	}

	// 弱密码 400
	if res, _ := doJSON(t, s, "POST", "/users", `{"email":"c@d.e","nick":"x","password":"short"}`, ""); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("weak password: want 400, got %d", res.StatusCode)
	}

	// 登录拿 token
	res, out = doJSON(t, s, "POST", "/login", `{"email":"a@b.c","password":"hunter2secret"}`, "")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login: want 200, got %d (%v)", res.StatusCode, out)
	}
	token, _ := out["token"].(string)
	if token == "" {
		t.Fatal("login should return token")
	}

	// 错密码 401，且与"没这人"响应一致（防枚举）
	if res, _ := doJSON(t, s, "POST", "/login", `{"email":"a@b.c","password":"wrongpassword"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password: want 401, got %d", res.StatusCode)
	}
	if res, _ := doJSON(t, s, "POST", "/login", `{"email":"ghost@x.y","password":"wrongpassword"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ghost login: want 401, got %d", res.StatusCode)
	}

	// 无 token 访问 profile 401
	if res, _ := doJSON(t, s, "GET", "/users/1", "", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: want 401, got %d", res.StatusCode)
	}

	// 带 token 看自己 200
	res, out = doJSON(t, s, "GET", "/users/1", "", token)
	if res.StatusCode != http.StatusOK || out["nick"] != "alice" {
		t.Fatalf("profile: want 200 alice, got %d (%v)", res.StatusCode, out)
	}

	// 看别人 403
	if res, _ := doJSON(t, s, "GET", "/users/2", "", token); res.StatusCode != http.StatusForbidden {
		t.Fatalf("other profile: want 403, got %d", res.StatusCode)
	}
}

func TestBadJSON(t *testing.T) {
	s := newTestServer(t)
	if res, _ := doJSON(t, s, "POST", "/users", `{"email":`, ""); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", res.StatusCode)
	}
}
