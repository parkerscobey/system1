package ingest

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
	_ "github.com/mattn/go-sqlite3"
)

var (
	ErrNoProgress = errors.New("no progress made in this ingestion cycle")
	ErrEmptyLog   = errors.New("session log is empty or does not exist")
	ErrParseError = errors.New("failed to parse session log event")
)

const (
	SpanTypeTurn     = "turn"
	SpanTypeSegment  = "segment"
	BoundaryExplicit = "explicit"
	BoundaryQuiet    = "quiet_period"
	BoundaryEOF      = "end_of_log"

	defaultInitialBackfillHours = 24
	defaultMaxEventsPerCycle    = 200
)

type QuietPeriodConfig struct {
	Threshold time.Duration
}

type Service struct {
	logger     *slog.Logger
	cfg        config.Config
	sessionLog string
	sourceKind string
	opencodeDB string
	mirrorPath string

	initialBackfillHours int
	maxEventsPerCycle    int

	cursorPath string
	quietConf  QuietPeriodConfig
	lastSpans  []artifacts.EventSpan
}

type IngestStats struct {
	EventsProcessed int
	SpansBuilt      int
	LastOffset      int64
	LastEventID     string
	CursorSaved     bool
}

type CursorState struct {
	LastOffset           int64     `json:"last_offset"`
	LastEvent            string    `json:"last_event_id"`
	LastOpenCodePartTime int64     `json:"last_opencode_part_time,omitempty"`
	LastOpenCodePartID   string    `json:"last_opencode_part_id,omitempty"`
	UpdatedAt            time.Time `json:"updated_at"`
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	return &Service{
		logger:     logger,
		cfg:        cfg,
		sessionLog: cfg.SessionLogPath,
		sourceKind: "jsonl",
		opencodeDB: "",
		mirrorPath: filepath.Join(cfg.StateDir, ".ingest_opencode_mirror.jsonl"),

		initialBackfillHours: envInt("SYSTEM1_INGEST_INITIAL_BACKFILL_HOURS", defaultInitialBackfillHours),
		maxEventsPerCycle:    envInt("SYSTEM1_INGEST_MAX_EVENTS_PER_CYCLE", defaultMaxEventsPerCycle),

		cursorPath: filepath.Join(cfg.StateDir, ".ingest_cursor.json"),
		quietConf: QuietPeriodConfig{
			Threshold: 30 * time.Second,
		},
	}
}

func (s *Service) Ingest(ctx context.Context) (*IngestStats, error) {
	stats := &IngestStats{}
	s.lastSpans = nil

	if err := s.discoverSessionLogPath(ctx); err != nil {
		s.logger.WarnContext(ctx, "session log discovery failed", "error", err)
	}
	if s.sourceKind == "opencode_sqlite" {
		return s.ingestOpenCodeSQLite(ctx)
	}
	logPath := s.sessionLog
	if _, err := os.Stat(logPath); errors.Is(err, os.ErrNotExist) {
		s.logger.DebugContext(ctx, "session log does not exist yet", slog.String("path", logPath))
		return stats, nil
	}

	file, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("open session log: %w", err)
	}
	defer file.Close()

	cursor, err := s.loadCursor(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to load cursor, starting from beginning", slog.String("error", err.Error()))
		cursor = &CursorState{}
	}

	stat, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat session log: %w", err)
	}

	if stat.Size() == 0 {
		s.logger.DebugContext(ctx, "session log is empty")
		return nil, ErrEmptyLog
	}

	if cursor.LastOffset >= stat.Size() {
		s.logger.DebugContext(ctx, "cursor at or past end of log", slog.Int64("cursor", cursor.LastOffset), slog.Int64("size", stat.Size()))
		return stats, nil
	}

	if cursor.LastOffset > 0 {
		if _, err := file.Seek(cursor.LastOffset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to cursor: %w", err)
		}
	}

	reader := bufio.NewReader(file)
	var pendingEvents []artifacts.RawEvent
	lineStartOffset := cursor.LastOffset

	for {
		offsetBeforeRead := lineStartOffset
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			lineStartOffset, _ = file.Seek(0, io.SeekCurrent)
			lineStartOffset -= int64(reader.Buffered())
			continue
		}

		event, err := s.parseEvent(ctx, line, logPath, offsetBeforeRead)
		if err != nil {
			s.logger.WarnContext(ctx, "skipping malformed event", slog.String("error", err.Error()))
			continue
		}

		stats.EventsProcessed++
		pendingEvents = append(pendingEvents, *event)
		stats.LastEventID = event.EventID

		if stats.EventsProcessed >= s.maxEventsPerCycle {
			s.logger.DebugContext(ctx, "jsonl ingest cycle event cap reached",
				slog.Int("processed", stats.EventsProcessed),
				slog.Int("cap", s.maxEventsPerCycle),
			)
			lineStartOffset, _ = file.Seek(0, io.SeekCurrent)
			lineStartOffset -= int64(reader.Buffered())
			break
		}

		lineStartOffset, _ = file.Seek(0, io.SeekCurrent)
		lineStartOffset -= int64(reader.Buffered())
	}

	if len(pendingEvents) == 0 {
		return stats, ErrEmptyLog
	}

	spans, err := s.buildSpans(ctx, pendingEvents)
	if err != nil {
		return nil, fmt.Errorf("build spans: %w", err)
	}

	s.lastSpans = spans
	stats.SpansBuilt = len(spans)

	lastOffset, err := currentReadOffset(file, reader)
	if err != nil {
		return nil, fmt.Errorf("determine current read offset: %w", err)
	}
	stats.LastOffset = lastOffset

	if len(spans) > 0 {
		lastSpan := spans[len(spans)-1]
		if len(lastSpan.EventIDs) > 0 {
			stats.LastEventID = lastSpan.EventIDs[len(lastSpan.EventIDs)-1]
		}
	}

	newCursor := &CursorState{
		LastOffset: stats.LastOffset,
		LastEvent:  stats.LastEventID,
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.saveCursor(ctx, newCursor); err != nil {
		s.logger.WarnContext(ctx, "failed to save cursor", slog.String("error", err.Error()))
	} else {
		stats.CursorSaved = true
	}

	s.logger.InfoContext(ctx, "ingestion cycle complete",
		slog.Int("events", stats.EventsProcessed),
		slog.Int("spans", stats.SpansBuilt),
		slog.Int64("offset", stats.LastOffset),
	)

	return stats, nil
}

func (s *Service) ingestOpenCodeSQLite(ctx context.Context) (*IngestStats, error) {
	stats := &IngestStats{}
	s.lastSpans = nil

	if !fileExists(s.opencodeDB) {
		s.logger.DebugContext(ctx, "opencode sqlite db does not exist yet", slog.String("path", s.opencodeDB))
		return stats, nil
	}

	cursor, err := s.loadCursor(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "failed to load cursor, starting sqlite ingestion from beginning", slog.String("error", err.Error()))
		cursor = &CursorState{}
	}

	db, err := sql.Open("sqlite3", s.opencodeDB)
	if err != nil {
		return nil, fmt.Errorf("open opencode sqlite db: %w", err)
	}
	defer db.Close()

	minPartTime := int64(0)
	if cursor.LastOpenCodePartTime == 0 && s.initialBackfillHours > 0 {
		minPartTime = time.Now().Add(-time.Duration(s.initialBackfillHours) * time.Hour).UnixMilli()
		s.logger.InfoContext(ctx, "applying initial opencode backfill window",
			slog.Int("hours", s.initialBackfillHours),
			slog.Int64("min_part_time", minPartTime),
		)
	}

	query := `
SELECT p.id, p.message_id, p.session_id, p.time_created, p.data, m.data
FROM part p
LEFT JOIN message m ON m.id = p.message_id
WHERE (p.time_created > ? OR (p.time_created = ? AND p.id > ?))
  AND p.time_created >= ?
ORDER BY p.time_created ASC, p.id ASC
LIMIT ?
`
	rows, err := db.QueryContext(ctx, query,
		cursor.LastOpenCodePartTime,
		cursor.LastOpenCodePartTime,
		cursor.LastOpenCodePartID,
		minPartTime,
		s.maxEventsPerCycle,
	)
	if err != nil {
		return nil, fmt.Errorf("query opencode parts: %w", err)
	}
	defer rows.Close()

	if err := os.MkdirAll(filepath.Dir(s.mirrorPath), 0o755); err != nil {
		return nil, fmt.Errorf("create mirror directory: %w", err)
	}

	mirror, err := os.OpenFile(s.mirrorPath, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open mirror log: %w", err)
	}
	defer mirror.Close()

	var pendingEvents []artifacts.RawEvent
	for rows.Next() {
		var partID, messageID, sessionID string
		var timeCreated int64
		var partData, messageData string
		if err := rows.Scan(&partID, &messageID, &sessionID, &timeCreated, &partData, &messageData); err != nil {
			return nil, fmt.Errorf("scan opencode part: %w", err)
		}

		event, ok := buildEventFromOpenCodePart(partID, messageID, sessionID, timeCreated, partData, messageData)
		if !ok {
			continue
		}

		offset, err := mirror.Seek(0, io.SeekEnd)
		if err != nil {
			return nil, fmt.Errorf("seek mirror end: %w", err)
		}

		line := map[string]any{
			"event_id":   event.EventID,
			"source_id":  event.SourceID,
			"session_id": event.SessionID,
			"timestamp":  event.Timestamp.Format(time.RFC3339Nano),
			"event_type": event.EventType,
			"actor_type": event.ActorType,
			"content":    event.Metadata["content"],
		}
		encoded, err := json.Marshal(line)
		if err != nil {
			return nil, fmt.Errorf("encode mirror event: %w", err)
		}
		if _, err := mirror.Write(append(encoded, '\n')); err != nil {
			return nil, fmt.Errorf("write mirror event: %w", err)
		}

		event.RawRef = fmt.Sprintf("%s:%d", s.mirrorPath, offset)
		pendingEvents = append(pendingEvents, event)
		stats.EventsProcessed++
		stats.LastEventID = event.EventID
		cursor.LastOpenCodePartTime = timeCreated
		cursor.LastOpenCodePartID = partID
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate opencode parts: %w", err)
	}

	if len(pendingEvents) == 0 {
		return stats, nil
	}

	spans, err := s.buildSpans(ctx, pendingEvents)
	if err != nil {
		return nil, fmt.Errorf("build spans from opencode parts: %w", err)
	}

	s.lastSpans = spans
	stats.SpansBuilt = len(spans)

	newCursor := &CursorState{
		LastOffset:           cursor.LastOffset,
		LastEvent:            stats.LastEventID,
		LastOpenCodePartTime: cursor.LastOpenCodePartTime,
		LastOpenCodePartID:   cursor.LastOpenCodePartID,
		UpdatedAt:            time.Now().UTC(),
	}
	if err := s.saveCursor(ctx, newCursor); err != nil {
		s.logger.WarnContext(ctx, "failed to save cursor", slog.String("error", err.Error()))
	} else {
		stats.CursorSaved = true
	}

	s.logger.InfoContext(ctx, "opencode sqlite ingestion cycle complete",
		slog.Int("events", stats.EventsProcessed),
		slog.Int("spans", stats.SpansBuilt),
		slog.String("db", s.opencodeDB),
	)

	return stats, nil
}

func buildEventFromOpenCodePart(partID, messageID, sessionID string, timeCreated int64, partData string, messageData string) (artifacts.RawEvent, bool) {
	var part map[string]any
	if err := json.Unmarshal([]byte(partData), &part); err != nil {
		return artifacts.RawEvent{}, false
	}
	if t, _ := part["type"].(string); t != "text" {
		return artifacts.RawEvent{}, false
	}
	text, _ := part["text"].(string)
	if strings.TrimSpace(text) == "" {
		return artifacts.RawEvent{}, false
	}

	actor := "agent"
	if messageData != "" {
		var msg map[string]any
		if err := json.Unmarshal([]byte(messageData), &msg); err == nil {
			if role, ok := msg["role"].(string); ok && strings.TrimSpace(role) != "" {
				actor = strings.TrimSpace(role)
			}
		}
	}

	ts := time.UnixMilli(timeCreated).UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}

	return artifacts.RawEvent{
		EventID:   "opencode_part_" + partID,
		SourceID:  "opencode",
		SessionID: sessionID,
		Timestamp: ts,
		EventType: "message",
		ActorType: actor,
		Metadata: map[string]any{
			"part_id":    partID,
			"message_id": messageID,
			"content":    text,
		},
	}, true
}

func (s *Service) SetSessionLogPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	s.sessionLog = path
}

func (s *Service) discoverSessionLogPath(ctx context.Context) error {
	if strings.TrimSpace(os.Getenv("SYSTEM1_SESSION_LOG_PATH")) != "" {
		return nil
	}

	if fileExists(s.sessionLog) {
		s.sourceKind = "jsonl"
		return nil
	}

	opencodePath, err := discoverOpenCodeSessionLog()
	if err != nil {
		return err
	}
	if opencodePath != "" {
		s.sourceKind = "jsonl"
		s.sessionLog = opencodePath
		s.logger.InfoContext(ctx, "auto-discovered opencode session log", slog.String("path", opencodePath))
		return nil
	}

	opencodeDB := discoverOpenCodeDBPath()
	if opencodeDB != "" {
		s.sourceKind = "opencode_sqlite"
		s.opencodeDB = opencodeDB
		s.logger.InfoContext(ctx, "auto-discovered opencode sqlite session source", slog.String("path", opencodeDB))
	}

	return nil
}

func discoverOpenCodeSessionLog() (string, error) {
	if _, err := exec.LookPath("opencode"); err != nil {
		return "", nil
	}

	if commandPath := probeOpenCodePathFromCommands(); fileExists(commandPath) {
		return commandPath, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}

	candidates := []string{
		filepath.Join(home, ".opencode", "sessions.jsonl"),
		filepath.Join(home, ".opencode", "session.jsonl"),
		filepath.Join(home, ".local", "share", "opencode", "sessions.jsonl"),
		filepath.Join(home, "Library", "Application Support", "opencode", "sessions.jsonl"),
	}

	for _, path := range candidates {
		if fileExists(path) {
			return path, nil
		}
	}

	searchRoots := []string{
		filepath.Join(home, ".opencode"),
		filepath.Join(home, ".local", "share", "opencode"),
		filepath.Join(home, "Library", "Application Support", "opencode"),
	}

	type candidate struct {
		path    string
		modTime time.Time
	}
	found := make([]candidate, 0, 4)

	for _, root := range searchRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := strings.ToLower(entry.Name())
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			if !strings.Contains(name, "session") && !strings.Contains(name, "log") {
				continue
			}
			fullPath := filepath.Join(root, entry.Name())
			stat, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			if stat.Size() == 0 {
				continue
			}
			found = append(found, candidate{path: fullPath, modTime: stat.ModTime()})
		}
	}

	if len(found) == 0 {
		return "", nil
	}

	sort.Slice(found, func(i, j int) bool {
		return found[i].modTime.After(found[j].modTime)
	})

	return found[0].path, nil
}

func discoverOpenCodeDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	candidates := []string{
		filepath.Join(home, ".local", "share", "opencode", "opencode.db"),
		filepath.Join(home, ".opencode", "opencode.db"),
	}
	for _, path := range candidates {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func probeOpenCodePathFromCommands() string {
	commands := [][]string{
		{"config", "get", "sessionLogPath"},
		{"config", "get", "session_log_path"},
		{"paths", "--json"},
	}

	for _, args := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		cmd := exec.CommandContext(ctx, "opencode", args...)
		out, err := cmd.Output()
		cancel()
		if err != nil {
			continue
		}
		candidate := parseOpenCodePathOutput(string(out))
		if fileExists(candidate) {
			return candidate
		}
	}

	return ""
}

func parseOpenCodePathOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}

	if fileExists(output) {
		return output
	}

	var m map[string]any
	if err := json.Unmarshal([]byte(output), &m); err != nil {
		return ""
	}

	for _, key := range []string{"session_log_path", "sessionLogPath", "log_path", "logPath", "sessions"} {
		if v, ok := m[key].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !stat.IsDir()
}

func (s *Service) parseEvent(ctx context.Context, line string, logPath string, offset int64) (*artifacts.RawEvent, error) {
	var raw json.RawMessage
	event := artifacts.RawEvent{
		Metadata: make(map[string]any),
		RawRef:   fmt.Sprintf("%s:%d", logPath, offset),
	}

	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, err)
	}

	decoder := json.NewDecoder(strings.NewReader(line))
	if err := decoder.Decode(&event); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, err)
	}

	if event.EventID == "" {
		event = normalizeRawEvent(raw, event)
	}

	if event.EventID == "" {
		return nil, fmt.Errorf("%w: missing event_id", ErrParseError)
	}

	return &event, nil
}

func normalizeRawEvent(raw json.RawMessage, base artifacts.RawEvent) artifacts.RawEvent {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return base
	}

	if base.EventID == "" {
		base.EventID = firstString(m, "event_id", "id", "uuid")
	}
	if base.SourceID == "" {
		base.SourceID = firstString(m, "source_id", "source", "agent", "app")
	}
	if base.SessionID == "" {
		base.SessionID = firstString(m, "session_id", "session", "conversation_id", "thread_id")
	}
	if base.EventType == "" {
		base.EventType = firstString(m, "event_type", "type", "kind")
	}
	if base.ActorType == "" {
		base.ActorType = firstString(m, "actor_type", "role", "actor", "sender")
	}
	if base.Timestamp.IsZero() {
		if ts := firstString(m, "timestamp", "time", "created_at"); ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				base.Timestamp = parsed
			}
		}
	}
	if base.Timestamp.IsZero() {
		base.Timestamp = time.Now().UTC()
	}

	if base.SourceID == "" {
		base.SourceID = "unknown_source"
	}
	if base.SessionID == "" {
		base.SessionID = "unknown_session"
	}
	if base.EventType == "" {
		base.EventType = "message"
	}
	if base.ActorType == "" {
		base.ActorType = "agent"
	}

	if base.Metadata == nil {
		base.Metadata = make(map[string]any)
	}

	return base
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return strings.TrimSpace(t)
				}
			case float64:
				return strings.TrimSpace(fmt.Sprintf("%.0f", t))
			}
		}
	}
	return ""
}

func (s *Service) buildSpans(ctx context.Context, events []artifacts.RawEvent) ([]artifacts.EventSpan, error) {
	if len(events) == 0 {
		return nil, nil
	}

	var spans []artifacts.EventSpan
	var currentEvents []artifacts.RawEvent
	var currentRefs []string

	for i, event := range events {
		currentEvents = append(currentEvents, event)
		currentRefs = append(currentRefs, event.RawRef)

		isLastEvent := i == len(events)-1
		hasExplicitBoundary := s.hasExplicitBoundary(event)

		if !isLastEvent && !hasExplicitBoundary {
			nextEvent := events[i+1]
			quietPeriod := nextEvent.Timestamp.Sub(event.Timestamp)

			if quietPeriod < s.quietConf.Threshold {
				continue
			}

			span := s.createSpan(currentEvents, currentRefs, BoundaryQuiet)
			spans = append(spans, span)
			currentEvents = nil
			currentRefs = nil
			continue
		}

		reason := BoundaryEOF
		if hasExplicitBoundary {
			reason = BoundaryExplicit
		} else if isLastEvent {
			reason = BoundaryEOF
		}

		span := s.createSpan(currentEvents, currentRefs, reason)
		spans = append(spans, span)
		currentEvents = nil
		currentRefs = nil
	}

	return spans, nil
}

func (s *Service) hasExplicitBoundary(event artifacts.RawEvent) bool {
	if event.Metadata == nil {
		return false
	}

	if boundary, ok := event.Metadata["turn_boundary"]; ok {
		if b, ok := boundary.(bool); ok && b {
			return true
		}
		if b, ok := boundary.(string); ok && strings.ToLower(b) == "true" {
			return true
		}
	}

	if eventType, ok := event.Metadata["event_type"].(string); ok {
		et := strings.ToLower(eventType)
		if et == "user_turn_end" || et == "agent_turn_end" || et == "turn_complete" {
			return true
		}
	}

	return false
}

func (s *Service) createSpan(events []artifacts.RawEvent, refs []string, reason string) artifacts.EventSpan {
	if len(events) == 0 {
		return artifacts.EventSpan{}
	}

	first := events[0]
	last := events[len(events)-1]

	eventIDs := make([]string, len(events))
	for i, e := range events {
		eventIDs[i] = e.EventID
	}

	spanType := SpanTypeTurn
	if len(events) > 1 {
		spanType = SpanTypeSegment
	}

	return artifacts.EventSpan{
		SpanID:         generateSpanID(),
		SpanType:       spanType,
		SourceID:       first.SourceID,
		SessionID:      first.SessionID,
		StartEventID:   first.EventID,
		EndEventID:     last.EventID,
		EventIDs:       eventIDs,
		RawRefs:        refs,
		BoundaryReason: reason,
		CreatedAt:      time.Now().UTC(),
	}
}

func (s *Service) loadCursor(ctx context.Context) (*CursorState, error) {
	data, err := os.ReadFile(s.cursorPath)
	if errors.Is(err, os.ErrNotExist) {
		return &CursorState{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cursor: %w", err)
	}

	var cursor CursorState
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, fmt.Errorf("parse cursor: %w", err)
	}

	return &cursor, nil
}

func (s *Service) saveCursor(ctx context.Context, cursor *CursorState) error {
	data, err := json.Marshal(cursor)
	if err != nil {
		return fmt.Errorf("marshal cursor: %w", err)
	}

	dir := filepath.Dir(s.cursorPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("mkdir for cursor: %w", err)
	}

	if err := os.WriteFile(s.cursorPath, data, 0600); err != nil {
		return fmt.Errorf("write cursor: %w", err)
	}

	return nil
}

func (s *Service) GetStatus(ctx context.Context) (*CursorState, error) {
	return s.loadCursor(ctx)
}

func (s *Service) GetSpans() []artifacts.EventSpan {
	return s.lastSpans
}

func currentReadOffset(file *os.File, reader *bufio.Reader) (int64, error) {
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}

	return offset - int64(reader.Buffered()), nil
}

func generateSpanID() string {
	return fmt.Sprintf("span_%d", time.Now().UnixNano())
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
