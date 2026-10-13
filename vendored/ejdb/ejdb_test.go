package ejdb

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ejdb-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestOpenClose(t *testing.T) {
	dir := tempDir(t)
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCollectionCRUD(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, err := db.CreateCollection("users")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if coll.Name() != "users" {
		t.Fatalf("expected name 'users', got %q", coll.Name())
	}

	// Put
	doc := []byte(`{"name": "Alice", "age": 30}`)
	id, err := coll.Put(doc, 0)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if id != 1 {
		t.Fatalf("expected id 1, got %d", id)
	}

	// Get
	got, err := coll.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	var result map[string]interface{}
	_ = json.Unmarshal(got, &result)
	if result["name"] != "Alice" {
		t.Fatalf("expected name 'Alice', got %v", result["name"])
	}

	// Update
	doc2 := []byte(`{"name": "Alice", "age": 31}`)
	_, err = coll.Put(doc2, id)
	if err != nil {
		t.Fatalf("Put (update): %v", err)
	}

	got, err = coll.Get(id)
	if err != nil {
		t.Fatalf("Get after update: %v", err)
	}
	_ = json.Unmarshal(got, &result)
	if result["age"] != float64(31) {
		t.Fatalf("expected age 31, got %v", result["age"])
	}

	if coll.DocCount() != 1 {
		t.Fatalf("expected count 1, got %d", coll.DocCount())
	}

	// Delete
	if err := coll.Del(id); err != nil {
		t.Fatalf("Del: %v", err)
	}
	_, err = coll.Get(id)
	if err == nil {
		t.Fatal("expected error after delete")
	}
	if coll.DocCount() != 0 {
		t.Fatalf("expected count 0, got %d", coll.DocCount())
	}
}

func TestQueryEQ(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, err := db.CreateCollection("users")
	if err != nil {
		t.Fatal(err)
	}

	_, _ = coll.Put([]byte(`{"name": "Alice", "age": 30}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Bob", "age": 25}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Charlie", "age": 30}`), 0)

	ctx := context.Background()

	// Query by name
	results, err := db.Query("users").Where(Eq("name", "Bob")).All(ctx)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// Query by age (with index)
	_, _ = coll.EnsureIndex("age", IndexI64)
	results, err = db.Query("users").Where(Eq("age", 30)).All(ctx)
	if err != nil {
		t.Fatalf("Query with index: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestQueryRange(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, _ := db.CreateCollection("items")
	for i := int64(1); i <= 10; i++ {
		doc, _ := json.Marshal(map[string]interface{}{"val": i})
		_, _ = coll.Put(doc, i)
	}

	ctx := context.Background()
	_, _ = coll.EnsureIndex("val", IndexI64)

	results, err := db.Query("items").Where(Gt("val", 5)).All(ctx)
	if err != nil {
		t.Fatalf("Query GT: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(results))
	}

	results, err = db.Query("items").Where(Gte("val", 5)).All(ctx)
	if err != nil {
		t.Fatalf("Query GTE: %v", err)
	}
	if len(results) != 6 {
		t.Fatalf("expected 6 results, got %d", len(results))
	}
}

func TestSort(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, _ := db.CreateCollection("users")
	_, _ = coll.Put([]byte(`{"name": "Charlie", "age": 35}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Alice", "age": 30}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Bob", "age": 25}`), 0)

	ctx := context.Background()

	results, err := db.Query("users").OrderBy(Asc("name")).All(ctx)
	if err != nil {
		t.Fatalf("Sort asc: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// Verify order
	var names []string
	for _, r := range results {
		m := r.(map[string]interface{})
		names = append(names, m["name"].(string))
	}
	if names[0] != "Alice" || names[1] != "Bob" || names[2] != "Charlie" {
		t.Fatalf("wrong asc order: %v", names)
	}

	// Descending
	results, err = db.Query("users").OrderBy(Desc("name")).All(ctx)
	if err != nil {
		t.Fatalf("Sort desc: %v", err)
	}
	names = nil
	for _, r := range results {
		m := r.(map[string]interface{})
		names = append(names, m["name"].(string))
	}
	if names[0] != "Charlie" || names[1] != "Bob" || names[2] != "Alice" {
		t.Fatalf("wrong desc order: %v", names)
	}
}

func TestAndOr(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, _ := db.CreateCollection("users")
	_, _ = coll.Put([]byte(`{"name": "Alice", "age": 30, "city": "NY"}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Bob", "age": 25, "city": "LA"}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Charlie", "age": 30, "city": "LA"}`), 0)

	ctx := context.Background()

	// AND
	results, err := db.Query("users").
		Where(And(Eq("age", 30), Eq("city", "LA"))).
		All(ctx)
	if err != nil {
		t.Fatalf("AND query: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	// OR
	results, err = db.Query("users").
		Where(Or(Eq("name", "Alice"), Eq("name", "Bob"))).
		All(ctx)
	if err != nil {
		t.Fatalf("OR query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestSkipLimit(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, _ := db.CreateCollection("items")
	for i := 1; i <= 10; i++ {
		doc, _ := json.Marshal(map[string]interface{}{"val": i})
		_, _ = coll.Put(doc, 0)
	}

	ctx := context.Background()

	// Skip 5, limit 3
	results, err := db.Query("items").Skip(5).Limit(3).All(ctx)
	if err != nil {
		t.Fatalf("Skip/Limit: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
}

func TestUniqueIndex(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, _ := db.CreateCollection("users")
	_, _ = coll.EnsureIndex("email", IndexUnique|IndexStr)

	doc1 := []byte(`{"email": "alice@example.com", "name": "Alice"}`)
	_, err := coll.Put(doc1, 0)
	if err != nil {
		t.Fatalf("Put first: %v", err)
	}

	doc2 := []byte(`{"email": "alice@example.com", "name": "Alice2"}`)
	_, err = coll.Put(doc2, 0)
	if err == nil {
		t.Fatal("expected unique constraint violation")
	}
}

func TestReopen(t *testing.T) {
	dir := tempDir(t)

	// First session
	db1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	coll, _ := db1.CreateCollection("users")
	_, _ = coll.EnsureIndex("name", IndexStr)
	_, _ = coll.Put([]byte(`{"name": "Alice"}`), 0)
	db1.Close()

	// Second session - verify data persists
	db2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	coll2 := db2.Collection("users")
	if coll2 == nil {
		t.Fatal("collection not found after reopen")
	}
	if coll2.DocCount() != 1 {
		t.Fatalf("expected count 1, got %d", coll2.DocCount())
	}

	results, err := db2.Query("users").Where(Eq("name", "Alice")).All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestINQuery(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	coll, _ := db.CreateCollection("users")
	_, _ = coll.Put([]byte(`{"name": "Alice"}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Bob"}`), 0)
	_, _ = coll.Put([]byte(`{"name": "Charlie"}`), 0)

	ctx := context.Background()
	_, _ = coll.EnsureIndex("name", IndexStr)

	results, err := db.Query("users").Where(In("name", "Alice", "Bob")).All(ctx)
	if err != nil {
		t.Fatalf("IN query: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func openDB(t *testing.T) *DB {
	t.Helper()
	dir := tempDir(t)
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
