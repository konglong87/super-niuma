package computerbridge

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestStrictJSON(t *testing.T) {
	for _, data := range []string{
		`[]`, `null`, `{"data":{}}x`, `{"data":{}}{"data":{}}`,
		`{"data":{},"data":{}}`, `{"data":{"id":1,"id":2}}`,
		`{"data":{"Id":1}}`, `{"data":{"i\u0064":1,"id":2}}`,
		`{"data":{"id":"` + string([]byte{0xff}) + `"}}`,
		`{"data":` + strings.Repeat(`[`, maxJSONDepth+1) + `0` + strings.Repeat(`]`, maxJSONDepth+1) + `}`,
		`{"unknown":true}`,
	} {
		var out Response
		if err := decodeStrict([]byte(data), &out); !errors.Is(err, ErrInvalidResponse) {
			t.Errorf("strict JSON accepted malformed input: %v", err)
		}
	}
	for _, data := range []string{`{"data":{"session_id":"s"}}`, `{"data":{"arrays":[{},1,"a",true,null]}}`} {
		var out Response
		if err := decodeStrict([]byte(data), &out); err != nil {
			t.Errorf("valid JSON rejected: %v", err)
		}
		if !json.Valid(out.Data) {
			t.Fatal("invalid raw payload")
		}
	}
}
