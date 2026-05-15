package copilotvscode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/hash"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

var forks = []string{"Code", "Code - Insiders", "Cursor", "Windsurf", "VSCodium", "Positron", "Antigravity"}

type Analyzer struct {
	Roots        []string
	DefaultModel string
}

func New() *Analyzer {
	home, _ := os.UserHomeDir()
	roots := make([]string, 0, len(forks))
	for _, fork := range forks {
		roots = append(roots, filepath.Join(home, "Library", "Application Support", fork, "User", "workspaceStorage"))
	}
	return &Analyzer{Roots: roots}
}

func (a *Analyzer) Name() string {
	return model.ToolGitHubCopilot
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	var sources []model.Source
	for _, root := range a.Roots {
		found, err := analyzer.WalkFiles(ctx, root, a.ValidPath)
		if err != nil {
			return sources, err
		}
		sources = append(sources, found...)
	}
	return sources, nil
}

func (a *Analyzer) ValidPath(path string) bool {
	return filepath.Ext(path) == ".json" && filepath.Base(filepath.Dir(path)) == "chatSessions"
}

type session struct {
	Requests        []request `json:"requests"`
	SessionID       string    `json:"sessionId"`
	CreationDate    int64     `json:"creationDate"`
	LastMessageDate int64     `json:"lastMessageDate"`
}

type request struct {
	RequestID string            `json:"requestId"`
	Message   userMessage       `json:"message"`
	Response  []json.RawMessage `json:"response"`
	Result    *result           `json:"result"`
	Timestamp int64             `json:"timestamp"`
	ModelID   string            `json:"modelId"`
}

type userMessage struct {
	Text string `json:"text"`
}

type result struct {
	Metadata *metadata `json:"metadata"`
}

type metadata struct {
	ToolCallResults any             `json:"toolCallResults"`
	ToolCallRounds  []toolCallRound `json:"toolCallRounds"`
}

type toolCallRound struct {
	Response  string     `json:"response"`
	ToolCalls []toolCall `json:"toolCalls"`
}

type toolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	var sess session
	if err := analyzer.ReadJSONFile(source.Path, &sess); err != nil {
		return nil, err
	}
	sessionIDRaw := sess.SessionID
	if sessionIDRaw == "" {
		sessionIDRaw = strings.TrimSuffix(filepath.Base(source.Path), filepath.Ext(source.Path))
	}
	sessionID := hash.Sum(sessionIDRaw)
	projectID := hash.Sum("github-copilot-global")
	records := make([]model.Record, 0, len(sess.Requests)*2)

	for _, req := range sess.Requests {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		modelName := analyzer.ResolveModel(analyzer.LastPathSegment(req.ModelID), a.DefaultModel)
		inputTokens := analyzer.CountTokens(req.Message.Text)
		var outputTokens uint64
		var toolCalls uint64

		if req.Result != nil && req.Result.Metadata != nil {
			inputTokens += analyzer.CountTokens(analyzer.ExtractText(req.Result.Metadata.ToolCallResults))
			for _, round := range req.Result.Metadata.ToolCallRounds {
				outputTokens += analyzer.CountTokens(round.Response)
				for _, call := range round.ToolCalls {
					toolCalls++
					outputTokens += analyzer.CountTokens(call.Name)
					outputTokens += analyzer.CountTokens(call.Arguments)
				}
			}
		}
		for _, raw := range req.Response {
			var part map[string]any
			if err := json.Unmarshal(raw, &part); err != nil {
				continue
			}
			if value, ok := part["value"].(string); ok {
				outputTokens += analyzer.CountTokens(value)
			}
			if part["kind"] == "toolInvocationSerialized" {
				toolCalls++
			}
		}

		records = append(records, model.Record{
			Tool:      a.Name(),
			Model:     modelName,
			SessionID: sessionID,
			ProjectID: projectID,
			Role:      model.RoleUser,
		})
		records = append(records, model.Record{
			Tool:      a.Name(),
			Model:     modelName,
			SessionID: sessionID,
			ProjectID: projectID,
			Role:      model.RoleAssistant,
			Tokens: model.TokenStats{
				Input:  inputTokens,
				Output: outputTokens,
			},
			ToolCalls: toolCalls,
		})
		_ = time.UnixMilli(req.Timestamp)
	}
	return records, nil
}
