package post

import (
	"path/filepath"
	"sync"
	"testing"
)

func exercise(t *testing.T, s PostStore) {
	t.Helper()
	for i := range 5 {
		p := &Post{AuthorID: 1, Title: "t", Content: "c"}
		if err := s.Create(p); err != nil {
			t.Fatal(err)
		}
		if p.ID != int64(i+1) {
			t.Fatalf("id backfill: got %d want %d", p.ID, i+1)
		}
	}
	p, err := s.GetByID(3)
	if err != nil || p.Title != "t" {
		t.Fatalf("get: %v %v", p, err)
	}
	if _, err := s.GetByID(99); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// List 分页（W06-3 K5：参数变化即换页；offset 越界=空切片不是错误）
	page1, err := s.List(2, 0)
	if err != nil || len(page1) != 2 || page1[0].ID != 1 {
		t.Fatalf("list page1: %v %v", page1, err)
	}
	page2, err := s.List(2, 2)
	if err != nil || len(page2) != 2 || page2[0].ID != 3 {
		t.Fatalf("list page2: %v %v", page2, err)
	}
	tail, err := s.List(10, 4)
	if err != nil || len(tail) != 1 {
		t.Fatalf("list tail: %v %v", tail, err)
	}
	if out, err := s.List(2, 50); err != nil || len(out) != 0 {
		t.Fatalf("offset beyond total: %v %v", out, err)
	}
}

func TestMemoryList(t *testing.T) { exercise(t, NewMemoryStore()) }

func TestSQLiteList(t *testing.T) {
	db := filepath.Join(t.TempDir(), "posts.db")
	s, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	exercise(t, s)
}

// TestMemoryConcurrent：Create 并发下 ID 不重不漏（-race 验证）。
func TestMemoryConcurrent(t *testing.T) {
	s := NewMemoryStore()
	var wg sync.WaitGroup
	const n = 50
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			if err := s.Create(&Post{AuthorID: 1, Title: "x"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got, err := s.List(n, 0)
	if err != nil || len(got) != n {
		t.Fatalf("concurrent create: %d posts %v", len(got), err)
	}
	seen := map[int64]bool{}
	for _, p := range got {
		if seen[p.ID] {
			t.Fatalf("duplicate id %d", p.ID)
		}
		seen[p.ID] = true
	}
}
