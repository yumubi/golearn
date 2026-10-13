package jsondoc

import (
	json "github.com/goccy/go-json"
	"strings"
)

// Path represents a JSON path as a sequence of field names.
type Path []string

// ParsePath parses a dot-separated or slash-separated path string.
// Accepts both "/address/city" and "address.city" forms.
func ParsePath(s string) Path {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	// Try slash-separated
	if strings.Contains(s, "/") {
		parts := strings.Split(strings.Trim(s, "/"), "/")
		result := make(Path, 0, len(parts))
		for _, p := range parts {
			if p != "" {
				result = append(result, p)
			}
		}
		return result
	}
	// Dot-separated
	parts := strings.Split(s, ".")
	result := make(Path, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// String returns the path in "/field1/field2" format.
func (p Path) String() string {
	if len(p) == 0 {
		return "/"
	}
	return "/" + strings.Join(p, "/")
}

// Len returns the number of segments in the path.
func (p Path) Len() int {
	return len(p)
}

// Equal reports whether two paths have identical segments.
func (p Path) Equal(other Path) bool {
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if p[i] != other[i] {
			return false
		}
	}
	return true
}

// ExtractFromMap traverses an already-parsed JSON object map along the path
// and returns the value. Zero allocations. Returns nil if the path does not exist.
func (p Path) ExtractFromMap(m map[string]interface{}) (interface{}, error) {
	if len(p) == 0 {
		return nil, nil
	}
	current := interface{}(m)
	for _, segment := range p {
		obj, ok := current.(map[string]interface{})
		if !ok {
			return nil, nil
		}
		val, exists := obj[segment]
		if !exists {
			return nil, nil
		}
		current = val
	}
	return current, nil
}

// Extract traverses a JSON document along the path and returns the value.
// Returns nil if the path does not exist.
func (p Path) Extract(doc []byte) (interface{}, error) {
	if len(p) == 0 || len(doc) == 0 {
		return nil, nil
	}
	var current interface{}
	if err := json.Unmarshal(doc, &current); err != nil {
		return nil, err
	}
	for _, segment := range p {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil, nil // not an object, can't traverse further
		}
		val, exists := m[segment]
		if !exists {
			return nil, nil
		}
		current = val
	}
	return current, nil
}

// ExtractString extracts a string value at the path.
func (p Path) ExtractString(doc []byte) (string, bool) {
	v, err := p.Extract(doc)
	if err != nil || v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ExtractFloat extracts a numeric value at the path as float64.
func (p Path) ExtractFloat(doc []byte) (float64, bool) {
	v, err := p.Extract(doc)
	if err != nil || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// ExtractArray extracts values from an array field.
func (p Path) ExtractArray(doc []byte) ([]interface{}, error) {
	v, err := p.Extract(doc)
	if err != nil || v == nil {
		return nil, nil
	}
	arr, ok := v.([]interface{})
	if !ok {
		// Single value wrapped as array
		return []interface{}{v}, nil
	}
	return arr, nil
}

// Set traverses and sets a value in a JSON object map.
func (p Path) Set(root map[string]interface{}, value interface{}) {
	if len(p) == 0 {
		return
	}
	current := root
	for i := 0; i < len(p)-1; i++ {
		next, ok := current[p[i]]
		if !ok {
			nextMap := make(map[string]interface{})
			current[p[i]] = nextMap
			current = nextMap
			continue
		}
		nextMap, ok := next.(map[string]interface{})
		if !ok {
			nextMap = make(map[string]interface{})
			current[p[i]] = nextMap
		}
		current = nextMap
	}
	current[p[len(p)-1]] = value
}
