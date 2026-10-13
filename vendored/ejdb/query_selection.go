package ejdb

import (
	"ejdb/storage"
	"ejdb/types"
)

// selectIndexes is the index selection algorithm, ported from jbi_selection.c.
func selectIndexes(ctx *execCtx) error {
	// Default cursor direction
	ctx.midx.cursorInit = storage.CursorBeforeFirst
	ctx.midx.cursorStep = storage.CursorNext

	// Check if sorting is needed
	if len(ctx.query.sorts) > 0 {
		ctx.sorting = true
	}

	// Inverse mode: reverse direction
	if ctx.query.mode&types.QueryInverse != 0 {
		ctx.midx.cursorInit = storage.CursorAfterLast
		ctx.midx.cursorStep = storage.CursorPrev
	}

	conds := ctx.query.cond
	if conds == nil {
		ctx.scanner = fullScanner
		return nil
	}

	leaves := conds.flatten()
	if len(leaves) == 0 {
		ctx.scanner = fullScanner
		return nil
	}

	indexes := ctx.coll.Indexes()
	if len(indexes) == 0 || ctx.query.mode&types.QueryNoIdx != 0 {
		ctx.scanner = fullScanner
		return nil
	}

	// Collect index candidates
	var candidates []indexMatch
	for i := range leaves {
		l := &leaves[i]
		// Skip non-indexable operators
		switch l.op {
		case types.OpRE:
			continue
		}

		for _, idx := range indexes {
			if !idx.path.Equal(l.path) {
				continue
			}
			// Skip IN with large arrays (empiric heuristic from EJDB2)
			if l.op == types.OpIN && len(l.values) > maxINOpArraySize {
				continue
			}
			if l.op == types.OpIN && len(l.values) > minINOpArraySize {
				if ctx.coll.DocCount() < int64(len(l.values)*maxINOpArrayRatio) {
					continue
				}
			}

			match := computeScanStrategy(idx, l, ctx)
			candidates = append(candidates, match)
		}
	}

	if len(candidates) == 0 {
		// Try fallback: index for ORDER BY
		if ctx.sorting && len(ctx.query.sorts) > 0 {
			orderByPath := ctx.query.sorts[0].Path
			for _, idx := range indexes {
				if idx.path.Equal(orderByPath) {
					match := indexMatch{
						idx:        idx,
						cursorInit: storage.CursorBeforeFirst,
						cursorStep: storage.CursorNext,
					}
					if ctx.query.sorts[0].Dir == types.SortDesc {
						match.cursorInit = storage.CursorAfterLast
						match.cursorStep = storage.CursorPrev
					}
					match.orderBySupport = true
					ctx.midx = match
					ctx.sorting = false // index satisfies ORDER BY
					setScanner(ctx)
					return nil
				}
			}
		}
		ctx.scanner = fullScanner
		return nil
	}

	// Sort candidates by cost
	sortCandidates(candidates)

	// Select best index
	ctx.midx = candidates[0]

	// Check if selected index supports ORDER BY
	if ctx.sorting && ctx.midx.orderBySupport {
		ctx.sorting = false
	}

	setScanner(ctx)
	return nil
}

// computeScanStrategy determines how an index will be scanned for a given condition.
// Ported from _jbi_compute_index_rules() in jbi_selection.c:132.
func computeScanStrategy(idx *Index, l *leafCondition, ctx *execCtx) indexMatch {
	m := indexMatch{
		idx:  idx,
		leaf: l,
	}

	switch l.op {
	case types.OpEQ:
		m.expr1 = l
		m.cursorInit = storage.CursorEQ
		m.cursorStep = storage.CursorNext

	case types.OpIN:
		m.expr1 = l
		m.cursorInit = storage.CursorEQ
		m.cursorStep = storage.CursorNext

	case types.OpGT, types.OpGTE:
		m.expr1 = l
		m.cursorInit = storage.CursorGE
		// For GT/GTE, we scan from the key upward (ASC) or downward for reverse ordering
		if ctx.midx.cursorStep == storage.CursorPrev {
			m.cursorStep = storage.CursorPrev
			m.cursorInit = storage.CursorAfterLast // adjust for reverse
		} else {
			m.cursorStep = storage.CursorNext
		}

	case types.OpLT, types.OpLTE:
		m.expr2 = l
		m.cursorInit = storage.CursorBeforeFirst
		m.cursorStep = storage.CursorNext

	case types.OpPrefix:
		if idx.mode&IndexStr != 0 {
			m.expr1 = l
			m.cursorInit = storage.CursorGE
			m.cursorStep = storage.CursorNext
		}
	}

	// Check ORDER BY compatibility
	if len(ctx.query.sorts) > 0 && idx.path.Equal(ctx.query.sorts[0].Path) {
		wantDesc := ctx.query.sorts[0].Dir == types.SortDesc
		isDesc := m.cursorStep == storage.CursorPrev
		if wantDesc == isDesc {
			m.orderBySupport = true
		}
	}

	return m
}

// sortCandidates sorts index candidates by cost.
// Ported from _jbi_idx_cmp() in jbi_selection.c:371.
func sortCandidates(candidates []indexMatch) {
	for i := 0; i < len(candidates); i++ {
		for j := i + 1; j < len(candidates); j++ {
			if compareCandidates(&candidates[i], &candidates[j]) > 0 {
				candidates[i], candidates[j] = candidates[j], candidates[i]
			}
		}
	}
}

func compareCandidates(a, b *indexMatch) int {
	// 1. Operator weight (higher = better)
	wa := operatorWeight(a)
	wb := operatorWeight(b)
	if wa != wb {
		return wb - wa
	}

	// 2. Has expr2 (range end) = better
	hasA := a.expr2 != nil
	hasB := b.expr2 != nil
	if hasA != hasB {
		if hasB {
			return 1
		}
		return -1
	}

	// 3. Smaller index = better
	if a.idx.Count() != b.idx.Count() {
		if a.idx.Count() < b.idx.Count() {
			return -1
		}
		return 1
	}

	// 4. Shorter path = better
	return a.idx.path.Len() - b.idx.path.Len()
}

// operatorWeight returns the weight for an operator. Higher = more selective.
// Ported from _jbi_idx_expr_op_weight() in jbi_selection.c:86.
func operatorWeight(m *indexMatch) int {
	if m.expr1 == nil {
		return 0
	}
	switch m.expr1.op {
	case types.OpEQ:
		return 10
	case types.OpIN:
		return 9
	}
	if m.orderBySupport {
		return 8
	}
	if m.expr1 == nil {
		return 0
	}
	switch m.expr1.op {
	case types.OpGT, types.OpGTE:
		return 7
	case types.OpPrefix:
		return 6
	case types.OpLT, types.OpLTE:
		return 5
	}
	return 0
}

// setScanner selects the scanner function based on the selected index.
func setScanner(ctx *execCtx) {
	if ctx.midx.idx == nil {
		ctx.scanner = fullScanner
		return
	}
	idx := ctx.midx.idx
	if idx.HasCompoundKeys() {
		ctx.scanner = dupScanner
	} else {
		ctx.scanner = uniqScanner
	}
}

// Empiric constants ported from ejdb2_internal.h:204-206.
const (
	maxINOpArraySize  = 500
	minINOpArraySize  = 10
	maxINOpArrayRatio = 200
)
