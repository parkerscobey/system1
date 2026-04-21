package hizal

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
)

type fakeCaller struct {
	responses     map[string]string
	fullResponses map[string]string
	calls         []string
}

func (f *fakeCaller) Call(_ context.Context, selector string, args []string) ([]byte, error) {
	call := selector
	if len(args) > 0 {
		call += " " + strings.Join(args, " ")
	}
	f.calls = append(f.calls, call)
	if resp, ok := f.fullResponses[call]; ok {
		return []byte(resp), nil
	}
	if resp, ok := f.responses[selector]; ok {
		return []byte(resp), nil
	}
	return nil, fmt.Errorf("unexpected selector: %s", call)
}

func newTestStore(t *testing.T, caller mcpCaller) *Store {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := NewStore(logger, "d5bca61a-3f27-4256-bb6b-14654b0fcd3f", []string{"IDENTITY", "MEMORY", "KNOWLEDGE"})
	store.basePath = t.TempDir()
	store.caller = caller
	return store
}

func TestStoreFindByTypePrimesInjectedCache(t *testing.T) {
	caller := &fakeCaller{responses: map[string]string{
		"hizal.get_active_session": `{"session_id":"sess-123","status":"active"}`,
		"hizal.resume_session":     `{"session_id":"sess-123","injected_chunks":[{"id":"chunk-1","scope":"AGENT","chunk_type":"IDENTITY","query_key":"adam_parker_relationship","title":"Adam — Human Context (Parker)","content":"Parker founder context","created_at":"2026-04-03T19:49:27Z","updated_at":"2026-04-11T04:17:07Z"}]}`,
	}}
	store := newTestStore(t, caller)

	results, err := store.FindByType(context.Background(), "IDENTITY")
	if err != nil {
		t.Fatalf("FindByType failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].ArtifactType != "IDENTITY" {
		t.Fatalf("artifact type = %q, want IDENTITY", results[0].ArtifactType)
	}
}

func TestStoreStartSessionUsesInjectedChunks(t *testing.T) {
	caller := &fakeCaller{responses: map[string]string{
		"hizal.get_active_session": `{"session_id":"sess-123","status":"active"}`,
		"hizal.resume_session":     `{"session_id":"sess-123","injected_chunks":[{"id":"chunk-1","scope":"AGENT","chunk_type":"IDENTITY","query_key":"adam_parker_relationship","title":"Adam — Human Context (Parker)","content":"Parker founder context","created_at":"2026-04-03T19:49:27Z","updated_at":"2026-04-11T04:17:07Z"}]}`,
	}}
	store := newTestStore(t, caller)

	result, err := store.StartSession(context.Background())
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if result.SessionID != "sess-123" {
		t.Fatalf("SessionID = %q, want sess-123", result.SessionID)
	}
	if len(result.Artifacts) != 1 {
		t.Fatalf("artifacts len = %d, want 1", len(result.Artifacts))
	}
	if result.Artifacts[0].ArtifactType != "IDENTITY" {
		t.Fatalf("artifact type = %q, want IDENTITY", result.Artifacts[0].ArtifactType)
	}
	if result.Artifacts[0].Title != "Adam — Human Context (Parker)" {
		t.Fatalf("title = %q", result.Artifacts[0].Title)
	}
}

func TestStoreFindByScopePrimesInjectedCache(t *testing.T) {
	caller := &fakeCaller{responses: map[string]string{
		"hizal.get_active_session": `{"session_id":"sess-123","status":"active"}`,
		"hizal.resume_session":     `{"session_id":"sess-123","injected_chunks":[{"id":"chunk-1","scope":"AGENT","chunk_type":"IDENTITY","query_key":"adam_parker_relationship","title":"Adam — Human Context (Parker)","content":"Parker founder context","created_at":"2026-04-03T19:49:27Z","updated_at":"2026-04-11T04:17:07Z"}]}`,
	}}
	store := newTestStore(t, caller)

	results, err := store.FindByScope(context.Background(), artifacts.ScopeAgent)
	if err != nil {
		t.Fatalf("FindByScope failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].Scope != string(artifacts.ScopeAgent) {
		t.Fatalf("scope = %q, want AGENT", results[0].Scope)
	}
}

func TestStoreSearchHydratesReadContext(t *testing.T) {
	caller := &fakeCaller{responses: map[string]string{
		"hizal.search_context": `{"results":[{"id":"chunk-1","scope":"","chunk_type":"","query_key":"adam_parker_relationship","title":"Adam — Human Context (Parker)","content":"stub search content","created_at":"2026-04-03T19:49:27Z","updated_at":"2026-04-11T04:17:07Z"}]}`,
		"hizal.read_context":   `{"id":"chunk-1","scope":"AGENT","chunk_type":"IDENTITY","query_key":"adam_parker_relationship","title":"Adam — Human Context (Parker)","content":"Parker real content","created_at":"2026-04-03T19:49:27Z","updated_at":"2026-04-11T04:17:07Z"}`,
	}}
	store := newTestStore(t, caller)

	results, err := store.Search(context.Background(), "what* OR parker*", 5)
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if results[0].ArtifactType != "IDENTITY" {
		t.Fatalf("artifact type = %q, want IDENTITY", results[0].ArtifactType)
	}
	if results[0].Body != "Parker real content" {
		t.Fatalf("body = %q, want hydrated read_context content", results[0].Body)
	}
	if got := caller.calls[0]; !strings.Contains(got, "query=what parker") {
		t.Fatalf("normalized query missing, first call = %q", got)
	}
}

func TestStoreGetFallsBackToSystem1QueryKey(t *testing.T) {
	caller := &fakeCaller{fullResponses: map[string]string{
		"hizal.read_context query_key=system1-knowledge-art-77": `{"id":"chunk-77","scope":"PROJECT","chunk_type":"KNOWLEDGE","query_key":"system1-knowledge-art-77","title":"Architecture","content":"Remote only","updated_at":"2026-04-21T00:00:00Z"}`,
	}}
	store := newTestStore(t, caller)

	got, err := store.Get(context.Background(), "art-77")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.PersistedID != "art-77" {
		t.Fatalf("PersistedID = %q, want requested persisted id", got.PersistedID)
	}
	if got.BackendMetadata["query_key"] != "system1-knowledge-art-77" {
		t.Fatalf("query_key = %v", got.BackendMetadata["query_key"])
	}
}

func TestStoreRemoteSaveProjectKnowledge(t *testing.T) {
	caller := &fakeCaller{responses: map[string]string{
		"hizal.write_knowledge": `{"id":"chunk-9","scope":"PROJECT","chunk_type":"KNOWLEDGE","query_key":"system1-knowledge-art-9","title":"Architecture","content":"System1 architecture","updated_at":"2026-04-21T00:00:00Z"}`,
	}}
	store := newTestStore(t, caller)

	artifact, err := store.remoteSave(context.Background(), artifacts.PersistedArtifact{
		PersistedID:  "art-9",
		ArtifactType: "KNOWLEDGE",
		Scope:        string(artifacts.ScopeProject),
		Title:        "Architecture",
		Body:         "System1 architecture",
	})
	if err != nil {
		t.Fatalf("remoteSave failed: %v", err)
	}
	if artifact.BackendRef != "hizal:chunk:chunk-9" {
		t.Fatalf("BackendRef = %q, want hizal chunk ref", artifact.BackendRef)
	}
	if artifact.BackendMetadata["query_key"] != "system1-knowledge-art-9" {
		t.Fatalf("query_key = %v", artifact.BackendMetadata["query_key"])
	}
	if got := caller.calls[0]; !strings.Contains(got, "project_id=d5bca61a-3f27-4256-bb6b-14654b0fcd3f") {
		t.Fatalf("project_id missing from call: %q", got)
	}
	if got := caller.calls[0]; !strings.Contains(got, "query_key=system1-knowledge-art-9") {
		t.Fatalf("query_key missing from call: %q", got)
	}
}

func TestStoreSaveFallsBackToLocalMirrorWhenRemoteUnsupported(t *testing.T) {
	caller := &fakeCaller{responses: map[string]string{}}
	store := newTestStore(t, caller)

	err := store.Save(context.Background(), artifacts.PersistedArtifact{
		PersistedID:  "art-1",
		ArtifactType: "PRINCIPLE",
		Scope:        string(artifacts.ScopeProject),
		Title:        "Quality bar",
		Body:         "Keep quality high",
		BackendType:  string(backend.BackendTypeHizal),
		WrittenAt:    time.Now().UTC(),
		WriteStatus:  "created",
	})
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	got, err := store.Get(context.Background(), "art-1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.BackendMetadata["store"] != "file" {
		t.Fatalf("store metadata = %v, want file", got.BackendMetadata["store"])
	}
}

func TestHizalChunkToArtifactDefaults(t *testing.T) {
	artifact := (hizalChunk{ID: "chunk-1", Title: "Untyped", Content: "body"}).toArtifact()
	if artifact.ArtifactType != "KNOWLEDGE" {
		t.Fatalf("ArtifactType = %q, want KNOWLEDGE", artifact.ArtifactType)
	}
	if artifact.Scope != string(artifacts.ScopeProject) {
		t.Fatalf("Scope = %q, want PROJECT", artifact.Scope)
	}
}
