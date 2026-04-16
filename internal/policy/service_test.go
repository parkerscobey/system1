package policy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

func TestEvaluateWithoutBackendSkipsDedup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-1",
		ArtifactType:  "MEMORY",
		ProposedScope: "PROJECT",
		Title:         "Clear APIs matter",
		Body:          "The user prefers clear APIs and understandable errors.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"The user prefers clear APIs."},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.Status != artifacts.StatusApproved {
		t.Fatalf("expected approved status, got %s", result.Status)
	}
}

func TestResolveDeferredWithoutBackendReturnsExplicitError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}, nil)

	_, err := svc.ResolveDeferred(context.Background())
	if !errors.Is(err, ErrNoBackend) {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}

func TestPersistApprovedWithoutBackendReturnsExplicitError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-2",
		ArtifactType:  "MEMORY",
		ProposedScope: "PROJECT",
		Title:         "Clear APIs matter",
		Body:          "The user prefers clear APIs and understandable errors.",
		Confidence:    artifacts.ConfidenceHigh,
		Status:        artifacts.StatusApproved,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"The user prefers clear APIs."},
		},
	}

	_, err := svc.PersistApproved(context.Background(), candidate)
	if !errors.Is(err, ErrNoBackend) {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}
