package operations

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/hvritual/PrdHarness/internal/csd"
)

func DecodeCommand(data []byte) (Command, error) {
	if len(data) > csd.MaxJSONBytes {
		return Command{}, problem("LIMIT", "$", "command exceeds 4 MiB")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return Command{}, problem("INVALID_JSON", "$", "expected valid UTF-8 JSON")
	}
	if err := rejectSurrogates(data); err != nil {
		return Command{}, err
	}
	if err := rejectDuplicateKeys(data); err != nil {
		return Command{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var c Command
	if err := dec.Decode(&c); err != nil {
		return Command{}, problem("INVALID_COMMAND", "$", err.Error())
	}
	if err := c.Validate(); err != nil {
		return Command{}, err
	}
	return c, nil
}

func CommandHash(c Command) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:sha256:%x", CommandVersion, sha256.Sum256(b)), nil
}

func rejectDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := scanValue(dec, "$", 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return problem("INVALID_JSON", "$", "expected exactly one value")
	}
	return nil
}

func scanValue(dec *json.Decoder, path string, depth int) error {
	if depth > 64 {
		return problem("LIMIT", path, "JSON nesting exceeds 64")
	}
	tok, err := dec.Token()
	if err != nil {
		return problem("INVALID_JSON", path, err.Error())
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return problem("INVALID_JSON", path, err.Error())
			}
			key, ok := kt.(string)
			if !ok {
				return problem("INVALID_JSON", path, "object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return problem("DUPLICATE_KEY", path+"/"+escapePointer(key), key)
			}
			seen[key] = struct{}{}
			if err := scanValue(dec, path+"/"+escapePointer(key), depth+1); err != nil {
				return err
			}
		}
		_, err = dec.Token()
	case '[':
		idx := 0
		for dec.More() {
			if err := scanValue(dec, path+"/"+strconv.Itoa(idx), depth+1); err != nil {
				return err
			}
			idx++
		}
		_, err = dec.Token()
	default:
		return problem("INVALID_JSON", path, "unexpected delimiter")
	}
	if err != nil {
		return problem("INVALID_JSON", path, err.Error())
	}
	return nil
}

func escapePointer(s string) string {
	var out bytes.Buffer
	for _, r := range s {
		switch r {
		case '~':
			out.WriteString("~0")
		case '/':
			out.WriteString("~1")
		default:
			out.WriteRune(r)
		}
	}
	return out.String()
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
			return problem("INVALID_JSON", "$", "truncated escape")
		}
		if b[i+1] != 'u' {
			escaped = true
			continue
		}
		if i+5 >= len(b) {
			return problem("INVALID_JSON", "$", "truncated unicode escape")
		}
		n, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
		if err != nil {
			return problem("INVALID_JSON", "$", "invalid unicode escape")
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
