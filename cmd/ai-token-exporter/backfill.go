package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jimyag/ai-token-exporter/internal/backfill"
	"github.com/jimyag/ai-token-exporter/internal/config"
)

func runBackfill(args []string, version, commit string) error {
	cfg := config.Default()
	cfg.Version = version
	cfg.Commit = commit

	hostname, _ := os.Hostname()
	options := backfill.Options{
		Step:     time.Minute,
		Job:      "ai-token-exporter",
		Instance: hostname + ":backfill",
		Hostname: hostname,
		Version:  version,
		Commit:   commit,
	}

	var enabled string
	var from, to string
	fs := flag.NewFlagSet("ai-token-exporter backfill", flag.ContinueOnError)
	config.BindFlags(fs, &cfg, &enabled)
	fs.StringVar(&from, "from", "", "start time in RFC3339 format; defaults to first record time")
	fs.StringVar(&to, "to", "", "end time in RFC3339 format; defaults to last record time")
	fs.DurationVar(&options.Step, "step", options.Step, "historical sample interval")
	fs.StringVar(&options.Job, "job", options.Job, "job label to write")
	fs.StringVar(&options.Instance, "instance", options.Instance, "instance label to write")
	fs.StringVar(&options.Hostname, "hostname", options.Hostname, "hostname label to write")
	fs.StringVar(&options.VMURL, "vm-url", "", "VictoriaMetrics /api/v1/import/prometheus URL; writes to stdout when empty")
	fs.BoolVar(&options.ReplaceExisting, "replace-existing", false, "delete existing ai-token-exporter series for the same job/instance/hostname before import")
	fs.StringVar(&options.DeleteURL, "delete-url", "", "VictoriaMetrics delete_series URL; inferred from --vm-url for single-node VictoriaMetrics")
	if err := fs.Parse(args); err != nil {
		return err
	}
	config.ApplyEnabled(&cfg, enabled)
	if options.ReplaceExisting && options.VMURL == "" {
		return fmt.Errorf("--replace-existing requires --vm-url")
	}

	var err error
	if options.From, err = parseOptionalTime(from); err != nil {
		return fmt.Errorf("invalid --from: %w", err)
	}
	if options.To, err = parseOptionalTime(to); err != nil {
		return fmt.Errorf("invalid --to: %w", err)
	}

	result, err := backfill.Run(context.Background(), buildAnalyzers(cfg), options, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "backfill complete: source_files=%d parse_errors=%d records=%d samples=%d\n", result.SourceFiles, result.ParseErrors, result.Records, result.Samples)
	return nil
}

func parseOptionalTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("expected RFC3339, RFC3339Nano, YYYY-MM-DD HH:MM:SS, or YYYY-MM-DD")
}
