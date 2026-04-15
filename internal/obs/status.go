package obs

import (
	"context"
	"log/slog"
	"time"
)

type Status struct {
	Healthy       bool      `json:"healthy"`
	Mode          string    `json:"mode"`
	LastCheck     time.Time `json:"last_check,omitempty"`
	Version       string    `json:"version,omitempty"`
	DeferredCount int       `json:"deferred_count,omitempty"`
}

func NewStatus(logger *slog.Logger) Status {
	logger.Debug("status requested")
	return Status{Healthy: true, Mode: "scaffold"}
}

type Health struct {
	logger      *slog.Logger
	lastCheck   time.Time
	deferredCnt int
}

func NewHealth(logger *slog.Logger) *Health {
	return &Health{logger: logger}
}

func (h *Health) Check(ctx context.Context, deferredCount int) Status {
	h.logger.DebugContext(ctx, "health check")
	h.lastCheck = time.Now()
	h.deferredCnt = deferredCount

	return Status{
		Healthy:       true,
		Mode:          "operational",
		LastCheck:     h.lastCheck,
		Version:       "0.1.0",
		DeferredCount: deferredCount,
	}
}
