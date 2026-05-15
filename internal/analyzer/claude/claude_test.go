package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestParseUsageCacheAndDedup(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "project-a")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session.jsonl")
	content := `{"type":"user","uuid":"u1","timestamp":"2026-05-16T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"assistant","uuid":"a1","requestId":"req1","timestamp":"2026-05-16T00:00:01Z","message":{"id":"msg1","role":"assistant","model":"claude-sonnet-4","content":[{"type":"tool_use","name":"Read","input":{}}],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":3,"cache_read_input_tokens":2}}}
{"type":"assistant","uuid":"a2","requestId":"req1","timestamp":"2026-05-16T00:00:02Z","message":{"id":"msg1","role":"assistant","model":"claude-sonnet-4","content":[],"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":3,"cache_read_input_tokens":2}}}
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
	if assistant.Tokens.Input != 10 || assistant.Tokens.Output != 5 {
		t.Fatalf("unexpected token stats: %+v", assistant.Tokens)
	}
	if assistant.Tokens.CacheCreation != 3 || assistant.Tokens.CacheRead != 2 || assistant.Tokens.Cached != 5 {
		t.Fatalf("unexpected cache stats: %+v", assistant.Tokens)
	}
	if assistant.ToolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", assistant.ToolCalls)
	}
}
