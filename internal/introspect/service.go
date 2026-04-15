package introspect

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	artifactslib "github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend/file"
	"github.com/XferOps/system1/internal/config"
)

type Result struct {
	Answer        string   `json:"answer"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
	DebugIncluded bool     `json:"debug_included"`
}

type Service struct {
	logger  *slog.Logger
	cfg     config.Config
	backend *file.Store
}

func NewService(logger *slog.Logger, cfg config.Config, backend *file.Store) *Service {
	return &Service{logger: logger, cfg: cfg, backend: backend}
}

func (s *Service) Query(ctx context.Context, query string, debug bool) (Result, error) {
	s.logger.InfoContext(ctx, "introspection requested", slog.String("query", query), slog.Bool("debug", debug))

	query = strings.TrimSpace(query)
	if query == "" {
		return Result{Answer: "No query provided."}, nil
	}

	isCalibration := isCalibrationQuery(query)

	ambientResults, err := s.queryAmbientContext(ctx, query)
	if err != nil {
		s.logger.WarnContext(ctx, "ambient context query failed", "error", err)
	}

	if len(ambientResults) > 0 && !isCalibration {
		return s.synthesizeResponse(query, ambientResults, debug, "ambient")
	}

	backendResults, err := s.queryBackend(ctx, query)
	if err != nil {
		s.logger.WarnContext(ctx, "backend query failed", "error", err)
	}

	var allResults []artifactslib.PersistedArtifact
	allResults = append(allResults, ambientResults...)
	allResults = append(allResults, backendResults...)

	if len(allResults) == 0 {
		return Result{
			Answer:        "No relevant context found. Starting fresh.",
			DebugIncluded: debug,
		}, nil
	}

	if isCalibration {
		return s.synthesizeCalibrationResponse(query, allResults, debug)
	}

	return s.synthesizeResponse(query, allResults, debug, "backend")
}

func isCalibrationQuery(query string) bool {
	lower := strings.ToLower(query)
	calibrationPhrases := []string{
		"what am i forgetting",
		"what am i missing",
		"what might i be missing",
		"what else should i know",
		"what don't i know",
		"gaps",
		"missing context",
	}
	for _, phrase := range calibrationPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func (s *Service) queryAmbientContext(ctx context.Context, query string) ([]artifactslib.PersistedArtifact, error) {
	var allArtifacts []artifactslib.PersistedArtifact

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
		return nil, nil
	}

	queryLower := strings.ToLower(query)
	var relevant []artifactslib.PersistedArtifact

	for _, a := range allArtifacts {
		titleLower := strings.ToLower(a.Title)
		bodyLower := strings.ToLower(a.Body)
		if strings.Contains(titleLower, queryLower) || strings.Contains(bodyLower, queryLower) {
			relevant = append(relevant, a)
		}
	}

	const maxAmbient = 10
	if len(relevant) > maxAmbient {
		relevant = relevant[:maxAmbient]
	}

	s.logger.DebugContext(ctx, "ambient context query completed",
		slog.Int("total_artifacts", len(allArtifacts)),
		slog.Int("relevant", len(relevant)))

	return relevant, nil
}

func (s *Service) queryBackend(ctx context.Context, query string) ([]artifactslib.PersistedArtifact, error) {
	results, err := s.backend.Search(ctx, query, 15)
	if err != nil {
		return nil, err
	}

	s.logger.DebugContext(ctx, "backend query completed", slog.Int("results", len(results)))

	return results, nil
}

func (s *Service) synthesizeResponse(query string, artifacts []artifactslib.PersistedArtifact, debug bool, source string) (Result, error) {
	var sb strings.Builder

	uniqueArtifacts := deduplicateArtifacts(artifacts)
	recent := getRecentArtifacts(uniqueArtifacts, 5)

	if len(recent) == 0 {
		return Result{Answer: "No relevant artifacts found."}, nil
	}

	sb.WriteString("Based on my recent context:\n\n")

	for i, a := range recent {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, a.ArtifactType, a.Title))
		if len(a.Body) > 150 {
			sb.WriteString(fmt.Sprintf("   %s...\n\n", a.Body[:150]))
		} else {
			sb.WriteString(fmt.Sprintf("   %s\n\n", a.Body))
		}
	}

	answer := sb.String()

	if source == "ambient" {
		answer += "\n(Found in preloaded context)"
	} else {
		answer += "\n(Retrieved from storage)"
	}

	refs := make([]string, len(uniqueArtifacts))
	ev := make([]string, 0)
	for i, a := range uniqueArtifacts {
		refs[i] = a.PersistedID
		ev = append(ev, a.Provenance.EvidenceSnippets...)
	}

	return Result{
		Answer:        strings.TrimSpace(answer),
		ArtifactRefs:  refs,
		Evidence:      ev,
		DebugIncluded: debug,
	}, nil
}

func (s *Service) synthesizeCalibrationResponse(query string, artifacts []artifactslib.PersistedArtifact, debug bool) (Result, error) {
	uniqueArtifacts := deduplicateArtifacts(artifacts)

	var sb strings.Builder
	sb.WriteString("Let me think about what might be missing...\n\n")

	topics := extractTopics(uniqueArtifacts)
	if len(topics) > 0 {
		sb.WriteString("Based on what I've found, here are areas that might be worth exploring:\n\n")
		for i, topic := range topics {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, topic))
		}
		sb.WriteString("\n")
	}

	var gaps []string
	if len(uniqueArtifacts) < 3 {
		gaps = append(gaps, "Limited session history - consider continuing the conversation to build more context.")
	}
	if !hasRecentArtifacts(uniqueArtifacts, 1*time.Hour) {
		gaps = append(gaps, "No very recent artifacts - current context may be sparse.")
	}

	if len(gaps) > 0 {
		sb.WriteString("Potential gaps identified:\n")
		for _, gap := range gaps {
			sb.WriteString(fmt.Sprintf("- %s\n", gap))
		}
	}

	answer := sb.String()

	refs := make([]string, len(uniqueArtifacts))
	for i, a := range uniqueArtifacts {
		refs[i] = a.PersistedID
	}

	return Result{
		Answer:        strings.TrimSpace(answer),
		ArtifactRefs:  refs,
		DebugIncluded: debug,
	}, nil
}

func deduplicateArtifacts(artifacts []artifactslib.PersistedArtifact) []artifactslib.PersistedArtifact {
	seen := make(map[string]bool)
	var result []artifactslib.PersistedArtifact
	for _, a := range artifacts {
		if !seen[a.PersistedID] {
			seen[a.PersistedID] = true
			result = append(result, a)
		}
	}
	return result
}

func getRecentArtifacts(artifacts []artifactslib.PersistedArtifact, max int) []artifactslib.PersistedArtifact {
	if len(artifacts) <= max {
		return artifacts
	}
	return artifacts[:max]
}

func hasRecentArtifacts(artifacts []artifactslib.PersistedArtifact, within time.Duration) bool {
	cutoff := time.Now().Add(-within)
	for _, a := range artifacts {
		if a.WrittenAt.After(cutoff) {
			return true
		}
	}
	return false
}

func extractTopics(artifacts []artifactslib.PersistedArtifact) []string {
	topicSet := make(map[string]bool)
	for _, a := range artifacts {
		title := strings.ToLower(a.Title)
		words := strings.Fields(title)
		for _, word := range words {
			if len(word) > 3 {
				topicSet[word] = true
			}
		}
		body := strings.ToLower(a.Body)
		words = strings.Fields(body)
		for _, word := range words {
			if len(word) > 4 {
				topicSet[word] = true
			}
		}
	}

	var topics []string
	for topic := range topicSet {
		topics = append(topics, topic)
		if len(topics) >= 5 {
			break
		}
	}
	return topics
}
