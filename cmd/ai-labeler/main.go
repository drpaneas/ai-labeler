// Package main provides the CLI entry point for the AI labeler
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/drpaneas/ai-labeler/internal/config"
	"github.com/drpaneas/ai-labeler/internal/jira"
	"github.com/drpaneas/ai-labeler/internal/labeler"
	"github.com/drpaneas/ai-labeler/internal/llm"
	"github.com/drpaneas/ai-labeler/internal/retry"
)

var (
	version   = "dev"
	buildTime = "unknown"
	gitCommit = "unknown"
)

func main() {
	opts, err := parseFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		flag.Usage()
		os.Exit(1)
	}

	logger := setupLogger(opts.verbose, opts.jsonLog)

	if opts.version {
		fmt.Printf("ai-labeler version %s (built %s, commit %s)\n", version, buildTime, gitCommit)
		os.Exit(0)
	}

	cfg, err := loadAndValidateConfig(opts, logger)
	if err != nil {
		logger.Error("Configuration error", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	setupSignalHandler(cancel, logger)

	if err := run(ctx, cfg, opts, logger); err != nil {
		logger.Error("Execution failed", "error", err)
		os.Exit(1)
	}
}

type options struct {
	configPath  string
	projectKey  string
	ticket      int
	startTicket int
	endTicket   int
	dryRun      bool
	workers     int
	verbose     bool
	jsonLog     bool
	version     bool
}

func parseFlags() (*options, error) {
	opts := &options{}

	flag.StringVar(&opts.configPath, "config", "", "Path to config file (default: config.json or CONFIG_FILE env var)")
	flag.StringVar(&opts.projectKey, "project", "", "JIRA project key (overrides config and env var)")
	flag.IntVar(&opts.ticket, "ticket", 0, "Single ticket number (alternative to start/end range)")
	flag.IntVar(&opts.startTicket, "start", 0, "Starting ticket number")
	flag.IntVar(&opts.endTicket, "end", 0, "Ending ticket number")
	flag.BoolVar(&opts.dryRun, "dry-run", false, "Preview changes without applying them")
	flag.IntVar(&opts.workers, "workers", 1, "Number of concurrent workers (default: 1)")
	flag.BoolVar(&opts.verbose, "verbose", false, "Enable verbose logging")
	flag.BoolVar(&opts.jsonLog, "json-log", false, "Output logs in JSON format")
	flag.BoolVar(&opts.version, "version", false, "Show version information")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "AI Labeler - Automatically label JIRA tickets using AI analysis\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Process a single ticket\n")
		fmt.Fprintf(os.Stderr, "  %s --ticket 105\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Process a range of tickets\n")
		fmt.Fprintf(os.Stderr, "  %s --start 100 --end 200\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  # Dry run with concurrent processing\n")
		fmt.Fprintf(os.Stderr, "  %s --start 100 --end 200 --dry-run --workers 5\n\n", os.Args[0])
	}

	flag.Parse()

	if opts.version {
		return opts, nil
	}

	if opts.ticket > 0 {
		opts.startTicket = opts.ticket
		opts.endTicket = opts.ticket
	} else if opts.startTicket == 0 && opts.endTicket == 0 {
		return nil, fmt.Errorf("must specify either --ticket <number> or --start <number> --end <number>")
	} else if opts.startTicket == 0 || opts.endTicket == 0 {
		return nil, fmt.Errorf("both --start and --end are required when using range")
	}

	if opts.startTicket > opts.endTicket {
		return nil, fmt.Errorf("start ticket (%d) cannot be greater than end ticket (%d)", opts.startTicket, opts.endTicket)
	}

	if opts.workers < 1 {
		opts.workers = 1
	} else if opts.workers > 20 {
		fmt.Fprintf(os.Stderr, "Warning: limiting workers to 20 (requested %d)\n", opts.workers)
		opts.workers = 20
	}

	return opts, nil
}

func setupLogger(verbose bool, jsonFormat bool) *slog.Logger {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: level,
		AddSource: verbose,
	}

	var handler slog.Handler
	if jsonFormat {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func loadAndValidateConfig(opts *options, logger *slog.Logger) (*config.Config, error) {
	cfg, err := config.LoadConfig(opts.configPath)
	if err != nil {
		return nil, err
	}

	cfg.ApplyEnvOverrides(logger)

	if opts.projectKey != "" {
		if opts.projectKey != cfg.JIRA.Project {
			logger.Info("JIRA project override detected",
				"config_value", cfg.JIRA.Project,
				"cli_value", opts.projectKey,
				"source", "command line")
		}
		cfg.JIRA.Project = opts.projectKey
	}

	return cfg, nil
}

func setupSignalHandler(cancel context.CancelFunc, logger *slog.Logger) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down gracefully", "signal", sig)
		cancel()
	}()
}

func run(ctx context.Context, cfg *config.Config, opts *options, logger *slog.Logger) error {
	logger.Info("Starting AI Labeler",
		"version", version,
		"project", cfg.JIRA.Project,
		"llm_provider", cfg.LLM.Provider,
		"ticket_range", fmt.Sprintf("%d-%d", opts.startTicket, opts.endTicket),
		"dry_run", opts.dryRun,
		"workers", opts.workers)

	retryConfig := &retry.Config{
		MaxRetries:     3,
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     10 * time.Second,
		Multiplier:     2.0,
	}

	jiraAuth, err := createJIRAAuth()
	if err != nil {
		return fmt.Errorf("JIRA authentication: %w", err)
	}

	jiraClient, err := jira.NewClient(cfg.JIRA.URL, jiraAuth,
		jira.WithLogger(logger),
		jira.WithRetryConfig(retryConfig),
	)
	if err != nil {
		return fmt.Errorf("creating JIRA client: %w", err)
	}

	llmAPIKey, err := lookupLLMAPIKey(cfg.LLM.Provider)
	if err != nil {
		return err
	}

	llmProvider, err := llm.NewProvider(ctx, cfg.LLM.Provider, cfg.LLM.Model, llmAPIKey, logger)
	if err != nil {
		return fmt.Errorf("creating LLM provider: %w", err)
	}

	logger.Info("Connected to services",
		"jira_url", cfg.JIRA.URL,
		"llm_provider", llmProvider.GetProviderName())

	l := labeler.New(cfg, jiraClient, llmProvider, logger, opts.dryRun)

	stats, results, err := l.ProcessTickets(ctx, cfg.JIRA.Project, opts.startTicket, opts.endTicket, opts.workers)
	if err != nil {
		return fmt.Errorf("processing tickets: %w", err)
	}

	printSummary(stats, results, opts.dryRun)

	if stats.Failed > 0 {
		return fmt.Errorf("%d tickets failed to process", stats.Failed)
	}

	return nil
}

func printSummary(stats *labeler.Stats, results []labeler.Result, dryRun bool) {
	fmt.Fprintln(os.Stderr, "\n=== Processing Summary ===")
	fmt.Fprintf(os.Stderr, "Total tickets:     %d\n", stats.Total)
	fmt.Fprintf(os.Stderr, "Processed:         %d\n", stats.Processed)
	fmt.Fprintf(os.Stderr, "Labels applied:    %d\n", stats.Labeled)
	fmt.Fprintf(os.Stderr, "Skipped:           %d\n", stats.Skipped)
	fmt.Fprintf(os.Stderr, "Failed:            %d\n", stats.Failed)
	fmt.Fprintf(os.Stderr, "Duration:          %s\n", stats.EndTime.Sub(stats.StartTime).Round(time.Millisecond))

	if stats.Processed > 0 {
		avgTime := stats.EndTime.Sub(stats.StartTime) / time.Duration(stats.Processed)
		fmt.Fprintf(os.Stderr, "Avg time/ticket:   %s\n", avgTime.Round(time.Millisecond))
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "\nDRY RUN MODE - No changes were applied")
	}

	if stats.Failed > 0 {
		fmt.Fprintln(os.Stderr, "\n=== Failed Tickets ===")
		for _, result := range results {
			if result.Error != nil {
				fmt.Fprintf(os.Stderr, "- %s: %v\n", result.Ticket, result.Error)
			}
		}
	}

	if stats.Labeled > 0 && dryRun {
		fmt.Fprintln(os.Stderr, "\n=== Would Apply Labels ===")
		for _, result := range results {
			if result.Success && result.Label != "" {
				fmt.Fprintf(os.Stderr, "- %s: %s\n", result.Ticket, result.Label)
			}
		}
	}
}

func createJIRAAuth() (jira.Authenticator, error) {
	token := os.Getenv("JIRA_API_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("JIRA_API_TOKEN environment variable not set")
	}

	if email := os.Getenv("JIRA_EMAIL"); email != "" {
		return jira.NewBasicAuth(email, token), nil
	}

	if strings.Contains(token, ":") {
		parts := strings.SplitN(token, ":", 2)
		if len(parts) == 2 {
			return jira.NewBasicAuth(parts[0], parts[1]), nil
		}
	}

	return jira.NewBearerAuth(token), nil
}

func lookupLLMAPIKey(provider string) (string, error) {
	envVar, err := llm.EnvVarForProvider(provider)
	if err != nil {
		return "", err
	}

	key := os.Getenv(envVar)
	if key == "" {
		return "", fmt.Errorf("%s environment variable not set (required for %s)", envVar, provider)
	}
	return key, nil
}
