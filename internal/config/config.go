// Package config handles application configuration loading and validation
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/drpaneas/ai-labeler/internal/jiraurl"
)

type LabelConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type JIRAConfig struct {
	URL     string `json:"url"`
	Project string `json:"project"`
}

type LLMConfig struct {
	Provider            string `json:"provider"`
	Model               string `json:"model,omitzero"`
	TicketContentMode   string `json:"ticket_content_mode,omitzero"`
	MaxDescriptionChars int    `json:"max_description_chars,omitzero"`
}

const (
	TicketContentModeRedacted    = "redacted"
	TicketContentModeSummaryOnly = "summary_only"
	TicketContentModeFull        = "full"
	DefaultMaxDescriptionChars   = 4000
)

// Config holds the application configuration. Use LoadConfig to create a
// validated instance. If constructing directly, call Validate before use.
type Config struct {
	Labels []LabelConfig `json:"labels"`
	JIRA   JIRAConfig    `json:"jira"`
	LLM    LLMConfig     `json:"llm"`
}

func LoadConfig(configPath string) (*Config, error) {
	if configPath == "" {
		configPath = os.Getenv("CONFIG_FILE")
		if configPath == "" {
			configPath = "config.json"
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config file %s is required but not found. Please create a config.json file (see config-example.json for reference)", configPath)
		}
		return nil, fmt.Errorf("reading config file %s: %w", configPath, err)
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("parsing config file %s: %w", configPath, err)
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config file %s: %w", configPath, err)
	}

	return &config, nil
}

func (c *Config) Validate() error {
	if len(c.Labels) == 0 {
		return fmt.Errorf("must contain at least one label in 'labels' array")
	}

	labelNames := make(map[string]bool)
	for i, label := range c.Labels {
		if label.Name == "" {
			return fmt.Errorf("label at index %d must have a name", i)
		}
		if label.Description == "" {
			return fmt.Errorf("label '%s' must have a description", label.Name)
		}
		lower := strings.ToLower(label.Name)
		if labelNames[lower] {
			return fmt.Errorf("duplicate label name '%s' (case-insensitive)", label.Name)
		}
		labelNames[lower] = true
	}

	if c.JIRA.URL == "" {
		return fmt.Errorf("must specify 'jira.url'")
	}
	normalizedJIRAURL, err := jiraurl.Normalize(c.JIRA.URL)
	if err != nil {
		return fmt.Errorf("invalid jira.url: %w", err)
	}
	c.JIRA.URL = normalizedJIRAURL
	if c.JIRA.Project == "" {
		return fmt.Errorf("must specify 'jira.project'")
	}
	if c.LLM.Provider == "" {
		return fmt.Errorf("must specify 'llm.provider'")
	}
	if c.LLM.TicketContentMode == "" {
		c.LLM.TicketContentMode = TicketContentModeRedacted
	}
	switch c.LLM.TicketContentMode {
	case TicketContentModeRedacted, TicketContentModeSummaryOnly, TicketContentModeFull:
	default:
		return fmt.Errorf("llm.ticket_content_mode must be one of %q, %q, or %q", TicketContentModeRedacted, TicketContentModeSummaryOnly, TicketContentModeFull)
	}
	if c.LLM.MaxDescriptionChars <= 0 {
		c.LLM.MaxDescriptionChars = DefaultMaxDescriptionChars
	}

	return nil
}

func (c *Config) ApplyEnvOverrides(logger *slog.Logger) {
	if envURL := os.Getenv("JIRA_URL"); envURL != "" {
		normalizedEnvURL, err := jiraurl.Normalize(envURL)
		if err != nil {
			logger.Error("Ignoring invalid JIRA URL override",
				"env_value", envURL,
				"error", err,
				"source", "environment variable")
		} else if !jiraurl.SameHost(c.JIRA.URL, normalizedEnvURL) {
			logger.Error("Ignoring untrusted JIRA URL override",
				"config_value", c.JIRA.URL,
				"env_value", normalizedEnvURL,
				"source", "environment variable")
		} else if normalizedEnvURL != c.JIRA.URL {
			logger.Info("JIRA URL override detected",
				"config_value", c.JIRA.URL,
				"env_value", normalizedEnvURL,
				"source", "environment variable")
			c.JIRA.URL = normalizedEnvURL
		}
	}

	if envProject := os.Getenv("JIRA_PROJECT"); envProject != "" {
		if envProject != c.JIRA.Project {
			logger.Info("JIRA project override detected",
				"config_value", c.JIRA.Project,
				"env_value", envProject,
				"source", "environment variable")
			c.JIRA.Project = envProject
		}
	}

	if envProvider := os.Getenv("LLM_PROVIDER"); envProvider != "" {
		envProvider = strings.ToLower(envProvider)
		if envProvider != strings.ToLower(c.LLM.Provider) {
			logger.Info("LLM provider override detected",
				"config_value", c.LLM.Provider,
				"env_value", envProvider,
				"source", "environment variable")
			c.LLM.Provider = envProvider
			if os.Getenv("LLM_MODEL") == "" {
				c.LLM.Model = ""
			}
		}
	}

	if envModel := os.Getenv("LLM_MODEL"); envModel != "" {
		if envModel != c.LLM.Model {
			logger.Info("LLM model override detected",
				"config_value", c.LLM.Model,
				"env_value", envModel,
				"source", "environment variable")
			c.LLM.Model = envModel
		}
	}
}

func (c *Config) BuildLabelInfo() string {
	var labelNames []string
	for _, label := range c.Labels {
		labelNames = append(labelNames, label.Name)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available labels: %s\n\n", strings.Join(labelNames, ", ")))

	for _, label := range c.Labels {
		sb.WriteString(fmt.Sprintf("- %s: %s\n", label.Name, label.Description))
	}

	return sb.String()
}

func (c *Config) ValidLabels() []string {
	labels := make([]string, len(c.Labels))
	for i, label := range c.Labels {
		labels[i] = label.Name
	}
	return labels
}

func (c *Config) LabelByName(name string) (*LabelConfig, bool) {
	nameLower := strings.ToLower(name)
	for i := range c.Labels {
		if strings.ToLower(c.Labels[i].Name) == nameLower {
			return &c.Labels[i], true
		}
	}
	return nil, false
}
