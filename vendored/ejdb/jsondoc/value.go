package jsondoc

import (
	"encoding/binary"
	json "github.com/goccy/go-json"
	"fmt"
	"math"
	"strconv"

	"ejdb/types"
)

// ExtractValue extracts a typed Value from a document at the given path.
func ExtractValue(doc []byte, p Path) (*types.Value, error) {
	raw, err := p.Extract(doc)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return &types.Value{IsNull: true}, nil
	}
	return toValue(raw), nil
}

// ToValue converts a raw interface{} value to a types.Value.
// Exported for use by the ejdb package for index key serialization.
func ToValue(raw interface{}) *types.Value {
	return toValue(raw)
}

func toValue(raw interface{}) *types.Value {
	v := &types.Value{}
	switch t := raw.(type) {
	case nil:
		v.IsNull = true
	case bool:
		v.Bool = &t
	case float64:
		if t == math.Trunc(t) && t <= math.MaxInt64 && t >= math.MinInt64 {
			i := int64(t)
			v.I64 = &i
		} else {
			v.F64 = &t
		}
	case int:
		i := int64(t)
		v.I64 = &i
	case int64:
		v.I64 = &t
	case int32:
		i := int64(t)
		v.I64 = &i
	case json.Number:
		s := t.String()
		if i, err := t.Int64(); err == nil {
			v.I64 = &i
		} else if f, err := t.Float64(); err == nil {
			v.F64 = &f
		} else {
			str := s
			v.Str = &str
		}
	case string:
		v.Str = &t
	case []interface{}:
		v.Arr = t
	case map[string]interface{}:
		b, err := json.Marshal(t)
		if err != nil {
			s := fmt.Sprintf("%v", t)
			v.Str = &s
			return v
		}
		s := string(b)
		v.Str = &s
	}
	return v
}

// SerializeKey serializes a value into an index key based on index mode.
func SerializeKey(val *types.Value, mode types.IndexMode) ([]byte, error) {
	if val.IsNull {
		return nil, fmt.Errorf("cannot serialize null value as index key")
	}

	switch {
	case mode&types.IndexI64 != 0:
		i, ok := val.AsInt64()
		if !ok {
			return nil, fmt.Errorf("expected int64 value for I64 index, got %v", val)
		}
		buf := make([]byte, 8)
		u := uint64(i) ^ (1 << 63)
		binary.BigEndian.PutUint64(buf, u)
		return buf, nil

	case mode&types.IndexF64 != 0:
		f, ok := val.AsFloat()
		if !ok {
			return nil, fmt.Errorf("expected float64 value for F64 index, got %v", val)
		}
		// Serialize float as string for lexicographic ordering
		return []byte(strconv.FormatFloat(f, 'g', -1, 64)), nil

	case mode&types.IndexStr != 0:
		s, ok := val.AsString()
		if !ok {
			// Convert non-string to string representation
			s = fmt.Sprintf("%v", val)
		}
		return []byte(s), nil

	default:
		// Default: serialize as string
		s, ok := val.AsString()
		if !ok {
			s = fmt.Sprintf("%v", val)
		}
		return []byte(s), nil
	}
}

// DeserializeKey reverses SerializeKey.
func DeserializeKey(data []byte, mode types.IndexMode) (*types.Value, error) {
	switch {
	case mode&types.IndexI64 != 0:
		if len(data) < 8 {
			return nil, fmt.Errorf("insufficient data for I64 key")
		}
		u := binary.BigEndian.Uint64(data)
		i := int64(u ^ (1 << 63))
		return &types.Value{I64: &i}, nil

	case mode&types.IndexF64 != 0:
		f, err := strconv.ParseFloat(string(data), 64)
		if err != nil {
			return nil, err
		}
		return &types.Value{F64: &f}, nil

	case mode&types.IndexStr != 0:
		s := string(data)
		return &types.Value{Str: &s}, nil

	default:
		s := string(data)
		return &types.Value{Str: &s}, nil
	}
}
