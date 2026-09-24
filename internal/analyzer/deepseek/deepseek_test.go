package deepseek

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/model"
	"github.com/klauspost/compress/zstd"
)

func TestParseCompressedSessionAndForkSeed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "workspace", "child")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session.jsonl.zstd")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	data := `{"type":"session","id":"child","parentSession":"parent","seedLength":5}
{"type":"assistant/message","seq":4,"data":{"message":{"source":{"model":"old"}},"usage":{"outputTokens":99}}}
{"type":"user/message","seq":5,"time":1786629152000,"data":{"source":{"kind":"user"}}}
{"type":"assistant/message","seq":6,"time":1786629153000,"data":{"turn":2,"step":1,"message":{"source":{"model":"deepseek-v4-flash"}},"usage":{"inputTokens":100,"outputTokens":20,"cacheReadTokens":30,"cacheWriteTokens":4,"reasoningTokens":5}}}
{"type":"tool/call","seq":7,"data":{"turn":2,"step":1,"name":"read"}}
`
	if _, err := encoder.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	parentDir := filepath.Join(root, "workspace", "parent")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentPath := filepath.Join(parentDir, "session.jsonl.zstd")
	if err := os.WriteFile(parentPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	az := New(root)
	sources, err := az.Discover(t.Context())
	if err != nil || len(sources) != 2 {
		t.Fatalf("sources = %+v, err = %v", sources, err)
	}
	records, err := az.Parse(t.Context(), model.Source{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Role != model.RoleUser || records[1].Role != model.RoleAssistant {
		t.Fatalf("unexpected records: %+v", records)
	}
	got := records[1]
	if got.Model != "deepseek-v4-flash" || got.Tokens.Input != 134 || got.Tokens.Output != 20 || got.Tokens.Cached != 30 || got.Tokens.Reasoning != 5 || got.ToolCalls != 1 {
		t.Fatalf("unexpected assistant usage: %+v", got)
	}
	if err := os.Remove(parentPath); err != nil {
		t.Fatal(err)
	}
	records, err = az.Parse(t.Context(), model.Source{Path: path})
	if err != nil || len(records) != 3 || records[0].Tokens.Output != 99 {
		t.Fatalf("seed without parent = %+v, err = %v", records, err)
	}
}

func TestParseKeepsCompleteFrameWhenNextFrameIsTruncated(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.jsonl.zstd")
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	first := encoder.EncodeAll([]byte("{\"type\":\"user/message\",\"data\":{\"source\":{\"kind\":\"user\"}}}\n"), nil)
	second := encoder.EncodeAll([]byte("{\"type\":\"assistant/message\"}\n"), nil)
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	data := slices.Concat(first, second[:len(second)-2])
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	records, err := New(root).Parse(t.Context(), model.Source{Path: path})
	if err != nil || len(records) != 1 || records[0].Role != model.RoleUser {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
}
