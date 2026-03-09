package labeler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/drpaneas/ai-labeler/internal/config"
	"github.com/drpaneas/ai-labeler/internal/jira"
)

// MockJIRAClient for testing
type MockJIRAClient struct {
	GetIssueFunc          func(ctx context.Context, issueKey string) (*jira.Issue, error)
	UpdateIssueLabelsFunc func(ctx context.Context, issueKey string, labels []string) error
}

func (m *MockJIRAClient) GetIssue(ctx context.Context, issueKey string) (*jira.Issue, error) {
	if m.GetIssueFunc != nil {
		return m.GetIssueFunc(ctx, issueKey)
	}
	return nil, nil
}

func (m *MockJIRAClient) UpdateIssueLabels(ctx context.Context, issueKey string, labels []string) error {
	if m.UpdateIssueLabelsFunc != nil {
		return m.UpdateIssueLabelsFunc(ctx, issueKey, labels)
	}
	return nil
}

type MockLLMProvider struct {
	AnalyzeFunc func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error)
}

func (m *MockLLMProvider) AnalyzeTicket(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
	if m.AnalyzeFunc != nil {
		return m.AnalyzeFunc(ctx, summary, description, labelInfo, validLabels)
	}
	return "", nil
}

func TestLabeler_ShouldAddLabel(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{
			{Name: "bug", Description: "Bug fixes"},
			{Name: "feature", Description: "New features"},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	l := New(cfg, nil, nil, logger, false)

	tests := []struct {
		name          string
		currentLabels []string
		wantAddLabel  bool
		wantReason    string
	}{
		{
			name:          "no labels",
			currentLabels: []string{},
			wantAddLabel:  true,
			wantReason:    "",
		},
		{
			name:          "has configured label",
			currentLabels: []string{"bug"},
			wantAddLabel:  false,
			wantReason:    "already has configured label: bug",
		},
		{
			name:          "has different labels",
			currentLabels: []string{"urgent", "customer"},
			wantAddLabel:  true,
			wantReason:    "",
		},
		{
			name:          "mixed labels",
			currentLabels: []string{"urgent", "feature", "customer"},
			wantAddLabel:  false,
			wantReason:    "already has configured label: feature",
		},
		{
			name:          "case insensitive match",
			currentLabels: []string{"BUG"},
			wantAddLabel:  false,
			wantReason:    "already has configured label: BUG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAdd, gotReason := l.shouldAddLabel(tt.currentLabels)

			if gotAdd != tt.wantAddLabel {
				t.Errorf("shouldAddLabel() add = %v, want %v", gotAdd, tt.wantAddLabel)
			}
			if gotReason != tt.wantReason {
				t.Errorf("shouldAddLabel() reason = %v, want %v", gotReason, tt.wantReason)
			}
		})
	}
}

func TestLabeler_ProcessSingleTicket(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{
			{Name: "bug", Description: "Bug fixes"},
			{Name: "feature", Description: "New features"},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	tests := []struct {
		name         string
		ticketNum    int
		setupMocks   func() (*MockJIRAClient, *MockLLMProvider)
		applyChanges bool
		wantResult   Result
	}{
		{
			name:      "successful labeling",
			ticketNum: 123,
			setupMocks: func() (*MockJIRAClient, *MockLLMProvider) {
				jiraClient := &MockJIRAClient{
					GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
						issue := &jira.Issue{Key: issueKey}
						issue.Fields.Summary = "App crashes on startup"
						issue.Fields.Description = "Null pointer exception"
						issue.Fields.Labels = []string{}
						return issue, nil
					},
					UpdateIssueLabelsFunc: func(ctx context.Context, issueKey string, labels []string) error {
						if len(labels) != 1 || labels[0] != "bug" {
							return errors.New("unexpected labels")
						}
						return nil
					},
				}

				llmProvider := &MockLLMProvider{
					AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
						return "bug", nil
					},
				}

				return jiraClient, llmProvider
			},
			applyChanges: true,
			wantResult: Result{
				Ticket:  "TEST-123",
				Summary: "App crashes on startup",
				Success: true,
				Label:   "bug",
			},
		},
		{
			name:      "dry run mode",
			ticketNum: 123,
			setupMocks: func() (*MockJIRAClient, *MockLLMProvider) {
				jiraClient := &MockJIRAClient{
					GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
						issue := &jira.Issue{Key: issueKey}
						issue.Fields.Summary = "Add dark mode"
						issue.Fields.Description = "Users want dark mode"
						issue.Fields.Labels = []string{}
						return issue, nil
					},
					UpdateIssueLabelsFunc: func(ctx context.Context, issueKey string, labels []string) error {
						return errors.New("should not be called in dry run")
					},
				}

				llmProvider := &MockLLMProvider{
					AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
						return "feature", nil
					},
				}

				return jiraClient, llmProvider
			},
			applyChanges: false,
			wantResult: Result{
				Ticket:  "TEST-123",
				Summary: "Add dark mode",
				Success: true,
				Label:   "feature",
			},
		},
		{
			name:      "skip already labeled",
			ticketNum: 123,
			setupMocks: func() (*MockJIRAClient, *MockLLMProvider) {
				jiraClient := &MockJIRAClient{
					GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
						issue := &jira.Issue{Key: issueKey}
						issue.Fields.Summary = "Already labeled issue"
						issue.Fields.Labels = []string{"bug", "urgent"}
						return issue, nil
					},
				}

				llmProvider := &MockLLMProvider{
					AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
						return "", errors.New("should not be called for already labeled issues")
					},
				}

				return jiraClient, llmProvider
			},
			applyChanges: true,
			wantResult: Result{
				Ticket:     "TEST-123",
				Summary:    "Already labeled issue",
				Success:    true,
				Skipped:    true,
				SkipReason: "already has configured label: bug",
			},
		},
		{
			name:      "jira fetch error",
			ticketNum: 404,
			setupMocks: func() (*MockJIRAClient, *MockLLMProvider) {
				jiraClient := &MockJIRAClient{
					GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
						return nil, errors.New("issue not found")
					},
				}

				llmProvider := &MockLLMProvider{}

				return jiraClient, llmProvider
			},
			applyChanges: true,
			wantResult: Result{
				Ticket: "TEST-404",
				Error:  errors.New("fetching issue: issue not found"),
			},
		},
		{
			name:      "llm analysis error",
			ticketNum: 123,
			setupMocks: func() (*MockJIRAClient, *MockLLMProvider) {
				jiraClient := &MockJIRAClient{
					GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
						issue := &jira.Issue{Key: issueKey}
						issue.Fields.Summary = "Test issue"
						issue.Fields.Labels = []string{}
						return issue, nil
					},
				}

				llmProvider := &MockLLMProvider{
					AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
						return "", errors.New("LLM service unavailable")
					},
				}

				return jiraClient, llmProvider
			},
			applyChanges: true,
			wantResult: Result{
				Ticket:  "TEST-123",
				Summary: "Test issue",
				Error:   errors.New("analyzing ticket: LLM service unavailable"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jiraClient, llmProvider := tt.setupMocks()
			l := &Labeler{
				config:       cfg,
				jiraClient:   jiraClient,
				llmProvider:  llmProvider,
				logger:       logger,
				applyChanges: tt.applyChanges,
			}

			ctx := t.Context()
			result := l.processSingleTicket(ctx, "TEST", tt.ticketNum)

			// Check key fields
			if result.Ticket != tt.wantResult.Ticket {
				t.Errorf("processSingleTicket() ticket = %v, want %v", result.Ticket, tt.wantResult.Ticket)
			}
			if result.Success != tt.wantResult.Success {
				t.Errorf("processSingleTicket() success = %v, want %v", result.Success, tt.wantResult.Success)
			}
			if result.Label != tt.wantResult.Label {
				t.Errorf("processSingleTicket() label = %v, want %v", result.Label, tt.wantResult.Label)
			}
			if result.Skipped != tt.wantResult.Skipped {
				t.Errorf("processSingleTicket() skipped = %v, want %v", result.Skipped, tt.wantResult.Skipped)
			}
			if result.SkipReason != tt.wantResult.SkipReason {
				t.Errorf("processSingleTicket() skipReason = %v, want %v", result.SkipReason, tt.wantResult.SkipReason)
			}

			// Check error presence (not exact match due to wrapping)
			if tt.wantResult.Error != nil && result.Error == nil {
				t.Error("processSingleTicket() expected error but got none")
			}
			if tt.wantResult.Error == nil && result.Error != nil {
				t.Errorf("processSingleTicket() unexpected error = %v", result.Error)
			}

			// Check processing time is set
			if result.ProcessTime == 0 {
				t.Error("processSingleTicket() ProcessTime not set")
			}
		})
	}
}

func TestLabeler_ProcessTickets(t *testing.T) {
	ctx := t.Context()
	cfg := &config.Config{
		Labels: []config.LabelConfig{
			{Name: "bug", Description: "Bug fixes"},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var mu sync.Mutex
	var processedTickets []string

	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			mu.Lock()
			processedTickets = append(processedTickets, issueKey)
			mu.Unlock()
			issue := &jira.Issue{Key: issueKey}
			issue.Fields.Summary = fmt.Sprintf("Issue %s", issueKey)
			issue.Fields.Labels = []string{}
			return issue, nil
		},
		UpdateIssueLabelsFunc: func(ctx context.Context, issueKey string, labels []string) error {
			return nil
		},
	}

	llmProvider := &MockLLMProvider{
		AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
			return "bug", nil
		},
	}

	l := New(cfg, jiraClient, llmProvider, logger, true)

	tests := []struct {
		name        string
		start       int
		end         int
		workers     int
		wantTotal   int
		checkResult func(t *testing.T, stats *Stats, results []Result)
	}{
		{
			name:      "sequential processing",
			start:     1,
			end:       3,
			workers:   1,
			wantTotal: 3,
			checkResult: func(t *testing.T, stats *Stats, results []Result) {
				if stats.Processed != 3 {
					t.Errorf("Processed = %d, want 3", stats.Processed)
				}
				if stats.Labeled != 3 {
					t.Errorf("Labeled = %d, want 3", stats.Labeled)
				}
				if len(results) != 3 {
					t.Errorf("Results count = %d, want 3", len(results))
				}
			},
		},
		{
			name:      "concurrent processing",
			start:     1,
			end:       5,
			workers:   3,
			wantTotal: 5,
			checkResult: func(t *testing.T, stats *Stats, results []Result) {
				if stats.Processed != 5 {
					t.Errorf("Processed = %d, want 5", stats.Processed)
				}
				if stats.Labeled != 5 {
					t.Errorf("Labeled = %d, want 5", stats.Labeled)
				}
				// Check all tickets were processed
				ticketMap := make(map[string]bool)
				for _, r := range results {
					ticketMap[r.Ticket] = true
				}
				for i := 1; i <= 5; i++ {
					expectedTicket := fmt.Sprintf("TEST-%d", i)
					if !ticketMap[expectedTicket] {
						t.Errorf("Ticket %s not found in results", expectedTicket)
					}
				}
			},
		},
		{
			name:      "single ticket",
			start:     10,
			end:       10,
			workers:   1,
			wantTotal: 1,
			checkResult: func(t *testing.T, stats *Stats, results []Result) {
				if stats.Total != 1 {
					t.Errorf("Total = %d, want 1", stats.Total)
				}
				if results[0].Ticket != "TEST-10" {
					t.Errorf("Ticket = %s, want TEST-10", results[0].Ticket)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processedTickets = nil // Reset

			stats, results, err := l.ProcessTickets(ctx, "TEST", tt.start, tt.end, tt.workers)

			if err != nil {
				t.Errorf("ProcessTickets() unexpected error = %v", err)
			}

			if stats.Total != tt.wantTotal {
				t.Errorf("ProcessTickets() total = %d, want %d", stats.Total, tt.wantTotal)
			}

			// Check timing
			if stats.EndTime.Before(stats.StartTime) {
				t.Error("EndTime before StartTime")
			}

			tt.checkResult(t, stats, results)
		})
	}
}

func TestLabeler_ProcessTickets_Cancellation(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{
			{Name: "bug", Description: "Bug fixes"},
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var processCount atomic.Int32
	started := make(chan struct{}, 2)
	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			processCount.Add(1)
			select {
			case started <- struct{}{}:
			default:
			}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}

	llmProvider := &MockLLMProvider{
		AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
			return "bug", nil
		},
	}

	l := New(cfg, jiraClient, llmProvider, logger, false)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type processResult struct {
		stats   *Stats
		results []Result
		err     error
	}
	done := make(chan processResult, 1)
	go func() {
		stats, results, err := l.ProcessTickets(ctx, "TEST", 1, 10, 2)
		done <- processResult{stats: stats, results: results, err: err}
	}()

	for range 2 {
		<-started
	}
	cancel()

	outcome := <-done
	stats, results, err := outcome.stats, outcome.results, outcome.err

	if err != nil {
		t.Errorf("ProcessTickets() unexpected error = %v", err)
	}

	if stats.Processed != 2 {
		t.Errorf("ProcessTickets() processed = %d, want 2", stats.Processed)
	}
	pc := int(processCount.Load())
	if pc != 2 {
		t.Errorf("ProcessTickets() started = %d, want 2", pc)
	}
	if stats.Failed != 2 {
		t.Errorf("ProcessTickets() failed = %d, want 2", stats.Failed)
	}
	if len(results) != 2 {
		t.Errorf("ProcessTickets() results = %d, want 2", len(results))
	}
}

func TestLabeler_ProcessSingleTicket_EmptyLabel(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{{Name: "bug", Description: "d"}},
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			issue := &jira.Issue{Key: issueKey}
			issue.Fields.Summary = "s"
			issue.Fields.Labels = []string{}
			return issue, nil
		},
	}
	llmProvider := &MockLLMProvider{
		AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
			return "", nil
		},
	}

	l := New(cfg, jiraClient, llmProvider, logger, false)
	result := l.processSingleTicket(t.Context(), "TEST", 1)
	if result.Error == nil {
		t.Error("expected error for empty label")
	}
}

func TestLabeler_ProcessSingleTicket_UpdateError(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{{Name: "bug", Description: "d"}},
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			issue := &jira.Issue{Key: issueKey}
			issue.Fields.Summary = "s"
			issue.Fields.Labels = []string{}
			return issue, nil
		},
		UpdateIssueLabelsFunc: func(ctx context.Context, issueKey string, labels []string) error {
			return errors.New("permission denied")
		},
	}
	llmProvider := &MockLLMProvider{
		AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
			return "bug", nil
		},
	}

	l := New(cfg, jiraClient, llmProvider, logger, true)
	result := l.processSingleTicket(t.Context(), "TEST", 1)
	if result.Error == nil {
		t.Error("expected error when update fails")
	}
	if result.Success {
		t.Error("should not be success when update fails")
	}
}

func TestLabeler_ProcessSequentially_Cancellation(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{{Name: "bug", Description: "d"}},
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var called atomic.Int32
	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			called.Add(1)
			issue := &jira.Issue{Key: issueKey}
			issue.Fields.Summary = "s"
			issue.Fields.Labels = []string{}
			return issue, nil
		},
	}
	llmProvider := &MockLLMProvider{
		AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
			return "bug", nil
		},
	}

	l := New(cfg, jiraClient, llmProvider, logger, false)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	stats, results, _ := l.ProcessTickets(ctx, "TEST", 1, 5, 1)
	if stats.Processed != 0 {
		t.Errorf("ProcessTickets() processed = %d, want 0", stats.Processed)
	}
	if len(results) != 0 {
		t.Errorf("ProcessTickets() results = %d, want 0", len(results))
	}
	if called.Load() != 0 {
		t.Errorf("GetIssue() called %d times, want 0", called.Load())
	}
}

func TestUpdateStats(t *testing.T) {
	stats := &Stats{}

	tests := []struct {
		name      string
		result    Result
		wantStats Stats
	}{
		{
			name: "successful label",
			result: Result{
				Success: true,
				Label:   "bug",
			},
			wantStats: Stats{
				Processed: 1,
				Labeled:   1,
			},
		},
		{
			name: "skipped ticket",
			result: Result{
				Success:    true,
				Skipped:    true,
				SkipReason: "already labeled",
			},
			wantStats: Stats{
				Processed: 1,
				Skipped:   1,
			},
		},
		{
			name: "failed ticket",
			result: Result{
				Error: errors.New("API error"),
			},
			wantStats: Stats{
				Processed: 1,
				Failed:    1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats = &Stats{} // Reset
			updateStats(stats, &tt.result)

			if stats.Processed != tt.wantStats.Processed {
				t.Errorf("Processed = %d, want %d", stats.Processed, tt.wantStats.Processed)
			}
			if stats.Labeled != tt.wantStats.Labeled {
				t.Errorf("Labeled = %d, want %d", stats.Labeled, tt.wantStats.Labeled)
			}
			if stats.Skipped != tt.wantStats.Skipped {
				t.Errorf("Skipped = %d, want %d", stats.Skipped, tt.wantStats.Skipped)
			}
			if stats.Failed != tt.wantStats.Failed {
				t.Errorf("Failed = %d, want %d", stats.Failed, tt.wantStats.Failed)
			}
		})
	}
}

func TestLabeler_ProcessSingleTicket_SanitizesLLMInput(t *testing.T) {
	cfg := &config.Config{
		Labels: []config.LabelConfig{{Name: "bug", Description: "d"}},
		LLM: config.LLMConfig{
			Provider:            "openai",
			TicketContentMode:   config.TicketContentModeRedacted,
			MaxDescriptionChars: 32,
		},
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	var gotSummary string
	var gotDescription string
	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			issue := &jira.Issue{Key: issueKey}
			issue.Fields.Summary = "Email alice@example.com"
			issue.Fields.Description = "Token=supersecret123 and see https://example.com/very/long/path"
			issue.Fields.Labels = []string{}
			return issue, nil
		},
	}
	llmProvider := &MockLLMProvider{
		AnalyzeFunc: func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
			gotSummary = summary
			gotDescription = description
			return "bug", nil
		},
	}

	l := New(cfg, jiraClient, llmProvider, logger, false)
	result := l.processSingleTicket(t.Context(), "TEST", 1)
	if result.Error != nil {
		t.Fatalf("processSingleTicket() unexpected error = %v", result.Error)
	}
	if strings.Contains(gotSummary, "alice@example.com") {
		t.Errorf("summary passed to LLM still contains email: %q", gotSummary)
	}
	if strings.Contains(gotDescription, "supersecret123") {
		t.Errorf("description passed to LLM still contains secret: %q", gotDescription)
	}
	if strings.Contains(gotDescription, "https://example.com/very/long/path") {
		t.Errorf("description passed to LLM still contains URL: %q", gotDescription)
	}
	if !strings.Contains(gotDescription, "[TRUNCATED]") {
		t.Errorf("description passed to LLM should be truncated, got %q", gotDescription)
	}
}
