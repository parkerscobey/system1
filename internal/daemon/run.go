package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/extract"
	"github.com/XferOps/system1/internal/ingest"
	"github.com/XferOps/system1/internal/introspect"
	"github.com/XferOps/system1/internal/mcp"
	"github.com/XferOps/system1/internal/policy"
	"github.com/XferOps/system1/internal/session"
)

type Runner struct {
	logger         *slog.Logger
	cfg            config.Config
	ingestService  *ingest.Service
	extractService *extract.Service
	policyService  *policy.Service
	sessionService *session.Service
	introspection  *introspect.Service

	seenPartIDs      map[string]struct{}
	seenPartIDOrder  []string
	maxSeenPartIDSet int
}

func NewRunner(
	logger *slog.Logger,
	cfg config.Config,
	ingestService *ingest.Service,
	extractService *extract.Service,
	policyService *policy.Service,
	sessionService *session.Service,
	introspection *introspect.Service,
) *Runner {
	return &Runner{
		logger:           logger,
		cfg:              cfg,
		ingestService:    ingestService,
		extractService:   extractService,
		policyService:    policyService,
		sessionService:   sessionService,
		introspection:    introspection,
		seenPartIDs:      make(map[string]struct{}),
		seenPartIDOrder:  make([]string, 0, 1024),
		maxSeenPartIDSet: 50000,
	}
}

func (r *Runner) Run(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	r.logger.InfoContext(ctx, "system1 daemon starting", slog.String("state_dir", r.cfg.StateDir))

	mcpServer := mcp.New(r.logger, r.sessionService, r.introspection)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return mcpServer.Start(gctx)
	})

	g.Go(func() error {
		return r.runPipeline(gctx)
	})

	g.Go(func() error {
		<-gctx.Done()
		return nil
	})

	err := g.Wait()
	r.resolveDeferredOnShutdown()
	r.logger.InfoContext(context.Background(), "system1 daemon stopped")
	return err
}

func (r *Runner) runPipeline(ctx context.Context) error {
	if r.ingestService == nil || r.extractService == nil || r.policyService == nil {
		r.logger.WarnContext(ctx, "pipeline services not fully configured; skipping ingestion/extraction loop")
		<-ctx.Done()
		return nil
	}

	const pollEvery = 5 * time.Second
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()

	for {
		if err := r.runPipelineCycle(ctx); err != nil {
			r.logger.WarnContext(ctx, "pipeline cycle failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Runner) runPipelineCycle(ctx context.Context) error {
	stats, err := r.ingestService.Ingest(ctx)
	if err != nil {
		if errors.Is(err, ingest.ErrEmptyLog) {
			return nil
		}
		return err
	}
	if stats == nil || stats.SpansBuilt == 0 {
		return nil
	}

	spans := r.ingestService.GetSpans()
	if len(spans) == 0 {
		return nil
	}

	var candidates []artifacts.CandidateArtifact
	skippedSeenParts := 0
	processedSpans := 0
	for _, span := range spans {
		if r.shouldSkipSpanBySeenPartIDs(span) {
			skippedSeenParts++
			continue
		}
		extracted, err := r.extractService.Extract(ctx, span)
		if err != nil {
			r.logger.WarnContext(ctx, "extract failed", slog.String("span_id", span.SpanID), "error", err)
			continue
		}
		r.rememberSpanPartIDs(span)
		processedSpans++
		candidates = append(candidates, extracted...)
	}
	if len(candidates) == 0 {
		r.logger.InfoContext(ctx, "pipeline cycle complete",
			slog.Int("spans_total", len(spans)),
			slog.Int("spans_processed", processedSpans),
			slog.Int("spans_skipped_seen_parts", skippedSeenParts),
			slog.Int("candidates", 0),
			slog.Int("approved", 0),
			slog.Int("persisted", 0),
			slog.Int("deferred", r.policyService.GetDeferredCount()),
		)
		return nil
	}

	approved := make([]artifacts.CandidateArtifact, 0, len(candidates))
	for _, candidate := range candidates {
		result, err := r.policyService.Evaluate(ctx, candidate)
		if err != nil {
			r.logger.WarnContext(ctx, "policy evaluate failed", slog.String("candidate_id", candidate.CandidateID), "error", err)
			continue
		}
		if result.Status == artifacts.StatusApproved {
			approved = append(approved, result)
		}
	}

	persistedCount := 0
	for _, candidate := range approved {
		if _, err := r.policyService.PersistApproved(ctx, candidate); err != nil {
			r.logger.WarnContext(ctx, "persist approved failed", slog.String("candidate_id", candidate.CandidateID), "error", err)
			continue
		}
		persistedCount++
	}

	r.logger.InfoContext(ctx, "pipeline cycle complete",
		slog.Int("spans_total", len(spans)),
		slog.Int("spans_processed", processedSpans),
		slog.Int("spans_skipped_seen_parts", skippedSeenParts),
		slog.Int("candidates", len(candidates)),
		slog.Int("approved", len(approved)),
		slog.Int("persisted", persistedCount),
		slog.Int("deferred", r.policyService.GetDeferredCount()),
	)

	return nil
}

func (r *Runner) shouldSkipSpanBySeenPartIDs(span artifacts.EventSpan) bool {
	partIDs := partIDsFromEventIDs(span.EventIDs)
	if len(partIDs) == 0 {
		return false
	}
	for _, id := range partIDs {
		if _, ok := r.seenPartIDs[id]; !ok {
			return false
		}
	}
	return true
}

func (r *Runner) rememberSpanPartIDs(span artifacts.EventSpan) {
	partIDs := partIDsFromEventIDs(span.EventIDs)
	if len(partIDs) == 0 {
		return
	}
	for _, id := range partIDs {
		if _, ok := r.seenPartIDs[id]; ok {
			continue
		}
		r.seenPartIDs[id] = struct{}{}
		r.seenPartIDOrder = append(r.seenPartIDOrder, id)
		if len(r.seenPartIDOrder) > r.maxSeenPartIDSet {
			oldest := r.seenPartIDOrder[0]
			r.seenPartIDOrder = r.seenPartIDOrder[1:]
			delete(r.seenPartIDs, oldest)
		}
	}
}

func partIDsFromEventIDs(eventIDs []string) []string {
	const prefix = "opencode_part_"
	ids := make([]string, 0, len(eventIDs))
	for _, eventID := range eventIDs {
		if strings.HasPrefix(eventID, prefix) && len(eventID) > len(prefix) {
			ids = append(ids, strings.TrimPrefix(eventID, prefix))
		}
	}
	return ids
}

func (r *Runner) resolveDeferredOnShutdown() {
	if r.policyService == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resolved, err := r.policyService.ResolveDeferred(ctx)
	if err != nil {
		r.logger.Warn("deferred resolve on shutdown failed", "error", err)
		return
	}

	persisted := 0
	for _, candidate := range resolved {
		if candidate.Status != artifacts.StatusApproved {
			continue
		}
		if _, err := r.policyService.PersistApproved(ctx, candidate); err != nil {
			r.logger.Warn("failed to persist resolved deferred candidate", slog.String("candidate_id", candidate.CandidateID), "error", err)
			continue
		}
		persisted++
	}

	if len(resolved) > 0 {
		r.logger.Info("resolved deferred candidates on shutdown",
			slog.Int("resolved", len(resolved)),
			slog.Int("persisted", persisted),
		)
	}
}
