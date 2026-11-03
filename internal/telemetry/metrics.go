package telemetry

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type MetricsSink struct {
	mu       sync.Mutex
	counters map[metricsKey]*metricsValue
}

type metricsKey struct {
	Name     string
	Category string
	Status   string
}

type metricsValue struct {
	Count        int64
	DurationSum  int64
	InputTokens  int64
	OutputTokens int64
}

const (
	metricsPrefix       = "golang_cc"
	legacyMetricsPrefix = "golang_claude_code"
)

func NewMetricsSink() *MetricsSink {
	return &MetricsSink{counters: make(map[metricsKey]*metricsValue)}
}

func (s *MetricsSink) Emit(_ context.Context, event Event) error {
	if s == nil {
		return nil
	}
	key := metricsKey{Name: event.Name, Category: event.Category, Status: event.Status}
	// Keep aggregation cardinality intentionally small; high-cardinality details
	// stay in logs/MySQL telemetry where they are searchable but not metric labels.
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.counters[key]
	if value == nil {
		value = &metricsValue{}
		s.counters[key] = value
	}
	value.Count++
	value.DurationSum += event.DurationMS
	value.InputTokens += int64(event.InputTokens)
	value.OutputTokens += int64(event.OutputTokens)
	return nil
}

func (s *MetricsSink) Prometheus() []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	// Snapshot under lock, then format outside the critical section so /metrics
	// scraping cannot block hot-path telemetry emission for long.
	snapshot := make(map[metricsKey]metricsValue, len(s.counters))
	for key, value := range s.counters {
		if value != nil {
			snapshot[key] = *value
		}
	}
	s.mu.Unlock()

	keys := make([]metricsKey, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return metricsSortKey(keys[i]) < metricsSortKey(keys[j])
	})

	var out bytes.Buffer
	for _, prefix := range []string{metricsPrefix, legacyMetricsPrefix} {
		writeMetrics(&out, prefix, keys, snapshot)
	}
	return out.Bytes()
}

func writeMetrics(out *bytes.Buffer, prefix string, keys []metricsKey, snapshot map[metricsKey]metricsValue) {
	events := prefix + "_telemetry_events_total"
	duration := prefix + "_telemetry_duration_ms_total"
	inputTokens := prefix + "_telemetry_input_tokens_total"
	outputTokens := prefix + "_telemetry_output_tokens_total"
	writeMetricHelp(out, events, "Total normalized telemetry events.")
	writeMetricHelp(out, duration, "Total telemetry duration in milliseconds.")
	writeMetricHelp(out, inputTokens, "Total input tokens reported by telemetry events.")
	writeMetricHelp(out, outputTokens, "Total output tokens reported by telemetry events.")
	for _, key := range keys {
		value := snapshot[key]
		labels := metricsLabels(key)
		fmt.Fprintf(out, "%s{%s} %d\n", events, labels, value.Count)
		fmt.Fprintf(out, "%s{%s} %d\n", duration, labels, value.DurationSum)
		fmt.Fprintf(out, "%s{%s} %d\n", inputTokens, labels, value.InputTokens)
		fmt.Fprintf(out, "%s{%s} %d\n", outputTokens, labels, value.OutputTokens)
	}
}

func writeMetricHelp(out *bytes.Buffer, name, help string) {
	fmt.Fprintf(out, "# HELP %s %s\n", name, help)
	fmt.Fprintf(out, "# TYPE %s counter\n", name)
}

func metricsLabels(key metricsKey) string {
	return fmt.Sprintf(`event_name="%s",category="%s",status="%s"`,
		escapeMetricLabel(key.Name),
		escapeMetricLabel(key.Category),
		escapeMetricLabel(key.Status),
	)
}

func metricsSortKey(key metricsKey) string {
	return key.Name + "\x00" + key.Category + "\x00" + key.Status
}

func escapeMetricLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return strings.ReplaceAll(value, `"`, `\"`)
}
