package introspect

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
)

type stubHizalBackend struct {
	searchReqs          []backend.SearchContextRequest
	searchResultsByCall [][]artifacts.PersistedArtifact
	searchCallCount     int
	readCalls           int
	defaultResult       artifacts.PersistedArtifact
}

func (b *stubHizalBackend) Save(context.Context, artifacts.PersistedArtifact) error {
	return nil
}

func (b *stubHizalBackend) Get(context.Context, string) (artifacts.PersistedArtifact, error) {
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (b *stubHizalBackend) GetByCandidate(context.Context, string) (artifacts.PersistedArtifact, error) {
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (b *stubHizalBackend) FindByType(context.Context, string) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}

func (b *stubHizalBackend) FindByScope(context.Context, artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}

func (b *stubHizalBackend) FindBounded(context.Context, time.Time, time.Time) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}

func (b *stubHizalBackend) Search(context.Context, string, int) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}

func (b *stubHizalBackend) TypeRegistry(context.Context) ([]string, error) {
	return []string{"MEMORY", "KNOWLEDGE"}, nil
}

func (b *stubHizalBackend) Close() error {
	return nil
}

func (b *stubHizalBackend) Type() backend.BackendType {
	return backend.BackendTypeHizal
}

func (b *stubHizalBackend) SearchContext(_ context.Context, req backend.SearchContextRequest) ([]artifacts.PersistedArtifact, error) {
	b.searchReqs = append(b.searchReqs, req)
	if b.searchCallCount < len(b.searchResultsByCall) {
		results := b.searchResultsByCall[b.searchCallCount]
		b.searchCallCount++
		return results, nil
	}
	b.searchCallCount++
	return []artifacts.PersistedArtifact{b.defaultResult}, nil
}

func (b *stubHizalBackend) ReadContext(_ context.Context, _ string, _ string) (artifacts.PersistedArtifact, error) {
	b.readCalls++
	return b.defaultResult, nil
}

func TestQueryHizalBackendRunsMultiStepSearchPlan(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:      tmpDir,
		ArtifactsDir:  filepath.Join(tmpDir, "artifacts"),
		SQLitePath:    filepath.Join(tmpDir, "test.db"),
		EnabledTypes:  []string{"MEMORY", "KNOWLEDGE"},
		BackendType:   "hizal",
		ModelTimeout:  5 * time.Second,
		ModelProvider: "openrouter",
	}

	b := &stubHizalBackend{defaultResult: artifacts.PersistedArtifact{
		PersistedID:  "chunk-1",
		ArtifactType: "MEMORY",
		Scope:        string(artifacts.ScopeAgent),
		Title:        "Parker preference",
		Body:         "I prefer clear APIs",
		WrittenAt:    time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC),
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"I prefer clear APIs"},
		},
		BackendMetadata: map[string]any{
			"chunk_id":  "chunk-1",
			"query_key": "pref-clear-apis",
		},
	}}

	mockProv := model.NewMockProvider("planner")
	mockProv.AddResponse(model.Response{Text: `{"steps":[{"query":"what do I know about preferences","limit":9},{"query":"preferences","scope":"AGENT","chunk_type":"MEMORY","limit":6}]}`})

	svc := NewService(logger, cfg, b)
	svc.SetModelProvider(mockProv)

	result, err := svc.Query(ctx, "what do I know about preferences", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(b.searchReqs) < 2 {
		t.Fatalf("expected multiple hizal search steps, got %d", len(b.searchReqs))
	}
	if b.readCalls == 0 {
		t.Fatal("expected read-context verification call")
	}
	if strings.Contains(result.Answer, "Starting fresh") {
		t.Fatalf("expected grounded answer from hizal retrieval, got %q", result.Answer)
	}
}

func TestQueryHizalBackendReflectiveModeCapsPasses(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:        tmpDir,
		ArtifactsDir:    filepath.Join(tmpDir, "artifacts"),
		SQLitePath:      filepath.Join(tmpDir, "test.db"),
		EnabledTypes:    []string{"MEMORY", "KNOWLEDGE"},
		BackendType:     "hizal",
		ModelTimeout:    5 * time.Second,
		ModelProvider:   "openrouter",
		DefaultPassMode: "reflective",
	}

	b := &stubHizalBackend{defaultResult: artifacts.PersistedArtifact{
		PersistedID:  "chunk-1",
		ArtifactType: "MEMORY",
		Scope:        string(artifacts.ScopeAgent),
		Title:        "Parker preference",
		Body:         "I prefer clear APIs",
		WrittenAt:    time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC),
		BackendMetadata: map[string]any{
			"chunk_id":  "chunk-1",
			"query_key": "pref-clear-apis",
		},
	}}

	mockProv := model.NewMockProvider("planner")
	mockProv.AddResponse(model.Response{Text: `{"steps":[{"query":"step-1"},{"query":"step-2"},{"query":"step-3"},{"query":"step-4"}]}`})

	svc := NewService(logger, cfg, b)
	svc.SetModelProvider(mockProv)

	_, err := svc.Query(ctx, "what do I know about preferences", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(b.searchReqs) != 2 {
		t.Fatalf("expected reflective mode to use 2 passes, got %d", len(b.searchReqs))
	}
}

func TestQueryHizalBackendStopsEarlyOnLowNovelty(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:        tmpDir,
		ArtifactsDir:    filepath.Join(tmpDir, "artifacts"),
		SQLitePath:      filepath.Join(tmpDir, "test.db"),
		EnabledTypes:    []string{"MEMORY", "KNOWLEDGE"},
		BackendType:     "hizal",
		ModelTimeout:    5 * time.Second,
		ModelProvider:   "openrouter",
		DefaultPassMode: "ruminating",
	}

	art := artifacts.PersistedArtifact{
		PersistedID:  "chunk-1",
		ArtifactType: "MEMORY",
		Scope:        string(artifacts.ScopeAgent),
		Title:        "Parker preference",
		Body:         "I prefer clear APIs",
		WrittenAt:    time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC),
		BackendMetadata: map[string]any{
			"chunk_id":  "chunk-1",
			"query_key": "pref-clear-apis",
		},
	}

	b := &stubHizalBackend{
		defaultResult: art,
		searchResultsByCall: [][]artifacts.PersistedArtifact{
			{art},
			{art},
			{art},
		},
	}

	mockProv := model.NewMockProvider("planner")
	mockProv.AddResponse(model.Response{Text: `{"steps":[{"query":"step-1"},{"query":"step-2"},{"query":"step-3"}]}`})

	svc := NewService(logger, cfg, b)
	svc.SetModelProvider(mockProv)

	_, err := svc.Query(ctx, "what do I know about preferences", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(b.searchReqs) != 2 {
		t.Fatalf("expected early stop on low novelty after second pass, got %d passes", len(b.searchReqs))
	}
}

func TestReflectiveModeDoesNotAmbientShortCircuitAmbiguousAcronym(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:        tmpDir,
		ArtifactsDir:    filepath.Join(tmpDir, "artifacts"),
		SQLitePath:      filepath.Join(tmpDir, "test.db"),
		EnabledTypes:    []string{"MEMORY", "KNOWLEDGE"},
		BackendType:     "hizal",
		DefaultPassMode: "reflective",
	}

	ambient := persistedArtifact("ambient-1", "Parker profile", "Parker founder profile context", time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{ambient})

	b := &stubHizalBackend{defaultResult: artifacts.PersistedArtifact{
		PersistedID:  "chunk-adh",
		ArtifactType: "KNOWLEDGE",
		Scope:        string(artifacts.ScopeProject),
		Title:        "ADH meaning",
		Body:         "ADH means Agentic Development with Hizal.",
		WrittenAt:    time.Date(2026, 4, 15, 11, 0, 0, 0, time.UTC),
	}}

	svc := NewService(logger, cfg, b)
	result, err := svc.Query(ctx, "what does Parker mean when he says ADH?", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if len(b.searchReqs) == 0 {
		t.Fatal("expected backend retrieval for ambiguous acronym query")
	}
	if strings.Contains(result.Answer, "Found in preloaded context") {
		t.Fatalf("expected query to avoid ambient-only early stop, got %q", result.Answer)
	}
}
