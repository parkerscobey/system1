package introspect

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
)

func TestQueryUsesPreloadedAmbientBeforeBackend(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	older := persistedArtifact("artifact-old", "Alpha topic memory", "ambient body", time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC))
	newer := persistedArtifact("artifact-new", "Alpha topic fresh", "backend body", time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{older})

	svc := NewService(logger, cfg, nil)
	result, err := svc.Query(ctx, "alpha topic", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !strings.Contains(result.Answer, "Found in preloaded context") {
		t.Fatalf("expected ambient answer, got %q", result.Answer)
	}
	if !strings.Contains(result.Answer, older.Title) {
		t.Fatalf("expected answer to reference preloaded artifact %q, got %q", older.Title, result.Answer)
	}
	if strings.Contains(result.Answer, newer.Title) {
		t.Fatalf("did not expect answer to reference backend-only artifact %q, got %q", newer.Title, result.Answer)
	}
	if len(result.ArtifactRefs) != 0 || len(result.Evidence) != 0 {
		t.Fatalf("expected debug fields to be hidden when debug=false, got refs=%v evidence=%v", result.ArtifactRefs, result.Evidence)
	}
}

func TestQueryMatchesStemmedAmbientTerms(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY"},
	}

	artifact := persistedArtifact("artifact-prefer", "Preference memory", "I prefer clear APIs and documented endpoints.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	result, err := svc.Query(ctx, "what did I learn about preferences", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !strings.Contains(result.Answer, artifact.Title) {
		t.Fatalf("expected stemmed query to match ambient artifact, got %q", result.Answer)
	}
}

func TestCalibrationFallsBackToAmbientContext(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY"},
	}

	artifact := persistedArtifact("artifact-gap", "Codebase notes", "The codebase uses Go, cobra, and sqlite3.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	result, err := svc.Query(ctx, "what am I forgetting", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if strings.Contains(result.Answer, "Starting fresh") {
		t.Fatalf("expected calibration to use ambient context, got %q", result.Answer)
	}
	if !strings.Contains(result.Answer, "Potential gaps identified") {
		t.Fatalf("expected calibration response, got %q", result.Answer)
	}
}

func TestBuildFTSQueryDropsStopwordsAndAddsPrefixMatching(t *testing.T) {
	got := buildFTSQuery("what did I learn about preferences")
	if strings.Contains(got, "what*") || strings.Contains(got, "about*") {
		t.Fatalf("expected stopwords to be removed, got %q", got)
	}
	for _, expected := range []string{"learn*", "preferences*", "preference*", "prefer*"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected FTS query to include %q, got %q", expected, got)
		}
	}
}

func TestQueryDebugModeIncludesProvenance(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY"},
	}

	artifact := persistedArtifact("artifact-debug", "Debug topic", "debug body", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	result, err := svc.Query(ctx, "debug", true)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !result.DebugIncluded {
		t.Fatal("expected DebugIncluded to be true")
	}
	if len(result.ArtifactRefs) != 1 || result.ArtifactRefs[0] != artifact.PersistedID {
		t.Fatalf("expected artifact refs to include %q, got %v", artifact.PersistedID, result.ArtifactRefs)
	}
	if len(result.Evidence) != 1 || result.Evidence[0] != artifact.Provenance.EvidenceSnippets[0] {
		t.Fatalf("expected evidence to include provenance snippet, got %v", result.Evidence)
	}
}

func TestGetRecentArtifactsSortsByWrittenAt(t *testing.T) {
	artifactsIn := []artifacts.PersistedArtifact{
		persistedArtifact("old", "Old", "body", time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)),
		persistedArtifact("new", "New", "body", time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC)),
		persistedArtifact("mid", "Mid", "body", time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)),
	}

	recent := getRecentArtifacts(artifactsIn, 2)
	got := []string{recent[0].PersistedID, recent[1].PersistedID}
	want := []string{"new", "mid"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected recency order %v, got %v", want, got)
	}
}

func TestExtractTopicsFiltersStopwordsAndSortsDeterministically(t *testing.T) {
	artifactsIn := []artifacts.PersistedArtifact{
		persistedArtifact("one", "This roadmap covers alpha planning", "about beta launch and gamma testing", time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)),
		persistedArtifact("two", "Delta rollout", "with epsilon checks and zeta review", time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)),
	}

	got := extractTopics(artifactsIn)
	want := []string{"alpha", "checks", "covers", "delta", "epsilon"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected topics %v, got %v", want, got)
	}
}

func writeAmbientSnapshot(t *testing.T, stateDir string, ambient []artifacts.PersistedArtifact) {
	t.Helper()
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}

	data, err := json.Marshal(ambient)
	if err != nil {
		t.Fatalf("marshal ambient snapshot: %v", err)
	}

	path := filepath.Join(stateDir, ".ambient_context.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write ambient snapshot: %v", err)
	}
}

func persistedArtifact(id, title, body string, writtenAt time.Time) artifacts.PersistedArtifact {
	return artifacts.PersistedArtifact{
		PersistedID:  id,
		ArtifactType: "MEMORY",
		Scope:        string(artifacts.ScopeAgent),
		Title:        title,
		Body:         body,
		Confidence:   artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"evidence for " + id},
		},
		CandidateID: "candidate-" + id,
		BackendType: "file",
		WrittenAt:   writtenAt,
		WriteStatus: "written",
	}
}
