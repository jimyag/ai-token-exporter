package backfill

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

type Options struct {
	From     time.Time
	To       time.Time
	Step     time.Duration
	Job      string
	Instance string
	Hostname string
	Version  string
	Commit   string
	VMURL    string
}

type Result struct {
	SourceFiles uint64
	ParseErrors uint64
	Records     uint64
	Samples     uint64
}

func Run(ctx context.Context, analyzers []analyzer.Analyzer, options Options, stdout io.Writer) (Result, error) {
	records, result, err := collectRecords(ctx, analyzers)
	if err != nil {
		return result, err
	}
	result.Records = uint64(len(records))

	var buf bytes.Buffer
	result.Samples, err = WritePrometheusImport(&buf, records, options)
	if err != nil {
		return result, err
	}
	if options.VMURL == "" {
		_, err = io.Copy(stdout, &buf)
		return result, err
	}
	return result, postVictoriaMetrics(ctx, options.VMURL, &buf)
}

func collectRecords(ctx context.Context, analyzers []analyzer.Analyzer) ([]model.Record, Result, error) {
	var result Result
	var records []model.Record
	for _, az := range analyzers {
		sources, err := az.Discover(ctx)
		if err != nil {
			return records, result, err
		}
		result.SourceFiles += uint64(len(sources))
		for _, source := range sources {
			parsed, err := az.Parse(ctx, source)
			if err != nil {
				result.ParseErrors++
				continue
			}
			for _, record := range parsed {
				record.Tool = nonEmpty(record.Tool, az.Name())
				record.Model = model.NormalizeModel(record.Model)
				record.Timestamp = nonZeroTime(record.Timestamp)
				records = append(records, record)
			}
		}
	}
	return records, result, nil
}

func WritePrometheusImport(w io.Writer, records []model.Record, options Options) (uint64, error) {
	options = normalizeOptions(options, records)
	if len(records) == 0 || options.From.After(options.To) {
		return 0, nil
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Timestamp.Before(records[j].Timestamp)
	})

	state := newState()
	var samples uint64
	idx := 0
	for ts := options.From; !ts.After(options.To); ts = ts.Add(options.Step) {
		for idx < len(records) && !records[idx].Timestamp.After(ts) {
			state.add(records[idx])
			idx++
		}
		written, err := writeState(w, state, options, ts)
		if err != nil {
			return samples, err
		}
		samples += written
	}
	return samples, nil
}

type state struct {
	aggregates map[model.SeriesKey]model.Aggregate
	sessions   map[string]map[string]bool
}

func newState() state {
	return state{
		aggregates: make(map[model.SeriesKey]model.Aggregate),
		sessions:   make(map[string]map[string]bool),
	}
}

func (s state) add(record model.Record) {
	if record.SessionID != "" {
		if s.sessions[record.Tool] == nil {
			s.sessions[record.Tool] = make(map[string]bool)
		}
		s.sessions[record.Tool][record.SessionID] = true
	}
	key := model.SeriesKey{Tool: record.Tool, Model: record.Model}
	agg := s.aggregates[key]
	if agg.Messages == nil {
		agg.Messages = make(map[string]uint64)
	}
	if record.Role != "" {
		agg.Messages[record.Role]++
	}
	agg.Tokens.Input += record.Tokens.Input
	agg.Tokens.Output += record.Tokens.Output
	agg.Tokens.Reasoning += record.Tokens.Reasoning
	agg.Tokens.CacheCreation += record.Tokens.CacheCreation
	agg.Tokens.CacheRead += record.Tokens.CacheRead
	agg.Tokens.Cached += record.Tokens.Cached
	agg.ToolCalls += record.ToolCalls
	s.aggregates[key] = agg
}

func writeState(w io.Writer, state state, options Options, ts time.Time) (uint64, error) {
	base := map[string]string{
		"job":      options.Job,
		"instance": options.Instance,
		"hostname": options.Hostname,
	}
	var samples uint64
	for key, agg := range state.aggregates {
		seriesLabels := cloneLabels(base)
		seriesLabels["tool"] = key.Tool
		seriesLabels["model"] = key.Model
		for tokenType, value := range tokenValues(agg.Tokens) {
			if value == 0 {
				continue
			}
			labels := cloneLabels(seriesLabels)
			labels["token_type"] = tokenType
			if err := writeSample(w, "ai_token_exporter_tokens", labels, value, ts); err != nil {
				return samples, err
			}
			samples++
		}
		for _, role := range []string{model.RoleUser, model.RoleAssistant} {
			value := agg.Messages[role]
			if value == 0 {
				continue
			}
			labels := cloneLabels(seriesLabels)
			labels["role"] = role
			if err := writeSample(w, "ai_token_exporter_messages", labels, value, ts); err != nil {
				return samples, err
			}
			samples++
		}
		if agg.ToolCalls > 0 {
			if err := writeSample(w, "ai_token_exporter_tool_calls", seriesLabels, agg.ToolCalls, ts); err != nil {
				return samples, err
			}
			samples++
		}
	}
	for tool, sessions := range state.sessions {
		if len(sessions) == 0 {
			continue
		}
		labels := cloneLabels(base)
		labels["tool"] = tool
		if err := writeSample(w, "ai_token_exporter_sessions", labels, uint64(len(sessions)), ts); err != nil {
			return samples, err
		}
		samples++
	}
	buildLabels := cloneLabels(base)
	buildLabels["version"] = nonEmpty(options.Version, "dev")
	buildLabels["commit"] = nonEmpty(options.Commit, "none")
	if err := writeSample(w, "ai_token_exporter_build_info", buildLabels, 1, ts); err != nil {
		return samples, err
	}
	samples++
	return samples, nil
}

func writeSample(w io.Writer, name string, labels map[string]string, value uint64, ts time.Time) error {
	_, err := fmt.Fprintf(w, "%s%s %d %d\n", name, formatLabels(labels), value, ts.UnixMilli())
	return err
}

func formatLabels(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key, value := range labels {
		if value != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteString("=\"")
		b.WriteString(escapeLabelValue(labels[key]))
		b.WriteByte('"')
	}
	b.WriteByte('}')
	return b.String()
}

func escapeLabelValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}

func normalizeOptions(options Options, records []model.Record) Options {
	if options.Step <= 0 {
		options.Step = time.Minute
	}
	options.Job = nonEmpty(options.Job, "ai-token-exporter")
	options.Instance = nonEmpty(options.Instance, "backfill")
	options.Hostname = nonEmpty(options.Hostname, "unknown")
	if options.From.IsZero() || options.To.IsZero() {
		minTime, maxTime := recordBounds(records)
		if options.From.IsZero() {
			options.From = minTime.Truncate(options.Step)
		}
		if options.To.IsZero() {
			options.To = maxTime.Truncate(options.Step)
			if options.To.Before(maxTime) {
				options.To = options.To.Add(options.Step)
			}
		}
	}
	return options
}

func recordBounds(records []model.Record) (time.Time, time.Time) {
	if len(records) == 0 {
		now := time.Now().UTC()
		return now, now
	}
	minTime := nonZeroTime(records[0].Timestamp)
	maxTime := minTime
	for _, record := range records[1:] {
		ts := nonZeroTime(record.Timestamp)
		if ts.Before(minTime) {
			minTime = ts
		}
		if ts.After(maxTime) {
			maxTime = ts
		}
	}
	return minTime, maxTime
}

func nonZeroTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func cloneLabels(labels map[string]string) map[string]string {
	cloned := make(map[string]string, len(labels))
	for key, value := range labels {
		cloned[key] = value
	}
	return cloned
}

func tokenValues(tokens model.TokenStats) map[string]uint64 {
	return map[string]uint64{
		"input":          tokens.Input,
		"output":         tokens.Output,
		"reasoning":      tokens.Reasoning,
		"cache_creation": tokens.CacheCreation,
		"cache_read":     tokens.CacheRead,
		"cached":         tokens.Cached,
	}
}

func postVictoriaMetrics(ctx context.Context, url string, body *bytes.Buffer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body.Bytes()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("victoriametrics import failed: status=%s body=%s", resp.Status, strings.TrimSpace(string(data)))
	}
	return nil
}
