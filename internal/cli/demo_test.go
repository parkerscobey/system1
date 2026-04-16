package cli

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend/file"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/extract"
	"github.com/XferOps/system1/internal/ingest"
	"github.com/XferOps/system1/internal/introspect"
	"github.com/XferOps/system1/internal/policy"
	"github.com/XferOps/system1/internal/session"
)

func TestDemoAcceptancePath(t *testing.T) {
	skipSQLiteFTSTest(t)

	tmpDir := t.TempDir()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sessionLog := filepath.Join(tmpDir, "session.jsonl")

	events := []string{
		`{"event_id":"evt_1","source_id":"test_agent","session_id":"test_session","timestamp":"2026-04-15T10:00:00Z","event_type":"message","actor_type":"user","content":"I prefer clear APIs. I don't like when error messages are unclear."}`,
		`{"event_id":"evt_2","source_id":"test_agent","session_id":"test_session","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"agent","content":"Remember that the user prefers clear APIs and good documentation."}`,
		`{"event_id":"evt_3","source_id":"test_agent","session_id":"test_session","timestamp":"2026-04-15T10:00:02Z","event_type":"message","actor_type":"user","content":"The project uses Go with cobra for the CLI and sqlite3 for the database."}`,
		`{"event_id":"evt_4","source_id":"test_agent","session_id":"test_session","timestamp":"2026-04-15T10:00:03Z","event_type":"message","actor_type":"agent","content":"The codebase architecture uses the Go cobra CLI framework and sqlite3 database."}`,
	}

	f, err := os.Create(sessionLog)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			t.Fatalf("close error: %v", cerr)
		}
	}()
	for _, e := range events {
		line := e + "\n"
		n, err := f.WriteString(line)
		if err != nil {
			t.Fatalf("write error: %v", err)
		}
		if n != len(line) {
			t.Fatalf("unexpected write length: got %d, want %d", n, len(line))
		}
	}

	cfg := config.Config{
		StateDir:        filepath.Join(tmpDir, ".system1"),
		ArtifactsDir:    filepath.Join(tmpDir, "artifacts"),
		SQLitePath:      filepath.Join(tmpDir, "system1.db"),
		LogLevel:        "debug",
		LogFormat:       "text",
		EnabledTypes:    []string{"MEMORY", "KNOWLEDGE"},
		SessionLogPath:  sessionLog,
		DefaultPassMode: "reflective",
	}

	ingestSvc := ingest.NewService(logger, cfg)
	ingestStats := &ingest.IngestStats{}
	stats, err := ingestSvc.Ingest(ctx)
	if err != nil && err != ingest.ErrEmptyLog {
		t.Fatalf("ingest: %v", err)
	}
	if stats != nil {
		ingestStats = stats
	}

	if ingestStats.EventsProcessed != 4 {
		t.Errorf("expected 4 events, got %d", ingestStats.EventsProcessed)
	}
	if ingestStats.SpansBuilt < 1 {
		t.Errorf("expected at least 1 span, got %d", ingestStats.SpansBuilt)
	}

	extractSvc := extract.NewService(logger, cfg)
	spans := ingestSvc.GetSpans()

	var candidates []artifacts.CandidateArtifact
	for _, span := range spans {
		extracted, err := extractSvc.Extract(ctx, span)
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		candidates = append(candidates, extracted...)
	}

	if len(candidates) == 0 {
		t.Fatal("expected candidates to be extracted, got none")
	}

	backend, err := file.NewStore(logger, cfg)
	if err != nil {
		t.Fatalf("create backend: %v", err)
	}
	defer backend.Close()

	policySvc := policy.NewService(logger, cfg, backend)

	var approved []artifacts.CandidateArtifact
	for _, candidate := range candidates {
		result, err := policySvc.Evaluate(ctx, candidate)
		if err != nil {
			t.Fatalf("policy evaluate: %v", err)
		}
		if result.Status == artifacts.StatusApproved {
			approved = append(approved, result)
		}
	}

	var persisted []artifacts.PersistedArtifact
	for _, a := range approved {
		p, err := policySvc.PersistApproved(ctx, a)
		if err != nil {
			t.Fatalf("persist: %v", err)
		}
		persisted = append(persisted, p)
	}

	if len(persisted) == 0 {
		t.Fatal("expected persisted artifacts, got none")
	}

	sessionSvc := session.NewService(logger, cfg, backend)
	sessionResult, err := sessionSvc.Start(ctx)
	if err != nil {
		t.Fatalf("session start: %v", err)
	}

	if len(sessionResult.AmbientContext) == 0 {
		t.Fatal("expected ambient context items, got none")
	}
	if sessionResult.WakingMind == "" {
		t.Fatal("expected waking mind content, got empty")
	}

	introspectionSvc := introspect.NewService(logger, cfg, backend)
	result, err := introspectionSvc.Query(ctx, "preferences", false)
	if err != nil {
		t.Fatalf("introspect: %v", err)
	}

	if result.Answer == "" {
		t.Fatal("expected non-empty answer from introspection, got empty")
	}

	t.Logf("Full acceptance path completed: %d artifacts persisted, %d in ambient context",
		len(persisted), len(sessionResult.AmbientContext))
}

func skipSQLiteFTSTest(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "testfts.db")

	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   dbPath,
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := file.NewStore(logger, cfg)
	if err != nil {
		if strings.Contains(err.Error(), "no such module") ||
			strings.Contains(err.Error(), "FTS5") ||
			strings.Contains(err.Error(), "fts5") {
			t.Skip("SQLite FTS5 support is not available")
		} else {
			t.Fatalf("Cannot create file store: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Failed to close store: %v", err)
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		t.Fatalf("Failed to remove temp dir: %v", err)
	}
}
