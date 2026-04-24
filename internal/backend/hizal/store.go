package hizal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
)

type Store struct {
	logger        *slog.Logger
	projectID     string
	skipRemoteEnd bool
	typeRegistry  backend.TypeRegistry
	hizalEndpoint string
	basePath      string
	chunks        map[string]artifacts.PersistedArtifact
	caller        mcpCaller
	mu            sync.RWMutex
}

func NewStore(logger *slog.Logger, projectID string, enabledTypes []string) *Store {
	if strings.ContainsAny(projectID, `/\\`) || projectID == "." || projectID == ".." {
		projectID = "default"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	bp := filepath.Join(home, ".system1", "hizal", projectID, "chunks")
	return &Store{
		logger:        logger,
		projectID:     projectID,
		skipRemoteEnd: envBool("SYSTEM1_HIZAL_SKIP_END_SESSION"),
		typeRegistry:  backend.NewTypeRegistry(enabledTypes),
		hizalEndpoint: "hizal",
		basePath:      bp,
		chunks:        make(map[string]artifacts.PersistedArtifact),
		caller:        newCLICaller(),
	}
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func (s *Store) Save(ctx context.Context, a artifacts.PersistedArtifact) error {
	if a.PersistedID == "" {
		return errors.New("persisted_id is required")
	}
	if strings.ContainsAny(a.PersistedID, `/\\`) || a.PersistedID == "." || a.PersistedID == ".." {
		return errors.New("persisted_id contains invalid path characters")
	}

	stored := a
	if remote, err := s.remoteSave(ctx, a); err == nil {
		stored = remote
	} else {
		s.logger.WarnContext(ctx, "remote hizal write failed, falling back to local mirror", "persisted_id", a.PersistedID, "error", err)
	}

	stored, chunkPath, err := s.writeLocalMirror(stored)
	if err != nil {
		return err
	}

	s.logger.InfoContext(ctx, "hizal chunk persisted",
		"persisted_id", a.PersistedID,
		"chunk_type", stored.ArtifactType,
		"path", chunkPath,
		"backend_ref", stored.BackendRef)

	return nil
}

func (s *Store) Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
	// Check in-memory cache first
	s.mu.RLock()
	if a, ok := s.chunks[id]; ok {
		s.mu.RUnlock()
		return a, nil
	}
	s.mu.RUnlock()

	if chunk, err := s.readRemoteChunk(ctx, id); err == nil {
		a := chunk.toArtifact()
		s.mu.Lock()
		s.chunks[id] = a
		s.mu.Unlock()
		return a, nil
	}
	if a, err := s.readRemoteSystem1Artifact(ctx, id); err == nil {
		s.mu.Lock()
		s.chunks[id] = a
		s.mu.Unlock()
		return a, nil
	}

	// Load from disk under write lock to prevent duplicate loads
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check after acquiring write lock (another goroutine may have loaded it)
	if a, ok := s.chunks[id]; ok {
		return a, nil
	}

	a, err := s.loadFromDisk(id)
	if err != nil {
		if errors.Is(err, backend.ErrNotFound) {
			return artifacts.PersistedArtifact{}, backend.ErrNotFound
		}
		return artifacts.PersistedArtifact{}, fmt.Errorf("load persisted artifact %q: %w", id, err)
	}
	s.chunks[id] = a
	return a, nil
}

func (s *Store) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
	if err := s.loadAllFromDisk(); err != nil {
		return artifacts.PersistedArtifact{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, a := range s.chunks {
		if a.CandidateID == candidateID {
			return a, nil
		}
	}
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
	if !s.typeRegistry.Has(artifactType) {
		return nil, nil
	}
	if isInjectedLookupType(artifactType) {
		if err := s.ensureInjectedCache(ctx); err != nil {
			s.logger.WarnContext(ctx, "failed to prime injected hizal cache for type lookup", "artifact_type", artifactType, "error", err)
		}
	}
	if err := s.loadTypeFromDisk(artifactType); err != nil {
		return nil, err
	}
	return s.filterCached(func(a artifacts.PersistedArtifact) bool {
		return a.ArtifactType == artifactType
	}), nil
}

func (s *Store) FindByScope(ctx context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	if isInjectedScope(scope) {
		if err := s.ensureInjectedCache(ctx); err != nil {
			s.logger.WarnContext(ctx, "failed to prime injected hizal cache for scope lookup", "scope", scope, "error", err)
		}
	}
	if err := s.loadAllFromDisk(); err != nil {
		return nil, err
	}

	scopeStr := string(scope)
	return s.filterCached(func(a artifacts.PersistedArtifact) bool {
		return a.Scope == scopeStr
	}), nil
}

func (s *Store) FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error) {
	if err := s.ensureInjectedCache(ctx); err != nil {
		s.logger.WarnContext(ctx, "failed to prime injected hizal cache for bounded lookup", "error", err)
	}
	if err := s.loadAllFromDisk(); err != nil {
		return nil, err
	}

	return s.filterCached(func(a artifacts.PersistedArtifact) bool {
		// Half-open interval [since, until)
		return !a.WrittenAt.Before(since) && a.WrittenAt.Before(until)
	}), nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
	return s.SearchContext(ctx, backend.SearchContextRequest{Query: query, Limit: limit})
}

func (s *Store) SearchContext(ctx context.Context, req backend.SearchContextRequest) ([]artifacts.PersistedArtifact, error) {
	query := req.Query
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	results, err := s.remoteSearchWithOptions(ctx, req)
	if err == nil && len(results) > 0 {
		return results, nil
	}
	if err != nil {
		s.logger.WarnContext(ctx, "remote hizal search failed, falling back to local mirror", "error", err)
	}

	return s.localSearch(query, limit)
}

func (s *Store) ReadContext(ctx context.Context, id string, queryKey string) (artifacts.PersistedArtifact, error) {
	if strings.TrimSpace(id) != "" {
		chunk, err := s.readRemoteChunk(ctx, id)
		if err == nil {
			a := chunk.toArtifact()
			s.mu.Lock()
			s.chunks[a.PersistedID] = a
			s.mu.Unlock()
			return a, nil
		}
	}

	if strings.TrimSpace(queryKey) != "" {
		chunk, err := s.readRemoteChunkByQueryKey(ctx, queryKey)
		if err == nil {
			a := chunk.toArtifact()
			s.mu.Lock()
			s.chunks[a.PersistedID] = a
			s.mu.Unlock()
			return a, nil
		}
	}

	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) localSearch(query string, limit int) ([]artifacts.PersistedArtifact, error) {
	if err := s.loadAllFromDisk(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(query)
	var local []artifacts.PersistedArtifact
	for _, a := range s.chunks {
		if strings.Contains(strings.ToLower(a.Title), q) || strings.Contains(strings.ToLower(a.Body), q) {
			local = append(local, a)
			if len(local) >= limit {
				break
			}
		}
	}
	return local, nil
}

func (s *Store) TypeRegistry(ctx context.Context) ([]string, error) {
	chunkTypes := []string{
		"IDENTITY", "MEMORY", "CONSTRAINT", "CONVENTION",
		"DECISION", "KNOWLEDGE", "LESSON", "PRINCIPLE",
		"RESEARCH", "PLAN", "IMPLEMENTATION", "SPEC",
	}
	var enabledTypes []string
	for _, t := range chunkTypes {
		if s.typeRegistry.Has(t) || s.typeRegistry.Has(s.mapChunkToArtifactType(t)) {
			enabledTypes = append(enabledTypes, t)
		}
	}
	return enabledTypes, nil
}

func (s *Store) Close() error {
	return nil
}

func (s *Store) Type() backend.BackendType {
	return backend.BackendTypeHizal
}

func (s *Store) ensureInjectedCache(ctx context.Context) error {
	result, err := s.StartSession(ctx)
	if err != nil {
		return err
	}
	if len(result.Artifacts) == 0 {
		return nil
	}
	return nil
}

func (s *Store) filterCached(fn func(artifacts.PersistedArtifact) bool) []artifacts.PersistedArtifact {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var res []artifacts.PersistedArtifact
	for _, a := range s.chunks {
		if fn(a) {
			res = append(res, a)
		}
	}
	return res
}

func (s *Store) writeLocalMirror(a artifacts.PersistedArtifact) (artifacts.PersistedArtifact, string, error) {
	chunkType := s.mapArtifactTypeToChunk(a.ArtifactType)
	prov := map[string]any{
		"source_ids":                a.Provenance.SourceIDs,
		"session_ids":               a.Provenance.SessionIDs,
		"span_ids":                  a.Provenance.SpanIDs,
		"event_ids":                 a.Provenance.EventIDs,
		"raw_refs":                  a.Provenance.RawRefs,
		"evidence_snippets":         a.Provenance.EvidenceSnippets,
		"extraction_model":          a.Provenance.ExtractionModel,
		"derived_from_artifact_ids": a.Provenance.DerivedFromArtifactIDs,
	}
	if !a.Provenance.ExtractionTime.IsZero() {
		prov["extraction_timestamp"] = a.Provenance.ExtractionTime.Format(time.RFC3339)
	}
	chunkData := map[string]any{
		"artifact_id":      a.PersistedID,
		"artifact_type":    a.ArtifactType,
		"scope":            a.Scope,
		"title":            a.Title,
		"body":             a.Body,
		"confidence":       a.Confidence,
		"candidate_id":     a.CandidateID,
		"backend_type":     a.BackendType,
		"backend_ref":      a.BackendRef,
		"backend_metadata": a.BackendMetadata,
		"written_at":       a.WrittenAt.Format(time.RFC3339),
		"write_status":     a.WriteStatus,
		"provenance":       prov,
	}

	content, err := json.Marshal(chunkData)
	if err != nil {
		return artifacts.PersistedArtifact{}, "", fmt.Errorf("marshal chunk data: %w", err)
	}

	dir := filepath.Join(s.basePath, strings.ToLower(chunkType))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return artifacts.PersistedArtifact{}, "", fmt.Errorf("create chunk dir: %w", err)
	}
	chunkPath := filepath.Join(dir, a.PersistedID+".json")
	if err := os.WriteFile(chunkPath, content, 0600); err != nil {
		return artifacts.PersistedArtifact{}, "", fmt.Errorf("write chunk file: %w", err)
	}

	stored := a
	meta := cloneMetadata(a.BackendMetadata)
	meta["chunk_path"] = chunkPath
	if _, ok := meta["chunk_type"]; !ok {
		meta["chunk_type"] = chunkType
	}
	if _, ok := meta["store"]; !ok {
		meta["store"] = "file"
		stored.BackendRef = chunkPath
	}
	stored.BackendMetadata = meta
	if stored.BackendRef == "" {
		stored.BackendRef = chunkPath
	}

	s.mu.Lock()
	s.chunks[a.PersistedID] = stored
	s.mu.Unlock()

	return stored, chunkPath, nil
}

// --- Disk helpers ---

func (s *Store) loadFromDisk(id string) (artifacts.PersistedArtifact, error) {
	if s.basePath == "" {
		return artifacts.PersistedArtifact{}, backend.ErrNotFound
	}
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifacts.PersistedArtifact{}, backend.ErrNotFound
		}
		return artifacts.PersistedArtifact{}, fmt.Errorf("read chunks dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(s.basePath, entry.Name(), id+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return artifacts.PersistedArtifact{}, fmt.Errorf("read chunk %q: %w", path, err)
		}
		var chunkData map[string]any
		if err := json.Unmarshal(data, &chunkData); err != nil {
			return artifacts.PersistedArtifact{}, fmt.Errorf("decode chunk %q: %w", path, err)
		}
		return chunkDataToArtifact(chunkData, path), nil
	}
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) loadTypeFromDisk(artifactType string) error {
	chunkType := s.mapArtifactTypeToChunk(artifactType)
	dir := filepath.Join(s.basePath, strings.ToLower(chunkType))
	return s.loadDir(dir, artifactType)
}

func (s *Store) loadAllFromDisk() error {
	if s.basePath == "" {
		return nil
	}
	entries, err := os.ReadDir(s.basePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read chunks dir: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(s.basePath, entry.Name())
		if err := s.loadDir(dir, ""); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadDir(dir, filterType string) error {
	files, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read type dir %q: %w", dir, err)
	}
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(f.Name(), ".json")
		// Quick check under read lock to skip already-loaded chunks
		s.mu.RLock()
		_, exists := s.chunks[id]
		s.mu.RUnlock()
		if exists {
			continue
		}
		path := filepath.Join(dir, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("read chunk %q: %w", path, err)
		}
		var chunkData map[string]any
		if err := json.Unmarshal(data, &chunkData); err != nil {
			return fmt.Errorf("decode chunk %q: %w", path, err)
		}
		a := chunkDataToArtifact(chunkData, path)
		if filterType != "" && a.ArtifactType != filterType {
			continue
		}
		// Write lock only for the actual map insert
		s.mu.Lock()
		if _, exists := s.chunks[id]; !exists {
			s.chunks[id] = a
		}
		s.mu.Unlock()
	}
	return nil
}

func chunkDataToArtifact(data map[string]any, path string) artifacts.PersistedArtifact {
	var a artifacts.PersistedArtifact
	a.PersistedID = jsonStr(data, "artifact_id")
	a.ArtifactType = jsonStr(data, "artifact_type")
	a.Scope = jsonStr(data, "scope")
	a.Title = jsonStr(data, "title")
	a.Body = jsonStr(data, "body")
	a.Confidence = jsonStr(data, "confidence")
	a.CandidateID = jsonStr(data, "candidate_id")
	a.BackendType = jsonStr(data, "backend_type")
	a.WriteStatus = jsonStr(data, "write_status")
	if v := jsonStr(data, "written_at"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			a.WrittenAt = t
		}
	}
	a.BackendRef = jsonStr(data, "backend_ref")
	if a.BackendRef == "" {
		a.BackendRef = path
	}
	if prov, ok := data["provenance"].(map[string]any); ok {
		a.Provenance = provenanceFromMap(prov)
	}
	a.BackendMetadata = jsonMap(data, "backend_metadata")
	if a.BackendMetadata == nil {
		a.BackendMetadata = map[string]any{}
	}
	a.BackendMetadata["chunk_path"] = path
	if _, ok := a.BackendMetadata["store"]; !ok {
		a.BackendMetadata["store"] = "file"
	}
	return a
}

func jsonStr(data map[string]any, key string) string {
	if v, ok := data[key].(string); ok {
		return v
	}
	return ""
}

func jsonMap(data map[string]any, key string) map[string]any {
	v, ok := data[key].(map[string]any)
	if !ok {
		return nil
	}
	return cloneMetadata(v)
}

func jsonStrSlice(data map[string]any, key string) []string {
	v, ok := data[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(v))
	for _, item := range v {
		if s, ok := item.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

func cloneMetadata(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func provenanceFromMap(m map[string]any) artifacts.Provenance {
	p := artifacts.Provenance{
		SourceIDs:              jsonStrSlice(m, "source_ids"),
		SessionIDs:             jsonStrSlice(m, "session_ids"),
		SpanIDs:                jsonStrSlice(m, "span_ids"),
		EventIDs:               jsonStrSlice(m, "event_ids"),
		RawRefs:                jsonStrSlice(m, "raw_refs"),
		EvidenceSnippets:       jsonStrSlice(m, "evidence_snippets"),
		ExtractionModel:        jsonStr(m, "extraction_model"),
		DerivedFromArtifactIDs: jsonStrSlice(m, "derived_from_artifact_ids"),
	}
	if v := jsonStr(m, "extraction_timestamp"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			p.ExtractionTime = t
		}
	}
	return p
}

func isInjectedLookupType(artifactType string) bool {
	switch strings.ToUpper(artifactType) {
	case "IDENTITY", "CONVENTION", "PRINCIPLE":
		return true
	default:
		return false
	}
}

func isInjectedScope(scope artifacts.ArtifactScope) bool {
	switch scope {
	case artifacts.ScopeAgent, artifacts.ScopeProject, artifacts.ScopeOrg:
		return true
	default:
		return false
	}
}

func (s *Store) mapArtifactTypeToChunk(artifactType string) string {
	switch artifactType {
	case "IDENTITY":
		return "IDENTITY"
	case "MEMORY":
		return "MEMORY"
	case "KNOWLEDGE":
		return "KNOWLEDGE"
	case "CONVENTION":
		return "CONVENTION"
	case "DECISION":
		return "DECISION"
	case "LESSON":
		return "LESSON"
	case "CONSTRAINT":
		return "CONSTRAINT"
	case "PRINCIPLE":
		return "PRINCIPLE"
	case "RESEARCH":
		return "RESEARCH"
	case "PLAN":
		return "PLAN"
	case "IMPLEMENTATION":
		return "IMPLEMENTATION"
	case "SPEC":
		return "SPEC"
	default:
		return "KNOWLEDGE"
	}
}

func (s *Store) mapChunkToArtifactType(chunkType string) string {
	switch chunkType {
	case "IDENTITY":
		return "IDENTITY"
	case "MEMORY":
		return "MEMORY"
	case "KNOWLEDGE":
		return "KNOWLEDGE"
	case "CONVENTION":
		return "CONVENTION"
	case "DECISION":
		return "DECISION"
	case "LESSON":
		return "LESSON"
	case "CONSTRAINT":
		return "CONSTRAINT"
	case "PRINCIPLE":
		return "PRINCIPLE"
	case "RESEARCH":
		return "RESEARCH"
	case "PLAN":
		return "PLAN"
	case "IMPLEMENTATION":
		return "IMPLEMENTATION"
	case "SPEC":
		return "SPEC"
	default:
		return chunkType
	}
}
