// Package llm provides interfaces and implementations for LLM providers
package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/anthropic"
	"github.com/tmc/langchaingo/llms/googleai"
	"github.com/tmc/langchaingo/llms/openai"
)

// Provider is an interface for LLM providers
type Provider interface {
	// AnalyzeTicket analyzes a JIRA ticket and returns a suggested label
	AnalyzeTicket(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error)
	// GetProviderName returns the name of the provider
	GetProviderName() string
}

type structuredResponse struct {
	Label      string `json:"label"`
	Confidence string `json:"confidence,omitempty"`
	Reasoning  string `json:"reasoning,omitempty"`
}

type baseProvider struct {
	llm      llms.Model
	provider string
	logger   *slog.Logger
}

func NewProvider(ctx context.Context, providerName, model, apiKey string, logger *slog.Logger) (Provider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required for provider %s", providerName)
	}

	provider := strings.ToLower(providerName)

	var llm llms.Model
	var err error

	switch provider {
	case "openai":
		llm, err = createOpenAIProvider(apiKey, model)
	case "gemini", "googleai":
		llm, err = createGeminiProvider(ctx, apiKey, model)
	case "claude", "anthropic":
		llm, err = createClaudeProvider(apiKey, model)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s (supported: openai, gemini, claude)", providerName)
	}

	if err != nil {
		return nil, fmt.Errorf("creating %s provider: %w", providerName, err)
	}

	return &baseProvider{
		llm:      llm,
		provider: providerName,
		logger:   logger,
	}, nil
}

// EnvVarForProvider returns the environment variable name that holds the
// API key for the given provider, or an error if the provider is unknown.
func EnvVarForProvider(provider string) (string, error) {
	switch strings.ToLower(provider) {
	case "openai":
		return "OPENAI_API_KEY", nil
	case "gemini", "googleai":
		return "GOOGLE_API_KEY", nil
	case "claude", "anthropic":
		return "ANTHROPIC_API_KEY", nil
	default:
		return "", fmt.Errorf("unsupported LLM provider: %s (supported: openai, gemini, claude)", provider)
	}
}

func createOpenAIProvider(apiKey, model string) (llms.Model, error) {
	if model == "" {
		model = "gpt-4"
	}
	return openai.New(
		openai.WithToken(apiKey),
		openai.WithModel(model),
	)
}

func createGeminiProvider(ctx context.Context, apiKey, model string) (llms.Model, error) {
	opts := []googleai.Option{
		googleai.WithAPIKey(apiKey),
	}

	if model == "" {
		model = "gemini-2.5-flash"
	}
	opts = append(opts, googleai.WithDefaultModel(model))

	return googleai.New(ctx, opts...)
}

func createClaudeProvider(apiKey, model string) (llms.Model, error) {
	if model == "" {
		model = "claude-3-opus-20240229"
	}
	return anthropic.New(
		anthropic.WithToken(apiKey),
		anthropic.WithModel(model),
	)
}

func (p *baseProvider) AnalyzeTicket(ctx context.Context, summary, description, labelInfo string, validLabels []string) (string, error) {
	prompt := p.buildStructuredPrompt(summary, description, labelInfo, validLabels)

	p.logger.Debug("Sending prompt to LLM",
		"provider", p.provider,
		"summary", summary)

	response, err := llms.GenerateFromSinglePrompt(ctx, p.llm, prompt)
	if err != nil {
		return "", fmt.Errorf("generating response from %s: %w", p.provider, err)
	}

	p.logger.Debug("Received response from LLM",
		"provider", p.provider,
		"response", response)

	label, err := p.parseStructuredResponse(response)
	if err == nil && label != "" {
		if canonical, ok := p.canonicalLabel(label, validLabels); ok {
			return canonical, nil
		}
	}

	return p.extractLabelFallback(response, validLabels)
}

func (p *baseProvider) GetProviderName() string {
	return p.provider
}

func (p *baseProvider) buildStructuredPrompt(summary, description, labelInfo string, validLabels []string) string {
	validLabelsStr := strings.Join(validLabels, ", ")

	prompt := fmt.Sprintf(`You are a JIRA ticket analyzer. Your task is to analyze the following JIRA ticket and select the most appropriate label from the provided list.

JIRA Ticket:
Summary: %s
Description: %s

Label Information:
%s

Instructions:
1. Analyze the ticket content carefully
2. Select EXACTLY ONE label from this list: [%s]
3. The label must match exactly (case-sensitive) from the provided list
4. Respond ONLY with valid JSON in this format:

{"label": "selected_label"}

Important: 
- Do not include any text before or after the JSON
- The "label" field must contain exactly one of: %s
- Do not create new labels or modify existing ones

JSON Response:`, summary, description, labelInfo, validLabelsStr, validLabelsStr)

	return prompt
}

func (p *baseProvider) parseStructuredResponse(response string) (string, error) {
	response = strings.TrimSpace(response)

	startIdx := strings.Index(response, "{")
	endIdx := strings.LastIndex(response, "}")
	
	if startIdx >= 0 && endIdx > startIdx {
		jsonStr := response[startIdx : endIdx+1]
		
		var structured structuredResponse
		if err := json.Unmarshal([]byte(jsonStr), &structured); err == nil {
			p.logger.Debug("Successfully parsed structured response",
				"label", structured.Label,
				"confidence", structured.Confidence,
				"reasoning", structured.Reasoning)
			return strings.TrimSpace(structured.Label), nil
		}
	}
	
	return "", fmt.Errorf("could not parse structured response")
}

// extractLabelFallback uses word-boundary matching as a fallback, checking
// longer labels first so short labels like "doc" don't shadow "documentation".
// It splits the output into words and matches whole words only, preventing
// false positives like "go" matching "going".
func (p *baseProvider) extractLabelFallback(output string, validLabels []string) (string, error) {
	outputLower := strings.ToLower(output)
	words := strings.Fields(outputLower)

	sorted := slices.Clone(validLabels)
	slices.SortFunc(sorted, func(a, b string) int {
		return len(b) - len(a)
	})

	for _, label := range sorted {
		labelLower := strings.ToLower(label)
		labelWords := strings.Fields(labelLower)
		if containsWordSequence(words, labelWords) {
			p.logger.Debug("Found label using fallback extraction",
				"label", label)
			return label, nil
		}
		normalized := strings.NewReplacer("-", " ", "_", " ").Replace(labelLower)
		normWords := strings.Fields(normalized)
		if len(normWords) != len(labelWords) && containsWordSequence(words, normWords) {
			p.logger.Debug("Found label using fallback extraction (normalized)",
				"label", label)
			return label, nil
		}
	}

	return "", fmt.Errorf("no valid label found in response")
}

func containsWordSequence(words, seq []string) bool {
	if len(seq) == 0 {
		return false
	}
	for i := 0; i <= len(words)-len(seq); i++ {
		match := true
		for j := range seq {
			if stripPunctuation(words[i+j]) != seq[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func stripPunctuation(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_')
	})
}

func (p *baseProvider) canonicalLabel(label string, validLabels []string) (string, bool) {
	for _, valid := range validLabels {
		if strings.EqualFold(label, valid) {
			return valid, true
		}
	}
	return "", false
}
