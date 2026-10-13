package ejdb

import (
	"ejdb/storage"
)

// fullScanner performs a full collection scan. Ported from jbi_full_scanner.c.
func fullScanner(ctx *execCtx, consumer docConsumer) error {
	cur, err := ctx.coll.store.NewCursor(ctx.coll.id)
	if err != nil {
		return err
	}
	defer cur.Close()

	op := ctx.midx.cursorInit
	if op == 0 {
		op = storage.CursorBeforeFirst
	}
	if err := cur.Seek(op, nil); err != nil {
		return err
	}

	lastID := int64(-1)
	for {
		key, val, err := cur.Next()
		if err != nil {
			break
		}
		id := keyToDocID(key.Data)
		if id == lastID {
			continue
		}
		lastID = id

		if !consumer(id, val.Data) {
			break
		}
	}
	return nil
}
