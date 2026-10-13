package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"

	"ejdb"
)

func main() {
	dir, _ := os.MkdirTemp("", "ejdb-crud-example")
	defer os.RemoveAll(dir)

	db, err := ejdb.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	coll, err := db.CreateCollection("users")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("created collection: %s\n", coll.Name())

	// id=0 auto-assigns a new ID
	id, err := coll.Put([]byte(`{"name": "Alice", "age": 30, "city": "NY"}`), 0)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("inserted doc with id: %d\n", id)

	coll.Put([]byte(`{"name": "Bob", "age": 25, "city": "LA"}`), 0)
	coll.Put([]byte(`{"name": "Charlie", "age": 35, "city": "NY"}`), 0)
	fmt.Printf("total documents: %d\n", coll.DocCount())

	doc, err := coll.Get(id)
	if err != nil {
		log.Fatal(err)
	}
	var parsed map[string]interface{}
	json.Unmarshal(doc, &parsed)
	fmt.Printf("get doc %d: name=%s age=%v city=%s\n",
		id, parsed["name"], parsed["age"], parsed["city"])

	_, err = coll.Put([]byte(`{"name": "Alice", "age": 31, "city": "SF"}`), id)
	if err != nil {
		log.Fatal(err)
	}
	doc, _ = coll.Get(id)
	json.Unmarshal(doc, &parsed)
	fmt.Printf("updated doc %d: name=%s age=%v city=%s\n",
		id, parsed["name"], parsed["age"], parsed["city"])

	err = coll.Del(id)
	if err != nil {
		log.Fatal(err)
	}
	_, err = coll.Get(id)
	if err != nil {
		fmt.Printf("doc %d deleted successfully\n", id)
	}
	fmt.Printf("total documents after delete: %d\n", coll.DocCount())
}
