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
	"github.com/google/uuid"
)

var ErrNoEnabledTypes = fmt.Errorf("no enabled types configured")

type Service struct {
	logger       *slog.Logger
	cfg          config.Config
	enabledTypes map[string]bool
}

func NewService(logger *slog.Logger, cfg config.Config) *Service {
	enabled := make(map[string]bool)
	for _, t := range cfg.EnabledTypes {
		enabled[strings.ToUpper(t)] = true
	}
	return &Service{logger: logger, cfg: cfg, enabledTypes: enabled}
}

func (s *Service) Extract(ctx context.Context, span artifacts.EventSpan) ([]artifacts.CandidateArtifact, error) {
	s.logger.DebugContext(ctx, "extract requested", slog.String("span_id", span.SpanID))

	if len(s.cfg.EnabledTypes) == 0 {
		return nil, ErrNoEnabledTypes
	}

	candidates := s.detectCandidates(ctx, span)
	s.logger.InfoContext(ctx, "extraction complete",
		slog.String("span_id", span.SpanID),
		slog.Int("candidates", len(candidates)),
	)

	return candidates, nil
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

	signalType := s.detectType(ctx, rawContent)
	if signalType == "" {
		s.logger.DebugContext(ctx, "no signal detected in span", slog.String("span_id", span.SpanID))
		return nil
	}

	signalScope := s.detectScope(ctx, rawContent)
	signalConfidence := s.detectConfidence(ctx, rawContent)

	spanText := s.summarizeSpan(span)
	candidate := &artifacts.CandidateArtifact{
		CandidateID:   generateCandidateID(),
		ArtifactType:  signalType,
		ProposedScope: signalScope,
		Title:         s.generateTitle(signalType, spanText),
		Body:          s.generateBody(signalType, spanText),
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

func (s *Service) summarizeSpan(span artifacts.EventSpan) string {
	if len(span.RawRefs) == 0 {
		return ""
	}

	firstRef := span.RawRefs[0]
	firstRef = strings.TrimSpace(firstRef)
	if len(firstRef) > 500 {
		firstRef = firstRef[:500] + "..."
	}
	return firstRef
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

	var event struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return "", fmt.Errorf("parse event JSON: %w", err)
	}

	if event.Content == "" {
		return "", fmt.Errorf("no content field in event")
	}

	return event.Content, nil
}
