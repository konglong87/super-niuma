package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/super/workorder"
)

func jsonContentType(r *http.Request) bool {
	value, ok := singleHeader(r, "Content-Type")
	if !ok || len(r.Header.Values("Content-Encoding")) != 0 {
		return false
	}
	media, params, err := mime.ParseMediaType(value)
	if err != nil || media != "application/json" {
		return false
	}
	for key, value := range params {
		if key != "charset" || !strings.EqualFold(value, "utf-8") {
			return false
		}
	}
	return true
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, string) {
	if r.ContentLength > MaxBodyBytes {
		return nil, codeBody
	}
	if r.Body == nil {
		return nil, ""
	}
	reader := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, codeBody
		}
		return nil, codeInvalidRequest
	}
	return body, ""
}

// encoding/json alone permits duplicate keys, case-folded struct fields, null,
// and replacement of invalid UTF-8. Check the object envelope explicitly first.
func decodeObject(body []byte) (map[string]json.RawMessage, bool) {
	if !utf8.Valid(body) {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		key, ok := token.(string)
		if !ok {
			return nil, false
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		fields[key] = value
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return fields, true
}

func decodeInput(fields map[string]json.RawMessage) (workorder.CreateInput, bool) {
	var input workorder.CreateInput
	for field, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return input, false
		}
		var target any
		switch field {
		case "title":
			target = &input.Title
		case "objective":
			target = &input.Objective
		case "acceptance_criteria":
			target = &input.AcceptanceCriteria
		case "workspace":
			target = &input.Workspace
		default:
			return input, false
		}
		if err := json.Unmarshal(value, target); err != nil {
			return input, false
		}
	}
	return input, true
}
