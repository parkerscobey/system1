package session

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend/file"
	"github.com/XferOps/system1/internal/config"
)

type StartResult struct {
	AmbientContext []string       `json:"ambient_context"`
	WakingMind     string         `json:"waking_mind"`
	Artifacts      []artifacts.PersistedArtifact `json:"artifacts"`
}

type Service struct {
	logger  *slog.Logger
	cfg     config.Config
	backend *file.Store
}

func NewService(logger *slog.Logger, cfg config.Config, backend *file.Store) *Service {
	return &Service{logger: logger, cfg: cfg, backend: backend}
}

func (s *Service) Start(ctx context.Context) (StartResult, error) {
	s.logger.InfoContext(ctx, "session start requested")

	ambientArtifacts, err := s.assembleAmbientContext(ctx)
	if err != nil {
		s.logger.ErrorContext(ctx, "failed to assemble ambient context", "error", err)
		return StartResult{}, err
	}

	ambientIDs := make([]string, 0, len(ambientArtifacts))
	for _, a := range ambientArtifacts {
		ambientIDs = append(ambientIDs, a.PersistedID)
	}

	wakingMind := s.generateWakingMind(ambientArtifacts)

	s.logger.InfoContext(ctx, "session started",
		slog.Int("ambient_artifacts", len(ambientArtifacts)),
		slog.Int("waking_mind_length", len(wakingMind)))

	return StartResult{
		AmbientContext: ambientIDs,
		WakingMind:    wakingMind,
		Artifacts:      ambientArtifacts,
	}, nil
}

func (s *Service) End(ctx context.Context) error {
	s.logger.InfoContext(ctx, "session end requested")

	s.logger.InfoContext(ctx, "session ended")
	return nil
}

func (s *Service) assembleAmbientContext(ctx context.Context) ([]artifacts.PersistedArtifact, error) {
	var allArtifacts []artifacts.PersistedArtifact

	for _, artifactType := range s.cfg.EnabledTypes {
		artifactsByType, err := s.backend.FindByType(ctx, artifactType)
		if err != nil {
			s.logger.WarnContext(ctx, "failed to fetch artifacts by type",
				slog.String("type", artifactType), "error", err)
			continue
		}
		allArtifacts = append(allArtifacts, artifactsByType...)
	}

	if len(allArtifacts) == 0 {
		s.logger.InfoContext(ctx, "no artifacts found for ambient context")
		return nil, nil
	}

	sort.Slice(allArtifacts, func(i, j int) bool {
		return allArtifacts[i].WrittenAt.After(allArtifacts[j].WrittenAt)
	})

	const maxAmbientArtifacts = 20
	var ambient []artifacts.PersistedArtifact

	if len(allArtifacts) <= maxAmbientArtifacts {
		ambient = allArtifacts
	} else {
		ambient = allArtifacts[:maxAmbientArtifacts]
	}

	s.logger.InfoContext(ctx, "assembled ambient context",
		slog.Int("total_artifacts", len(allArtifacts)),
		slog.Int("ambient_selected", len(ambient)))

	return ambient, nil
}

func (s *Service) generateWakingMind(artifacts []artifacts.PersistedArtifact) string {
	if len(artifacts) == 0 {
		return "No recent context available. Starting fresh."
	}

	var sb strings.Builder
	sb.WriteString("=== WAKING MIND ===\n\n")
	sb.WriteString("Recent context:\n\n")

	for i, a := range artifacts {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, a.ArtifactType, a.Title))
		if len(a.Body) > 100 {
			sb.WriteString(fmt.Sprintf("   %s...\n", a.Body[:100]))
		} else {
			sb.WriteString(fmt.Sprintf("   %s\n", a.Body))
		}
	}

	sb.WriteString("\n--- END ---\n")

	return sb.String()
}
