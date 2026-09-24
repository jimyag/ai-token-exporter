package pi

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

type Analyzer struct{ SessionsDir string }

func New(sessionsDir string) *Analyzer { return &Analyzer{SessionsDir: sessionsDir} }
func (a *Analyzer) Name() string       { return model.ToolPiAgent }

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	return analyzer.WalkFiles(ctx, a.SessionsDir, a.ValidPath)
}

func (a *Analyzer) ValidPath(path string) bool {
	return filepath.Ext(path) == ".jsonl" && filepath.Dir(path) != a.SessionsDir
}

type entry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Provider  string `json:"provider"`
	ModelID   string `json:"modelId"`
	Message   struct {
		Role     string          `json:"role"`
		Provider string          `json:"provider"`
		Model    string          `json:"model"`
		Content  json.RawMessage `json:"content"`
		Usage    *struct {
			Input      uint64 `json:"input"`
			Output     uint64 `json:"output"`
			CacheRead  uint64 `json:"cacheRead"`
			CacheWrite uint64 `json:"cacheWrite"`
		} `json:"usage"`
	} `json:"message"`
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()

	sessionID := hash.Sum(source.Path)
	var currentModel, currentProvider string
	var records []model.Record
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var item entry
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			continue
		}
		switch item.Type {
		case "session", "model_change":
			if item.ModelID != "" {
				currentModel = item.ModelID
			}
			if item.Provider != "" {
				currentProvider = item.Provider
			}
		case "message":
			msg := item.Message
			if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
				continue
			}
			if msg.Model != "" {
				currentModel = msg.Model
			}
			if msg.Provider != "" {
				currentProvider = msg.Provider
			}
			modelName := currentModel
			if currentProvider != "" && modelName != "" && !strings.Contains(modelName, "/") {
				modelName = currentProvider + "/" + modelName
			}
			record := model.Record{
				Tool: a.Name(), Model: analyzer.ResolveModel(modelName), SessionID: sessionID,
				Role: msg.Role, Timestamp: analyzer.ParseTime(item.Timestamp),
			}
			if msg.Role == model.RoleAssistant {
				if msg.Usage != nil {
					record.Tokens = model.TokenStats{
						Input:  msg.Usage.Input + msg.Usage.CacheRead + msg.Usage.CacheWrite,
						Output: msg.Usage.Output, CacheRead: msg.Usage.CacheRead,
						CacheCreation: msg.Usage.CacheWrite, Cached: msg.Usage.CacheRead,
					}
				}
				record.ToolCalls = countToolCalls(msg.Content)
			}
			records = append(records, record)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func countToolCalls(content json.RawMessage) uint64 {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(content, &blocks) != nil {
		return 0
	}
	var count uint64
	for _, block := range blocks {
		if block.Type == "toolCall" {
			count++
		}
	}
	return count
}
