package scanner

import (
	"context"
	"sync"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

type Scanner struct {
	analyzers []analyzer.Analyzer
	version   string
	commit    string

	mu       sync.RWMutex
	snapshot model.Snapshot
}

func New(analyzers []analyzer.Analyzer, version, commit string) *Scanner {
	return &Scanner{
		analyzers: analyzers,
		version:   version,
		commit:    commit,
		snapshot: model.Snapshot{
			Aggregates: make(map[model.SeriesKey]model.Aggregate),
			Tools:      make(map[string]model.ToolSnapshot),
			Version:    version,
			Commit:     commit,
		},
	}
}

func (s *Scanner) Run(ctx context.Context, interval time.Duration) {
	s.ScanOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.ScanOnce(ctx)
		}
	}
}

func (s *Scanner) ScanOnce(ctx context.Context) model.Snapshot {
	start := time.Now()
	previous := s.Snapshot()
	next := model.Snapshot{
		Aggregates:         make(map[model.SeriesKey]model.Aggregate),
		Tools:              make(map[string]model.ToolSnapshot),
		LastSuccessfulScan: previous.LastSuccessfulScan,
		Version:            s.version,
		Commit:             s.commit,
	}
	success := true

	for _, az := range s.analyzers {
		tool := az.Name()
		toolStat := model.ToolSnapshot{}
		sessions := map[string]bool{}

		sources, err := az.Discover(ctx)
		if err != nil {
			success = false
			next.Tools[tool] = toolStat
			continue
		}
		toolStat.SourceFiles = uint64(len(sources))

		for _, source := range sources {
			records, err := az.Parse(ctx, source)
			if err != nil {
				toolStat.ParseErrors++
				continue
			}
			for _, record := range records {
				record.Model = model.NormalizeModel(record.Model)
				if record.SessionID != "" {
					sessions[record.SessionID] = true
				}
				key := model.SeriesKey{
					Tool:  record.Tool,
					Model: record.Model,
				}
				agg := next.Aggregates[key]
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
				next.Aggregates[key] = agg
			}
		}
		toolStat.Sessions = uint64(len(sessions))
		next.Tools[tool] = toolStat
	}

	next.LastScan = time.Now()
	next.ScanDuration = next.LastScan.Sub(start)
	next.LastScanSuccess = success
	if success {
		next.LastSuccessfulScan = next.LastScan
	}

	s.mu.Lock()
	s.snapshot = next
	s.mu.Unlock()
	return next
}

func (s *Scanner) Snapshot() model.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.snapshot)
}

func cloneSnapshot(in model.Snapshot) model.Snapshot {
	out := in
	out.Aggregates = make(map[model.SeriesKey]model.Aggregate, len(in.Aggregates))
	for key, agg := range in.Aggregates {
		copied := agg
		copied.Messages = make(map[string]uint64, len(agg.Messages))
		for role, count := range agg.Messages {
			copied.Messages[role] = count
		}
		out.Aggregates[key] = copied
	}
	out.Tools = make(map[string]model.ToolSnapshot, len(in.Tools))
	for tool, stat := range in.Tools {
		out.Tools[tool] = stat
	}
	return out
}
