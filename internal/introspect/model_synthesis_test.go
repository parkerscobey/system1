package introspect

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
)

func TestModelSynthesisHappyPath(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	// Create mock provider that returns a synthesized response
	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: `{"inferred_intent":"summarize known codebase context","answer":"Based on your session context, you're working on Go backend services with SQLite storage. The codebase uses the model provider interface.","supporting_artifact_ids":["artifact-1"]}`,
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Model:    "test",
		},
	})

	artifact := persistedArtifact("artifact-1", "Codebase notes", "The codebase uses Go, cobra, and sqlite3.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	svc.SetModelProvider(mockProv)

	result, err := svc.Query(ctx, "what do I know about the codebase", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !strings.Contains(result.Answer, "Based on your session context") {
		t.Fatalf("expected model-synthesized answer, got %q", result.Answer)
	}

	if mockProv.CallCount() != 1 {
		t.Fatalf("expected 1 model call, got %d", mockProv.CallCount())
	}
}

func TestModelSynthesisFallbackOnError(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	// Create mock provider that returns an error
	mockProv := model.NewMockProvider("test-model")
	mockProv.AddError(errors.New("model unavailable"))

	artifact := persistedArtifact("artifact-1", "Codebase notes", "The codebase uses Go, cobra, and sqlite3.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	svc.SetModelProvider(mockProv)

	result, err := svc.Query(ctx, "what do I know", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	// Should fallback to heuristic synthesis
	if !strings.Contains(result.Answer, "Based on my recent context") {
		t.Fatalf("expected heuristic fallback answer, got %q", result.Answer)
	}

	if mockProv.CallCount() != 1 {
		t.Fatalf("expected 1 model call, got %d", mockProv.CallCount())
	}
}

func TestModelSynthesisFallbackOnEmptyResponse(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	// Create mock provider that returns an empty response
	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: "",
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
		},
	})

	artifact := persistedArtifact("artifact-1", "Codebase notes", "The codebase uses Go, cobra, and sqlite3.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	svc.SetModelProvider(mockProv)

	result, err := svc.Query(ctx, "what do I know", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	// Should fallback to heuristic synthesis
	if !strings.Contains(result.Answer, "Based on my recent context") {
		t.Fatalf("expected heuristic fallback answer, got %q", result.Answer)
	}
}

func TestModelSynthesisPreservesDebugRefsAndEvidence(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	// Create mock provider that returns a synthesized response
	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: `{"inferred_intent":"debug response","answer":"Based on the retrieved artifacts, here is your answer.","supporting_artifact_ids":["artifact-debug"]}`,
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
		},
	})

	artifact := persistedArtifact("artifact-debug", "Debug topic", "debug body content", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	svc.SetModelProvider(mockProv)

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

func TestModelSynthesisWithoutProviderUsesHeuristics(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	artifact := persistedArtifact("artifact-1", "Codebase notes", "The codebase uses Go, cobra, and sqlite3.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	// Create service without setting a model provider
	svc := NewService(logger, cfg, nil)

	result, err := svc.Query(ctx, "what do I know", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	// Should use heuristic synthesis since no model provider is set
	if !strings.Contains(result.Answer, "Based on my recent context") {
		t.Fatalf("expected heuristic answer, got %q", result.Answer)
	}

	if !strings.Contains(result.Answer, artifact.Title) {
		t.Fatalf("expected answer to reference artifact %q, got %q", artifact.Title, result.Answer)
	}
}

func TestModelSynthesisCalibrationQuery(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	// Create mock provider that returns a calibration-style response
	mockProv := model.NewMockProvider("test-model")
	mockProv.AddResponse(model.Response{
		Text: `{"inferred_intent":"identify missing areas","answer":"Looking at your context, you have strong coverage of Go backend patterns but limited frontend documentation. Consider adding UI component examples.","supporting_artifact_ids":["artifact-cal"]}`,
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
		},
	})

	artifact := persistedArtifact("artifact-cal", "Backend notes", "Go backend patterns for API design.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	svc.SetModelProvider(mockProv)

	result, err := svc.Query(ctx, "what am I forgetting", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	if !strings.Contains(result.Answer, "Looking at your context") {
		t.Fatalf("expected model-synthesized calibration answer, got %q", result.Answer)
	}
}

func TestModelSynthesisCalibrationFallback(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	cfg := config.Config{
		StateDir:     tmpDir,
		ArtifactsDir: filepath.Join(tmpDir, "artifacts"),
		SQLitePath:   filepath.Join(tmpDir, "test.db"),
		EnabledTypes: []string{"MEMORY", "KNOWLEDGE"},
	}

	// Create mock provider that returns an error
	mockProv := model.NewMockProvider("test-model")
	mockProv.AddError(errors.New("model timeout"))

	artifact := persistedArtifact("artifact-cal", "Backend notes", "Go backend patterns for API design.", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	writeAmbientSnapshot(t, cfg.StateDir, []artifacts.PersistedArtifact{artifact})

	svc := NewService(logger, cfg, nil)
	svc.SetModelProvider(mockProv)

	result, err := svc.Query(ctx, "what am I forgetting", false)
	if err != nil {
		t.Fatalf("query: %v", err)
	}

	// Should fallback to heuristic calibration synthesis
	if !strings.Contains(result.Answer, "Let me think about what might be missing") {
		t.Fatalf("expected heuristic calibration fallback answer, got %q", result.Answer)
	}
}

func TestBuildModelPromptIncludesAllContext(t *testing.T) {
	artifacts := []artifacts.PersistedArtifact{
		{
			PersistedID:  "artifact-1",
			ArtifactType: "MEMORY",
			Title:        "Test memory",
			Body:         "This is a test memory body that contains important information about the session.",
			Provenance: artifacts.Provenance{
				EvidenceSnippets: []string{"source code reference", "commit message"},
			},
		},
	}

	prompt := buildModelPrompt("what do I know about testing", artifacts, false, "ambient", "reflective")

	if !strings.Contains(prompt, "what do I know about testing") {
		t.Error("prompt should contain user query")
	}

	if !strings.Contains(prompt, "MEMORY") {
		t.Error("prompt should contain artifact type")
	}

	if !strings.Contains(prompt, "Test memory") {
		t.Error("prompt should contain artifact title")
	}

	if !strings.Contains(prompt, "source code reference") {
		t.Error("prompt should contain evidence snippets")
	}

	if !strings.Contains(prompt, "ambient") {
		t.Error("prompt should contain source")
	}
}

func TestBuildModelPromptCalibrationFlag(t *testing.T) {
	artifacts := []artifacts.PersistedArtifact{}

	// Test non-calibration prompt
	normalPrompt := buildModelPrompt("test query", artifacts, false, "ambient", "reflective")
	if strings.Contains(normalPrompt, "calibration query") {
		t.Error("normal prompt should not mention calibration")
	}

	// Test calibration prompt
	calPrompt := buildModelPrompt("what am I missing", artifacts, true, "ambient", "reflective")
	if !strings.Contains(calPrompt, "calibration query") {
		t.Error("calibration prompt should mention calibration")
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	normalPrompt := buildSystemPrompt(false, "reflective")
	if strings.Contains(normalPrompt, "calibration") {
		t.Error("normal system prompt should not mention calibration")
	}
	if !strings.Contains(normalPrompt, "subconscious") {
		t.Error("normal system prompt should mention subconscious voice")
	}
	if !strings.Contains(normalPrompt, "first person") {
		t.Error("normal system prompt should enforce first-person voice")
	}

	calPrompt := buildSystemPrompt(true, "metacognitive")
	if !strings.Contains(calPrompt, "likely missing context") {
		t.Error("calibration system prompt should mention identifying gaps")
	}
}

func TestSetModelProvider(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{}
	svc := NewService(logger, cfg, nil)

	// Initially should be nil
	if svc.modelProv != nil {
		t.Error("expected model provider to be nil initially")
	}

	// Set a provider
	mockProv := model.NewMockProvider("test-model")
	svc.SetModelProvider(mockProv)

	if svc.modelProv == nil {
		t.Error("expected model provider to be set")
	}

	if svc.modelProv.Name() != "test-model" {
		t.Errorf("expected provider name 'test-model', got %q", svc.modelProv.Name())
	}
}
