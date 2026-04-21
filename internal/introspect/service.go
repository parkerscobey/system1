package introspect

import (
	"context"
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

	ambientResults, err := s.queryAmbientContext(ctx, query)
	if err != nil {
		s.logger.WarnContext(ctx, "ambient context query failed", "error", err)
	}

	if len(ambientResults) > 0 && !isCalibration {
		return s.synthesizeResponse(query, ambientResults, debug, "ambient")
	}

	backendResults, err := s.queryBackend(ctx, query)
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
		return s.synthesizeCalibrationResponse(query, allResults, debug)
	}

	return s.synthesizeResponse(query, allResults, debug, "backend")
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

	prompt := buildModelPrompt(query, artifacts, isCalibration, source)
	systemPrompt := buildSystemPrompt(isCalibration)

	response, err := s.modelProv.Complete(ctx, prompt, systemPrompt)
	if err != nil {
		return "", fmt.Errorf("model completion failed: %w", err)
	}

	if response.Text == "" {
		return "", fmt.Errorf("model returned empty response")
	}

	return response.Text, nil
}

func buildModelPrompt(query string, artifacts []artifactslib.PersistedArtifact, isCalibration bool, source string) string {
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

	sb.WriteString("Retrieved Artifacts:\n")
	sb.WriteString("===================\n\n")

	recent := getRecentArtifacts(artifacts, 10)
	for i, a := range recent {
		sb.WriteString(fmt.Sprintf("Artifact %d:\n", i+1))
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

	sb.WriteString("\nPlease provide a grounded, natural-language answer based on the retrieved artifacts above.")
	if isCalibration {
		sb.WriteString(" Focus on identifying gaps or missing context that might be relevant to the user's query.")
	}

	return sb.String()
}

func buildSystemPrompt(isCalibration bool) string {
	if isCalibration {
		return `You are an introspection assistant helping a user identify gaps in their context.
You have been given retrieved artifacts from the user's session history.
Provide a helpful response that:
1. Acknowledges what was found
2. Identifies potential gaps or missing context
3. Suggests areas worth exploring
Be concise and natural. Do not invent information not present in the artifacts.`
	}
	return `You are an introspection assistant helping a user understand their context.
You have been given retrieved artifacts from the user's session history.
Provide a helpful, natural-language answer that synthesizes the information from the artifacts.
Be grounded in the provided artifacts - don't hallucinate details.
Be concise and direct. Focus on answering the user's query using the available context.`
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
