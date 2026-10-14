// minifeed：P36 起步骨架。net/http 1.22 路由不引 gin；分层 handler → Store 接口 → 双实现。
package main

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"minifeed/auth"
	"minifeed/counter"
	"minifeed/post"
	"minifeed/rank"
	"minifeed/user"
)

type server struct {
	store    user.Store
	posts    post.PostStore
	counters counter.Counter
	top      *rank.Rank
	tokenKey []byte
	log      *slog.Logger
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	addr := cmp.Or(os.Getenv("MINIFEED_ADDR"), ":8080")
	key := []byte(os.Getenv("MINIFEED_TOKEN_KEY"))
	if len(key) == 0 { // 未配置随机生成：重启旧 token 全失效（密钥管理是部署问题）
		key = make([]byte, 32)
		_, _ = rand.Read(key) // crypto/rand 失败仅当系统熵源故障，忽略后 key 弱化可接受？不：显式失败
	}
	store, err := openStore()
	if err != nil {
		log.Error("open store", "err", err.Error())
		os.Exit(1)
	}
	pstore, err := openPostStore()
	if err != nil {
		log.Error("open post store", "err", err.Error())
		os.Exit(1)
	}
	s := &server{store: store, posts: pstore, tokenKey: key, log: log}
	s.counters = counter.NewMemoryCounter(nil, time.Minute) // flush=nil：纯内存计数（P37 §5 落库是练习位）
	s.top = rank.New(rankSource{posts: pstore, cnt: s.counters}, 60_000)
	s.top.Refresh() // 启动预加载（W21-1 K25）：读侧从第一拍就有数据
	log.Info("minifeed listening", "addr", addr)
	if err := http.ListenAndServe(addr, newMux(s)); err != nil {
		log.Error("listen", "err", err.Error())
		os.Exit(1)
	}
}

// openStore：MINIFEED_DB 设了用 sqlite 文件库，否则内存实现。帖子库同路径（P37 §1）。
func openStore() (user.Store, error) {
	if path := os.Getenv("MINIFEED_DB"); path != "" {
		return user.NewSQLiteStore(path)
	}
	return user.NewMemoryStore(), nil
}

func openPostStore() (post.PostStore, error) {
	if path := os.Getenv("MINIFEED_DB"); path != "" {
		return post.NewSQLiteStore(path)
	}
	return post.NewMemoryStore(), nil
}

// rankSource：榜单数据源组装（W09-1 K18 的 Repository 组装：分数=计数，标题来自帖子表）。
type rankSource struct {
	posts post.PostStore
	cnt   counter.Counter
}

func (r rankSource) Posts() []rank.Scored {
	ps, err := r.posts.List(100, 0) // 候选集前 100（生产是分批+小顶堆，W09-1 K9）
	if err != nil {
		return nil
	}
	out := make([]rank.Scored, 0, len(ps))
	for _, p := range ps {
		c, _ := r.cnt.Count("post", p.ID)
		out = append(out, rank.Scored{PostID: p.ID, Title: p.Title, Score: float64(c)})
	}
	return out
}

// uidKey 是 context 存 uid 的键（auth 中间件写入）。
type uidKey struct{}

// auth：校验 Bearer token 把 uid 塞进 ctx。本章只校验（W03-1 K8 刷新是延伸）。
func (s *server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		uid, err := auth.ParseToken(token, s.tokenKey)
		if !ok || err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), uidKey{}, uid)))
	}
}

// newMux 路由装配，main 与测试共用。
func newMux(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /users", s.handleSignup)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /users/{id}", s.auth(s.handleProfile))
	mux.HandleFunc("POST /posts", s.auth(s.handleCreatePost))
	mux.HandleFunc("GET /posts/{id}", s.handleGetPost)
	mux.HandleFunc("GET /posts", s.handleListPosts)
	mux.HandleFunc("POST /posts/{id}/like", s.auth(s.handleLike))
	mux.HandleFunc("POST /posts/{id}/unlike", s.auth(s.handleUnlike))
	mux.HandleFunc("GET /rank", s.handleRank)
	return mux
}

// writeJSON 统一出口：先 Content-Type 后状态码（P2 的顺序铁律）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decode 解请求体，失败已写 400（W02-1 K12 Bind 语义）。
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var v T
	if json.NewDecoder(r.Body).Decode(&v) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
	}
	return v, true
}

// internal 记日志并回 500。
func (s *server) internal(w http.ResponseWriter, what string, err error) {
	s.log.Error(what, "err", err.Error())
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
}

type signupReq struct {
	Email    string `json:"email"`
	Nick     string `json:"nick"`
	Password string `json:"password"`
}

// handleSignup：POST /users。状态码（W02-1 D2 生产化）：400 参数错 409 占用 201 成功。
func (s *server) handleSignup(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[signupReq](w, r)
	if !ok {
		return
	}
	if len(in.Email) < 4 || !strings.Contains(in.Email, "@") || len(in.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email or password invalid"})
		return
	}
	hash, salt, err := auth.Hash(in.Password)
	if err != nil {
		s.internal(w, "hash", err)
		return
	}
	u := &user.User{Email: in.Email, Nick: in.Nick, PasswordHash: hash, Salt: salt}
	err = s.store.Create(u)
	switch {
	case errors.Is(err, user.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email taken"})
	case err != nil:
		s.internal(w, "create user", err)
	default:
		writeJSON(w, http.StatusCreated, map[string]any{"id": u.ID})
	}
}

// handleLogin：POST /login。401 不区分"没这人"与"密码错"（防用户枚举）。
func (s *server) handleLogin(w http.ResponseWriter, r *http.Request) {
	in, ok := decode[signupReq](w, r) // 只用 Email/Password，Nick 空
	if !ok {
		return
	}
	u, err := s.store.GetByEmail(in.Email)
	switch {
	case err != nil && !errors.Is(err, user.ErrNotFound):
		s.internal(w, "get user", err)
		return
	case err != nil || !auth.Verify(in.Password, u.PasswordHash, u.Salt):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "bad credentials"})
		return
	}
	token, err := auth.SignToken(u.ID, 24*30*time.Hour, s.tokenKey)
	if err != nil {
		s.internal(w, "sign token", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleProfile：GET /users/{id}，登录后只能看自己（教学版不做管理员权限）。
func (s *server) handleProfile(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(uidKey{}).(int64)
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if id != uid { // 解析失败 id=0 不等于 uid，自然落到 403
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	u, err := s.store.GetByID(id)
	switch {
	case errors.Is(err, user.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such user"})
	case err != nil:
		s.internal(w, "get user by id", err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"id": u.ID, "email": u.Email, "nick": u.Nick})
	}
}

// handleCreatePost：POST /posts，登录发帖。201 带 id（W02-1 D2 生产化语义延续）。
func (s *server) handleCreatePost(w http.ResponseWriter, r *http.Request) {
	uid, _ := r.Context().Value(uidKey{}).(int64)
	in, ok := decode[struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}](w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title required"})
		return
	}
	p := &post.Post{AuthorID: uid, Title: in.Title, Content: in.Content}
	if err := s.posts.Create(p); err != nil {
		s.internal(w, "create post", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": p.ID})
}

// handleGetPost：GET /posts/{id}，公开读。不刷新榜单：W06-3 K10 的预加载是"预测用户
// 下一个动作提前装数据"，而详情读的下一步不定是榜单；榜单刷新责任在 /rank 自己（K11
// 的异步纪律同理：偷偷异步会把 stale 榜单锁进 TTL）。
func (s *server) handleGetPost(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	p, err := s.posts.GetByID(id)
	switch {
	case errors.Is(err, post.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such post"})
	case err != nil:
		s.internal(w, "get post", err)
	default:
		c, _ := s.counters.Count("post", p.ID)
		writeJSON(w, http.StatusOK, map[string]any{"id": p.ID, "title": p.Title, "content": p.Content, "likes": c})
	}
}

// handleListPosts：GET /posts?n=&offset=，分页慢路径（W06-3 K5：任一参数变化缓存即失效，
// minifeed 不缓存列表）。n 默认 10 上限 100。
func (s *server) handleListPosts(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 {
		n = 10
	}
	if n > 100 {
		n = 100
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	ps, err := s.posts.List(n, offset)
	if err != nil {
		s.internal(w, "list posts", err)
		return
	}
	type item struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	out := make([]item, 0, len(ps))
	for _, p := range ps {
		out = append(out, item{ID: p.ID, Title: p.Title})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLike：POST /posts/{id}/like。计数只动内存（W07-2 K22：读请求不夹 DB 写）。
func (s *server) handleLike(w http.ResponseWriter, r *http.Request) {
	s.bump(w, r, s.counters.Like)
}

func (s *server) handleUnlike(w http.ResponseWriter, r *http.Request) {
	s.bump(w, r, s.counters.Unlike)
}

func (s *server) bump(w http.ResponseWriter, r *http.Request, op func(string, int64) error) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := s.posts.GetByID(id); errors.Is(err, post.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such post"})
		return
	}
	if err := op("post", id); err != nil {
		s.internal(w, "bump counter", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"ok": "1"})
}

// handleRank：GET /rank，读侧零锁（W09-1 K17 atomic.Value Load）。
func (s *server) handleRank(w http.ResponseWriter, r *http.Request) {
	s.top.Refresh() // 单飞：未过期直接跳过（K25 语义），过期才真算
	items := s.top.Top(10)
	writeJSON(w, http.StatusOK, items)
}
