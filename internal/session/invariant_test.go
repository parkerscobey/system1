package session

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
)

type testBackend struct {
	artifacts []artifacts.PersistedArtifact
	err       error
}

func (b *testBackend) Save(_ context.Context, a artifacts.PersistedArtifact) error {
	if b.err != nil {
		return b.err
	}
	b.artifacts = append(b.artifacts, a)
	return nil
}

func (b *testBackend) Get(_ context.Context, id string) (artifacts.PersistedArtifact, error) {
	if b.err != nil {
		return artifacts.PersistedArtifact{}, b.err
	}
	for _, a := range b.artifacts {
		if a.PersistedID == id {
			return a, nil
		}
	}
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (b *testBackend) GetByCandidate(_ context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
	if b.err != nil {
		return artifacts.PersistedArtifact{}, b.err
	}
	for _, a := range b.artifacts {
		if a.CandidateID == candidateID {
			return a, nil
		}
	}
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (b *testBackend) FindByType(_ context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
	if b.err != nil {
		return nil, b.err
	}
	var result []artifacts.PersistedArtifact
	for _, a := range b.artifacts {
		if a.ArtifactType == artifactType {
			result = append(result, a)
		}
	}
	return result, nil
}

func (b *testBackend) FindByScope(_ context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	if b.err != nil {
		return nil, b.err
	}
	var result []artifacts.PersistedArtifact
	for _, a := range b.artifacts {
		if a.Scope == string(scope) {
			result = append(result, a)
		}
	}
	return result, nil
}

func (b *testBackend) FindBounded(_ context.Context, _, _ time.Time) ([]artifacts.PersistedArtifact, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.artifacts, nil
}

func (b *testBackend) Search(_ context.Context, _ string, _ int) ([]artifacts.PersistedArtifact, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.artifacts, nil
}

func (b *testBackend) TypeRegistry(_ context.Context) ([]string, error) {
	if b.err != nil {
		return nil, b.err
	}
	return []string{"MEMORY", "KNOWLEDGE"}, nil
}

func (b *testBackend) Close() error              { return nil }
func (b *testBackend) Type() backend.BackendType { return backend.BackendType("test") }

// Invariant 5: "failures must degrade, not destroy"

func TestSessionStartDegradesGracefullyOnPartialBackendFailure(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{
		artifacts: []artifacts.PersistedArtifact{
			{PersistedID: "p1", ArtifactType: "KNOWLEDGE", Title: "Architecture", Body: "MVP structure.", WrittenAt: time.Now().UTC()},
		},
	}

	cfg := config.Config{StateDir: tmpDir, EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}

	svc := NewService(logger, cfg, be)
	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("invariant 5 violation: session start must not fail on partial backend, got: %v", err)
	}
	if result.WakingMind == "" {
		t.Fatalf("invariant 5 violation: session start should produce a Waking Mind even with partial data")
	}
}

func TestSessionStartHandlesEmptyBackend(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{artifacts: nil}

	cfg := config.Config{StateDir: tmpDir, EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}

	svc := NewService(logger, cfg, be)
	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("invariant 5 violation: session start must not fail on empty backend, got: %v", err)
	}
	if result.WakingMind == "" {
		t.Fatalf("invariant 5 violation: session start must produce a Waking Mind even with no artifacts")
	}
}

func TestLoadAmbientSnapshotMissingDir(t *testing.T) {
	result, err := LoadAmbientSnapshot(filepath.Join(t.TempDir(), "nonexistent"))
	if err != nil {
		t.Fatalf("invariant 5 violation: loadAmbientSnapshot must degrade on missing dir, got: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil ambient snapshot from missing dir, got %v", result)
	}
}

func TestLoadAmbientSnapshotCorruptFile(t *testing.T) {
	tmpDir := t.TempDir()
	stateDir := filepath.Join(tmpDir, "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stateDir, ".ambient_context.json"), []byte("{corrupt"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	result, err := LoadAmbientSnapshot(stateDir)
	if err == nil {
		t.Fatalf("invariant 5 violation: corrupt ambient snapshot must return error, got nil")
	}
	if result != nil {
		t.Fatalf("invariant 5 violation: corrupt ambient snapshot must return nil result, got %v", result)
	}
}

func TestSessionEndWithoutStartIsNoOp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{artifacts: nil}
	cfg := config.Config{StateDir: tmpDir, EnabledTypes: []string{"MEMORY"}}

	svc := NewService(logger, cfg, be)
	err := svc.End(context.Background())
	if err != nil {
		t.Fatalf("invariant 5 violation: End without prior Start must not fail, got: %v", err)
	}
}

func TestSessionStartPersistsAmbientSnapshot(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{
		artifacts: []artifacts.PersistedArtifact{
			{PersistedID: "p1", ArtifactType: "MEMORY", Title: "Test", Body: "body", WrittenAt: time.Now().UTC()},
		},
	}

	cfg := config.Config{StateDir: tmpDir, EnabledTypes: []string{"MEMORY"}}

	svc := NewService(logger, cfg, be)
	_, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	snapshot, err := LoadAmbientSnapshot(tmpDir)
	if err != nil {
		t.Fatalf("load ambient snapshot: %v", err)
	}
	if len(snapshot) != 1 {
		t.Fatalf("invariant 4 violation: persisted snapshot should contain artifacts, got %d", len(snapshot))
	}
	if snapshot[0].PersistedID != "p1" {
		t.Fatalf("expected persisted_id p1, got %s", snapshot[0].PersistedID)
	}
}
