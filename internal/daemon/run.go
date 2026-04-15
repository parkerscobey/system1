package daemon

import (
	"context"
	"log/slog"
	"os/signal"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/XferOps/system1/internal/config"
)

type Runner struct {
	logger *slog.Logger
	cfg    config.Config
}

func NewRunner(logger *slog.Logger, cfg config.Config) *Runner {
	return &Runner{logger: logger, cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	r.logger.InfoContext(ctx, "system1 daemon starting", slog.String("state_dir", r.cfg.StateDir))

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		<-gctx.Done()
		return nil
	})

	err := g.Wait()
	r.logger.InfoContext(context.Background(), "system1 daemon stopped")
	return err
}
