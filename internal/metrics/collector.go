package metrics

import (
	"strings"

	"github.com/jimyag/ai-token-exporter/internal/model"
	"github.com/prometheus/client_golang/prometheus"
)

type SnapshotProvider interface {
	Snapshot() model.Snapshot
}

type Collector struct {
	provider             SnapshotProvider
	includeSessionLabels bool

	tokensDesc       *prometheus.Desc
	messagesDesc     *prometheus.Desc
	toolCallsDesc    *prometheus.Desc
	sessionsDesc     *prometheus.Desc
	sourceFilesDesc  *prometheus.Desc
	parseErrorsDesc  *prometheus.Desc
	lastScanDesc     *prometheus.Desc
	lastSuccessDesc  *prometheus.Desc
	scanDurationDesc *prometheus.Desc
	lastScanSuccess  *prometheus.Desc
	buildInfoDesc    *prometheus.Desc
}

func NewCollector(provider SnapshotProvider, includeSessionLabels bool) *Collector {
	seriesLabels := []string{"tool", "model"}
	if includeSessionLabels {
		seriesLabels = append(seriesLabels, "session_id", "project_id")
	}
	return &Collector{
		provider:             provider,
		includeSessionLabels: includeSessionLabels,
		tokensDesc:           prometheus.NewDesc("ai_token_exporter_tokens", "Token usage in the current parsed snapshot.", append(seriesLabels, "token_type"), nil),
		messagesDesc:         prometheus.NewDesc("ai_token_exporter_messages", "Messages in the current parsed snapshot.", append(seriesLabels, "role"), nil),
		toolCallsDesc:        prometheus.NewDesc("ai_token_exporter_tool_calls", "Tool calls in the current parsed snapshot.", seriesLabels, nil),
		sessionsDesc:         prometheus.NewDesc("ai_token_exporter_sessions", "Distinct sessions discovered in the latest scan.", []string{"tool"}, nil),
		sourceFilesDesc:      prometheus.NewDesc("ai_token_exporter_source_files", "Source files discovered in the latest scan.", []string{"tool"}, nil),
		parseErrorsDesc:      prometheus.NewDesc("ai_token_exporter_source_parse_errors", "Source files that failed to parse in the latest scan.", []string{"tool"}, nil),
		lastScanDesc:         prometheus.NewDesc("ai_token_exporter_last_scan_timestamp_seconds", "Unix timestamp when the latest scan finished.", nil, nil),
		lastSuccessDesc:      prometheus.NewDesc("ai_token_exporter_last_successful_scan_timestamp_seconds", "Unix timestamp when the latest successful scan finished.", nil, nil),
		scanDurationDesc:     prometheus.NewDesc("ai_token_exporter_scan_duration_seconds", "Duration of the latest scan in seconds.", nil, nil),
		lastScanSuccess:      prometheus.NewDesc("ai_token_exporter_last_scan_success", "Whether the latest scan completed without fatal scanner errors.", nil, nil),
		buildInfoDesc:        prometheus.NewDesc("ai_token_exporter_build_info", "Build information for ai-token-exporter.", []string{"version", "commit"}, nil),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.tokensDesc
	ch <- c.messagesDesc
	ch <- c.toolCallsDesc
	ch <- c.sessionsDesc
	ch <- c.sourceFilesDesc
	ch <- c.parseErrorsDesc
	ch <- c.lastScanDesc
	ch <- c.lastSuccessDesc
	ch <- c.scanDurationDesc
	ch <- c.lastScanSuccess
	ch <- c.buildInfoDesc
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	snapshot := c.provider.Snapshot()
	for key, agg := range snapshot.Aggregates {
		base := c.seriesLabelValues(key)
		for tokenType, value := range tokenValues(agg.Tokens) {
			ch <- prometheus.MustNewConstMetric(c.tokensDesc, prometheus.GaugeValue, float64(value), append(base, tokenType)...)
		}
		for _, role := range []string{model.RoleUser, model.RoleAssistant} {
			if count := agg.Messages[role]; count > 0 {
				ch <- prometheus.MustNewConstMetric(c.messagesDesc, prometheus.GaugeValue, float64(count), append(base, role)...)
			}
		}
		if agg.ToolCalls > 0 {
			ch <- prometheus.MustNewConstMetric(c.toolCallsDesc, prometheus.GaugeValue, float64(agg.ToolCalls), base...)
		}
	}
	for tool, stat := range snapshot.Tools {
		ch <- prometheus.MustNewConstMetric(c.sessionsDesc, prometheus.GaugeValue, float64(stat.Sessions), tool)
		ch <- prometheus.MustNewConstMetric(c.sourceFilesDesc, prometheus.GaugeValue, float64(stat.SourceFiles), tool)
		ch <- prometheus.MustNewConstMetric(c.parseErrorsDesc, prometheus.GaugeValue, float64(stat.ParseErrors), tool)
	}
	if !snapshot.LastScan.IsZero() {
		ch <- prometheus.MustNewConstMetric(c.lastScanDesc, prometheus.GaugeValue, float64(snapshot.LastScan.Unix()))
	}
	if !snapshot.LastSuccessfulScan.IsZero() {
		ch <- prometheus.MustNewConstMetric(c.lastSuccessDesc, prometheus.GaugeValue, float64(snapshot.LastSuccessfulScan.Unix()))
	}
	ch <- prometheus.MustNewConstMetric(c.scanDurationDesc, prometheus.GaugeValue, snapshot.ScanDuration.Seconds())
	success := 0.0
	if snapshot.LastScanSuccess {
		success = 1
	}
	ch <- prometheus.MustNewConstMetric(c.lastScanSuccess, prometheus.GaugeValue, success)
	ch <- prometheus.MustNewConstMetric(c.buildInfoDesc, prometheus.GaugeValue, 1, nonEmpty(snapshot.Version, "dev"), nonEmpty(snapshot.Commit, "none"))
}

func (c *Collector) seriesLabelValues(key model.SeriesKey) []string {
	values := []string{key.Tool, key.Model}
	if c.includeSessionLabels {
		values = append(values, key.SessionID, key.ProjectID)
	}
	return values
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

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
