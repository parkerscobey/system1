package extract

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

// Invariant 4: "raw experience precedes synthesis"
// Candidate provenance must always carry evidence snippets derived from actual
// session content, not fabricated or empty.

func TestCandidateProvenanceCarriesEvidenceSnippets(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I prefer using WebSockets for real-time data. I like when messages arrive instantly, and don't like when there's latency."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:   "span_1",
		EventIDs: []string{"evt_1"},
		RawRefs:  []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}

	for _, c := range candidates {
		if len(c.Provenance.EvidenceSnippets) == 0 {
			t.Fatalf("candidate %q: invariant 4 violation: provenance must carry evidence snippets, got empty", c.CandidateID)
		}
		for _, s := range c.Provenance.EvidenceSnippets {
			if s == "" {
				t.Fatalf("candidate %q: invariant 4 violation: evidence snippet must not be empty string", c.CandidateID)
			}
		}
	}
}

// Invariant 4: provenance must always reference originating span and event IDs.

func TestCandidateProvenanceReferencesOrigin(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_42","source_id":"agent","session_id":"sess_7","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I always forget to check the database migrations before deploying. I prefer running the full test suite first."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}})
	span := artifacts.EventSpan{
		SpanID:    "span_42",
		SessionID: "sess_7",
		SourceID:  "agent",
		EventIDs:  []string{"evt_42"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}

	c := candidates[0]
	if len(c.Provenance.SpanIDs) == 0 {
		t.Fatalf("invariant 4 violation: provenance must reference originating span IDs")
	}
	if len(c.Provenance.EventIDs) == 0 {
		t.Fatalf("invariant 4 violation: provenance must reference originating event IDs")
	}
	if len(c.Provenance.SessionIDs) == 0 {
		t.Fatalf("invariant 4 violation: provenance must reference originating session IDs")
	}
}

// Invariant 8: "bounded intelligence, not open-ended agency"
// Extraction must abstain (return zero candidates) when span content has no
// detectable signal. The system must not hallucinate or guess.

func TestExtractionAbstainsOnLowSignalContent(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"ok"}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:   "span_1",
		EventIDs: []string{"evt_1"},
		RawRefs:  []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("invariant 8 violation: expected abstention on low-signal content, got %d candidates", len(candidates))
	}
}

func TestExtractionAbstainsOnNoRefs(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:   "span_empty",
		EventIDs: []string{"evt_1"},
		RawRefs:  nil,
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("invariant 8 violation: expected abstention on empty refs, got %d candidates", len(candidates))
	}
}

func TestExtractionAbstainsOnUnreadableRef(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:   "span_bad_ref",
		EventIDs: []string{"evt_1"},
		RawRefs:  []string{"/nonexistent/file.jsonl:0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("invariant 8 violation: expected abstention on unreadable ref, got %d candidates", len(candidates))
	}
}

// Invariant 4: candidate must have non-empty title and body (not just raw ref strings).

func TestCandidateTitleAndBodyAreResolvedContent(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I hate when the deploy script silently swallows database connection errors during CI. I prefer running the full test suite first."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}})
	span := artifacts.EventSpan{
		SpanID:   "span_1",
		EventIDs: []string{"evt_1"},
		RawRefs:  []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate")
	}

	c := candidates[0]
	if c.Title == "" {
		t.Fatalf("invariant 4 violation: candidate title must not be empty")
	}
	if c.Body == "" {
		t.Fatalf("invariant 4 violation: candidate body must not be empty")
	}
}

// Invariant 1: "supportive, not sovereign"
// Extraction must only propose candidates, never approve them autonomously.

func TestExtractionOnlyProposesNeverApproves(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I prefer clear APIs. I like when errors are well documented."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:   "span_1",
		EventIDs: []string{"evt_1"},
		RawRefs:  []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	for _, c := range candidates {
		if c.Status != artifacts.StatusProposed {
			t.Fatalf("invariant 1 violation: extraction must only propose, got status %s", c.Status)
		}
	}
}

// Invariant 8: "bounded intelligence"
// Extraction must not produce candidates for types not in the enabled registry.

func TestExtractionRejectsUnregisteredTypes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}})

	if !svc.isValidType("MEMORY") {
		t.Fatal("MEMORY should be valid")
	}
	if svc.isValidType("KNOWLEDGE") {
		t.Fatal("invariant 8 violation: KNOWLEDGE should not be valid when only MEMORY is enabled")
	}
	if svc.isValidType("DECISION") {
		t.Fatal("invariant 8 violation: DECISION should not be valid when only MEMORY is enabled")
	}
	if svc.isValidType("") {
		t.Fatal("invariant 8 violation: empty type should never be valid")
	}
}
