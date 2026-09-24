package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/hash"
	"github.com/jimyag/ai-token-exporter/internal/model"
	"github.com/klauspost/compress/zstd"
)

type Analyzer struct{ SessionsDir string }

func New(sessionsDir string) *Analyzer { return &Analyzer{SessionsDir: sessionsDir} }
func (a *Analyzer) Name() string       { return model.ToolDeepSeekHarness }

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	return analyzer.WalkFiles(ctx, a.SessionsDir, a.ValidPath)
}

func (a *Analyzer) ValidPath(path string) bool {
	if filepath.Base(path) != "session.jsonl.zstd" {
		return false
	}
	relative, err := filepath.Rel(a.SessionsDir, path)
	return err == nil && len(strings.Split(relative, string(filepath.Separator))) == 3 && !strings.HasPrefix(relative, "..")
}

type event struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Seq           uint64 `json:"seq"`
	Time          int64  `json:"time"`
	ParentSession string `json:"parentSession"`
	SeedLength    uint64 `json:"seedLength"`
	Data          struct {
		ID     string `json:"id"`
		Source struct {
			Kind string `json:"kind"`
		} `json:"source"`
		Turn    uint64 `json:"turn"`
		Step    uint64 `json:"step"`
		Message struct {
			ID     string `json:"id"`
			Source struct {
				Model string `json:"model"`
			} `json:"source"`
		} `json:"message"`
		Usage struct {
			Input      uint64 `json:"inputTokens"`
			Output     uint64 `json:"outputTokens"`
			CacheRead  uint64 `json:"cacheReadTokens"`
			CacheWrite uint64 `json:"cacheWriteTokens"`
			Reasoning  uint64 `json:"reasoningTokens"`
		} `json:"usage"`
	} `json:"data"`
}

type step struct{ turn, step uint64 }

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	file, err := os.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	decoder, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	// ponytail: one decompressed session stays in memory; stream frames if sessions grow too large.
	data, readErr := io.ReadAll(decoder)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, readErr
	}

	sessionID := hash.Sum(filepath.Dir(source.Path))
	var seedLength uint64
	var skipSeed bool
	var records []model.Record
	assistantSteps := map[step]int{}
	lastTime := info.ModTime().UTC()
	lines := bytes.Split(data, []byte{'\n'})
	for i, line := range lines {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if i == len(lines)-1 && readErr != nil {
			break // An incomplete compressed tail cannot contribute a complete event.
		}
		var item event
		if err := json.Unmarshal(line, &item); err != nil {
			continue
		}
		if item.Type == "session" {
			if item.ID != "" {
				sessionID = hash.Sum(item.ID)
			}
			seedLength = item.SeedLength
			if item.ParentSession != "" && filepath.Base(item.ParentSession) == item.ParentSession {
				parentPath := filepath.Join(filepath.Dir(filepath.Dir(source.Path)), item.ParentSession, "session.jsonl.zstd")
				_, err := os.Stat(parentPath)
				skipSeed = err == nil
			}
			continue
		}
		// Forked sessions replay their parent's events. Count the seed only in the parent.
		if skipSeed && item.Seq < seedLength {
			continue
		}
		timestamp := lastTime
		if item.Time != 0 {
			timestamp = time.UnixMilli(item.Time).UTC()
			lastTime = timestamp
		}
		switch item.Type {
		case "user/message":
			if item.Data.Source.Kind == "user" {
				records = append(records, model.Record{Tool: a.Name(), Model: model.UnknownModel,
					SessionID: sessionID, Role: model.RoleUser, Timestamp: timestamp})
			}
		case "assistant/message":
			usage := item.Data.Usage
			assistantSteps[step{item.Data.Turn, item.Data.Step}] = len(records)
			records = append(records, model.Record{Tool: a.Name(),
				Model: analyzer.ResolveModel(item.Data.Message.Source.Model), SessionID: sessionID,
				Role: model.RoleAssistant, Timestamp: timestamp,
				Tokens: model.TokenStats{
					Input:  usage.Input + usage.CacheRead + usage.CacheWrite,
					Output: usage.Output, Reasoning: usage.Reasoning,
					CacheRead: usage.CacheRead, CacheCreation: usage.CacheWrite, Cached: usage.CacheRead,
				},
			})
		case "tool/call":
			if index, ok := assistantSteps[step{item.Data.Turn, item.Data.Step}]; ok {
				records[index].ToolCalls++
			}
		}
	}
	return records, nil
}
