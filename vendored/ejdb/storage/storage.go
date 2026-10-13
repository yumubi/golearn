package storage

import "errors"

// DBID is a database identifier within a KV store.
type DBID uint32

// DBFlags configure database behavior.
type DBFlags uint32

const (
	DBVnum64Keys   DBFlags = 1 << 5 // variable-length number keys
	DBCompoundKeys DBFlags = 1 << 6 // compound keys: <key><compound>
	DBRealnumKeys  DBFlags = 1 << 4 // float keys serialized as strings
)

// CursorOp defines cursor positioning operations.
type CursorOp int

const (
	CursorBeforeFirst CursorOp = iota + 1 // position before first record
	CursorAfterLast                       // position after last record
	CursorNext                            // move to next record
	CursorPrev                            // move to previous record
	CursorEQ                              // exact key match
	CursorGE                              // greater than or equal
)

// Val represents a key or value in the KV store.
type Val struct {
	Data     []byte
	Compound int64 // extra key part for compound keys
}

// EmptyVal is a shared zero-size value used for compound key entries.
var EmptyVal = Val{}

// Cursor iterates over KV pairs.
type Cursor interface {
	// Seek positions the cursor. For BeforeFirst/AfterLast, key is ignored.
	Seek(op CursorOp, key *Val) error
	// Next advances the cursor. Returns (key, value, error).
	// Returns ErrNoMore when exhausted.
	Next() (*Val, *Val, error)
	// Close releases cursor resources.
	Close() error
}

// Txn represents a read or write transaction.
type Txn interface {
	Put(db DBID, key, value *Val, noOverwrite bool) error
	Get(db DBID, key *Val) (*Val, error)
	Del(db DBID, key *Val) error
	NewCursor(db DBID) (Cursor, error)
}

// KVStore is the abstract interface for embedded KV storage.
type KVStore interface {
	// OpenDB opens or creates a named database within the store.
	OpenDB(id DBID, flags DBFlags) error
	// CloseDB closes a database.
	CloseDB(id DBID) error
	// Put inserts or updates a record.
	Put(db DBID, key, value *Val, noOverwrite bool) error
	// Get retrieves a record by key. Returns ErrNotFound if absent.
	Get(db DBID, key *Val) (*Val, error)
	// Del removes a record.
	Del(db DBID, key *Val) error
	// NewCursor creates a cursor for iteration.
	NewCursor(db DBID) (Cursor, error)
	// View runs a read-only transaction.
	View(fn func(txn Txn) error) error
	// Update runs a read-write transaction.
	Update(fn func(txn Txn) error) error
	// Close shuts down the store and flushes pending writes.
	Close() error
	// Sync flushes pending writes to disk.
	Sync() error
}

// ErrNotFound is returned when a key does not exist.
var ErrNotFound = errors.New("key not found")

// ErrNoMore is returned by Cursor.Next when exhausted.
var ErrNoMore = errors.New("no more entries")

// ErrKeyExists is returned on Put with noOverwrite when key already exists.
var ErrKeyExists = errors.New("key already exists")
