package policy

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

// Invariant 4: "raw experience precedes synthesis"
// Candidates without provenance evidence must not be approved.

func TestCandidateWithoutEvidenceIsRejected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-no-evidence",
		ArtifactType:  "MEMORY",
		ProposedScope: "AGENT",
		Title:         "Something about the codebase",
		Body:          "The codebase is organized with internal packages.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance:    artifacts.Provenance{EvidenceSnippets: nil},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Status == artifacts.StatusApproved {
		t.Fatalf("invariant 4 violation: candidate without evidence must not be approved, got %s", result.Status)
	}
}

// Invariant 4: empty evidence strings must be rejected at validation.

func TestCandidateWithEmptyEvidenceStringIsRejected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-empty-evidence",
		ArtifactType:  "MEMORY",
		ProposedScope: "AGENT",
		Title:         "Something",
		Body:          "The codebase is organized.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{""},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Status == artifacts.StatusApproved {
		t.Fatalf("invariant 4 violation: candidate with empty evidence string must not be approved, got %s", result.Status)
	}
}

func TestCandidateWithWhitespaceEvidenceStringIsRejected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-whitespace",
		ArtifactType:  "MEMORY",
		ProposedScope: "AGENT",
		Title:         "Something",
		Body:          "The codebase is organized.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"  \t\n  "},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Status == artifacts.StatusApproved {
		t.Fatalf("invariant 4 violation: candidate with whitespace-only evidence must not be approved, got %s", result.Status)
	}
}

func TestCandidateWithMixedGoodAndEmptyEvidenceIsRejected(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-mixed",
		ArtifactType:  "MEMORY",
		ProposedScope: "AGENT",
		Title:         "Something",
		Body:          "The codebase is organized.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"good evidence", ""},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Status == artifacts.StatusApproved {
		t.Fatalf("invariant 4 violation: candidate with any empty evidence snippet must not be approved, got %s", result.Status)
	}
}

func TestCandidateWithEvidenceIsApproved(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-good",
		ArtifactType:  "MEMORY",
		ProposedScope: "AGENT",
		Title:         "User prefers WebSockets",
		Body:          "I always use WebSockets for real-time updates instead of polling.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"I always use WebSockets for real-time updates instead of polling."},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Status != artifacts.StatusApproved {
		t.Fatalf("expected approved, got %s (reason: %s)", result.Status, result.ApprovalReason)
	}
}

// Invariant 2: "attributable, not mysterious"
// Approved candidates must carry provenance metadata linking them to their origin.

func TestApprovedCandidateRetainsProvenance(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-provenance",
		ArtifactType:  "MEMORY",
		ProposedScope: "AGENT",
		Title:         "User prefers clear errors",
		Body:          "I like when error messages are clear.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			SpanIDs:          []string{"span_1"},
			EventIDs:         []string{"evt_1"},
			SessionIDs:       []string{"sess_1"},
			SourceIDs:        []string{"agent"},
			EvidenceSnippets: []string{"I like when error messages are clear."},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.Status != artifacts.StatusApproved {
		t.Fatalf("expected approved, got %s", result.Status)
	}

	prov := result.Provenance
	if len(prov.SpanIDs) == 0 || len(prov.EventIDs) == 0 || len(prov.SessionIDs) == 0 {
		t.Fatalf("invariant 2 violation: approved candidate must retain provenance metadata")
	}
	if len(prov.EvidenceSnippets) == 0 {
		t.Fatalf("invariant 2 violation: approved candidate must retain evidence snippets")
	}
}
