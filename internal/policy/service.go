package policy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
	"github.com/google/uuid"
)

var (
	ErrInvalidCandidate = fmt.Errorf("candidate failed structural validation")
	ErrTypeNotEnabled   = fmt.Errorf("artifact type not enabled")
	ErrScopeNotAllowed  = fmt.Errorf("scope not allowed in configuration")
	ErrLowConfidence    = fmt.Errorf("candidate confidence too low for approval")
	ErrDuplicate        = fmt.Errorf("candidate is duplicate")
	ErrAmbiguous        = fmt.Errorf("candidate too ambiguous for decision")
)

type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
	DecisionDefer   Decision = "defer"
)

type Service struct {
	logger       *slog.Logger
	cfg          config.Config
	backend      backend.Backend
	mu           sync.RWMutex
	deferred     map[string]artifacts.CandidateArtifact
	enabledTypes map[string]bool
}

func NewService(logger *slog.Logger, cfg config.Config, backend backend.Backend) *Service {
	enabledTypes := make(map[string]bool)
	for _, t := range cfg.EnabledTypes {
		enabledTypes[strings.ToUpper(t)] = true
	}

	return &Service{
		logger:       logger,
		cfg:          cfg,
		backend:      backend,
		deferred:     make(map[string]artifacts.CandidateArtifact),
		enabledTypes: enabledTypes,
	}
}

func (s *Service) Evaluate(ctx context.Context, candidate artifacts.CandidateArtifact) (artifacts.CandidateArtifact, error) {
	s.logger.DebugContext(ctx, "policy evaluate requested", slog.String("candidate_id", candidate.CandidateID))

	if err := s.validateStructure(ctx, candidate); err != nil {
		return s.reject(candidate, "structural validation failed: "+err.Error()), nil
	}

	if err := s.validatePolicy(ctx, candidate); err != nil {
		if err == ErrTypeNotEnabled {
			return s.reject(candidate, "type not in enabled registry"), nil
		}
		if err == ErrLowConfidence {
			return s.deferCandidate(candidate, "confidence too low to decide"), nil
		}
		return s.reject(candidate, "policy validation failed: "+err.Error()), nil
	}

	duplicate, existing, err := s.checkDedup(ctx, candidate)
	if err != nil {
		s.logger.ErrorContext(ctx, "dedup check failed", "error", err)
		return candidate, err
	}
	if duplicate {
		s.logger.InfoContext(ctx, "candidate rejected as duplicate",
			slog.String("candidate_id", candidate.CandidateID),
			slog.String("existing_id", existing.PersistedID))
		return s.reject(candidate, "duplicate of existing artifact: "+existing.PersistedID), nil
	}

	decision, reason := s.makeDecision(ctx, candidate)
	s.logger.InfoContext(ctx, "policy decision",
		slog.String("candidate_id", candidate.CandidateID),
		slog.String("decision", string(decision)),
		slog.String("reason", reason))

	switch decision {
	case DecisionApprove:
		return s.approve(candidate, reason), nil
	case DecisionDefer:
		return s.deferCandidate(candidate, reason), nil
	default:
		return s.reject(candidate, reason), nil
	}
}

func (s *Service) ResolveDeferred(ctx context.Context) ([]artifacts.CandidateArtifact, error) {
	s.mu.Lock()
	count := len(s.deferred)
	s.mu.Unlock()

	s.logger.InfoContext(ctx, "resolving deferred candidates", slog.Int("count", count))

	s.mu.RLock()
	deferred := s.deferred
	s.mu.RUnlock()

	var resolved []artifacts.CandidateArtifact

	for _, candidate := range deferred {
		existing, err := s.backend.GetByCandidate(ctx, candidate.CandidateID)
		if err == nil {
			s.logger.DebugContext(ctx, "candidate already persisted",
				slog.String("candidate_id", candidate.CandidateID),
				slog.String("persisted_id", existing.PersistedID))
			continue
		}

		if !errors.Is(err, backend.ErrNotFound) {
			s.logger.ErrorContext(ctx, "failed to check candidate persistence",
				slog.String("candidate_id", candidate.CandidateID),
				"error", err)
			resolved = append(resolved, s.reject(candidate, "persistence check failed: "+err.Error()))
			continue
		}

		duplicate, existing, err := s.checkDedup(ctx, candidate)
		if err != nil {
			s.logger.ErrorContext(ctx, "dedup check failed during resolve",
				slog.String("candidate_id", candidate.CandidateID),
				"error", err)
			resolved = append(resolved, s.reject(candidate, "dedup check failed during resolve"))
			continue
		}

		if duplicate {
			resolved = append(resolved, s.reject(candidate, "resolved as duplicate: "+existing.PersistedID))
			continue
		}

		if candidate.Confidence == artifacts.ConfidenceLow {
			resolved = append(resolved, s.reject(candidate, "low confidence at session end"))
			continue
		}

		decision, reason := s.makeDecision(ctx, candidate)
		if decision == DecisionApprove {
			resolved = append(resolved, s.approve(candidate, "resolved with updated confidence"))
		} else {
			resolved = append(resolved, s.reject(candidate, "failed session-end resolve: "+reason))
		}
	}

	s.mu.Lock()
	s.deferred = make(map[string]artifacts.CandidateArtifact)
	s.mu.Unlock()

	s.logger.InfoContext(ctx, "deferred resolution complete", slog.Int("resolved", len(resolved)))

	return resolved, nil
}

func (s *Service) PersistApproved(ctx context.Context, candidate artifacts.CandidateArtifact) (artifacts.PersistedArtifact, error) {
	if candidate.Status != artifacts.StatusApproved {
		s.logger.ErrorContext(ctx, "cannot persist candidate that is not approved",
			slog.String("candidate_id", candidate.CandidateID),
			slog.String("status", string(candidate.Status)))
		return artifacts.PersistedArtifact{}, fmt.Errorf("candidate status %s not approved for persistence", candidate.Status)
	}

	persisted := artifacts.PersistedArtifact{
		PersistedID:  uuid.New().String(),
		ArtifactType: candidate.ArtifactType,
		Scope:        candidate.ProposedScope,
		Title:        candidate.Title,
		Body:         candidate.Body,
		Confidence:   candidate.Confidence,
		Provenance:   candidate.Provenance,
		CandidateID:  candidate.CandidateID,
		BackendType:  "file",
		BackendRef:   "",
		WrittenAt:    time.Now().UTC(),
		WriteStatus:  "created",
	}

	if err := s.backend.Save(ctx, persisted); err != nil {
		s.logger.ErrorContext(ctx, "failed to persist artifact",
			slog.String("candidate_id", candidate.CandidateID),
			"error", err)
		return artifacts.PersistedArtifact{}, err
	}

	s.logger.InfoContext(ctx, "artifact persisted",
		slog.String("candidate_id", candidate.CandidateID),
		slog.String("persisted_id", persisted.PersistedID))

	return persisted, nil
}

func (s *Service) GetDeferredCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.deferred)
}

func (s *Service) validateStructure(ctx context.Context, candidate artifacts.CandidateArtifact) error {
	if candidate.CandidateID == "" {
		return fmt.Errorf("candidate_id is required")
	}
	if candidate.ArtifactType == "" {
		return fmt.Errorf("artifact_type is required")
	}
	if candidate.ProposedScope == "" {
		return fmt.Errorf("proposed_scope is required")
	}
	if candidate.Title == "" {
		return fmt.Errorf("title is required")
	}
	if candidate.Body == "" {
		return fmt.Errorf("body is required")
	}
	if candidate.Confidence == "" {
		return fmt.Errorf("confidence is required")
	}
	if len(candidate.Provenance.EvidenceSnippets) == 0 {
		return fmt.Errorf("provenance evidence required")
	}

	return nil
}

func (s *Service) validatePolicy(ctx context.Context, candidate artifacts.CandidateArtifact) error {
	if !s.enabledTypes[strings.ToUpper(candidate.ArtifactType)] {
		return ErrTypeNotEnabled
	}

	validScopes := map[string]bool{
		"PROJECT": true,
		"AGENT":   true,
		"ORG":     true,
	}
	if !validScopes[strings.ToUpper(candidate.ProposedScope)] {
		return ErrScopeNotAllowed
	}

	return nil
}

func (s *Service) checkDedup(ctx context.Context, candidate artifacts.CandidateArtifact) (bool, artifacts.PersistedArtifact, error) {
	exactMatches, err := s.backend.FindByType(ctx, candidate.ArtifactType)
	if err != nil {
		return false, artifacts.PersistedArtifact{}, err
	}

	candidateTitleWords := normalizeForDedup(candidate.Title)
	candidateBodyWords := normalizeForDedup(candidate.Body)

	for _, existing := range exactMatches {
		existingTitleWords := normalizeForDedup(existing.Title)
		existingBodyWords := normalizeForDedup(existing.Body)

		if candidateTitleWords == existingTitleWords && candidateBodyWords == existingBodyWords {
			return true, existing, nil
		}

		overlap := computeOverlap(candidateTitleWords, existingTitleWords)
		if overlap > 0.8 {
			return true, existing, nil
		}

		bodyOverlap := computeOverlap(candidateBodyWords, existingBodyWords)
		if bodyOverlap > 0.8 {
			return true, existing, nil
		}
	}

	return false, artifacts.PersistedArtifact{}, nil
}

func (s *Service) makeDecision(ctx context.Context, candidate artifacts.CandidateArtifact) (Decision, string) {
	switch candidate.Confidence {
	case artifacts.ConfidenceHigh:
		return DecisionApprove, "high confidence"
	case artifacts.ConfidenceMid:
		if len(candidate.Provenance.EvidenceSnippets) >= 2 {
			return DecisionApprove, "medium confidence with sufficient evidence"
		}
		return DecisionDefer, "medium confidence but limited evidence"
	default:
		return DecisionDefer, "low confidence - waiting for more signal"
	}
}

func (s *Service) approve(candidate artifacts.CandidateArtifact, reason string) artifacts.CandidateArtifact {
	candidate.Status = artifacts.StatusApproved
	candidate.ApprovalReason = reason
	candidate.DeferReason = ""
	s.mu.Lock()
	delete(s.deferred, candidate.CandidateID)
	s.mu.Unlock()
	return candidate
}

func (s *Service) reject(candidate artifacts.CandidateArtifact, reason string) artifacts.CandidateArtifact {
	candidate.Status = artifacts.StatusRejected
	candidate.ApprovalReason = reason
	candidate.DeferReason = ""
	s.mu.Lock()
	delete(s.deferred, candidate.CandidateID)
	s.mu.Unlock()
	return candidate
}

func (s *Service) deferCandidate(candidate artifacts.CandidateArtifact, reason string) artifacts.CandidateArtifact {
	candidate.Status = artifacts.StatusDeferred
	candidate.ApprovalReason = ""
	candidate.DeferReason = reason
	s.mu.Lock()
	s.deferred[candidate.CandidateID] = candidate
	s.mu.Unlock()
	return candidate
}

func normalizeForDedup(text string) string {
	text = strings.ToLower(text)
	text = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == ' ' {
			return r
		}
		return -1
	}, text)

	words := strings.Fields(text)
	seen := make(map[string]bool)
	var uniq []string
	for _, w := range words {
		if !seen[w] && len(w) > 2 {
			seen[w] = true
			uniq = append(uniq, w)
		}
	}
	return strings.Join(uniq, " ")
}

func computeOverlap(a, b string) float64 {
	if a == "" || b == "" {
		return 0
	}

	wordsA := strings.Fields(a)
	wordsB := strings.Fields(b)

	if len(wordsA) == 0 || len(wordsB) == 0 {
		return 0
	}

	seenB := make(map[string]bool)
	for _, w := range wordsB {
		seenB[w] = true
	}

	var intersection int
	for _, w := range wordsA {
		if seenB[w] {
			intersection++
		}
	}

	minLen := len(wordsA)
	if len(wordsB) < minLen {
		minLen = len(wordsB)
	}

	if minLen == 0 {
		return 0
	}

	return float64(intersection) / float64(minLen)
}
