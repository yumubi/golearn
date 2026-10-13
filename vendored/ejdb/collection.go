package ejdb

import (
	json "github.com/goccy/go-json"
	"fmt"
	"sync"
	"sync/atomic"

	"ejdb/jsondoc"
	"ejdb/storage"
)

// Collection represents a document collection.
type Collection struct {
	id      storage.DBID
	name    string
	db      *DB
	store   storage.KVStore
	indexes []*Index
	rnum    int64
	mu      sync.RWMutex
	idSeq   int64
}

func (c *Collection) ID() storage.DBID { return c.id }
func (c *Collection) Name() string     { return c.name }

func (c *Collection) DocCount() int64 { return atomic.LoadInt64(&c.rnum) }

func (c *Collection) Indexes() []*Index {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]*Index, len(c.indexes))
	copy(out, c.indexes)
	return out
}

// Put inserts or updates a JSON document. Auto-assigns ID if id==0.
func (c *Collection) Put(doc []byte, id int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if id == 0 {
		id = atomic.AddInt64(&c.idSeq, 1)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}

	oldDoc, oldExists := c.getRaw(id)

	key := &storage.Val{Data: docIDToKey(id)}
	val := &storage.Val{Data: doc}
	if err := c.store.Put(c.id, key, val, false); err != nil {
		return 0, err
	}

	if oldExists {
		var oldParsed map[string]interface{}
		_ = json.Unmarshal(oldDoc, &oldParsed)
		_ = c.removeIndexRecords(id, oldParsed)
	}
	if err := c.addIndexRecords(id, parsed); err != nil {
		_ = c.store.Del(c.id, key) // rollback the doc insert
		return id, err
	}

	if !oldExists {
		atomic.AddInt64(&c.rnum, 1)
	}
	return id, nil
}

// Get retrieves a document by ID.
func (c *Collection) Get(id int64) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	doc, ok := c.getRaw(id)
	if !ok {
		return nil, fmt.Errorf("document %d not found", id)
	}
	return doc, nil
}

func (c *Collection) getRaw(id int64) ([]byte, bool) {
	var keyBuf [8]byte
	docIDToKeyBuf(id, keyBuf[:])
	key := &storage.Val{Data: keyBuf[:]}
	v, err := c.store.Get(c.id, key)
	if err != nil || v == nil {
		return nil, false
	}
	return v.Data, true
}

// getDocUnsafe retrieves a raw document without locking or allocating.
// Caller must hold c.mu.RLock() or Lock(). keyBuf must be at least 8 bytes.
func (c *Collection) getDocUnsafe(id int64, keyBuf []byte) ([]byte, bool) {
	docIDToKeyBuf(id, keyBuf)
	key := &storage.Val{Data: keyBuf[:8]}
	v, err := c.store.Get(c.id, key)
	if err != nil || v == nil {
		return nil, false
	}
	return v.Data, true
}

// Del removes a document by ID.
func (c *Collection) Del(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	oldDoc, exists := c.getRaw(id)
	if !exists {
		return nil
	}

	var oldParsed map[string]interface{}
	if err := json.Unmarshal(oldDoc, &oldParsed); err == nil {
		_ = c.removeIndexRecords(id, oldParsed)
	}

	key := &storage.Val{Data: docIDToKey(id)}
	if err := c.store.Del(c.id, key); err != nil {
		return err
	}
	atomic.AddInt64(&c.rnum, -1)
	return nil
}

// EnsureIndex creates an index on the given path if it doesn't exist.
func (c *Collection) EnsureIndex(path string, mode IndexMode) (*Index, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	jpath := jsondoc.ParsePath(path)

	for _, idx := range c.indexes {
		if idx.path.Equal(jpath) && idx.mode == mode {
			return idx, nil
		}
	}

	dbid := c.db.allocDBID()
	flags := storage.DBFlags(0)
	if mode&IndexI64 != 0 {
		flags |= storage.DBVnum64Keys
	} else if mode&IndexF64 != 0 {
		flags |= storage.DBRealnumKeys
	}
	if mode&IndexUnique == 0 {
		flags |= storage.DBCompoundKeys
	}

	if err := c.store.OpenDB(dbid, flags); err != nil {
		return nil, fmt.Errorf("open index db: %w", err)
	}

	idx := &Index{
		id:    dbid,
		mode:  mode,
		path:  jpath,
		coll:  c,
		store: c.store,
		flags: flags,
	}

	if err := c.db.saveIndexMeta(c.id, idx); err != nil {
		_ = c.store.CloseDB(dbid)
		return nil, err
	}

	if err := idx.fillFromCollection(c); err != nil {
		_ = c.store.CloseDB(dbid)
		return nil, fmt.Errorf("fill index: %w", err)
	}

	c.indexes = append(c.indexes, idx)
	return idx, nil
}

func (db *DB) saveIndexMeta(collDBID storage.DBID, idx *Index) error {
	meta := indexMeta{
		CollDBID: collDBID,
		DBID:     idx.id,
		Mode:     uint8(idx.mode),
		Path:     idx.path.String(),
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	key := &storage.Val{Data: idxMetaKey(collDBID, idx.id)}
	val := &storage.Val{Data: data}
	return db.store.Put(metadbID, key, val, false)
}

type indexMeta struct {
	CollDBID storage.DBID `json:"colldbid"`
	DBID     storage.DBID `json:"dbid"`
	Mode     uint8        `json:"mode"`
	Path     string       `json:"path"`
}

func (c *Collection) loadIndexes() error {
	c.indexes = nil

	cur, err := c.store.NewCursor(metadbID)
	if err != nil {
		return err
	}
	defer cur.Close()

	prefix := fmt.Sprintf("i.%d.", c.id)
	if err := cur.Seek(storage.CursorGE, &storage.Val{Data: []byte(prefix)}); err != nil {
		return err
	}

	for {
		key, val, err := cur.Next()
		if err != nil {
			break
		}
		ks := string(key.Data)
		if len(ks) < len(prefix) || ks[:len(prefix)] != prefix {
			break
		}

		var meta indexMeta
		if err := json.Unmarshal(val.Data, &meta); err != nil {
			continue
		}

		flags := storage.DBFlags(0)
		mode := IndexMode(meta.Mode)
		if mode&IndexI64 != 0 {
			flags |= storage.DBVnum64Keys
		} else if mode&IndexF64 != 0 {
			flags |= storage.DBRealnumKeys
		}
		if mode&IndexUnique == 0 {
			flags |= storage.DBCompoundKeys
		}

		if _, err := c.store.Get(meta.DBID, nil); err == storage.ErrNotFound {
			if err := c.store.OpenDB(meta.DBID, flags); err != nil {
				return err
			}
		}

		idx := &Index{
			id:    meta.DBID,
			mode:  mode,
			path:  jsondoc.ParsePath(meta.Path),
			coll:  c,
			store: c.store,
			flags: flags,
		}
		c.indexes = append(c.indexes, idx)
	}
	return nil
}

func (c *Collection) addIndexRecords(docID int64, doc map[string]interface{}) error {
	for _, idx := range c.indexes {
		if err := idx.addRecord(docID, doc); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collection) removeIndexRecords(docID int64, doc map[string]interface{}) error {
	for _, idx := range c.indexes {
		if err := idx.removeRecord(docID, doc); err != nil {
			return err
		}
	}
	return nil
}

func (c *Collection) scanAll(fn func(id int64, doc []byte) bool) error {
	cur, err := c.store.NewCursor(c.id)
	if err != nil {
		return err
	}
	defer cur.Close()

	if err := cur.Seek(storage.CursorBeforeFirst, nil); err != nil {
		return err
	}

	for {
		key, val, err := cur.Next()
		if err != nil {
			break
		}
		id := keyToDocID(key.Data)
		if !fn(id, val.Data) {
			break
		}
	}
	return nil
}
