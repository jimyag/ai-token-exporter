package scanner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

type fakeAnalyzer struct {
	name    string
	sources []model.Source
	records map[string][]model.Record
	fail    map[string]bool
	calls   map[string]int
}

func (f *fakeAnalyzer) Name() string { return f.name }

func (f *fakeAnalyzer) Discover(context.Context) ([]model.Source, error) {
	return f.sources, nil
}

func (f *fakeAnalyzer) Parse(_ context.Context, source model.Source) ([]model.Record, error) {
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[source.Path]++
	if f.fail[source.Path] {
		return nil, errors.New("parse failed")
	}
	return f.records[source.Path], nil
}

func (f *fakeAnalyzer) ValidPath(string) bool { return true }

func TestScanAggregatesRecordsAndParseErrors(t *testing.T) {
	az := fakeAnalyzer{
		name:    model.ToolCodexCLI,
		sources: []model.Source{{Path: "ok"}, {Path: "bad"}},
		records: map[string][]model.Record{
			"ok": {
				{
					Tool:      model.ToolCodexCLI,
					Model:     "gpt-5",
					SessionID: "s1",
					Role:      model.RoleAssistant,
					Tokens: model.TokenStats{
						Input:  10,
						Output: 5,
					},
					ToolCalls: 2,
				},
				{
					Tool:      model.ToolCodexCLI,
					Model:     "",
					SessionID: "s1",
					Role:      model.RoleUser,
				},
			},
		},
		fail: map[string]bool{"bad": true},
	}
	snapshot := New([]analyzer.Analyzer{&az}, "v", "c").ScanOnce(context.Background())
	stat := snapshot.Tools[model.ToolCodexCLI]
	if stat.SourceFiles != 2 || stat.ParseErrors != 1 || stat.Sessions != 1 {
		t.Fatalf("unexpected tool stat: %+v", stat)
	}
	if snapshot.LastScanSuccess {
		t.Fatal("scan success should be false when any source parse fails")
	}
	key := model.SeriesKey{Tool: model.ToolCodexCLI, Model: "gpt-5"}
	if snapshot.Aggregates[key].Tokens.Input != 10 || snapshot.Aggregates[key].ToolCalls != 2 {
		t.Fatalf("assistant aggregate missing: %+v", snapshot.Aggregates[key])
	}
	unknown := model.SeriesKey{Tool: model.ToolCodexCLI, Model: model.UnknownModel}
	if snapshot.Aggregates[unknown].Messages[model.RoleUser] != 1 {
		t.Fatalf("unknown model user message missing: %+v", snapshot.Aggregates[unknown])
	}
}

func TestScanCachesUnchangedSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	az := &fakeAnalyzer{
		name:    model.ToolCodexCLI,
		sources: []model.Source{{Path: path}},
		records: map[string][]model.Record{
			path: {{
				Tool:      model.ToolCodexCLI,
				Model:     "gpt-5",
				SessionID: "s1",
				Role:      model.RoleAssistant,
				Tokens:    model.TokenStats{Input: 10},
			}},
		},
	}
	scn := New([]analyzer.Analyzer{az}, "v", "c")

	first := scn.ScanOnce(context.Background())
	second := scn.ScanOnce(context.Background())

	if got, want := az.calls[path], 1; got != want {
		t.Fatalf("parse calls = %d, want %d", got, want)
	}
	if first.Tools[model.ToolCodexCLI].CacheMisses != 1 {
		t.Fatalf("first scan cache stats = %+v", first.Tools[model.ToolCodexCLI])
	}
	if second.Tools[model.ToolCodexCLI].CacheHits != 1 || second.Tools[model.ToolCodexCLI].CacheMisses != 0 {
		t.Fatalf("second scan cache stats = %+v", second.Tools[model.ToolCodexCLI])
	}
	key := model.SeriesKey{Tool: model.ToolCodexCLI, Model: "gpt-5"}
	if second.Aggregates[key].Tokens.Input != 10 {
		t.Fatalf("cached aggregate missing: %+v", second.Aggregates[key])
	}
}

func TestScanReparsesChangedSources(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	az := &fakeAnalyzer{
		name:    model.ToolCodexCLI,
		sources: []model.Source{{Path: path}},
		records: map[string][]model.Record{
			path: {{
				Tool:      model.ToolCodexCLI,
				Model:     "gpt-5",
				SessionID: "s1",
				Tokens:    model.TokenStats{Input: 10},
			}},
		},
	}
	scn := New([]analyzer.Analyzer{az}, "v", "c")
	scn.ScanOnce(context.Background())

	if err := os.WriteFile(path, []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	az.records[path][0].Tokens.Input = 20
	snapshot := scn.ScanOnce(context.Background())

	if got, want := az.calls[path], 2; got != want {
		t.Fatalf("parse calls = %d, want %d", got, want)
	}
	if snapshot.Tools[model.ToolCodexCLI].CacheMisses != 1 {
		t.Fatalf("cache stats = %+v", snapshot.Tools[model.ToolCodexCLI])
	}
	key := model.SeriesKey{Tool: model.ToolCodexCLI, Model: "gpt-5"}
	if snapshot.Aggregates[key].Tokens.Input != 20 {
		t.Fatalf("changed aggregate missing: %+v", snapshot.Aggregates[key])
	}
}

func TestScanReparsesWhenSQLiteWALChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.db")
	walPath := path + "-wal"
	if err := os.WriteFile(path, []byte("database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(walPath, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	az := &fakeAnalyzer{
		name:    model.ToolAgy,
		sources: []model.Source{{Path: path}},
		records: map[string][]model.Record{
			path: {{
				Tool:      model.ToolAgy,
				Model:     "gemini-2.5-flash",
				SessionID: "s1",
				Tokens:    model.TokenStats{Input: 10},
			}},
		},
	}
	scn := New([]analyzer.Analyzer{az}, "v", "c")
	scn.ScanOnce(context.Background())

	if err := os.WriteFile(walPath, []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	az.records[path][0].Tokens.Input = 20
	snapshot := scn.ScanOnce(context.Background())

	if got, want := az.calls[path], 2; got != want {
		t.Fatalf("parse calls = %d, want %d", got, want)
	}
	if snapshot.Tools[model.ToolAgy].CacheMisses != 1 {
		t.Fatalf("cache stats = %+v", snapshot.Tools[model.ToolAgy])
	}
	key := model.SeriesKey{Tool: model.ToolAgy, Model: "gemini-2.5-flash"}
	if snapshot.Aggregates[key].Tokens.Input != 20 {
		t.Fatalf("WAL aggregate missing: %+v", snapshot.Aggregates[key])
	}
}

func TestScanDropsDeletedSourcesFromCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("session"), 0o644); err != nil {
		t.Fatal(err)
	}
	az := &fakeAnalyzer{
		name:    model.ToolCodexCLI,
		sources: []model.Source{{Path: path}},
		records: map[string][]model.Record{
			path: {{Tool: model.ToolCodexCLI, Model: "gpt-5", SessionID: "s1"}},
		},
	}
	scn := New([]analyzer.Analyzer{az}, "v", "c")
	scn.ScanOnce(context.Background())

	az.sources = nil
	deleted := scn.ScanOnce(context.Background())
	if len(deleted.Aggregates) != 0 || deleted.Tools[model.ToolCodexCLI].Sessions != 0 {
		t.Fatalf("deleted source still contributes: %+v", deleted)
	}

	az.sources = []model.Source{{Path: path}}
	recreated := scn.ScanOnce(context.Background())
	if got, want := az.calls[path], 2; got != want {
		t.Fatalf("parse calls after recreate = %d, want %d", got, want)
	}
	if recreated.Tools[model.ToolCodexCLI].CacheMisses != 1 {
		t.Fatalf("recreated source should miss cache: %+v", recreated.Tools[model.ToolCodexCLI])
	}
}

func TestScanRetriesChangedSourceAfterParseFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	az := &fakeAnalyzer{
		name:    model.ToolCodexCLI,
		sources: []model.Source{{Path: path}},
		records: map[string][]model.Record{
			path: {{Tool: model.ToolCodexCLI, Model: "gpt-5", SessionID: "s1"}},
		},
		fail: make(map[string]bool),
	}
	scn := New([]analyzer.Analyzer{az}, "v", "c")
	scn.ScanOnce(context.Background())

	if err := os.WriteFile(path, []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	az.fail[path] = true
	failed := scn.ScanOnce(context.Background())
	if failed.LastScanSuccess || failed.Tools[model.ToolCodexCLI].ParseErrors != 1 || len(failed.Aggregates) != 0 {
		t.Fatalf("failed parse reused stale contribution: %+v", failed)
	}

	az.fail[path] = false
	retried := scn.ScanOnce(context.Background())
	if got, want := az.calls[path], 3; got != want {
		t.Fatalf("parse calls after retry = %d, want %d", got, want)
	}
	if !retried.LastScanSuccess || retried.Tools[model.ToolCodexCLI].Sessions != 1 {
		t.Fatalf("retry did not restore contribution: %+v", retried)
	}
}
