package filestore

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"unicode/utf8"
)

func validateJSON(data []byte) error {
	if !utf8.Valid(data) || !json.Valid(data) {
		return problem("CORRUPT_STATE", "$", "invalid UTF-8 JSON")
	}
	if err := rejectSurrogates(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := walkJSON(dec, "$", 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return problem("CORRUPT_STATE", "$", "expected exactly one value")
	}
	return nil
}
func walkJSON(dec *json.Decoder, path string, depth int) error {
	if depth > 64 {
		return problem("LIMIT", path, "JSON nesting exceeds 64")
	}
	t, err := dec.Token()
	if err != nil {
		return problem("CORRUPT_STATE", path, err.Error())
	}
	d, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k := kt.(string)
			if seen[k] {
				return problem("DUPLICATE_KEY", path+"/"+k, k)
			}
			seen[k] = true
			if err = walkJSON(dec, path+"/"+k, depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
		return err
	case '[':
		i := 0
		for dec.More() {
			if err := walkJSON(dec, path+"/"+strconv.Itoa(i), depth+1); err != nil {
				return err
			}
			i++
		}
		_, err = dec.Token()
		return err
	default:
		return problem("CORRUPT_STATE", path, "unexpected delimiter")
	}
}

func rejectSurrogates(b []byte) error {
	inside := false
	escaped := false
	for i := 0; i < len(b); i++ {
		if !inside {
			if b[i] == '"' {
				inside = true
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if b[i] == '"' {
			inside = false
			continue
		}
		if b[i] != '\\' {
			continue
		}
		if i+1 >= len(b) {
			return problem("CORRUPT_STATE", "$", "truncated escape")
		}
		if b[i+1] != 'u' {
			escaped = true
			continue
		}
		if i+5 >= len(b) {
			return problem("CORRUPT_STATE", "$", "truncated unicode escape")
		}
		n, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
		if err != nil {
			return problem("CORRUPT_STATE", "$", "invalid unicode escape")
		}
		i += 5
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
