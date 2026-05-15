package gemini

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
	TmpDir       string
	DefaultModel string
}

func New(tmpDir string) *Analyzer {
	defaultModel := analyzer.ReadDefaultModelFromJSON(filepath.Join(filepath.Dir(tmpDir), "settings.json"))
	return &Analyzer{TmpDir: tmpDir, DefaultModel: defaultModel}
}

func (a *Analyzer) Name() string {
	return model.ToolGeminiCLI
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	return analyzer.WalkFiles(ctx, a.TmpDir, a.ValidPath)
}

func (a *Analyzer) ValidPath(path string) bool {
	ext := filepath.Ext(path)
	if ext != ".json" && ext != ".jsonl" {
		return false
	}
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "chats" {
			return true
		}
		next := filepath.Dir(dir)
		if next == dir {
			break
		}
	}
	return false
}

type sessionFile struct {
	SessionID string         `json:"sessionId"`
	Messages  []messageEntry `json:"messages"`
}

type messageEntry struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Content   json.RawMessage `json:"content"`
	Model     string          `json:"model"`
	Tokens    *tokenUsage     `json:"tokens"`
	ToolCalls []any           `json:"toolCalls"`
}

type tokenUsage struct {
	Input    uint64 `json:"input"`
	Output   uint64 `json:"output"`
	Cached   uint64 `json:"cached"`
	Thoughts uint64 `json:"thoughts"`
	Tool     uint64 `json:"tool"`
	Total    uint64 `json:"total"`
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	if filepath.Ext(source.Path) == ".jsonl" {
		return a.parseJSONL(ctx, source.Path)
	}
	return a.parseJSON(ctx, source.Path)
}

func (a *Analyzer) parseJSON(ctx context.Context, path string) ([]model.Record, error) {
	var session sessionFile
	if err := analyzer.ReadJSONFile(path, &session); err != nil {
		return nil, err
	}
	sessionID := session.SessionID
	if sessionID == "" {
		sessionID = path
	}
	return a.recordsFromMessages(ctx, hash.Sum(sessionID), session.Messages)
}

func (a *Analyzer) parseJSONL(ctx context.Context, path string) ([]model.Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	order := []string{}
	latest := map[string]messageEntry{}
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
		var msg messageEntry
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if msg.Type == "$set" || msg.Type == "" || msg.ID == "" {
			continue
		}
		if _, seen := latest[msg.ID]; !seen {
			order = append(order, msg.ID)
		}
		latest[msg.ID] = msg
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	messages := make([]messageEntry, 0, len(order))
	for _, id := range order {
		messages = append(messages, latest[id])
	}
	return a.recordsFromMessages(ctx, hash.Sum(path), messages)
}

func (a *Analyzer) recordsFromMessages(ctx context.Context, sessionID string, messages []messageEntry) ([]model.Record, error) {
	records := make([]model.Record, 0, len(messages))
	currentModel := a.firstSessionModel(messages)
	for _, msg := range messages {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		switch msg.Type {
		case "user":
			records = append(records, model.Record{
				Tool:      a.Name(),
				Model:     analyzer.ResolveModel(currentModel, a.DefaultModel),
				SessionID: sessionID,
				Role:      model.RoleUser,
			})
		case "gemini":
			currentModel = analyzer.ResolveModel(msg.Model, currentModel, a.DefaultModel)
			tokens := model.TokenStats{}
			if msg.Tokens != nil {
				tokens.Input = msg.Tokens.Input
				tokens.Output = msg.Tokens.Output
				tokens.Reasoning = msg.Tokens.Thoughts
				tokens.Cached = msg.Tokens.Cached
			}
			records = append(records, model.Record{
				Tool:      a.Name(),
				Model:     currentModel,
				SessionID: sessionID,
				Role:      model.RoleAssistant,
				Tokens:    tokens,
				ToolCalls: uint64(len(msg.ToolCalls)),
			})
		}
	}
	return records, nil
}

func (a *Analyzer) firstSessionModel(messages []messageEntry) string {
	for _, msg := range messages {
		if msg.Type == "gemini" {
			return analyzer.ResolveModel(msg.Model, a.DefaultModel)
		}
	}
	return analyzer.ResolveModel(a.DefaultModel)
}
