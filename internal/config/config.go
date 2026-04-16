package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func userHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}
