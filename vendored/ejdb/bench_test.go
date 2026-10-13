package ejdb

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"

	"ejdb/jsondoc"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func benchDir(b *testing.B) string {
	b.Helper()
	dir, err := os.MkdirTemp("", "ejdb-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func openBenchDB(b *testing.B) *DB {
	b.Helper()
	db, err := Open(benchDir(b))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return db
}

func makeDoc(id int) []byte {
	data, _ := json.Marshal(map[string]interface{}{
		"id":   id,
		"name": fmt.Sprintf("user-%d", id),
		"age":  id % 80,
		"city": cities[id%len(cities)],
		"score": float64(id) * 1.5,
		"tags":  tags[id%len(tags)],
	})
	return data
}

var cities = []string{"NY", "LA", "SF", "CHI", "SEA", "BOS", "DEN", "ATL", "PDX", "MIA"}
var tags = [][]string{
	{"go", "db"},
	{"go", "json"},
	{"rust", "db"},
	{"java", "web"},
	{"python", "ml"},
	{"go", "bench"},
	{"rust", "wasm"},
	{"typescript", "web"},
	{"c", "embedded"},
	{"zig", "systems"},
}

func populate(b *testing.B, coll *Collection, n int) {
	b.Helper()
	// Disable auto-count for benchmarks to avoid repeated scanAll
	for i := range n {
		doc := makeDoc(i)
		if _, err := coll.Put(doc, 0); err != nil {
			b.Fatalf("put doc %d: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// CRUD benchmarks
// ---------------------------------------------------------------------------

func BenchmarkPut(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, err := db.CreateCollection("bench")
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := range b.N {
				doc := makeDoc(i % n)
				_, _ = coll.Put(doc, 0)
			}
		})
	}
}

func BenchmarkGet(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			b.ResetTimer()
			b.ReportAllocs()
			for i := range b.N {
				_, _ = coll.Get(int64(i%n + 1))
			}
		})
	}
}

func BenchmarkPutUpdate(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			// Pre-populate
			ids := make([]int64, n)
			for i := range n {
				id, _ := coll.Put(makeDoc(i), 0)
				ids[i] = id
			}
			b.ResetTimer()
			b.ReportAllocs()
			for i := range b.N {
				id := ids[i%n]
				doc := makeDoc(i)
				_, _ = coll.Put(doc, id)
			}
		})
	}
}

func BenchmarkDelete(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")

			// Each iteration: put then del
			b.ResetTimer()
			b.ReportAllocs()
			for i := range b.N {
				id, _ := coll.Put(makeDoc(i), 0)
				_ = coll.Del(id)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Query benchmarks
// ---------------------------------------------------------------------------

func BenchmarkQueryFullScan(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _ = db.Query("bench").All(ctx)
			}
		})
	}
}

func BenchmarkQueryEQ(b *testing.B) {
	// Without index — brute force
	b.Run("noindex", func(b *testing.B) {
		db := openBenchDB(b)
		coll, _ := db.CreateCollection("bench")
		populate(b, coll, 1000)
		ctx := context.Background()
		b.ResetTimer()
		b.ReportAllocs()
		for range b.N {
			_, _ = db.Query("bench").Where(Eq("age", 42)).All(ctx)
		}
	})

	// With index
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("withidx_n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			_, _ = coll.EnsureIndex("age", IndexI64)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _ = db.Query("bench").Where(Eq("age", 42)).All(ctx)
			}
		})
	}
}

func BenchmarkQueryRange(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			_, _ = coll.EnsureIndex("age", IndexI64)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _ = db.Query("bench").Where(Gt("age", 40)).All(ctx)
			}
		})
	}
}

func BenchmarkQueryRangeBoth(b *testing.B) {
	// GTE + LT — double-bounded range
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			_, _ = coll.EnsureIndex("age", IndexI64)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				results, _ := db.Query("bench").
					Where(And(Gte("age", 20), Lt("age", 60))).
					All(ctx)
				_ = results
			}
		})
	}
}

func BenchmarkQueryIN(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			_, _ = coll.EnsureIndex("age", IndexI64)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _ = db.Query("bench").Where(In("age", 20, 30, 40, 50)).All(ctx)
			}
		})
	}
}

func BenchmarkQueryStringEQ(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			_, _ = coll.EnsureIndex("city", IndexStr)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _ = db.Query("bench").Where(Eq("city", "NY")).All(ctx)
			}
		})
	}
}

func BenchmarkQueryPrefix(b *testing.B) {
	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")
	populate(b, coll, 1000)
	_, _ = coll.EnsureIndex("name", IndexStr)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, _ = db.Query("bench").Where(Prefix("name", "user-1")).All(ctx)
	}
}

func BenchmarkQuerySort(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _ = db.Query("bench").OrderBy(Asc("age")).All(ctx)
			}
		})
	}
}

func BenchmarkQueryProjection(b *testing.B) {
	n := 1000
	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")
	populate(b, coll, n)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, _ = db.Query("bench").Project("name", "age").All(ctx)
	}
}

func BenchmarkQuerySkipLimit(b *testing.B) {
	n := 10000
	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")
	populate(b, coll, n)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, _ = db.Query("bench").Skip(100).Limit(50).All(ctx)
	}
}

// ---------------------------------------------------------------------------
// Index benchmarks
// ---------------------------------------------------------------------------

func BenchmarkEnsureIndex(b *testing.B) {
	sizes := []int{100, 1000, 10000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				// Create a new index each iteration on a different field
				field := fmt.Sprintf("idxfield_%d", b.N)
				_, _ = coll.EnsureIndex(field, IndexI64)
			}
		})
	}
}

func BenchmarkIndexUnique(b *testing.B) {
	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")
	_, _ = coll.EnsureIndex("id", IndexUnique|IndexI64)
	b.ResetTimer()
	b.ReportAllocs()
	for i := range b.N {
		doc := []byte(fmt.Sprintf(`{"id":%d,"val":"%d"}`, i, i))
		_, err := coll.Put(doc, 0)
		if err != nil {
			b.Fatalf("put %d: %v", i, err)
		}
	}
}

// BenchmarkIndexUniqueViolation measures the cost of detecting duplicate keys.
func BenchmarkIndexUniqueViolation(b *testing.B) {
	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")
	_, _ = coll.EnsureIndex("email", IndexUnique|IndexStr)
	_, _ = coll.Put([]byte(`{"email":"dup@example.com"}`), 0)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := coll.Put([]byte(`{"email":"dup@example.com"}`), 0)
		if err == nil {
			b.Fatal("expected unique violation")
		}
	}
}

// ---------------------------------------------------------------------------
// Concurrent benchmarks
// ---------------------------------------------------------------------------

func BenchmarkConcurrentPut(b *testing.B) {
	for _, goroutines := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("g=%d", goroutines), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			var mu sync.Mutex
			nextID := 1

			b.ResetTimer()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					mu.Lock()
					id := nextID
					nextID++
					mu.Unlock()
					doc := makeDoc(id)
					_, _ = coll.Put(doc, 0)
				}
			})
		})
	}
}

func BenchmarkConcurrentRead(b *testing.B) {
	n := 1000
	for _, goroutines := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("g=%d", goroutines), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")
			populate(b, coll, n)

			b.ResetTimer()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					id := int64(i%n + 1)
					_, _ = coll.Get(id)
					i++
				}
			})
		})
	}
}

func BenchmarkConcurrentMixed(b *testing.B) {
	n := 1000
	for _, goroutines := range []int{4, 8} {
		b.Run(fmt.Sprintf("g=%d", goroutines), func(b *testing.B) {
			db := openBenchDB(b)
			coll, _ := db.CreateCollection("bench")

			// Pre-populate
			ids := make([]int64, n)
			for i := range n {
				id, _ := coll.Put(makeDoc(i), 0)
				ids[i] = id
			}

			var counter int64
			b.ResetTimer()
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				idx := 0
				for pb.Next() {
					i := idx % n
					switch i % 4 {
					case 0:
						// Read
						_, _ = coll.Get(ids[i])
					case 1:
						// Write
						_, _ = coll.Put(makeDoc(i), ids[i])
					case 2:
						// Insert new
						id := int64(counter)
						_, _ = coll.Put(makeDoc(int(id)), 0)
						counter++
					case 3:
						// Query
						_, _ = db.Query("bench").Where(Eq("age", i%80)).All(context.Background())
					}
					idx++
				}
			})
		})
	}
}

// ---------------------------------------------------------------------------
// Large document benchmarks
// ---------------------------------------------------------------------------

func BenchmarkLargeDocument(b *testing.B) {
	// Build a ~10KB document
	large := make(map[string]interface{})
	large["title"] = "large document"
	nested := make([]map[string]interface{}, 50)
	for i := range 50 {
		nested[i] = map[string]interface{}{
			"idx":   i,
			"data":  fmt.Sprintf("value-%d", i),
			"nested": map[string]interface{}{
				"a": float64(i),
				"b": fmt.Sprintf("str-%d", i),
			},
		}
	}
	large["items"] = nested
	raw, _ := json.Marshal(large)

	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, _ = coll.Put(raw, 0)
	}
}

func BenchmarkLargeCollectionFullScan(b *testing.B) {
	// 100K small documents — measure raw scan throughput
	n := 100_000
	db := openBenchDB(b)
	coll, _ := db.CreateCollection("bench")
	for i := range n {
		doc, _ := json.Marshal(map[string]interface{}{
			"id": i, "val": float64(i),
		})
		_, _ = coll.Put(doc, 0)
	}
	ctx := context.Background()

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, _ = db.Query("bench").All(ctx)
	}
}

// ---------------------------------------------------------------------------
// Memory allocation micro-benchmarks
// ---------------------------------------------------------------------------

func BenchmarkDocIDKeyConversion(b *testing.B) {
	b.ReportAllocs()
	for i := range b.N {
		key := docIDToKey(int64(i))
		_ = keyToDocID(key)
	}
}

func BenchmarkJsonPathParse(b *testing.B) {
	paths := []string{
		"address.city",
		"users.0.name",
		"data.metadata.tags.1.value",
		"/address/city",
		"/users/0/name",
	}
	b.ReportAllocs()
	for i := range b.N {
		_ = jsondoc.ParsePath(paths[i%len(paths)])
	}
}

func BenchmarkJsonPathExtract(b *testing.B) {
	doc := []byte(`{"user":{"name":"alice","profile":{"age":30,"city":"NY"}}}`)
	p := jsondoc.ParsePath("user.profile.age")
	b.ReportAllocs()
	for range b.N {
		_, _ = p.Extract(doc)
	}
}

func BenchmarkSerializeKey(b *testing.B) {
	vals := []struct {
		val  interface{}
		mode IndexMode
	}{
		{int64(42), IndexI64},
		{float64(3.14), IndexF64},
		{"hello", IndexStr},
		{int64(math.MaxInt64), IndexI64},
		{int64(-math.MaxInt64), IndexI64},
	}

	b.ReportAllocs()
	for i := range b.N {
		v := vals[i%len(vals)]
		tv := toValue(v.val)
		_, _ = jsondoc.SerializeKey(tv, v.mode)
	}
}

// ---------------------------------------------------------------------------
// DB open/close benchmarks
// ---------------------------------------------------------------------------

func BenchmarkOpenClose(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		dir := benchDir(b)
		db, err := Open(dir)
		if err != nil {
			b.Fatal(err)
		}
		_ = db.Close()
	}
}
