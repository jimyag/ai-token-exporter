package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestParseModelChangesAndUsage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl")
	data := `{"type":"session","modelId":"initial","provider":"openai"}
{"type":"message","timestamp":"2026-09-24T00:00:00Z","message":{"role":"user","content":"hello"}}
{"type":"model_change","modelId":"next","provider":"anthropic"}
{"type":"message","timestamp":"2026-09-24T00:00:01Z","message":{"role":"assistant","content":[{"type":"text","text":"done"},{"type":"toolCall","name":"read"}],"usage":{"input":10,"output":5,"cacheRead":3,"cacheWrite":2}}}
{"type":"message","timestamp":"2026-09-24T00:00:02Z","message":{"role":"toolResult"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := New(root).Parse(t.Context(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Model != "openai/initial" || records[1].Model != "anthropic/next" {
		t.Fatalf("unexpected model/role records: %+v", records)
	}
	got := records[1]
	if got.Tokens.Input != 15 || got.Tokens.Output != 5 || got.Tokens.Cached != 3 || got.Tokens.CacheCreation != 2 || got.ToolCalls != 1 {
		t.Fatalf("unexpected usage: %+v", got)
	}
	sources, err := New(root).Discover(context.Background())
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, err = %v", sources, err)
	}
}
