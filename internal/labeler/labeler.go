// Package labeler provides the core logic for labeling JIRA tickets
package labeler

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/drpaneas/ai-labeler/internal/config"
	"github.com/drpaneas/ai-labeler/internal/jira"
)

// JIRAClient defines the interface for JIRA operations needed by the labeler.
type JIRAClient interface {
	GetIssue(ctx context.Context, issueKey string) (*jira.Issue, error)
	UpdateIssueLabels(ctx context.Context, issueKey string, labels []string) error
}

// LLMProvider defines the interface for LLM operations needed by the labeler.
type LLMProvider interface {
	AnalyzeTicket(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error)
}

type Labeler struct {
	config      *config.Config
	jiraClient  JIRAClient
	llmProvider LLMProvider
	logger      *slog.Logger
	dryRun      bool
}

// Result represents the result of processing a single ticket
type Result struct {
	Ticket      string
	Summary     string
	Success     bool
	Label       string
	Error       error
	Skipped     bool
	SkipReason  string
	ProcessTime time.Duration
}

// Stats represents overall processing statistics
type Stats struct {
	Total     int
	Processed int
	Labeled   int
	Skipped   int
	Failed    int
	StartTime time.Time
	EndTime   time.Time
}

// New creates a new Labeler instance
func New(cfg *config.Config, jiraClient JIRAClient, llmProvider LLMProvider, logger *slog.Logger, dryRun bool) *Labeler {
	return &Labeler{
		config:      cfg,
		jiraClient:  jiraClient,
		llmProvider: llmProvider,
		logger:      logger,
		dryRun:      dryRun,
	}
}

func (l *Labeler) ProcessTickets(ctx context.Context, projectKey string, startTicket, endTicket int, workers int) (*Stats, []Result, error) {
	tickets := make([]int, endTicket-startTicket+1)
	for i := range tickets {
		tickets[i] = startTicket + i
	}

	stats := &Stats{
		Total:     len(tickets),
		StartTime: time.Now(),
	}

	var results []Result
	if workers > 1 {
		results = l.processConcurrently(ctx, projectKey, tickets, workers, stats)
	} else {
		results = l.processSequentially(ctx, projectKey, tickets, stats)
	}

	stats.EndTime = time.Now()

	l.logger.Info("Processing completed",
		"total", stats.Total,
		"processed", stats.Processed,
		"labeled", stats.Labeled,
		"skipped", stats.Skipped,
		"failed", stats.Failed,
		"duration", stats.EndTime.Sub(stats.StartTime))

	return stats, results, nil
}

func (l *Labeler) processSequentially(ctx context.Context, projectKey string, tickets []int, stats *Stats) []Result {
	results := make([]Result, len(tickets))

	for i, ticketNum := range tickets {
		select {
		case <-ctx.Done():
			l.logger.Warn("Processing cancelled", "at_ticket", ticketNum)
			return results[:i]
		default:
		}

		result := l.processSingleTicket(ctx, projectKey, ticketNum)
		results[i] = result
		updateStats(stats, &result)
	}

	return results
}

func (l *Labeler) processConcurrently(ctx context.Context, projectKey string, tickets []int, workers int, stats *Stats) []Result {
	ticketChan := make(chan int)
	resultChan := make(chan Result, workers)

	var wg sync.WaitGroup
	for i := range workers {
		wg.Go(func() {
			l.worker(ctx, i, projectKey, ticketChan, resultChan)
		})
	}

	go func() {
		defer close(ticketChan)
		for _, ticket := range tickets {
			select {
			case ticketChan <- ticket:
			case <-ctx.Done():
				return
			}
		}
	}()

	go func() {
		wg.Wait()
		close(resultChan)
	}()

	results := make([]Result, 0, len(tickets))
	for result := range resultChan {
		results = append(results, result)
		updateStats(stats, &result)
	}

	return results
}

func (l *Labeler) worker(ctx context.Context, workerID int, projectKey string, tickets <-chan int, results chan<- Result) {
	l.logger.Debug("Worker started", "worker_id", workerID)

	for ticketNum := range tickets {
		select {
		case <-ctx.Done():
			l.logger.Debug("Worker cancelled", "worker_id", workerID)
			return
		default:
		}

		result := l.processSingleTicket(ctx, projectKey, ticketNum)
		results <- result
	}

	l.logger.Debug("Worker finished", "worker_id", workerID)
}

func (l *Labeler) processSingleTicket(ctx context.Context, projectKey string, ticketNum int) (result Result) {
	start := time.Now()
	ticketKey := fmt.Sprintf("%s-%d", projectKey, ticketNum)

	result = Result{
		Ticket: ticketKey,
	}
	defer func() { result.ProcessTime = time.Since(start) }()

	l.logger.Info("Processing ticket", "ticket", ticketKey)

	issue, err := l.jiraClient.GetIssue(ctx, ticketKey)
	if err != nil {
		result.Error = fmt.Errorf("fetching issue: %w", err)
		l.logger.Error("Failed to fetch issue", "ticket", ticketKey, "error", err)
		return result
	}

	result.Summary = issue.Fields.Summary
	description := jira.ExtractDescription(issue.Fields.Description)
	currentLabels := issue.Fields.Labels

	needsLabel, reason := l.shouldAddLabel(currentLabels)
	if !needsLabel {
		result.Skipped = true
		result.SkipReason = reason
		result.Success = true
		l.logger.Info("Skipping ticket", "ticket", ticketKey, "reason", reason)
		return result
	}

	labelInfo := l.config.BuildLabelInfo()
	validLabels := l.config.ValidLabels()

	suggestedLabel, err := l.llmProvider.AnalyzeTicket(ctx, result.Summary, description, labelInfo, validLabels)
	if err != nil {
		result.Error = fmt.Errorf("analyzing ticket: %w", err)
		l.logger.Error("Failed to analyze ticket", "ticket", ticketKey, "error", err)
		return result
	}

	if suggestedLabel == "" {
		result.Error = fmt.Errorf("no label suggested by LLM")
		l.logger.Warn("No label suggested", "ticket", ticketKey)
		return result
	}

	result.Label = suggestedLabel
	l.logger.Info("Label suggested", "ticket", ticketKey, "label", suggestedLabel)

	if !l.dryRun {
		newLabels := slices.Concat(currentLabels, []string{suggestedLabel})

		err = l.jiraClient.UpdateIssueLabels(ctx, ticketKey, newLabels)
		if err != nil {
			result.Error = fmt.Errorf("updating labels: %w", err)
			l.logger.Error("Failed to update labels", "ticket", ticketKey, "error", err)
			return result
		}

		l.logger.Info("Label applied", "ticket", ticketKey, "label", suggestedLabel)
	} else {
		l.logger.Info("Dry run: would apply label", "ticket", ticketKey, "label", suggestedLabel)
	}

	result.Success = true
	return result
}

func (l *Labeler) shouldAddLabel(currentLabels []string) (bool, string) {
	if len(currentLabels) == 0 {
		return true, ""
	}

	for _, currentLabel := range currentLabels {
		if _, exists := l.config.LabelByName(currentLabel); exists {
			return false, fmt.Sprintf("already has configured label: %s", currentLabel)
		}
	}

	return true, ""
}

// updateStats must be called from a single goroutine; it is not safe for concurrent use.
func updateStats(stats *Stats, result *Result) {
	stats.Processed++

	if result.Skipped {
		stats.Skipped++
	} else if result.Error != nil {
		stats.Failed++
	} else if result.Success && result.Label != "" {
		stats.Labeled++
	}
}
