package obs

import (
	"context"
	"log/slog"
	"time"
)

type Decision struct {
	CandidateID  string    `json:"candidate_id"`
	ArtifactType string    `json:"artifact_type"`
	Scope        string    `json:"scope"`
	Status       string    `json:"status"`
	Reason       string    `json:"reason"`
	Timestamp    time.Time `json:"timestamp"`
}

type DecisionLog struct {
	logger    *slog.Logger
	decisions []Decision
}

func NewDecisionLog(logger *slog.Logger) *DecisionLog {
	return &DecisionLog{logger: logger}
}

func (d *DecisionLog) Record(ctx context.Context, candidateID, artifactType, scope, status, reason string) {
	d.logger.InfoContext(ctx, "decision recorded",
		slog.String("candidate_id", candidateID),
		slog.String("artifact_type", artifactType),
		slog.String("status", status),
		slog.String("reason", reason))

	d.decisions = append(d.decisions, Decision{
		CandidateID:  candidateID,
		ArtifactType: artifactType,
		Scope:        scope,
		Status:       status,
		Reason:       reason,
		Timestamp:    time.Now(),
	})
}

func (d *DecisionLog) Recent(limit int) []Decision {
	if limit <= 0 {
		limit = 10
	}
	if len(d.decisions) <= limit {
		return d.decisions
	}
	start := len(d.decisions) - limit
	return d.decisions[start:]
}
