package scanner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/model"
)

type Scanner struct {
	analyzers []analyzer.Analyzer
	version   string
	commit    string

	scanMu sync.Mutex
	cache  map[sourceCacheKey]sourceCacheEntry

	mu       sync.RWMutex
	snapshot model.Snapshot
}

type sourceCacheKey struct {
	tool string
	path string
}

type sourceSignature struct {
	source fileSignature
	wal    fileSignature
}

type fileSignature struct {
	exists      bool
	size        int64
	modTimeNano int64
}

type sourceContribution struct {
	aggregates map[model.SeriesKey]model.Aggregate
	sessions   map[string]struct{}
}

type sourceCacheEntry struct {
	signature    sourceSignature
	contribution sourceContribution
}

func New(analyzers []analyzer.Analyzer, version, commit string) *Scanner {
	return &Scanner{
		analyzers: analyzers,
		version:   version,
		commit:    commit,
		cache:     make(map[sourceCacheKey]sourceCacheEntry),
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
	s.scanMu.Lock()
	defer s.scanMu.Unlock()

	start := time.Now()
	next := model.Snapshot{
		Aggregates:         make(map[model.SeriesKey]model.Aggregate),
		Tools:              make(map[string]model.ToolSnapshot),
		LastSuccessfulScan: s.lastSuccessfulScan(),
		Version:            s.version,
		Commit:             s.commit,
	}
	success := true
	nextCache := make(map[sourceCacheKey]sourceCacheEntry)

	for _, az := range s.analyzers {
		tool := az.Name()
		toolStat := model.ToolSnapshot{}
		sessions := make(map[string]struct{})

		sources, err := az.Discover(ctx)
		if err != nil {
			success = false
			next.Tools[tool] = toolStat
			continue
		}
		toolStat.SourceFiles = uint64(len(sources))

		for _, source := range sources {
			key := sourceCacheKey{tool: tool, path: source.Path}
			signature, signatureOK := statSource(source.Path)
			entry, cacheHit := s.cache[key]
			cacheHit = cacheHit && signatureOK && entry.signature == signature

			var contribution sourceContribution
			if cacheHit {
				toolStat.CacheHits++
				contribution = entry.contribution
				nextCache[key] = entry
			} else {
				toolStat.CacheMisses++
				records, err := az.Parse(ctx, source)
				if err != nil {
					success = false
					toolStat.ParseErrors++
					continue
				}
				contribution = contributionFromRecords(records)
				if signatureOK {
					nextCache[key] = sourceCacheEntry{
						signature:    signature,
						contribution: contribution,
					}
				}
			}
			mergeContribution(next.Aggregates, sessions, contribution)
		}
		toolStat.Sessions = uint64(len(sessions))
		next.Tools[tool] = toolStat
	}
	s.cache = nextCache

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

func statSource(path string) (sourceSignature, bool) {
	source, ok := statFile(path)
	if !ok {
		return sourceSignature{}, false
	}
	return sourceSignature{
		source: source,
		wal:    statSQLiteWAL(path),
	}, true
}

func statFile(path string) (fileSignature, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return fileSignature{}, false
	}
	return fileSignature{
		exists:      true,
		size:        info.Size(),
		modTimeNano: info.ModTime().UnixNano(),
	}, true
}

func statSQLiteWAL(path string) fileSignature {
	if filepath.Ext(path) != ".db" {
		return fileSignature{}
	}
	signature, _ := statFile(path + "-wal")
	return signature
}

func contributionFromRecords(records []model.Record) sourceContribution {
	contribution := sourceContribution{
		aggregates: make(map[model.SeriesKey]model.Aggregate),
		sessions:   make(map[string]struct{}),
	}
	for _, record := range records {
		record.Model = model.NormalizeModel(record.Model)
		if record.SessionID != "" {
			contribution.sessions[record.SessionID] = struct{}{}
		}
		key := model.SeriesKey{
			Tool:  record.Tool,
			Model: record.Model,
		}
		aggregate := contribution.aggregates[key]
		addRecord(&aggregate, record)
		contribution.aggregates[key] = aggregate
	}
	return contribution
}

func mergeContribution(
	aggregates map[model.SeriesKey]model.Aggregate,
	sessions map[string]struct{},
	contribution sourceContribution,
) {
	for sessionID := range contribution.sessions {
		sessions[sessionID] = struct{}{}
	}
	for key, sourceAggregate := range contribution.aggregates {
		aggregate := aggregates[key]
		addAggregate(&aggregate, sourceAggregate)
		aggregates[key] = aggregate
	}
}

func addRecord(aggregate *model.Aggregate, record model.Record) {
	if aggregate.Messages == nil {
		aggregate.Messages = make(map[string]uint64)
	}
	if record.Role != "" {
		aggregate.Messages[record.Role]++
	}
	aggregate.Tokens.Input += record.Tokens.Input
	aggregate.Tokens.Output += record.Tokens.Output
	aggregate.Tokens.Reasoning += record.Tokens.Reasoning
	aggregate.Tokens.CacheCreation += record.Tokens.CacheCreation
	aggregate.Tokens.CacheRead += record.Tokens.CacheRead
	aggregate.Tokens.Cached += record.Tokens.Cached
	aggregate.ToolCalls += record.ToolCalls
}

func addAggregate(aggregate *model.Aggregate, source model.Aggregate) {
	if aggregate.Messages == nil {
		aggregate.Messages = make(map[string]uint64)
	}
	for role, count := range source.Messages {
		aggregate.Messages[role] += count
	}
	aggregate.Tokens.Input += source.Tokens.Input
	aggregate.Tokens.Output += source.Tokens.Output
	aggregate.Tokens.Reasoning += source.Tokens.Reasoning
	aggregate.Tokens.CacheCreation += source.Tokens.CacheCreation
	aggregate.Tokens.CacheRead += source.Tokens.CacheRead
	aggregate.Tokens.Cached += source.Tokens.Cached
	aggregate.ToolCalls += source.ToolCalls
}

func (s *Scanner) Snapshot() model.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneSnapshot(s.snapshot)
}

func (s *Scanner) lastSuccessfulScan() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.LastSuccessfulScan
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
