package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jimyag/ai-token-exporter/internal/model"
)

type Analyzer interface {
	Name() string
	Discover(ctx context.Context) ([]model.Source, error)
	Parse(ctx context.Context, source model.Source) ([]model.Record, error)
	ValidPath(path string) bool
}

func WalkFiles(ctx context.Context, root string, valid func(string) bool) ([]model.Source, error) {
	var sources []model.Source
	if root == "" {
		return sources, nil
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return sources, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.Type().IsRegular() && valid(path) {
			sources = append(sources, model.Source{Path: path})
		}
		return nil
	})
	return sources, err
}

func ParseTime(value string) time.Time {
	if value == "" {
		return time.Now().UTC()
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

func CountTokens(text string) uint64 {
	if text == "" {
		return 0
	}
	runes := utf8.RuneCountInString(text)
	if runes == 0 {
		return 0
	}
	return uint64((runes + 3) / 4)
}

func LastPathSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parts := strings.Split(value, "/")
	return parts[len(parts)-1]
}

func ExtractText(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if text := ExtractText(item); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		for _, key := range []string{"content", "text", "message", "output", "result", "error"} {
			if val, ok := v[key]; ok {
				if text := ExtractText(val); strings.TrimSpace(text) != "" {
					return text
				}
			}
		}
		parts := make([]string, 0, len(v))
		for _, val := range v {
			if text := ExtractText(val); strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func ExtractModel(value any) string {
	return extractModel(value, 0)
}

func extractModel(value any, depth int) string {
	if depth > 5 {
		return ""
	}
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"model", "model_name", "modelName", "modelId", "model_id", "newModel"} {
			if raw, ok := v[key].(string); ok && strings.TrimSpace(raw) != "" {
				return LastPathSegment(raw)
			}
		}
		for _, key := range []string{"metadata", "info", "context"} {
			if nested, ok := v[key]; ok {
				if modelName := extractModel(nested, depth+1); modelName != "" {
					return modelName
				}
			}
		}
	case []any:
		for _, item := range v {
			if modelName := extractModel(item, depth+1); modelName != "" {
				return modelName
			}
		}
	}
	return ""
}

func ReadJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

func ReadDefaultModelFromJSON(path string) string {
	var raw any
	if err := ReadJSONFile(path, &raw); err != nil {
		return ""
	}
	return ExtractModel(raw)
}

func ReadDefaultModelFromText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?m)^\s*model\s*=\s*["']([^"']+)["']`)
	match := re.FindSubmatch(data)
	if len(match) == 2 {
		return strings.TrimSpace(string(match[1]))
	}
	return ""
}

func ResolveModel(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return model.UnknownModel
}

func ErrNoRecords(path string) error {
	return errors.New("no parseable records in " + path)
}
