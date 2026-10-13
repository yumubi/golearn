package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"ejdb"
)

func main() {
	dir, _ := os.MkdirTemp("", "ejdb-index-example")
	defer os.RemoveAll(dir)

	db, err := ejdb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	coll, _ := db.CreateCollection("users")

	coll.EnsureIndex("name", ejdb.IndexStr)
	coll.EnsureIndex("age", ejdb.IndexI64)
	coll.EnsureIndex("score", ejdb.IndexF64)
	coll.EnsureIndex("email", ejdb.IndexUnique|ejdb.IndexStr)

	// Print index info before data
	fmt.Println("=== Indexes ===")
	for _, idx := range coll.Indexes() {
		fmt.Printf("  path=%s unique=%v count=%d\n",
			idx.Path(), idx.IsUnique(), idx.Count())
	}

	// Insert data
	coll.Put([]byte(`{"name": "Alice", "age": 30, "score": 85.5, "email": "alice@example.com"}`), 0)
	coll.Put([]byte(`{"name": "Bob", "age": 25, "score": 92.0, "email": "bob@example.com"}`), 0)
	coll.Put([]byte(`{"name": "Charlie", "age": 35, "score": 78.0, "email": "charlie@example.com"}`), 0)

	// Print index count after data insert
	for _, idx := range coll.Indexes() {
		fmt.Printf("  after insert - path=%s count=%d\n", idx.Path(), idx.Count())
	}

	// Unique index: duplicate email will fail
	_, err = coll.Put([]byte(`{"name": "Alice2", "email": "alice@example.com"}`), 0)
	if err != nil {
		fmt.Printf("\nunique constraint works: %v\n", err)
	}

	// Prefix query using string index
	ctx := context.Background()
	fmt.Println("\n=== Prefix query: name like A% ===")
	results, _ := db.Query("users").Where(ejdb.Prefix("name", "A")).All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s\n", m["name"])
	}
}
