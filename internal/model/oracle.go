package model

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// OracleConfig configures the Oracle CLI provider.
type OracleConfig struct {
	// Path to the oracle binary (defaults to /opt/homebrew/bin/oracle)
	BinaryPath string

	// Engine to use (defaults to "api")
	Engine string

	// Model to use (empty means oracle default)
	Model string

	// Timeout for oracle commands (defaults to 30 seconds)
	Timeout time.Duration

	// Logger for debugging
	Logger *slog.Logger
}

// OracleProvider implements Provider using the oracle CLI tool.
type OracleProvider struct {
	config OracleConfig
	logger *slog.Logger
}

// NewOracleProvider creates a new oracle CLI provider.
func NewOracleProvider(config OracleConfig) *OracleProvider {
	if config.BinaryPath == "" {
		config.BinaryPath = "/opt/homebrew/bin/oracle"
	}
	if config.Engine == "" {
		config.Engine = "api"
	}
	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}

	return &OracleProvider{
		config: config,
		logger: config.Logger,
	}
}

// Name returns the provider name.
func (p *OracleProvider) Name() string {
	return "oracle"
}

// Health checks if oracle is available and working.
func (p *OracleProvider) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	// Check if oracle binary exists
	if _, err := os.Stat(p.config.BinaryPath); os.IsNotExist(err) {
		return fmt.Errorf("oracle binary not found at %s", p.config.BinaryPath)
	}

	// Try a simple oracle command to verify it works
	cmd := exec.CommandContext(ctx, p.config.BinaryPath, "-p", "test")
	if p.config.Engine != "" {
		cmd.Args = append(cmd.Args, "--engine", p.config.Engine)
	}

	p.logger.DebugContext(ctx, "running oracle health check", slog.String("cmd", strings.Join(cmd.Args, " ")))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("oracle health check failed: %v (output: %s)", err, string(output))
	}

	p.logger.DebugContext(ctx, "oracle health check passed")
	return nil
}

// Complete generates a response using oracle CLI.
func (p *OracleProvider) Complete(ctx context.Context, prompt string, systemPrompt string, opts ...Option) (Response, error) {
	start := time.Now()
	options := applyOptions(opts)

	// Create temporary file for the prompt
	tempFile, err := p.createPromptFile(prompt, systemPrompt)
	if err != nil {
		return Response{}, fmt.Errorf("failed to create prompt file: %w", err)
	}
	defer func() { _ = os.Remove(tempFile) }()

	// Build oracle command
	args := []string{"-p", prompt}
	if tempFile != "" && systemPrompt != "" {
		args = append(args, "--file", tempFile)
	}
	if p.config.Engine != "" {
		args = append(args, "--engine", p.config.Engine)
	}

	// Use specific model if provided
	model := p.config.Model
	if options.model != "" {
		model = options.model
	}
	if model != "" {
		args = append(args, "--model", model)
	}

	// Wait for oracle to complete (not background mode)
	args = append(args, "--wait")

	ctx, cancel := context.WithTimeout(ctx, p.config.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.config.BinaryPath, args...)
	p.logger.DebugContext(ctx, "executing oracle command",
		slog.String("cmd", strings.Join(cmd.Args, " ")),
		slog.String("model", model),
		slog.Bool("structured", options.structured))

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return Response{}, fmt.Errorf("oracle command failed (exit code %d): %s", exitErr.ExitCode(), string(exitErr.Stderr))
		}
		return Response{}, fmt.Errorf("oracle command failed: %w", err)
	}

	duration := time.Since(start)
	responseText := strings.TrimSpace(string(output))

	// Strip oracle banner/preamble if present
	responseText = stripOracleBanner(responseText)

	response := Response{
		Text: responseText,
		Metadata: ResponseMetadata{
			Provider: "oracle",
			Model:    model,
			Duration: duration.String(),
		},
	}

	// If structured output was requested, try to parse the response as JSON
	if options.structured {
		var structuredData json.RawMessage
		if err := json.Unmarshal([]byte(responseText), &structuredData); err != nil {
			p.logger.WarnContext(ctx, "failed to parse structured output",
				slog.String("error", err.Error()),
				slog.String("response", responseText))
			// Don't return an error - just log the warning and return without structured data
		} else {
			response.Structured = structuredData
		}
	}

	p.logger.InfoContext(ctx, "oracle request completed",
		slog.String("duration", duration.String()),
		slog.Int("response_length", len(responseText)),
		slog.Bool("has_structured", len(response.Structured) > 0))

	return response, nil
}

// stripOracleBanner removes the oracle CLI banner and prompt echo from output.
// Oracle output format:
//
//	🧿 oracle 0.8.5 — <tagline>.
//	Reattach via: oracle session <slug>
//	Prompt:
//	<prompt echo>
//	---
//	<actual response>
func stripOracleBanner(output string) string {
	// If output doesn't start with the oracle banner, return as-is
	if !strings.HasPrefix(output, "🧿") {
		return output
	}
	// Look for the "---" separator that precedes the actual response
	if idx := strings.Index(output, "\n---\n"); idx >= 0 {
		return strings.TrimSpace(output[idx+5:])
	}
	// No separator found — strip banner lines and return remaining content
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			return strings.TrimSpace(strings.Join(lines[i+1:], "\n"))
		}
		if i > 0 && trimmed != "" && !strings.HasPrefix(line, "🧿") &&
			!strings.HasPrefix(line, "Reattach via:") && !strings.HasPrefix(line, "Prompt:") &&
			!strings.HasPrefix(line, "Pro runs") {
			return strings.TrimSpace(strings.Join(lines[i:], "\n"))
		}
	}
	return output
}

// createPromptFile creates a temporary file with the full prompt including system prompt.
func (p *OracleProvider) createPromptFile(prompt, systemPrompt string) (string, error) {
	if systemPrompt == "" {
		return "", nil // No need for file if no system prompt
	}

	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, fmt.Sprintf("oracle_prompt_%d.txt", time.Now().UnixNano()))

	var content strings.Builder
	if systemPrompt != "" {
		content.WriteString("SYSTEM: ")
		content.WriteString(systemPrompt)
		content.WriteString("\n\n")
	}
	content.WriteString("USER: ")
	content.WriteString(prompt)

	if err := os.WriteFile(tempFile, []byte(content.String()), 0600); err != nil {
		return "", fmt.Errorf("failed to write prompt file: %w", err)
	}

	return tempFile, nil
}
