package backend

import (
	"context"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
)

func TestTypeRegistry(t *testing.T) {
	tr := NewTypeRegistry([]string{"MEMORY", "KNOWLEDGE", "CONVENTION"})

	tests := []struct {
		name     string
		typeName string
		want     bool
	}{
		{"has memory", "MEMORY", true},
		{"has knowledge", "KNOWLEDGE", true},
		{"has convention", "CONVENTION", true},
		{"missing identity", "IDENTITY", false},
		{"missing principle", "PRINCIPLE", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tr.Has(tt.typeName); got != tt.want {
				t.Errorf("TypeRegistry.Has(%q) = %v, want %v", tt.typeName, got, tt.want)
			}
		})
	}

	types := tr.Types()
	if len(types) != 3 {
		t.Errorf("TypeRegistry.Types() = %v, want 3 types", types)
	}
}

type mockBackend struct {
	artifacts map[string]artifacts.PersistedArtifact
	types     []string
}

func (m *mockBackend) Save(ctx context.Context, a artifacts.PersistedArtifact) error {
	m.artifacts[a.PersistedID] = a
	return nil
}

func (m *mockBackend) Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
	if a, ok := m.artifacts[id]; ok {
		return a, nil
	}
	return artifacts.PersistedArtifact{}, ErrNotFound
}

func (m *mockBackend) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
	for _, a := range m.artifacts {
		if a.CandidateID == candidateID {
			return a, nil
		}
	}
	return artifacts.PersistedArtifact{}, ErrNotFound
}

func (m *mockBackend) FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
	var results []artifacts.PersistedArtifact
	for _, a := range m.artifacts {
		if a.ArtifactType == artifactType {
			results = append(results, a)
		}
	}
	return results, nil
}

func (m *mockBackend) FindByScope(ctx context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	var results []artifacts.PersistedArtifact
	for _, a := range m.artifacts {
		if a.Scope == string(scope) {
			results = append(results, a)
		}
	}
	return results, nil
}

func (m *mockBackend) FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error) {
	var results []artifacts.PersistedArtifact
	for _, a := range m.artifacts {
		if !a.WrittenAt.Before(since) && !a.WrittenAt.After(until) {
			results = append(results, a)
		}
	}
	return results, nil
}

func (m *mockBackend) Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}

func (m *mockBackend) TypeRegistry(ctx context.Context) ([]string, error) {
	return m.types, nil
}

func (m *mockBackend) Close() error {
	return nil
}

func (m *mockBackend) Type() BackendType {
	return BackendTypeFile
}

func TestBackendInterface(t *testing.T) {
	be := &mockBackend{
		artifacts: make(map[string]artifacts.PersistedArtifact),
		types:     []string{"MEMORY", "KNOWLEDGE"},
	}

	ctx := context.Background()
	now := time.Now()

	artifact := artifacts.PersistedArtifact{
		PersistedID:  "test-1",
		ArtifactType: "MEMORY",
		Scope:        "PROJECT",
		Title:        "Test Memory",
		Body:         "This is a test memory",
		Confidence:   "high",
		CandidateID:  "candidate-1",
		BackendType:  "file",
		WrittenAt:    now,
		WriteStatus:  "created",
	}

	if err := be.Save(ctx, artifact); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	retrieved, err := be.Get(ctx, "test-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if retrieved.PersistedID != "test-1" {
		t.Errorf("Get returned wrong ID: %v", retrieved.PersistedID)
	}

	types, err := be.TypeRegistry(ctx)
	if err != nil {
		t.Fatalf("TypeRegistry failed: %v", err)
	}
	if len(types) != 2 {
		t.Errorf("TypeRegistry returned %v, want 2 types", types)
	}

	if be.Type() != BackendTypeFile {
		t.Errorf("Type returned %v, want file", be.Type())
	}
}
