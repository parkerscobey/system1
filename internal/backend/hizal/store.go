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
    typeRegistry  backend.TypeRegistry
    hizalEndpoint string
    basePath      string
    chunks        map[string]artifacts.PersistedArtifact
    mu            sync.RWMutex
}

func NewStore(logger *slog.Logger, projectID string, enabledTypes []string) *Store {
    home, err := os.UserHomeDir()
    if err != nil {
        home = "."
    }
    bp := filepath.Join(home, ".system1", "hizal", projectID, "chunks")
    return &Store{
        logger:        logger,
        projectID:     projectID,
        typeRegistry:  backend.NewTypeRegistry(enabledTypes),
        hizalEndpoint: "hizal",
        basePath:      bp,
        chunks:        make(map[string]artifacts.PersistedArtifact),
    }
}

func (s *Store) Save(ctx context.Context, a artifacts.PersistedArtifact) error {
    if a.PersistedID == "" {
        return errors.New("persisted_id is required")
    }

    chunkType := s.mapArtifactTypeToChunk(a.ArtifactType)
    chunkData := map[string]any{
        "artifact_id":   a.PersistedID,
        "artifact_type": a.ArtifactType,
        "scope":         a.Scope,
        "title":         a.Title,
        "body":          a.Body,
        "confidence":    a.Confidence,
        "candidate_id":  a.CandidateID,
        "backend_type":  a.BackendType,
        "written_at":    a.WrittenAt.Format(time.RFC3339),
        "write_status":  a.WriteStatus,
        "provenance": map[string]any{
            "source_ids":        a.Provenance.SourceIDs,
            "session_ids":       a.Provenance.SessionIDs,
            "span_ids":          a.Provenance.SpanIDs,
            "event_ids":         a.Provenance.EventIDs,
            "raw_refs":          a.Provenance.RawRefs,
            "evidence_snippets": a.Provenance.EvidenceSnippets,
            "extraction_model":  a.Provenance.ExtractionModel,
        },
    }

    content, err := json.Marshal(chunkData)
    if err != nil {
        return fmt.Errorf("marshal chunk data: %w", err)
    }

    // Write to disk: ~/.system1/hizal/<project>/chunks/<type>/<id>.json
    dir := filepath.Join(s.basePath, strings.ToLower(chunkType))
    if err := os.MkdirAll(dir, 0755); err != nil {
        return fmt.Errorf("create chunk dir: %w", err)
    }
    chunkPath := filepath.Join(dir, a.PersistedID+".json")
    if err := os.WriteFile(chunkPath, content, 0644); err != nil {
        return fmt.Errorf("write chunk file: %w", err)
    }

    // Update in-memory index
    s.mu.Lock()
    stored := a
    stored.BackendRef = chunkPath
    if stored.BackendMetadata == nil {
        stored.BackendMetadata = map[string]any{}
    }
    stored.BackendMetadata["store"] = "file"
    stored.BackendMetadata["chunk_path"] = chunkPath
    stored.BackendMetadata["chunk_type"] = chunkType
    s.chunks[a.PersistedID] = stored
    s.mu.Unlock()

    s.logger.InfoContext(ctx, "hizal chunk persisted",
        "persisted_id", a.PersistedID, "chunk_type", chunkType, "path", chunkPath)

    return nil
}

func (s *Store) Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
    s.mu.RLock()
    if a, ok := s.chunks[id]; ok {
        s.mu.RUnlock()
        return a, nil
    }
    s.mu.RUnlock()

    // Try loading from disk
    a, err := s.loadFromDisk(id)
    if err != nil {
        return artifacts.PersistedArtifact{}, backend.ErrNotFound
    }
    s.mu.Lock()
    s.chunks[id] = a
    s.mu.Unlock()
    return a, nil
}

func (s *Store) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
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

    // Rebuild index from disk for this type
    s.loadTypeFromDisk(artifactType)

    s.mu.RLock()
    defer s.mu.RUnlock()
    var res []artifacts.PersistedArtifact
    for _, a := range s.chunks {
        if a.ArtifactType == artifactType {
            res = append(res, a)
        }
    }
    return res, nil
}

func (s *Store) FindByScope(ctx context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    var res []artifacts.PersistedArtifact
    scopeStr := string(scope)
    for _, a := range s.chunks {
        if a.Scope == scopeStr {
            res = append(res, a)
        }
    }
    return res, nil
}

func (s *Store) FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    var res []artifacts.PersistedArtifact
    for _, a := range s.chunks {
        if a.WrittenAt.After(since) && a.WrittenAt.Before(until) {
            res = append(res, a)
        }
    }
    return res, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
    if limit <= 0 {
        limit = 20
    }
    if query == "" {
        return nil, nil
    }

    // Rebuild index from disk
    s.loadAllFromDisk()

    s.mu.RLock()
    defer s.mu.RUnlock()
    q := strings.ToLower(query)
    var results []artifacts.PersistedArtifact
    for _, a := range s.chunks {
        if strings.Contains(strings.ToLower(a.Title), q) || strings.Contains(strings.ToLower(a.Body), q) {
            results = append(results, a)
            if len(results) >= limit {
                break
            }
        }
    }
    return results, nil
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

// --- Disk helpers ---

func (s *Store) loadFromDisk(id string) (artifacts.PersistedArtifact, error) {
    if s.basePath == "" {
        return artifacts.PersistedArtifact{}, backend.ErrNotFound
    }
    entries, err := os.ReadDir(s.basePath)
    if err != nil {
        return artifacts.PersistedArtifact{}, err
    }
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }
        path := filepath.Join(s.basePath, entry.Name(), id+".json")
        data, err := os.ReadFile(path)
        if err != nil {
            continue
        }
        var chunkData map[string]any
        if err := json.Unmarshal(data, &chunkData); err != nil {
            continue
        }
        return chunkDataToArtifact(chunkData, path), nil
    }
    return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) loadTypeFromDisk(artifactType string) {
    chunkType := s.mapArtifactTypeToChunk(artifactType)
    dir := filepath.Join(s.basePath, strings.ToLower(chunkType))
    s.loadDir(dir, artifactType)
}

func (s *Store) loadAllFromDisk() {
    if s.basePath == "" {
        return
    }
    entries, err := os.ReadDir(s.basePath)
    if err != nil {
        return
    }
    for _, entry := range entries {
        if !entry.IsDir() {
            continue
        }
        dir := filepath.Join(s.basePath, entry.Name())
        s.loadDir(dir, "")
    }
}

func (s *Store) loadDir(dir, filterType string) {
    files, err := os.ReadDir(dir)
    if err != nil {
        return
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    for _, f := range files {
        if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
            continue
        }
        id := strings.TrimSuffix(f.Name(), ".json")
        if _, exists := s.chunks[id]; exists {
            continue
        }
        path := filepath.Join(dir, f.Name())
        data, err := os.ReadFile(path)
        if err != nil {
            continue
        }
        var chunkData map[string]any
        if err := json.Unmarshal(data, &chunkData); err != nil {
            continue
        }
        a := chunkDataToArtifact(chunkData, path)
        if filterType != "" && a.ArtifactType != filterType {
            continue
        }
        s.chunks[id] = a
    }
}

func chunkDataToArtifact(data map[string]any, path string) artifacts.PersistedArtifact {
    var a artifacts.PersistedArtifact
    if v, ok := data["artifact_id"].(string); ok {
        a.PersistedID = v
    }
    if v, ok := data["artifact_type"].(string); ok {
        a.ArtifactType = v
    }
    if v, ok := data["scope"].(string); ok {
        a.Scope = v
    }
    if v, ok := data["title"].(string); ok {
        a.Title = v
    }
    if v, ok := data["body"].(string); ok {
        a.Body = v
    }
    if v, ok := data["confidence"].(string); ok {
        a.Confidence = v
    }
    if v, ok := data["candidate_id"].(string); ok {
        a.CandidateID = v
    }
    if v, ok := data["backend_type"].(string); ok {
        a.BackendType = v
    }
    if v, ok := data["write_status"].(string); ok {
        a.WriteStatus = v
    }
    if v, ok := data["written_at"].(string); ok {
        if t, err := time.Parse(time.RFC3339, v); err == nil {
            a.WrittenAt = t
        }
    }
    a.BackendRef = path
    if prov, ok := data["provenance"].(map[string]any); ok {
        a.Provenance = provenanceFromMap(prov)
    }
    a.BackendMetadata = map[string]any{
        "store":      "file",
        "chunk_path": path,
    }
    return a
}

func provenanceFromMap(m map[string]any) artifacts.Provenance {
    var p artifacts.Provenance
    if v, ok := m["source_ids"].([]any); ok {
        for _, s := range v {
            if str, ok := s.(string); ok {
                p.SourceIDs = append(p.SourceIDs, str)
            }
        }
    }
    if v, ok := m["session_ids"].([]any); ok {
        for _, s := range v {
            if str, ok := s.(string); ok {
                p.SessionIDs = append(p.SessionIDs, str)
            }
        }
    }
    if v, ok := m["span_ids"].([]any); ok {
        for _, s := range v {
            if str, ok := s.(string); ok {
                p.SpanIDs = append(p.SpanIDs, str)
            }
        }
    }
    if v, ok := m["event_ids"].([]any); ok {
        for _, s := range v {
            if str, ok := s.(string); ok {
                p.EventIDs = append(p.EventIDs, str)
            }
        }
    }
    if v, ok := m["raw_refs"].([]any); ok {
        for _, s := range v {
            if str, ok := s.(string); ok {
                p.RawRefs = append(p.RawRefs, str)
            }
        }
    }
    if v, ok := m["evidence_snippets"].([]any); ok {
        for _, s := range v {
            if str, ok := s.(string); ok {
                p.EvidenceSnippets = append(p.EvidenceSnippets, str)
            }
        }
    }
    if v, ok := m["extraction_model"].(string); ok {
        p.ExtractionModel = v
    }
    return p
}

func (s *Store) mapArtifactTypeToChunk(artifactType string) string {
    switch artifactType {
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
