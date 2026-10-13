# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

EJDB is a Go port of the C EJDB2 embeddable JSON database engine. It stores JSON documents in collections with indexing and query support, backed by Badger (LSM-tree KV store).

## Commands

```sh
go test ./...          # Run all tests
go test -v -run <Name> # Run a single test (e.g., TestQueryEQ)
go build ./...         # Build all packages
go vet ./...           # Static analysis
```

## Architecture

### Package layout

```
ejdb/          — Main package: DB, Collection, Index, Query types
storage/       — KVStore abstraction (interface + Badger implementation)
jsondoc/       — JSON path parsing, value serialization for index keys
types/         — Shared types: IndexMode, Op, Value, CompareValues
examples/      — Usage examples (crud, index, query)
c-code/        — Original EJDB2 C source (reference only, not part of Go build)
```

### Key architectural concepts

**Multi-DBID namespace on top of a single Badger instance**: Each collection and each index is assigned a unique `DBID` (uint32). All keys are prefixed with the 4-byte big-endian DBID encoding, so Badger's prefix-based iteration simulates separate databases:
- DBID 1 = metadata (collection/index definitions)
- DBID 2 = document count tracking
- DBID 100+ = user collections and their indexes (sequential allocation)

**Document storage**: Documents are stored as raw JSON bytes keyed by big-endian int64 document IDs. Auto-incrementing IDs are maintained per-collection via `atomic.AddInt64`.

**Index storage**: Index entries map JSON field values to document IDs.
- **Unique indexes**: key = serialized field value, value = doc ID (8 bytes)
- **Non-unique indexes**: key = serialized field value + compound doc ID suffix (last 8 bytes), value = empty. The compound suffix allows multiple documents with the same field value.

**Index key serialization** (see `jsondoc/value.go`):
- I64: big-endian with sign bit flip (for lexicographic ordering of signed ints)
- F64: `strconv.FormatFloat(f, 'g', -1, 64)` — string representation for lex order
- Str: raw string bytes

**Query engine**:
- Chainable query builder: `db.Query("coll").Where(Eq("age", 30)).OrderBy(Asc("name")).Skip(5).Limit(10).All(ctx)`
- Index selection is heuristic-based: operator weight (EQ > IN > GT > Prefix > LT) -> range end presence -> index size -> path length
- OR and NOT conditions at the top level disable index usage
- Compound key scanners (`query_scanner_dup.go`) iterate with `CursorGE` at the min compound value and break on prefix mismatch
- Unique key scanners (`query_scanner_uniq.go`) use direct `Get` for EQ lookups
- ORDER BY uses in-memory sort when no index supports the sort order; if an index's natural scan order matches the sort, sorting is elided

**JSON path format**: Accepts both `"address.city"` and `"/address/city"` forms (see `jsondoc/path.go`).

**Storage backend**: Badger is the only implemented backend. The `KVStore` interface in `storage/storage.go` abstracts it, with bbolt listed as planned but unimplemented.
