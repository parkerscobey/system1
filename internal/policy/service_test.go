package policy

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
)

func TestEvaluateWithoutBackendSkipsDedup(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-1",
		ArtifactType:  "MEMORY",
		ProposedScope: "PROJECT",
		Title:         "Clear APIs matter",
		Body:          "The user prefers clear APIs and understandable errors.",
		Confidence:    artifacts.ConfidenceHigh,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"The user prefers clear APIs."},
		},
	}

	result, err := svc.Evaluate(context.Background(), candidate)
	if err != nil {
		t.Fatalf("Evaluate returned error: %v", err)
	}
	if result.Status != artifacts.StatusApproved {
		t.Fatalf("expected approved status, got %s", result.Status)
	}
}

func TestResolveDeferredWithoutBackendReturnsExplicitError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}, nil)

	_, err := svc.ResolveDeferred(context.Background())
	if !errors.Is(err, ErrNoBackend) {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}

func TestPersistApprovedWithoutBackendReturnsExplicitError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}, nil)

	candidate := artifacts.CandidateArtifact{
		CandidateID:   "cand-2",
		ArtifactType:  "MEMORY",
		ProposedScope: "PROJECT",
		Title:         "Clear APIs matter",
		Body:          "The user prefers clear APIs and understandable errors.",
		Confidence:    artifacts.ConfidenceHigh,
		Status:        artifacts.StatusApproved,
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"The user prefers clear APIs."},
		},
	}

	_, err := svc.PersistApproved(context.Background(), candidate)
	if !errors.Is(err, ErrNoBackend) {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}

type maintenanceStub struct {
	byID     map[string]artifacts.PersistedArtifact
	updated  bool
	updatedID string
}

func (m *maintenanceStub) Save(context.Context, artifacts.PersistedArtifact) error { return nil }
func (m *maintenanceStub) Get(_ context.Context, id string) (artifacts.PersistedArtifact, error) {
	a, ok := m.byID[id]
	if !ok {
		return artifacts.PersistedArtifact{}, backend.ErrNotFound
	}
	return a, nil
}
func (m *maintenanceStub) GetByCandidate(context.Context, string) (artifacts.PersistedArtifact, error) {
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}
func (m *maintenanceStub) FindByType(_ context.Context, _ string) ([]artifacts.PersistedArtifact, error) {
	out := make([]artifacts.PersistedArtifact, 0, len(m.byID))
	for _, a := range m.byID {
		out = append(out, a)
	}
	return out, nil
}
func (m *maintenanceStub) FindByScope(context.Context, artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}
func (m *maintenanceStub) FindBounded(context.Context, time.Time, time.Time) ([]artifacts.PersistedArtifact, error) {
	return nil, nil
}
func (m *maintenanceStub) Search(context.Context, string, int) ([]artifacts.PersistedArtifact, error) { return nil, nil }
func (m *maintenanceStub) TypeRegistry(context.Context) ([]string, error) { return []string{"MEMORY"}, nil }
func (m *maintenanceStub) Close() error { return nil }
func (m *maintenanceStub) Type() backend.BackendType { return backend.BackendTypeFile }
func (m *maintenanceStub) UpdateExisting(_ context.Context, existing artifacts.PersistedArtifact, candidate artifacts.CandidateArtifact) (artifacts.PersistedArtifact, error) {
	m.updated = true
	m.updatedID = existing.PersistedID
	existing.Body = candidate.Body
	existing.Title = candidate.Title
	existing.WriteStatus = "updated"
	return existing, nil
}

func TestPersistApprovedUsesSilentRectificationPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &maintenanceStub{byID: map[string]artifacts.PersistedArtifact{
		"chunk-1": {
			PersistedID:  "chunk-1",
			ArtifactType: "MEMORY",
			Scope:        "AGENT",
			Title:        "About Parker",
			Body:         "Location: Chicago",
		},
	}}
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY"}}, store)

	candidate := artifacts.CandidateArtifact{
		CandidateID:    "cand-update",
		ArtifactType:   "MEMORY",
		ProposedScope:  "AGENT",
		Title:          "About Parker",
		Body:           "Location: Tennessee",
		Confidence:     artifacts.ConfidenceHigh,
		Status:         artifacts.StatusApproved,
		ApprovalReason: "update_existing:chunk-1",
		Provenance: artifacts.Provenance{
			EvidenceSnippets: []string{"Location: Tennessee"},
		},
	}

	persisted, err := svc.PersistApproved(context.Background(), candidate)
	if err != nil {
		t.Fatalf("PersistApproved failed: %v", err)
	}
	if !store.updated {
		t.Fatalf("expected maintenance update path to run")
	}
	if store.updatedID != "chunk-1" {
		t.Fatalf("updatedID = %q, want chunk-1", store.updatedID)
	}
	if persisted.WriteStatus != "updated" {
		t.Fatalf("write status = %q, want updated", persisted.WriteStatus)
	}
}
