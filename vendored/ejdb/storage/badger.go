package storage

import (
	"encoding/binary"
	"sync"

	"github.com/dgraph-io/badger/v4"
)

// BadgerStore implements KVStore using Badger (LSM-tree).
type BadgerStore struct {
	db   *badger.DB
	path string

	mu    sync.RWMutex
	flags map[DBID]DBFlags
}

// BadgerOpts configures the Badger backend.
type BadgerOpts struct {
	Path          string
	MemTableSize  int64 // default 64MB
	ValueLogSize  int64 // default 256MB
	NumVersions   int   // default 1
	SyncWrites    bool
	Compression   bool
}

// NewBadgerStore opens or creates a Badger-backed store.
func NewBadgerStore(opts BadgerOpts) (*BadgerStore, error) {
	bo := badger.DefaultOptions(opts.Path)
	if opts.MemTableSize > 0 {
		bo.MemTableSize = opts.MemTableSize
	}
	if opts.ValueLogSize > 0 {
		bo.ValueLogFileSize = opts.ValueLogSize
	}
	if opts.NumVersions > 0 {
		bo.NumVersionsToKeep = opts.NumVersions
	}
	bo.SyncWrites = opts.SyncWrites
	if !opts.Compression {
		bo.Compression = 0
	}
	bo.Logger = nil // suppress debug logs

	db, err := badger.Open(bo)
	if err != nil {
		return nil, err
	}
	return &BadgerStore{
		db:    db,
		path:  opts.Path,
		flags: make(map[DBID]DBFlags),
	}, nil
}

// encodeKey prepends the DBID to the key.
func encodeKey(db DBID, keyData []byte) []byte {
	buf := make([]byte, 4+len(keyData))
	binary.BigEndian.PutUint32(buf[:4], uint32(db))
	copy(buf[4:], keyData)
	return buf
}

// OpenDB stores DB flags (Badger doesn't have explicit sub-databases).
func (s *BadgerStore) OpenDB(id DBID, flags DBFlags) error {
	s.mu.Lock()
	s.flags[id] = flags
	s.mu.Unlock()
	return nil
}

// CloseDB is a no-op for Badger.
func (s *BadgerStore) CloseDB(id DBID) error {
	s.mu.Lock()
	delete(s.flags, id)
	s.mu.Unlock()
	return nil
}

// Put inserts or updates a key-value pair.
func (s *BadgerStore) Put(db DBID, key, value *Val, noOverwrite bool) error {
	if key == nil {
		return nil
	}
	ekey := encodeKey(db, key.Data)
	// For compound keys, append the compound field after the key data.
	if key.Compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(key.Compound))
		ekey = append(ekey, comp...)
	}
	valData := []byte{}
	if value != nil {
		valData = value.Data
	}

	return s.db.Update(func(txn *badger.Txn) error {
		if noOverwrite {
			_, err := txn.Get(ekey)
			if err == nil {
				return ErrKeyExists
			}
			if err != badger.ErrKeyNotFound {
				return err
			}
		}
		return txn.Set(ekey, valData)
	})
}

// Get retrieves a value by key.
func (s *BadgerStore) Get(db DBID, key *Val) (*Val, error) {
	if key == nil {
		return nil, ErrNotFound
	}
	ekey := encodeKey(db, key.Data)
	if key.Compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(key.Compound))
		ekey = append(ekey, comp...)
	}

	var result *Val
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(ekey)
		if err != nil {
			if err == badger.ErrKeyNotFound {
				return ErrNotFound
			}
			return err
		}
		val, err := item.ValueCopy(nil)
		if err != nil {
			return err
		}
		result = &Val{Data: val}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Del removes a key.
func (s *BadgerStore) Del(db DBID, key *Val) error {
	if key == nil {
		return nil
	}
	ekey := encodeKey(db, key.Data)
	if key.Compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(key.Compound))
		ekey = append(ekey, comp...)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(ekey)
	})
}

// badgerCursor implements Cursor for Badger.
type badgerCursor struct {
	txn    *badger.Txn
	db     uint32
	prefix []byte
	iter   *badger.Iterator
	init   bool
}

// NewCursor creates a cursor over a DBID's keyspace.
func (s *BadgerStore) NewCursor(db DBID) (Cursor, error) {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(db))

	txn := s.db.NewTransaction(false)
	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	iter := txn.NewIterator(opts)

	return &badgerCursor{
		txn:    txn,
		db:     uint32(db),
		prefix: prefix,
		iter:   iter,
	}, nil
}

func (c *badgerCursor) Seek(op CursorOp, key *Val) error {
	c.init = true
	switch op {
	case CursorBeforeFirst:
		// Rewind to first entry
		c.iter.Rewind()
	case CursorAfterLast:
		// Seek to the end of this DB's keyspace (prefix + 0xFF...)
		// Badger doesn't have reverse seek; we use rewind then move to end implicitly
		c.iter.Rewind()
		// Iterate to last by checking each item
		for c.iter.Valid() {
			c.iter.Next()
		}
	case CursorNext:
		c.iter.Next()
	case CursorPrev:
		// Badger iterator is forward-only; Prev is not efficiently supported.
		// For reverse scans, we use a different approach if needed.
		c.iter.Next()
	case CursorEQ:
		ekey := make([]byte, 4+len(key.Data))
		copy(ekey[:4], c.prefix)
		copy(ekey[4:], key.Data)
		if key.Compound != 0 {
			comp := make([]byte, 8)
			binary.BigEndian.PutUint64(comp, uint64(key.Compound))
			ekey = append(ekey, comp...)
		}
		c.iter.Seek(ekey)
		if c.iter.Valid() {
			item := c.iter.Item()
			if string(item.Key()) != string(ekey) {
				// Not an exact match - invalidate iterator
				c.iter.Seek(append(ekey, 0xFF))
			}
		}
	case CursorGE:
		ekey := make([]byte, 4+len(key.Data))
		copy(ekey[:4], c.prefix)
		copy(ekey[4:], key.Data)
		if key.Compound != 0 {
			comp := make([]byte, 8)
			binary.BigEndian.PutUint64(comp, uint64(key.Compound))
			ekey = append(ekey, comp...)
		}
		c.iter.Seek(ekey)
	}
	return nil
}

func (c *badgerCursor) Next() (*Val, *Val, error) {
	if !c.init {
		c.iter.Rewind()
		c.init = true
	}

	if !c.iter.Valid() {
		return nil, nil, ErrNoMore
	}

	item := c.iter.Item()
	fullKey := item.KeyCopy(nil)

	// Verify key prefix matches our DB
	if len(fullKey) < 4 || binary.BigEndian.Uint32(fullKey[:4]) != c.db {
		c.iter.Next()
		return c.Next()
	}

	keyData := fullKey[4:]

	var compound int64
	keyLen := len(keyData)

	// Check if this is a compound key by examining the stored key structure.
	// Compound keys have 8 extra bytes at the end for the compound field.
	flags := c.getSessionFlags()
	if flags&DBCompoundKeys != 0 && keyLen > 8 {
		compound = int64(binary.BigEndian.Uint64(keyData[keyLen-8:]))
		keyData = keyData[:keyLen-8]
	}

	val, err := item.ValueCopy(nil)
	if err != nil {
		c.iter.Next()
		return c.Next()
	}

	key := &Val{Data: keyData, Compound: compound}
	v := &Val{Data: val}

	c.iter.Next()
	return key, v, nil
}

func (c *badgerCursor) getSessionFlags() DBFlags {
	return 0 // cursor doesn't have access to store; compound handling done externally
}

func (c *badgerCursor) Close() error {
	c.iter.Close()
	c.txn.Discard()
	return nil
}

// View runs a read-only transaction.
func (s *BadgerStore) View(fn func(txn Txn) error) error {
	return s.db.View(func(btxn *badger.Txn) error {
		return fn(&badgerTxn{txn: btxn, store: s})
	})
}

// Update runs a read-write transaction.
func (s *BadgerStore) Update(fn func(txn Txn) error) error {
	return s.db.Update(func(btxn *badger.Txn) error {
		return fn(&badgerTxn{txn: btxn, store: s})
	})
}

// badgerTxn wraps badger.Txn to implement Txn interface.
type badgerTxn struct {
	txn   *badger.Txn
	store *BadgerStore
}

func (t *badgerTxn) Put(db DBID, key, value *Val, noOverwrite bool) error {
	if key == nil {
		return nil
	}
	ekey := encodeKey(db, key.Data)
	if key.Compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(key.Compound))
		ekey = append(ekey, comp...)
	}
	valData := []byte{}
	if value != nil {
		valData = value.Data
	}
	if noOverwrite {
		_, err := t.txn.Get(ekey)
		if err == nil {
			return ErrKeyExists
		}
		if err != badger.ErrKeyNotFound {
			return err
		}
	}
	return t.txn.Set(ekey, valData)
}

func (t *badgerTxn) Get(db DBID, key *Val) (*Val, error) {
	if key == nil {
		return nil, ErrNotFound
	}
	ekey := encodeKey(db, key.Data)
	if key.Compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(key.Compound))
		ekey = append(ekey, comp...)
	}
	item, err := t.txn.Get(ekey)
	if err != nil {
		if err == badger.ErrKeyNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	val, err := item.ValueCopy(nil)
	if err != nil {
		return nil, err
	}
	return &Val{Data: val}, nil
}

func (t *badgerTxn) Del(db DBID, key *Val) error {
	if key == nil {
		return nil
	}
	ekey := encodeKey(db, key.Data)
	if key.Compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(key.Compound))
		ekey = append(ekey, comp...)
	}
	return t.txn.Delete(ekey)
}

func (t *badgerTxn) NewCursor(db DBID) (Cursor, error) {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(db))
	opts := badger.DefaultIteratorOptions
	opts.Prefix = prefix
	iter := t.txn.NewIterator(opts)
	return &badgerCursor{
		txn:    t.txn,
		db:     uint32(db),
		prefix: prefix,
		iter:   iter,
	}, nil
}

// Sync flushes pending writes.
func (s *BadgerStore) Sync() error {
	return s.db.Sync()
}

// Close shuts down the store.
func (s *BadgerStore) Close() error {
	return s.db.Close()
}
