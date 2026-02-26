package labeler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drpaneas/ai-labeler/internal/config"
	"github.com/drpaneas/ai-labeler/internal/jira"
)

// MockJIRAClient for testing
type MockJIRAClient struct {
	GetIssueFunc         func(ctx context.Context, issueKey string) (*jira.Issue, error)
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
		name           string
		currentLabels  []string
		wantAddLabel   bool
		wantReason     string
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
		name       string
		ticketNum  int
		setupMocks func() (*MockJIRAClient, *MockLLMProvider)
		dryRun     bool
		wantResult Result
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
			dryRun: false,
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
			dryRun: true,
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
			dryRun: false,
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
			dryRun: false,
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
			dryRun: false,
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
				config:      cfg,
				jiraClient:  jiraClient,
				llmProvider: llmProvider,
				logger:      logger,
				dryRun:      tt.dryRun,
			}
			
			ctx := context.Background()
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
	
	l := New(cfg, jiraClient, llmProvider, logger, false)
	
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
			
			ctx := context.Background()
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
	jiraClient := &MockJIRAClient{
		GetIssueFunc: func(ctx context.Context, issueKey string) (*jira.Issue, error) {
			processCount.Add(1)
			// Simulate slow processing
			time.Sleep(100 * time.Millisecond)
			
			// Check for cancellation
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
			}
			
			issue := &jira.Issue{Key: issueKey}
			issue.Fields.Summary = "Test"
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
	
	// Create cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	
	// Cancel after a short delay
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()
	
	stats, results, err := l.ProcessTickets(ctx, "TEST", 1, 10, 2)

	if err != nil {
		t.Errorf("ProcessTickets() unexpected error = %v", err)
	}

	// Cancellation should cause some tickets to fail
	if stats.Failed == 0 {
		t.Error("ProcessTickets() expected some failed tickets from cancellation")
	}

	// Should have processed some but not all tickets
	pc := int(processCount.Load())
	if pc >= 10 {
		t.Error("ProcessTickets() processed all tickets despite cancellation")
	}
	if pc == 0 {
		t.Error("ProcessTickets() processed no tickets")
	}

	// Results should not exceed processed count
	if len(results) > pc {
		t.Errorf("More results (%d) than processed tickets (%d)", len(results), pc)
	}
}

func TestUpdateStats(t *testing.T) {
	stats := &Stats{}
	
	tests := []struct {
		name       string
		result     Result
		wantStats  Stats
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
