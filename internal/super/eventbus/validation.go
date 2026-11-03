package eventbus

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"unicode/utf8"
)

var keyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

func ValidKey(value string) bool {
	return len(value) > 0 && len(value) <= MaxKeyBytes && keyPattern.MatchString(value)
}
func ValidTopic(value string) bool { return len(value) <= MaxTopicBytes && ValidKey(value) }
func ValidCode(code string) bool {
	switch code {
	case CodeConsumerFailed, CodeLeaseExpired, CodeUnavailable, CodeInvalidPayload:
		return true
	}
	return false
}
func NormalizeEvent(e Event) (Event, error) {
	if e.Sequence != 0 || !ValidKey(e.ID) || !ValidKey(e.Scope) || !ValidTopic(e.Topic) || !ValidKey(e.AggregateID) || e.Version != EventVersion || e.OccurredAtMS <= 0 || len(e.Payload) == 0 || len(e.Payload) > MaxPayloadBytes || !utf8.Valid(e.Payload) {
		return Event{}, ErrInvalidInput
	}
	decoder := json.NewDecoder(bytes.NewReader(e.Payload))
	decoder.UseNumber()
	value, err := readJSON(decoder, 0)
	if err != nil {
		return Event{}, ErrInvalidInput
	}
	if _, ok := value.(map[string]any); !ok {
		return Event{}, ErrInvalidInput
	}
	if _, err = decoder.Token(); err != io.EOF {
		return Event{}, ErrInvalidInput
	}
	payload, err := json.Marshal(value)
	if err != nil || len(payload) > MaxPayloadBytes {
		return Event{}, ErrInvalidInput
	}
	e.Payload = payload
	return e, nil
}

// Reject duplicate keys at every nesting level; decoder.Unmarshal alone would
// silently permit different producers to interpret the same identity differently.
func readJSON(d *json.Decoder, depth int) (any, error) {
	if depth > MaxJSONDepth {
		return nil, ErrInvalidInput
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, ErrInvalidInput
			}
			if _, exists := object[name]; exists {
				return nil, ErrInvalidInput
			}
			value, err := readJSON(d, depth+1)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, ErrInvalidInput
		}
		return object, nil
	case '[':
		array := []any{}
		for d.More() {
			value, err := readJSON(d, depth+1)
			if err != nil {
				return nil, err
			}
			array = append(array, value)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, ErrInvalidInput
		}
		return array, nil
	default:
		return nil, ErrInvalidInput
	}
}
func NormalizeSubscription(s Subscription) (Subscription, error) {
	if !ValidKey(s.Scope) || !ValidKey(s.Name) || len(s.Topics) == 0 || len(s.Topics) > MaxTopics || s.MaxAttempts < 1 || s.MaxAttempts > MaxAttempts || (s.Start != StartBeginning && s.Start != StartNow) {
		return Subscription{}, ErrInvalidInput
	}
	s.Topics = append([]string(nil), s.Topics...)
	sort.Strings(s.Topics)
	for i, topic := range s.Topics {
		if !ValidTopic(topic) || (i > 0 && topic == s.Topics[i-1]) {
			return Subscription{}, ErrInvalidInput
		}
	}
	return s, nil
}
func ValidateToken(t DeliveryToken) error {
	if !ValidKey(t.Scope) || !ValidKey(t.Name) || !ValidKey(t.Owner) || t.Sequence <= 0 || t.Epoch <= 0 || t.LeaseUntilMS <= 0 || len(t.Token) != 64 {
		return ErrInvalidInput
	}
	if _, err := hex.DecodeString(t.Token); err != nil {
		return ErrInvalidInput
	}
	return nil
}
