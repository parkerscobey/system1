package artifacts

import "time"

type RawEvent struct {
	EventID   string         `json:"event_id"`
	SourceID  string         `json:"source_id"`
	SessionID string         `json:"session_id"`
	Timestamp time.Time      `json:"timestamp"`
	EventType string         `json:"event_type"`
	ActorType string         `json:"actor_type"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	RawRef    string         `json:"raw_ref"`
}

type EventSpan struct {
	SpanID         string    `json:"span_id"`
	SpanType       string    `json:"span_type"`
	SourceID       string    `json:"source_id"`
	SessionID      string    `json:"session_id"`
	StartEventID   string    `json:"start_event_id"`
	EndEventID     string    `json:"end_event_id"`
	EventIDs       []string  `json:"event_ids"`
	RawRefs        []string  `json:"raw_refs"`
	BoundaryReason string    `json:"boundary_reason"`
	CreatedAt      time.Time `json:"created_at"`
}

type Provenance struct {
	SourceIDs              []string  `json:"source_ids"`
	SessionIDs             []string  `json:"session_ids"`
	SpanIDs                []string  `json:"span_ids"`
	EventIDs               []string  `json:"event_ids"`
	RawRefs                []string  `json:"raw_refs"`
	EvidenceSnippets       []string  `json:"evidence_snippets"`
	ExtractionModel        string    `json:"extraction_model,omitempty"`
	ExtractionTime         time.Time `json:"extraction_timestamp,omitempty"`
	DerivedFromArtifactIDs []string  `json:"derived_from_artifact_ids,omitempty"`
}

type CandidateStatus string

const (
	StatusProposed CandidateStatus = "proposed"
	StatusApproved CandidateStatus = "approved"
	StatusRejected CandidateStatus = "rejected"
	StatusDeferred CandidateStatus = "deferred"
)

type CandidateArtifact struct {
	CandidateID    string          `json:"candidate_id"`
	ArtifactType   string          `json:"artifact_type"`
	ProposedScope  string          `json:"proposed_scope"`
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	Confidence     string          `json:"confidence"`
	Provenance     Provenance      `json:"provenance"`
	Status         CandidateStatus `json:"status"`
	ApprovalReason string          `json:"approval_reason,omitempty"`
	DeferReason    string          `json:"defer_reason,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

func (c CandidateArtifact) GetArtifactType() string   { return c.ArtifactType }
func (c CandidateArtifact) GetScope() string          { return c.ProposedScope }
func (c CandidateArtifact) GetTitle() string          { return c.Title }
func (c CandidateArtifact) GetBody() string           { return c.Body }
func (c CandidateArtifact) GetConfidence() string     { return c.Confidence }
func (c CandidateArtifact) GetProvenance() Provenance { return c.Provenance }

type PersistedArtifact struct {
	PersistedID     string         `json:"persisted_id"`
	ArtifactType    string         `json:"artifact_type"`
	Scope           string         `json:"scope"`
	Title           string         `json:"title"`
	Body            string         `json:"body"`
	Confidence      string         `json:"confidence"`
	Provenance      Provenance     `json:"provenance"`
	CandidateID     string         `json:"candidate_id"`
	BackendType     string         `json:"backend_type"`
	BackendRef      string         `json:"backend_ref"`
	WrittenAt       time.Time      `json:"written_at"`
	WriteStatus     string         `json:"write_status"`
	BackendMetadata map[string]any `json:"backend_metadata,omitempty"`
}

type ArtifactScope string

const (
	ScopeProject ArtifactScope = "PROJECT"
	ScopeAgent   ArtifactScope = "AGENT"
	ScopeOrg     ArtifactScope = "ORG"
)

func (s ArtifactScope) IsValid() bool {
	switch s {
	case ScopeProject, ScopeAgent, ScopeOrg:
		return true
	}
	return false
}
