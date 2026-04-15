package policy

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

type Service struct {
	logger *slog.Logger
	cfg    config.Config
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	return &Service{logger: logger, cfg: cfg}
}

func (s *Service) Evaluate(ctx context.Context, candidate artifacts.CandidateArtifact) (artifacts.CandidateArtifact, error) {
	s.logger.DebugContext(ctx, "policy evaluate requested", slog.String("candidate_id", candidate.CandidateID))
	return candidate, fmt.Errorf("policy evaluation not implemented")
}
