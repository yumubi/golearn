package types

import "fmt"

// IndexMode defines index type flags.
type IndexMode uint8

const (
	IndexUnique IndexMode = 1 << iota // 0x01 unique constraint
	IndexStr                          // 0x04 string type index
	IndexI64                          // 0x08 64-bit integer index
	IndexF64                          // 0x10 float/double index
)

func (m IndexMode) String() string {
	switch m {
	case IndexUnique:
		return "unique"
	case IndexStr:
		return "str"
	case IndexI64:
		return "i64"
	case IndexF64:
		return "f64"
	default:
		return fmt.Sprintf("mode(%d)", m)
	}
}

// Operator enum.
type Op uint8

const (
	OpEQ     Op = iota // =
	OpGT               // >
	OpGTE              // >=
	OpLT               // <
	OpLTE              // <=
	OpIN               // in [...]
	OpNI               // not in [...]
	OpPrefix           // prefix match
	OpRE               // regex (not indexable)
)

func (o Op) String() string {
	switch o {
	case OpEQ:
		return "EQ"
	case OpGT:
		return "GT"
	case OpGTE:
		return "GTE"
	case OpLT:
		return "LT"
	case OpLTE:
		return "LTE"
	case OpIN:
		return "IN"
	case OpNI:
		return "NI"
	case OpPrefix:
		return "PREFIX"
	case OpRE:
		return "RE"
	default:
		return fmt.Sprintf("op(%d)", o)
	}
}

// JoinOp for combining conditions.
type JoinOp uint8

const (
	JoinAND JoinOp = iota
	JoinOR
)

// SortDir for ORDER BY direction.
type SortDir uint8

const (
	SortAsc  SortDir = 0
	SortDesc SortDir = 1
)

// QueryMode flags.
type QueryMode uint8

const (
	QueryNoIdx   QueryMode = 1 << iota // disable index usage
	QueryInverse                       // reverse scan direction
	QueryCountOnly                     // only return count
)

// Value represents a typed value extractable from JSON documents.
type Value struct {
	Str   *string
	I64   *int64
	F64   *float64
	Bool  *bool
	Arr   []interface{}
	IsNull bool
}

// IsNumeric returns true for I64 or F64 values.
func (v *Value) IsNumeric() bool {
	return v.I64 != nil || v.F64 != nil
}

// AsFloat returns the value as float64 (converts I64 if needed).
func (v *Value) AsFloat() (float64, bool) {
	if v.F64 != nil {
		return *v.F64, true
	}
	if v.I64 != nil {
		return float64(*v.I64), true
	}
	return 0, false
}

// AsInt64 returns the value as int64.
func (v *Value) AsInt64() (int64, bool) {
	if v.I64 != nil {
		return *v.I64, true
	}
	return 0, false
}

// AsString returns the value as string.
func (v *Value) AsString() (string, bool) {
	if v.Str != nil {
		return *v.Str, true
	}
	return "", false
}

// Comparison result constants.
const (
	CmpLT = -1
	CmpEQ = 0
	CmpGT = 1
)

// CompareValues compares two Values. Returns CmpLT, CmpEQ, or CmpGT.
// Type precedence: null < bool < i64/f64 < str
func CompareValues(a, b *Value) int {
	if a.IsNull && b.IsNull {
		return CmpEQ
	}
	if a.IsNull {
		return CmpLT
	}
	if b.IsNull {
		return CmpGT
	}

	// Numeric comparison when both are numeric
	af, aNum := a.AsFloat()
	bf, bNum := b.AsFloat()
	if aNum && bNum {
		if af < bf {
			return CmpLT
		}
		if af > bf {
			return CmpGT
		}
		return CmpEQ
	}

	// Bool comparison
	if a.Bool != nil && b.Bool != nil {
		if !*a.Bool && *b.Bool {
			return CmpLT
		}
		if *a.Bool && !*b.Bool {
			return CmpGT
		}
		return CmpEQ
	}

	// String comparison (fallback)
	as, aStr := a.AsString()
	bs, bStr := b.AsString()
	if aStr && bStr {
		if as < bs {
			return CmpLT
		}
		if as > bs {
			return CmpGT
		}
		return CmpEQ
	}

	// Cross-type: numeric < string
	if aNum {
		return CmpLT
	}
	return CmpGT
}
