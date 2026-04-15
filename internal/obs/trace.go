package obs

import (
	"context"
	"log/slog"
	"time"
)

type IntrospectionTrace struct {
	logger  *slog.Logger
	queries []IntrospectionQuery
}

type IntrospectionQuery struct {
	Query         string    `json:"query"`
	ResultCount   int       `json:"result_count"`
	DebugIncluded bool      `json:"debug_included"`
	Timestamp     time.Time `json:"timestamp"`
	ArtifactRefs  []string  `json:"artifact_refs,omitempty"`
	Evidence      []string  `json:"evidence,omitempty"`
}

func NewIntrospectionTrace(logger *slog.Logger) *IntrospectionTrace {
	return &IntrospectionTrace{logger: logger}
}

func (t *IntrospectionTrace) Record(ctx context.Context, query string, resultCount int, debug bool, refs, evidence []string) {
	t.logger.InfoContext(ctx, "introspection trace recorded",
		slog.String("query", query),
		slog.Int("result_count", resultCount))

	t.queries = append(t.queries, IntrospectionQuery{
		Query:         query,
		ResultCount:   resultCount,
		DebugIncluded: debug,
		Timestamp:     time.Now(),
		ArtifactRefs:  refs,
		Evidence:      evidence,
	})
}

func (t *IntrospectionTrace) Recent(limit int) []IntrospectionQuery {
	if limit <= 0 {
		limit = 10
	}
	if len(t.queries) <= limit {
		return t.queries
	}
	start := len(t.queries) - limit
	return t.queries[start:]
}
