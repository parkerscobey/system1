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
	CandidateID     string          `json:"candidate_id"`
	ArtifactType    string          `json:"artifact_type"`
	ProposedScope   string          `json:"proposed_scope"`
	Title           string          `json:"title"`
	Body            string          `json:"body"`
	Confidence      string          `json:"confidence"`
	Provenance      Provenance      `json:"provenance"`
	Status          CandidateStatus `json:"status"`
	ApprovalReason  string          `json:"approval_reason,omitempty"`
	RejectionReason string          `json:"rejection_reason,omitempty"`
	DeferReason     string          `json:"defer_reason,omitempty"`
	BackendTarget   string          `json:"backend_target,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type PersistedArtifact struct {
	PersistedID     string         `json:"persisted_id"`
	BackendType     string         `json:"backend_type"`
	BackendRef      string         `json:"backend_ref"`
	CandidateID     string         `json:"candidate_id"`
	WrittenAt       time.Time      `json:"written_at"`
	WriteStatus     string         `json:"write_status"`
	BackendMetadata map[string]any `json:"backend_metadata,omitempty"`
}
