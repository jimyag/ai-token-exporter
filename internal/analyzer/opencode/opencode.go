package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/hash"
	"github.com/jimyag/ai-token-exporter/internal/model"
	_ "modernc.org/sqlite"
)

type Analyzer struct{ DataDir string }

func New(dataDir string) *Analyzer { return &Analyzer{DataDir: dataDir} }
func (a *Analyzer) Name() string   { return model.ToolOpenCode }

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	legacy, err := analyzer.WalkFiles(ctx, filepath.Join(a.DataDir, "storage", "message"), a.ValidPath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(a.DataDir)
	if os.IsNotExist(err) {
		return legacy, nil
	}
	if err != nil {
		return nil, err
	}
	var databases []model.Source
	for _, entry := range entries {
		path := filepath.Join(a.DataDir, entry.Name())
		if entry.Type().IsRegular() && a.ValidPath(path) {
			databases = append(databases, model.Source{Path: path})
		}
	}
	// Keep the SQLite copy when a migration left the same message as legacy JSON.
	seen := map[string]bool{}
	for _, source := range databases {
		ids, err := messageIDs(ctx, source.Path)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Parse reports the database error; legacy files can still contribute.
			continue
		}
		for id := range ids {
			seen[id] = true
		}
	}
	for _, source := range legacy {
		if !seen[strings.TrimSuffix(filepath.Base(source.Path), ".json")] {
			databases = append(databases, source)
		}
	}
	return databases, nil
}

func (a *Analyzer) ValidPath(path string) bool {
	name := filepath.Base(path)
	if filepath.Dir(path) == filepath.Clean(a.DataDir) {
		return name == "opencode.db" || strings.HasPrefix(name, "opencode-") && strings.HasSuffix(name, ".db")
	}
	if filepath.Ext(path) != ".json" {
		return false
	}
	rel, err := filepath.Rel(filepath.Join(a.DataDir, "storage", "message"), path)
	return err == nil && !strings.HasPrefix(rel, "..") && len(strings.Split(rel, string(filepath.Separator))) == 2
}

type message struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Role      string `json:"role"`
	Time      struct {
		Created int64 `json:"created"`
	} `json:"time"`
	ModelID string `json:"modelID"`
	Model   struct {
		ModelID string `json:"modelID"`
	} `json:"model"`
	Tokens messageTokens `json:"tokens"`
}

type part struct {
	Type   string        `json:"type"`
	Tool   string        `json:"tool"`
	Tokens messageTokens `json:"tokens"`
}

type messageTokens struct {
	Input     uint64 `json:"input"`
	Output    uint64 `json:"output"`
	Reasoning uint64 `json:"reasoning"`
	Cache     struct {
		Read  uint64 `json:"read"`
		Write uint64 `json:"write"`
	} `json:"cache"`
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	if filepath.Ext(source.Path) == ".db" {
		return a.parseDB(ctx, source.Path)
	}
	var msg message
	if err := analyzer.ReadJSONFile(source.Path, &msg); err != nil {
		return nil, err
	}
	if msg.ID == "" {
		msg.ID = strings.TrimSuffix(filepath.Base(source.Path), ".json")
	}
	if msg.SessionID == "" {
		msg.SessionID = filepath.Base(filepath.Dir(source.Path))
	}
	record, ok := a.record(msg)
	if !ok {
		return nil, nil
	}
	if msg.Role == model.RoleAssistant {
		partsDir := filepath.Join(a.DataDir, "storage", "part", msg.ID)
		entries, err := os.ReadDir(partsDir)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		var fallback model.TokenStats
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			var p part
			if analyzer.ReadJSONFile(filepath.Join(partsDir, entry.Name()), &p) == nil {
				if p.Type == "tool" {
					record.ToolCalls++
				} else if p.Type == "step-finish" {
					addTokens(&fallback, tokenStats(p.Tokens))
				}
			}
		}
		if record.Tokens == (model.TokenStats{}) {
			record.Tokens = fallback
		}
	}
	return []model.Record{record}, nil
}

func (a *Analyzer) record(msg message) (model.Record, bool) {
	if msg.Role != model.RoleUser && msg.Role != model.RoleAssistant {
		return model.Record{}, false
	}
	modelName := msg.ModelID
	if modelName == "" {
		modelName = msg.Model.ModelID
	}
	record := model.Record{
		Tool: a.Name(), Model: analyzer.ResolveModel(modelName), SessionID: hash.Sum(msg.SessionID),
		Role: msg.Role, Timestamp: time.UnixMilli(msg.Time.Created).UTC(),
	}
	if msg.Role == model.RoleAssistant {
		record.Tokens = tokenStats(msg.Tokens)
	}
	return record, true
}

func tokenStats(tokens messageTokens) model.TokenStats {
	return model.TokenStats{
		Input:  tokens.Input + tokens.Cache.Read + tokens.Cache.Write,
		Output: tokens.Output, Reasoning: tokens.Reasoning,
		CacheRead: tokens.Cache.Read, CacheCreation: tokens.Cache.Write,
		Cached: tokens.Cache.Read,
	}
}

func addTokens(dst *model.TokenStats, src model.TokenStats) {
	dst.Input += src.Input
	dst.Output += src.Output
	dst.Reasoning += src.Reasoning
	dst.CacheRead += src.CacheRead
	dst.CacheCreation += src.CacheCreation
	dst.Cached += src.Cached
}

func openDB(path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func messageIDs(ctx context.Context, path string) (map[string]bool, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT id FROM message")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids[id] = true
	}
	return ids, rows.Err()
}

func (a *Analyzer) parseDB(ctx context.Context, path string) ([]model.Record, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT id, session_id, time_created, data FROM message")
	if err != nil {
		return nil, err
	}
	var records []model.Record
	indices := map[string]int{}
	fallback := map[string]model.TokenStats{}
	for rows.Next() {
		var id, sessionID, data string
		var created int64
		if err := rows.Scan(&id, &sessionID, &created, &data); err != nil {
			rows.Close()
			return nil, err
		}
		var msg message
		if json.Unmarshal([]byte(data), &msg) != nil {
			continue
		}
		msg.ID, msg.SessionID = id, sessionID
		if msg.Time.Created == 0 {
			msg.Time.Created = created
		}
		if record, ok := a.record(msg); ok {
			indices[id] = len(records)
			records = append(records, record)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	parts, err := db.QueryContext(ctx, "SELECT message_id, data FROM part WHERE json_extract(data, '$.type') IN ('tool', 'step-finish')")
	if err != nil {
		return nil, err
	}
	defer parts.Close()
	for parts.Next() {
		var id, data string
		if err := parts.Scan(&id, &data); err != nil {
			return nil, err
		}
		index, ok := indices[id]
		if !ok || records[index].Role != model.RoleAssistant {
			continue
		}
		var p part
		if json.Unmarshal([]byte(data), &p) == nil {
			if p.Type == "tool" {
				records[index].ToolCalls++
			} else if p.Type == "step-finish" {
				tokens := fallback[id]
				addTokens(&tokens, tokenStats(p.Tokens))
				fallback[id] = tokens
			}
		}
	}
	if err := parts.Err(); err != nil {
		return nil, err
	}
	for id, tokens := range fallback {
		if index, ok := indices[id]; ok && records[index].Tokens == (model.TokenStats{}) {
			records[index].Tokens = tokens
		}
	}
	return records, nil
}
