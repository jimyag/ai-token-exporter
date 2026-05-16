package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/jimmicro/version"
	"github.com/jimyag/ai-token-exporter/internal/analyzer"
	"github.com/jimyag/ai-token-exporter/internal/analyzer/claude"
	"github.com/jimyag/ai-token-exporter/internal/analyzer/codex"
	"github.com/jimyag/ai-token-exporter/internal/analyzer/copilotcli"
	"github.com/jimyag/ai-token-exporter/internal/analyzer/copilotvscode"
	"github.com/jimyag/ai-token-exporter/internal/analyzer/gemini"
	"github.com/jimyag/ai-token-exporter/internal/config"
	"github.com/jimyag/ai-token-exporter/internal/model"
	"github.com/jimyag/ai-token-exporter/internal/scanner"
	"github.com/jimyag/ai-token-exporter/internal/server"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}
	cfg.Version = version
	cfg.Commit = commit

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scn := scanner.New(buildAnalyzers(cfg), cfg.Version, cfg.Commit)
	go scn.Run(ctx, cfg.ScanInterval)

	srv := server.New(cfg.Listen, scn)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("ai-token-exporter listening on %s", cfg.Listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func buildAnalyzers(cfg config.Config) []analyzer.Analyzer {
	var analyzers []analyzer.Analyzer
	if cfg.Enabled[model.ToolClaudeCode] {
		analyzers = append(analyzers, claude.New(cfg.ClaudeDir))
	}
	if cfg.Enabled[model.ToolCodexCLI] {
		analyzers = append(analyzers, codex.New(cfg.CodexDir))
	}
	if cfg.Enabled[model.ToolCopilotCLI] {
		analyzers = append(analyzers, copilotcli.New(cfg.CopilotDir))
	}
	if cfg.Enabled[model.ToolGitHubCopilot] {
		analyzers = append(analyzers, copilotvscode.New(cfg.VSCodeConfigDir))
	}
	if cfg.Enabled[model.ToolGeminiCLI] {
		analyzers = append(analyzers, gemini.New(cfg.GeminiDir, cfg.GeminiConfigDir))
	}
	return analyzers
}
