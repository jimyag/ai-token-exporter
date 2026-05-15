# ai-token-exporter Design

## Goal

`ai-token-exporter` is a local Prometheus exporter that reads token usage from AI coding tool session logs and exposes the current parsed snapshot through `GET /metrics`.

Supported tools for v1:

- Claude Code
- Codex CLI
- Gemini CLI
- GitHub Copilot CLI
- GitHub Copilot Chat sessions from VS Code-compatible editors

The exporter does not calculate cost in v1. Model pricing changes often and should be handled later through explicit external configuration if needed.

## Metrics

All usage metrics are gauges. The source of truth is local log files, so values can decrease when files are removed, sessions are compacted, parser behavior changes, or de-duplication corrects earlier data.

```prometheus
ai_token_exporter_tokens{tool,model,token_type}
ai_token_exporter_messages{tool,model,role}
ai_token_exporter_tool_calls{tool,model}
ai_token_exporter_sessions{tool}
ai_token_exporter_source_files{tool}
ai_token_exporter_source_parse_errors{tool}
ai_token_exporter_last_scan_timestamp_seconds
ai_token_exporter_last_successful_scan_timestamp_seconds
ai_token_exporter_scan_duration_seconds
ai_token_exporter_last_scan_success
ai_token_exporter_build_info{version,commit}
```

Label values:

- `tool`: `claude_code`, `codex_cli`, `copilot_cli`, `github_copilot`, `gemini_cli`
- `token_type`: `input`, `output`, `reasoning`, `cache_creation`, `cache_read`, `cached`
- `role`: `user`, `assistant`
- `model`: normalized model name, falling back through tool defaults before `unknown`

Session and project identifiers are intentionally not exposed as metric labels. The exporter aggregates those locally into `tool` and `model` series to keep Prometheus cardinality low and avoid leaking local activity shape.

Metric semantics:

- `ai_token_exporter_tokens`: total tokens in the current parsed snapshot for the label set.
- `ai_token_exporter_messages`: total messages in the current parsed snapshot for the label set.
- `ai_token_exporter_tool_calls`: total tool calls in the current parsed snapshot for the label set.
- `ai_token_exporter_sessions`: number of distinct sessions discovered for a tool in the latest scan.
- `ai_token_exporter_source_files`: number of source files discovered for a tool in the latest scan.
- `ai_token_exporter_source_parse_errors`: number of source files that failed to parse in the latest scan.
- `ai_token_exporter_last_scan_timestamp_seconds`: Unix timestamp when the latest scan finished.
- `ai_token_exporter_last_successful_scan_timestamp_seconds`: Unix timestamp when the latest successful scan finished.
- `ai_token_exporter_scan_duration_seconds`: duration of the latest scan.
- `ai_token_exporter_last_scan_success`: `1` when the latest scan completed without fatal scanner errors, otherwise `0`.
- `ai_token_exporter_build_info`: fixed value `1`, with version and commit labels.

## Model Resolution

Parser-provided model metadata has highest priority. If a message or token event does not include a model, the analyzer should fall back to the tool's default model configuration before using `unknown`.

Resolution order:

1. Model on the token usage event or assistant message.
2. Model from session metadata or turn context.
3. Tool default configuration file.
4. Built-in conservative default for that tool, if the tool has a stable default.
5. `unknown`.

Tool-specific defaults:

- Claude Code: read known Claude settings files when available, then fall back to `unknown`.
- Codex CLI: read Codex config from the user's Codex home when available; if no configured model is found, use the current Codex CLI fallback default used by the analyzer.
- Gemini CLI: read model from each Gemini message first, then fall back to `~/.gemini/settings.json`, then `unknown`.
- Copilot CLI: read model from session start/context events first, then any Copilot CLI config if available, then `unknown`.
- GitHub Copilot Chat: read `modelId` from each request first; if missing, use any session/editor configuration discovered locally, then `unknown`.

Default model lookup must be best-effort. Failure to read or parse a config file should not fail the scan.

## Data Sources

Default source locations:

- Claude Code: `~/.claude/projects/*/*.jsonl`
- Codex CLI: `~/.codex/sessions/**/*.jsonl`
- Gemini CLI: `~/.gemini/tmp/**/chats/*.{json,jsonl}`
- Copilot CLI: `~/.copilot/session-state/**/*.jsonl`, `~/.copilot/history-session-state/**/*.jsonl`
- GitHub Copilot Chat: `~/Library/Application Support/{Code,Code - Insiders,Cursor,Windsurf,VSCodium,Positron,Antigravity}/User/workspaceStorage/*/chatSessions/*.json`

Config overrides:

```text
AI_TOKEN_EXPORTER_LISTEN
AI_TOKEN_EXPORTER_SCAN_INTERVAL
AI_TOKEN_EXPORTER_ENABLED
AI_TOKEN_EXPORTER_CLAUDE_DIR
AI_TOKEN_EXPORTER_CODEX_DIR
AI_TOKEN_EXPORTER_COPILOT_DIR
AI_TOKEN_EXPORTER_GEMINI_DIR
```

Default CLI:

```bash
ai-token-exporter \
  --listen=:9108 \
  --scan-interval=30s \
  --enabled=claude_code,codex_cli,copilot_cli,github_copilot,gemini_cli
```

## Architecture

Recommended Go package layout:

```text
cmd/ai-token-exporter/main.go
internal/config/
internal/server/
internal/metrics/
internal/scanner/
internal/analyzer/
internal/analyzer/claude/
internal/analyzer/codex/
internal/analyzer/gemini/
internal/analyzer/copilotcli/
internal/analyzer/copilotvscode/
internal/model/
internal/hash/
internal/testutil/
```

Core flow:

1. Load config from flags and environment variables.
2. Build enabled analyzers.
3. Scanner runs immediately on startup, then every `scan-interval`.
4. Each analyzer discovers source files and parses them independently.
5. Per-file parse failures are counted and do not block other files.
6. Parsed records are aggregated into an immutable snapshot.
7. `/metrics` renders only the latest snapshot, without scanning or reading source files during scrape.

Analyzer interface:

```go
type Analyzer interface {
    Name() string
    Discover(ctx context.Context) ([]Source, error)
    Parse(ctx context.Context, source Source) ([]Record, error)
    ValidPath(path string) bool
}
```

Record shape:

```go
type Record struct {
    Tool         string
    Model        string
    SessionID    string
    Role         string
    InputTokens  uint64
    OutputTokens uint64
    Reasoning    uint64
    CacheCreate  uint64
    CacheRead    uint64
    Cached       uint64
    ToolCalls    uint64
}
```

The scanner should aggregate exported metrics by `tool` and `model`. It may retain session hashes internally only to compute `ai_token_exporter_sessions{tool}`.

## Parser Notes

Claude Code:

- Read `message.usage.input_tokens`, `output_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`.
- Set `cached = cache_creation + cache_read`.
- Model comes from `message.model`.
- Project ID comes from the project directory under `~/.claude/projects`.
- Session ID comes from the conversation file path hash.
- De-duplicate by `request_id + message.id`; when unavailable, fall back to file path plus UUID.

Codex CLI:

- Parse wrapper JSONL entries: `session_meta`, `turn_context`, `response_item`, and `event_msg`.
- Token usage comes from `event_msg` entries where event type is `token_count`.
- Prefer `last_token_usage`; otherwise calculate delta from `total_token_usage`.
- Set `input = input_tokens - cached_input_tokens`.
- Set `cached = cached_input_tokens`.
- Set `reasoning = reasoning_output_tokens`.
- Extract model from token event, turn context, or metadata before falling back to config/defaults.

Gemini CLI:

- Parse `~/.gemini/tmp/**/chats/*.{json,jsonl}`.
- For JSON files, parse the top-level `messages` list.
- For JSONL files, ignore `$set` updates and valid non-message lines, keep the latest message per `id`, and preserve first-seen order.
- Count user messages with role `user`.
- Count Gemini assistant messages with role `assistant`.
- Extract model from the Gemini message `model` field, then fall back to `~/.gemini/settings.json`.
- Map `tokens.input` to input, `tokens.output` to output, `tokens.thoughts` to reasoning, and `tokens.cached` to cached.
- Count tool calls from `toolCalls`.

Copilot CLI:

- Parse `session.start`, `session.model_change`, `user.message`, `assistant.*`, `tool.execution_*`, and shutdown metrics.
- Prefer shutdown `modelMetrics.usage` when present.
- Otherwise estimate tokens from visible text and tool inputs/outputs.
- Count tool calls from `tool.execution_start`.

GitHub Copilot Chat:

- Parse `chatSessions/*.json`.
- Estimate input tokens from user messages plus tool result text.
- Estimate output tokens from assistant responses and tool call rounds.
- Extract model from request `modelId`.
- Treat token counts as estimates, not provider billing truth.

## Testing

Required tests:

- Claude parser handles usage/cache fields and de-duplication.
- Codex parser handles `last_token_usage` and `total_token_usage` deltas.
- Copilot CLI parser prefers shutdown metrics when available.
- Gemini CLI parser handles JSON sessions, JSONL latest-message updates, token fields, and tool calls.
- GitHub Copilot parser estimates tokens and normalizes `modelId`.
- Aggregation handles multiple sessions and models.
- Missing model falls back through default config and finally `unknown`.
- Per-file parse errors increment scan error metrics without blocking other files.
- `/metrics` returns Prometheus text format before and after the first successful scan.

Verification commands:

```bash
go test ./...
go run ./cmd/ai-token-exporter --listen=:9108
curl localhost:9108/metrics
```
