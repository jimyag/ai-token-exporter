package config

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	Listen               string
	ScanInterval         time.Duration
	Enabled              map[string]bool
	IncludeSessionLabels bool
	ClaudeDir            string
	CodexDir             string
	CopilotDir           string
	Version              string
	Commit               string
}

func Load(args []string) (Config, error) {
	home, _ := os.UserHomeDir()
	cfg := Config{
		Listen:               envString("AI_TOKEN_EXPORTER_LISTEN", ":9108"),
		ScanInterval:         envDuration("AI_TOKEN_EXPORTER_SCAN_INTERVAL", 30*time.Second),
		Enabled:              parseEnabled(envString("AI_TOKEN_EXPORTER_ENABLED", "claude_code,codex_cli,copilot_cli,github_copilot")),
		IncludeSessionLabels: envBool("AI_TOKEN_EXPORTER_INCLUDE_SESSION_LABELS", true),
		ClaudeDir:            envString("AI_TOKEN_EXPORTER_CLAUDE_DIR", filepath.Join(home, ".claude", "projects")),
		CodexDir:             envString("AI_TOKEN_EXPORTER_CODEX_DIR", filepath.Join(home, ".codex")),
		CopilotDir:           envString("AI_TOKEN_EXPORTER_COPILOT_DIR", filepath.Join(home, ".copilot")),
		Version:              "dev",
		Commit:               "none",
	}

	var enabled string
	fs := flag.NewFlagSet("ai-token-exporter", flag.ContinueOnError)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address")
	fs.DurationVar(&cfg.ScanInterval, "scan-interval", cfg.ScanInterval, "scan interval")
	fs.StringVar(&enabled, "enabled", joinEnabled(cfg.Enabled), "comma-separated enabled tools")
	fs.BoolVar(&cfg.IncludeSessionLabels, "include-session-labels", cfg.IncludeSessionLabels, "include session_id and project_id labels")
	fs.StringVar(&cfg.ClaudeDir, "claude-dir", cfg.ClaudeDir, "Claude Code projects directory")
	fs.StringVar(&cfg.CodexDir, "codex-dir", cfg.CodexDir, "Codex home directory")
	fs.StringVar(&cfg.CopilotDir, "copilot-dir", cfg.CopilotDir, "Copilot home directory")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	cfg.Enabled = parseEnabled(enabled)
	return cfg, nil
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if parsed, err := time.ParseDuration(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value := strings.TrimSpace(strings.ToLower(os.Getenv(key))); value != "" {
		return value == "1" || value == "true" || value == "yes"
	}
	return fallback
}

func parseEnabled(value string) map[string]bool {
	enabled := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			enabled[item] = true
		}
	}
	return enabled
}

func joinEnabled(enabled map[string]bool) string {
	values := make([]string, 0, len(enabled))
	for key, ok := range enabled {
		if ok {
			values = append(values, key)
		}
	}
	return strings.Join(values, ",")
}
