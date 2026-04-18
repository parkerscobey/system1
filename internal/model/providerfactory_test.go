package model

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/XferOps/system1/internal/config"
)

func TestNewProviderOracle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		ModelProvider: "oracle",
		OracleEngine: "api",
		OracleModel:  "sonnet",
	}

	provider, err := NewProvider(cfg, logger)
	if err != nil {
		t.Fatalf("NewProvider(oracle) error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider, got nil")
	}
	if provider.Name() != "oracle" {
		t.Errorf("provider name = %q, expected oracle", provider.Name())
	}
}

func TestNewProviderOpenRouter(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		ModelProvider:   "openrouter",
		OpenRouterAPIKey: "test-key",
		OpenRouterModel:  "anthropic/claude-3-sonnet",
	}

	provider, err := NewProvider(cfg, logger)
	if err != nil {
		t.Fatalf("NewProvider(openrouter) error: %v", err)
	}
	if provider == nil {
		t.Fatal("expected provider, got nil")
	}
	if provider.Name() != "openrouter" {
		t.Errorf("provider name = %q, expected openrouter", provider.Name())
	}
}

func TestNewProviderUnknown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		ModelProvider: "unknown-provider",
	}

	_, err := NewProvider(cfg, logger)
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
}

func TestNewProviderEmpty(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		ModelProvider: "",
	}

	provider, err := NewProvider(cfg, logger)
	if err != nil {
		t.Fatalf("NewProvider('') error: %v", err)
	}
	if provider != nil {
		t.Error("expected nil provider for empty string")
	}
}

func TestNewProviderNone(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.Config{
		ModelProvider: "none",
	}

	provider, err := NewProvider(cfg, logger)
	if err != nil {
		t.Fatalf("NewProvider(none) error: %v", err)
	}
	if provider != nil {
		t.Error("expected nil provider for none")
	}
}

var _ = context.Background