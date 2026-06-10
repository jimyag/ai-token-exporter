package agy

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/hash"
	"github.com/jimyag/ai-token-exporter/internal/model"
	_ "modernc.org/sqlite"
)

const defaultModel = "gemini-2.5-flash"

type Analyzer struct {
	ConversationsDir string
}

func New(conversationsDir string) *Analyzer {
	return &Analyzer{ConversationsDir: conversationsDir}
}

func (a *Analyzer) Name() string {
	return model.ToolAgy
}

func (a *Analyzer) Discover(ctx context.Context) ([]model.Source, error) {
	return analyzer.WalkFiles(ctx, a.ConversationsDir, a.ValidPath)
}

func (a *Analyzer) ValidPath(path string) bool {
	return filepath.Ext(path) == ".db"
}

type genMetaInfo struct {
	ModelID         string
	InputTokens     uint64
	OutputTokens    uint64
	ReasoningTokens uint64
}

type stepRow struct {
	Index   int
	Type    int
	Payload []byte
}

func (a *Analyzer) Parse(ctx context.Context, source model.Source) ([]model.Record, error) {
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(source.Path))
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		return nil, err
	}

	genMeta, err := readGenMetadata(ctx, db)
	if err != nil {
		return nil, err
	}
	steps, err := readSteps(ctx, db)
	if err != nil {
		return nil, err
	}

	sessionID := hash.Sum(source.Path)
	currentModel := ""
	records := make([]model.Record, 0, len(steps))
	for _, step := range steps {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		if len(step.Payload) == 0 {
			continue
		}
		fields, ok := protoParse(step.Payload)
		if !ok {
			continue
		}
		toolCalls := countToolCalls(fields)

		record := model.Record{
			Tool:      a.Name(),
			Model:     analyzer.ResolveModel(currentModel, defaultModel),
			SessionID: sessionID,
			Timestamp: earliestTimestamp(fields),
		}
		if record.Timestamp.IsZero() {
			record.Timestamp = time.Now().UTC()
		}

		switch step.Type {
		case 14:
			if stepText(fields) == "" {
				continue
			}
			record.Role = model.RoleUser
		case 15:
			record.Role = model.RoleAssistant
			record.ToolCalls = toolCalls
			if info, ok := genMeta[step.Index]; ok {
				record.Model = analyzer.ResolveModel(info.ModelID, currentModel, defaultModel)
				record.Tokens.Input = info.InputTokens
				record.Tokens.Output = info.OutputTokens
				record.Tokens.Reasoning = info.ReasoningTokens
				currentModel = record.Model
			} else if len(genMeta) == 0 {
				text := stepText(fields)
				if text == "" {
					continue
				}
				record.Model = analyzer.ResolveModel(currentModel, defaultModel)
				record.Tokens.Output = analyzer.CountTokens(text)
				currentModel = record.Model
			}
		default:
			if toolCalls == 0 {
				continue
			}
			record.ToolCalls = toolCalls
		}
		records = append(records, record)
	}
	return records, nil
}

func sqliteReadOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	return u.String()
}

func readGenMetadata(ctx context.Context, db *sql.DB) (map[int]genMetaInfo, error) {
	out := map[int]genMetaInfo{}
	rows, err := db.QueryContext(ctx, "SELECT idx, data FROM gen_metadata")
	if err != nil {
		if sqliteNoSuchTable(err) {
			return out, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var idx int
		var data []byte
		if err := rows.Scan(&idx, &data); err != nil {
			return nil, err
		}
		fields, ok := protoParse(data)
		if !ok {
			continue
		}
		if info, ok := parseGenMetadata(fields); ok {
			out[idx] = info
		}
	}
	return out, rows.Err()
}

func readSteps(ctx context.Context, db *sql.DB) ([]stepRow, error) {
	rows, err := db.QueryContext(ctx, "SELECT idx, step_type, step_payload FROM steps ORDER BY idx")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var steps []stepRow
	for rows.Next() {
		var step stepRow
		if err := rows.Scan(&step.Index, &step.Type, &step.Payload); err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

func sqliteNoSuchTable(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no such table")
}

func parseGenMetadata(fields []protoField) (genMetaInfo, bool) {
	envelope := findField(fields, 1)
	if envelope == nil || len(envelope.Nested) == 0 {
		return genMetaInfo{}, false
	}
	info := genMetaInfo{}
	for _, field := range envelope.Nested {
		switch field.Number {
		case 19:
			info.ModelID, _ = protoString(field)
		case 4:
			for _, tokenField := range field.Nested {
				switch tokenField.Number {
				case 5:
					info.InputTokens = tokenField.Varint
				case 2:
					info.OutputTokens = tokenField.Varint
				case 3:
					info.ReasoningTokens = tokenField.Varint
				}
			}
		}
	}
	if info.ModelID == "" && info.InputTokens == 0 && info.OutputTokens == 0 && info.ReasoningTokens == 0 {
		return genMetaInfo{}, false
	}
	return info, true
}

func countToolCalls(fields []protoField) uint64 {
	var count uint64
	for _, field := range fields {
		if field.Number != 20 {
			continue
		}
		for _, nested20 := range field.Nested {
			if nested20.Number != 7 {
				continue
			}
			for _, nested7 := range nested20.Nested {
				if nested7.Number == 2 {
					if toolName, ok := protoString(nested7); ok && toolName != "" {
						count++
					}
				}
			}
		}
	}
	return count
}

func earliestTimestamp(fields []protoField) time.Time {
	var best time.Time
	var walk func([]protoField)
	walk = func(fields []protoField) {
		for _, field := range fields {
			if ts, ok := protoTimestamp(field.Nested); ok {
				if best.IsZero() || ts.Before(best) {
					best = ts
				}
			}
			if len(field.Nested) > 0 {
				walk(field.Nested)
			}
		}
	}
	walk(fields)
	return best
}

func protoTimestamp(fields []protoField) (time.Time, bool) {
	var seconds int64
	var nanos uint64
	seenSeconds := false
	for _, field := range fields {
		if field.Wire != wireVarint {
			return time.Time{}, false
		}
		switch field.Number {
		case 1:
			seconds = int64(field.Varint)
			seenSeconds = true
		case 2:
			nanos = field.Varint
		default:
			return time.Time{}, false
		}
	}
	if !seenSeconds || seconds <= 946684800 || seconds >= 4102444800 || nanos > 999999999 {
		return time.Time{}, false
	}
	return time.Unix(seconds, int64(nanos)).UTC(), true
}

func stepText(fields []protoField) string {
	var out []string
	var walk func([]protoField)
	walk = func(fields []protoField) {
		for _, field := range fields {
			if field.Wire == wireBytes && len(field.Nested) == 0 {
				if value, ok := protoString(field); ok {
					value = strings.TrimSpace(value)
					if len([]rune(value)) >= 20 {
						out = append(out, value)
					}
				}
			}
			if len(field.Nested) > 0 {
				walk(field.Nested)
			}
		}
	}
	walk(fields)
	seen := map[string]bool{}
	unique := make([]string, 0, len(out))
	for _, value := range out {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return strings.Join(unique, "\n\n")
}

func findField(fields []protoField, number uint32) *protoField {
	for i := range fields {
		if fields[i].Number == number {
			return &fields[i]
		}
	}
	return nil
}

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireBytes   = 2
	wireFixed32 = 5
	maxDepth    = 32
)

type protoField struct {
	Number uint32
	Wire   uint8
	Varint uint64
	Fixed  []byte
	Bytes  []byte
	Nested []protoField
}

func protoParse(data []byte) ([]protoField, bool) {
	return protoParseDepth(data, 0)
}

func protoParseDepth(data []byte, depth int) ([]protoField, bool) {
	if depth > maxDepth {
		return nil, false
	}
	fields := []protoField{}
	for len(data) > 0 {
		tag, rest, ok := readVarint(data)
		if !ok {
			return nil, false
		}
		data = rest
		number := uint32(tag >> 3)
		wire := uint8(tag & 0x7)
		if number == 0 {
			return nil, false
		}
		field := protoField{Number: number, Wire: wire}
		switch wire {
		case wireVarint:
			value, rest, ok := readVarint(data)
			if !ok {
				return nil, false
			}
			field.Varint = value
			data = rest
		case wireFixed64:
			if len(data) < 8 {
				return nil, false
			}
			field.Fixed = data[:8]
			data = data[8:]
		case wireBytes:
			size, rest, ok := readVarint(data)
			if !ok || uint64(len(rest)) < size {
				return nil, false
			}
			sizeInt := int(size)
			field.Bytes = rest[:sizeInt]
			data = rest[sizeInt:]
			if nested, ok := protoParseDepth(field.Bytes, depth+1); ok && looksLikeMessage(nested) {
				field.Nested = nested
			}
		case wireFixed32:
			if len(data) < 4 {
				return nil, false
			}
			field.Fixed = data[:4]
			data = data[4:]
		case 3, 4:
		default:
			return nil, false
		}
		fields = append(fields, field)
	}
	return fields, true
}

func readVarint(data []byte) (uint64, []byte, bool) {
	var value uint64
	var shift uint
	for i, b := range data {
		value |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return value, data[i+1:], true
		}
		shift += 7
		if shift >= 64 {
			break
		}
	}
	return 0, nil, false
}

func looksLikeMessage(fields []protoField) bool {
	if len(fields) == 0 {
		return false
	}
	for _, field := range fields {
		if field.Number == 0 || field.Number > 100000 {
			return false
		}
	}
	return true
}

func protoString(field protoField) (string, bool) {
	if field.Wire != wireBytes || len(field.Nested) > 0 {
		return "", false
	}
	if !utf8.Valid(field.Bytes) {
		return "", false
	}
	return string(field.Bytes), true
}
