package ejdb

import (
	"context"
	json "github.com/goccy/go-json"
	"fmt"
	"strings"

	"ejdb/jsondoc"
	"ejdb/storage"
	"ejdb/types"
)

type execCtx struct {
	query     *Query
	coll      *Collection
	db        *DB
	scanner   scannerFunc
	midx      indexMatch
	sorting   bool
	sortCtx   *sortContext
	results   []interface{}
	countOnly bool
}

type indexMatch struct {
	idx            *Index
	leaf           *leafCondition
	expr1          *leafCondition
	expr2          *leafCondition
	cursorInit     storage.CursorOp
	cursorStep     storage.CursorOp
	orderBySupport bool
}

type (
	scannerFunc func(ctx *execCtx, consumer docConsumer) error
	docConsumer func(id int64, doc []byte) bool
)

func executeQuery(q *Query, _ context.Context) ([]interface{}, error) {
	coll := q.db.Collection(q.collName)
	if coll == nil {
		return nil, fmt.Errorf("collection %q not found", q.collName)
	}

	exec := &execCtx{
		query:     q,
		coll:      coll,
		db:        q.db,
		countOnly: q.mode&types.QueryCountOnly != 0,
	}

	if err := selectIndexes(exec); err != nil {
		return nil, err
	}

	var consumer docConsumer
	if exec.sorting {
		consumer = sorterConsumer(exec)
	} else {
		consumer = plainConsumer(exec)
	}

	if err := exec.scanner(exec, consumer); err != nil {
		return nil, err
	}

	if exec.sorting && exec.sortCtx != nil {
		exec.sortCtx.finalize(exec)
	}

	return exec.results, nil
}

func plainConsumer(exec *execCtx) docConsumer {
	skip := exec.query.skip
	limit := exec.query.limit
	skipped := int64(0)
	delivered := int64(0)
	cond := exec.query.cond
	hasProj := len(exec.query.proj) > 0

	// Build simplified condition excluding already-satisfied index leaf
	remainingCond := cond
	if exec.midx.idx != nil && exec.midx.leaf != nil {
		remainingCond = stripCondition(cond, exec.midx.leaf)
	}

	return func(id int64, doc []byte) bool {
		if limit > 0 && delivered >= limit {
			return false
		}

		// Fast path: count only, no conditions, no projection
		if remainingCond == nil && !hasProj && exec.countOnly {
			delivered++
			return true
		}

		if remainingCond != nil || hasProj {
			// Parse once and share between match and project
			var parsed map[string]interface{}
			if err := json.Unmarshal(doc, &parsed); err != nil {
				return true
			}

			if remainingCond != nil && !matchDocumentParsed(parsed, remainingCond) {
				return true
			}
			if skip > 0 && skipped < skip {
				skipped++
				return true
			}
			delivered++
			if exec.countOnly {
				return true
			}
			output, err := applyProjectionFromMap(parsed, exec.query.proj)
			if err != nil {
				return true
			}
			exec.results = append(exec.results, output)
			return true
		}

		// No conditions, no projection — count
		if skip > 0 && skipped < skip {
			skipped++
			return true
		}
		delivered++
		if exec.countOnly {
			return true
		}
		output, err := applyProjection(doc, exec.query.proj)
		if err != nil {
			return true
		}
		exec.results = append(exec.results, output)
		return true
	}
}

func matchDocument(doc []byte, cond Condition) bool {
	if cond == nil {
		return true
	}
	switch c := cond.(type) {
	case leafCondition:
		return matchLeaf(doc, &c)
	case *andCondition:
		for _, child := range c.children {
			if !matchDocument(doc, child) {
				return false
			}
		}
		return true
	case *orCondition:
		for _, child := range c.children {
			if matchDocument(doc, child) {
				return true
			}
		}
		return false
	case *notCondition:
		return !matchDocument(doc, c.child)
	}
	return false
}

// matchDocumentParsed is like matchDocument but operates on an already-parsed map.
func matchDocumentParsed(doc map[string]interface{}, cond Condition) bool {
	if cond == nil {
		return true
	}
	switch c := cond.(type) {
	case leafCondition:
		return matchLeafParsed(doc, &c)
	case *andCondition:
		for _, child := range c.children {
			if !matchDocumentParsed(doc, child) {
				return false
			}
		}
		return true
	case *orCondition:
		for _, child := range c.children {
			if matchDocumentParsed(doc, child) {
				return true
			}
		}
		return false
	case *notCondition:
		return !matchDocumentParsed(doc, c.child)
	}
	return false
}

func matchLeaf(doc []byte, l *leafCondition) bool {
	val, err := l.path.Extract(doc)
	if err != nil || val == nil {
		return false
	}
	switch l.op {
	case types.OpEQ:
		return compareInterface(val, l.value) == 0
	case types.OpGT:
		return compareInterface(val, l.value) > 0
	case types.OpGTE:
		return compareInterface(val, l.value) >= 0
	case types.OpLT:
		return compareInterface(val, l.value) < 0
	case types.OpLTE:
		return compareInterface(val, l.value) <= 0
	case types.OpIN:
		for _, v := range l.values {
			if compareInterface(val, v) == 0 {
				return true
			}
		}
		return false
	case types.OpNI:
		for _, v := range l.values {
			if compareInterface(val, v) == 0 {
				return false
			}
		}
		return true
	case types.OpPrefix:
		s, ok := val.(string)
		if !ok {
			return false
		}
		prefix, _ := l.value.(string)
		return strings.HasPrefix(s, prefix)
	}
	return false
}

// matchLeafParsed is like matchLeaf but operates on an already-parsed map.
func matchLeafParsed(doc map[string]interface{}, l *leafCondition) bool {
	val, err := l.path.ExtractFromMap(doc)
	if err != nil || val == nil {
		return false
	}
	switch l.op {
	case types.OpEQ:
		return compareInterface(val, l.value) == 0
	case types.OpGT:
		return compareInterface(val, l.value) > 0
	case types.OpGTE:
		return compareInterface(val, l.value) >= 0
	case types.OpLT:
		return compareInterface(val, l.value) < 0
	case types.OpLTE:
		return compareInterface(val, l.value) <= 0
	case types.OpIN:
		for _, v := range l.values {
			if compareInterface(val, v) == 0 {
				return true
			}
		}
		return false
	case types.OpNI:
		for _, v := range l.values {
			if compareInterface(val, v) == 0 {
				return false
			}
		}
		return true
	case types.OpPrefix:
		s, ok := val.(string)
		if !ok {
			return false
		}
		prefix, _ := l.value.(string)
		return strings.HasPrefix(s, prefix)
	}
	return false
}

func compareInterface(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if aok && bok {
		if af < bf {
			return -1
		}
		if af > bf {
			return 1
		}
		return 0
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		if as < bs {
			return -1
		}
		if as > bs {
			return 1
		}
		return 0
	}
	ab, aok := a.(bool)
	bb, bok := b.(bool)
	if aok && bok {
		if !ab && bb {
			return -1
		}
		if ab && !bb {
			return 1
		}
		return 0
	}
	return 0
}

func toFloat(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	}
	return 0, false
}

func applyProjection(doc []byte, fields []string) (interface{}, error) {
	if len(fields) == 0 {
		var result interface{}
		if err := json.Unmarshal(doc, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	var full map[string]interface{}
	if err := json.Unmarshal(doc, &full); err != nil {
		return nil, err
	}
	return extractFields(full, fields), nil
}

// applyProjectionFromMap applies field projection from an already-parsed map.
func applyProjectionFromMap(doc map[string]interface{}, fields []string) (interface{}, error) {
	if len(fields) == 0 {
		return doc, nil
	}
	return extractFields(doc, fields), nil
}

// extractFields builds a new map containing only the specified fields.
func extractFields(doc map[string]interface{}, fields []string) interface{} {
	out := make(map[string]interface{})
	for _, f := range fields {
		p := jsondoc.ParsePath(f)
		v, _ := p.ExtractFromMap(doc)
		p.Set(out, v)
	}
	return out
}

func stripCondition(cond Condition, idxLeaf *leafCondition) Condition {
	if cond == nil || idxLeaf == nil {
		return cond
	}
	if idxLeaf.op != types.OpEQ && idxLeaf.op != types.OpIN {
		return cond
	}
	switch c := cond.(type) {
	case leafCondition:
		if leafCondEqual(&c, idxLeaf) {
			return nil
		}
		return c
	case *andCondition:
		var remaining []Condition
		for _, child := range c.children {
			r := stripCondition(child, idxLeaf)
			if r != nil {
				remaining = append(remaining, r)
			}
		}
		switch len(remaining) {
		case 0:
			return nil
		case 1:
			return remaining[0]
		default:
			return &andCondition{children: remaining}
		}
	default:
		return cond
	}
}

func leafCondEqual(a, b *leafCondition) bool {
	if a.op != b.op {
		return false
	}
	if !a.path.Equal(b.path) {
		return false
	}
	if a.op == types.OpIN || a.op == types.OpNI {
		if len(a.values) != len(b.values) {
			return false
		}
		for i := range a.values {
			if a.values[i] != b.values[i] {
				return false
			}
		}
		return true
	}
	return a.value == b.value
}
