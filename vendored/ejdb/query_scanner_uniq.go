package ejdb

import (
	"ejdb/jsondoc"
	"ejdb/storage"
	"ejdb/types"
)

// uniqScanner scans a unique index. Ported from jbi_uniq_scanner.c.
func uniqScanner(ctx *execCtx, consumer docConsumer) error {
	idx := ctx.midx.idx
	if idx == nil {
		return fullScanner(ctx, consumer)
	}

	m := &ctx.midx
	if m.expr1 != nil {
		switch m.expr1.op {
		case types.OpEQ:
			return uniqConsumeEQ(ctx, m, consumer)
		case types.OpIN:
			return uniqConsumeIN(ctx, m, consumer)
		default:
			return uniqConsumeScan(ctx, m, consumer)
		}
	}
	return uniqConsumeScan(ctx, m, consumer)
}

// uniqConsumeEQ performs a direct lookup via Get for unique indexes.
func uniqConsumeEQ(ctx *execCtx, m *indexMatch, consumer docConsumer) error {
	if m.expr1 == nil {
		return nil
	}
	val := toValue(m.expr1.value)
	keyData, err := jsondoc.SerializeKey(val, m.idx.mode)
	if err != nil {
		return nil
	}

	key := &storage.Val{Data: keyData}
	result, err := m.idx.store.Get(m.idx.id, key)
	if err != nil {
		return nil // not found
	}

	// Value contains the document ID
	id := keyToDocID(result.Data)
	ctx.coll.mu.RLock()
	doc, ok := ctx.coll.getDocUnsafe(id, make([]byte, 8))
	ctx.coll.mu.RUnlock()
	if !ok {
		return nil
	}
	consumer(id, doc)
	return nil
}

// uniqConsumeIN handles IN operator for unique indexes.
func uniqConsumeIN(ctx *execCtx, m *indexMatch, consumer docConsumer) error {
	if m.expr1 == nil {
		return nil
	}
	ctx.coll.mu.RLock()
	defer ctx.coll.mu.RUnlock()
	var keyBuf [8]byte
	for _, v := range m.expr1.values {
		val := toValue(v)
		keyData, err := jsondoc.SerializeKey(val, m.idx.mode)
		if err != nil {
			continue
		}

		key := &storage.Val{Data: keyData}
		result, err := m.idx.store.Get(m.idx.id, key)
		if err != nil {
			continue
		}

		id := keyToDocID(result.Data)
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

// uniqConsumeScan performs a range scan on a unique index.
func uniqConsumeScan(ctx *execCtx, m *indexMatch, consumer docConsumer) error {
	cur, err := m.idx.store.NewCursor(m.idx.id)
	if err != nil {
		return err
	}
	defer cur.Close()

	initOp := m.cursorInit
	if initOp == 0 {
		initOp = storage.CursorBeforeFirst
	}

	var seekKey *storage.Val
	if m.expr1 != nil && (initOp == storage.CursorGE || initOp == storage.CursorEQ) {
		val := toValue(m.expr1.value)
		keyData, _ := jsondoc.SerializeKey(val, m.idx.mode)
		seekKey = &storage.Val{Data: keyData}
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
		key, val, err := cur.Next()
		if err != nil {
			break
		}

		// Check if we've passed the range end
		if m.expr2 != nil {
			limit := toValue(m.expr2.value)
			if !matchRangeEnd(key.Data, limit, m.idx.mode, m) {
				break
			}
		}

		id := keyToDocID(val.Data)
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

// matchRangeEnd checks if the current key is still within the range boundary.
func matchRangeEnd(keyData []byte, limit *types.Value, mode IndexMode, m *indexMatch) bool {
	curVal, err := jsondoc.DeserializeKey(keyData, mode)
	if err != nil {
		return true // continue scanning
	}
	cmp := types.CompareValues(curVal, limit)
	switch {
	case m.expr2.op == types.OpLT || m.expr2.op == types.OpLTE:
		if m.expr2.op == types.OpLT {
			return cmp < 0
		}
		return cmp <= 0
	case m.expr2.op == types.OpGT || m.expr2.op == types.OpGTE:
		if m.expr2.op == types.OpGT {
			return cmp > 0
		}
		return cmp >= 0
	}
	return true
}

// toValue converts a raw interface{} to a *types.Value using the shared implementation.
// This avoids float64->int64 precision loss that the old local version had.
func toValue(raw interface{}) *types.Value {
	return jsondoc.ToValue(raw)
}
