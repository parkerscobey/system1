package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
)

func TestModelWakingMindHappyPath(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{
		artifacts: []artifacts.PersistedArtifact{
			{
				PersistedID:  "p1",
				ArtifactType: "MEMORY",
				Title:        "Go preferences",
				Body:         "Prefers clear error messages and well-structured Go packages.",
				WrittenAt:    time.Now().UTC(),
				Provenance: artifacts.Provenance{
					EvidenceSnippets: []string{"prefers clear errors"},
				},
			},
			{
				PersistedID:  "p2",
				ArtifactType: "KNOWLEDGE",
				Title:        "System-1 architecture",
				Body:         "Go daemon with extraction pipeline, policy evaluation, and introspection API.",
				WrittenAt:    time.Now().UTC().Add(-1 * time.Hour),
				Provenance: artifacts.Provenance{
					EvidenceSnippets: []string{"Go daemon architecture"},
				},
			},
		},
	}

	cfg := config.Config{
		StateDir:     tmpDir,
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
		ModelTimeout: 10 * time.Second,
	}

	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: "You're returning to a Go-based context. Your recent work focused on System-1's architecture — specifically the extraction pipeline and introspection API. You have a strong preference for clear error messages and well-structured packages. The ambient context gives you both technical architecture knowledge and personal working preferences.",
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Model:    "test",
		},
	})

	svc := NewService(logger, cfg, be)
	svc.SetModelProvider(mockProv)

	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if !strings.Contains(result.WakingMind, "System-1") {
		t.Fatalf("expected model-generated waking mind to reference System-1, got %q", result.WakingMind)
	}

	if strings.Contains(result.WakingMind, "=== WAKING MIND ===") {
		t.Fatalf("model response should not contain heuristic header, got %q", result.WakingMind)
	}

	if mockProv.CallCount() != 1 {
		t.Fatalf("expected 1 model call, got %d", mockProv.CallCount())
	}
}

func TestModelWakingMindFallbackOnError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{
		artifacts: []artifacts.PersistedArtifact{
			{PersistedID: "p1", ArtifactType: "MEMORY", Title: "Test", Body: "body", WrittenAt: time.Now().UTC()},
		},
	}

	cfg := config.Config{
		StateDir:     tmpDir,
		EnabledTypes: []string{"MEMORY"},
		ModelTimeout: 10 * time.Second,
	}

	mockProv := model.NewMockProvider("test-model")
	mockProv.AddError(errors.New("model unavailable"))

	svc := NewService(logger, cfg, be)
	svc.SetModelProvider(mockProv)

	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Should fall back to heuristic format
	if !strings.Contains(result.WakingMind, "=== WAKING MIND ===") {
		t.Fatalf("expected heuristic fallback, got %q", result.WakingMind)
	}

	if mockProv.CallCount() != 1 {
		t.Fatalf("expected 1 model call, got %d", mockProv.CallCount())
	}
}

func TestModelWakingMindFallbackOnEmptyResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{
		artifacts: []artifacts.PersistedArtifact{
			{PersistedID: "p1", ArtifactType: "MEMORY", Title: "Test", Body: "body", WrittenAt: time.Now().UTC()},
		},
	}

	cfg := config.Config{
		StateDir:     tmpDir,
		EnabledTypes: []string{"MEMORY"},
		ModelTimeout: 10 * time.Second,
	}

	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: "",
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
		},
	})

	svc := NewService(logger, cfg, be)
	svc.SetModelProvider(mockProv)

	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Should fall back to heuristic format
	if !strings.Contains(result.WakingMind, "=== WAKING MIND ===") {
		t.Fatalf("expected heuristic fallback, got %q", result.WakingMind)
	}
}

func TestModelWakingMindNilProviderUsesHeuristics(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{
		artifacts: []artifacts.PersistedArtifact{
			{PersistedID: "p1", ArtifactType: "MEMORY", Title: "Test title", Body: "Test body content", WrittenAt: time.Now().UTC()},
		},
	}

	cfg := config.Config{
		StateDir:     tmpDir,
		EnabledTypes: []string{"MEMORY"},
	}

	// No model provider set — should use heuristics
	svc := NewService(logger, cfg, be)

	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if !strings.Contains(result.WakingMind, "=== WAKING MIND ===") {
		t.Fatalf("expected heuristic waking mind, got %q", result.WakingMind)
	}

	if !strings.Contains(result.WakingMind, "Test title") {
		t.Fatalf("expected heuristic waking mind to contain artifact title, got %q", result.WakingMind)
	}
}

func TestModelWakingMindWithManyArtifacts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	var longArtifacts []artifacts.PersistedArtifact
	for i := 0; i < 20; i++ {
		longArtifacts = append(longArtifacts, artifacts.PersistedArtifact{
			PersistedID:  fmt.Sprintf("p%d", i),
			ArtifactType: "MEMORY",
			Title:        fmt.Sprintf("Artifact %d", i),
			Body:         strings.Repeat("Long body content. ", 20),
			WrittenAt:    time.Now().UTC().Add(time.Duration(-i) * time.Hour),
		})
	}

	be := &testBackend{artifacts: longArtifacts}

	cfg := config.Config{
		StateDir:     tmpDir,
		EnabledTypes: []string{"MEMORY"},
		ModelTimeout: 10 * time.Second,
	}

	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: "Concise orientation based on 20 recent artifacts covering various memory types.",
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
		},
	})

	svc := NewService(logger, cfg, be)
	svc.SetModelProvider(mockProv)

	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Verify the model response is used as-is
	if result.WakingMind != "Concise orientation based on 20 recent artifacts covering various memory types." {
		t.Fatalf("expected model response to be used verbatim, got %q", result.WakingMind)
	}
}

func TestModelWakingMindEmptyBackendStillProducesOutput(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()

	be := &testBackend{artifacts: nil}

	cfg := config.Config{
		StateDir:     tmpDir,
		EnabledTypes: []string{"MEMORY"},
	}

	mockProv := model.NewMockProvider("test-model")

	svc := NewService(logger, cfg, be)
	svc.SetModelProvider(mockProv)

	result, err := svc.Start(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// Empty backend should not call model at all
	if mockProv.CallCount() != 0 {
		t.Fatalf("expected 0 model calls for empty backend, got %d", mockProv.CallCount())
	}

	if result.WakingMind == "" {
		t.Fatal("expected non-empty waking mind even with no artifacts")
	}
}

func TestBuildWakingMindPromptIncludesAllContext(t *testing.T) {
	testArtifacts := []artifacts.PersistedArtifact{
		{
			PersistedID:  "artifact-1",
			ArtifactType: "MEMORY",
			Title:        "User preferences",
			Body:         "Prefers concise communication and clear error messages.",
			WrittenAt:    time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC),
			Provenance: artifacts.Provenance{
				EvidenceSnippets: []string{"prefers concise comms", "clear error messages"},
			},
		},
		{
			PersistedID:  "artifact-2",
			ArtifactType: "KNOWLEDGE",
			Title:        "System-1 extraction pipeline",
			Body:         "Go-based extraction pipeline with model-driven and heuristic paths.",
			WrittenAt:    time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC),
			Provenance: artifacts.Provenance{
				EvidenceSnippets: []string{"extraction pipeline design"},
			},
		},
	}

	prompt := buildWakingMindPrompt(testArtifacts)

	if !strings.Contains(prompt, "MEMORY") {
		t.Error("prompt should contain artifact type")
	}
	if !strings.Contains(prompt, "User preferences") {
		t.Error("prompt should contain artifact title")
	}
	if !strings.Contains(prompt, "Prefers concise communication") {
		t.Error("prompt should contain artifact body")
	}
	if !strings.Contains(prompt, "2026-04-17") {
		t.Error("prompt should contain written date")
	}
	if !strings.Contains(prompt, "prefers concise comms") {
		t.Error("prompt should contain evidence snippets")
	}
	if !strings.Contains(prompt, "KNOWLEDGE") {
		t.Error("prompt should contain second artifact type")
	}
	if !strings.Contains(prompt, "System-1 extraction pipeline") {
		t.Error("prompt should contain second artifact title")
	}
}

func TestBuildWakingMindPromptTruncatesLongBodies(t *testing.T) {
	longBody := strings.Repeat("x", 500)
	testArtifacts := []artifacts.PersistedArtifact{
		{
			PersistedID:  "artifact-1",
			ArtifactType: "MEMORY",
			Title:        "Long artifact",
			Body:         longBody,
			WrittenAt:    time.Now().UTC(),
		},
	}

	prompt := buildWakingMindPrompt(testArtifacts)

	// Body should be truncated to 300 chars + "..."
	xCount := strings.Count(prompt, "x")
	if xCount < 299 || xCount > 301 {
		t.Errorf("expected body truncated to ~300 x chars, got %d", xCount)
	}
	if !strings.Contains(prompt, "...") {
		t.Error("expected truncation indicator")
	}
}

func TestBuildWakingMindPromptLimitsEvidence(t *testing.T) {
	testArtifacts := []artifacts.PersistedArtifact{
		{
			PersistedID:  "artifact-1",
			ArtifactType: "MEMORY",
			Title:        "Multi-evidence",
			Body:         "body",
			WrittenAt:    time.Now().UTC(),
			Provenance: artifacts.Provenance{
				EvidenceSnippets: []string{"snippet-alpha", "snippet-beta", "snippet-gamma", "snippet-delta", "snippet-epsilon"},
			},
		},
	}

	prompt := buildWakingMindPrompt(testArtifacts)

	// Should include first 3 evidence snippets
	if !strings.Contains(prompt, "snippet-alpha") {
		t.Error("expected first evidence snippet")
	}
	if !strings.Contains(prompt, "snippet-beta") {
		t.Error("expected second evidence snippet")
	}
	if !strings.Contains(prompt, "snippet-gamma") {
		t.Error("expected third evidence snippet")
	}
	// Should NOT include 4th and 5th
	if strings.Contains(prompt, "snippet-delta") {
		t.Error("should not include 4th evidence snippet")
	}
	if strings.Contains(prompt, "snippet-epsilon") {
		t.Error("should not include 5th evidence snippet")
	}
}

func TestSetModelProviderSession(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{}
	svc := NewService(logger, cfg, nil)

	if svc.provider != nil {
		t.Error("expected provider to be nil initially")
	}

	mockProv := model.NewMockProvider("test-model")
	svc.SetModelProvider(mockProv)

	if svc.provider == nil {
		t.Error("expected provider to be set")
	}

	if svc.provider.Name() != "test-model" {
		t.Errorf("expected provider name 'test-model', got %q", svc.provider.Name())
	}
}
