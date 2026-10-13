package ejdb

import (
	"sort"

	"ejdb/types"
)

type sortContext struct {
	refs  []docRef
	docs  []byte
	sorts []SortSpec
}

type docRef struct {
	offset int
	size   int
	id     int64
}

type sortEntry struct {
	ref  docRef
	keys []interface{}
}

func sorterConsumer(exec *execCtx) docConsumer {
	sc := &sortContext{sorts: exec.query.sorts}
	exec.sortCtx = sc
	return sc.collect
}

func (sc *sortContext) collect(id int64, doc []byte) bool {
	if len(doc) == 0 {
		return true
	}
	ref := docRef{
		offset: len(sc.docs),
		size:   len(doc),
		id:     id,
	}
	sc.refs = append(sc.refs, ref)
	sc.docs = append(sc.docs, doc...)
	return true
}

func (sc *sortContext) finalize(exec *execCtx) {
	if len(sc.refs) == 0 {
		return
	}

	entries := make([]sortEntry, len(sc.refs))
	for i, ref := range sc.refs {
		doc := sc.docs[ref.offset : ref.offset+ref.size]
		entries[i] = sortEntry{
			ref:  ref,
			keys: extractSortKeys(doc, sc.sorts),
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return compareSortEntries(&entries[i], &entries[j], sc.sorts)
	})

	skip := exec.query.skip
	limit := exec.query.limit
	skipped := int64(0)
	delivered := int64(0)

	for _, entry := range entries {
		if limit > 0 && delivered >= limit {
			break
		}
		if skip > 0 && skipped < skip {
			skipped++
			continue
		}
		delivered++

		doc := sc.docs[entry.ref.offset : entry.ref.offset+entry.ref.size]
		output, err := applyProjection(doc, exec.query.proj)
		if err != nil {
			continue
		}
		exec.results = append(exec.results, output)
	}
}

func extractSortKeys(doc []byte, sorts []SortSpec) []interface{} {
	keys := make([]interface{}, len(sorts))
	for i, s := range sorts {
		v, _ := s.Path.Extract(doc)
		keys[i] = v
	}
	return keys
}

func compareSortEntries(a, b *sortEntry, sorts []SortSpec) bool {
	for i, s := range sorts {
		cmp := compareInterface(a.keys[i], b.keys[i])
		if cmp != 0 {
			if s.Dir == types.SortDesc {
				return cmp > 0
			}
			return cmp < 0
		}
	}
	return false
}

