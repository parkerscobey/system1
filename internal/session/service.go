package session

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
)

type StartResult struct {
	AmbientContext []string                      `json:"ambient_context"`
	WakingMind     string                        `json:"waking_mind"`
	Artifacts      []artifacts.PersistedArtifact `json:"artifacts"`
}

type Service struct {
	logger   *slog.Logger
	cfg      config.Config
	backend  backend.Backend
	provider model.Provider
}

const (
	ambientSnapshotFilename = ".ambient_context.json"
	maxWakingMindTokens     = 500
)

func NewService(logger *slog.Logger, cfg config.Config, backend backend.Backend) *Service {
	return &Service{logger: logger, cfg: cfg, backend: backend}
}

// SetModelProvider sets the model provider for Waking Mind generation.
// When set, this provider will be used to generate orientation framing instead of snippet concatenation.
func (s *Service) SetModelProvider(provider model.Provider) {
	s.provider = provider
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

	wakingMind := s.generateWakingMind(ctx, ambientArtifacts)
	if err := persistAmbientSnapshot(s.cfg.StateDir, ambientArtifacts); err != nil {
		s.logger.ErrorContext(ctx, "failed to persist ambient snapshot", "error", err)
		return StartResult{}, err
	}

	s.logger.InfoContext(ctx, "session started",
		slog.Int("ambient_artifacts", len(ambientArtifacts)),
		slog.Int("waking_mind_length", len(wakingMind)))

	return StartResult{
		AmbientContext: ambientIDs,
		WakingMind:     wakingMind,
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

func (s *Service) generateWakingMind(ctx context.Context, artifacts []artifacts.PersistedArtifact) string {
	if len(artifacts) == 0 {
		return "No recent context available. Starting fresh."
	}

	// Try model-driven generation if provider is available
	if s.provider != nil {
		mind, err := s.generateWakingMindWithModel(ctx, artifacts)
		if err == nil && mind != "" {
			s.logger.Debug("model waking mind generation succeeded",
				slog.Int("artifacts", len(artifacts)),
				slog.Int("mind_length", len(mind)))
			return mind
		}
		if err != nil {
			s.logger.Warn("model waking mind generation failed, falling back to heuristics",
				slog.String("error", err.Error()))
		} else {
			s.logger.Warn("model waking mind generation returned empty, falling back to heuristics")
		}
	}

	return s.generateWakingMindHeuristic(artifacts)
}

func (s *Service) generateWakingMindWithModel(parentCtx context.Context, artifacts []artifacts.PersistedArtifact) (string, error) {
	timeout := s.cfg.ModelTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	prompt := buildWakingMindPrompt(artifacts)
	systemPrompt := wakingMindSystemPrompt

	response, err := s.provider.Complete(ctx, prompt, systemPrompt,
		model.WithMaxTokens(maxWakingMindTokens),
		model.WithTemperature(0.7))
	if err != nil {
		return "", fmt.Errorf("model completion: %w", err)
	}

	if response.Text == "" {
		return "", fmt.Errorf("model returned empty response")
	}

	return strings.TrimSpace(response.Text), nil
}

func (s *Service) generateWakingMindHeuristic(artifacts []artifacts.PersistedArtifact) string {
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

const wakingMindSystemPrompt = `You are a session orientation assistant. Your job is to produce a concise "waking mind" — a brief orientation framing that helps an agent understand what ambient context is available at session start.

Given a set of recent artifacts from the agent's history, produce a natural-language orientation that:
1. Summarizes the key themes and topics present in the context
2. Highlights the most important or recent artifacts
3. Notes any connections between artifacts
4. Gives the agent a sense of "what I know going in"

Be concise. Write in first person as if the agent is orienting itself.
Do not invent information not present in the artifacts.
Do not list artifacts as a numbered list — synthesize them into a coherent narrative.`

func buildWakingMindPrompt(artifacts []artifacts.PersistedArtifact) string {
	var sb strings.Builder

	sb.WriteString("You are starting a new session. Here are your ambient context artifacts from recent history:\n\n")

	for i, a := range artifacts {
		sb.WriteString(fmt.Sprintf("Artifact %d:\n", i+1))
		sb.WriteString(fmt.Sprintf("  Type: %s\n", a.ArtifactType))
		sb.WriteString(fmt.Sprintf("  Title: %s\n", a.Title))
		body := a.Body
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("  Body: %s\n", body))
		sb.WriteString(fmt.Sprintf("  Written: %s\n", a.WrittenAt.Format("2006-01-02 15:04")))
		if len(a.Provenance.EvidenceSnippets) > 0 {
			maxEvidence := len(a.Provenance.EvidenceSnippets)
			if maxEvidence > 3 {
				maxEvidence = 3
			}
			sb.WriteString(fmt.Sprintf("  Evidence: %s\n", strings.Join(a.Provenance.EvidenceSnippets[:maxEvidence], "; ")))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("Produce your waking mind orientation now.")

	return sb.String()
}

// LoadAmbientSnapshot returns the last persisted preloaded ambient context set.
func LoadAmbientSnapshot(stateDir string) ([]artifacts.PersistedArtifact, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, ambientSnapshotFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var ambient []artifacts.PersistedArtifact
	if err := json.Unmarshal(data, &ambient); err != nil {
		return nil, err
	}

	return ambient, nil
}

func persistAmbientSnapshot(stateDir string, ambient []artifacts.PersistedArtifact) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	data, err := json.MarshalIndent(ambient, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ambient snapshot: %w", err)
	}

	if err := os.WriteFile(filepath.Join(stateDir, ambientSnapshotFilename), data, 0o644); err != nil {
		return fmt.Errorf("write ambient snapshot: %w", err)
	}

	return nil
}
