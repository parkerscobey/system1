package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	StateDir        string
	ArtifactsDir    string
	SQLitePath      string
	LogLevel        string
	LogFormat       string
	EnabledTypes    []string
	DefaultPassMode string
	SessionLogPath  string
	EnableDebug     bool
	UseMCPServer    bool
	BackendType     string
	HizalProjectID  string

	// Model provider configuration
	ModelProvider     string        // default: "none"
	OracleEngine      string        // default: "api"
	OracleModel       string        // default: empty (uses oracle default)
	OpenRouterAPIKey  string        // required when provider=openrouter
	OpenRouterModel   string        // required when provider=openrouter
	OpenRouterBaseURL string        // default: "https://openrouter.ai/api/v1"
	OpenRouterAppName string        // optional app name metadata (X-Title)
	OpenRouterSiteURL string        // optional site metadata (HTTP-Referer)
	ModelTimeout      time.Duration // default: 30 seconds
}

func Load() (Config, error) {
	stateDir := envOr("SYSTEM1_STATE_DIR", filepath.Join(userHomeDir(), ".system1"))
	artifactDir := filepath.Join(stateDir, "artifacts")

	cfg := Config{
		StateDir:        stateDir,
		ArtifactsDir:    artifactDir,
		SQLitePath:      filepath.Join(stateDir, "system1.db"),
		LogLevel:        envOr("SYSTEM1_LOG_LEVEL", "info"),
		LogFormat:       envOr("SYSTEM1_LOG_FORMAT", "text"),
		EnabledTypes:    envCSV("SYSTEM1_ENABLED_TYPES", []string{"MEMORY", "KNOWLEDGE"}),
		DefaultPassMode: envOr("SYSTEM1_INTROSPECTION_MODE", "reflective"),
		SessionLogPath:  envOr("SYSTEM1_SESSION_LOG_PATH", filepath.Join(stateDir, "sessions.jsonl")),
		EnableDebug:     strings.EqualFold(envOr("SYSTEM1_DEBUG", "false"), "true"),
		UseMCPServer:    true,
		BackendType:     envOr("SYSTEM1_BACKEND_TYPE", "file"),
		HizalProjectID:  envOr("SYSTEM1_HIZAL_PROJECT_ID", ""),

		// Model provider configuration
		ModelProvider:     envOr("SYSTEM1_MODEL_PROVIDER", "none"),
		OracleEngine:      envOr("SYSTEM1_ORACLE_ENGINE", "api"),
		OracleModel:       envOr("SYSTEM1_ORACLE_MODEL", ""),
		OpenRouterAPIKey:  envOr("SYSTEM1_OPENROUTER_API_KEY", ""),
		OpenRouterModel:   envOr("SYSTEM1_OPENROUTER_MODEL", ""),
		OpenRouterBaseURL: envOr("SYSTEM1_OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"),
		OpenRouterAppName: envOr("SYSTEM1_OPENROUTER_APP_NAME", ""),
		OpenRouterSiteURL: envOr("SYSTEM1_OPENROUTER_SITE_URL", ""),
		ModelTimeout:      envDuration("SYSTEM1_MODEL_TIMEOUT", 30*time.Second),
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if c.StateDir == "" {
		return fmt.Errorf("state dir is required")
	}
	if c.SQLitePath == "" {
		return fmt.Errorf("sqlite path is required")
	}
	if len(c.EnabledTypes) == 0 {
		return fmt.Errorf("at least one enabled type is required")
	}
	if c.BackendType != "file" && c.BackendType != "hizal" {
		return fmt.Errorf("invalid backend type %q: must be one of \"file\", \"hizal\"", c.BackendType)
	}
	if c.BackendType == "hizal" && c.HizalProjectID == "" {
		return fmt.Errorf("hizal backend requires HizalProjectID to be set (SYSTEM1_HIZAL_PROJECT_ID)")
	}

	switch c.ModelProvider {
	case "", "none", "oracle":
		// valid
	case "openrouter":
		if strings.TrimSpace(c.OpenRouterAPIKey) == "" {
			return fmt.Errorf("openrouter provider requires OpenRouterAPIKey to be set (SYSTEM1_OPENROUTER_API_KEY)")
		}
		if strings.TrimSpace(c.OpenRouterModel) == "" {
			return fmt.Errorf("openrouter provider requires OpenRouterModel to be set (SYSTEM1_OPENROUTER_MODEL)")
		}
	default:
		return fmt.Errorf("unknown model provider %q: must be one of \"none\", \"oracle\", \"openrouter\"", c.ModelProvider)
	}

	return nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envCSV(key string, fallback []string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}

	// Try parsing as seconds first (integer)
	if seconds, err := strconv.Atoi(v); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Try parsing as duration string
	if duration, err := time.ParseDuration(v); err == nil {
		return duration
	}

	return fallback
}

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
