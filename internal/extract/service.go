package extract

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

func (s *Service) Extract(ctx context.Context, span artifacts.EventSpan) ([]artifacts.CandidateArtifact, error) {
	s.logger.DebugContext(ctx, "extract requested", slog.String("span_id", span.SpanID))
	return nil, fmt.Errorf("extract not implemented")
}
