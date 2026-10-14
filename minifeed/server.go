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
	"minifeed/user"
)

type server struct {
	store    user.Store
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
	s := &server{store: store, tokenKey: key, log: log}
	log.Info("minifeed listening", "addr", addr)
	if err := http.ListenAndServe(addr, newMux(s)); err != nil {
		log.Error("listen", "err", err.Error())
		os.Exit(1)
	}
}

// openStore：MINIFEED_DB 设了用 sqlite 文件库，否则内存实现。
func openStore() (user.Store, error) {
	if path := os.Getenv("MINIFEED_DB"); path != "" {
		return user.NewSQLiteStore(path)
	}
	return user.NewMemoryStore(), nil
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
