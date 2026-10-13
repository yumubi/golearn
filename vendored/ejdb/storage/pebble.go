package storage

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"

	"github.com/cockroachdb/pebble"
)

// PebbleStore implements KVStore using Pebble (LSM-tree).
type PebbleStore struct {
	db   *pebble.DB
	path string

	mu      sync.RWMutex
	flags   map[DBID]DBFlags
	closeMu sync.Mutex
	closed  bool
}

// PebbleOpts configures the Pebble backend.
type PebbleOpts struct {
	Path        string
	SyncWrites  bool
	CacheSize   int64 // default 64MB
	MemTableSize int64 // default 64MB
}

// NewPebbleStore opens or creates a Pebble-backed store.
func NewPebbleStore(opts PebbleOpts) (*PebbleStore, error) {
	po := &pebble.Options{}
	if opts.CacheSize > 0 {
		po.Cache = pebble.NewCache(opts.CacheSize)
	}
	if opts.MemTableSize > 0 {
		po.MemTableSize = uint64(opts.MemTableSize)
	}

	db, err := pebble.Open(opts.Path, po)
	if err != nil {
		return nil, err
	}
	return &PebbleStore{
		db:    db,
		path:  opts.Path,
		flags: make(map[DBID]DBFlags),
	}, nil
}

// encodePebbleKey prepends the DBID to the key (same encoding as badger.go).
func encodePebbleKey(db DBID, keyData []byte, compound int64) []byte {
	buf := make([]byte, 4+len(keyData))
	binary.BigEndian.PutUint32(buf[:4], uint32(db))
	copy(buf[4:], keyData)
	if compound != 0 {
		comp := make([]byte, 8)
		binary.BigEndian.PutUint64(comp, uint64(compound))
		buf = append(buf, comp...)
	}
	return buf
}

// prefix returns the 4-byte DBID prefix for prefix-scanning.
func (s *PebbleStore) prefix(db DBID) []byte {
	p := make([]byte, 4)
	binary.BigEndian.PutUint32(p, uint32(db))
	return p
}

// OpenDB stores DB flags (Pebble doesn't have explicit sub-databases).
func (s *PebbleStore) OpenDB(id DBID, flags DBFlags) error {
	s.mu.Lock()
	s.flags[id] = flags
	s.mu.Unlock()
	return nil
}

// CloseDB is a no-op for Pebble.
func (s *PebbleStore) CloseDB(id DBID) error {
	s.mu.Lock()
	delete(s.flags, id)
	s.mu.Unlock()
	return nil
}

// getFlags returns flags for a DBID.
func (s *PebbleStore) getFlags(id DBID) DBFlags {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.flags[id]
}

// Put inserts or updates a key-value pair.
func (s *PebbleStore) Put(db DBID, key, value *Val, noOverwrite bool) error {
	if key == nil {
		return nil
	}
	ekey := encodePebbleKey(db, key.Data, key.Compound)
	valData := []byte{}
	if value != nil {
		valData = value.Data
	}

	if noOverwrite {
		_, closer, err := s.db.Get(ekey)
		if err == nil {
			closer.Close()
			return ErrKeyExists
		}
		if err != pebble.ErrNotFound {
			return err
		}
	}

	return s.db.Set(ekey, valData, pebble.NoSync)
}

// Get retrieves a value by key.
func (s *PebbleStore) Get(db DBID, key *Val) (*Val, error) {
	if key == nil {
		return nil, ErrNotFound
	}
	ekey := encodePebbleKey(db, key.Data, key.Compound)
	val, closer, err := s.db.Get(ekey)
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	data := make([]byte, len(val))
	copy(data, val)
	closer.Close()
	return &Val{Data: data}, nil
}

// Del removes a key.
func (s *PebbleStore) Del(db DBID, key *Val) error {
	if key == nil {
		return nil
	}
	ekey := encodePebbleKey(db, key.Data, key.Compound)
	return s.db.Delete(ekey, pebble.NoSync)
}

// NewCursor creates a cursor over a DBID's keyspace.
func (s *PebbleStore) NewCursor(db DBID) (Cursor, error) {
	prefix := s.prefix(db)
	// Upper bound is prefix + 0x00...01 for prefix-scoped iteration
	upper := make([]byte, 4)
	binary.BigEndian.PutUint32(upper, uint32(db)+1)

	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	}
	iter, err := s.db.NewIter(opts)
	if err != nil {
		return nil, err
	}
	return &pebbleCursor{
		store:  s,
		db:     uint32(db),
		prefix: prefix,
		iter:   iter,
	}, nil
}

// pebbleCursor implements Cursor for Pebble.
type pebbleCursor struct {
	store  *PebbleStore
	db     uint32
	prefix []byte
	iter   *pebble.Iterator
	init   bool
}

func (c *pebbleCursor) Seek(op CursorOp, key *Val) error {
	c.init = true
	switch op {
	case CursorBeforeFirst:
		c.iter.First()
	case CursorAfterLast:
		c.iter.Last()
		if c.iter.Valid() {
			c.iter.Next() // position past last
		}
	case CursorNext:
		c.iter.Next()
	case CursorPrev:
		c.iter.Prev()
	case CursorEQ:
		ekey := encodePebbleKey(DBID(c.db), key.Data, key.Compound)
		c.iter.SeekGE(ekey)
		if c.iter.Valid() {
			if !bytes.Equal(c.iter.Key(), ekey) {
				// Not an exact match - position past end
				c.iter.SeekLT(ekey)
				if c.iter.Valid() {
					c.iter.Next()
				}
			}
		}
	case CursorGE:
		ekey := encodePebbleKey(DBID(c.db), key.Data, key.Compound)
		c.iter.SeekGE(ekey)
	}
	return nil
}

func (c *pebbleCursor) Next() (*Val, *Val, error) {
	if !c.init {
		c.iter.First()
		c.init = true
	}

	if !c.iter.Valid() {
		return nil, nil, ErrNoMore
	}

	fullKey := c.iter.Key()
	keyCopy := make([]byte, len(fullKey))
	copy(keyCopy, fullKey)

	// Verify key prefix matches our DB
	if len(keyCopy) < 4 || binary.BigEndian.Uint32(keyCopy[:4]) != c.db {
		return nil, nil, ErrNoMore
	}

	keyData := keyCopy[4:]

	valData := make([]byte, len(c.iter.Value()))
	copy(valData, c.iter.Value())

	key := &Val{Data: keyData}
	v := &Val{Data: valData}

	c.iter.Next()
	return key, v, nil
}

func (c *pebbleCursor) Close() error {
	return c.iter.Close()
}

// pebbleTxn implements Txn for Pebble (wraps either a Snapshot or direct DB access).
type pebbleTxn struct {
	db      *pebble.DB
	snap    *pebble.Snapshot
	isWrite bool
}

func (t *pebbleTxn) Get(db DBID, key *Val) (*Val, error) {
	if key == nil {
		return nil, ErrNotFound
	}
	ekey := encodePebbleKey(db, key.Data, key.Compound)

	var val []byte
	var closer io.Closer
	var err error

	if t.snap != nil {
		val, closer, err = t.snap.Get(ekey)
	} else {
		val, closer, err = t.db.Get(ekey)
	}
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	data := make([]byte, len(val))
	copy(data, val)
	closer.Close()
	return &Val{Data: data}, nil
}

func (t *pebbleTxn) Put(db DBID, key, value *Val, noOverwrite bool) error {
	if !t.isWrite {
		return nil
	}
	if key == nil {
		return nil
	}
	ekey := encodePebbleKey(db, key.Data, key.Compound)

	if noOverwrite {
		_, closer, err := t.db.Get(ekey)
		if err == nil {
			closer.Close()
			return ErrKeyExists
		}
		if err != pebble.ErrNotFound {
			return err
		}
	}

	valData := []byte{}
	if value != nil {
		valData = value.Data
	}
	return t.db.Set(ekey, valData, pebble.NoSync)
}

func (t *pebbleTxn) Del(db DBID, key *Val) error {
	if !t.isWrite {
		return nil
	}
	if key == nil {
		return nil
	}
	ekey := encodePebbleKey(db, key.Data, key.Compound)
	return t.db.Delete(ekey, pebble.NoSync)
}

func (t *pebbleTxn) NewCursor(db DBID) (Cursor, error) {
	return newPebbleTxnCursor(t.db, t.snap, db)
}

// newPebbleTxnCursor creates a cursor within a transaction context.
func newPebbleTxnCursor(dbp *pebble.DB, snap *pebble.Snapshot, db DBID) (Cursor, error) {
	prefix := make([]byte, 4)
	binary.BigEndian.PutUint32(prefix, uint32(db))
	upper := make([]byte, 4)
	binary.BigEndian.PutUint32(upper, uint32(db)+1)

	opts := &pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: upper,
	}

	var iter *pebble.Iterator
	var ierr error
	if snap != nil {
		iter, ierr = snap.NewIter(opts)
	} else {
		iter, ierr = dbp.NewIter(opts)
	}
	if ierr != nil {
		return nil, ierr
	}

	return &pebbleCursor{
		db:     uint32(db),
		prefix: prefix,
		iter:   iter,
	}, nil
}

// View runs a read-only transaction using a snapshot.
func (s *PebbleStore) View(fn func(txn Txn) error) error {
	snap := s.db.NewSnapshot()
	defer snap.Close()
	return fn(&pebbleTxn{db: s.db, snap: snap, isWrite: false})
}

// Update runs a read-write transaction.
func (s *PebbleStore) Update(fn func(txn Txn) error) error {
	return fn(&pebbleTxn{db: s.db, isWrite: true})
}

// Sync flushes pending writes.
func (s *PebbleStore) Sync() error {
	return s.db.Flush()
}

// Close shuts down the store. Idempotent (safe to call multiple times).
func (s *PebbleStore) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return nil
	}
	s.closed = true
	s.closeMu.Unlock()
	return s.db.Close()
}
