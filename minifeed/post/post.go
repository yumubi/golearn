// Package post：帖子存储与分页列表。P36 分层语义的复用：PostStore 接口 + 内存/SQLite 双实现。
// 素材对照（W06-3 K5/K6）：分页缓存难做（任一参数变化即失效），minifeed 只缓存单帖不缓存列表；
// List 的 LIMIT ? OFFSET ? 就是第一页判定的慢路径（P37 §7）。
package post

import (
	"database/sql"
	"errors"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("post not found")

// Post 是 domain 层对象（W02-2 K10：时间归存储实现转换）。
type Post struct {
	ID        int64
	AuthorID  int64
	Title     string
	Content   string
	CreatedAt time.Time
}

// PostStore 帖子存储最小接口：Create/GetByID/List 分页（n 条，offset 起始）。
type PostStore interface {
	Create(p *Post) error
	GetByID(id int64) (*Post, error)
	List(n, offset int) ([]Post, error)
}

// MemoryStore：进程内 map，锁保护并发 Create/Get/List（user.MemoryStore 同构）。
type MemoryStore struct {
	mu     sync.Mutex
	nextID int64
	byID   map[int64]*Post
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nextID: 1, byID: map[int64]*Post{}}
}

func (m *MemoryStore) Create(p *Post) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.ID = m.nextID
	m.nextID++
	p.CreatedAt = time.Now().UTC()
	m.byID[p.ID] = p // 契约同 user.MemoryStore：调用方此后不得再改 p
	return nil
}

func (m *MemoryStore) GetByID(id int64) (*Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.byID[id]; ok {
		return p, nil
	}
	return nil, ErrNotFound
}

func (m *MemoryStore) List(n, offset int) ([]Post, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]int64, 0, len(m.byID))
	for id := range m.byID {
		ids = append(ids, id)
	}
	sortSlice(ids)
	out := []Post{}
	if offset >= len(ids) { // offset 越界=空页不是错误（K5）
		return out, nil
	}
	for _, id := range ids[offset:] {
		if len(out) == n {
			break
		}
		p := m.byID[id]
		out = append(out, *p)
	}
	return out, nil
}

func sortSlice(ids []int64) {
	for i := range len(ids) {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
}

// SQLiteStore：帖子表（title/content 不建索引，教学从简）。
type SQLiteStore struct{ db *sql.DB }

const schema = `CREATE TABLE IF NOT EXISTS posts (id INTEGER PRIMARY KEY AUTOINCREMENT, author_id INTEGER NOT NULL, title TEXT NOT NULL, content TEXT NOT NULL, created_at INTEGER NOT NULL)`

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) Create(p *Post) error {
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(
		`INSERT INTO posts (author_id, title, content, created_at) VALUES (?, ?, ?, ?)`,
		p.AuthorID, p.Title, p.Content, p.CreatedAt.UnixMilli(),
	)
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId() // C:go_basic/database K2：Exec 结果对
	return err
}

func (s *SQLiteStore) GetByID(id int64) (*Post, error) {
	return scanPost(s.db.QueryRow(`SELECT id, author_id, title, content, created_at FROM posts WHERE id = ?`, id))
}

// List：LIMIT ? OFFSET ?（W06-3 K6 的判定走这里：n+offset 才是页参数，缓存第一页是上层事）。
func (s *SQLiteStore) List(n, offset int) ([]Post, error) {
	rows, err := s.db.Query(`SELECT id, author_id, title, content, created_at FROM posts ORDER BY id LIMIT ? OFFSET ?`, n, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close() // C:go_basic/database K4：rows 必须关
	out := []Post{}
	for rows.Next() {
		p, scanErr := scanPostRows(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func scanPost(row *sql.Row) (*Post, error) {
	var p Post
	var ms int64
	err := row.Scan(&p.ID, &p.AuthorID, &p.Title, &p.Content, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt = time.UnixMilli(ms).UTC()
	return &p, nil
}

func scanPostRows(rows *sql.Rows) (*Post, error) {
	var p Post
	var ms int64
	if err := rows.Scan(&p.ID, &p.AuthorID, &p.Title, &p.Content, &ms); err != nil {
		return nil, err
	}
	p.CreatedAt = time.UnixMilli(ms).UTC()
	return &p, nil
}

// Title 化简：NFKC 一类真实清洗不做，仅 trim（教学边界声明）。
