package hizal

import (
	"context"
	"log/slog"
	"time"
)

type SessionLifecycle struct {
	logger     *slog.Logger
	projectID  string
	sessionID  string
	isActive   bool
	sessionTTL time.Duration
}

type SessionResult struct {
	SessionID      string
	WakingMind     string
	AmbientContext []string
	ChunkIDs       []string
}

func NewSessionLifecycle(logger *slog.Logger, projectID string) *SessionLifecycle {
	return &SessionLifecycle{
		logger:     logger,
		projectID:  projectID,
		sessionTTL: 30 * time.Minute,
	}
}

func (s *SessionLifecycle) Start(ctx context.Context) (SessionResult, error) {
	s.logger.InfoContext(ctx, "hizal session start initiated",
		"project_id", s.projectID)

	s.sessionID = generateSessionID()
	s.isActive = true

	s.logger.InfoContext(ctx, "hizal session started",
		"session_id", s.sessionID,
		"project_id", s.projectID)

	return SessionResult{
		SessionID:      s.sessionID,
		WakingMind:     "",
		AmbientContext: nil,
		ChunkIDs:       nil,
	}, nil
}

func (s *SessionLifecycle) End(ctx context.Context) error {
	if !s.isActive {
		s.logger.WarnContext(ctx, "attempted to end inactive hizal session")
		return nil
	}

	s.logger.InfoContext(ctx, "hizal session end initiated",
		"session_id", s.sessionID)

	s.isActive = false

	s.logger.InfoContext(ctx, "hizal session ended",
		"session_id", s.sessionID)

	return nil
}

func (s *SessionLifecycle) Resume(ctx context.Context) (SessionResult, error) {
	if !s.isActive {
		s.logger.WarnContext(ctx, "attempted to resume inactive session, starting new")
		return s.Start(ctx)
	}

	s.logger.InfoContext(ctx, "hizal session resumed",
		"session_id", s.sessionID)

	return SessionResult{
		SessionID:      s.sessionID,
		WakingMind:     "",
		AmbientContext: nil,
		ChunkIDs:       nil,
	}, nil
}

func (s *SessionLifecycle) RegisterFocus(ctx context.Context, task string, tags []string) error {
	s.logger.InfoContext(ctx, "registering focus with hizal",
		"session_id", s.sessionID,
		"task", task,
		"tags", tags)

	return nil
}

func (s *SessionLifecycle) IsActive() bool {
	return s.isActive
}

func (s *SessionLifecycle) SessionID() string {
	return s.sessionID
}

func (s *SessionLifecycle) ProjectID() string {
	return s.projectID
}

func generateSessionID() string {
	return "sys1-" + time.Now().Format("20060102-150405")
}
