package copilotvscode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestParseChatSessionModelAndEstimatedTokens(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "workspace", "chatSessions")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sessionDir, "session.json")
	content := `{
  "sessionId": "s1",
  "requests": [{
    "requestId": "r1",
    "timestamp": 1778889600000,
    "modelId": "generic-copilot/litellm/anthropic/claude-sonnet-4",
    "message": {"text": "please inspect this file"},
    "response": [{"value": "done"}, {"kind": "toolInvocationSerialized"}],
    "result": {"metadata": {
      "toolCallResults": {"content": "file contents"},
      "toolCallRounds": [{"response": "thinking", "toolCalls": [{"name": "read_file", "arguments": "{\"path\":\"main.go\"}"}]}]
    }}
  }]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := (&Analyzer{Roots: []string{root}}).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	assistant := records[1]
	if assistant.Model != "claude-sonnet-4" {
		t.Fatalf("model = %q", assistant.Model)
	}
	if assistant.Tokens.Input == 0 || assistant.Tokens.Output == 0 {
		t.Fatalf("expected estimated tokens, got %+v", assistant.Tokens)
	}
	if assistant.ToolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", assistant.ToolCalls)
	}
}

func TestNewUsesProvidedConfigDir(t *testing.T) {
	root := t.TempDir()
	az := New(root)
	if got, want := az.Roots[0], filepath.Join(root, "Code", "User", "workspaceStorage"); got != want {
		t.Fatalf("root = %q, want %q", got, want)
	}
}
