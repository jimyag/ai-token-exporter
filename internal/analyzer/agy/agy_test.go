package agy

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

func TestParseSQLiteSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE steps (idx INTEGER, step_type INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE gen_metadata (idx INTEGER, data BLOB)`); err != nil {
		t.Fatal(err)
	}

	userPayload := concatProto(
		encodeProtoString(3, "How does ai-token-exporter parse agy sessions?"),
		encodeProtoTimestamp(4, 1779246143, 0),
	)
	assistantPayload := concatProto(
		encodeProtoString(3, "It reads Antigravity conversation SQLite databases."),
		encodeProtoTimestamp(4, 1779246150, 0),
		encodeToolCall("run_command"),
	)
	genMetadata := encodeGenMetadata("gemini-3-flash-a", 100, 45, 12)

	if _, err := db.Exec(`INSERT INTO steps (idx, step_type, step_payload) VALUES (?, ?, ?)`, 0, 14, userPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO steps (idx, step_type, step_payload) VALUES (?, ?, ?)`, 1, 15, assistantPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gen_metadata (idx, data) VALUES (?, ?)`, 1, genMetadata); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	records, err := New(filepath.Dir(path)).Parse(context.Background(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}

	user := records[0]
	if user.Tool != model.ToolAgy || user.Role != model.RoleUser {
		t.Fatalf("unexpected user record: %+v", user)
	}
	if !user.Timestamp.Equal(time.Unix(1779246143, 0).UTC()) {
		t.Fatalf("user timestamp = %s", user.Timestamp)
	}

	assistant := records[1]
	if assistant.Tool != model.ToolAgy || assistant.Role != model.RoleAssistant {
		t.Fatalf("unexpected assistant record: %+v", assistant)
	}
	if assistant.Model != "gemini-3-flash-a" {
		t.Fatalf("model = %q, want gemini-3-flash-a", assistant.Model)
	}
	if assistant.Tokens.Input != 100 || assistant.Tokens.Output != 45 || assistant.Tokens.Reasoning != 12 {
		t.Fatalf("unexpected token stats: %+v", assistant.Tokens)
	}
	if assistant.ToolCalls != 1 {
		t.Fatalf("tool calls = %d, want 1", assistant.ToolCalls)
	}
}

func TestDiscoverFindsDBFiles(t *testing.T) {
	root := t.TempDir()
	dbPath := filepath.Join(root, "nested", "session.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("not used by discovery"), 0o644); err != nil {
		t.Fatal(err)
	}

	sources, err := New(root).Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(sources), 1; got != want {
		t.Fatalf("sources = %d, want %d", got, want)
	}
	if sources[0].Path != dbPath {
		t.Fatalf("source path = %q, want %q", sources[0].Path, dbPath)
	}
}

func encodeGenMetadata(modelID string, input, output, reasoning uint64) []byte {
	tokenFields := concatProto(
		encodeProtoVarint(5, input),
		encodeProtoVarint(2, output),
		encodeProtoVarint(3, reasoning),
	)
	envelope := concatProto(
		encodeProtoString(19, modelID),
		encodeProtoBytes(4, tokenFields),
	)
	return encodeProtoBytes(1, envelope)
}

func encodeToolCall(name string) []byte {
	return encodeProtoBytes(20, encodeProtoBytes(7, encodeProtoString(2, name)))
}

func encodeProtoTimestamp(fieldNumber uint32, seconds int64, nanos uint32) []byte {
	return encodeProtoBytes(fieldNumber, concatProto(
		encodeProtoVarint(1, uint64(seconds)),
		encodeProtoVarint(2, uint64(nanos)),
	))
}

func encodeProtoString(fieldNumber uint32, value string) []byte {
	return encodeProtoBytes(fieldNumber, []byte(value))
}

func encodeProtoBytes(fieldNumber uint32, value []byte) []byte {
	out := encodeVarint(uint64(fieldNumber<<3 | wireBytes))
	out = append(out, encodeVarint(uint64(len(value)))...)
	out = append(out, value...)
	return out
}

func encodeProtoVarint(fieldNumber uint32, value uint64) []byte {
	out := encodeVarint(uint64(fieldNumber << 3))
	out = append(out, encodeVarint(value)...)
	return out
}

func encodeVarint(value uint64) []byte {
	var out []byte
	for {
		b := byte(value & 0x7f)
		value >>= 7
		if value != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if value == 0 {
			return out
		}
	}
}

func concatProto(parts ...[]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}
