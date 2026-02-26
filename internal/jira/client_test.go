package jira

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drpaneas/ai-labeler/internal/retry"
)

func TestExtractDescription(t *testing.T) {
	tests := []struct {
		name        string
		description any
		want        string
	}{
		{
			name:        "nil description",
			description: nil,
			want:        "",
		},
		{
			name:        "string description",
			description: "Simple string description",
			want:        "Simple string description",
		},
		{
			name: "ADF document format",
			description: map[string]any{
				"type":    "doc",
				"version": 1,
				"content": []any{
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "First paragraph",
							},
						},
					},
					map[string]any{
						"type": "paragraph",
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "Second paragraph",
							},
						},
					},
				},
			},
			want: "First paragraph Second paragraph",
		},
		{
			name: "nested content",
			description: map[string]any{
				"content": []any{
					map[string]any{
						"content": []any{
							map[string]any{
								"text": "Deeply nested text",
							},
						},
					},
				},
			},
			want: "Deeply nested text",
		},
		{
			name: "mixed content types",
			description: map[string]any{
				"content": []any{
					map[string]any{
						"text": "Direct text",
					},
					map[string]any{
						"content": []any{
							map[string]any{
								"text": "Nested text",
							},
						},
					},
				},
			},
			want: "Direct text Nested text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractDescription(tt.description)
			if got != tt.want {
				t.Errorf("ExtractDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		auth      Authenticator
		wantErr   bool
	}{
		{
			name:    "empty base URL",
			baseURL: "",
			auth:    NewBearerAuth("test-token"),
			wantErr: true,
		},
		{
			name:    "nil authenticator",
			baseURL: "https://test.atlassian.net",
			auth:    nil,
			wantErr: true,
		},
		{
			name:    "basic auth",
			baseURL: "https://test.atlassian.net",
			auth:    NewBasicAuth("test@example.com", "test-token"),
			wantErr: false,
		},
		{
			name:    "bearer auth",
			baseURL: "https://test.atlassian.net",
			auth:    NewBearerAuth("test-token"),
			wantErr: false,
		},
		{
			name:    "trailing slash removed",
			baseURL: "https://test.atlassian.net/",
			auth:    NewBearerAuth("test-token"),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.baseURL, tt.auth)

			if tt.wantErr {
				if err == nil {
					t.Error("NewClient() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("NewClient() unexpected error = %v", err)
				}
				if strings.HasSuffix(client.baseURL, "/") {
					t.Error("Base URL should not have trailing slash")
				}
			}
		})
	}
}

func TestClient_GetIssue(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Error("Missing Authorization header")
		}

		// Return based on issue key
		if strings.Contains(r.URL.Path, "TEST-404") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		
		if strings.Contains(r.URL.Path, "TEST-401") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if strings.Contains(r.URL.Path, "TEST-500") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Return success response
		issue := Issue{
			Key: "TEST-123",
		}
		issue.Fields.Summary = "Test Issue"
		issue.Fields.Description = "Test Description"
		issue.Fields.Labels = []string{"bug", "urgent"}

		json.NewEncoder(w).Encode(issue)
	}))
	defer server.Close()

	retryConfig := &retry.Config{
		MaxRetries:     2,
		InitialBackoff: 1 * time.Millisecond,
		MaxBackoff:     10 * time.Millisecond,
		Multiplier:     2.0,
	}

	client, err := NewClient(server.URL, NewBearerAuth("test-token"),
		WithRetryConfig(retryConfig),
		WithLogger(slog.New(slog.NewTextHandler(os.Stdout, nil))),
	)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	tests := []struct {
		name      string
		issueKey  string
		wantErr   bool
		wantIssue bool
	}{
		{
			name:      "successful request",
			issueKey:  "TEST-123",
			wantErr:   false,
			wantIssue: true,
		},
		{
			name:      "not found",
			issueKey:  "TEST-404",
			wantErr:   true,
			wantIssue: false,
		},
		{
			name:      "unauthorized",
			issueKey:  "TEST-401",
			wantErr:   true,
			wantIssue: false,
		},
		{
			name:      "server error (retryable)",
			issueKey:  "TEST-500",
			wantErr:   true,
			wantIssue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			issue, err := client.GetIssue(ctx, tt.issueKey)
			
			if tt.wantErr {
				if err == nil {
					t.Error("GetIssue() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("GetIssue() unexpected error = %v", err)
				}
			}
			
			if tt.wantIssue {
				if issue == nil {
					t.Error("GetIssue() expected issue but got nil")
				} else {
					if issue.Key != tt.issueKey {
						t.Errorf("GetIssue() issue key = %s, want %s", issue.Key, tt.issueKey)
					}
					if issue.Fields.Summary != "Test Issue" {
						t.Errorf("GetIssue() summary = %s, want Test Issue", issue.Fields.Summary)
					}
				}
			}
		})
	}
}

func TestClient_UpdateIssueLabels(t *testing.T) {
	requestReceived := false
	var receivedLabels []string

	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("Request method = %s, want PUT", r.Method)
		}

		// Check content type
		if r.Header.Get("Content-Type") != "application/json" {
			t.Error("Missing Content-Type: application/json header")
		}

		// Parse request body
		var update IssueUpdate
		if err := json.NewDecoder(r.Body).Decode(&update); err == nil {
			receivedLabels = update.Fields.Labels
			requestReceived = true
		}

		// Return based on issue key
		if strings.Contains(r.URL.Path, "TEST-403") {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(server.URL, NewBearerAuth("test-token"))
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	tests := []struct {
		name     string
		issueKey string
		labels   []string
		wantErr  bool
	}{
		{
			name:     "successful update",
			issueKey: "TEST-123",
			labels:   []string{"bug", "urgent", "security"},
			wantErr:  false,
		},
		{
			name:     "forbidden",
			issueKey: "TEST-403",
			labels:   []string{"bug"},
			wantErr:  true,
		},
		{
			name:     "empty labels",
			issueKey: "TEST-123",
			labels:   []string{},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestReceived = false
			receivedLabels = nil
			
			ctx := context.Background()
			err := client.UpdateIssueLabels(ctx, tt.issueKey, tt.labels)
			
			if tt.wantErr {
				if err == nil {
					t.Error("UpdateIssueLabels() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("UpdateIssueLabels() unexpected error = %v", err)
				}
				if !requestReceived {
					t.Error("UpdateIssueLabels() request not received by server")
				}
				// Check labels match
				if len(receivedLabels) != len(tt.labels) {
					t.Errorf("UpdateIssueLabels() sent %d labels, want %d", len(receivedLabels), len(tt.labels))
				}
			}
		})
	}
}

func TestAuthenticators(t *testing.T) {
	tests := []struct {
		name      string
		auth      Authenticator
		wantErr   bool
		checkAuth func(t *testing.T, req *http.Request)
	}{
		{
			name:    "basic auth valid",
			auth:    NewBasicAuth("test@example.com", "test-token"),
			wantErr: false,
			checkAuth: func(t *testing.T, req *http.Request) {
				auth := req.Header.Get("Authorization")
				if !strings.HasPrefix(auth, "Basic ") {
					t.Error("Expected Basic auth header")
				}
			},
		},
		{
			name:    "basic auth missing email",
			auth:    NewBasicAuth("", "test-token"),
			wantErr: true,
		},
		{
			name:    "basic auth missing token",
			auth:    NewBasicAuth("test@example.com", ""),
			wantErr: true,
		},
		{
			name:    "bearer auth valid",
			auth:    NewBearerAuth("test-token"),
			wantErr: false,
			checkAuth: func(t *testing.T, req *http.Request) {
				auth := req.Header.Get("Authorization")
				if auth != "Bearer test-token" {
					t.Errorf("Expected 'Bearer test-token', got %s", auth)
				}
			},
		},
		{
			name:    "bearer auth missing token",
			auth:    NewBearerAuth(""),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://test.com", nil)
			err := tt.auth.SetAuth(req)
			
			if tt.wantErr {
				if err == nil {
					t.Error("SetAuth() expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("SetAuth() unexpected error = %v", err)
				}
				if tt.checkAuth != nil {
					tt.checkAuth(t, req)
				}
			}
		})
	}
}
