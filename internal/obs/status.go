package obs

import "log/slog"

type Status struct {
	Healthy bool   `json:"healthy"`
	Mode    string `json:"mode"`
}

func NewStatus(logger *slog.Logger) Status {
	logger.Debug("status requested")
	return Status{Healthy: true, Mode: "scaffold"}
}
