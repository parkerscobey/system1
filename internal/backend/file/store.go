package file

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

type Store struct {
	logger *slog.Logger
	cfg    config.Config
}

func NewStore(logger *slog.Logger, cfg config.Config) *Store {
	return &Store{logger: logger, cfg: cfg}
}

func (s *Store) Save(ctx context.Context, artifact artifacts.PersistedArtifact) error {
	s.logger.DebugContext(ctx, "file backend save requested", slog.String("persisted_id", artifact.PersistedID))
	return fmt.Errorf("file backend save not implemented")
}
