package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestParseFlagsFromArgs_ModeSelection(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
		check       func(t *testing.T, opts *options)
	}{
		{
			name:        "requires mode",
			args:        []string{"--ticket", "1"},
			wantErr:     true,
			errContains: "exactly one of --write or --dry-run",
		},
		{
			name: "write mode",
			args: []string{"--ticket", "1", "--write"},
			check: func(t *testing.T, opts *options) {
				if !opts.write || opts.dryRun {
					t.Fatalf("expected write mode, got %+v", opts)
				}
			},
		},
		{
			name: "dry run mode",
			args: []string{"--ticket", "1", "--dry-run"},
			check: func(t *testing.T, opts *options) {
				if opts.write || !opts.dryRun {
					t.Fatalf("expected dry-run mode, got %+v", opts)
				}
			},
		},
		{
			name:        "rejects both modes",
			args:        []string{"--ticket", "1", "--write", "--dry-run"},
			wantErr:     true,
			errContains: "exactly one of --write or --dry-run",
		},
		{
			name: "version does not require mode",
			args: []string{"--version"},
			check: func(t *testing.T, opts *options) {
				if !opts.version {
					t.Fatal("expected version flag to be set")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseFlagsFromArgs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %v, want containing %q", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFlagsFromArgs() unexpected error = %v", err)
			}
			if tt.check != nil {
				tt.check(t, opts)
			}
		})
	}
}

func TestCreateJIRAAuth(t *testing.T) {
	tests := []struct {
		name        string
		email       string
		token       string
		wantErr     bool
		errContains string
	}{
		{
			name:  "basic auth from Jira Cloud credentials",
			email: "user@example.com",
			token: "api-token",
		},
		{
			name:        "missing Jira email",
			token:       "api-token",
			wantErr:     true,
			errContains: "JIRA_EMAIL",
		},
		{
			name:        "missing Jira token",
			email:       "user@example.com",
			wantErr:     true,
			errContains: "JIRA_API_TOKEN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("JIRA_EMAIL", tt.email)
			t.Setenv("JIRA_API_TOKEN", tt.token)

			auth, err := createJIRAAuth()
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %v, want containing %q", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("createJIRAAuth() unexpected error = %v", err)
			}

			req, err := http.NewRequest("GET", "https://example.atlassian.net", nil)
			if err != nil {
				t.Fatalf("http.NewRequest() error = %v", err)
			}
			if err := auth.SetAuth(req); err != nil {
				t.Fatalf("SetAuth() error = %v", err)
			}
			if !strings.HasPrefix(req.Header.Get("Authorization"), "Basic ") {
				t.Fatalf("Authorization = %q, want Basic auth header", req.Header.Get("Authorization"))
			}
		})
	}
}
