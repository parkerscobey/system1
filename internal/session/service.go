package session

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/XferOps/system1/internal/config"
)

type StartResult struct {
	AmbientContext []string `json:"ambient_context"`
	WakingMind     string   `json:"waking_mind"`
}

type Service struct {
	logger *slog.Logger
	cfg    config.Config
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	return &Service{logger: logger, cfg: cfg}
}

func (s *Service) Start(ctx context.Context) (StartResult, error) {
	s.logger.InfoContext(ctx, "session start requested")
	return StartResult{}, fmt.Errorf("session start not implemented")
}

func (s *Service) End(ctx context.Context) error {
	s.logger.InfoContext(ctx, "session end requested")
	return nil
}
