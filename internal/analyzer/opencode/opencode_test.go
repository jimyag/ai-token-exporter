package opencode

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestSQLiteTakesPriorityAndStepFinishFallback(t *testing.T) {
	root := t.TempDir()
	legacyDir := filepath.Join(root, "storage", "message", "session-a")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"id":"message-a","sessionID":"session-a","role":"assistant","summary":true,"modelID":"old","tokens":{"input":99}}`
	if err := os.WriteFile(filepath.Join(legacyDir, "message-a.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	uniqueLegacy := `{"id":"message-c","sessionID":"session-a","role":"assistant","summary":false,"modelID":"legacy","tokens":{"input":4,"output":1}}`
	if err := os.WriteFile(filepath.Join(legacyDir, "message-c.json"), []byte(uniqueLegacy), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "opencode.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"CREATE TABLE message (id TEXT, session_id TEXT, time_created INTEGER, data TEXT)",
		"CREATE TABLE part (message_id TEXT, data TEXT)",
		`INSERT INTO message VALUES ('message-a','session-a',1786629153000,'{"role":"assistant","summary":true,"modelID":"new","tokens":{"input":10,"output":5,"cache":{"read":3,"write":2}}}')`,
		`INSERT INTO message VALUES ('message-b','session-a',1786629154000,'{"role":"assistant","summary":{"title":"summary"},"modelID":"next"}')`,
		`INSERT INTO part VALUES ('message-a','{"type":"tool","tool":"read"}')`,
		`INSERT INTO part VALUES ('message-b','{"type":"step-finish","tokens":{"input":7,"output":2}}')`,
		`INSERT INTO part VALUES ('message-b','{"type":"step-finish","tokens":{"input":8,"output":3,"cache":{"read":1}}}')`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	az := New(root)
	sources, err := az.Discover(t.Context())
	if err != nil || len(sources) != 2 || sources[0].Path != dbPath || sources[1].Path != filepath.Join(legacyDir, "message-c.json") {
		t.Fatalf("sources = %+v, err = %v", sources, err)
	}
	legacyRecords, err := az.Parse(t.Context(), sources[1])
	if err != nil || len(legacyRecords) != 1 || legacyRecords[0].Tokens.Input != 4 {
		t.Fatalf("legacy records = %+v, err = %v", legacyRecords, err)
	}
	records, err := az.Parse(t.Context(), model.Source{Path: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %+v", records)
	}
	if records[0].Model != "new" || records[0].Tokens.Input != 15 || records[0].ToolCalls != 1 {
		t.Fatalf("message-a = %+v", records[0])
	}
	if records[1].Model != "next" || records[1].Tokens.Input != 16 || records[1].Tokens.Output != 5 || records[1].Tokens.Cached != 1 {
		t.Fatalf("message-b = %+v", records[1])
	}
}
