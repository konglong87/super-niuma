package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var errJSONShape = errors.New("invalid or excessive JSON structure")

type jsonScanner struct {
	ctx     context.Context
	decoder *json.Decoder
	tokens  int
}

// Inspect tokens instead of unmarshalling into a map: encoding/json otherwise
// silently overwrites duplicate keys, including escaped-equivalent keys.
func checkJSONKeys(ctx context.Context, data []byte, required []string) (bool, error) {
	if !utf8.Valid(data) {
		return false, errJSONShape
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	s := jsonScanner{ctx: ctx, decoder: d}
	first, err := s.token()
	if err != nil || first != json.Delim('{') {
		return false, jsonError(err)
	}
	keys, err := s.object(1)
	if err != nil {
		return false, err
	}
	if _, err := s.token(); err != io.EOF {
		return false, jsonError(err)
	}
	for _, key := range required {
		if !keys[key] {
			return false, nil
		}
	}
	return true, nil
}
func jsonError(err error) error {
	if err != nil {
		return err
	}
	return errJSONShape
}
func (s *jsonScanner) token() (json.Token, error) {
	if err := s.ctx.Err(); err != nil {
		return nil, err
	}
	s.tokens++
	if s.tokens > MaxJSONTokens {
		return nil, errJSONShape
	}
	return s.decoder.Token()
}
func (s *jsonScanner) object(depth int) (map[string]bool, error) {
	if depth > MaxJSONDepth {
		return nil, errJSONShape
	}
	seen := make(map[string]bool)
	for s.decoder.More() {
		token, err := s.token()
		if err != nil {
			return nil, err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return nil, errJSONShape
		}
		seen[key] = true
		if err := s.value(depth); err != nil {
			return nil, err
		}
	}
	last, err := s.token()
	if err != nil || last != json.Delim('}') {
		return nil, jsonError(err)
	}
	return seen, nil
}
func (s *jsonScanner) value(depth int) error {
	token, err := s.token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		_, err := s.object(depth + 1)
		return err
	case json.Delim('['):
		if depth+1 > MaxJSONDepth {
			return errJSONShape
		}
		for s.decoder.More() {
			if err := s.value(depth + 1); err != nil {
				return err
			}
		}
		last, err := s.token()
		if err != nil || last != json.Delim(']') {
			return jsonError(err)
		}
		return nil
	case json.Delim('}'), json.Delim(']'):
		return errJSONShape
	default:
		return nil
	}
}
