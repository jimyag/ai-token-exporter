package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

type staticProvider struct {
	snapshot model.Snapshot
}

func (s staticProvider) Snapshot() model.Snapshot {
	return s.snapshot
}

func TestMetricsEndpointUsesPrometheusTextFormat(t *testing.T) {
	provider := staticProvider{snapshot: model.Snapshot{
		Aggregates: map[model.SeriesKey]model.Aggregate{
			{Tool: model.ToolCodexCLI, Model: "gpt-5", SessionID: "s1", ProjectID: "p1"}: {
				Tokens:   model.TokenStats{Input: 10, Output: 5},
				Messages: map[string]uint64{model.RoleAssistant: 1},
			},
		},
		Tools: map[string]model.ToolSnapshot{
			model.ToolCodexCLI: {SourceFiles: 1, Sessions: 1},
		},
		LastScan:           time.Unix(100, 0),
		LastSuccessfulScan: time.Unix(100, 0),
		LastScanSuccess:    true,
		Version:            "test",
		Commit:             "abc",
	}}
	srv := New(":0", provider, true)
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	bodyBytes, _ := io.ReadAll(rec.Body)
	body := string(bodyBytes)
	for _, want := range []string{
		"# HELP ai_token_exporter_tokens",
		`ai_token_exporter_tokens{model="gpt-5",project_id="p1",session_id="s1",token_type="input",tool="codex_cli"} 10`,
		`ai_token_exporter_messages{model="gpt-5",project_id="p1",role="assistant",session_id="s1",tool="codex_cli"} 1`,
		`ai_token_exporter_build_info{commit="abc",version="test"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, body)
		}
	}
}
