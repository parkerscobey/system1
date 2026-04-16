package hizal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
}

func NewStore(logger *slog.Logger, projectID string, enabledTypes []string) *Store {
	return &Store{
		logger:        logger,
		projectID:     projectID,
		typeRegistry:  backend.NewTypeRegistry(enabledTypes),
		hizalEndpoint: "hizal",
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

	s.logger.InfoContext(ctx, "hizal save not implemented - using file backend for persistence",
		"persisted_id", a.PersistedID, "chunk_type", chunkType, "content_len", len(content))

	_ = content

	return nil
}

func (s *Store) Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
	s.logger.DebugContext(ctx, "hizal get not implemented", "id", id)
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
	s.logger.DebugContext(ctx, "hizal get by candidate not implemented", "candidate_id", candidateID)
	return artifacts.PersistedArtifact{}, backend.ErrNotFound
}

func (s *Store) FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
	if !s.typeRegistry.Has(artifactType) {
		s.logger.DebugContext(ctx, "type not in registry", "type", artifactType)
		return nil, nil
	}

	chunkType := s.mapArtifactTypeToChunk(artifactType)
	s.logger.InfoContext(ctx, "hizal find by type searching chunks",
		"artifact_type", artifactType, "chunk_type", chunkType)

	return nil, nil
}

func (s *Store) FindByScope(ctx context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	s.logger.DebugContext(ctx, "hizal find by scope not implemented", "scope", scope)
	return nil, nil
}

func (s *Store) FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error) {
	s.logger.DebugContext(ctx, "hizal find bounded not implemented")
	return nil, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
	if limit <= 0 {
		limit = 20
	}

	s.logger.InfoContext(ctx, "hizal search querying context",
		"query", query, "limit", limit)

	return nil, nil
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
