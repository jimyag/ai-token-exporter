package claude

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

type Analyzer struct {
	ProjectsDir  string
	DefaultModel string
}

func New(projectsDir string) *Analyzer {
	home, _ := os.UserHomeDir()
	defaultModel := analyzer.ReadDefaultModelFromJSON(filepath.Join(home, ".claude", "settings.json"))
	if defaultModel == "" {
		defaultModel = analyzer.ReadDefaultModelFromJSON(filepath.Join(home, ".claude.json"))
	}
	return &Analyzer{ProjectsDir: projectsDir, DefaultModel: defaultModel}
}

func (a *Analyzer) Name() string {
	return model.ToolClaudeCode
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	return analyzer.WalkFiles(ctx, a.ProjectsDir, a.ValidPath)
}

func (a *Analyzer) ValidPath(path string) bool {
	if filepath.Ext(path) != ".jsonl" {
		return false
	}
	parent := filepath.Dir(path)
	if parent == "." || parent == a.ProjectsDir {
		return false
	}
	return true
}

type usage struct {
	InputTokens              uint64 `json:"input_tokens"`
	OutputTokens             uint64 `json:"output_tokens"`
	CacheCreationInputTokens uint64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     uint64 `json:"cache_read_input_tokens"`
}

type message struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
	Usage   *usage          `json:"usage"`
}

type entry struct {
	Type          string          `json:"type"`
	RequestID     string          `json:"requestId"`
	UUID          string          `json:"uuid"`
	Timestamp     string          `json:"timestamp"`
	Message       *message        `json:"message"`
	ToolUseResult json.RawMessage `json:"toolUseResult"`
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sessionID := hash.Sum(source.Path)
	seen := map[string]bool{}
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
		var item entry
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		if item.Message == nil || item.Message.Model == "<synthetic>" {
			continue
		}

		dedupeKey := ""
		if item.RequestID != "" && item.Message.ID != "" {
			dedupeKey = item.RequestID + ":" + item.Message.ID
		} else if item.UUID != "" {
			dedupeKey = source.Path + ":" + item.UUID
		}
		if dedupeKey != "" {
			if seen[dedupeKey] {
				continue
			}
			seen[dedupeKey] = true
		}

		role := normalizeRole(item.Message.Role)
		if item.Message.Usage == nil {
			role = model.RoleUser
		}
		tokens := model.TokenStats{}
		if item.Message.Usage != nil {
			tokens.Input = item.Message.Usage.InputTokens
			tokens.Output = item.Message.Usage.OutputTokens
			tokens.CacheCreation = item.Message.Usage.CacheCreationInputTokens
			tokens.CacheRead = item.Message.Usage.CacheReadInputTokens
			tokens.Cached = tokens.CacheRead
		}

		records = append(records, model.Record{
			Tool:      a.Name(),
			Model:     analyzer.ResolveModel(item.Message.Model, a.DefaultModel),
			SessionID: sessionID,
			Role:      role,
			Timestamp: analyzer.ParseTime(item.Timestamp),
			Tokens:    tokens,
			ToolCalls: countToolUse(item.Message.Content),
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func normalizeRole(role string) string {
	if role == model.RoleUser {
		return model.RoleUser
	}
	return model.RoleAssistant
}

func countToolUse(raw json.RawMessage) uint64 {
	if len(raw) == 0 {
		return 0
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0
	}
	blocks, ok := value.([]any)
	if !ok {
		return 0
	}
	var count uint64
	for _, block := range blocks {
		obj, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if obj["type"] == "tool_use" {
			count++
		}
	}
	return count
}
