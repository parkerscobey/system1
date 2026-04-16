package file

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrAlreadyExists = errors.New("artifact already exists")
)

const schema = `
CREATE TABLE IF NOT EXISTS artifacts (
	persisted_id TEXT PRIMARY KEY,
	artifact_type TEXT NOT NULL,
	scope TEXT NOT NULL,
	title TEXT NOT NULL,
	body TEXT NOT NULL,
	confidence TEXT NOT NULL,
	candidate_id TEXT NOT NULL,
	backend_type TEXT NOT NULL,
	backend_ref TEXT NOT NULL,
	written_at INTEGER NOT NULL,
	write_status TEXT NOT NULL,
	evidence_snippets TEXT,
	source_ids TEXT,
	session_ids TEXT,
	span_ids TEXT,
	event_ids TEXT,
	raw_refs TEXT,
	extraction_model TEXT,
	derived_from_artifact_ids TEXT
);

CREATE INDEX IF NOT EXISTS idx_artifacts_type ON artifacts(artifact_type);
CREATE INDEX IF NOT EXISTS idx_artifacts_scope ON artifacts(scope);
CREATE INDEX IF NOT EXISTS idx_artifacts_written_at ON artifacts(written_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_candidate_id ON artifacts(candidate_id);

CREATE VIRTUAL TABLE IF NOT EXISTS artifacts_fts USING fts5(
	persisted_id,
	title,
	body,
	content='artifacts',
	content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS artifacts_ai AFTER INSERT ON artifacts BEGIN
	INSERT INTO artifacts_fts(rowid, persisted_id, title, body)
	VALUES (new.rowid, new.persisted_id, new.title, new.body);
END;

CREATE TRIGGER IF NOT EXISTS artifacts_ad AFTER DELETE ON artifacts BEGIN
	INSERT INTO artifacts_fts(artifacts_fts, rowid, persisted_id, title, body)
	VALUES ('delete', old.rowid, old.persisted_id, old.title, old.body);
END;

CREATE TRIGGER IF NOT EXISTS artifacts_au AFTER UPDATE ON artifacts BEGIN
	INSERT INTO artifacts_fts(artifacts_fts, rowid, persisted_id, title, body)
	VALUES ('delete', old.rowid, old.persisted_id, old.title, old.body);
	INSERT INTO artifacts_fts(rowid, persisted_id, title, body)
	VALUES (new.rowid, new.persisted_id, new.title, new.body);
END;

CREATE TABLE IF NOT EXISTS cursors (
	cursor_id TEXT PRIMARY KEY,
	cursor_data TEXT NOT NULL,
	updated_at INTEGER NOT NULL
);
`

type DB struct {
	*sql.DB
}

type Store struct {
	logger *slog.Logger
	cfg    config.Config
	db     *DB
}

func NewStore(logger *slog.Logger, cfg config.Config) (*Store, error) {
	db, err := initDB(cfg.SQLitePath, logger)
	if err != nil {
		return nil, err
	}
	return &Store{logger: logger, cfg: cfg, db: db}, nil
}

func initDB(dbPath string, logger *slog.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		logger.Error("failed to ensure schema", "error", err)
		return nil, err
	}
	logger.Info("schema initialized")
	return &DB{db}, nil
}

func (s *Store) Save(ctx context.Context, a artifacts.PersistedArtifact) error {
	if a.PersistedID == "" {
		return errors.New("persisted_id is required")
	}
	exists, err := s.db.Exists(ctx, a.PersistedID)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyExists
	}
	scopeDir := filepath.Join(s.cfg.ArtifactsDir, strings.ToLower(a.Scope))
	if err := os.MkdirAll(scopeDir, 0755); err != nil {
		return fmt.Errorf("create artifacts dir: %w", err)
	}
	jsonPath := filepath.Join(scopeDir, a.PersistedID+".json")
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal artifact: %w", err)
	}
	if err := os.WriteFile(jsonPath, data, 0644); err != nil {
		return fmt.Errorf("write artifact file: %w", err)
	}
	return s.db.InsertArtifact(ctx, a, jsonPath)
}

func (s *Store) Get(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
	if id == "" {
		return artifacts.PersistedArtifact{}, errors.New("id is required")
	}
	return s.db.GetArtifact(ctx, id)
}

func (s *Store) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
	if candidateID == "" {
		return artifacts.PersistedArtifact{}, errors.New("candidate_id is required")
	}
	return s.db.GetByCandidate(ctx, candidateID)
}

func (s *Store) FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
	return s.db.FindByType(ctx, artifactType)
}

func (s *Store) FindByScope(ctx context.Context, scope artifacts.ArtifactScope) ([]artifacts.PersistedArtifact, error) {
	return s.db.FindByScope(ctx, string(scope))
}

func (s *Store) FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error) {
	return s.db.FindBounded(ctx, since, until)
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	return s.db.Search(ctx, query, limit)
}

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *Store) Type() backend.BackendType {
	return backend.BackendTypeFile
}

func (s *Store) TypeRegistry(ctx context.Context) ([]string, error) {
	return s.cfg.EnabledTypes, nil
}

func (db *DB) Exists(ctx context.Context, id string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifacts WHERE persisted_id = ?", id).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (db *DB) InsertArtifact(ctx context.Context, a artifacts.PersistedArtifact, jsonPath string) error {
	evidence, _ := json.Marshal(a.Provenance.EvidenceSnippets)
	sources, _ := json.Marshal(a.Provenance.SourceIDs)
	sessions, _ := json.Marshal(a.Provenance.SessionIDs)
	spans, _ := json.Marshal(a.Provenance.SpanIDs)
	events, _ := json.Marshal(a.Provenance.EventIDs)
	rawRefs, _ := json.Marshal(a.Provenance.RawRefs)
	derived, _ := json.Marshal(a.Provenance.DerivedFromArtifactIDs)
	_, err := db.ExecContext(ctx, `
		INSERT INTO artifacts (
			persisted_id, artifact_type, scope, title, body, confidence,
			candidate_id, backend_type, backend_ref, written_at, write_status,
			evidence_snippets, source_ids, session_ids, span_ids, event_ids, raw_refs,
			extraction_model, derived_from_artifact_ids
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.PersistedID, a.ArtifactType, a.Scope, a.Title, a.Body, a.Confidence,
		a.CandidateID, a.BackendType, jsonPath, a.WrittenAt.Unix(), a.WriteStatus,
		string(evidence), string(sources), string(sessions), string(spans), string(events), string(rawRefs),
		a.Provenance.ExtractionModel, string(derived),
	)
	return err
}

func (db *DB) GetArtifact(ctx context.Context, id string) (artifacts.PersistedArtifact, error) {
	var a artifacts.PersistedArtifact
	var writtenAt int64
	err := db.QueryRowContext(ctx, `
		SELECT persisted_id, artifact_type, scope, title, body, confidence,
			candidate_id, backend_type, backend_ref, written_at, write_status
		FROM artifacts WHERE persisted_id = ?`, id).Scan(
		&a.PersistedID, &a.ArtifactType, &a.Scope, &a.Title, &a.Body, &a.Confidence,
		&a.CandidateID, &a.BackendType, &a.BackendRef, &writtenAt, &a.WriteStatus,
	)
	if err == sql.ErrNoRows {
		return artifacts.PersistedArtifact{}, fmt.Errorf("artifact %q not found: %w", id, backend.ErrNotFound)
	}
	if err != nil {
		return artifacts.PersistedArtifact{}, err
	}
	a.WrittenAt = time.Unix(writtenAt, 0)
	prov, err := db.getProvenance(ctx, id)
	if err == nil {
		a.Provenance = prov
	}
	return a, nil
}

func (db *DB) GetByCandidate(ctx context.Context, candidateID string) (artifacts.PersistedArtifact, error) {
	var a artifacts.PersistedArtifact
	var writtenAt int64
	err := db.QueryRowContext(ctx, `
		SELECT persisted_id, artifact_type, scope, title, body, confidence,
			candidate_id, backend_type, backend_ref, written_at, write_status
		FROM artifacts WHERE candidate_id = ?`, candidateID).Scan(
		&a.PersistedID, &a.ArtifactType, &a.Scope, &a.Title, &a.Body, &a.Confidence,
		&a.CandidateID, &a.BackendType, &a.BackendRef, &writtenAt, &a.WriteStatus,
	)
	if err == sql.ErrNoRows {
		return artifacts.PersistedArtifact{}, fmt.Errorf("artifact for candidate %q not found: %w", candidateID, backend.ErrNotFound)
	}
	if err != nil {
		return artifacts.PersistedArtifact{}, err
	}
	a.WrittenAt = time.Unix(writtenAt, 0)
	prov, err := db.getProvenance(ctx, a.PersistedID)
	if err == nil {
		a.Provenance = prov
	}
	return a, nil
}

func (db *DB) getProvenance(ctx context.Context, id string) (artifacts.Provenance, error) {
	var prov artifacts.Provenance
	var evidence, sources, sessions, spans, events, rawRefs, derived string
	err := db.QueryRowContext(ctx, `
		SELECT evidence_snippets, source_ids, session_ids, span_ids, event_ids, raw_refs, derived_from_artifact_ids
		FROM artifacts WHERE persisted_id = ?`, id).Scan(
		&evidence, &sources, &sessions, &spans, &events, &rawRefs, &derived,
	)
	if err != nil {
		return prov, err
	}
	json.Unmarshal([]byte(evidence), &prov.EvidenceSnippets)
	json.Unmarshal([]byte(sources), &prov.SourceIDs)
	json.Unmarshal([]byte(sessions), &prov.SessionIDs)
	json.Unmarshal([]byte(spans), &prov.SpanIDs)
	json.Unmarshal([]byte(events), &prov.EventIDs)
	json.Unmarshal([]byte(rawRefs), &prov.RawRefs)
	json.Unmarshal([]byte(derived), &prov.DerivedFromArtifactIDs)
	return prov, nil
}

func (db *DB) FindByType(ctx context.Context, artifactType string) ([]artifacts.PersistedArtifact, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT persisted_id, artifact_type, scope, title, body, confidence, candidate_id, written_at
		FROM artifacts WHERE artifact_type = ? ORDER BY written_at DESC`, artifactType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func (db *DB) FindByScope(ctx context.Context, scope string) ([]artifacts.PersistedArtifact, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT persisted_id, artifact_type, scope, title, body, confidence, candidate_id, written_at
		FROM artifacts WHERE scope = ? ORDER BY written_at DESC`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func (db *DB) FindBounded(ctx context.Context, since, until time.Time) ([]artifacts.PersistedArtifact, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT persisted_id, artifact_type, scope, title, body, confidence, candidate_id, written_at
		FROM artifacts WHERE written_at >= ? AND written_at <= ? ORDER BY written_at DESC`,
		since.Unix(), until.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func (db *DB) Search(ctx context.Context, query string, limit int) ([]artifacts.PersistedArtifact, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT a.persisted_id, a.artifact_type, a.scope, a.title, a.body, a.confidence, a.candidate_id, a.written_at
		FROM artifacts a
		JOIN artifacts_fts fts ON a.rowid = fts.rowid
		WHERE artifacts_fts MATCH ?
		ORDER BY rank
		LIMIT ?`, query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanArtifacts(rows)
}

func scanArtifacts(rows *sql.Rows) ([]artifacts.PersistedArtifact, error) {
	var results []artifacts.PersistedArtifact
	for rows.Next() {
		var a artifacts.PersistedArtifact
		var writtenAt int64
		if err := rows.Scan(&a.PersistedID, &a.ArtifactType, &a.Scope, &a.Title, &a.Body, &a.Confidence, &a.CandidateID, &writtenAt); err != nil {
			return nil, err
		}
		a.WrittenAt = time.Unix(writtenAt, 0)
		results = append(results, a)
	}
	return results, rows.Err()
}
