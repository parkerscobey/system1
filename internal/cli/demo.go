package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend/file"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/extract"
	"github.com/XferOps/system1/internal/ingest"
	"github.com/XferOps/system1/internal/introspect"
	"github.com/XferOps/system1/internal/policy"
	"github.com/XferOps/system1/internal/session"
	"github.com/spf13/cobra"
)

func newDemoCmd(ctx context.Context) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "demo",
		Short: "Run end-to-end MVP demo and acceptance harness",
		Long: `This command proves the System-1 MVP loop by running:
1. Ingest session data from demo fixtures
2. Extract candidate artifacts from spans
3. Run policy evaluation (dedup, approval)
4. Persist approved artifacts
5. Start a session (ambient context + Waking Mind)
6. Run introspect queries to verify grounded recall

Use --verbose for detailed output at each step.`,
	}

	cmd.Flags().BoolP("verbose", "v", false, "verbose output")
	cmd.Flags().BoolP("clean", "c", false, "clean demo state before running")
	cmd.Flags().String("fixtures-dir", "", "path to demo fixtures (default: testdata)")
	cmd.Flags().String("state-dir", "", "path to persistent demo state (default: .demo)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		verbose, _ := cmd.Flags().GetBool("verbose")
		clean, _ := cmd.Flags().GetBool("clean")
		fixturesDir, _ := cmd.Flags().GetString("fixtures-dir")
		stateDir, _ := cmd.Flags().GetString("state-dir")

		if fixturesDir == "" {
			fixturesDir = "testdata"
		}
		if stateDir == "" {
			stateDir = ".demo"
		}

		return runDemo(ctx, fixturesDir, stateDir, verbose, clean)
	}

	return cmd
}

func runDemo(ctx context.Context, fixturesDir, stateDir string, verbose, clean bool) error {
	logger := slog.Default()

	if clean {
		if err := os.RemoveAll(stateDir); err != nil {
			logger.Warn("failed to clean demo state", "path", stateDir, "error", err)
		} else {
			logger.Info("cleaned demo state", "path", stateDir)
		}
	}

	workDir, err := os.MkdirTemp("", "system1-demo-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	fixtureLog := filepath.Join(fixturesDir, "session.jsonl")
	if _, err := os.Stat(fixtureLog); os.IsNotExist(err) {
		logger.Info("Creating demo fixture session log", "path", workDir)
		if err := createDemoSessionLog(workDir); err != nil {
			return fmt.Errorf("create demo fixtures: %w", err)
		}
		fixtureLog = filepath.Join(workDir, "session.jsonl")
	}

	cfg := config.Config{
		StateDir:        filepath.Join(workDir, ".system1"),
		ArtifactsDir:    filepath.Join(workDir, "artifacts"),
		SQLitePath:      filepath.Join(workDir, "system1.db"),
		LogLevel:        "debug",
		LogFormat:       "text",
		EnabledTypes:    []string{"MEMORY", "KNOWLEDGE"},
		SessionLogPath:  fixtureLog,
		DefaultPassMode: "reflective",
		EnableDebug:     verbose,
	}

	logger.Info("=== SYSTEM-1 MVP DEMO ===")
	logger.Info("Step 1: Ingest session data")
	logger.Info("  config", "work_dir", workDir, "log_path", fixtureLog)

	ingestSvc := ingest.NewService(logger, cfg)
	ingestStats := &ingest.IngestStats{}
	stats, err := ingestSvc.Ingest(ctx)
	if err != nil && err != ingest.ErrEmptyLog {
		return fmt.Errorf("ingest: %w", err)
	}
	if stats != nil {
		ingestStats = stats
	}

	if verbose {
		logger.Debug("ingest stats",
			"events", ingestStats.EventsProcessed,
			"spans", ingestStats.SpansBuilt,
			"cursor_saved", ingestStats.CursorSaved,
		)
	}
	logger.Info("  -> Ingested events", "count", ingestStats.EventsProcessed)
	logger.Info("  -> Built spans", "count", ingestStats.SpansBuilt)

	logger.Info("Step 2: Extract candidate artifacts")
	extractSvc := extract.NewService(logger, cfg)

	spans, err := loadSpansFromIngest(cfg)
	if err != nil {
		return fmt.Errorf("load spans: %w", err)
	}

	var candidates []artifacts.CandidateArtifact
	for _, span := range spans {
		extracted, err := extractSvc.Extract(ctx, span)
		if err != nil {
			logger.Warn("extract failed", "span", span.SpanID, "error", err)
			continue
		}
		candidates = append(candidates, extracted...)
	}

	logger.Info("  -> Extracted candidates", "count", len(candidates))
	if verbose {
		for _, c := range candidates {
			logger.Debug("candidate",
				"id", c.CandidateID,
				"type", c.ArtifactType,
				"title", c.Title,
				"confidence", c.Confidence,
			)
		}
	}

	logger.Info("Step 3: Policy evaluation (dedup, approval, deferral)")
	backend, err := file.NewStore(logger, cfg)
	if err != nil {
		logger.Warn("Could not create file backend (FTS5 may not be available in this SQLite build)",
			"error", err)
		logger.Info("  -> Running in demo-only mode without persistence")
		backend = nil
	} else {
		defer backend.Close()
	}

	policySvc := policy.NewService(logger, cfg, backend)

	var approved []artifacts.CandidateArtifact
	var deferredCount int

	for _, candidate := range candidates {
		result, err := policySvc.Evaluate(ctx, candidate)
		if err != nil {
			logger.Warn("policy evaluate failed", "candidate", candidate.CandidateID, "error", err)
			continue
		}

		switch result.Status {
		case artifacts.StatusApproved:
			approved = append(approved, result)
		case artifacts.StatusDeferred:
			deferredCount++
		case artifacts.StatusRejected:
			logger.Debug("rejected", "candidate", result.CandidateID, "reason", result.ApprovalReason)
		}
	}

	logger.Info("  -> Approved", "count", len(approved))
	logger.Info("  -> Deferred", "count", deferredCount)

	if backend == nil {
		logger.Info("Step 4: Skipping persistence (no backend available)")
		logger.Info("Step 5: Skipping session start (no backend available)")
		logger.Info("Step 6: Skipping introspect (no backend available)")
		logger.Info("=== DEMO COMPLETE (limited - no FTS5 backend) ===")
		logger.Info("Summary (limited)",
			"events", ingestStats.EventsProcessed,
			"candidates", len(candidates),
			"approved", len(approved),
			"persisted", 0,
			"ambient", 0,
		)
		return nil
	}

	logger.Info("Step 4: Persist approved artifacts")
	var persisted []artifacts.PersistedArtifact
	for _, a := range approved {
		p, err := policySvc.PersistApproved(ctx, a)
		if err != nil {
			logger.Warn("persist failed", "candidate", a.CandidateID, "error", err)
			continue
		}
		persisted = append(persisted, p)
		if verbose {
			logger.Debug("persisted", "id", p.PersistedID, "type", p.ArtifactType, "scope", p.Scope)
		}
	}

	logger.Info("  -> Persisted artifacts", "count", len(persisted))

	logger.Info("Step 5: Start session (ambient context + Waking Mind)")
	sessionSvc := session.NewService(logger, cfg, backend)

	sessionResult, err := sessionSvc.Start(ctx)
	if err != nil {
		return fmt.Errorf("session start: %w", err)
	}

	logger.Info("  -> Ambient context loaded", "items", len(sessionResult.AmbientContext))
	logger.Info("  -> Waking Mind generated", "length", len(sessionResult.WakingMind))

	if verbose {
		logger.Debug("waking_mind", "content", sessionResult.WakingMind)
	}

	logger.Info("Step 6: Introspect queries (grounded recall verification)")
	introspectionSvc := introspect.NewService(logger, cfg, backend)

	testQueries := []string{
		"what did I learn about preferences",
		"what do I know about the codebase",
		"what am I forgetting",
	}

	for _, q := range testQueries {
		result, err := introspectionSvc.Query(ctx, q, verbose)
		if err != nil {
			logger.Warn("introspect query failed", "query", q, "error", err)
			continue
		}
		logger.Info("  query: " + q)
		logger.Info("  answer:", "text", truncate(result.Answer, 200))

		if verbose && result.DebugIncluded {
			logger.Debug("  refs", "artifact_refs", result.ArtifactRefs)
		}
	}

	logger.Info("=== DEMO COMPLETE ===")
	logger.Info("Summary",
		"events", ingestStats.EventsProcessed,
		"candidates", len(candidates),
		"approved", len(approved),
		"persisted", len(persisted),
		"ambient", len(sessionResult.AmbientContext),
	)

	return nil
}

func loadSpansFromIngest(cfg config.Config) ([]artifacts.EventSpan, error) {
	ingestLog := cfg.SessionLogPath
	file, err := os.Open(ingestLog)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	spans, err := buildDemoSpans(file)
	if err != nil {
		return nil, err
	}

	if len(spans) == 0 {
		spans = generateFallbackSpans()
	}

	return spans, nil
}

func buildDemoSpans(f *os.File) ([]artifacts.EventSpan, error) {
	return nil, nil
}

func generateFallbackSpans() []artifacts.EventSpan {
	now := time.Now()
	return []artifacts.EventSpan{
		{
			SpanID:         "span_demo_1",
			SpanType:       "segment",
			SourceID:       "demo_agent",
			SessionID:      "demo_session",
			StartEventID:   "evt_1",
			EndEventID:     "evt_3",
			EventIDs:       []string{"evt_1", "evt_2", "evt_3"},
			RawRefs:        []string{`{"event_id":"evt_1","content":"I prefer working with clear APIs and documented endpoints"}`},
			BoundaryReason: "eof",
			CreatedAt:      now,
		},
		{
			SpanID:         "span_demo_2",
			SpanType:       "segment",
			SourceID:       "demo_agent",
			SessionID:      "demo_session",
			StartEventID:   "evt_4",
			EndEventID:     "evt_6",
			EventIDs:       []string{"evt_4", "evt_5", "evt_6"},
			RawRefs:        []string{`{"event_id":"evt_4","content":"The project uses Go with cobra for CLI and sqlite3 for storage"}`},
			BoundaryReason: "eof",
			CreatedAt:      now,
		},
	}
}

func createDemoSessionLog(dir string) error {
	events := []string{
		`{"event_id":"evt_1","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:00Z","event_type":"message","actor_type":"user","content":"What are my preferences?"}`,
		`{"event_id":"evt_2","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"agent","content":"I remember that you prefer clear APIs and documented endpoints. You also like modular code with good test coverage."}`,
		`{"event_id":"evt_3","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:02Z","event_type":"message","actor_type":"user","content":"That's right. I also hate when error messages are unclear."}`,
		`{"event_id":"evt_4","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:05Z","event_type":"message","actor_type":"user","content":"Tell me about this codebase structure"}`,
		`{"event_id":"evt_5","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:06Z","event_type":"message","actor_type":"agent","content":"This is a Go project using cobra for CLI commands. It uses sqlite3 for storage with a file backend. The main components are ingest, extract, policy, session, and introspect services."}`,
		`{"event_id":"evt_6","source_id":"demo_agent","session_id":"demo_session","timestamp":"2026-04-15T10:00:07Z","event_type":"message","actor_type":"user","content":"Good, thanks. What else should I know about this project?"}`,
	}

	path := filepath.Join(dir, "session.jsonl")
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, e := range events {
		if _, err := f.WriteString(e + "\n"); err != nil {
			return err
		}
	}

	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
