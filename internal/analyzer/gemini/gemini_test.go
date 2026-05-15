package gemini

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestParseJSONSessionTokens(t *testing.T) {
	root := t.TempDir()
	chatDir := filepath.Join(root, "project-a", "chats")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chatDir, "session.json")
	content := `{
  "sessionId": "s1",
  "messages": [
    {"id":"u1","type":"user","timestamp":"2026-05-16T00:00:00Z","content":"hello"},
    {"id":"g1","type":"gemini","timestamp":"2026-05-16T00:00:01Z","model":"gemini-2.5-pro","tokens":{"input":10,"output":5,"cached":3,"thoughts":2,"tool":4,"total":24},"toolCalls":[{"name":"read_many_files"},{"name":"run_shell_command"}]}
  ]
}`
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
	if records[0].Model != "gemini-2.5-pro" {
		t.Fatalf("user model = %q, want gemini-2.5-pro", records[0].Model)
	}
	assistant := records[1]
	if assistant.Tool != model.ToolGeminiCLI || assistant.Model != "gemini-2.5-pro" {
		t.Fatalf("unexpected assistant record: %+v", assistant)
	}
	if assistant.Tokens.Input != 10 || assistant.Tokens.Output != 5 || assistant.Tokens.Cached != 3 || assistant.Tokens.Reasoning != 2 {
		t.Fatalf("unexpected token stats: %+v", assistant.Tokens)
	}
	if assistant.ToolCalls != 2 {
		t.Fatalf("tool calls = %d, want 2", assistant.ToolCalls)
	}
}

func TestParseJSONLUsesLatestMessageByID(t *testing.T) {
	root := t.TempDir()
	chatDir := filepath.Join(root, "project-a", "chats")
	if err := os.MkdirAll(chatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(chatDir, "session.jsonl")
	content := `{"id":"g1","type":"gemini","timestamp":"2026-05-16T00:00:01Z","model":"gemini-2.5-flash","tokens":{"input":1,"output":1}}
{"$set":{"messages":[]}}
{"id":"bad","type":"gemini","tokens":
{"id":"g1","type":"gemini","timestamp":"2026-05-16T00:00:02Z","model":"gemini-2.5-flash","tokens":{"input":7,"output":3,"cached":2,"thoughts":1}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := New(root).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 1; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	if records[0].Tokens.Input != 7 || records[0].Tokens.Output != 3 || records[0].Tokens.Cached != 2 || records[0].Tokens.Reasoning != 1 {
		t.Fatalf("latest message was not used: %+v", records[0].Tokens)
	}
}
