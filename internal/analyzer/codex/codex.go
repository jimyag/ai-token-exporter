package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/hash"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

const fallbackModel = "gpt-5"

type Analyzer struct {
	CodexDir     string
	SessionsDir  string
	DefaultModel string
}

func New(codexDir string) *Analyzer {
	defaultModel := analyzer.ReadDefaultModelFromText(filepath.Join(codexDir, "config.toml"))
	if defaultModel == "" {
		defaultModel = fallbackModel
	}
	return &Analyzer{
		CodexDir:     codexDir,
		SessionsDir:  filepath.Join(codexDir, "sessions"),
		DefaultModel: defaultModel,
	}
}

func (a *Analyzer) Name() string {
	return model.ToolCodexCLI
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	return analyzer.WalkFiles(ctx, a.SessionsDir, a.ValidPath)
}

func (a *Analyzer) ValidPath(path string) bool {
	return filepath.Ext(path) == ".jsonl"
}

type wrapper struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type tokenUsage struct {
	InputTokens           uint64 `json:"input_tokens"`
	OutputTokens          uint64 `json:"output_tokens"`
	CachedInputTokens     uint64 `json:"cached_input_tokens"`
	ReasoningOutputTokens uint64 `json:"reasoning_output_tokens"`
	TotalTokens           uint64 `json:"total_tokens"`
}

type tokenInfo struct {
	TotalTokenUsage *tokenUsage `json:"total_token_usage"`
	LastTokenUsage  *tokenUsage `json:"last_token_usage"`
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sessionID := hash.Sum(source.Path)
	projectID := hash.Sum("codex-global")
	currentModel := a.DefaultModel
	var previous *tokenUsage
	var toolCalls uint64
	var records []model.Record

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item wrapper
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		var payload any
		_ = json.Unmarshal(item.Payload, &payload)
		if found := analyzer.ExtractModel(payload); found != "" {
			currentModel = found
		}

		switch item.Type {
		case "session_meta", "turn_context":
			continue
		case "response_item":
			obj, _ := payload.(map[string]any)
			if obj["type"] == "function_call" {
				toolCalls++
				continue
			}
			if obj["type"] == "message" {
				role, _ := obj["role"].(string)
				if role == model.RoleUser || role == model.RoleAssistant {
					records = append(records, model.Record{
						Tool:      a.Name(),
						Model:     analyzer.ResolveModel(currentModel, a.DefaultModel),
						SessionID: sessionID,
						ProjectID: projectID,
						Role:      role,
					})
				}
			}
		case "event_msg":
			obj, _ := payload.(map[string]any)
			if obj["type"] != "token_count" {
				continue
			}
			infoRaw, ok := obj["info"]
			if !ok {
				continue
			}
			infoBytes, _ := json.Marshal(infoRaw)
			var info tokenInfo
			if err := json.Unmarshal(infoBytes, &info); err != nil {
				return nil, err
			}
			usage := info.LastTokenUsage
			if usage == nil && info.TotalTokenUsage != nil {
				calculated := subtract(*info.TotalTokenUsage, previous)
				usage = &calculated
			}
			if info.TotalTokenUsage != nil {
				copyValue := *info.TotalTokenUsage
				previous = &copyValue
			}
			if usage == nil {
				continue
			}
			records = append(records, model.Record{
				Tool:      a.Name(),
				Model:     analyzer.ResolveModel(currentModel, a.DefaultModel),
				SessionID: sessionID,
				ProjectID: projectID,
				Role:      model.RoleAssistant,
				Tokens: model.TokenStats{
					Input:     usage.InputTokens - min(usage.InputTokens, usage.CachedInputTokens),
					Output:    usage.OutputTokens,
					Reasoning: usage.ReasoningOutputTokens,
					Cached:    usage.CachedInputTokens,
				},
				ToolCalls: toolCalls,
			})
			toolCalls = 0
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func subtract(current tokenUsage, previous *tokenUsage) tokenUsage {
	if previous == nil {
		return current
	}
	return tokenUsage{
		InputTokens:           current.InputTokens - min(current.InputTokens, previous.InputTokens),
		OutputTokens:          current.OutputTokens - min(current.OutputTokens, previous.OutputTokens),
		CachedInputTokens:     current.CachedInputTokens - min(current.CachedInputTokens, previous.CachedInputTokens),
		ReasoningOutputTokens: current.ReasoningOutputTokens - min(current.ReasoningOutputTokens, previous.ReasoningOutputTokens),
		TotalTokens:           current.TotalTokens - min(current.TotalTokens, previous.TotalTokens),
	}
}
