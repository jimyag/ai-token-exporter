package config

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Config struct {
	Listen          string
	ScanInterval    time.Duration
	Enabled         map[string]bool
	ClaudeDir       string
	CodexDir        string
	CopilotDir      string
	GeminiDir       string
	GeminiConfigDir string
	VSCodeConfigDir string
	Version         string
	Commit          string
}

func Load(args []string) (Config, error) {
	home, _ := os.UserHomeDir()
	cfg := Config{
		Listen:          envString("AI_TOKEN_EXPORTER_LISTEN", ":9108"),
		ScanInterval:    envDuration("AI_TOKEN_EXPORTER_SCAN_INTERVAL", 30*time.Second),
		Enabled:         parseEnabled(envString("AI_TOKEN_EXPORTER_ENABLED", "claude_code,codex_cli,copilot_cli,github_copilot,gemini_cli")),
		ClaudeDir:       envString("AI_TOKEN_EXPORTER_CLAUDE_DIR", filepath.Join(home, ".claude", "projects")),
		CodexDir:        envString("AI_TOKEN_EXPORTER_CODEX_DIR", filepath.Join(home, ".codex")),
		CopilotDir:      envString("AI_TOKEN_EXPORTER_COPILOT_DIR", filepath.Join(home, ".copilot")),
		GeminiDir:       envString("AI_TOKEN_EXPORTER_GEMINI_DIR", filepath.Join(home, ".gemini", "tmp")),
		GeminiConfigDir: envString("AI_TOKEN_EXPORTER_GEMINI_CONFIG_DIR", filepath.Join(home, ".gemini")),
		VSCodeConfigDir: envString("AI_TOKEN_EXPORTER_VSCODE_CONFIG_DIR", ""),
		Version:         "dev",
		Commit:          "none",
	}

	var enabled string
	fs := flag.NewFlagSet("ai-token-exporter", flag.ContinueOnError)
	fs.StringVar(&cfg.Listen, "listen", cfg.Listen, "listen address")
	fs.DurationVar(&cfg.ScanInterval, "scan-interval", cfg.ScanInterval, "scan interval")
	fs.StringVar(&enabled, "enabled", joinEnabled(cfg.Enabled), "comma-separated enabled tools")
	fs.StringVar(&cfg.ClaudeDir, "claude-dir", cfg.ClaudeDir, "Claude Code projects directory")
	fs.StringVar(&cfg.CodexDir, "codex-dir", cfg.CodexDir, "Codex home directory")
	fs.StringVar(&cfg.CopilotDir, "copilot-dir", cfg.CopilotDir, "Copilot home directory")
	fs.StringVar(&cfg.GeminiDir, "gemini-dir", cfg.GeminiDir, "Gemini CLI tmp directory")
	fs.StringVar(&cfg.GeminiConfigDir, "gemini-config-dir", cfg.GeminiConfigDir, "Gemini CLI config directory")
	fs.StringVar(&cfg.VSCodeConfigDir, "vscode-config-dir", cfg.VSCodeConfigDir, "VS Code-compatible editor config directory")
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
	sort.Strings(values)
	return strings.Join(values, ",")
}
