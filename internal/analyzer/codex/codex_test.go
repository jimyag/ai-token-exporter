package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestParseLastUsageAndTotalDelta(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config.toml"), []byte(`model = "gpt-4.1"`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "sessions", "session.jsonl")
	content := `{"timestamp":"2026-05-16T00:00:00Z","type":"session_meta","payload":{"model":"gpt-4.1"}}
{"timestamp":"2026-05-16T00:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20,"cached_input_tokens":40,"reasoning_output_tokens":5,"total_tokens":120}}}}
{"timestamp":"2026-05-16T00:00:01.5Z","type":"event_msg","payload":
{"timestamp":"2026-05-16T00:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"output_tokens":30,"cached_input_tokens":60,"reasoning_output_tokens":7,"total_tokens":180}}}}
{"timestamp":"2026-05-16T00:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":8,"cached_input_tokens":5,"reasoning_output_tokens":3,"total_tokens":28}}}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := New(root).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 3; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	if records[0].Tokens.Input != 100 || records[0].Tokens.Cached != 40 {
		t.Fatalf("first total usage not parsed correctly: %+v", records[0].Tokens)
	}
	if records[1].Tokens.Input != 50 || records[1].Tokens.Output != 10 || records[1].Tokens.Cached != 20 || records[1].Tokens.Reasoning != 2 {
		t.Fatalf("total delta not parsed correctly: %+v", records[1].Tokens)
	}
	if records[2].Tokens.Input != 20 || records[2].Tokens.Output != 8 || records[2].Tokens.Cached != 5 || records[2].Tokens.Reasoning != 3 {
		t.Fatalf("last usage not parsed correctly: %+v", records[2].Tokens)
	}
}
