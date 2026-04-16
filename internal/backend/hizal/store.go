package hizal

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log/slog"
    "strings"
    "sync"
    "time"

    "github.com/XferOps/system1/internal/artifacts"
    "github.com/XferOps/system1/internal/backend"
)

var ErrNotImplemented = errors.New("not implemented in hizal backend")

type Store struct {
    logger        *slog.Logger
    projectID     string
    typeRegistry  backend.TypeRegistry
    hizalEndpoint string
    // in-memory persistence used to simulate Hizal chunks for tests
    chunks map[string]artifacts.PersistedArtifact
    mu     sync.RWMutex
}

func NewStore(logger *slog.Logger, projectID string, enabledTypes []string) *Store {
    return &Store{
        logger:        logger,
        projectID:     projectID,
        typeRegistry:  backend.NewTypeRegistry(enabledTypes),
        hizalEndpoint: "hizal",
        chunks:        make(map[string]artifacts.PersistedArtifact),
    }
}

func (s *Store) Save(ctx context.Context, a artifacts.PersistedArtifact) error {
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

    // Persist in-memory to simulate Hizal chunk write
    if a.PersistedID == "" {
        return errors.New("persisted_id is required")
    }

    // Ensure in-memory store exists
    s.mu.Lock()
    defer s.mu.Unlock()

    // Make a copy and supplement with a reference and metadata
    stored := a
    if stored.BackendRef == "" {
        stored.BackendRef = "mem://" + a.PersistedID
    }
    if stored.BackendMetadata == nil {
        stored.BackendMetadata = map[string]any{}
    }
    stored.BackendMetadata["store"] = "memory"
    stored.BackendMetadata["chunk_id"] = a.PersistedID

    s.chunks[a.PersistedID] = stored

    s.logger.InfoContext(ctx, "hizal in-memory chunk persisted",
        "persisted_id", a.PersistedID, "chunk_type", chunkType, "content_len", len(content))

    return nil
}

func (s *Store) Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    if a, ok := s.chunks[id]; ok {
        return a, nil
    }
    s.logger.DebugContext(ctx, "hizal get not implemented (cache miss)", "id", id)
    return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    for _, a := range s.chunks {
        if a.CandidateID == candidateID {
            return a, nil
        }
    }
    s.logger.DebugContext(ctx, "hizal get by candidate not implemented", "candidate_id", candidateID)
    return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
    if !s.typeRegistry.Has(artifactType) {
        s.logger.DebugContext(ctx, "type not in registry", "type", artifactType)
        return nil, nil
    }
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

    s.logger.InfoContext(ctx, "hizal search querying context",
        "query", query, "limit", limit)

    s.mu.RLock()
    defer s.mu.RUnlock()
    if query == "" {
        return nil, nil
    }
    var results []artifacts.PersistedArtifact
    for _, a := range s.chunks {
        if strings.Contains(a.Title, query) || strings.Contains(a.Body, query) {
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
		"IDENTITY",
		"MEMORY",
		"CONSTRAINT",
		"CONVENTION",
		"DECISION",
		"KNOWLEDGE",
		"LESSON",
		"PRINCIPLE",
		"RESEARCH",
		"PLAN",
		"IMPLEMENTATION",
		"SPEC",
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
