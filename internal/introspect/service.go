package introspect

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	artifactslib "github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/backend"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
	"github.com/XferOps/system1/internal/session"
)

type Result struct {
	Answer        string   `json:"answer"`
	ArtifactRefs  []string `json:"artifact_refs,omitempty"`
	Evidence      []string `json:"evidence,omitempty"`
	DebugIncluded bool     `json:"debug_included"`
}

type Service struct {
	logger    *slog.Logger
	cfg       config.Config
	backend   backend.Backend
	modelProv model.Provider
}

func NewService(logger *slog.Logger, cfg config.Config, backend backend.Backend) *Service {
	return &Service{logger: logger, cfg: cfg, backend: backend}
}

// SetModelProvider sets the model provider for synthesis.
// When set, this provider will be used to generate responses instead of snippet concatenation.
func (s *Service) SetModelProvider(provider model.Provider) {
	s.modelProv = provider
}

func (s *Service) Query(ctx context.Context, query string, debug bool) (Result, error) {
	s.logger.InfoContext(ctx, "introspection requested", slog.String("query", query), slog.Bool("debug", debug))

	query = strings.TrimSpace(query)
	if query == "" {
		return Result{Answer: "No query provided."}, nil
	}

	isCalibration := isCalibrationQuery(query)
	isBroadRecall := isBroadRecallQuery(query)
	mode := introspectionMode(s.cfg.DefaultPassMode)
	queryForRetrieval := query

	ambientResults, err := s.queryAmbientContext(ctx, query)
	if err != nil {
		s.logger.WarnContext(ctx, "ambient context query failed", "error", err)
	}

	if s.cfg.BackendType == "hizal" && s.modelProv != nil && len(ambientResults) > 0 && !isCalibration {
		reconstructed, recErr := s.reconstructIntent(ctx, query, ambientResults)
		if recErr != nil {
			s.logger.DebugContext(ctx, "intent reconstruction failed, using original query", "error", recErr)
		} else if strings.TrimSpace(reconstructed.InterpretedQuery) != "" {
			queryForRetrieval = strings.TrimSpace(reconstructed.InterpretedQuery)
			s.logger.DebugContext(ctx, "intent reconstructed",
				slog.String("original_query", query),
				slog.String("interpreted_query", queryForRetrieval),
				slog.Bool("needs_more_context", reconstructed.NeedsMoreContext),
				slog.Float64("confidence", reconstructed.Confidence),
			)
		}
	}

	if len(ambientResults) > 0 && !isCalibration && shouldUseAmbientOnly(mode, query, queryForRetrieval, ambientResults) {
		return s.synthesizeResponse(queryForRetrieval, ambientResults, debug, "ambient")
	}

	backendResults, err := s.queryBackend(ctx, queryForRetrieval)
	if err != nil {
		s.logger.WarnContext(ctx, "backend query failed", "error", err)
	}

	var allResults []artifactslib.PersistedArtifact
	allResults = append(allResults, ambientResults...)
	allResults = append(allResults, backendResults...)

	if isCalibration && len(allResults) == 0 {
		allResults, err = s.loadCalibrationContext(ctx)
		if err != nil {
			s.logger.WarnContext(ctx, "calibration context load failed", "error", err)
		}
	}

	if isBroadRecall && len(allResults) == 0 {
		allResults, err = s.loadBroadRecallContext(ctx)
		if err != nil {
			s.logger.WarnContext(ctx, "broad recall context load failed", "error", err)
		}
	}

	if len(allResults) == 0 {
		return Result{
			Answer:        "No relevant context found. Starting fresh.",
			DebugIncluded: debug,
		}, nil
	}

	if isCalibration {
		return s.synthesizeCalibrationResponse(queryForRetrieval, allResults, debug)
	}

	source := "backend"
	if len(backendResults) == 0 && len(ambientResults) > 0 {
		source = "ambient"
	}

	return s.synthesizeResponse(queryForRetrieval, allResults, debug, source)
}

func shouldUseAmbientOnly(mode string, originalQuery string, queryForRetrieval string, ambient []artifactslib.PersistedArtifact) bool {
	if mode != "reflective" {
		return false
	}
	if queryLooksAmbiguous(originalQuery) {
		return false
	}
	return hasStrongCoverage(queryForRetrieval, ambient)
}

func queryLooksAmbiguous(query string) bool {
	for _, token := range strings.Fields(query) {
		cleaned := strings.Trim(token, ".,!?:;()[]{}\"'`")
		cleaned = strings.Trim(cleaned, "-_/")
		if len(cleaned) >= 2 && len(cleaned) <= 6 && cleaned == strings.ToUpper(cleaned) {
			return true
		}
	}
	return false
}

func isCalibrationQuery(query string) bool {
	lower := strings.ToLower(query)
	calibrationPhrases := []string{
		"what am i forgetting",
		"what am i missing",
		"what might i be missing",
		"what else should i know",
		"what don't i know",
		"gaps",
		"missing context",
	}
	for _, phrase := range calibrationPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func isBroadRecallQuery(query string) bool {
	lower := strings.ToLower(strings.TrimSpace(query))
	broadRecallPhrases := []string{
		"what do i know",
		"what do i know?",
		"what do i know about",
		"what do i remember",
		"what do i have context on",
	}
	for _, phrase := range broadRecallPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}

func (s *Service) queryAmbientContext(ctx context.Context, query string) ([]artifactslib.PersistedArtifact, error) {
	allArtifacts, err := session.LoadAmbientSnapshot(s.cfg.StateDir)
	if err != nil {
		return nil, err
	}

	if len(allArtifacts) == 0 {
		return nil, nil
	}

	queryTerms := normalizedTerms(query)
	var relevant []artifactslib.PersistedArtifact

	for _, a := range allArtifacts {
		if matchesArtifact(queryTerms, a) {
			relevant = append(relevant, a)
		}
	}

	sortArtifactsByRecency(relevant)

	const maxAmbient = 10
	if len(relevant) > maxAmbient {
		relevant = relevant[:maxAmbient]
	}

	s.logger.DebugContext(ctx, "ambient context query completed",
		slog.Int("total_artifacts", len(allArtifacts)),
		slog.Int("relevant", len(relevant)))

	return relevant, nil
}

func (s *Service) queryBackend(ctx context.Context, query string) ([]artifactslib.PersistedArtifact, error) {
	if s.cfg.BackendType == "hizal" {
		if richBackend, ok := s.backend.(backend.ContextSearchBackend); ok {
			return s.queryHizalContext(ctx, query, richBackend)
		}
	}

	if s.backend == nil {
		return nil, nil
	}

	ftsQuery := buildFTSQuery(query)
	if ftsQuery == "" {
		return nil, nil
	}

	results, err := s.backend.Search(ctx, ftsQuery, 15)
	if err != nil {
		return nil, err
	}

	s.logger.DebugContext(ctx, "backend query completed", slog.Int("results", len(results)), slog.String("fts_query", ftsQuery))

	return results, nil
}

type hizalSearchStep struct {
	Query            string `json:"query"`
	Scope            string `json:"scope,omitempty"`
	ChunkType        string `json:"chunk_type,omitempty"`
	Limit            int    `json:"limit,omitempty"`
	AlwaysInjectOnly bool   `json:"always_inject_only,omitempty"`
}

type hizalSearchPlan struct {
	Steps []hizalSearchStep `json:"steps"`
}

type introspectionSynthesis struct {
	InferredIntent        string   `json:"inferred_intent"`
	Answer                string   `json:"answer"`
	SupportingArtifactIDs []string `json:"supporting_artifact_ids"`
	Uncertainty           string   `json:"uncertainty,omitempty"`
}

type reconstructedIntent struct {
	InterpretedQuery string  `json:"interpreted_query"`
	Reasoning        string  `json:"reasoning,omitempty"`
	NeedsMoreContext bool    `json:"needs_more_context,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
}

const hizalPlanSchema = `{"type":"object","required":["steps"],"properties":{"steps":{"type":"array","minItems":2,"maxItems":4,"items":{"type":"object","required":["query"],"properties":{"query":{"type":"string","minLength":2},"scope":{"type":"string","enum":["","AGENT","PROJECT","ORG"]},"chunk_type":{"type":"string","enum":["","IDENTITY","MEMORY","KNOWLEDGE","CONVENTION","PRINCIPLE","DECISION","LESSON","CONSTRAINT","RESEARCH","PLAN","IMPLEMENTATION","SPEC"]},"limit":{"type":"integer","minimum":1,"maximum":20},"always_inject_only":{"type":"boolean"}},"additionalProperties":false}}}`

const hizalPlanSystemPrompt = `You are a retrieval planner for Hizal context tools.
Return a JSON object with a "steps" array only.
Plan 2-4 search_context calls that maximize recall and then improve authority.
Rules:
- First step should be broad discovery.
- Later steps should narrow scope/chunk_type only when useful.
- Do not force project scoping by default.
- Keep each query concise and semantically meaningful.
- Never return markdown or commentary.`

const hizalNextStepSchema = `{"type":"object","required":["query"],"properties":{"query":{"type":"string","minLength":2},"scope":{"type":"string","enum":["","AGENT","PROJECT","ORG"]},"chunk_type":{"type":"string","enum":["","IDENTITY","MEMORY","KNOWLEDGE","CONVENTION","PRINCIPLE","DECISION","LESSON","CONSTRAINT","RESEARCH","PLAN","IMPLEMENTATION","SPEC"]},"limit":{"type":"integer","minimum":1,"maximum":20},"always_inject_only":{"type":"boolean"}},"additionalProperties":false}`

const modelSynthesisSchema = `{"type":"object","required":["inferred_intent","answer","supporting_artifact_ids"],"properties":{"inferred_intent":{"type":"string","minLength":4},"answer":{"type":"string","minLength":8},"supporting_artifact_ids":{"type":"array","items":{"type":"string"}},"uncertainty":{"type":"string"}},"additionalProperties":false}`

const intentReconstructionSchema = `{"type":"object","required":["interpreted_query"],"properties":{"interpreted_query":{"type":"string","minLength":4},"reasoning":{"type":"string"},"needs_more_context":{"type":"boolean"},"confidence":{"type":"number","minimum":0,"maximum":1}},"additionalProperties":false}`

const intentReconstructionSystemPrompt = `You are the quiet subconscious layer helping my conscious agent self.
Take a vague question and reinterpret it in the most useful way using memory evidence first.

Rules:
- Prefer what memory evidence implies over surface wording.
- If a term is ambiguous, choose the meaning best supported by artifacts.
- If support is weak, keep the interpreted query cautious and set needs_more_context=true.
- Return JSON only, matching the schema.`

func (s *Service) queryHizalContext(ctx context.Context, query string, rich backend.ContextSearchBackend) ([]artifactslib.PersistedArtifact, error) {
	plan := defaultHizalSearchPlan(query)
	mode := introspectionMode(s.cfg.DefaultPassMode)
	maxPasses := maxRetrievalPasses(s.cfg.DefaultPassMode)
	isCalibration := isCalibrationQuery(query)

	if s.modelProv != nil {
		if planned, err := s.planHizalSearch(ctx, query); err == nil && len(planned) > 0 {
			plan = planned
		} else if err != nil {
			s.logger.WarnContext(ctx, "hizal retrieval planning failed, using default plan", "error", err)
		}
	}
	if len(plan) > maxPasses {
		plan = plan[:maxPasses]
	}

	collected := make([]artifactslib.PersistedArtifact, 0, 24)
	seen := make(map[string]struct{})
	for idx := 0; idx < maxPasses; idx++ {
		step, ok := pickSearchStep(plan, idx)
		if modeUsesAdaptiveNextStep(mode) && idx > 0 && s.modelProv != nil {
			if adapted, err := s.planNextHizalStep(ctx, query, collected, idx+1, maxPasses); err == nil {
				step = adapted
				ok = true
			} else {
				s.logger.DebugContext(ctx, "adaptive next-step planning failed, using fallback step", "error", err)
			}
		}
		if !ok {
			break
		}
		if strings.TrimSpace(step.Query) == "" {
			continue
		}
		results, err := rich.SearchContext(ctx, backend.SearchContextRequest{
			Query:            step.Query,
			Limit:            step.Limit,
			Scope:            step.Scope,
			ChunkType:        step.ChunkType,
			AlwaysInjectOnly: step.AlwaysInjectOnly,
		})
		if err != nil {
			s.logger.WarnContext(ctx, "hizal search step failed",
				slog.String("query", step.Query),
				slog.String("scope", step.Scope),
				slog.String("chunk_type", step.ChunkType),
				"error", err)
			continue
		}

		newCount := 0
		for _, a := range results {
			key := artifactIdentityKey(a)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			collected = append(collected, a)
			newCount++
		}

		if !shouldContinueHizalRetrieval(query, collected, len(results), newCount, idx+1, maxPasses, isCalibration) {
			break
		}
	}

	if len(collected) == 0 {
		return nil, nil
	}

	unique := deduplicateArtifacts(collected)
	verified := make([]artifactslib.PersistedArtifact, 0, len(unique))
	for _, a := range unique {
		id, queryKey := artifactLookupKeys(a)
		if strings.TrimSpace(id) == "" && strings.TrimSpace(queryKey) == "" {
			verified = append(verified, a)
			continue
		}
		full, err := rich.ReadContext(ctx, id, queryKey)
		if err != nil {
			verified = append(verified, a)
			continue
		}
		verified = append(verified, full)
	}

	return deduplicateArtifacts(verified), nil
}

func pickSearchStep(plan []hizalSearchStep, idx int) (hizalSearchStep, bool) {
	if idx < len(plan) {
		return plan[idx], true
	}
	return hizalSearchStep{}, false
}

func modeUsesAdaptiveNextStep(mode string) bool {
	return mode == "metacognitive" || mode == "ruminating"
}

func introspectionMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "metacognitive", "ruminating":
		return mode
	default:
		return "reflective"
	}
}

func (s *Service) planNextHizalStep(parentCtx context.Context, query string, collected []artifactslib.PersistedArtifact, passNum int, passBudget int) (hizalSearchStep, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 20*time.Second)
	defer cancel()

	var sb strings.Builder
	sb.WriteString("Original query: ")
	sb.WriteString(strings.TrimSpace(query))
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("Pass %d of %d.\n", passNum, passBudget))
	sb.WriteString("Recent retrieved artifacts:\n")
	for i, a := range getTopArtifactsForQuery(query, collected, 6) {
		sb.WriteString(fmt.Sprintf("- %d) ID=%s TYPE=%s SCOPE=%s TITLE=%s BODY=%s\n",
			i+1,
			a.PersistedID,
			a.ArtifactType,
			a.Scope,
			a.Title,
			truncateForPrompt(a.Body, 220),
		))
	}
	sb.WriteString("Return the best next search_context step to improve coverage and authority.")
	s.logPrompt(ctx, "plan_next_hizal_step", hizalPlanSystemPrompt, sb.String())

	resp, err := s.modelProv.Complete(ctx, sb.String(), hizalPlanSystemPrompt,
		model.WithStructuredOutput(),
		model.WithJSONSchema(hizalNextStepSchema),
		model.WithTemperature(0.1),
	)
	if err != nil {
		return hizalSearchStep{}, err
	}

	var payload []byte
	if len(resp.Structured) > 0 {
		payload = resp.Structured
	} else {
		payload = []byte(resp.Text)
	}

	var step hizalSearchStep
	if err := json.Unmarshal(payload, &step); err != nil {
		return hizalSearchStep{}, fmt.Errorf("parse next hizal step: %w", err)
	}
	step.Query = strings.TrimSpace(step.Query)
	step.Scope = strings.ToUpper(strings.TrimSpace(step.Scope))
	step.ChunkType = strings.ToUpper(strings.TrimSpace(step.ChunkType))
	if step.Query == "" {
		return hizalSearchStep{}, fmt.Errorf("next step had empty query")
	}
	if step.Limit <= 0 || step.Limit > 20 {
		step.Limit = 8
	}
	return step, nil
}

func artifactIdentityKey(a artifactslib.PersistedArtifact) string {
	if id := strings.TrimSpace(a.PersistedID); id != "" {
		return "id:" + id
	}
	if v, ok := a.BackendMetadata["query_key"].(string); ok && strings.TrimSpace(v) != "" {
		return "query_key:" + strings.TrimSpace(v)
	}
	return "fallback:" + strings.TrimSpace(a.Title) + ":" + strings.TrimSpace(a.Body)
}

func shouldContinueHizalRetrieval(query string, collected []artifactslib.PersistedArtifact, lastBatchSize int, lastBatchNew int, passesUsed int, maxPasses int, isCalibration bool) bool {
	if passesUsed >= maxPasses {
		return false
	}
	if len(collected) == 0 {
		return true
	}
	if lastBatchSize > 0 {
		noveltyRatio := float64(lastBatchNew) / float64(lastBatchSize)
		if noveltyRatio < 0.25 {
			return false
		}
	}

	target := 5
	if isCalibration {
		target = 7
	}
	if len(collected) >= target && hasStrongCoverage(query, collected) {
		return false
	}

	return true
}

func hasStrongCoverage(query string, artifacts []artifactslib.PersistedArtifact) bool {
	terms := normalizedTerms(query)
	if len(terms) == 0 {
		return len(artifacts) >= 5
	}

	hits := 0
	for _, a := range artifacts {
		if relevanceScore(terms, a) >= 3 {
			hits++
		}
		if hits >= 2 {
			return true
		}
	}
	return false
}

func maxRetrievalPasses(mode string) int {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "metacognitive":
		return 3
	case "ruminating":
		return 4
	default:
		return 2
	}
}

func defaultHizalSearchPlan(query string) []hizalSearchStep {
	query = strings.TrimSpace(query)
	if query == "" {
		query = "recent context"
	}
	return []hizalSearchStep{
		{Query: query, Limit: 12},
		{Query: query, Scope: "AGENT", ChunkType: "MEMORY", Limit: 8},
		{Query: query, Scope: "PROJECT", Limit: 8},
	}
}

func (s *Service) planHizalSearch(parentCtx context.Context, query string) ([]hizalSearchStep, error) {
	timeout := s.cfg.ModelTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	prompt := "User query: " + strings.TrimSpace(query) + "\n\nBuild a retrieval plan now."
	s.logPrompt(ctx, "plan_hizal_search", hizalPlanSystemPrompt, prompt)
	resp, err := s.modelProv.Complete(ctx, prompt, hizalPlanSystemPrompt,
		model.WithStructuredOutput(),
		model.WithJSONSchema(hizalPlanSchema),
		model.WithTemperature(0.1),
		model.WithMaxTokens(300),
	)
	if err != nil {
		return nil, err
	}

	var payload []byte
	if len(resp.Structured) > 0 {
		payload = resp.Structured
	} else {
		payload = []byte(resp.Text)
	}

	var plan hizalSearchPlan
	if err := json.Unmarshal(payload, &plan); err != nil {
		return nil, fmt.Errorf("parse hizal retrieval plan: %w", err)
	}

	steps := make([]hizalSearchStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		step.Query = strings.TrimSpace(step.Query)
		if step.Query == "" {
			continue
		}
		step.Scope = strings.ToUpper(strings.TrimSpace(step.Scope))
		step.ChunkType = strings.ToUpper(strings.TrimSpace(step.ChunkType))
		if step.Limit <= 0 || step.Limit > 20 {
			step.Limit = 8
		}
		steps = append(steps, step)
	}

	if len(steps) == 0 {
		return nil, fmt.Errorf("empty retrieval plan")
	}

	return steps, nil
}

func artifactLookupKeys(a artifactslib.PersistedArtifact) (string, string) {
	id := strings.TrimSpace(a.PersistedID)
	if v, ok := a.BackendMetadata["chunk_id"].(string); ok && strings.TrimSpace(v) != "" {
		id = strings.TrimSpace(v)
	}
	var queryKey string
	if v, ok := a.BackendMetadata["query_key"].(string); ok {
		queryKey = strings.TrimSpace(v)
	}
	return id, queryKey
}

func (s *Service) reconstructIntent(parentCtx context.Context, query string, artifacts []artifactslib.PersistedArtifact) (reconstructedIntent, error) {
	ctx, cancel := context.WithTimeout(parentCtx, 20*time.Second)
	defer cancel()

	prompt := buildIntentReconstructionPrompt(query, artifacts)
	s.logPrompt(ctx, "reconstruct_intent", intentReconstructionSystemPrompt, prompt)
	resp, err := s.modelProv.Complete(ctx, prompt, intentReconstructionSystemPrompt,
		model.WithStructuredOutput(),
		model.WithJSONSchema(intentReconstructionSchema),
		model.WithTemperature(0.1),
	)
	if err != nil {
		return reconstructedIntent{}, fmt.Errorf("intent reconstruction completion failed: %w", err)
	}

	var payload []byte
	if len(resp.Structured) > 0 {
		payload = resp.Structured
	} else {
		payload = []byte(resp.Text)
	}

	var out reconstructedIntent
	if err := json.Unmarshal(payload, &out); err != nil {
		return reconstructedIntent{}, fmt.Errorf("parse intent reconstruction: %w", err)
	}
	out.InterpretedQuery = strings.TrimSpace(out.InterpretedQuery)
	if out.InterpretedQuery == "" {
		return reconstructedIntent{}, fmt.Errorf("intent reconstruction returned empty interpreted query")
	}
	if out.Confidence < 0 {
		out.Confidence = 0
	}
	if out.Confidence > 1 {
		out.Confidence = 1
	}
	return out, nil
}

func buildIntentReconstructionPrompt(query string, artifacts []artifactslib.PersistedArtifact) string {
	var sb strings.Builder
	sb.WriteString("Original query: ")
	sb.WriteString(strings.TrimSpace(query))
	sb.WriteString("\n\n")
	sb.WriteString("Relevant artifacts:\n")
	for i, a := range getTopArtifactsForQuery(query, artifacts, 8) {
		sb.WriteString(fmt.Sprintf("Artifact %d:\n", i+1))
		sb.WriteString(fmt.Sprintf("  ID: %s\n", a.PersistedID))
		sb.WriteString(fmt.Sprintf("  Type: %s\n", a.ArtifactType))
		sb.WriteString(fmt.Sprintf("  Scope: %s\n", a.Scope))
		sb.WriteString(fmt.Sprintf("  Title: %s\n", a.Title))
		sb.WriteString(fmt.Sprintf("  Body: %s\n", truncateForPrompt(a.Body, 260)))
	}
	sb.WriteString("\nInfer the best interpreted query for retrieval and answer synthesis.")
	return sb.String()
}

func (s *Service) synthesizeResponse(query string, artifacts []artifactslib.PersistedArtifact, debug bool, source string) (Result, error) {
	uniqueArtifacts := deduplicateArtifacts(artifacts)
	recent := getRecentArtifacts(uniqueArtifacts, 5)

	if len(recent) == 0 {
		return Result{Answer: "No relevant artifacts found."}, nil
	}

	// Try model synthesis if provider is configured
	if s.modelProv != nil {
		answer, err := s.synthesizeWithModel(query, uniqueArtifacts, false, source)
		if err == nil && answer != "" {
			result := Result{
				Answer:        strings.TrimSpace(answer),
				DebugIncluded: debug,
			}
			if debug {
				result.ArtifactRefs, result.Evidence = collectDebugEvidence(uniqueArtifacts)
			}
			return result, nil
		}
		if err != nil {
			s.logger.Warn("model synthesis failed, falling back to heuristics", "error", err)
		}
	}

	return s.synthesizeWithHeuristics(query, uniqueArtifacts, debug, source)
}

func (s *Service) synthesizeWithHeuristics(query string, artifacts []artifactslib.PersistedArtifact, debug bool, source string) (Result, error) {
	var sb strings.Builder

	recent := getTopArtifactsForQuery(query, artifacts, 5)

	if len(recent) == 0 {
		return Result{Answer: "No relevant artifacts found."}, nil
	}

	sb.WriteString("Based on my recent context:\n\n")

	for i, a := range recent {
		sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, a.ArtifactType, a.Title))
		if len(a.Body) > 150 {
			sb.WriteString(fmt.Sprintf("   %s...\n\n", a.Body[:150]))
		} else {
			sb.WriteString(fmt.Sprintf("   %s\n\n", a.Body))
		}
	}

	answer := sb.String()

	if source == "ambient" {
		answer += "\n(Found in preloaded context)"
	} else {
		answer += "\n(Retrieved from storage)"
	}

	result := Result{
		Answer:        strings.TrimSpace(answer),
		DebugIncluded: debug,
	}
	if debug {
		result.ArtifactRefs, result.Evidence = collectDebugEvidence(artifacts)
	}

	return result, nil
}

func (s *Service) synthesizeWithModel(query string, artifacts []artifactslib.PersistedArtifact, isCalibration bool, source string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mode := introspectionMode(s.cfg.DefaultPassMode)
	prompt := buildModelPrompt(query, artifacts, isCalibration, source, mode)
	systemPrompt := buildSystemPrompt(isCalibration, mode)
	s.logPrompt(ctx, "synthesize_with_model", systemPrompt, prompt)

	response, err := s.modelProv.Complete(ctx, prompt, systemPrompt,
		model.WithStructuredOutput(),
		model.WithJSONSchema(modelSynthesisSchema),
		model.WithTemperature(0.1),
	)
	if err != nil {
		return "", fmt.Errorf("model completion failed: %w", err)
	}

	parsed, err := parseStructuredSynthesis(response)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(parsed.Answer) == "" {
		return "", fmt.Errorf("model returned empty answer")
	}
	if !hasSupportingArtifacts(parsed.SupportingArtifactIDs, artifacts) {
		return "", fmt.Errorf("model answer lacked grounded artifact support")
	}

	answer := strings.TrimSpace(parsed.Answer)
	if strings.TrimSpace(parsed.Uncertainty) != "" {
		answer += "\n\nUncertainty: " + strings.TrimSpace(parsed.Uncertainty)
	}
	return answer, nil
}

func (s *Service) logPrompt(ctx context.Context, stage string, systemPrompt string, userPrompt string) {
	s.logger.DebugContext(ctx, "introspection model prompt",
		slog.String("stage", stage),
		slog.String("system_prompt", systemPrompt),
		slog.String("user_prompt", userPrompt),
	)
}

func buildModelPrompt(query string, artifacts []artifactslib.PersistedArtifact, isCalibration bool, source string, mode string) string {
	var sb strings.Builder

	sb.WriteString("User Query: ")
	sb.WriteString(query)
	sb.WriteString("\n\n")

	if isCalibration {
		sb.WriteString("This is a calibration query - help identify what context might be missing.\n\n")
	}

	sb.WriteString("Source: ")
	sb.WriteString(source)
	sb.WriteString("\n\n")
	sb.WriteString("Mode: ")
	sb.WriteString(mode)
	sb.WriteString("\n\n")

	sb.WriteString("Retrieved Artifacts:\n")
	sb.WriteString("===================\n\n")

	recent := getRecentArtifacts(artifacts, 10)
	for i, a := range recent {
		sb.WriteString(fmt.Sprintf("Artifact %d:\n", i+1))
		sb.WriteString(fmt.Sprintf("  ID: %s\n", a.PersistedID))
		sb.WriteString(fmt.Sprintf("  Type: %s\n", a.ArtifactType))
		sb.WriteString(fmt.Sprintf("  Title: %s\n", a.Title))
		body := a.Body
		if len(body) > 300 {
			body = body[:300] + "..."
		}
		sb.WriteString(fmt.Sprintf("  Body: %s\n", body))
		if len(a.Provenance.EvidenceSnippets) > 0 {
			sb.WriteString(fmt.Sprintf("  Evidence: %s\n", strings.Join(a.Provenance.EvidenceSnippets, "; ")))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("\nBefore answering, infer what I most likely meant from memory context.")
	sb.WriteString("\nAnswer in first person as my internal voice (" + `"I"` + "), concise and grounded.")
	sb.WriteString("\nTreat human profiles (e.g. Parker) as people I work with, not my own identity.")
	sb.WriteString("\nIf asked who I am, prefer agent identity artifacts over human contact/profile artifacts.")
	sb.WriteString("\nReturn JSON matching schema with inferred_intent, supporting_artifact_ids, and answer.")
	sb.WriteString("\nDo not make claims unsupported by listed artifact IDs.")
	if isCalibration {
		sb.WriteString(" Focus on identifying gaps or missing context that might be relevant to the user's query.")
	}

	return sb.String()
}

func buildSystemPrompt(isCalibration bool, mode string) string {
	modeDirective := ""
	switch mode {
	case "metacognitive":
		modeDirective = "Use a metacognitive style: reinterpret vague wording from memory evidence, compare possible meanings, and choose the best-supported one."
	case "ruminating":
		modeDirective = "Use a ruminating style: sit with ambiguity, connect nearby memories, and surface uncertainty clearly when support is thin."
	default:
		modeDirective = "Use a reflective style: give the smallest grounded answer that resolves the question."
	}

	if isCalibration {
		return `You are my subconscious speaking to my conscious self.
Write as my internal voice in first person.
I am the agent. The human (for example Parker) is someone I work with, not me.
When identity is ambiguous, prefer agent identity artifacts for "who am I" questions.

For this calibration query:
1. Acknowledge what I already know
2. Identify likely missing context
3. Suggest the next things I should check

Be concise, natural, and grounded in artifacts. Do not invent details.
` + modeDirective
	}
	return `You are my subconscious speaking to my conscious self.
Write as my internal voice in first person.
I am the agent. The human (for example Parker) is someone I work with, not me.
When identity is ambiguous, prefer agent identity artifacts for "who am I" questions.

Give a concise, natural answer grounded in retrieved artifacts.
If support is weak, say so clearly instead of guessing.
` + modeDirective
}

func parseStructuredSynthesis(resp model.Response) (introspectionSynthesis, error) {
	var payload []byte
	if len(resp.Structured) > 0 {
		payload = resp.Structured
	} else {
		payload = []byte(resp.Text)
	}
	var parsed introspectionSynthesis
	if err := json.Unmarshal(payload, &parsed); err != nil {
		return introspectionSynthesis{}, fmt.Errorf("parse structured synthesis: %w", err)
	}
	return parsed, nil
}

func hasSupportingArtifacts(ids []string, artifacts []artifactslib.PersistedArtifact) bool {
	if len(ids) == 0 {
		return false
	}
	known := make(map[string]struct{}, len(artifacts))
	for _, a := range artifacts {
		if strings.TrimSpace(a.PersistedID) != "" {
			known[strings.TrimSpace(a.PersistedID)] = struct{}{}
		}
	}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := known[id]; ok {
			return true
		}
	}
	return false
}

func truncateForPrompt(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func (s *Service) synthesizeCalibrationResponse(query string, artifacts []artifactslib.PersistedArtifact, debug bool) (Result, error) {
	uniqueArtifacts := deduplicateArtifacts(artifacts)

	// Try model synthesis if provider is configured
	if s.modelProv != nil {
		answer, err := s.synthesizeWithModel(query, uniqueArtifacts, true, "calibration")
		if err == nil && answer != "" {
			result := Result{
				Answer:        strings.TrimSpace(answer),
				DebugIncluded: debug,
			}
			if debug {
				result.ArtifactRefs, result.Evidence = collectDebugEvidence(uniqueArtifacts)
			}
			return result, nil
		}
		if err != nil {
			s.logger.Warn("model synthesis failed for calibration, falling back to heuristics", "error", err)
		}
	}

	return s.synthesizeCalibrationWithHeuristics(query, uniqueArtifacts, debug)
}

func (s *Service) synthesizeCalibrationWithHeuristics(query string, artifacts []artifactslib.PersistedArtifact, debug bool) (Result, error) {
	var sb strings.Builder
	sb.WriteString("Let me think about what might be missing...\n\n")

	topics := extractTopics(artifacts)
	if len(topics) > 0 {
		sb.WriteString("Based on what I've found, here are areas that might be worth exploring:\n\n")
		for i, topic := range topics {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, topic))
		}
		sb.WriteString("\n")
	}

	var gaps []string
	if len(artifacts) < 3 {
		gaps = append(gaps, "Limited session history - consider continuing the conversation to build more context.")
	}
	if !hasRecentArtifacts(artifacts, 1*time.Hour) {
		gaps = append(gaps, "No very recent artifacts - current context may be sparse.")
	}

	if len(gaps) > 0 {
		sb.WriteString("Potential gaps identified:\n")
		for _, gap := range gaps {
			sb.WriteString(fmt.Sprintf("- %s\n", gap))
		}
	}

	answer := sb.String()

	result := Result{
		Answer:        strings.TrimSpace(answer),
		DebugIncluded: debug,
	}
	if debug {
		result.ArtifactRefs, result.Evidence = collectDebugEvidence(artifacts)
	}

	return result, nil
}

func deduplicateArtifacts(artifacts []artifactslib.PersistedArtifact) []artifactslib.PersistedArtifact {
	seen := make(map[string]bool)
	var result []artifactslib.PersistedArtifact
	for _, a := range artifacts {
		if !seen[a.PersistedID] {
			seen[a.PersistedID] = true
			result = append(result, a)
		}
	}
	return result
}

func getRecentArtifacts(artifacts []artifactslib.PersistedArtifact, max int) []artifactslib.PersistedArtifact {
	artifacts = append([]artifactslib.PersistedArtifact(nil), artifacts...)
	sortArtifactsByRecency(artifacts)

	if len(artifacts) <= max {
		return artifacts
	}
	return artifacts[:max]
}

func getTopArtifactsForQuery(query string, artifacts []artifactslib.PersistedArtifact, max int) []artifactslib.PersistedArtifact {
	artifacts = append([]artifactslib.PersistedArtifact(nil), artifacts...)
	queryTerms := normalizedTerms(query)
	if len(queryTerms) == 0 {
		return getRecentArtifacts(artifacts, max)
	}

	sort.SliceStable(artifacts, func(i, j int) bool {
		left := relevanceScore(queryTerms, artifacts[i])
		right := relevanceScore(queryTerms, artifacts[j])
		if left != right {
			return left > right
		}
		return artifacts[i].WrittenAt.After(artifacts[j].WrittenAt)
	})

	if len(artifacts) <= max {
		return artifacts
	}
	return artifacts[:max]
}

func hasRecentArtifacts(artifacts []artifactslib.PersistedArtifact, within time.Duration) bool {
	cutoff := time.Now().Add(-within)
	for _, a := range artifacts {
		if a.WrittenAt.After(cutoff) {
			return true
		}
	}
	return false
}

func extractTopics(artifacts []artifactslib.PersistedArtifact) []string {
	topicSet := make(map[string]bool)
	for _, a := range artifacts {
		words := strings.Fields(a.Title)
		for _, word := range words {
			word = normalizeTopicTerm(word)
			if shouldIncludeTopic(word, 4) {
				topicSet[word] = true
			}
		}
		words = strings.Fields(a.Body)
		for _, word := range words {
			word = normalizeTopicTerm(word)
			if shouldIncludeTopic(word, 5) {
				topicSet[word] = true
			}
		}
	}

	keys := make([]string, 0, len(topicSet))
	for topic := range topicSet {
		keys = append(keys, topic)
	}
	sort.Strings(keys)
	if len(keys) > 5 {
		keys = keys[:5]
	}

	return keys
}

func sortArtifactsByRecency(artifacts []artifactslib.PersistedArtifact) {
	sort.SliceStable(artifacts, func(i, j int) bool {
		return artifacts[i].WrittenAt.After(artifacts[j].WrittenAt)
	})
}

func normalizedTerms(text string) []string {
	words := strings.Fields(text)
	seen := make(map[string]bool)
	terms := make([]string, 0, len(words))
	for _, word := range words {
		for _, term := range expandNormalizedTerms(word) {
			if term == "" || seen[term] {
				continue
			}
			seen[term] = true
			terms = append(terms, term)
		}
	}
	return terms
}

func matchesArtifact(queryTerms []string, artifact artifactslib.PersistedArtifact) bool {
	return relevanceScore(queryTerms, artifact) > 0
}

func relevanceScore(queryTerms []string, artifact artifactslib.PersistedArtifact) int {
	if len(queryTerms) == 0 {
		return 0
	}

	titleTerms := make(map[string]bool)
	for _, word := range normalizedTerms(artifact.Title) {
		titleTerms[word] = true
	}
	bodyTerms := make(map[string]bool)
	for _, word := range normalizedTerms(artifact.Body) {
		bodyTerms[word] = true
	}

	score := 0
	for _, term := range queryTerms {
		if titleTerms[term] {
			score += 3
		}
		if bodyTerms[term] {
			score += 1
		}
	}
	return score
}

func collectDebugEvidence(artifacts []artifactslib.PersistedArtifact) ([]string, []string) {
	refs := make([]string, len(artifacts))
	evidence := make([]string, 0)
	for i, a := range artifacts {
		refs[i] = a.PersistedID
		evidence = append(evidence, a.Provenance.EvidenceSnippets...)
	}
	return refs, evidence
}

func normalizeTopicTerm(word string) string {
	term := strings.Trim(strings.ToLower(word), ".,!?:;()[]{}\"'`")
	term = strings.Trim(term, "-_/")
	return term
}

func expandNormalizedTerms(word string) []string {
	term := normalizeTopicTerm(word)
	if term == "" || topicStopwords[term] {
		return nil
	}

	seen := map[string]bool{term: true}
	variants := []string{term}
	current := term
	for i := 0; i < 3; i++ {
		next := stemTerm(current)
		if next == "" || next == current || seen[next] || topicStopwords[next] {
			break
		}
		seen[next] = true
		variants = append(variants, next)
		current = next
	}
	return variants
}

func stemTerm(term string) string {
	switch {
	case len(term) > 5 && strings.HasSuffix(term, "ences"):
		return strings.TrimSuffix(term, "ences") + "ence"
	case len(term) > 4 && strings.HasSuffix(term, "ence"):
		return strings.TrimSuffix(term, "ence")
	case len(term) > 5 && strings.HasSuffix(term, "ings"):
		return strings.TrimSuffix(term, "ings")
	case len(term) > 4 && strings.HasSuffix(term, "ing"):
		return strings.TrimSuffix(term, "ing")
	case len(term) > 4 && strings.HasSuffix(term, "ied"):
		return strings.TrimSuffix(term, "ied") + "y"
	case len(term) > 3 && strings.HasSuffix(term, "ed"):
		return strings.TrimSuffix(term, "ed")
	case len(term) > 4 && strings.HasSuffix(term, "es"):
		return strings.TrimSuffix(term, "es")
	case len(term) > 3 && strings.HasSuffix(term, "s"):
		return strings.TrimSuffix(term, "s")
	default:
		return term
	}
}

func buildFTSQuery(query string) string {
	terms := normalizedTerms(query)
	if len(terms) == 0 {
		return ""
	}

	parts := make([]string, 0, len(terms))
	for _, term := range terms {
		parts = append(parts, term+"*")
	}
	return strings.Join(parts, " OR ")
}

func (s *Service) loadCalibrationContext(ctx context.Context) ([]artifactslib.PersistedArtifact, error) {
	return s.loadAmbientSnapshot()
}

func (s *Service) loadBroadRecallContext(ctx context.Context) ([]artifactslib.PersistedArtifact, error) {
	return s.loadAmbientSnapshot()
}

func (s *Service) loadAmbientSnapshot() ([]artifactslib.PersistedArtifact, error) {
	ambient, err := session.LoadAmbientSnapshot(s.cfg.StateDir)
	if err != nil {
		return nil, err
	}
	if len(ambient) > 0 {
		sortArtifactsByRecency(ambient)
		return ambient, nil
	}
	return nil, nil
}

func shouldIncludeTopic(word string, minLength int) bool {
	if len(word) < minLength {
		return false
	}
	return !topicStopwords[word]
}

var topicStopwords = map[string]bool{
	"about":  true,
	"after":  true,
	"also":   true,
	"am":     true,
	"and":    true,
	"did":    true,
	"do":     true,
	"else":   true,
	"from":   true,
	"have":   true,
	"i":      true,
	"just":   true,
	"know":   true,
	"might":  true,
	"should": true,
	"that":   true,
	"them":   true,
	"they":   true,
	"this":   true,
	"what":   true,
	"when":   true,
	"with":   true,
	"your":   true,
}
