package llm

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"
)

func TestNewProvider(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	tests := []struct {
		name         string
		providerName string
		model        string
		apiKey       string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "empty API key",
			providerName: "openai",
			apiKey:       "",
			wantErr:      true,
			errContains:  "API key is required",
		},
		{
			name:         "unsupported provider",
			providerName: "unsupported",
			apiKey:       "some-key",
			wantErr:      true,
			errContains:  "unsupported LLM provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewProvider(ctx, tt.providerName, tt.model, tt.apiKey, logger)

			if tt.wantErr {
				if err == nil {
					t.Error("NewProvider() expected error but got none")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("NewProvider() error = %v, want error containing %v", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("NewProvider() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestBaseProvider_ParseStructuredResponse(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	provider := &baseProvider{
		provider: "test",
		logger:   logger,
	}

	tests := []struct {
		name     string
		response string
		want     string
		wantErr  bool
	}{
		{
			name: "valid JSON response",
			response: `{
				"label": "bug",
				"confidence": "high",
				"reasoning": "This is clearly a bug report"
			}`,
			want:    "bug",
			wantErr: false,
		},
		{
			name: "JSON with extra text",
			response: `Here is my analysis:
			{
				"label": "feature",
				"confidence": "medium",
				"reasoning": "New functionality requested"
			}
			That's my recommendation.`,
			want:    "feature",
			wantErr: false,
		},
		{
			name:     "no JSON",
			response: "I think the label should be bug",
			want:     "",
			wantErr:  true,
		},
		{
			name:     "invalid JSON",
			response: `{"label": "bug", "confidence": }`,
			want:     "",
			wantErr:  true,
		},
		{
			name: "JSON with whitespace in label",
			response: `{
				"label": "  security  ",
				"confidence": "high"
			}`,
			want:    "security",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := provider.parseStructuredResponse(tt.response)

			if tt.wantErr {
				if err == nil {
					t.Error("parseStructuredResponse() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("parseStructuredResponse() unexpected error = %v", err)
				}
				if got != tt.want {
					t.Errorf("parseStructuredResponse() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestBaseProvider_ExtractLabelFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	provider := &baseProvider{
		provider: "test",
		logger:   logger,
	}

	validLabels := []string{"bug", "feature", "security", "documentation", "aws-abuse"}

	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name:    "exact match",
			output:  "The label is bug",
			want:    "bug",
			wantErr: false,
		},
		{
			name:    "case insensitive match",
			output:  "I recommend FEATURE",
			want:    "feature",
			wantErr: false,
		},
		{
			name:    "multiple labels mentioned",
			output:  "This could be bug or feature, but I think bug",
			want:    "feature", // Longest match wins
			wantErr: false,
		},
		{
			name:    "no valid label",
			output:  "This is about testing",
			want:    "",
			wantErr: true,
		},
		{
			name:    "substring should not match",
			output:  "This is bugging me and features are featureless",
			want:    "",
			wantErr: true,
		},
		{
			name:    "hyphenated label with hyphen",
			output:  "this is aws-abuse related",
			want:    "aws-abuse",
			wantErr: false,
		},
		{
			name:    "hyphenated label without hyphen",
			output:  "this is aws abuse related",
			want:    "aws-abuse",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := provider.extractLabelFallback(tt.output, validLabels)

			if tt.wantErr {
				if err == nil {
					t.Error("extractLabelFallback() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("extractLabelFallback() unexpected error = %v", err)
				}
				if got != tt.want {
					t.Errorf("extractLabelFallback() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestBaseProvider_CanonicalLabel(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	provider := &baseProvider{
		provider: "test",
		logger:   logger,
	}

	validLabels := []string{"bug", "feature", "security"}

	tests := []struct {
		name      string
		label     string
		wantLabel string
		wantOk    bool
	}{
		{"exact match", "bug", "bug", true},
		{"case insensitive returns canonical", "BUG", "bug", true},
		{"mixed case returns canonical", "Feature", "feature", true},
		{"not in list", "documentation", "", false},
		{"empty", "", "", false},
		{"partial match", "sec", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := provider.canonicalLabel(tt.label, validLabels)
			if ok != tt.wantOk {
				t.Errorf("canonicalLabel(%v) ok = %v, want %v", tt.label, ok, tt.wantOk)
			}
			if got != tt.wantLabel {
				t.Errorf("canonicalLabel(%v) = %v, want %v", tt.label, got, tt.wantLabel)
			}
		})
	}
}

func TestBaseProvider_BuildStructuredPrompt(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	provider := &baseProvider{
		provider: "test",
		logger:   logger,
	}

	summary := "Application crashes on startup"
	description := "The app crashes with a null pointer exception when starting"
	labelInfo := "- bug: For defects and issues\n- feature: For new functionality"
	validLabels := []string{"bug", "feature"}

	prompt := provider.buildStructuredPrompt(summary, description, labelInfo, validLabels)

	// Check that prompt contains key elements
	checks := []struct {
		name     string
		contains string
	}{
		{"summary", summary},
		{"description", description},
		{"label info", labelInfo},
		{"valid labels list", "[bug, feature]"},
		{"JSON format instruction", "valid JSON"},
		{"label field instruction", `"label"`},
	}

	for _, check := range checks {
		t.Run("contains "+check.name, func(t *testing.T) {
			if !strings.Contains(prompt, check.contains) {
				t.Errorf("Prompt missing %s: %s", check.name, check.contains)
			}
		})
	}
}

func TestStructuredResponse(t *testing.T) {
	tests := []struct {
		name  string
		resp  structuredResponse
		check func(t *testing.T, resp structuredResponse)
	}{
		{
			name: "full response",
			resp: structuredResponse{
				Label:      "bug",
				Confidence: "high",
				Reasoning:  "Clear bug report",
			},
			check: func(t *testing.T, resp structuredResponse) {
				if resp.Label != "bug" {
					t.Errorf("Label = %v, want bug", resp.Label)
				}
				if resp.Confidence != "high" {
					t.Errorf("Confidence = %v, want high", resp.Confidence)
				}
				if resp.Reasoning != "Clear bug report" {
					t.Errorf("Reasoning = %v, want Clear bug report", resp.Reasoning)
				}
			},
		},
		{
			name: "minimal response",
			resp: structuredResponse{
				Label: "feature",
			},
			check: func(t *testing.T, resp structuredResponse) {
				if resp.Label != "feature" {
					t.Errorf("Label = %v, want feature", resp.Label)
				}
				if resp.Confidence != "" {
					t.Errorf("Confidence = %v, want empty", resp.Confidence)
				}
				if resp.Reasoning != "" {
					t.Errorf("Reasoning = %v, want empty", resp.Reasoning)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.check(t, tt.resp)
		})
	}
}

// MockLLMProvider for testing
type MockLLMProvider struct {
	AnalyzeFunc func(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error)
	Name        string
}

func (m *MockLLMProvider) AnalyzeTicket(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
	if m.AnalyzeFunc != nil {
		return m.AnalyzeFunc(ctx, summary, description, labelInfo, validLabels)
	}
	return "", nil
}

func (m *MockLLMProvider) GetProviderName() string {
	return m.Name
}

var _ Provider = (*MockLLMProvider)(nil) // Compile-time interface check

func TestEnvVarForProvider(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		want     string
		wantErr  bool
	}{
		{"openai", "openai", "OPENAI_API_KEY", false},
		{"openai upper", "OpenAI", "OPENAI_API_KEY", false},
		{"gemini", "gemini", "GOOGLE_API_KEY", false},
		{"googleai", "googleai", "GOOGLE_API_KEY", false},
		{"claude", "claude", "ANTHROPIC_API_KEY", false},
		{"anthropic", "anthropic", "ANTHROPIC_API_KEY", false},
		{"unknown", "llama", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EnvVarForProvider(tt.provider)
			if tt.wantErr {
				if err == nil {
					t.Error("EnvVarForProvider() expected error")
				}
			} else {
				if err != nil {
					t.Errorf("EnvVarForProvider() unexpected error = %v", err)
				}
				if got != tt.want {
					t.Errorf("EnvVarForProvider() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestGetProviderName(t *testing.T) {
	p := &baseProvider{provider: "gemini", logger: slog.New(slog.NewTextHandler(os.Stdout, nil))}
	if got := p.GetProviderName(); got != "gemini" {
		t.Errorf("GetProviderName() = %q, want %q", got, "gemini")
	}
}

type fakeLLM struct {
	response string
	err      error
}

func (f *fakeLLM) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llms.ContentResponse{
		Choices: []*llms.ContentChoice{
			{Content: f.response},
		},
	}, nil
}

func (f *fakeLLM) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.response, nil
}

func TestAnalyzeTicket_StructuredJSON(t *testing.T) {
	ctx := t.Context()
	p := &baseProvider{
		llm:      &fakeLLM{response: `{"label": "bug"}`},
		provider: "test",
		logger:   slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	label, err := p.AnalyzeTicket(ctx, "crash", "null pointer", "info", []string{"bug", "feature"})
	if err != nil {
		t.Fatalf("AnalyzeTicket() unexpected error = %v", err)
	}
	if label != "bug" {
		t.Errorf("AnalyzeTicket() = %q, want %q", label, "bug")
	}
}

func TestAnalyzeTicket_FallbackExtraction(t *testing.T) {
	ctx := t.Context()
	p := &baseProvider{
		llm:      &fakeLLM{response: "I think it is a feature request"},
		provider: "test",
		logger:   slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	label, err := p.AnalyzeTicket(ctx, "add button", "new ui", "info", []string{"bug", "feature"})
	if err != nil {
		t.Fatalf("AnalyzeTicket() unexpected error = %v", err)
	}
	if label != "feature" {
		t.Errorf("AnalyzeTicket() = %q, want %q", label, "feature")
	}
}

func TestAnalyzeTicket_LLMError(t *testing.T) {
	ctx := t.Context()
	p := &baseProvider{
		llm:      &fakeLLM{err: errors.New("api down")},
		provider: "test",
		logger:   slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	_, err := p.AnalyzeTicket(ctx, "s", "d", "info", []string{"bug"})
	if err == nil {
		t.Fatal("AnalyzeTicket() expected error")
	}
	if !strings.Contains(err.Error(), "generating response") {
		t.Errorf("AnalyzeTicket() error = %v, want containing 'generating response'", err)
	}
}

func TestAnalyzeTicket_NonCanonicalJSON(t *testing.T) {
	ctx := t.Context()
	p := &baseProvider{
		llm:      &fakeLLM{response: `{"label": "BUG"}`},
		provider: "test",
		logger:   slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	label, err := p.AnalyzeTicket(ctx, "crash", "err", "info", []string{"bug", "feature"})
	if err != nil {
		t.Fatalf("AnalyzeTicket() unexpected error = %v", err)
	}
	if label != "bug" {
		t.Errorf("AnalyzeTicket() = %q, want canonical %q", label, "bug")
	}
}

func TestAnalyzeTicket_InvalidLabelFallsToFallback(t *testing.T) {
	ctx := t.Context()
	p := &baseProvider{
		llm:      &fakeLLM{response: `{"label": "nonexistent"}`},
		provider: "test",
		logger:   slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
	_, err := p.AnalyzeTicket(ctx, "s", "d", "info", []string{"bug", "feature"})
	if err == nil {
		t.Fatal("AnalyzeTicket() expected error for invalid label with no fallback match")
	}
}

func TestContainsWordSequence_EmptySeq(t *testing.T) {
	if containsWordSequence([]string{"hello"}, nil) {
		t.Error("containsWordSequence() should return false for empty seq")
	}
}

func TestNewProvider_OpenAI(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	p, err := NewProvider(ctx, "openai", "gpt-4", "fake-key", logger)
	if err != nil {
		t.Fatalf("NewProvider(openai) unexpected error = %v", err)
	}
	if p.GetProviderName() != "openai" {
		t.Errorf("GetProviderName() = %q, want %q", p.GetProviderName(), "openai")
	}
}

func TestNewProvider_Claude(t *testing.T) {
	ctx := t.Context()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	p, err := NewProvider(ctx, "claude", "", "fake-key", logger)
	if err != nil {
		t.Fatalf("NewProvider(claude) unexpected error = %v", err)
	}
	if p.GetProviderName() != "claude" {
		t.Errorf("GetProviderName() = %q, want %q", p.GetProviderName(), "claude")
	}
}
