package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

type fakeAnalyzer struct {
	name    string
	sources []model.Source
	records map[string][]model.Record
	fail    map[string]bool
}

func (f fakeAnalyzer) Name() string { return f.name }

func (f fakeAnalyzer) Discover(context.Context) ([]model.Source, error) {
	return f.sources, nil
}

func (f fakeAnalyzer) Parse(_ context.Context, source model.Source) ([]model.Record, error) {
	if f.fail[source.Path] {
		return nil, errors.New("parse failed")
	}
	return f.records[source.Path], nil
}

func (f fakeAnalyzer) ValidPath(string) bool { return true }

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
	snapshot := New([]analyzer.Analyzer{az}, "v", "c").ScanOnce(context.Background())
	stat := snapshot.Tools[model.ToolCodexCLI]
	if stat.SourceFiles != 2 || stat.ParseErrors != 1 || stat.Sessions != 1 {
		t.Fatalf("unexpected tool stat: %+v", stat)
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
