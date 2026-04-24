package backend

import (
	"context"
	"errors"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
)

var (
	ErrNotFound = errors.New("artifact not found")
)

type BackendType string

const (
	BackendTypeFile  BackendType = "file"
	BackendTypeHizal BackendType = "hizal"
)

type Backend interface {
	Save(ctx context.Context, a artifacts.PersistedArtifact) error
	Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error)
	GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error)
	FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error)
	FindByScope(ctx context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error)
	FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error)
	Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error)
	TypeRegistry(ctx context.Context) ([]string, error)
	Close() error
	Type() BackendType
}

// SearchContextRequest configures semantic chunk discovery for backends that
// expose richer context APIs (for example Hizal's search_context tool).
type SearchContextRequest struct {
	Query            string
	Limit            int
	Scope            string
	ChunkType        string
	AlwaysInjectOnly bool
}

// ContextSearchBackend is an optional backend capability for advanced
// introspection retrieval workflows that need semantic discovery + exact reads.
type ContextSearchBackend interface {
	SearchContext(ctx context.Context, req SearchContextRequest) ([]artifacts.PersistedArtifact, error)
	ReadContext(ctx context.Context, id string, queryKey string) (artifacts.PersistedArtifact, error)
}

// MaintenanceBackend is an optional backend capability for in-place memory
// corrections when policy determines a candidate should rectify an existing
// artifact instead of creating a new one.
type MaintenanceBackend interface {
	UpdateExisting(ctx context.Context, existing artifacts.PersistedArtifact, candidate artifacts.CandidateArtifact) (artifacts.PersistedArtifact, error)
}

type NativeSessionResult struct {
	SessionID string
	Artifacts []artifacts.PersistedArtifact
}

type NativeSessionBackend interface {
	StartSession(ctx context.Context) (NativeSessionResult, error)
	EndSession(ctx context.Context) error
}

type TypeRegistry map[string]struct{}

func NewTypeRegistry(types []string) TypeRegistry {
	tr := make(TypeRegistry)
	for _, t := range types {
		tr[t] = struct{}{}
	}
	return tr
}

func (tr TypeRegistry) Has(t string) bool {
	_, ok := tr[t]
	return ok
}

func (tr TypeRegistry) Types() []string {
	types := make([]string, 0, len(tr))
	for t := range tr {
		types = append(types, t)
	}
	return types
}
