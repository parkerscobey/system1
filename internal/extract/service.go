package extract

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/XferOps/system1/internal/artifacts"
	"github.com/XferOps/system1/internal/config"
	"github.com/XferOps/system1/internal/model"
	"github.com/google/uuid"
)

var ErrNoEnabledTypes = fmt.Errorf("no enabled types configured")

type Service struct {
	logger       *slog.Logger
	cfg          config.Config
	enabledTypes map[string]bool
	provider     model.Provider
	traceLogs    bool
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	enabled := make(map[string]bool)
	for _, t := range cfg.EnabledTypes {
		enabled[strings.ToUpper(t)] = true
	}
	return &Service{logger: logger, cfg: cfg, enabledTypes: enabled, traceLogs: envBool("SYSTEM1_TRACE_EXTRACTION")}
}

func (s *Service) WithModelProvider(provider model.Provider) *Service {
	s.provider = provider
	return s
}

func (s *Service) Extract(ctx context.Context, span artifacts.EventSpan) ([]artifacts.CandidateArtifact, error) {
	if s.traceLogs {
		s.logger.DebugContext(ctx, "extract requested", slog.String("span_id", span.SpanID))
	}

	if len(s.cfg.EnabledTypes) == 0 {
		return nil, ErrNoEnabledTypes
	}

	candidates := s.detectCandidates(ctx, span)
	if s.traceLogs {
		s.logger.DebugContext(ctx, "extraction complete",
			slog.String("span_id", span.SpanID),
			slog.Int("candidates", len(candidates)),
		)
	}

	return candidates, nil
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func (s *Service) detectCandidates(ctx context.Context, span artifacts.EventSpan) []artifacts.CandidateArtifact {
	if len(span.EventIDs) == 0 {
		return nil
	}

	var candidates []artifacts.CandidateArtifact

	signal := s.extractSignal(ctx, span)
	if signal == nil {
		return nil
	}

	if !s.isValidType(signal.ArtifactType) {
		s.logger.DebugContext(ctx, "skipping candidate - type not in enabled registry",
			slog.String("type", signal.ArtifactType))
		return nil
	}

	candidates = append(candidates, *signal)
	return candidates
}

func (s *Service) isValidType(t string) bool {
	return s.enabledTypes[strings.ToUpper(t)]
}

func (s *Service) extractSignal(ctx context.Context, span artifacts.EventSpan) *artifacts.CandidateArtifact {
	if len(span.RawRefs) == 0 {
		return nil
	}

	var content strings.Builder
	for _, ref := range span.RawRefs {
		text, err := readContentFromRef(ref)
		if err != nil {
			s.logger.WarnContext(ctx, "failed to read content from ref", slog.String("ref", ref), slog.String("error", err.Error()))
			continue
		}
		content.WriteString(text)
		content.WriteString("\n")
	}
	rawContent := content.String()

	if rawContent == "" {
		s.logger.DebugContext(ctx, "no content extracted from refs", slog.String("span_id", span.SpanID))
		return nil
	}

	// Try model-driven extraction first if provider is available
	if s.provider != nil {
		candidate := s.extractWithModel(ctx, span, rawContent)
		if candidate != nil {
			s.logger.DebugContext(ctx, "model extraction succeeded", slog.String("span_id", span.SpanID))
			return candidate
		}
		// Fallback to heuristic extraction if model fails
		s.logger.DebugContext(ctx, "model extraction failed, falling back to heuristics", slog.String("span_id", span.SpanID))
	}

	return s.extractWithHeuristics(ctx, span, rawContent)
}

// modelResponse represents the structured JSON response from the model
type modelResponse struct {
	ArtifactType  string `json:"artifact_type"`
	Scope         string `json:"scope"`
	Confidence    string `json:"confidence"`
	Title         string `json:"title"`
	Body          string `json:"body"`
	ShouldExtract bool   `json:"should_extract"`
}

const extractionSystemPrompt = `You are an artifact extraction assistant. Your task is to analyze content and extract structured artifacts.

You must respond with a valid JSON object containing these fields:
- artifact_type: either "MEMORY" (user preferences, habits, personal info) or "KNOWLEDGE" (technical facts, architecture, implementation details)
- scope: one of "AGENT" (agent-specific), "PROJECT" (project-level), or "ORG" (organization-wide)
- confidence: one of "low", "medium", or "high" indicating how certain you are
- title: a short descriptive title (max 100 chars)
- body: the relevant content to preserve (relevant excerpts)
- should_extract: boolean indicating if this content is worth extracting (true) or should be skipped (false)

If the content lacks signal or is too vague, set should_extract to false.
Be conservative - only extract when there's clear value.`

func (s *Service) extractWithModel(ctx context.Context, span artifacts.EventSpan, rawContent string) *artifacts.CandidateArtifact {
	prompt := fmt.Sprintf(`Analyze the following content and extract an artifact if present:

---
%s
---

Extract a structured artifact or abstain if there's insufficient signal.`, rawContent)

	response, err := s.provider.Complete(ctx, prompt, extractionSystemPrompt, model.WithStructuredOutput())
	if err != nil {
		s.logger.WarnContext(ctx, "model extraction failed", slog.String("error", err.Error()))
		return nil
	}

	var modelResp modelResponse
	if len(response.Structured) > 0 {
		if err := json.Unmarshal(response.Structured, &modelResp); err != nil {
			s.logger.WarnContext(ctx, "failed to parse model structured response", slog.String("error", err.Error()))
			return nil
		}
	} else if response.Text != "" {
		// Try to parse the text as JSON
		if err := json.Unmarshal([]byte(response.Text), &modelResp); err != nil {
			s.logger.WarnContext(ctx, "failed to parse model text response as JSON", slog.String("error", err.Error()))
			return nil
		}
	} else {
		s.logger.WarnContext(ctx, "model returned empty response")
		return nil
	}

	// Check if model abstained
	if !modelResp.ShouldExtract {
		s.logger.DebugContext(ctx, "model abstained from extraction")
		return nil
	}

	// Validate required fields
	if modelResp.ArtifactType == "" || modelResp.Scope == "" || modelResp.Confidence == "" {
		s.logger.WarnContext(ctx, "model response missing required fields")
		return nil
	}

	// Normalize values
	modelResp.ArtifactType = strings.ToUpper(modelResp.ArtifactType)
	modelResp.Scope = strings.ToUpper(modelResp.Scope)
	modelResp.Confidence = strings.ToLower(modelResp.Confidence)

	// Validate artifact type
	if !s.isValidType(modelResp.ArtifactType) {
		s.logger.DebugContext(ctx, "model returned invalid artifact type",
			slog.String("type", modelResp.ArtifactType))
		return nil
	}

	// Validate scope
	validScopes := map[string]bool{
		string(artifacts.ScopeAgent):   true,
		string(artifacts.ScopeProject): true,
		string(artifacts.ScopeOrg):     true,
	}
	if !validScopes[modelResp.Scope] {
		s.logger.DebugContext(ctx, "model returned invalid scope", slog.String("scope", modelResp.Scope))
		return nil
	}

	// Validate confidence
	validConfidences := map[string]bool{
		artifacts.ConfidenceLow:  true,
		artifacts.ConfidenceMid:  true,
		artifacts.ConfidenceHigh: true,
	}
	if !validConfidences[modelResp.Confidence] {
		modelResp.Confidence = artifacts.ConfidenceMid
	}

	candidate := &artifacts.CandidateArtifact{
		CandidateID:   generateCandidateID(),
		ArtifactType:  modelResp.ArtifactType,
		ProposedScope: modelResp.Scope,
		Title:         modelResp.Title,
		Body:          modelResp.Body,
		Confidence:    modelResp.Confidence,
		Provenance: artifacts.Provenance{
			SpanIDs:          []string{span.SpanID},
			EventIDs:         span.EventIDs,
			RawRefs:          span.RawRefs,
			SessionIDs:       []string{span.SessionID},
			SourceIDs:        []string{span.SourceID},
			EvidenceSnippets: s.extractEvidence(span),
			ExtractionModel:  s.provider.Name(),
			ExtractionTime:   time.Now().UTC(),
		},
		Status:    artifacts.StatusProposed,
		CreatedAt: time.Now().UTC(),
	}

	return candidate
}

func (s *Service) extractWithHeuristics(ctx context.Context, span artifacts.EventSpan, rawContent string) *artifacts.CandidateArtifact {
	signalType := s.detectType(ctx, rawContent)
	if signalType == "" {
		s.logger.DebugContext(ctx, "no signal detected in span", slog.String("span_id", span.SpanID))
		return nil
	}

	signalScope := s.detectScope(ctx, rawContent)
	signalConfidence := s.detectConfidence(ctx, rawContent)

	candidate := &artifacts.CandidateArtifact{
		CandidateID:   generateCandidateID(),
		ArtifactType:  signalType,
		ProposedScope: signalScope,
		Title:         s.generateTitle(signalType, rawContent),
		Body:          s.generateBody(signalType, rawContent),
		Confidence:    signalConfidence,
		Provenance: artifacts.Provenance{
			SpanIDs:          []string{span.SpanID},
			EventIDs:         span.EventIDs,
			RawRefs:          span.RawRefs,
			SessionIDs:       []string{span.SessionID},
			SourceIDs:        []string{span.SourceID},
			EvidenceSnippets: s.extractEvidence(span),
		},
		Status:    artifacts.StatusProposed,
		CreatedAt: time.Now().UTC(),
	}

	return candidate
}

func (s *Service) detectType(ctx context.Context, content string) string {
	content = strings.ToLower(content)

	typeFlags := map[string]int{
		"MEMORY":    0,
		"KNOWLEDGE": 0,
	}

	patterns := map[string][]string{
		"MEMORY": {
			"remember", "preferred", "like to", "don't like", "hate when",
			"always forget", "never forget", "remind me", "tell me once",
			"my preference", "i prefer", "i hate", "i love when",
			"user prefers", "user likes", "user hates", "don't remind",
		},
		"KNOWLEDGE": {
			"architecture", "design", "implementation", "the system", "the api",
			"how do i", "how does", "what is the", "where is the",
			"the codebase", "the repo", "the project", "the code",
			"function", "class", "method", "file", "directory",
			"database", "config", "settings", "environment",
		},
	}

	for t, words := range patterns {
		for _, word := range words {
			if strings.Contains(content, word) {
				typeFlags[t]++
			}
		}
	}

	bestType := ""
	bestScore := 0

	typeNames := make([]string, 0, len(typeFlags))
	for name := range typeFlags {
		typeNames = append(typeNames, name)
	}
	sort.Strings(typeNames)

	for _, t := range typeNames {
		score := typeFlags[t]
		if score > bestScore {
			bestScore = score
			bestType = t
		}
	}

	if bestScore >= 2 {
		return bestType
	}

	return ""
}

func (s *Service) detectScope(ctx context.Context, content string) string {
	content = strings.ToLower(content)

	if strings.Contains(content, "org") || strings.Contains(content, "company") ||
		strings.Contains(content, "team") || strings.Contains(content, "everyone") {
		return string(artifacts.ScopeOrg)
	}

	if strings.Contains(content, "project") || strings.Contains(content, "workspace") ||
		strings.Contains(content, "repo") {
		return string(artifacts.ScopeProject)
	}

	return string(artifacts.ScopeAgent)
}

func (s *Service) detectConfidence(ctx context.Context, content string) string {
	content = strings.ToLower(content)

	highConfidence := []string{"always", "never", "definitely", "explicitly", "clearly", "definitely not"}
	lowConfidence := []string{"maybe", "probably", "might", "could be", "perhaps", "i think", "i guess"}

	for _, phrase := range highConfidence {
		if strings.Contains(content, phrase) {
			return artifacts.ConfidenceHigh
		}
	}

	for _, phrase := range lowConfidence {
		if strings.Contains(content, phrase) {
			return artifacts.ConfidenceLow
		}
	}

	return artifacts.ConfidenceMid
}

func (s *Service) generateTitle(artifactType string, content string) string {
	content = strings.TrimSpace(content)
	preview := content
	if len(preview) > 100 {
		preview = preview[:100]
		lastSpace := strings.LastIndex(preview, " ")
		if lastSpace > 0 {
			preview = preview[:lastSpace]
		}
		preview += "..."
	}

	return fmt.Sprintf("[%s] %s", artifactType, preview)
}

func (s *Service) generateBody(artifactType string, content string) string {
	lines := strings.Split(content, "\n")
	var significantLines []string
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 20 {
			significantLines = append(significantLines, fmt.Sprintf("%d: %s", i+1, line))
		}
	}

	body := strings.Join(significantLines, "\n")
	if len(body) > 2000 {
		body = body[:2000] + "\n...(truncated)"
	}
	return body
}

func (s *Service) extractEvidence(span artifacts.EventSpan) []string {
	var evidence []string
	for _, ref := range span.RawRefs {
		ref = strings.TrimSpace(ref)
		if len(ref) > 10 && len(ref) < 1000 {
			evidence = append(evidence, ref)
		}
	}

	maxEvidence := 5
	if len(evidence) > maxEvidence {
		evidence = evidence[:maxEvidence]
	}

	return evidence
}

func generateCandidateID() string {
	return uuid.New().String()
}

func readContentFromRef(ref string) (string, error) {
	idx := strings.LastIndex(ref, ":")
	if idx == -1 {
		return "", fmt.Errorf("invalid ref format: no colon separator")
	}

	filePath := ref[:idx]
	offsetStr := ref[idx+1:]
	offset := int64(0)
	if _, err := fmt.Sscanf(offsetStr, "%d", &offset); err != nil {
		return "", fmt.Errorf("parse offset: %w", err)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek to offset: %w", err)
	}

	reader := bufio.NewReader(file)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read line: %w", err)
	}

	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("empty line at offset")
	}

	content := extractContentFromGenericEvent(line)
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("no content field in event")
	}

	return content, nil
}

func extractContentFromGenericEvent(line string) string {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return ""
	}

	if content, ok := event["content"]; ok {
		if text := normalizeContentValue(content); strings.TrimSpace(text) != "" {
			return text
		}
	}
	for _, key := range []string{"text", "message", "body"} {
		if v, ok := event[key]; ok {
			if text := normalizeContentValue(v); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}

	if payload, ok := event["payload"].(map[string]any); ok {
		if text := normalizeContentValue(payload["content"]); strings.TrimSpace(text) != "" {
			return text
		}
	}

	if data, ok := event["data"].(map[string]any); ok {
		if text := normalizeContentValue(data["content"]); strings.TrimSpace(text) != "" {
			return text
		}
	}

	return ""
}

func normalizeContentValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			switch p := item.(type) {
			case string:
				if strings.TrimSpace(p) != "" {
					parts = append(parts, strings.TrimSpace(p))
				}
			case map[string]any:
				if text, ok := p["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, strings.TrimSpace(text))
				}
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if text, ok := t["text"].(string); ok {
			return strings.TrimSpace(text)
		}
	}
	return ""
}
