package model

import "time"

const (
	ToolClaudeCode    = "claude_code"
	ToolCodexCLI      = "codex_cli"
	ToolCopilotCLI    = "copilot_cli"
	ToolGitHubCopilot = "github_copilot"
	ToolGeminiCLI     = "gemini_cli"

	RoleUser      = "user"
	RoleAssistant = "assistant"

	UnknownModel = "unknown"
)

var TokenTypes = []string{"input", "output", "reasoning", "cache_creation", "cache_read", "cached"}

type Source struct {
	Path string
}

type TokenStats struct {
	// Input is gross input tokens, including tokens served from cache when the source reports them.
	Input         uint64
	Output        uint64
	Reasoning     uint64
	CacheCreation uint64
	CacheRead     uint64
	// Cached is cache hit/reused input tokens. It intentionally does not include cache creation tokens.
	Cached uint64
}

type Record struct {
	Tool      string
	Model     string
	SessionID string
	Role      string
	Tokens    TokenStats
	ToolCalls uint64
}

type SeriesKey struct {
	Tool  string
	Model string
}

type Aggregate struct {
	Tokens    TokenStats
	Messages  map[string]uint64
	ToolCalls uint64
}

type ToolSnapshot struct {
	SourceFiles uint64
	ParseErrors uint64
	Sessions    uint64
}

type Snapshot struct {
	Aggregates         map[SeriesKey]Aggregate
	Tools              map[string]ToolSnapshot
	LastScan           time.Time
	LastSuccessfulScan time.Time
	ScanDuration       time.Duration
	LastScanSuccess    bool
	Version            string
	Commit             string
}

func NormalizeModel(value string) string {
	if value == "" {
		return UnknownModel
	}
	return value
}
