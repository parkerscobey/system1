package model

import (
	"fmt"
	"log/slog"

	"github.com/XferOps/system1/internal/config"
)

func NewProvider(cfg config.Config, logger *slog.Logger) (Provider, error) {
	switch cfg.ModelProvider {
	case "oracle":
		return NewOracleProvider(OracleConfig{
			Engine:  cfg.OracleEngine,
			Model:   cfg.OracleModel,
			Timeout: cfg.ModelTimeout,
			Logger:  logger,
		}), nil
	case "openrouter":
		return NewOpenRouterProvider(OpenRouterConfig{
			APIKey:  cfg.OpenRouterAPIKey,
			Model:   cfg.OpenRouterModel,
			BaseURL: cfg.OpenRouterBaseURL,
			AppName: cfg.OpenRouterAppName,
			SiteURL: cfg.OpenRouterSiteURL,
			Timeout: cfg.ModelTimeout,
			Logger:  logger,
		}), nil
	case "", "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown model provider: %q (supported: oracle, openrouter)", cfg.ModelProvider)
	}
}