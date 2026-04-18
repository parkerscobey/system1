package extract

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
)

// MockProvider implements Provider for testing
type MockProvider struct {
	name        string
	responses   []model.Response
	errors      []error
	callCount   int
	healthError error
}

func NewMockProvider(name string) *MockProvider {
	return &MockProvider{
		name:      name,
		responses: []model.Response{},
		errors:    []error{},
	}
}

func (m *MockProvider) Name() string {
	return m.name
}

func (m *MockProvider) Health(ctx context.Context) error {
	return m.healthError
}

func (m *MockProvider) Complete(ctx context.Context, prompt string, systemPrompt string, opts ...model.Option) (model.Response, error) {
	defer func() { m.callCount++ }()

	if m.callCount < len(m.errors) && m.errors[m.callCount] != nil {
		return model.Response{}, m.errors[m.callCount]
	}

	if m.callCount < len(m.responses) {
		return m.responses[m.callCount], nil
	}

	return model.Response{
		Text: "mock response",
		Metadata: model.ResponseMetadata{
			Provider: m.name,
			Duration: "100ms",
		},
	}, nil
}

func (m *MockProvider) AddResponse(response model.Response) {
	m.responses = append(m.responses, response)
}

func (m *MockProvider) AddError(err error) {
	m.errors = append(m.errors, err)
}

func (m *MockProvider) CallCount() int {
	return m.callCount
}

// modelExtractionHappyPath returns a valid extraction response
func modelExtractionHappyPath() model.Response {
	structured := modelResponse{
		ArtifactType:  "KNOWLEDGE",
		Scope:         "PROJECT",
		Confidence:    "high",
		Title:         "Project uses WebSocket architecture",
		Body:          "The system uses WebSockets for real-time communication between client and server.",
		ShouldExtract: true,
	}
	data, _ := json.Marshal(structured)
	return model.Response{
		Text:       string(data),
		Structured: data,
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Model:    "gpt-4",
			Duration: "200ms",
		},
	}
}

// modelExtractionAbstention returns a response indicating no extraction should occur
func modelExtractionAbstention() model.Response {
	structured := modelResponse{
		ArtifactType:  "MEMORY",
		Scope:         "AGENT",
		Confidence:    "low",
		Title:         "",
		Body:          "",
		ShouldExtract: false,
	}
	data, _ := json.Marshal(structured)
	return model.Response{
		Text:       string(data),
		Structured: data,
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Duration: "150ms",
		},
	}
}

func TestModelExtractionHappyPath(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"We should use WebSockets for real-time communication in this project."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	mockProvider.AddResponse(modelExtractionHappyPath())

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	c := candidates[0]
	if c.ArtifactType != "KNOWLEDGE" {
		t.Errorf("artifact type = %q, expected KNOWLEDGE", c.ArtifactType)
	}
	if c.ProposedScope != "PROJECT" {
		t.Errorf("scope = %q, expected PROJECT", c.ProposedScope)
	}
	if c.Confidence != "high" {
		t.Errorf("confidence = %q, expected high", c.Confidence)
	}
	if c.Title != "Project uses WebSocket architecture" {
		t.Errorf("title = %q, expected 'Project uses WebSocket architecture'", c.Title)
	}
	if c.Status != artifacts.StatusProposed {
		t.Errorf("status = %q, expected proposed", c.Status)
	}
	if c.Provenance.ExtractionModel != "test-model" {
		t.Errorf("extraction_model = %q, expected test-model", c.Provenance.ExtractionModel)
	}
	if !strings.Contains(c.Body, "WebSockets") {
		t.Errorf("body should contain 'WebSockets', got: %s", c.Body)
	}

	// Verify invariant: provenance carries evidence snippets
	if len(c.Provenance.EvidenceSnippets) == 0 {
		t.Error("invariant violation: provenance must carry evidence snippets")
	}

	// Verify the model was called
	if mockProvider.CallCount() != 1 {
		t.Errorf("expected 1 model call, got %d", mockProvider.CallCount())
	}
}

func TestModelExtractionAbstention(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"ok"}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	mockProvider.AddResponse(modelExtractionAbstention())

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Model abstained, so we should get 0 candidates
	if len(candidates) != 0 {
		t.Fatalf("expected 0 candidates (model abstained), got %d", len(candidates))
	}

	// Verify the model was called
	if mockProvider.CallCount() != 1 {
		t.Errorf("expected 1 model call, got %d", mockProvider.CallCount())
	}
}

func TestModelExtractionFallbackOnError(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	// Content that would trigger heuristic extraction - need 2+ pattern matches for MEMORY
	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I prefer using WebSockets for real-time data. I hate when messages arrive late. I also prefer JSON over XML."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	mockProvider.AddError(errors.New("model unavailable"))

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Fallback to heuristics should produce a candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (fallback to heuristics), got %d", len(candidates))
	}

	c := candidates[0]
	if c.ArtifactType != "MEMORY" {
		t.Errorf("artifact type = %q, expected MEMORY (heuristic detection)", c.ArtifactType)
	}
	if c.Status != artifacts.StatusProposed {
		t.Errorf("status = %q, expected proposed", c.Status)
	}
	// Fallback extraction should not have extraction_model set
	if c.Provenance.ExtractionModel != "" {
		t.Errorf("extraction_model should be empty for fallback, got %q", c.Provenance.ExtractionModel)
	}

	// Verify the model was called
	if mockProvider.CallCount() != 1 {
		t.Errorf("expected 1 model call, got %d", mockProvider.CallCount())
	}
}

func TestModelExtractionFallbackOnBadJSON(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	// Content that would trigger heuristic extraction - need 2+ pattern matches for MEMORY
	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I prefer using WebSockets for real-time data. I hate when messages arrive late. I also prefer JSON over XML."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	// Return invalid JSON
	mockProvider.AddResponse(model.Response{
		Text: "this is not valid json",
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Duration: "100ms",
		},
	})

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Fallback to heuristics should produce a candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (fallback to heuristics), got %d", len(candidates))
	}

	c := candidates[0]
	if c.Status != artifacts.StatusProposed {
		t.Errorf("status = %q, expected proposed", c.Status)
	}
	// Fallback extraction should not have extraction_model set
	if c.Provenance.ExtractionModel != "" {
		t.Errorf("extraction_model should be empty for fallback, got %q", c.Provenance.ExtractionModel)
	}

	// Verify the model was called
	if mockProvider.CallCount() != 1 {
		t.Errorf("expected 1 model call, got %d", mockProvider.CallCount())
	}
}

func TestModelExtractionInvalidArtifactType(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	// Content that would trigger heuristic extraction as fallback - need 2+ pattern matches
	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I prefer using WebSockets for real-time data. I hate when messages arrive late. I also prefer JSON over XML."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	// Return valid JSON but with invalid artifact type
	structured := modelResponse{
		ArtifactType:  "INVALID_TYPE",
		Scope:         "PROJECT",
		Confidence:    "high",
		Title:         "Test",
		Body:          "Test body",
		ShouldExtract: true,
	}
	data, _ := json.Marshal(structured)
	mockProvider.AddResponse(model.Response{
		Text:       string(data),
		Structured: data,
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Duration: "100ms",
		},
	})

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	// Fallback to heuristics should produce a candidate
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate (fallback to heuristics), got %d", len(candidates))
	}
}

func TestModelExtractionWithoutProvider(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	// Content that triggers heuristic extraction - need 2+ pattern matches for MEMORY
	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I prefer using WebSockets for real-time data. I hate when messages arrive late. I also prefer JSON over XML."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	// Service without model provider - should use heuristics
	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}})
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	c := candidates[0]
	if c.ArtifactType != "MEMORY" {
		t.Errorf("artifact type = %q, expected MEMORY", c.ArtifactType)
	}
	// Should not have extraction_model set since no model was used
	if c.Provenance.ExtractionModel != "" {
		t.Errorf("extraction_model should be empty for heuristic extraction, got %q", c.Provenance.ExtractionModel)
	}
}

func TestModelExtractionPreservesInvariants(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I always prefer using WebSockets for real-time data."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	mockProvider.AddResponse(modelExtractionHappyPath())

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	c := candidates[0]

	// Invariant 1: extraction only produces "proposed" status
	if c.Status != artifacts.StatusProposed {
		t.Errorf("invariant 1 violation: expected status 'proposed', got %q", c.Status)
	}

	// Invariant 4: provenance carries evidence snippets
	if len(c.Provenance.EvidenceSnippets) == 0 {
		t.Error("invariant 4 violation: provenance must carry evidence snippets")
	}

	// Invariant 4: provenance references origin
	if len(c.Provenance.SpanIDs) == 0 || c.Provenance.SpanIDs[0] != "span_1" {
		t.Error("invariant 4 violation: provenance must reference span IDs")
	}
	if len(c.Provenance.EventIDs) == 0 || c.Provenance.EventIDs[0] != "evt_1" {
		t.Error("invariant 4 violation: provenance must reference event IDs")
	}
	if len(c.Provenance.SessionIDs) == 0 || c.Provenance.SessionIDs[0] != "sess" {
		t.Error("invariant 4 violation: provenance must reference session IDs")
	}
}

func TestModelExtractionTextResponseFallback(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "session.jsonl")

	line := `{"event_id":"evt_1","source_id":"agent","session_id":"sess","timestamp":"2026-04-15T10:00:01Z","event_type":"message","actor_type":"user","content":"I always prefer using WebSockets for real-time data."}` + "\n"
	if err := os.WriteFile(logPath, []byte(line), 0o644); err != nil {
		t.Fatalf("write session log: %v", err)
	}

	mockProvider := NewMockProvider("test-model")
	// Return valid JSON in Text field (not Structured)
	structured := modelResponse{
		ArtifactType:  "KNOWLEDGE",
		Scope:         "PROJECT",
		Confidence:    "high",
		Title:         "Architecture decision",
		Body:          "WebSockets for real-time communication",
		ShouldExtract: true,
	}
	data, _ := json.Marshal(structured)
	mockProvider.AddResponse(model.Response{
		Text:       string(data),
		Structured: nil, // No structured data
		Metadata: model.ResponseMetadata{
			Provider: "test-model",
			Duration: "100ms",
		},
	})

	svc := NewService(logger, config.Config{EnabledTypes: []string{"MEMORY", "KNOWLEDGE"}}).WithModelProvider(mockProvider)
	span := artifacts.EventSpan{
		SpanID:    "span_1",
		SessionID: "sess",
		SourceID:  "agent",
		EventIDs:  []string{"evt_1"},
		RawRefs:   []string{logPath + ":0"},
	}

	candidates, err := svc.Extract(ctx, span)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}

	c := candidates[0]
	if c.Title != "Architecture decision" {
		t.Errorf("title = %q, expected 'Architecture decision'", c.Title)
	}
}
