package ejdb

import (
	"context"

	"ejdb/jsondoc"
	"ejdb/types"
)

// Query is a chainable query builder.
type Query struct {
	db       *DB
	collName string
	cond     Condition
	sorts    []SortSpec
	proj     []string
	skip     int64
	limit    int64
	mode     types.QueryMode
}

// Condition is a query filter expression.
type Condition interface {
	// isCondition seals the interface.
	isCondition()
	// flatten returns the flattened leaf conditions for index matching.
	flatten() []leafCondition
	// joinOp returns the join operation, used for tree traversal.
	joinOp() types.JoinOp
}

// leafCondition is a single filter: path OP value(s).
type leafCondition struct {
	path   jsondoc.Path
	op     types.Op
	value  interface{}
	values []interface{} // for IN/NI
}

func (l leafCondition) isCondition()             {}
func (l leafCondition) flatten() []leafCondition { return []leafCondition{l} }
func (l leafCondition) joinOp() types.JoinOp     { return types.JoinAND }

type andCondition struct {
	children []Condition
}

func (a *andCondition) isCondition() {}
func (a *andCondition) flatten() []leafCondition {
	var result []leafCondition
	for _, c := range a.children {
		result = append(result, c.flatten()...)
	}
	return result
}
func (a *andCondition) joinOp() types.JoinOp { return types.JoinAND }

type orCondition struct {
	children []Condition
}

func (o *orCondition) isCondition() {}
func (o *orCondition) flatten() []leafCondition {
	return nil // OR conditions cannot use indexes
}
func (o *orCondition) joinOp() types.JoinOp { return types.JoinOR }

type notCondition struct {
	child Condition
}

func (n *notCondition) isCondition() {}
func (n *notCondition) flatten() []leafCondition {
	return nil // negated conditions cannot use indexes
}
func (n *notCondition) joinOp() types.JoinOp { return types.JoinAND }

// SortSpec defines an ORDER BY specification.
type SortSpec struct {
	Path jsondoc.Path
	Dir  types.SortDir
}

// Condition constructors

// Eq creates an equality condition.
func Eq(path string, value interface{}) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpEQ, value: value}
}

// Gt creates a greater-than condition.
func Gt(path string, value interface{}) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpGT, value: value}
}

// Gte creates a greater-than-or-equal condition.
func Gte(path string, value interface{}) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpGTE, value: value}
}

// Lt creates a less-than condition.
func Lt(path string, value interface{}) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpLT, value: value}
}

// Lte creates a less-than-or-equal condition.
func Lte(path string, value interface{}) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpLTE, value: value}
}

// In creates an IN condition.
func In(path string, values ...interface{}) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpIN, values: values}
}

// Prefix creates a string prefix condition.
func Prefix(path string, value string) Condition {
	return leafCondition{path: jsondoc.ParsePath(path), op: types.OpPrefix, value: value}
}

// And combines conditions with logical AND.
func And(conds ...Condition) Condition {
	return &andCondition{children: conds}
}

// Or combines conditions with logical OR.
func Or(conds ...Condition) Condition {
	return &orCondition{children: conds}
}

// Not negates a condition.
func Not(cond Condition) Condition {
	return &notCondition{child: cond}
}

// Asc sorts ascending by path.
func Asc(path string) SortSpec {
	return SortSpec{Path: jsondoc.ParsePath(path), Dir: types.SortAsc}
}

// Desc sorts descending by path.
func Desc(path string) SortSpec {
	return SortSpec{Path: jsondoc.ParsePath(path), Dir: types.SortDesc}
}

// Where adds filter conditions.
func (q *Query) Where(cond Condition) *Query {
	if q.cond == nil {
		q.cond = cond
	} else {
		q.cond = And(q.cond, cond)
	}
	return q
}

// OrderBy adds sort specifications.
func (q *Query) OrderBy(sorts ...SortSpec) *Query {
	q.sorts = append(q.sorts, sorts...)
	return q
}

// Project limits returned fields.
func (q *Query) Project(fields ...string) *Query {
	q.proj = append(q.proj, fields...)
	return q
}

// Skip sets the number of documents to skip.
func (q *Query) Skip(n int64) *Query {
	q.skip = n
	return q
}

// Limit sets the maximum number of documents to return.
func (q *Query) Limit(n int64) *Query {
	q.limit = n
	return q
}

// NoIdx disables index usage for this query.
func (q *Query) NoIdx() *Query {
	q.mode |= types.QueryNoIdx
	return q
}

// Inverse reverses the scan direction.
func (q *Query) Inverse() *Query {
	q.mode |= types.QueryInverse
	return q
}

// All executes the query and returns all matching documents.
func (q *Query) All(ctx context.Context) ([]interface{}, error) {
	return executeQuery(q, ctx)
}

// One executes the query and returns the first matching document.
func (q *Query) One(ctx context.Context) (interface{}, error) {
	q.limit = 1
	results, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results[0], nil
}

// Count returns the number of matching documents.
func (q *Query) Count(ctx context.Context) (int64, error) {
	q.mode |= types.QueryCountOnly
	q.limit = 0
	q.skip = 0
	results, err := executeQuery(q, ctx)
	if err != nil {
		return 0, err
	}
	return int64(len(results)), nil
}
