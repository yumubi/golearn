package user

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite" // 纯 Go 驱动，匿名导入注册 driver
)

// ErrNotFound 查不到；ErrEmailTaken 邮箱已存在。调用方 errors.Is 定 404/409。
var (
	ErrNotFound   = errors.New("user not found")
	ErrEmailTaken = errors.New("email already taken")
)

// User 是 domain 层对象，不直接映射表列（W02-2 K10）：时间归存储实现转换。
type User struct {
	ID           int64
	Email        string
	Nick         string
	PasswordHash string // hex(sha256(salt || password))
	Salt         string // hex，每用户随机
	CreatedAt    time.Time
}

// Store 用户存储最小接口（handler 依赖接口，测试换内存实现）。
type Store interface {
	Create(u *User) error
	GetByEmail(email string) (*User, error)
	GetByID(id int64) (*User, error)
}

// MemoryStore：进程内 map 实现，测试与本地跑通用。RWMutex 保护，ID 锁内自增。
type MemoryStore struct {
	mu     sync.RWMutex
	nextID int64
	byID   map[int64]*User
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, byID: map[int64]*User{}}
}

func (m *MemoryStore) Create(u *User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, e := range m.byID {
		if e.Email == u.Email {
			return ErrEmailTaken
		}
	}
	u.ID = m.nextID
	m.nextID++
	u.CreatedAt = time.Now().UTC()
	m.byID[u.ID] = u // 存指针：Create 的调用方此后不得再改 u（接口契约）
	return nil
}

func (m *MemoryStore) GetByEmail(email string) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, u := range m.byID {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) GetByID(id int64) (*User, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if u, ok := m.byID[id]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}

// SQLiteStore：modernc.org/sqlite 纯 Go 无 cgo。教学不引迁移工具，Open 建表。
type SQLiteStore struct{ db *sql.DB }

const schema = `CREATE TABLE IF NOT EXISTS users (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE, nick TEXT NOT NULL, password_hash TEXT NOT NULL, salt TEXT NOT NULL, created_at INTEGER NOT NULL)`

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec(schema); err != nil {
		db.Close() // 建表失败关掉半开连接
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) Create(u *User) error {
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(
		`INSERT INTO users (email, nick, password_hash, salt, created_at) VALUES (?, ?, ?, ?, ?)`,
		u.Email, u.Nick, u.PasswordHash, u.Salt, u.CreatedAt.UnixMilli(),
	)
	if err != nil {
		// 唯一约束冲突是正常业务分支（modernc 报错串带 UNIQUE constraint failed）
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return ErrEmailTaken
		}
		return err
	}

	u.ID, err = res.LastInsertId()
	return err
}

func (s *SQLiteStore) GetByEmail(email string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT id, email, nick, password_hash, salt, created_at FROM users WHERE email = ?`, email))
}

func (s *SQLiteStore) GetByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT id, email, nick, password_hash, salt, created_at FROM users WHERE id = ?`, id))
}

// scanUser 收敛列映射（W02-2 K10：dao 映射表列，created_at 存 UnixMilli）。
func scanUser(row *sql.Row) (*User, error) {
	var u User
	var ms int64
	err := row.Scan(&u.ID, &u.Email, &u.Nick, &u.PasswordHash, &u.Salt, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.CreatedAt = time.UnixMilli(ms).UTC()
	return &u, nil
}
