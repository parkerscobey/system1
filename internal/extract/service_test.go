package extract

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

func TestExtractUsesResolvedContentForTitleAndBody(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"agent","content":"I prefer clear APIs and documented endpoints. I hate when error messages are unclear. The project uses Go with cobra and sqlite3."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:    "span_evt_1",
		SourceID:  "demo_agent",
		SessionID: "demo_session",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	candidate := candidates[0]
	if strings.Contains(candidate.Title, logPath) {
		t.Fatalf("expected title to use resolved content, got %q", candidate.Title)
	}
	if strings.Contains(candidate.Body, logPath) {
		t.Fatalf("expected body to use resolved content, got %q", candidate.Body)
	}
	if !strings.Contains(candidate.Title, "I prefer clear APIs") {
		t.Fatalf("expected title to include resolved content, got %q", candidate.Title)
	}
	if !strings.Contains(candidate.Body, "I prefer clear APIs and documented endpoints") {
		t.Fatalf("expected body to include resolved content, got %q", candidate.Body)
	}
}

func TestReadContentFromRefSupportsGenericNestedContent(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"id":"evt_generic","type":"message","content":[{"type":"text","text":"ADH means Agentic Development with Hizal."}]}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	content, err := readContentFromRef(logPath + ":0")
	if err != nil {
		t.Fatalf("readContentFromRef failed: %v", err)
	}
	if !strings.Contains(content, "Agentic Development with Hizal") {
		t.Fatalf("expected normalized nested content, got %q", content)
	}
}
