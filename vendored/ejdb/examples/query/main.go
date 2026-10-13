package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"ejdb"
)

func main() {
	dir, _ := os.MkdirTemp("", "ejdb-query-example")
	defer os.RemoveAll(dir)

	db, err := ejdb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	coll, _ := db.CreateCollection("users")
	users := []string{
		`{"name": "Alice", "age": 30, "city": "NY", "score": 85.5}`,
		`{"name": "Bob", "age": 25, "city": "LA", "score": 92.0}`,
		`{"name": "Charlie", "age": 30, "city": "LA", "score": 78.0}`,
		`{"name": "Diana", "age": 22, "city": "NY", "score": 95.5}`,
		`{"name": "Eve", "age": 35, "city": "SF", "score": 88.0}`,
	}
	for _, u := range users {
		coll.Put([]byte(u), 0)
	}

	coll.EnsureIndex("age", ejdb.IndexI64)
	coll.EnsureIndex("city", ejdb.IndexStr)
	coll.EnsureIndex("score", ejdb.IndexF64)

	ctx := context.Background()

	// Eq condition
	fmt.Println("=== Eq query: age == 30 ===")
	results, _ := db.Query("users").Where(ejdb.Eq("age", 30)).All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s (age %v)\n", m["name"], m["age"])
	}

	// Range query with Gt
	fmt.Println("\n=== Gt query: score > 90 ===")
	results, _ = db.Query("users").Where(ejdb.Gt("score", 90.0)).All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s (score %v)\n", m["name"], m["score"])
	}

	// In condition
	fmt.Println("\n=== In query: city in (NY, SF) ===")
	results, _ = db.Query("users").Where(ejdb.In("city", "NY", "SF")).All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s (city %s)\n", m["name"], m["city"])
	}

	// And composite condition
	fmt.Println("\n=== And query: age == 30 AND city == LA ===")
	results, _ = db.Query("users").
		Where(ejdb.And(ejdb.Eq("age", 30), ejdb.Eq("city", "LA"))).
		All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s (age %v, city %s)\n", m["name"], m["age"], m["city"])
	}

	// Or composite condition
	fmt.Println("\n=== Or query: name == Alice OR name == Bob ===")
	results, _ = db.Query("users").
		Where(ejdb.Or(ejdb.Eq("name", "Alice"), ejdb.Eq("name", "Bob"))).
		All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s\n", m["name"])
	}

	// Sort
	fmt.Println("\n=== OrderBy: score desc ===")
	results, _ = db.Query("users").OrderBy(ejdb.Desc("score")).All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s (score %v)\n", m["name"], m["score"])
	}

	// Pagination
	fmt.Println("\n=== Skip 1, Limit 2 (name asc) ===")
	results, _ = db.Query("users").
		OrderBy(ejdb.Asc("name")).
		Skip(1).
		Limit(2).
		All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s\n", m["name"])
	}

	// Projection: return only specified fields
	fmt.Println("\n=== Project: only name and age ===")
	results, _ = db.Query("users").
		Where(ejdb.Eq("city", "NY")).
		Project("name", "age").
		All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		doc, _ := json.Marshal(m)
		fmt.Printf("  %s\n", doc)
	}

	// One: return first match only
	fmt.Println("\n=== One: first match age >= 30 ===")
	doc, _ := db.Query("users").Where(ejdb.Gte("age", 30)).One(ctx)
	if doc != nil {
		m := doc.(map[string]interface{})
		fmt.Printf("  %s (age %v)\n", m["name"], m["age"])
	}

	// Count matching documents
	fmt.Println("\n=== Count: age >= 25 ===")
	count, _ := db.Query("users").Where(ejdb.Gte("age", 25)).Count(ctx)
	fmt.Printf("  count = %d\n", count)

	// All documents
	fmt.Println("\n=== All documents ===")
	results, _ = db.Query("users").All(ctx)
	for _, r := range results {
		m := r.(map[string]interface{})
		fmt.Printf("  %s (age %v, city %s, score %v)\n",
			m["name"], m["age"], m["city"], m["score"])
	}
}
