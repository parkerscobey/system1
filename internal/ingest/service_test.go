package ingest

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

func TestParseEvent(t *testing.T) {
	logger := slog.Default()

	cfg := config.Config{
		StateDir:       t.TempDir(),
		ArtifactsDir:   filepath.Join(t.TempDir(), "artifacts"),
		SQLitePath:     filepath.Join(t.TempDir(), "test.db"),
		SessionLogPath: filepath.Join(t.TempDir(), "sessions.jsonl"),
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	line := `{"event_id":"evt_001","source_id":"agent_1","session_id":"sess_abc","timestamp":"2026-04-15T10:00:00Z","event_type":"message","actor_type":"user","content":"hello"}`

	event, err := svc.parseEvent(context.Background(), line)
	if err != nil {
		t.Fatalf("parseEvent failed: %v", err)
	}

	if event.EventID != "evt_001" {
		t.Errorf("expected event_id evt_001, got %s", event.EventID)
	}
	if event.Content != "hello" {
		t.Errorf("expected content hello, got %s", event.Content)
	}
}

func TestBuildSpans(t *testing.T) {
	logger := slog.Default()

	cfg := config.Config{
		StateDir:       t.TempDir(),
		SessionLogPath: filepath.Join(t.TempDir(), "sessions.jsonl"),
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	events := []artifacts.RawEvent{
		{
			EventID:   "evt_001",
			SourceID:  "agent_1",
			SessionID: "sess_abc",
			Content:   "hi",
		},
		{
			EventID:   "evt_002",
			SourceID:  "agent_1",
			SessionID: "sess_abc",
			Content:   "there",
		},
	}

	spans, err := svc.buildSpans(context.Background(), events)
	if err != nil {
		t.Fatalf("buildSpans failed: %v", err)
	}

	if len(spans) != 1 {
		t.Errorf("expected 1 span, got %d", len(spans))
	}

	if spans[0].SpanType != "segment" {
		t.Errorf("expected span type segment, got %s", spans[0].SpanType)
	}
}

func TestBuildSpansWithBoundary(t *testing.T) {
	logger := slog.Default()

	cfg := config.Config{
		StateDir:       t.TempDir(),
		SessionLogPath: filepath.Join(t.TempDir(), "sessions.jsonl"),
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	events := []artifacts.RawEvent{
		{
			EventID:   "evt_001",
			SourceID:  "agent_1",
			SessionID: "sess_abc",
			Content:   "hi",
		},
		{
			EventID:   "evt_002",
			SourceID:  "agent_1",
			SessionID: "sess_abc",
			Content:   "response",
			Metadata:  map[string]any{"turn_boundary": true},
		},
		{
			EventID:   "evt_003",
			SourceID:  "agent_1",
			SessionID: "sess_abc",
			Content:   "followup",
		},
	}

	spans, err := svc.buildSpans(context.Background(), events)
	if err != nil {
		t.Fatalf("buildSpans failed: %v", err)
	}

	if len(spans) != 2 {
		t.Errorf("expected 2 spans, got %d", len(spans))
	}
}

func TestIngestFullCycle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sessions.jsonl")

	events := []string{
		`{"event_id":"evt_1","source_id":"agent_1","session_id":"sess_x","timestamp":"2026-04-15T10:00:00Z","event_type":"message","actor_type":"user","content":"first"}`,
		`{"event_id":"evt_2","source_id":"agent_1","session_id":"sess_x","timestamp":"2026-04-15T10:00:05Z","event_type":"message","actor_type":"agent","content":"response"}`,
		`{"event_id":"evt_3","source_id":"agent_1","session_id":"sess_x","timestamp":"2026-04-15T10:00:10Z","event_type":"message","actor_type":"user","content":"second"}`,
	}

	writeSessionLog(t, logPath, events)

	cfg := config.Config{
		StateDir:       tmpDir,
		ArtifactsDir:   filepath.Join(tmpDir, "artifacts"),
		SQLitePath:     filepath.Join(tmpDir, "test.db"),
		SessionLogPath: logPath,
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	stats, err := svc.Ingest(context.Background())
	if err != nil {
		t.Fatalf("Ingest failed: %v", err)
	}

	if stats.EventsProcessed != 3 {
		t.Errorf("expected 3 events, got %d", stats.EventsProcessed)
	}
	if stats.SpansBuilt < 1 {
		t.Errorf("expected at least 1 span, got %d", stats.SpansBuilt)
	}
	if !stats.CursorSaved {
		t.Error("expected cursor to be saved")
	}

	status, err := svc.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}

	fileInfo, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}

	if status.LastOffset != fileInfo.Size() {
		t.Fatalf("expected cursor offset %d, got %d", fileInfo.Size(), status.LastOffset)
	}
}

func TestIngestResumeFromCursor(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sessions.jsonl")

	initialEvents := []string{
		`{"event_id":"evt_1","source_id":"agent_1","session_id":"sess_x","timestamp":"2026-04-15T10:00:00Z","event_type":"message","actor_type":"user","content":"first"}`,
		`{"event_id":"evt_2","source_id":"agent_1","session_id":"sess_x","timestamp":"2026-04-15T10:00:05Z","event_type":"message","actor_type":"agent","content":"response"}`,
	}

	writeSessionLog(t, logPath, initialEvents)

	cfg := config.Config{
		StateDir:       tmpDir,
		ArtifactsDir:   filepath.Join(tmpDir, "artifacts"),
		SQLitePath:     filepath.Join(tmpDir, "test.db"),
		SessionLogPath: logPath,
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	firstStats, err := svc.Ingest(context.Background())
	if err != nil {
		t.Fatalf("first Ingest failed: %v", err)
	}
	if firstStats.EventsProcessed != 2 {
		t.Fatalf("expected first ingest to process 2 events, got %d", firstStats.EventsProcessed)
	}

	secondStats, err := svc.Ingest(context.Background())
	if err != nil {
		t.Fatalf("second Ingest failed: %v", err)
	}
	if secondStats.EventsProcessed != 0 {
		t.Fatalf("expected second ingest to process 0 events, got %d", secondStats.EventsProcessed)
	}

	appendEvent(t, logPath, `{"event_id":"evt_3","source_id":"agent_1","session_id":"sess_x","timestamp":"2026-04-15T10:00:10Z","event_type":"message","actor_type":"user","content":"followup"}`)

	thirdStats, err := svc.Ingest(context.Background())
	if err != nil {
		t.Fatalf("third Ingest failed: %v", err)
	}
	if thirdStats.EventsProcessed != 1 {
		t.Fatalf("expected third ingest to process 1 new event, got %d", thirdStats.EventsProcessed)
	}
	if thirdStats.LastEventID != "evt_3" {
		t.Fatalf("expected last event evt_3, got %s", thirdStats.LastEventID)
	}
}

func TestIngestEmptyLog(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sessions.jsonl")

	if err := os.WriteFile(logPath, []byte(""), 0644); err != nil {
		t.Fatalf("create log file: %v", err)
	}

	cfg := config.Config{
		StateDir:       tmpDir,
		ArtifactsDir:   filepath.Join(tmpDir, "artifacts"),
		SQLitePath:     filepath.Join(tmpDir, "test.db"),
		SessionLogPath: logPath,
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	stats, err := svc.Ingest(context.Background())
	if err != ErrEmptyLog {
		t.Errorf("expected ErrEmptyLog, got %v", err)
	}
	if stats != nil {
		t.Error("expected nil stats on empty log")
	}
}

func TestIngestNoLog(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tmpDir := t.TempDir()

	cfg := config.Config{
		StateDir:       tmpDir,
		ArtifactsDir:   filepath.Join(tmpDir, "artifacts"),
		SQLitePath:     filepath.Join(tmpDir, "test.db"),
		SessionLogPath: filepath.Join(tmpDir, "sessions.jsonl"),
		EnabledTypes:   []string{"MEMORY", "KNOWLEDGE"},
	}

	svc := NewService(logger, cfg)

	stats, err := svc.Ingest(context.Background())
	if err != nil {
		t.Errorf("expected nil error for missing log, got %v", err)
	}
	if stats.EventsProcessed != 0 {
		t.Errorf("expected 0 events, got %d", stats.EventsProcessed)
	}
}

func writeSessionLog(t *testing.T, path string, events []string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatalf("create log file: %v", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer f.Close()

	for _, e := range events {
		if _, err := f.WriteString(e + "\n"); err != nil {
			t.Fatalf("write event: %v", err)
		}
	}
}

func appendEvent(t *testing.T, path string, event string) {
	t.Helper()

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open log for append: %v", err)
	}
	defer f.Close()

	if _, err := f.WriteString(event + "\n"); err != nil {
		t.Fatalf("append event: %v", err)
	}
}
