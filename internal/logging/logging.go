package logging

import (
	"log/slog"
	"os"
	"strings"
)

func New(level, format string) *slog.Logger {
	logLevel := parseLevel(level)
	options := &slog.HandlerOptions{Level: logLevel}

	if strings.EqualFold(format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stdout, options))
	}

	return slog.New(slog.NewTextHandler(os.Stdout, options))
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
