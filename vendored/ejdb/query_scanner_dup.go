package ejdb

import (
	"encoding/binary"
	"math"

	"ejdb/jsondoc"
	"ejdb/storage"
	"ejdb/types"
)

const (
	minCompound int64 = 0
	maxCompound int64 = math.MaxInt64
)

// dupScanner scans a non-unique (compound-key) index. Ported from jbi_dup_scanner.c.
func dupScanner(ctx *execCtx, consumer docConsumer) error {
	idx := ctx.midx.idx
	if idx == nil {
		return fullScanner(ctx, consumer)
	}
	m := &ctx.midx
	if m.expr1 != nil {
		switch m.expr1.op {
		case types.OpEQ:
			return dupConsumeEQ(ctx, m, consumer)
		case types.OpIN:
			return dupConsumeIN(ctx, m, consumer)
		default:
			return dupConsumeScan(ctx, m, consumer)
		}
	}
	return dupConsumeScan(ctx, m, consumer)
}

func dupConsumeEQ(ctx *execCtx, m *indexMatch, consumer docConsumer) error {
	val := toValue(m.expr1.value)
	keyData, err := jsondoc.SerializeKey(val, m.idx.mode)
	if err != nil {
		return nil
	}

	key := &storage.Val{Data: keyData, Compound: minCompound}
	cur, err := m.idx.store.NewCursor(m.idx.id)
	if err != nil {
		return err
	}
	defer cur.Close()

	if err := cur.Seek(storage.CursorGE, key); err != nil {
		return err
	}

	// Batch lock for the entire scan (not per-document)
	ctx.coll.mu.RLock()
	defer ctx.coll.mu.RUnlock()

	lastID := int64(-1)
	var keyBuf [8]byte
	for {
		k, _, err := cur.Next()
		if err != nil {
			break
		}
		if !keyPrefixMatch(k.Data, keyData) {
			break
		}
		id := extractCompoundID(k.Data)
		if id == lastID {
			continue
		}
		lastID = id

		doc, ok := ctx.coll.getDocUnsafe(id, keyBuf[:])
		if !ok {
			continue
		}
		if !consumer(id, doc) {
			return nil
		}
	}
	return nil
}

func dupConsumeIN(ctx *execCtx, m *indexMatch, consumer docConsumer) error {
	// Batch lock for the entire scan
	ctx.coll.mu.RLock()
	defer ctx.coll.mu.RUnlock()

	var keyBuf [8]byte
	for _, v := range m.expr1.values {
		val := toValue(v)
		keyData, err := jsondoc.SerializeKey(val, m.idx.mode)
		if err != nil {
			continue
		}

		key := &storage.Val{Data: keyData, Compound: minCompound}
		cur, err := m.idx.store.NewCursor(m.idx.id)
		if err != nil {
			continue
		}

		if err := cur.Seek(storage.CursorGE, key); err != nil {
			cur.Close()
			continue
		}

		lastID := int64(-1)
		for {
			k, _, err := cur.Next()
			if err != nil {
				break
			}
			if !keyPrefixMatch(k.Data, keyData) {
				break
			}
			id := extractCompoundID(k.Data)
			if id == lastID {
				continue
			}
			lastID = id

			doc, ok := ctx.coll.getDocUnsafe(id, keyBuf[:])
			if !ok {
				continue
			}
			if !consumer(id, doc) {
				cur.Close()
				return nil
			}
		}
		cur.Close()
	}
	return nil
}

func dupConsumeScan(ctx *execCtx, m *indexMatch, consumer docConsumer) error {
	cur, err := m.idx.store.NewCursor(m.idx.id)
	if err != nil {
		return err
	}
	defer cur.Close()

	var seekKey *storage.Val
	if m.expr1 != nil {
		val := toValue(m.expr1.value)
		keyData, _ := jsondoc.SerializeKey(val, m.idx.mode)
		comp := minCompound
		if m.cursorStep == storage.CursorPrev {
			comp = maxCompound
		}
		seekKey = &storage.Val{Data: keyData, Compound: comp}
	}

	initOp := m.cursorInit
	if initOp == 0 {
		initOp = storage.CursorBeforeFirst
	}

	if err := cur.Seek(initOp, seekKey); err != nil {
		return err
	}

	// Batch lock for the entire scan
	ctx.coll.mu.RLock()
	defer ctx.coll.mu.RUnlock()

	var keyBuf [8]byte
	lastID := int64(-1)
	for {
		k, _, err := cur.Next()
		if err != nil {
			break
		}

		if m.expr2 != nil {
			limit := toValue(m.expr2.value)
			if !matchRangeEnd(keyDataPart(k.Data), limit, m.idx.mode, m) {
				break
			}
		}

		id := extractCompoundID(k.Data)
		if id == lastID {
			continue
		}
		lastID = id

		doc, ok := ctx.coll.getDocUnsafe(id, keyBuf[:])
		if !ok {
			continue
		}
		if !consumer(id, doc) {
			return nil
		}
	}
	return nil
}

func keyPrefixMatch(key, prefix []byte) bool {
	if len(key) < len(prefix) {
		return false
	}
	for i := range prefix {
		if key[i] != prefix[i] {
			return false
		}
	}
	return true
}

func keyDataPart(key []byte) []byte {
	if len(key) <= 8 {
		return key
	}
	return key[:len(key)-8]
}

func extractCompoundID(key []byte) int64 {
	if len(key) < 8 {
		return 0
	}
	return int64(binary.BigEndian.Uint64(key[len(key)-8:]))
}
