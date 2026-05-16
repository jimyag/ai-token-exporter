package backfill

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestWritePrometheusImportCumulativeSamples(t *testing.T) {
	start := time.Date(2026, 5, 16, 10, 0, 0, 0, time.UTC)
	records := []model.Record{
		{
			Tool:      model.ToolCodexCLI,
			Model:     "gpt-5",
			SessionID: "session-a",
			Role:      model.RoleUser,
			Timestamp: start,
		},
		{
			Tool:      model.ToolCodexCLI,
			Model:     "gpt-5",
			SessionID: "session-a",
			Role:      model.RoleAssistant,
			Timestamp: start.Add(30 * time.Second),
			Tokens: model.TokenStats{
				Input:  100,
				Output: 20,
				Cached: 40,
			},
			ToolCalls: 2,
		},
		{
			Tool:      model.ToolCodexCLI,
			Model:     "gpt-5",
			SessionID: "session-b",
			Role:      model.RoleAssistant,
			Timestamp: start.Add(90 * time.Second),
			Tokens: model.TokenStats{
				Input: 50,
			},
		},
	}

	var out bytes.Buffer
	samples, err := WritePrometheusImport(&out, records, Options{
		From:     start,
		To:       start.Add(2 * time.Minute),
		Step:     time.Minute,
		Job:      "ai-token-exporter",
		Instance: "127.0.0.1:9108",
		Hostname: "local-dev",
		Version:  "v0.1.0",
		Commit:   "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if samples == 0 {
		t.Fatal("expected samples")
	}

	got := out.String()
	assertContains(t, got, `ai_token_exporter_tokens{hostname="local-dev",instance="127.0.0.1:9108",job="ai-token-exporter",model="gpt-5",token_type="input",tool="codex_cli"} 100 1778925660000`)
	assertContains(t, got, `ai_token_exporter_tokens{hostname="local-dev",instance="127.0.0.1:9108",job="ai-token-exporter",model="gpt-5",token_type="input",tool="codex_cli"} 150 1778925720000`)
	assertContains(t, got, `ai_token_exporter_messages{hostname="local-dev",instance="127.0.0.1:9108",job="ai-token-exporter",model="gpt-5",role="user",tool="codex_cli"} 1 1778925600000`)
	assertContains(t, got, `ai_token_exporter_sessions{hostname="local-dev",instance="127.0.0.1:9108",job="ai-token-exporter",tool="codex_cli"} 2 1778925720000`)
	assertContains(t, got, `ai_token_exporter_build_info{commit="abc123",hostname="local-dev",instance="127.0.0.1:9108",job="ai-token-exporter",version="v0.1.0"} 1 1778925600000`)
	if strings.Contains(got, `token_type="reasoning"`) {
		t.Fatalf("zero reasoning token sample should not be emitted:\n%s", got)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("missing line:\n%s\n\noutput:\n%s", want, got)
	}
}
