package computerbridge

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

const maxJSONDepth = 32

// Each string is pre-bounded by validRequest before marshaling. Escaped JSON
// can still exceed the wire budget and is checked before any network activity.
func encodeRequest(in Request) ([]byte, error) {
	data, err := json.Marshal(in)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if len(data) > MaxRequestBytes {
		return nil, ErrRequestTooLarge
	}
	return data, nil
}

func decodeStrict(data []byte, dst any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' || !utf8.Valid(data) {
		return ErrInvalidResponse
	}
	// encoding/json alone accepts duplicate keys and case-insensitive aliases.
	// Check the entire tree, including RawMessage payloads, before typed decoding.
	check := json.NewDecoder(bytes.NewReader(data))
	check.UseNumber()
	if !uniqueJSON(check, 0) {
		return ErrInvalidResponse
	}
	if _, err := check.Token(); err != io.EOF {
		return ErrInvalidResponse
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrInvalidResponse
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return ErrInvalidResponse
	}
	return nil
}

func uniqueJSON(dec *json.Decoder, depth int) bool {
	if depth > maxJSONDepth {
		return false
	}
	token, err := dec.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for dec.More() {
			token, err := dec.Token()
			key, ok := token.(string)
			// Wire field names are lowercase; reject Go's case-insensitive aliases.
			if err != nil || !ok || key != strings.ToLower(key) || seen[key] {
				return false
			}
			seen[key] = true
			if !uniqueJSON(dec, depth+1) {
				return false
			}
		}
		end, err := dec.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for dec.More() {
			if !uniqueJSON(dec, depth+1) {
				return false
			}
		}
		end, err := dec.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}
