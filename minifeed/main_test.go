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
	"time"

	"minifeed/counter"
	"minifeed/post"
	"minifeed/rank"
	"minifeed/user"
)

func newTestServer(t *testing.T) *server {
	t.Helper()
	s := &server{
		store:    user.NewMemoryStore(),
		posts:    post.NewMemoryStore(),
		tokenKey: []byte("test-key-32-bytes-long-aaaaaaaaaaaa"),
		log:      discardLogger(),
	}
	s.counters = counter.NewMemoryCounter(nil, time.Hour)
	s.top = rank.New(rankSource{posts: s.posts, cnt: s.counters}, time.Hour.Milliseconds())
	return s
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

// doListJSON：数组响应版 doJSON（/posts 与 /rank 返回数组不是对象）。
func doListJSON(t *testing.T, s *server, path string) []map[string]any {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	newMux(s).ServeHTTP(rec, req)
	var out []map[string]any
	_ = json.NewDecoder(rec.Result().Body).Decode(&out)
	return out
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

// TestPostFlow：发帖 → 读详情 → 点赞 → 计数 → 列表 → 榜单全链（P37 §3/§4/§5 的最小闭环）。
func TestPostFlow(t *testing.T) {
	s := newTestServer(t)

	// 建两个用户拿 token
	tokens := []string{}
	for _, em := range []string{"a@b.c", "d@e.f"} {
		if res, _ := doJSON(t, s, "POST", "/users", `{"email":"`+em+`","nick":"n","password":"hunter2secret"}`, ""); res.StatusCode != http.StatusCreated {
			t.Fatalf("signup %s", em)
		}
		_, out := doJSON(t, s, "POST", "/login", `{"email":"`+em+`","password":"hunter2secret"}`, "")
		tok, _ := out["token"].(string)
		tokens = append(tokens, tok)
	}

	// 无 token 发帖 401
	if res, _ := doJSON(t, s, "POST", "/posts", `{"title":"hi","content":"c"}`, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token post: want 401 got %d", res.StatusCode)
	}
	// 空标题 400
	if res, _ := doJSON(t, s, "POST", "/posts", `{"title":"  ","content":"c"}`, tokens[0]); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty title: want 400 got %d", res.StatusCode)
	}
	// 发帖 201，ID 回填
	_, out := doJSON(t, s, "POST", "/posts", `{"title":"first","content":"c1"}`, tokens[0])
	if id, _ := out["id"].(float64); id != 1 {
		t.Fatalf("post id: %v", out)
	}
	_, out = doJSON(t, s, "POST", "/posts", `{"title":"second","content":"c2"}`, tokens[1])
	if id, _ := out["id"].(float64); id != 2 {
		t.Fatalf("post2 id: %v", out)
	}

	// 详情公开可读（无需 token），likes 初值 0
	res, out := doJSON(t, s, "GET", "/posts/1", "", "")
	if res.StatusCode != http.StatusOK || out["title"] != "first" || out["likes"] != float64(0) {
		t.Fatalf("get post: %d %v", res.StatusCode, out)
	}
	// 不存在的帖 404
	if res, _ := doJSON(t, s, "GET", "/posts/99", "", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("get 99: want 404 got %d", res.StatusCode)
	}

	// 点赞：3 次 like + 1 次 unlike = 2（计数只动内存）
	for range 3 {
		if res, _ := doJSON(t, s, "POST", "/posts/1/like", "", tokens[0]); res.StatusCode != http.StatusOK {
			t.Fatalf("like: want 200 got %d", res.StatusCode)
		}
	}
	if res, _ := doJSON(t, s, "POST", "/posts/1/unlike", "", tokens[1]); res.StatusCode != http.StatusOK {
		t.Fatalf("unlike: want 200 got %d", res.StatusCode)
	}
	_, out = doJSON(t, s, "GET", "/posts/1", "", "")
	if out["likes"] != float64(2) {
		t.Fatalf("likes: want 2 got %v", out["likes"])
	}
	// 给帖 2 点 5 个赞（榜单排序素材）
	for range 5 {
		_, _ = doJSON(t, s, "POST", "/posts/2/like", "", tokens[0])
	}

	// 列表分页：n=1 offset=0 只见帖 1
	list := doListJSON(t, s, "/posts?n=1&offset=0")
	if len(list) != 1 || list[0]["title"] != "first" {
		t.Fatalf("list page1: %v", list)
	}

	// 榜单：帖 2（5 赞）在帖 1（2 赞）前
	ranks := doListJSON(t, s, "/rank")
	if len(ranks) != 2 || ranks[0]["PostID"] != float64(2) || ranks[1]["PostID"] != float64(1) {
		t.Fatalf("rank order: %v", ranks)
	}

	// 不存在的帖点赞 404
	if res, _ := doJSON(t, s, "POST", "/posts/99/like", "", tokens[0]); res.StatusCode != http.StatusNotFound {
		t.Fatalf("like 99: want 404 got %d", res.StatusCode)
	}
}
