package ingest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
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
)

type QuietPeriodConfig struct {
	Threshold time.Duration
}

type Service struct {
	logger     *slog.Logger
	cfg        config.Config
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
	LastOffset int64     `json:"last_offset"`
	LastEvent  string    `json:"last_event_id"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	return &Service{
		logger:     logger,
		cfg:        cfg,
		cursorPath: filepath.Join(cfg.StateDir, ".ingest_cursor.json"),
		quietConf: QuietPeriodConfig{
			Threshold: 30 * time.Second,
		},
	}
}

func (s *Service) Ingest(ctx context.Context) (*IngestStats, error) {
	stats := &IngestStats{}
	s.lastSpans = nil

	logPath := s.cfg.SessionLogPath
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

	for {
		line, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read line: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		event, err := s.parseEvent(ctx, line)
		if err != nil {
			s.logger.WarnContext(ctx, "skipping malformed event", slog.String("error", err.Error()))
			continue
		}

		stats.EventsProcessed++
		pendingEvents = append(pendingEvents, *event)
		stats.LastEventID = event.EventID
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

func (s *Service) parseEvent(ctx context.Context, line string) (*artifacts.RawEvent, error) {
	var raw json.RawMessage
	event := artifacts.RawEvent{
		Metadata: make(map[string]any),
		RawRef:   line,
	}

	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, err)
	}

	decoder := json.NewDecoder(strings.NewReader(line))
	if err := decoder.Decode(&event); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrParseError, err)
	}

	if event.EventID == "" {
		return nil, fmt.Errorf("%w: missing event_id", ErrParseError)
	}

	return &event, nil
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
