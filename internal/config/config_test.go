package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		configJSON  string
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			configJSON: `{
				"labels": [
					{"name": "bug", "description": "Bug fixes"},
					{"name": "feature", "description": "New features"}
				],
				"jira": {
					"url": "https://test.atlassian.net",
					"project": "TEST"
				},
				"llm": {
					"provider": "openai",
					"model": "gpt-4"
				}
			}`,
			wantErr: false,
		},
		{
			name: "missing labels",
			configJSON: `{
				"labels": [],
				"jira": {
					"url": "https://test.atlassian.net",
					"project": "TEST"
				},
				"llm": {
					"provider": "openai"
				}
			}`,
			wantErr:     true,
			errContains: "at least one label",
		},
		{
			name: "missing jira url",
			configJSON: `{
				"labels": [{"name": "bug", "description": "Bug fixes"}],
				"jira": {
					"project": "TEST"
				},
				"llm": {
					"provider": "openai"
				}
			}`,
			wantErr:     true,
			errContains: "jira.url",
		},
		{
			name: "duplicate labels",
			configJSON: `{
				"labels": [
					{"name": "bug", "description": "Bug fixes"},
					{"name": "bug", "description": "Duplicate"}
				],
				"jira": {
					"url": "https://test.atlassian.net",
					"project": "TEST"
				},
				"llm": {
					"provider": "openai"
				}
			}`,
			wantErr:     true,
			errContains: "duplicate label name",
		},
		{
			name: "case-insensitive duplicate labels",
			configJSON: `{
				"labels": [
					{"name": "Bug", "description": "Bug fixes"},
					{"name": "bug", "description": "Also bugs"}
				],
				"jira": {
					"url": "https://test.atlassian.net",
					"project": "TEST"
				},
				"llm": {
					"provider": "openai"
				}
			}`,
			wantErr:     true,
			errContains: "duplicate label name",
		},
		{
			name: "label without name",
			configJSON: `{
				"labels": [
					{"description": "Missing name"}
				],
				"jira": {
					"url": "https://test.atlassian.net",
					"project": "TEST"
				},
				"llm": {
					"provider": "openai"
				}
			}`,
			wantErr:     true,
			errContains: "must have a name",
		},
		{
			name: "trailing slash in url",
			configJSON: `{
				"labels": [{"name": "bug", "description": "Bug fixes"}],
				"jira": {
					"url": "https://test.atlassian.net/",
					"project": "TEST"
				},
				"llm": {
					"provider": "openai"
				}
			}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tempDir := t.TempDir()
			configPath := filepath.Join(tempDir, "config.json")
			err := os.WriteFile(configPath, []byte(tt.configJSON), 0644)
			if err != nil {
				t.Fatalf("Failed to write test config: %v", err)
			}

			// Load config
			cfg, err := LoadConfig(configPath)
			
			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Errorf("LoadConfig() expected error but got none")
				} else if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("LoadConfig() error = %v, want error containing %v", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("LoadConfig() unexpected error = %v", err)
				} else {
					// Verify trailing slash is removed
					if strings.HasSuffix(cfg.JIRA.URL, "/") {
						t.Errorf("JIRA URL should not have trailing slash, got %s", cfg.JIRA.URL)
					}
				}
			}
		})
	}
}

func TestConfig_ApplyEnvOverrides(t *testing.T) {
	cfg := &Config{
		JIRA: JIRAConfig{
			URL:     "https://original.atlassian.net",
			Project: "ORIG",
		},
		LLM: LLMConfig{
			Provider: "openai",
		},
	}

	t.Setenv("JIRA_URL", "https://override.atlassian.net")
	t.Setenv("JIRA_PROJECT", "OVERRIDE")
	t.Setenv("LLM_PROVIDER", "gemini")

	// Apply overrides
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg.ApplyEnvOverrides(logger)

	// Verify overrides
	if cfg.JIRA.URL != "https://override.atlassian.net" {
		t.Errorf("JIRA URL not overridden, got %s", cfg.JIRA.URL)
	}
	if cfg.JIRA.Project != "OVERRIDE" {
		t.Errorf("JIRA Project not overridden, got %s", cfg.JIRA.Project)
	}
	if cfg.LLM.Provider != "gemini" {
		t.Errorf("LLM Provider not overridden, got %s", cfg.LLM.Provider)
	}
}

func TestConfig_ApplyEnvOverrides_ProviderClearsModel(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Provider: "gemini",
			Model:    "gemini-2.5-flash",
		},
		JIRA: JIRAConfig{
			URL:     "https://test.atlassian.net",
			Project: "TEST",
		},
	}

	t.Setenv("LLM_PROVIDER", "openai")

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg.ApplyEnvOverrides(logger)

	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM Provider = %s, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "" {
		t.Errorf("LLM Model should be cleared when provider changes without LLM_MODEL, got %s", cfg.LLM.Model)
	}
}

func TestConfig_ApplyEnvOverrides_ModelOverride(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Provider: "gemini",
			Model:    "gemini-2.5-flash",
		},
		JIRA: JIRAConfig{
			URL:     "https://test.atlassian.net",
			Project: "TEST",
		},
	}

	t.Setenv("LLM_PROVIDER", "openai")
	t.Setenv("LLM_MODEL", "gpt-4o")

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg.ApplyEnvOverrides(logger)

	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM Provider = %s, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("LLM Model = %s, want gpt-4o", cfg.LLM.Model)
	}
}

func TestConfig_BuildLabelInfo(t *testing.T) {
	cfg := &Config{
		Labels: []LabelConfig{
			{Name: "bug", Description: "Bug fixes"},
			{Name: "feature", Description: "New features"},
		},
	}

	info := cfg.BuildLabelInfo()

	// Check that info contains label names
	if !strings.Contains(info, "bug") || !strings.Contains(info, "feature") {
		t.Errorf("BuildLabelInfo() missing label names")
	}

	// Check that info contains descriptions
	if !strings.Contains(info, "Bug fixes") || !strings.Contains(info, "New features") {
		t.Errorf("BuildLabelInfo() missing descriptions")
	}
}

func TestConfig_ValidLabels(t *testing.T) {
	cfg := &Config{
		Labels: []LabelConfig{
			{Name: "bug", Description: "Bug fixes"},
			{Name: "feature", Description: "New features"},
			{Name: "docs", Description: "Documentation"},
		},
	}

	labels := cfg.ValidLabels()

	if len(labels) != 3 {
		t.Errorf("ValidLabels() returned %d labels, want 3", len(labels))
	}

	expected := []string{"bug", "feature", "docs"}
	for i, label := range labels {
		if label != expected[i] {
			t.Errorf("ValidLabels()[%d] = %s, want %s", i, label, expected[i])
		}
	}
}

func TestConfig_LabelByName(t *testing.T) {
	cfg := &Config{
		Labels: []LabelConfig{
			{Name: "Bug", Description: "Bug fixes"},
			{Name: "Feature", Description: "New features"},
		},
	}

	tests := []struct {
		name      string
		searchFor string
		wantFound bool
		wantName  string
	}{
		{"exact match", "Bug", true, "Bug"},
		{"case insensitive", "bug", true, "Bug"},
		{"mixed case", "FEATURE", true, "Feature"},
		{"not found", "invalid", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, found := cfg.LabelByName(tt.searchFor)
			
			if found != tt.wantFound {
				t.Errorf("LabelByName(%s) found = %v, want %v", tt.searchFor, found, tt.wantFound)
			}
			
			if found && label.Name != tt.wantName {
				t.Errorf("LabelByName(%s) returned %s, want %s", tt.searchFor, label.Name, tt.wantName)
			}
		})
	}
}

func TestConfigFileNotFound(t *testing.T) {
	_, err := LoadConfig("/non/existent/path/config.json")
	if err == nil {
		t.Error("LoadConfig() expected error for non-existent file")
	}
	if !strings.Contains(err.Error(), "required but not found") {
		t.Errorf("LoadConfig() error message should mention file is required")
	}
}
