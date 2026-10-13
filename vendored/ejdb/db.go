package ejdb

import (
	"encoding/binary"
	json "github.com/goccy/go-json"
	"fmt"
	"path/filepath"
	"sync"

	"ejdb/storage"
)

const (
	metadbID  storage.DBID = 1
	countdbID storage.DBID = 2
	userDBID  storage.DBID = 100 // user databases start at 100
)

// DBOpts configures a DB instance.
type DBOpts struct {
	Path        string
	StorageType string // "badger" (default) or "bbolt"
}

// DB is the main database instance.
type DB struct {
	store  storage.KVStore
	path   string
	colls  map[string]*Collection
	mu     sync.RWMutex
	open   bool
	nextDB storage.DBID // next available database ID
}

// Open opens or creates a database at the given path.
func Open(path string) (*DB, error) {
	return OpenWithOpts(DBOpts{Path: path, StorageType: "pebble"})
}

// OpenWithOpts opens a database with custom options.
func OpenWithOpts(opts DBOpts) (*DB, error) {
	path := opts.Path
	if path == "" {
		path = "ejdb_data"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	var store storage.KVStore
	switch opts.StorageType {
	case "badger":
		store, err = storage.NewBadgerStore(storage.BadgerOpts{
			Path:         absPath,
			MemTableSize: 64 << 20,  // 64MB
			ValueLogSize: 256 << 20, // 256MB
			SyncWrites:   true,
			Compression:  true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open Badger store: %w", err)
		}
	case "bbolt":
		return nil, fmt.Errorf("BBolt backend not yet implemented")
	default:
		store, err = storage.NewPebbleStore(storage.PebbleOpts{
			Path:       absPath,
			SyncWrites: true,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to open Pebble store: %w", err)
		}
	}

	db := &DB{
		store:  store,
		path:   absPath,
		colls:  make(map[string]*Collection),
		open:   true,
		nextDB: userDBID,
	}

	// Open metadata and count databases
	if err := store.OpenDB(metadbID, 0); err != nil {
		store.Close()
		return nil, fmt.Errorf("open metadb: %w", err)
	}
	if err := store.OpenDB(countdbID, storage.DBVnum64Keys); err != nil {
		store.Close()
		return nil, fmt.Errorf("open countdb: %w", err)
	}

	// Load existing collections
	if err := db.loadCollections(); err != nil {
		store.Close()
		return nil, fmt.Errorf("load collections: %w", err)
	}

	return db, nil
}

// Close shuts down the database.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.open = false

	// Persist document counts before closing
	for _, coll := range db.colls {
		_ = saveDocCount(db.store, coll.id, coll.rnum)
	}
	db.colls = make(map[string]*Collection)

	if err := db.store.CloseDB(metadbID); err != nil {
		return err
	}
	if err := db.store.CloseDB(countdbID); err != nil {
		return err
	}
	for i := userDBID; i < db.nextDB; i++ {
		_ = db.store.CloseDB(i)
	}
	return db.store.Close()
}

// Collection returns a collection by name, or nil if not found.
func (db *DB) Collection(name string) *Collection {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.colls[name]
}

// CreateCollection creates a new collection.
func (db *DB) CreateCollection(name string) (*Collection, error) {
	db.mu.Lock()
	defer db.mu.Unlock()

	if !db.open {
		return nil, fmt.Errorf("database is closed")
	}
	if _, exists := db.colls[name]; exists {
		return nil, fmt.Errorf("collection %q already exists", name)
	}

	dbid := db.nextDB
	db.nextDB++

	if err := db.store.OpenDB(dbid, storage.DBVnum64Keys); err != nil {
		return nil, fmt.Errorf("open collection db: %w", err)
	}

	coll := &Collection{
		id:    dbid,
		name:  name,
		db:    db,
		store: db.store,
	}

	// Persist collection metadata
	meta := collectionMeta{Name: name, DBID: dbid}
	if err := db.saveCollectionMeta(coll, &meta); err != nil {
		_ = db.store.CloseDB(dbid)
		return nil, err
	}

	db.colls[name] = coll
	return coll, nil
}

// Query returns a new query builder for the given collection.
func (db *DB) Query(collName string) *Query { // defined in query/builder.go
	return &Query{db: db, collName: collName}
}

type collectionMeta struct {
	Name string       `json:"name"`
	DBID storage.DBID `json:"dbid"`
}

func (db *DB) saveCollectionMeta(coll *Collection, meta *collectionMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	key := &storage.Val{Data: collMetaKey(coll.id)}
	val := &storage.Val{Data: data}
	return db.store.Put(metadbID, key, val, false)
}

func collMetaKey(dbid storage.DBID) []byte {
	return []byte(fmt.Sprintf("c.%d", dbid))
}

func idxMetaKey(collDBID, idxDBID storage.DBID) []byte {
	return []byte(fmt.Sprintf("i.%d.%d", collDBID, idxDBID))
}

func countKey(dbid storage.DBID) *storage.Val {
	return &storage.Val{Data: []byte(fmt.Sprintf("n.%d", dbid))}
}

func saveDocCount(store storage.KVStore, collID storage.DBID, count int64) error {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(count))
	return store.Put(countdbID, countKey(collID), &storage.Val{Data: buf}, false)
}

func loadDocCount(store storage.KVStore, collID storage.DBID) int64 {
	val, err := store.Get(countdbID, countKey(collID))
	if err != nil {
		return 0
	}
	if len(val.Data) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(val.Data[:8]))
}

func (db *DB) loadCollections() error {
	cur, err := db.store.NewCursor(metadbID)
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
		// Only load collection metadata (keys starting with "c.")
		ks := string(key.Data)
		if len(ks) < 2 || ks[:2] != "c." {
			continue
		}

		var meta collectionMeta
		if err := json.Unmarshal(val.Data, &meta); err != nil {
			continue
		}

		coll := &Collection{
			id:    meta.DBID,
			name:  meta.Name,
			db:    db,
			store: db.store,
		}

		if _, err := db.store.Get(coll.id, nil); err == storage.ErrNotFound {
			if err := db.store.OpenDB(coll.id, storage.DBVnum64Keys); err != nil {
				return err
			}
		}

		// Update nextDB
		if coll.id >= db.nextDB {
			db.nextDB = coll.id + 1
		}

		// Load indexes for this collection
		if err := coll.loadIndexes(); err != nil {
			return fmt.Errorf("load indexes for %q: %w", meta.Name, err)
		}

		// Load persistent document count (avoid O(n) scanAll)
		coll.rnum = loadDocCount(db.store, coll.id)

		db.colls[meta.Name] = coll
	}
	return nil
}

// docIDToKey encodes an int64 document ID as a big-endian byte key.
func docIDToKey(id int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(id))
	return buf
}

// docIDToKeyBuf writes the big-endian int64 key into a caller-provided buffer.
// The buffer must be at least 8 bytes.
func docIDToKeyBuf(id int64, buf []byte) {
	binary.BigEndian.PutUint64(buf, uint64(id))
}

// keyToDocID decodes a key back to int64 document ID.
func keyToDocID(key []byte) int64 {
	if len(key) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(key[:8]))
}

// allocDBID allocates a new database ID.
func (db *DB) allocDBID() storage.DBID {
	id := db.nextDB
	db.nextDB++
	return id
}

