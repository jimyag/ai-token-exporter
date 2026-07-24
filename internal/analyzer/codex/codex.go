package codex

import (
	"bufio"
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

type Analyzer struct {
	CodexDir            string
	SessionsDir         string
	ArchivedSessionsDir string
	DefaultModel        string
}

func New(codexDir string) *Analyzer {
	defaultModel := analyzer.ReadDefaultModelFromText(filepath.Join(codexDir, "config.toml"))
	return &Analyzer{
		CodexDir:            codexDir,
		SessionsDir:         filepath.Join(codexDir, "sessions"),
		ArchivedSessionsDir: filepath.Join(codexDir, "archived_sessions"),
		DefaultModel:        defaultModel,
	}
}

func (a *Analyzer) Name() string {
	return model.ToolCodexCLI
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	var sources []model.Source
	seen := make(map[string]bool)
	for _, dir := range []string{a.SessionsDir, a.ArchivedSessionsDir} {
		discovered, err := analyzer.WalkFiles(ctx, dir, a.ValidPath)
		if err != nil {
			return nil, err
		}
		for _, source := range discovered {
			identity := a.sessionPath(source.Path)
			if seen[identity] {
				continue
			}
			seen[identity] = true
			sources = append(sources, source)
		}
	}
	return sources, nil
}

func (a *Analyzer) ValidPath(path string) bool {
	return filepath.Ext(path) == ".jsonl"
}

func (a *Analyzer) sessionPath(path string) string {
	relative, err := filepath.Rel(a.ArchivedSessionsDir, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}

	name := filepath.Base(path)
	const prefix = "rollout-"
	if !strings.HasPrefix(name, prefix) || len(name) < len(prefix)+len("2006-01-02") {
		return path
	}
	date, err := time.Parse("2006-01-02", name[len(prefix):len(prefix)+len("2006-01-02")])
	if err != nil {
		return path
	}
	return filepath.Join(
		a.SessionsDir,
		date.Format("2006"),
		date.Format("01"),
		date.Format("02"),
		name,
	)
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

	sessionID := hash.Sum(a.sessionPath(source.Path))
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
			continue
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
				if role == model.RoleUser {
					records = append(records, model.Record{
						Tool:      a.Name(),
						Model:     analyzer.ResolveModel(currentModel, a.DefaultModel),
						SessionID: sessionID,
						Role:      role,
						Timestamp: analyzer.ParseTime(item.Timestamp),
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
				continue
			}
			usage := info.LastTokenUsage
			if usage == nil && info.TotalTokenUsage != nil {
				calculated := subtract(*info.TotalTokenUsage, previous)
				usage = &calculated
			}
			if info.TotalTokenUsage != nil {
				copyValue := *info.TotalTokenUsage
				previous = &copyValue
			} else if usage != nil {
				accumulated := add(previous, *usage)
				previous = &accumulated
			}
			if usage == nil {
				continue
			}
			records = append(records, model.Record{
				Tool:      a.Name(),
				Model:     analyzer.ResolveModel(currentModel, a.DefaultModel),
				SessionID: sessionID,
				Role:      model.RoleAssistant,
				Timestamp: analyzer.ParseTime(item.Timestamp),
				Tokens: model.TokenStats{
					Input:     usage.InputTokens,
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

func add(previous *tokenUsage, delta tokenUsage) tokenUsage {
	if previous == nil {
		return delta
	}
	return tokenUsage{
		InputTokens:           previous.InputTokens + delta.InputTokens,
		OutputTokens:          previous.OutputTokens + delta.OutputTokens,
		CachedInputTokens:     previous.CachedInputTokens + delta.CachedInputTokens,
		ReasoningOutputTokens: previous.ReasoningOutputTokens + delta.ReasoningOutputTokens,
		TotalTokens:           previous.TotalTokens + delta.TotalTokens,
	}
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
