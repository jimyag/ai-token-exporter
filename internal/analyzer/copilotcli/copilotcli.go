package copilotcli

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

var stateDirs = []string{"session-state", "history-session-state"}

type Analyzer struct {
	CopilotDir   string
	DefaultModel string
}

func New(copilotDir string) *Analyzer {
	return &Analyzer{CopilotDir: copilotDir}
}

func (a *Analyzer) Name() string {
	return model.ToolCopilotCLI
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	var sources []model.Source
	for _, dir := range stateDirs {
		root := filepath.Join(a.CopilotDir, dir)
		found, err := analyzer.WalkFiles(ctx, root, a.ValidPath)
		if err != nil {
			return sources, err
		}
		sources = append(sources, found...)
	}
	return sources, nil
}

func (a *Analyzer) ValidPath(path string) bool {
	if filepath.Ext(path) != ".jsonl" {
		return false
	}
	if filepath.Base(path) == "events.jsonl" {
		parent := filepath.Dir(filepath.Dir(path))
		return contains(stateDirs, filepath.Base(parent))
	}
	return contains(stateDirs, filepath.Base(filepath.Dir(path)))
}

type event struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      any    `json:"data"`
}

type turn struct {
	Model          string
	UserText       string
	InputParts     []string
	OutputParts    []string
	ReasoningParts []string
	ExactOutput    uint64
	ToolCalls      uint64
}

type usageTotals struct {
	Input      uint64
	Output     uint64
	CacheRead  uint64
	CacheWrite uint64
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	events := []event{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item event
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		events = append(events, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	sessionRaw := strings.TrimSuffix(filepath.Base(source.Path), filepath.Ext(source.Path))
	currentModel := a.DefaultModel
	for _, item := range events {
		if item.Type != "session.start" {
			continue
		}
		data, _ := item.Data.(map[string]any)
		if id, ok := data["sessionId"].(string); ok && id != "" {
			sessionRaw = id
		}
		if contextMap, ok := data["context"].(map[string]any); ok {
			if found := analyzer.ExtractModel(contextMap); found != "" {
				currentModel = found
			}
		}
	}

	sessionID := hash.Sum(sessionRaw)
	records := []model.Record{}
	var current *turn

	flush := func() {
		if current == nil {
			return
		}
		outputText := strings.Join(current.OutputParts, "\n")
		reasoningText := strings.Join(current.ReasoningParts, "\n")
		outputTokens := current.ExactOutput
		if outputTokens == 0 {
			outputTokens = analyzer.CountTokens(outputText)
		}
		records = append(records, model.Record{
			Tool:      a.Name(),
			Model:     analyzer.ResolveModel(current.Model, currentModel, a.DefaultModel),
			SessionID: sessionID,
			Role:      model.RoleAssistant,
			Tokens: model.TokenStats{
				Input:     analyzer.CountTokens(current.UserText + "\n" + strings.Join(current.InputParts, "\n")),
				Output:    outputTokens,
				Reasoning: min(outputTokens, analyzer.CountTokens(reasoningText)),
			},
			ToolCalls: current.ToolCalls,
		})
		current = nil
	}

	for _, item := range events {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, _ := item.Data.(map[string]any)
		if found := analyzer.ExtractModel(data); found != "" {
			currentModel = found
			if current != nil {
				current.Model = found
			}
		}
		switch item.Type {
		case "session.model_change":
			if found := analyzer.ExtractModel(data); found != "" {
				currentModel = found
			}
		case "user.message":
			flush()
			text := analyzer.ExtractText(data["content"])
			records = append(records, model.Record{
				Tool:      a.Name(),
				Model:     analyzer.ResolveModel(currentModel, a.DefaultModel),
				SessionID: sessionID,
				Role:      model.RoleUser,
			})
			current = &turn{Model: currentModel, UserText: text}
		case "assistant.turn_start":
			if current == nil {
				current = &turn{Model: currentModel}
			}
		case "assistant.turn_end":
			flush()
		case "assistant.message", "assistant.message.delta":
			if current == nil {
				current = &turn{Model: currentModel}
			}
			if text := analyzer.ExtractText(data["content"]); text != "" {
				current.OutputParts = append(current.OutputParts, text)
			}
			if text := analyzer.ExtractText(data["reasoningText"]); text != "" {
				current.ReasoningParts = append(current.ReasoningParts, text)
			}
			if raw, ok := data["outputTokens"].(float64); ok && raw > 0 {
				current.ExactOutput += uint64(raw)
			}
		case "assistant.reasoning":
			if current == nil {
				current = &turn{Model: currentModel}
			}
			if text := analyzer.ExtractText(data["content"]); text != "" {
				current.ReasoningParts = append(current.ReasoningParts, text)
			}
		case "tool.execution_start":
			if current == nil {
				current = &turn{Model: currentModel}
			}
			current.ToolCalls++
			toolName, _ := data["toolName"].(string)
			if toolName == "" {
				toolName = "unknown"
			}
			args, _ := json.Marshal(data["arguments"])
			current.OutputParts = append(current.OutputParts, toolName+" "+string(args))
		case "tool.execution_complete":
			if current == nil {
				current = &turn{Model: currentModel}
			}
			if text := analyzer.ExtractText(data["result"]); text != "" {
				current.InputParts = append(current.InputParts, text)
			}
		case "session.shutdown":
			flush()
			applyShutdown(records, extractShutdownMetrics(data))
		}
		_ = analyzer.ParseTime(item.Timestamp)
	}
	flush()
	return records, nil
}

func extractShutdownMetrics(data map[string]any) map[string]usageTotals {
	result := map[string]usageTotals{}
	modelMetrics, _ := data["modelMetrics"].(map[string]any)
	for rawModel, rawMetrics := range modelMetrics {
		metricsMap, _ := rawMetrics.(map[string]any)
		usageMap, _ := metricsMap["usage"].(map[string]any)
		if len(usageMap) == 0 {
			continue
		}
		result[analyzer.LastPathSegment(rawModel)] = usageTotals{
			Input:      number(usageMap["inputTokens"]),
			Output:     number(usageMap["outputTokens"]),
			CacheRead:  number(usageMap["cacheReadTokens"]),
			CacheWrite: number(usageMap["cacheWriteTokens"]),
		}
	}
	return result
}

func applyShutdown(records []model.Record, metrics map[string]usageTotals) {
	if len(metrics) == 0 {
		return
	}
	for modelName, usage := range metrics {
		indexes := []int{}
		for i := range records {
			if records[i].Role == model.RoleAssistant && records[i].Model == modelName {
				indexes = append(indexes, i)
			}
		}
		if len(indexes) == 0 && len(metrics) == 1 {
			for i := range records {
				if records[i].Role == model.RoleAssistant {
					records[i].Model = modelName
					indexes = append(indexes, i)
				}
			}
		}
		if len(indexes) == 0 {
			continue
		}
		for pos, idx := range indexes {
			records[idx].Tokens.Input = splitEvenly(usage.Input, len(indexes), pos)
			records[idx].Tokens.Output = splitEvenly(usage.Output, len(indexes), pos)
			records[idx].Tokens.CacheRead = splitEvenly(usage.CacheRead, len(indexes), pos)
			records[idx].Tokens.CacheCreation = splitEvenly(usage.CacheWrite, len(indexes), pos)
			records[idx].Tokens.Cached = records[idx].Tokens.CacheRead + records[idx].Tokens.CacheCreation
			records[idx].Tokens.Reasoning = min(records[idx].Tokens.Reasoning, records[idx].Tokens.Output)
		}
	}
}

func splitEvenly(total uint64, count int, pos int) uint64 {
	if count <= 0 {
		return 0
	}
	base := total / uint64(count)
	if pos == count-1 {
		return total - base*uint64(count-1)
	}
	return base
}

func number(value any) uint64 {
	switch v := value.(type) {
	case float64:
		return uint64(v)
	case int:
		return uint64(v)
	case uint64:
		return v
	default:
		return 0
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ = time.Now
