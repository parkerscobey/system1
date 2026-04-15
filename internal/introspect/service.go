package introspect

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/XferOps/system1/internal/config"
)

type Result struct {
	Answer        string   `json:"answer"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
	DebugIncluded bool     `json:"debug_included"`
}

type Service struct {
	logger *slog.Logger
	cfg    config.Config
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	return &Service{logger: logger, cfg: cfg}
}

func (s *Service) Query(ctx context.Context, query string, debug bool) (Result, error) {
	s.logger.InfoContext(ctx, "introspection requested", slog.String("query", query), slog.Bool("debug", debug))
	return Result{}, fmt.Errorf("introspection not implemented")
}
