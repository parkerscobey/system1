package model

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewOracleProvider(t *testing.T) {
	t.Run("default config", func(t *testing.T) {
		provider := NewOracleProvider(OracleConfig{})

		if provider.config.BinaryPath != "/opt/homebrew/bin/oracle" {
			t.Errorf("BinaryPath = %q, expected %q", provider.config.BinaryPath, "/opt/homebrew/bin/oracle")
		}

		if provider.config.Engine != "api" {
			t.Errorf("Engine = %q, expected %q", provider.config.Engine, "api")
		}

		if provider.config.Timeout != 30*time.Second {
			t.Errorf("Timeout = %v, expected %v", provider.config.Timeout, 30*time.Second)
		}

		if provider.Name() != "oracle" {
			t.Errorf("Name() = %q, expected %q", provider.Name(), "oracle")
		}
	})

	t.Run("custom config", func(t *testing.T) {
		config := OracleConfig{
			BinaryPath: "/custom/path/oracle",
			Engine:     "local",
			Model:      "custom-model",
			Timeout:    60 * time.Second,
		}

		provider := NewOracleProvider(config)

		if provider.config.BinaryPath != "/custom/path/oracle" {
			t.Errorf("BinaryPath = %q, expected %q", provider.config.BinaryPath, "/custom/path/oracle")
		}

		if provider.config.Engine != "local" {
			t.Errorf("Engine = %q, expected %q", provider.config.Engine, "local")
		}

		if provider.config.Model != "custom-model" {
			t.Errorf("Model = %q, expected %q", provider.config.Model, "custom-model")
		}

		if provider.config.Timeout != 60*time.Second {
			t.Errorf("Timeout = %v, expected %v", provider.config.Timeout, 60*time.Second)
		}
	})
}

func TestOracleProviderHealth(t *testing.T) {
	ctx := context.Background()

	t.Run("binary not found", func(t *testing.T) {
		provider := NewOracleProvider(OracleConfig{
			BinaryPath: "/nonexistent/oracle",
		})

		err := provider.Health(ctx)
		if err == nil {
			t.Error("Health() should return error when binary not found")
		}

		if !strings.Contains(err.Error(), "oracle binary not found") {
			t.Errorf("Health() error = %v, should contain 'oracle binary not found'", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		// Create a mock oracle script that sleeps
		tempDir := t.TempDir()
		mockOracle := filepath.Join(tempDir, "oracle")
		script := "#!/bin/bash\nsleep 2\necho 'success'"

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
			Timeout:    100 * time.Millisecond, // Very short timeout
		})

		err := provider.Health(ctx)
		if err == nil {
			t.Error("Health() should return timeout error")
		}

		if !strings.Contains(err.Error(), "health check failed") {
			t.Errorf("Health() error = %v, should contain 'health check failed'", err)
		}
	})
}

func TestOracleProviderComplete(t *testing.T) {
	ctx := context.Background()

	// Create a temporary directory for mock oracle
	tempDir := t.TempDir()
	mockOracle := filepath.Join(tempDir, "oracle")

	t.Run("successful completion", func(t *testing.T) {
		// Create mock oracle that echoes the prompt
		script := `#!/bin/bash
# Simple mock that returns the prompt as response
echo "Mock response to: $*"`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
			Engine:     "test",
			Model:      "test-model",
		})

		resp, err := provider.Complete(ctx, "test prompt", "")
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		if resp.Text == "" {
			t.Error("Complete() returned empty response text")
		}

		if resp.Metadata.Provider != "oracle" {
			t.Errorf("Response provider = %q, expected %q", resp.Metadata.Provider, "oracle")
		}

		if resp.Metadata.Model != "test-model" {
			t.Errorf("Response model = %q, expected %q", resp.Metadata.Model, "test-model")
		}

		if resp.Metadata.Duration == "" {
			t.Error("Response duration should not be empty")
		}
	})

	t.Run("structured output parsing", func(t *testing.T) {
		// Create mock oracle that returns JSON
		script := `#!/bin/bash
echo '{"type": "test", "confidence": 0.9}'`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
		})

		resp, err := provider.Complete(ctx, "test prompt", "", WithStructuredOutput())
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		if len(resp.Structured) == 0 {
			t.Error("Complete() should have parsed structured output")
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(resp.Structured, &parsed); err != nil {
			t.Errorf("Failed to parse structured output: %v", err)
		}

		if parsed["type"] != "test" {
			t.Errorf("Structured output type = %v, expected 'test'", parsed["type"])
		}
	})

	t.Run("malformed structured output", func(t *testing.T) {
		// Create mock oracle that returns invalid JSON
		script := `#!/bin/bash
echo 'not valid json{'`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
			Logger:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
		})

		resp, err := provider.Complete(ctx, "test prompt", "", WithStructuredOutput())
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		// Should still return response with text, but no structured data
		if resp.Text == "" {
			t.Error("Complete() should still return text even with malformed JSON")
		}

		if len(resp.Structured) > 0 {
			t.Error("Complete() should not have structured output for malformed JSON")
		}
	})

	t.Run("command failure", func(t *testing.T) {
		// Create mock oracle that exits with error
		script := `#!/bin/bash
echo "Error message" >&2
exit 1`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
		})

		_, err := provider.Complete(ctx, "test prompt", "")
		if err == nil {
			t.Error("Complete() should return error when oracle command fails")
		}

		if !strings.Contains(err.Error(), "oracle command failed") {
			t.Errorf("Complete() error = %v, should contain 'oracle command failed'", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		// Create mock oracle that sleeps
		script := `#!/bin/bash
sleep 2
echo "response"`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
			Timeout:    100 * time.Millisecond,
		})

		_, err := provider.Complete(ctx, "test prompt", "")
		if err == nil {
			t.Error("Complete() should return timeout error")
		}
	})

	t.Run("system prompt file creation", func(t *testing.T) {
		// Create mock oracle that outputs the file contents
		script := `#!/bin/bash
if [ "$1" = "-p" ] && [ "$3" = "--file" ]; then
    cat "$4"
else
    echo "$2"
fi`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
		})

		resp, err := provider.Complete(ctx, "user prompt", "system prompt")
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		if !strings.Contains(resp.Text, "SYSTEM: system prompt") {
			t.Errorf("Response should contain system prompt, got: %s", resp.Text)
		}

		if !strings.Contains(resp.Text, "USER: user prompt") {
			t.Errorf("Response should contain user prompt, got: %s", resp.Text)
		}
	})

	t.Run("custom model option", func(t *testing.T) {
		// Create mock oracle that echoes arguments
		script := `#!/bin/bash
echo "Args: $*"`

		if err := os.WriteFile(mockOracle, []byte(script), 0755); err != nil {
			t.Fatalf("Failed to create mock oracle: %v", err)
		}

		provider := NewOracleProvider(OracleConfig{
			BinaryPath: mockOracle,
			Engine:     "test-engine",
			Model:      "default-model",
		})

		resp, err := provider.Complete(ctx, "test", "", WithModel("custom-model"))
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		// Should use custom model instead of default
		if !strings.Contains(resp.Text, "custom-model") {
			t.Errorf("Response should contain custom model, got: %s", resp.Text)
		}

		if resp.Metadata.Model != "custom-model" {
			t.Errorf("Response metadata model = %q, expected %q", resp.Metadata.Model, "custom-model")
		}
	})
}

func TestCreatePromptFile(t *testing.T) {
	provider := NewOracleProvider(OracleConfig{})

	t.Run("no system prompt", func(t *testing.T) {
		file, err := provider.createPromptFile("user prompt", "")
		if err != nil {
			t.Errorf("createPromptFile() returned error: %v", err)
		}
		if file != "" {
			t.Errorf("createPromptFile() should return empty string when no system prompt, got: %s", file)
		}
	})

	t.Run("with system prompt", func(t *testing.T) {
		file, err := provider.createPromptFile("user prompt", "system prompt")
		if err != nil {
			t.Errorf("createPromptFile() returned error: %v", err)
		}
		if file == "" {
			t.Error("createPromptFile() should return file path when system prompt provided")
		}

		// Clean up
		defer func() { _ = os.Remove(file) }()

		// Check file contents
		content, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("Failed to read prompt file: %v", err)
		}

		contentStr := string(content)
		if !strings.Contains(contentStr, "SYSTEM: system prompt") {
			t.Errorf("File should contain system prompt, got: %s", contentStr)
		}

		if !strings.Contains(contentStr, "USER: user prompt") {
			t.Errorf("File should contain user prompt, got: %s", contentStr)
		}
	})
}

// TestOracleIntegration tests with a real oracle binary if available
func TestOracleIntegration(t *testing.T) {
	// Skip if ORACLE_INTEGRATION_TEST is not set
	if os.Getenv("ORACLE_INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test (set ORACLE_INTEGRATION_TEST to run)")
	}

	// Check if oracle binary exists
	oraclePath := "/opt/homebrew/bin/oracle"
	if _, err := os.Stat(oraclePath); os.IsNotExist(err) {
		t.Skipf("Oracle binary not found at %s", oraclePath)
	}

	provider := NewOracleProvider(OracleConfig{})
	ctx := context.Background()

	t.Run("health check", func(t *testing.T) {
		if err := provider.Health(ctx); err != nil {
			t.Errorf("Health check failed: %v", err)
		}
	})

	t.Run("simple completion", func(t *testing.T) {
		resp, err := provider.Complete(ctx, "Say 'hello world'", "")
		if err != nil {
			t.Errorf("Complete() returned error: %v", err)
		}

		if resp.Text == "" {
			t.Error("Complete() returned empty response")
		}

		t.Logf("Oracle response: %s", resp.Text)
	})
}
