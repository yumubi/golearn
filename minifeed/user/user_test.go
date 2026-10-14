// user 包测试：两个 Store 实现跑同一套用例（接口一致的证明，P36 正章的分层测试要点）。
// SQLiteStore 用 t.TempDir() 文件库，进程内即可测，不需要 Docker。
package user

import (
	"errors"
	"testing"
)

func TestStores(t *testing.T) {
	t.Run("memory", func(t *testing.T) { exercise(t, NewMemoryStore()) })
	t.Run("sqlite", func(t *testing.T) {
		s, err := NewSQLiteStore(t.TempDir() + "/users.db")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		exercise(t, s)
	})
}

func exercise(t *testing.T, s Store) {
	t.Helper()

	// 创建：返回 ID，CreatedAt 填好
	u := &User{Email: "a@b.c", Nick: "alice", PasswordHash: "hh", Salt: "ss"}
	if err := s.Create(u); err != nil {
		t.Fatal(err)
	}
	if u.ID == 0 || u.CreatedAt.IsZero() {
		t.Fatalf("create should fill ID and CreatedAt: %+v", u)
	}

	// 重复邮箱：ErrEmailTaken
	dup := &User{Email: "a@b.c", Nick: "bob", PasswordHash: "hh", Salt: "ss"}
	if err := s.Create(dup); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("want ErrEmailTaken, got %v", err)
	}

	// 按 email 查
	got, err := s.GetByEmail("a@b.c")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID || got.Nick != "alice" || got.Salt != "ss" {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}

	// 按 id 查与查不到
	if _, err := s.GetByID(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if _, err := s.GetByEmail("nobody@x.y"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// 并发 Create 的 id 分配与唯一约束：-race 下跑。
func TestMemoryStoreConcurrent(t *testing.T) {
	s := NewMemoryStore()
	const n = 50
	done := make(chan struct{})
	for i := range n {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			s.Create(&User{Email: string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@x.y", Nick: "n"})
		}(i)
	}
	for range n {
		<-done
	}
}
