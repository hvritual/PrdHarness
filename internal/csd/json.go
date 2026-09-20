package csd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

const MaxJSONBytes = 4 << 20
const maxDepth = 64

// Error is a machine-readable failure. Invalid input never yields a partial document.
type Error struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e *Error) Error() string                 { return e.Code + " at " + e.Path + ": " + e.Message }
func problem(code, path, message string) error { return &Error{code, path, message} }

// Value holds immutable canonical JSON. Numbers are signed 64-bit integers in v1.
// Decimals should be domain-defined strings, never silently rounded float64 values.
type Value struct{ raw string }

func ParseValue(data []byte) (Value, error) {
	v, err := strictJSON(data)
	if err != nil {
		return Value{}, err
	}
	b, err := json.Marshal(v)
	return Value{raw: string(b)}, err
}

func (v Value) MarshalJSON() ([]byte, error) {
	if v.raw == "" {
		return nil, problem("INVALID_VALUE", "$", "uninitialized Value")
	}
	return []byte(v.raw), nil
}

func (v *Value) UnmarshalJSON(data []byte) error {
	next, err := ParseValue(data)
	if err == nil {
		*v = next
	}
	return err
}

func strictJSON(data []byte) (any, error) {
	if len(data) > MaxJSONBytes {
		return nil, problem("LIMIT", "$", "JSON exceeds 4 MiB")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return nil, problem("INVALID_JSON", "$", "expected valid UTF-8 JSON")
	}
	if err := checkSurrogates(data); err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := readValue(d, 0)
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, problem("INVALID_JSON", "$", "expected exactly one value")
	}
	return v, nil
}

func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, problem("LIMIT", "$", "JSON nesting exceeds 64")
	}
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch x := t.(type) {
	case json.Delim:
		if x == '{' {
			m := make(map[string]any)
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				k, ok := key.(string)
				if !ok {
					return nil, problem("INVALID_JSON", "$", "non-string object key")
				}
				if _, ok := m[k]; ok {
					return nil, problem("DUPLICATE_KEY", "$", k)
				}
				v, err := readValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				m[k] = v
			}
			_, err := d.Token()
			return m, err
		}
		if x == '[' {
			a := make([]any, 0)
			for d.More() {
				v, err := readValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err := d.Token()
			return a, err
		}
		return nil, problem("INVALID_JSON", "$", "unexpected delimiter")
	case json.Number:
		n, err := strconv.ParseInt(string(x), 10, 64)
		if err != nil {
			return nil, problem("INVALID_NUMBER", "$", "v1 requires decimal int64 numbers (no fraction/exponent)")
		}
		return n, nil
	default:
		return t, nil
	}
}

// encoding/json replaces lone UTF-16 surrogates; a canonical source must reject them.
func checkSurrogates(b []byte) error {
	inside := false
	for i := 0; i < len(b); i++ {
		if b[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || b[i] != '\\' {
			continue
		}
		i++
		if b[i] != 'u' {
			continue
		}
		n, _ := strconv.ParseUint(string(b[i+1:i+5]), 16, 16)
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return problem("INVALID_UNICODE", "$", "unpaired low surrogate")
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return problem("INVALID_UNICODE", "$", "unpaired high surrogate")
			}
			low, err := strconv.ParseUint(string(b[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return problem("INVALID_UNICODE", "$", "invalid surrogate pair")
			}
			i += 6
		}
	}
	return nil
}

func object(v any, path string, allowed, required []string) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, problem("INVALID_TYPE", path, "expected object")
	}
	for _, key := range keys(m) {
		found := false
		for _, a := range allowed {
			if key == a {
				found = true
				break
			}
		}
		if !found {
			return nil, problem("UNKNOWN_FIELD", path+"/"+key, "field not allowed")
		}
	}
	for _, k := range required {
		if _, ok := m[k]; !ok {
			return nil, problem("MISSING_FIELD", path+"/"+k, "required")
		}
	}
	return m, nil
}

func decodeNodeShape(v any, path string) error {
	m, err := object(v, path, []string{"id", "type", "title", "fields"}, []string{"id", "type", "title", "fields"})
	if err != nil {
		return err
	}
	if _, ok := m["fields"].(map[string]any); !ok {
		return problem("INVALID_TYPE", path+"/fields", "expected object")
	}
	return nil
}

func DecodeNode(data []byte) (Node, error) {
	v, err := strictJSON(data)
	if err != nil {
		return Node{}, err
	}
	if err = decodeNodeShape(v, "$"); err != nil {
		return Node{}, err
	}
	var n Node
	err = json.Unmarshal(data, &n)
	if err != nil {
		return Node{}, problem("INVALID_TYPE", "$", err.Error())
	}
	if err = validateNode(n, "$"); err != nil {
		return Node{}, err
	}
	return n, nil
}

func DecodePatch(data []byte) (NodePatch, error) {
	v, err := strictJSON(data)
	if err != nil {
		return NodePatch{}, err
	}
	m, err := object(v, "$", []string{"title", "set_fields", "remove_fields"}, nil)
	if err != nil {
		return NodePatch{}, err
	}
	for k, v := range m {
		if v == nil {
			return NodePatch{}, problem("INVALID_TYPE", "$/"+k, "null patch member")
		}
	}
	var p NodePatch
	if err := json.Unmarshal(data, &p); err != nil {
		return p, problem("INVALID_TYPE", "$", err.Error())
	}
	return p, nil
}

func shapeSnapshot(v any) error {
	fields := []string{"schema", "id", "type", "title", "revision", "nodes", "edges", "presentation"}
	m, err := object(v, "$", fields, fields[:7])
	if err != nil {
		return err
	}
	nodes, ok := m["nodes"].([]any)
	if !ok {
		return problem("INVALID_TYPE", "$/nodes", "expected array")
	}
	for i, n := range nodes {
		if err := decodeNodeShape(n, fmt.Sprintf("$/nodes/%d", i)); err != nil {
			return err
		}
	}
	edges, ok := m["edges"].([]any)
	if !ok {
		return problem("INVALID_TYPE", "$/edges", "expected array")
	}
	for i, e := range edges {
		f := []string{"from", "type", "to"}
		if _, err := object(e, fmt.Sprintf("$/edges/%d", i), f, f); err != nil {
			return err
		}
	}
	if p, ok := m["presentation"]; ok {
		pm, err := object(p, "$/presentation", []string{"node_order", "collapsed"}, nil)
		if err != nil {
			return err
		}
		for k, a := range pm {
			if _, ok := a.([]any); !ok {
				return problem("INVALID_TYPE", "$/presentation/"+k, "expected array")
			}
		}
	}
	return nil
}
