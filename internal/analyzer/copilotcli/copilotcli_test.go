package copilotcli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestShutdownMetricsOverrideEstimatedTokens(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "session-state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "events.jsonl")
	content := `{"type":"session.start","timestamp":"2026-05-16T00:00:00Z","data":{"sessionId":"s1","context":{"cwd":"/repo","model":"generic-copilot/litellm/anthropic/claude-haiku-4.5"}}}
{"type":"user.message","timestamp":"2026-05-16T00:00:01Z","data":{"content":"hello"}}
{"type":"assistant.message","timestamp":
{"type":"assistant.message","timestamp":"2026-05-16T00:00:02Z","data":{"content":"world"}}
{"type":"tool.execution_start","timestamp":"2026-05-16T00:00:03Z","data":{"toolName":"read_file","arguments":{"path":"main.go"}}}
{"type":"assistant.turn_end","timestamp":"2026-05-16T00:00:04Z","data":{}}
{"type":"session.shutdown","timestamp":"2026-05-16T00:00:05Z","data":{"modelMetrics":{"claude-haiku-4.5":{"usage":{"inputTokens":100,"outputTokens":50,"cacheReadTokens":20,"cacheWriteTokens":5}}}}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := New(root).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	assistant := records[1]
	if assistant.Model != "claude-haiku-4.5" {
		t.Fatalf("model = %q", assistant.Model)
	}
	if assistant.Tokens.Input != 100 || assistant.Tokens.Output != 50 || assistant.Tokens.CacheRead != 20 || assistant.Tokens.CacheCreation != 5 || assistant.Tokens.Cached != 20 {
		t.Fatalf("shutdown metrics not applied: %+v", assistant.Tokens)
	}
	if assistant.ToolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", assistant.ToolCalls)
	}
}

func TestToolArgumentsDoNotOverrideModel(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "session-state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "events.jsonl")
	content := `{"type":"session.start","timestamp":"2026-05-16T00:00:00Z","data":{"sessionId":"s1","context":{"model":"claude-sonnet-4"}}}
{"type":"user.message","timestamp":"2026-05-16T00:00:01Z","data":{"content":"hello"}}
{"type":"tool.execution_start","timestamp":"2026-05-16T00:00:02Z","data":{"toolName":"run","arguments":{"model":"accidental-argument-model"}}}
{"type":"assistant.message","timestamp":"2026-05-16T00:00:03Z","data":{"content":"world"}}
{"type":"assistant.turn_end","timestamp":"2026-05-16T00:00:04Z","data":{}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := New(root).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := records[1].Model, "claude-sonnet-4"; got != want {
		t.Fatalf("assistant model = %q, want %q", got, want)
	}
}

func TestShutdownMetricsApplyOnlyToCurrentSegment(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, "session-state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "events.jsonl")
	data := `{"type":"session.start","data":{"sessionId":"s1","context":{"model":"claude-sonnet-4"}}}
{"type":"user.message","data":{"content":"one"}}
{"type":"assistant.turn_end","data":{}}
{"type":"session.shutdown","data":{"modelMetrics":{"claude-sonnet-4":{"usage":{"inputTokens":100,"outputTokens":10}}}}}
{"type":"user.message","data":{"content":"two"}}
{"type":"assistant.turn_end","data":{}}
{"type":"session.shutdown","data":{"modelMetrics":{"claude-sonnet-4":{"usage":{"inputTokens":200,"outputTokens":20}}}}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := New(root).Parse(t.Context(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 4 || records[1].Tokens.Input != 100 || records[1].Tokens.Output != 10 || records[3].Tokens.Input != 200 || records[3].Tokens.Output != 20 {
		t.Fatalf("shutdown segments = %+v", records)
	}
}
