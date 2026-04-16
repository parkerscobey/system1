package app

import (
	"context"
	"log/slog"

	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/backend/file"
	"github.com/XferOps/system1/internal/backend/hizal"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/daemon"
	"github.com/XferOps/system1/internal/extract"
	"github.com/XferOps/system1/internal/introspect"
	"github.com/XferOps/system1/internal/logging"
	"github.com/XferOps/system1/internal/obs"
	"github.com/XferOps/system1/internal/policy"
	"github.com/XferOps/system1/internal/session"
)

type App struct {
	Config             config.Config
	Logger             *slog.Logger
	Backend            backend.Backend
	SessionService     *session.Service
	Introspection      *introspect.Service
	ExtractService     *extract.Service
	PolicyService      *policy.Service
	Daemon             *daemon.Runner
	Health             *obs.Health
	DecisionLog        *obs.DecisionLog
	IntrospectionTrace *obs.IntrospectionTrace
}

func New() (*App, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}

	logger := logging.New(cfg.LogLevel, cfg.LogFormat)

	var be backend.Backend
	switch cfg.BackendType {
	case "hizal":
		logger.Info("using hizal backend", "project_id", cfg.HizalProjectID)
		be = hizal.NewStore(logger, cfg.HizalProjectID, cfg.EnabledTypes)
	default:
		logger.Info("using file backend")
		be, err = file.NewStore(logger, cfg)
		if err != nil {
			return nil, err
		}
	}

	sessionSvc := session.NewService(logger, cfg, be)
	introspectionSvc := introspect.NewService(logger, cfg, be)
	extractSvc := extract.NewService(logger, cfg)
	policySvc := policy.NewService(logger, cfg, be)
	daemonRunner := daemon.NewRunner(logger, cfg, sessionSvc, introspectionSvc)

	health := obs.NewHealth(logger)
	decisionLog := obs.NewDecisionLog(logger)
	introspectionTrace := obs.NewIntrospectionTrace(logger)

	return &App{
		Config:             cfg,
		Logger:             logger,
		Backend:            be,
		SessionService:     sessionSvc,
		Introspection:      introspectionSvc,
		ExtractService:     extractSvc,
		PolicyService:      policySvc,
		Daemon:             daemonRunner,
		Health:             health,
		DecisionLog:        decisionLog,
		IntrospectionTrace: introspectionTrace,
	}, nil
}

func (a *App) Run(ctx context.Context) error {
	return a.Daemon.Run(ctx)
}
