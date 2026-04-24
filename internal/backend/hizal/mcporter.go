package hizal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
)

type mcpCaller interface {
	Call(ctx context.Context, selector string, args []string) ([]byte, error)
}

type cliCaller struct {
	binary string
}

func newCLICaller() mcpCaller {
	return cliCaller{binary: defaultMcporterBinary()}
}

func defaultMcporterBinary() string {
	if _, err := os.Stat("/opt/homebrew/bin/mcporter"); err == nil {
		return "/opt/homebrew/bin/mcporter"
	}
	if path, err := exec.LookPath("mcporter"); err == nil {
		return path
	}
	return "mcporter"
}

func (c cliCaller) Call(ctx context.Context, selector string, args []string) ([]byte, error) {
	argv := append([]string{"call", selector}, args...)
	cmd := exec.CommandContext(ctx, c.binary, argv...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if stderr != "" {
				return nil, fmt.Errorf("mcporter call %s failed: %s", selector, stderr)
			}
		}
		return nil, fmt.Errorf("mcporter call %s failed: %w", selector, err)
	}
	return out, nil
}

type activeSessionResponse struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type remoteSessionResponse struct {
	SessionID      string       `json:"session_id"`
	InjectedChunks []hizalChunk `json:"injected_chunks"`
	Message        string       `json:"message"`
}

type searchContextResponse struct {
	Results []hizalChunk `json:"results"`
}

type readContextResponse hizalChunk

type hizalChunk struct {
	ID         string   `json:"id"`
	Scope      string   `json:"scope"`
	ChunkType  string   `json:"chunk_type"`
	QueryKey   string   `json:"query_key"`
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Related    []string `json:"related"`
	Visibility string   `json:"visibility"`
	CreatedAt  string   `json:"created_at"`
	UpdatedAt  string   `json:"updated_at"`
}

func (s *Store) StartSession(ctx context.Context) (backend.NativeSessionResult, error) {
	active, err := s.getActiveSession(ctx)
	if err != nil {
		return backend.NativeSessionResult{}, err
	}

	var sessionResp remoteSessionResponse
	if active.SessionID != "" && strings.EqualFold(active.Status, "active") {
		if err := s.callJSON(ctx, "hizal.resume_session", []string{"session_id=" + active.SessionID}, &sessionResp); err != nil {
			return backend.NativeSessionResult{}, err
		}
	} else {
		args := []string{"project_id=" + s.projectID}
		if err := s.callJSON(ctx, "hizal.start_session", args, &sessionResp); err != nil {
			return backend.NativeSessionResult{}, err
		}
	}

	artifacts := make([]artifacts.PersistedArtifact, 0, len(sessionResp.InjectedChunks))
	for _, chunk := range sessionResp.InjectedChunks {
		artifacts = append(artifacts, chunk.toArtifact())
	}

	if len(artifacts) > 0 {
		s.mu.Lock()
		for _, a := range artifacts {
			s.chunks[a.PersistedID] = a
		}
		s.mu.Unlock()
	}

	return backend.NativeSessionResult{
		SessionID: sessionResp.SessionID,
		Artifacts: artifacts,
	}, nil
}

func (s *Store) EndSession(ctx context.Context) error {
	// Shared API-key sessions make remote end_session risky here because System-1
	// would close the caller's active Hizal session. Keep System-1 end_session
	// local/no-op until dedicated agent-scoped auth exists.
	s.logger.InfoContext(ctx, "skipping remote hizal end_session for shared session safety")
	return nil
}

func (s *Store) getActiveSession(ctx context.Context) (activeSessionResponse, error) {
	var resp activeSessionResponse
	if err := s.callJSON(ctx, "hizal.get_active_session", nil, &resp); err != nil {
		return activeSessionResponse{}, err
	}
	return resp, nil
}

func (s *Store) callJSON(ctx context.Context, selector string, args []string, out any) error {
	payload, err := s.caller.Call(ctx, selector, args)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode %s response: %w", selector, err)
	}
	return nil
}

func (s *Store) remoteSearch(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
	query = normalizeSemanticQuery(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var resp searchContextResponse
	args := []string{
		"query=" + query,
		fmt.Sprintf("limit=%d", limit),
	}
	if err := s.callJSON(ctx, "hizal.search_context", args, &resp); err != nil {
		return nil, err
	}

	results := make([]artifacts.PersistedArtifact, 0, len(resp.Results))
	for _, chunk := range resp.Results {
		if chunk.ChunkType == "" || chunk.Scope == "" {
			full, err := s.readRemoteChunk(ctx, chunk.ID)
			if err == nil {
				chunk = full
			} else {
				s.logger.WarnContext(ctx, "failed to hydrate search result from hizal", "id", chunk.ID, "error", err)
			}
		}
		results = append(results, chunk.toArtifact())
	}

	if len(results) > 0 {
		s.mu.Lock()
		for _, a := range results {
			s.chunks[a.PersistedID] = a
		}
		s.mu.Unlock()
	}

	return results, nil
}

func (s *Store) readRemoteChunk(ctx context.Context, id string) (hizalChunk, error) {
	var resp readContextResponse
	if err := s.callJSON(ctx, "hizal.read_context", []string{"id=" + id}, &resp); err != nil {
		return hizalChunk{}, err
	}
	return hizalChunk(resp), nil
}

func (s *Store) readRemoteChunkByQueryKey(ctx context.Context, queryKey string) (hizalChunk, error) {
	var resp readContextResponse
	if err := s.callJSON(ctx, "hizal.read_context", []string{"query_key=" + queryKey}, &resp); err != nil {
		return hizalChunk{}, err
	}
	return hizalChunk(resp), nil
}

func (s *Store) readRemoteSystem1Artifact(ctx context.Context, persistedID string) (artifacts.PersistedArtifact, error) {
	for _, artifactType := range s.typeRegistry.Types() {
		queryKey := system1ArtifactQueryKey(artifactType, persistedID)
		chunk, err := s.readRemoteChunkByQueryKey(ctx, queryKey)
		if err != nil {
			if errors.Is(err, backend.ErrNotFound) {
				continue
			}
			return artifacts.PersistedArtifact{}, err
		}
		artifact := chunk.toArtifact()
		artifact.PersistedID = persistedID
		meta := cloneMetadata(artifact.BackendMetadata)
		meta["query_key"] = queryKey
		meta["chunk_id"] = chunk.ID
		artifact.BackendMetadata = meta
		return artifact, nil
	}
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) remoteSave(ctx context.Context, a artifacts.PersistedArtifact) (artifacts.PersistedArtifact, error) {
	selector, args, queryKey, err := s.buildWriteCall(a)
	if err != nil {
		return artifacts.PersistedArtifact{}, err
	}

	var resp hizalChunk
	if err := s.callJSON(ctx, selector, args, &resp); err != nil {
		return artifacts.PersistedArtifact{}, err
	}

	stored := a
	stored.BackendType = string(backend.BackendTypeHizal)
	stored.WriteStatus = "written"
	if writtenAt := parseHizalTime(resp.UpdatedAt); !writtenAt.IsZero() {
		stored.WrittenAt = writtenAt
	}
	if stored.WrittenAt.IsZero() {
		stored.WrittenAt = time.Now().UTC()
	}

	meta := cloneMetadata(a.BackendMetadata)
	meta["store"] = "hizal"
	meta["query_key"] = queryKey
	meta["scope"] = a.Scope
	meta["chunk_type"] = a.ArtifactType
	if resp.ID != "" {
		meta["chunk_id"] = resp.ID
		stored.BackendRef = hizalRef(resp.ID)
	} else {
		stored.BackendRef = "hizal:query_key:" + queryKey
	}
	stored.BackendMetadata = meta
	return stored, nil
}

func (s *Store) buildWriteCall(a artifacts.PersistedArtifact) (string, []string, string, error) {
	queryKey, err := system1QueryKey(a)
	if err != nil {
		return "", nil, "", err
	}
	baseArgs := []string{
		"content=" + a.Body,
		"query_key=" + queryKey,
		"title=" + a.Title,
	}

	scope := strings.ToUpper(a.Scope)
	artifactType := strings.ToUpper(a.ArtifactType)
	switch scope {
	case string(artifacts.ScopeAgent):
		switch artifactType {
		case "MEMORY":
			return "hizal.write_memory", baseArgs, queryKey, nil
		case "IDENTITY":
			return "hizal.write_identity", baseArgs, queryKey, nil
		default:
			return "", nil, "", fmt.Errorf("remote hizal write unsupported for agent scope type %s", artifactType)
		}
	case string(artifacts.ScopeProject):
		projectArgs := append([]string{"project_id=" + s.projectID}, baseArgs...)
		switch artifactType {
		case "KNOWLEDGE":
			return "hizal.write_knowledge", projectArgs, queryKey, nil
		case "CONVENTION":
			return "hizal.write_convention", projectArgs, queryKey, nil
		case "PRINCIPLE":
			return "", nil, "", fmt.Errorf("remote hizal write unsupported for project scope principle artifacts")
		default:
			return "hizal.write_chunk", append(projectArgs, "type="+artifactType), queryKey, nil
		}
	default:
		return "", nil, "", fmt.Errorf("remote hizal write unsupported for scope %s", scope)
	}
}

func system1QueryKey(a artifacts.PersistedArtifact) (string, error) {
	base := strings.TrimSpace(a.PersistedID)
	if base == "" {
		base = strings.TrimSpace(a.CandidateID)
	}
	if base == "" {
		return "", fmt.Errorf("empty deterministic query-key base: both PersistedID and CandidateID empty")
	}
	return system1ArtifactQueryKey(a.ArtifactType, base), nil
}

func system1ArtifactQueryKey(artifactType, base string) string {
	base = strings.ToLower(strings.TrimSpace(base))
	base = strings.ReplaceAll(base, " ", "-")
	return fmt.Sprintf("system1-%s-%s", strings.ToLower(artifactType), base)
}

func normalizeSemanticQuery(query string) string {
	query = strings.ReplaceAll(query, "*", "")
	parts := strings.Fields(query)
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.EqualFold(part, "OR") || strings.EqualFold(part, "AND") {
			continue
		}
		cleaned = append(cleaned, part)
	}
	return strings.TrimSpace(strings.Join(cleaned, " "))
}

func (c hizalChunk) toArtifact() artifacts.PersistedArtifact {
	writtenAt := parseHizalTime(c.UpdatedAt)
	if writtenAt.IsZero() {
		writtenAt = parseHizalTime(c.CreatedAt)
	}
	if writtenAt.IsZero() {
		writtenAt = time.Now().UTC()
	}

	artifactType := c.ChunkType
	if artifactType == "" {
		artifactType = "KNOWLEDGE"
	}
	scope := c.Scope
	if scope == "" {
		scope = string(artifacts.ScopeProject)
	}

	meta := map[string]any{
		"store":      "hizal",
		"chunk_id":   c.ID,
		"query_key":  c.QueryKey,
		"chunk_type": artifactType,
		"scope":      scope,
		"visibility": c.Visibility,
	}
	if len(c.Related) > 0 {
		meta["related"] = c.Related
	}

	return artifacts.PersistedArtifact{
		PersistedID:     c.ID,
		ArtifactType:    artifactType,
		Scope:           scope,
		Title:           c.Title,
		Body:            c.Content,
		Confidence:      artifacts.ConfidenceHigh,
		BackendType:     string(backend.BackendTypeHizal),
		BackendRef:      hizalRef(c.ID),
		WrittenAt:       writtenAt,
		WriteStatus:     "written",
		BackendMetadata: meta,
		Provenance: artifacts.Provenance{
			RawRefs: []string{hizalRef(c.ID)},
		},
	}
}

func hizalRef(id string) string {
	return "hizal:chunk:" + id
}

func parseHizalTime(v string) time.Time {
	if strings.TrimSpace(v) == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339Nano, time.RFC3339}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
