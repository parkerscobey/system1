package hizal

import (
	"context"
	"log/slog"
	"testing"

	"github.com/XferOps/system1/internal/backend"
)

func TestStore_NewStore(t *testing.T) {
	logger := slog.Default()
	store := NewStore(logger, "test-project", []string{"MEMORY", "KNOWLEDGE", "CONVENTION"})

	if store == nil {
		t.Fatal("NewStore returned nil")
	}

	if store.Type() != backend.BackendTypeHizal {
		t.Errorf("Type() = %v, want hizal", store.Type())
	}
}

func TestStore_TypeRegistry(t *testing.T) {
	logger := slog.Default()
	store := NewStore(logger, "test-project", []string{"MEMORY", "KNOWLEDGE"})

	ctx := context.Background()
	types, err := store.TypeRegistry(ctx)
	if err != nil {
		t.Fatalf("TypeRegistry failed: %v", err)
	}

	if len(types) == 0 {
		t.Error("TypeRegistry returned empty types")
	}
}

func TestStore_MappingFunctions(t *testing.T) {
	logger := slog.Default()
	store := NewStore(logger, "test-project", []string{"MEMORY", "KNOWLEDGE"})

	tests := []struct {
		artifactType string
		wantChunk    string
	}{
		{"MEMORY", "MEMORY"},
		{"KNOWLEDGE", "KNOWLEDGE"},
		{"CONVENTION", "CONVENTION"},
		{"DECISION", "DECISION"},
		{"PRINCIPLE", "PRINCIPLE"},
		{"IDENTITY", "KNOWLEDGE"},
	}

	for _, tt := range tests {
		t.Run(tt.artifactType, func(t *testing.T) {
			got := store.mapArtifactTypeToChunk(tt.artifactType)
			if got != tt.wantChunk {
				t.Errorf("mapArtifactTypeToChunk(%q) = %v, want %v", tt.artifactType, got, tt.wantChunk)
			}
		})
	}

	for _, tt := range tests {
		isOneWay := tt.artifactType == "IDENTITY"
		name := tt.artifactType
		if isOneWay {
			name = "one-way-" + name
		} else {
			name = "bidir-" + name
		}
		t.Run(name, func(t *testing.T) {
			got := store.mapChunkToArtifactType(tt.wantChunk)
			if isOneWay {
				if got != tt.wantChunk {
					t.Errorf("mapChunkToArtifactType(%q) = %v, want %v (one-way mapping)", tt.wantChunk, got, tt.wantChunk)
				}
			} else {
				if got != tt.artifactType {
					t.Errorf("mapChunkToArtifactType(%q) = %v, want %v", tt.wantChunk, got, tt.artifactType)
				}
			}
		})
	}
}

func TestSessionLifecycle_StartEnd(t *testing.T) {
	logger := slog.Default()
	lifecycle := NewSessionLifecycle(logger, "test-project")

	ctx := context.Background()

	result, err := lifecycle.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if result.SessionID == "" {
		t.Error("Start returned empty session ID")
	}

	if !lifecycle.IsActive() {
		t.Error("IsActive should be true after Start")
	}

	if err := lifecycle.End(ctx); err != nil {
		t.Fatalf("End failed: %v", err)
	}

	if lifecycle.IsActive() {
		t.Error("IsActive should be false after End")
	}
}

func TestSessionLifecycle_Resume(t *testing.T) {
	logger := slog.Default()
	lifecycle := NewSessionLifecycle(logger, "test-project")

	ctx := context.Background()

	result1, err := lifecycle.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	result2, err := lifecycle.Resume(ctx)
	if err != nil {
		t.Fatalf("Resume failed: %v", err)
	}

	if result1.SessionID != result2.SessionID {
		t.Errorf("Resume returned different session ID: %v vs %v", result1.SessionID, result2.SessionID)
	}
}

func TestSessionLifecycle_RegisterFocus(t *testing.T) {
	logger := slog.Default()
	lifecycle := NewSessionLifecycle(logger, "test-project")

	ctx := context.Background()

	lifecycle.Start(ctx)

	err := lifecycle.RegisterFocus(ctx, "SYS1-11: Test Task", []string{"system1", "ticket:SYS1-11", "area:backend"})
	if err != nil {
		t.Fatalf("RegisterFocus failed: %v", err)
	}

	lifecycle.End(ctx)
}