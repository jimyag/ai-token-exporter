# ai-token-exporter

`ai-token-exporter` exposes local AI coding tool token usage as Prometheus metrics.

It reads session logs from:

- Claude Code
- Codex CLI
- Gemini CLI
- GitHub Copilot CLI
- GitHub Copilot Chat sessions from VS Code-compatible editors

The exporter keeps an in-memory snapshot and serves it at `GET /metrics`. Scrapes do not read session files from disk.

## Install

Build from source:

```bash
go install github.com/jimyag/ai-token-exporter/cmd/ai-token-exporter@latest
```

Run from the repository:

```bash
go run ./cmd/ai-token-exporter
```

Docker:

```bash
docker run --rm -p 9108:9108 \
  -v "$HOME/.claude:/home/nonroot/.claude:ro" \
  -v "$HOME/.codex:/home/nonroot/.codex:ro" \
  -v "$HOME/.gemini:/home/nonroot/.gemini:ro" \
  -v "$HOME/.copilot:/home/nonroot/.copilot:ro" \
  -v "$HOME/Library/Application Support:/home/nonroot/.config:ro" \
  ghcr.io/jimyag/ai-token-exporter:latest
```

## Usage

```bash
ai-token-exporter \
  --listen=:9108 \
  --scan-interval=30s \
  --enabled=claude_code,codex_cli,copilot_cli,github_copilot,gemini_cli
```

Check metrics:

```bash
curl localhost:9108/metrics
```

Show version metadata:

```bash
ai-token-exporter --version
```

`--version` is provided by `github.com/jimmicro/version`. Release builds inject the git tag and build time through GoReleaser ldflags.

## Configuration

Flags can be set with environment variables:

| Flag | Environment variable | Default |
| --- | --- | --- |
| `--listen` | `AI_TOKEN_EXPORTER_LISTEN` | `:9108` |
| `--scan-interval` | `AI_TOKEN_EXPORTER_SCAN_INTERVAL` | `30s` |
| `--enabled` | `AI_TOKEN_EXPORTER_ENABLED` | `claude_code,codex_cli,copilot_cli,github_copilot,gemini_cli` |
| `--claude-dir` | `AI_TOKEN_EXPORTER_CLAUDE_DIR` | `~/.claude/projects` |
| `--codex-dir` | `AI_TOKEN_EXPORTER_CODEX_DIR` | `~/.codex` |
| `--copilot-dir` | `AI_TOKEN_EXPORTER_COPILOT_DIR` | `~/.copilot` |
| `--gemini-dir` | `AI_TOKEN_EXPORTER_GEMINI_DIR` | `~/.gemini/tmp` |

Default source locations:

- Claude Code: `~/.claude/projects/*/*.jsonl`
- Codex CLI: `~/.codex/sessions/**/*.jsonl`
- Gemini CLI: `~/.gemini/tmp/**/chats/*.{json,jsonl}`
- Copilot CLI: `~/.copilot/session-state/**/*.jsonl`, `~/.copilot/history-session-state/**/*.jsonl`
- GitHub Copilot Chat: `{user config dir}/{Code,Code - Insiders,Cursor,Windsurf,VSCodium,Positron,Antigravity}/User/workspaceStorage/*/chatSessions/*.json`

## Metrics

All usage metrics are gauges because local log snapshots can decrease when files are removed, compacted, or de-duplicated differently.

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

`input` is gross input, including cached input when the source reports it. `cached` is cache hit/reused input and does not include cache creation.

Session and project identifiers are intentionally not exposed as labels. The exporter aggregates those locally into `tool` and `model` series to keep Prometheus cardinality low.

## Prometheus

Example scrape config:

```yaml
scrape_configs:
  - job_name: ai-token-exporter
    static_configs:
      - targets: ["localhost:9108"]
```

## Release

Releases are published when pushing a tag that starts with `v`.

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow uses GoReleaser to publish:

- Linux, macOS, and Windows binaries for `amd64` and `arm64`
- GitHub release archives
- Multi-arch container image: `ghcr.io/jimyag/ai-token-exporter:<tag>` and `latest`

Local release dry-run:

```bash
goreleaser release --snapshot --clean
```

## Development

```bash
go test ./...
go vet ./...
```

With Task:

```bash
task deps
task lint
task test
task build
task run -- --listen=:9108
task docker-build
task release-snapshot
```
