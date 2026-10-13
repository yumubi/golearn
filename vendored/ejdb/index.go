package ejdb

import (
	json "github.com/goccy/go-json"
	"fmt"
	"sync/atomic"

	"ejdb/jsondoc"
	"ejdb/storage"
	"ejdb/types"
)

// IndexMode re-export from types for convenience.
type IndexMode = types.IndexMode

const (
	IndexUnique = types.IndexUnique
	IndexStr    = types.IndexStr
	IndexI64    = types.IndexI64
	IndexF64    = types.IndexF64
)

// Index represents a database index on a JSON field path.
type Index struct {
	id    storage.DBID
	mode  IndexMode
	path  jsondoc.Path
	coll  *Collection
	store storage.KVStore
	flags storage.DBFlags
	rnum  int64 // atomic: number of index entries
}

// ID returns the index's database ID.
func (idx *Index) ID() storage.DBID { return idx.id }

// Mode returns the index mode flags.
func (idx *Index) Mode() IndexMode { return idx.mode }

// Path returns the indexed JSON path.
func (idx *Index) Path() jsondoc.Path { return idx.path }

// Count returns the number of entries in this index.
func (idx *Index) Count() int64 { return atomic.LoadInt64(&idx.rnum) }

// IsUnique returns true if this is a unique index.
func (idx *Index) IsUnique() bool { return idx.mode&IndexUnique != 0 }

// HasCompoundKeys returns true for non-unique (duplicate) indexes.
func (idx *Index) HasCompoundKeys() bool { return idx.flags&storage.DBCompoundKeys != 0 }

func (idx *Index) addRecord(docID int64, doc map[string]interface{}) error {
	values := idx.extractValues(doc)
	if values == nil {
		return nil
	}

	for _, val := range values {
		keyData, err := jsondoc.SerializeKey(val, idx.mode)
		if err != nil {
			continue
		}

		key := &storage.Val{Data: keyData}
		var storeVal *storage.Val

		if idx.HasCompoundKeys() {
			key.Compound = docID
			storeVal = &storage.Val{}
		} else {
			storeVal = &storage.Val{Data: docIDToKey(docID)}
		}

		if err := idx.store.Put(idx.id, key, storeVal, idx.IsUnique()); err != nil {
			if err == storage.ErrKeyExists {
				return fmt.Errorf("unique constraint violation on %s", idx.path)
			}
			return err
		}
		atomic.AddInt64(&idx.rnum, 1)
	}
	return nil
}

func (idx *Index) removeRecord(docID int64, doc map[string]interface{}) error {
	values := idx.extractValues(doc)
	if values == nil {
		return nil
	}

	for _, val := range values {
		keyData, err := jsondoc.SerializeKey(val, idx.mode)
		if err != nil {
			continue
		}
		key := &storage.Val{Data: keyData}
		if idx.HasCompoundKeys() {
			key.Compound = docID
		}

		if err := idx.store.Del(idx.id, key); err != nil {
			return err
		}
		atomic.AddInt64(&idx.rnum, -1)
	}
	return nil
}

func (idx *Index) extractValues(doc map[string]interface{}) []*types.Value {
	// Traverse the document structure using the path
	current := interface{}(doc)
	for _, segment := range idx.path {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		val, exists := m[segment]
		if !exists {
			return nil
		}
		current = val
	}

	// Handle array fields - return each element as a separate value
	if arr, ok := current.([]interface{}); ok {
		result := make([]*types.Value, 0, len(arr))
		for _, elem := range arr {
			result = append(result, toVal(elem))
		}
		return result
	}

	return []*types.Value{toVal(current)}
}

func toVal(raw interface{}) *types.Value {
	return jsondoc.ToValue(raw)
}

// fillFromCollection scans the entire collection and builds the index.
func (idx *Index) fillFromCollection(coll *Collection) error {
	return coll.scanAll(func(id int64, doc []byte) bool {
		var parsed map[string]interface{}
		if err := json.Unmarshal(doc, &parsed); err != nil {
			return true // skip invalid documents
		}
		_ = idx.addRecord(id, parsed)
		return true
	})
}


