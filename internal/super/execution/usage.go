package execution

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/konglong87/super-niuma/internal/anthropic"
	"github.com/konglong87/super-niuma/internal/config"
)

// objectMembers rejects duplicate keys, scalars and trailing data. In particular
// ambiguous duplicate usage/counter fields must never refund a reservation.
func objectMembers(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalid
	}
	members := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		name, ok := token.(string)
		if !ok {
			return nil, ErrInvalid
		}
		if _, exists := members[name]; exists {
			return nil, ErrInvalid
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, ErrInvalid
		}
		members[name] = raw
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrInvalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	return members, nil
}
func usageCounter(raw json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return 0, ErrInvalid
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, ErrInvalid
	}
	n, err := number.Int64()
	if err != nil || n < 0 || n > maxRequestBytes {
		return 0, ErrInvalid
	}
	return int(n), nil
}
func optionalCounter(m map[string]json.RawMessage, key string) (int, error) {
	raw, ok := m[key]
	if !ok {
		return 0, nil
	}
	return usageCounter(raw)
}
func detailCounter(m map[string]json.RawMessage, key, member string) (int, error) {
	raw, ok := m[key]
	if !ok {
		return 0, nil
	}
	detail, err := objectMembers(raw)
	if err != nil {
		return 0, ErrInvalid
	}
	return optionalCounter(detail, member)
}

// Usage is decoded independently of output/finish reason. A valid response may
// have fully known cost while its text/tool outcome must still fail closed.
func strictUsage(protocol config.ProviderProtocol, body []byte) (anthropic.Usage, UsageState, error) {
	bad := func(state UsageState, code ErrorCode) (anthropic.Usage, UsageState, error) {
		return anthropic.Usage{}, state, codedFailure(code)
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		return bad(UsageNotObserved, CodeJSONInvalid)
	}
	envelope, err := objectMembers(body)
	if err != nil {
		return bad(UsageNotObserved, CodeJSONInvalid)
	}
	raw, exists := envelope["usage"]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return bad(UsageMissing, CodeUsageMissing)
	}
	members, err := objectMembers(raw)
	if err != nil {
		return bad(UsageInvalid, CodeUsageInvalid)
	}
	inKey, outKey := "input_tokens", "output_tokens"
	if protocol == config.ProviderProtocolOpenAIChatCompletions {
		inKey, outKey = "prompt_tokens", "completion_tokens"
	}
	in, inExists := members[inKey]
	out, outExists := members[outKey]
	if !inExists || !outExists {
		state := UsageMissing
		if inExists || outExists {
			state = UsagePartial
		}
		return bad(state, CodeUsageMissing)
	}
	input, err := usageCounter(in)
	if err != nil {
		return bad(UsageInvalid, CodeUsageInvalid)
	}
	output, err := usageCounter(out)
	if err != nil {
		return bad(UsageInvalid, CodeUsageInvalid)
	}
	usage := anthropic.Usage{InputTokens: input, OutputTokens: output, InputTokensIncludeCacheRead: protocol != config.ProviderProtocolAnthropicMessages}
	if protocol == config.ProviderProtocolAnthropicMessages {
		usage.CacheCreationInputTokens, err = optionalCounter(members, "cache_creation_input_tokens")
		if err != nil {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
		usage.CacheReadInputTokens, err = optionalCounter(members, "cache_read_input_tokens")
		if err != nil {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
		one, err := detailCounter(members, "cache_creation", "ephemeral_1h_input_tokens")
		if err != nil {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
		five, err := detailCounter(members, "cache_creation", "ephemeral_5m_input_tokens")
		if err != nil {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
		if _, present := members["cache_creation_input_tokens"]; present && one+five > usage.CacheCreationInputTokens {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
		usage.CacheCreation.Ephemeral1hInputTokens, usage.CacheCreation.Ephemeral5mInputTokens = one, five
	} else {
		inputDetails, outputDetails := "input_tokens_details", "output_tokens_details"
		if protocol == config.ProviderProtocolOpenAIChatCompletions {
			inputDetails, outputDetails = "prompt_tokens_details", "completion_tokens_details"
		}
		usage.CacheReadInputTokens, err = detailCounter(members, inputDetails, "cached_tokens")
		if err != nil || usage.CacheReadInputTokens > input {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
		usage.ReasoningOutputTokens, err = detailCounter(members, outputDetails, "reasoning_tokens")
		if err != nil || usage.ReasoningOutputTokens > output {
			return bad(UsageInvalid, CodeUsageInvalid)
		}
	}
	if _, err := observedUsage(usage); err != nil {
		return bad(UsageInvalid, CodeUsageInvalid)
	}
	return usage, UsageKnown, nil
}

func observedUsage(u anthropic.Usage) (Usage, error) {
	for _, n := range []int{u.InputTokens, u.OutputTokens, u.CacheReadInputTokens, u.CacheCreationInputTokens, u.CacheCreationEphemeral1hInputTokens, u.CacheCreationEphemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens, u.CacheCreation.Ephemeral5mInputTokens, u.ReasoningOutputTokens} {
		if n < 0 || n > maxRequestBytes {
			return Usage{}, codedFailure(CodeUsageInvalid)
		}
	}
	creation := max(u.CacheCreationInputTokens, u.CacheCreationEphemeral1hInputTokens+u.CacheCreationEphemeral5mInputTokens, u.CacheCreation.Ephemeral1hInputTokens+u.CacheCreation.Ephemeral5mInputTokens)
	total := u.InputTokens + creation
	if !u.InputTokensIncludeCacheRead {
		total += u.CacheReadInputTokens
	}
	if total <= 0 || u.ReasoningOutputTokens > u.OutputTokens {
		return Usage{}, codedFailure(CodeUsageInvalid)
	}
	return Usage{InputTokens: total, OutputTokens: u.OutputTokens}, nil
}
