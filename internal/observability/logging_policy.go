package observability

import "context"

type loggingPolicyKey struct{}

// WithoutLogging disables observability log emission for this context and its
// descendants, including explicit slog loggers. It does not mutate the parent,
// process-wide logger or other requests, and does not affect telemetry sinks.
// Use it for restricted runtimes whose provider errors may contain raw secrets.
func WithoutLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, loggingPolicyKey{}, true)
}

func loggingDisabled(ctx context.Context) bool {
	return ctx != nil && ctx.Value(loggingPolicyKey{}) == true
}
