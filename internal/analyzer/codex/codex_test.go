package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

const sessionFileName = "rollout-2026-07-22T12-34-56-019f8a69-8cab-75b0-8f7c-b6b6339ed90b.jsonl"

const testSession = `{"timestamp":"2026-07-22T12:34:56Z","type":"session_meta","payload":{"model":"gpt-5"}}
{"timestamp":"2026-07-22T12:35:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
{"timestamp":"2026-07-22T12:35:01Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":10,"output_tokens":2,"cached_input_tokens":5,"reasoning_output_tokens":1,"total_tokens":12}}}}
`

func TestDiscoverIncludesArchivedSessions(t *testing.T) {
	root := t.TempDir()
	activeDir := filepath.Join(root, "sessions", "2026", "07", "22")
	archivedDir := filepath.Join(root, "archived_sessions")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archivedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(activeDir, "active.jsonl")
	archivedPath := filepath.Join(archivedDir, sessionFileName)
	if err := os.WriteFile(activePath, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivedPath, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := New(root).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sources), 2; got != want {
		t.Fatalf("sources = %d, want %d", got, want)
	}
}

func TestDiscoverDeduplicatesActiveAndArchivedCopies(t *testing.T) {
	root := t.TempDir()
	activeDir := filepath.Join(root, "sessions", "2026", "07", "22")
	archivedDir := filepath.Join(root, "archived_sessions")
	if err := os.MkdirAll(activeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(archivedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(activeDir, sessionFileName)
	archivedPath := filepath.Join(archivedDir, sessionFileName)
	if err := os.WriteFile(activePath, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivedPath, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := New(root).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sources), 1; got != want {
		t.Fatalf("sources = %d, want %d", got, want)
	}
	if sources[0].Path != activePath {
		t.Fatalf("source path = %q, want active path %q", sources[0].Path, activePath)
	}
}

func TestSessionIDRemainsStableAfterArchiving(t *testing.T) {
	root := t.TempDir()
	activePath := filepath.Join(root, "sessions", "2026", "07", "22", sessionFileName)
	archivedPath := filepath.Join(root, "archived_sessions", sessionFileName)
	if err := os.MkdirAll(filepath.Dir(activePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(archivedPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activePath, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivedPath, []byte(testSession), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(root)
	activeRecords, err := a.Parse(context.Background(), model.Source{Path: activePath})
	if err != nil {
		t.Fatal(err)
	}
	archivedRecords, err := a.Parse(context.Background(), model.Source{Path: archivedPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(activeRecords) == 0 || len(archivedRecords) == 0 {
		t.Fatal("expected records from both active and archived sessions")
	}
	if activeRecords[0].SessionID != archivedRecords[0].SessionID {
		t.Fatalf("session IDs differ: active=%q archived=%q", activeRecords[0].SessionID, archivedRecords[0].SessionID)
	}
}

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
{"timestamp":"2026-05-16T00:00:00.1Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
{"timestamp":"2026-05-16T00:00:00.2Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}}
{"timestamp":"2026-05-16T00:00:01Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":100,"output_tokens":20,"cached_input_tokens":40,"reasoning_output_tokens":5,"total_tokens":120}}}}
{"timestamp":"2026-05-16T00:00:01.5Z","type":"event_msg","payload":
{"timestamp":"2026-05-16T00:00:02Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":150,"output_tokens":30,"cached_input_tokens":60,"reasoning_output_tokens":7,"total_tokens":180}}}}
{"timestamp":"2026-05-16T00:00:03Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":8,"cached_input_tokens":5,"reasoning_output_tokens":3,"total_tokens":28}}}}
{"timestamp":"2026-05-16T00:00:04Z","type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":180,"output_tokens":40,"cached_input_tokens":70,"reasoning_output_tokens":12,"total_tokens":220}}}}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := New(root).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 5; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	roleCounts := map[string]int{}
	for _, record := range records {
		roleCounts[record.Role]++
	}
	if roleCounts[model.RoleUser] != 1 || roleCounts[model.RoleAssistant] != 4 {
		t.Fatalf("unexpected role counts: %+v", roleCounts)
	}
	if records[1].Tokens.Input != 100 || records[1].Tokens.Cached != 40 {
		t.Fatalf("first total usage not parsed correctly: %+v", records[1].Tokens)
	}
	if records[2].Tokens.Input != 50 || records[2].Tokens.Output != 10 || records[2].Tokens.Cached != 20 || records[2].Tokens.Reasoning != 2 {
		t.Fatalf("total delta not parsed correctly: %+v", records[2].Tokens)
	}
	if records[3].Tokens.Input != 20 || records[3].Tokens.Output != 8 || records[3].Tokens.Cached != 5 || records[3].Tokens.Reasoning != 3 {
		t.Fatalf("last usage not parsed correctly: %+v", records[3].Tokens)
	}
	if records[4].Tokens.Input != 10 || records[4].Tokens.Output != 2 || records[4].Tokens.Cached != 5 || records[4].Tokens.Reasoning != 2 {
		t.Fatalf("mixed last and total usage not parsed correctly: %+v", records[4].Tokens)
	}
}
