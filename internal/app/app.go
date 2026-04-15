package app

import (
	"context"
	"log/slog"

	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/daemon"
	"github.com/XferOps/system1/internal/extract"
	"github.com/XferOps/system1/internal/introspect"
	"github.com/XferOps/system1/internal/logging"
	"github.com/XferOps/system1/internal/session"
)

type App struct {
	Config         config.Config
	Logger         *slog.Logger
	SessionService *session.Service
	Introspection  *introspect.Service
	ExtractService *extract.Service
	Daemon         *daemon.Runner
}

func New() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)
	sessionSvc := session.NewService(logger, cfg)
	introspectionSvc := introspect.NewService(logger, cfg)
	extractSvc := extract.NewService(logger, cfg)
	daemonRunner := daemon.NewRunner(logger, cfg)

	return &App{
		Config:         cfg,
		Logger:         logger,
		SessionService: sessionSvc,
		Introspection:  introspectionSvc,
		ExtractService: extractSvc,
		Daemon:         daemonRunner,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.Daemon.Run(ctx)
}
