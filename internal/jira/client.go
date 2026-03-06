// Package jira provides a client for interacting with JIRA REST API v3
package jira

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/drpaneas/ai-labeler/internal/retry"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
	auth       Authenticator
	logger     *slog.Logger
	retry      *retry.Config
}

// Authenticator sets authorization headers on HTTP requests.
type Authenticator interface {
	SetAuth(req *http.Request) error
}

type basicAuth struct {
	email string
	token string
}

func (ba *basicAuth) SetAuth(req *http.Request) error {
	if ba.email == "" || ba.token == "" {
		return fmt.Errorf("email and token are required for basic authentication")
	}
	auth := base64.StdEncoding.EncodeToString([]byte(ba.email + ":" + ba.token))
	req.Header.Set("Authorization", "Basic "+auth)
	return nil
}

type bearerAuth struct {
	token string
}

func (ba *bearerAuth) SetAuth(req *http.Request) error {
	if ba.token == "" {
		return fmt.Errorf("token is required for bearer authentication")
	}
	req.Header.Set("Authorization", "Bearer "+ba.token)
	return nil
}

// NewBasicAuth returns an Authenticator that uses HTTP Basic Auth.
func NewBasicAuth(email, token string) Authenticator {
	return &basicAuth{email: email, token: token}
}

// NewBearerAuth returns an Authenticator that uses Bearer token auth.
func NewBearerAuth(token string) Authenticator {
	return &bearerAuth{token: token}
}

type ClientOption func(*Client)

func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

func WithLogger(logger *slog.Logger) ClientOption {
	return func(c *Client) {
		c.logger = logger
	}
}

func WithRetryConfig(cfg *retry.Config) ClientOption {
	return func(c *Client) {
		c.retry = cfg
	}
}

func NewClient(baseURL string, auth Authenticator, opts ...ClientOption) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}
	if auth == nil {
		return nil, fmt.Errorf("authenticator is required")
	}

	client := &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		auth:   auth,
		logger: slog.Default(),
		retry:  retry.DefaultConfig(),
	}

	for _, opt := range opts {
		opt(client)
	}

	return client, nil
}

type Issue struct {
	Key    string `json:"key"`
	Fields struct {
		Summary     string   `json:"summary"`
		Description any      `json:"description"` // Can be string or JIRA document object
		Labels      []string `json:"labels"`
	} `json:"fields"`
}

type IssueUpdate struct {
	Fields struct {
		Labels []string `json:"labels"`
	} `json:"fields"`
}

func classifyResponse(resp *http.Response, issueKey string) error {
	defer func() {
		// Best-effort drain to allow connection reuse.
		_, _ = io.Copy(io.Discard, resp.Body)
	}()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return retry.NonRetryable(fmt.Errorf("authentication failed (401): check your JIRA_API_TOKEN"))
	case http.StatusForbidden:
		return retry.NonRetryable(fmt.Errorf("access forbidden (403): check permissions for issue %s", issueKey))
	case http.StatusNotFound:
		return retry.NonRetryable(fmt.Errorf("issue %s not found", issueKey))
	case http.StatusTooManyRequests:
		// TODO: respect Retry-After header from JIRA instead of using own backoff
		return retry.Retryable(fmt.Errorf("rate limit exceeded (429)"))
	default:
		if resp.StatusCode >= 500 {
			return retry.Retryable(fmt.Errorf("server error (%d)", resp.StatusCode))
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return retry.NonRetryable(fmt.Errorf("unexpected status code %d (failed to read body: %w)", resp.StatusCode, err))
		}
		return retry.NonRetryable(fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body)))
	}
}

func (c *Client) GetIssue(ctx context.Context, issueKey string) (*Issue, error) {
	var issue Issue
	err := retry.DoWithNotify(ctx, c.retry, func() error {
		url := fmt.Sprintf("%s/rest/api/3/issue/%s", c.baseURL, issueKey)

		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return retry.NonRetryable(fmt.Errorf("creating request: %w", err))
		}

		if err := c.auth.SetAuth(req); err != nil {
			return retry.NonRetryable(fmt.Errorf("setting auth: %w", err))
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return retry.Retryable(fmt.Errorf("making request: %w", err))
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				c.logger.Debug("Failed to close JIRA response body",
					"error", err,
					"issue", issueKey)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return classifyResponse(resp, issueKey)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return retry.NonRetryable(fmt.Errorf("reading response body: %w", err))
		}

		if err := json.Unmarshal(body, &issue); err != nil {
			return retry.NonRetryable(fmt.Errorf("parsing response: %w", err))
		}

		return nil
	}, func(err error, backoff time.Duration) {
		c.logger.Warn("Retrying JIRA request",
			"error", err,
			"backoff", backoff,
			"issue", issueKey)
	})

	if err != nil {
		return nil, err
	}

	return &issue, nil
}

func (c *Client) UpdateIssueLabels(ctx context.Context, issueKey string, labels []string) error {
	return retry.DoWithNotify(ctx, c.retry, func() error {
		url := fmt.Sprintf("%s/rest/api/3/issue/%s", c.baseURL, issueKey)

		update := IssueUpdate{}
		update.Fields.Labels = labels

		payload, err := json.Marshal(update)
		if err != nil {
			return retry.NonRetryable(fmt.Errorf("marshaling update: %w", err))
		}

		req, err := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(payload))
		if err != nil {
			return retry.NonRetryable(fmt.Errorf("creating request: %w", err))
		}

		if err := c.auth.SetAuth(req); err != nil {
			return retry.NonRetryable(fmt.Errorf("setting auth: %w", err))
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return retry.Retryable(fmt.Errorf("making request: %w", err))
		}
		defer func() {
			if err := resp.Body.Close(); err != nil {
				c.logger.Debug("Failed to close JIRA response body",
					"error", err,
					"issue", issueKey)
			}
		}()

		if resp.StatusCode == http.StatusNoContent {
			return nil
		}
		return classifyResponse(resp, issueKey)
	}, func(err error, backoff time.Duration) {
		c.logger.Warn("Retrying JIRA update",
			"error", err,
			"backoff", backoff,
			"issue", issueKey)
	})
}

// ExtractDescription extracts plain text from a JIRA description field,
// handling both plain strings and Atlassian Document Format (ADF).
func ExtractDescription(description any) string {
	if description == nil {
		return ""
	}

	if str, ok := description.(string); ok {
		return str
	}

	var sb strings.Builder
	needSpace := false
	var walk func(any)
	walk = func(v any) {
		if v == nil {
			return
		}
		if m, ok := v.(map[string]any); ok {
			if text, ok := m["text"].(string); ok && text != "" {
				if needSpace {
					sb.WriteByte(' ')
				}
				sb.WriteString(text)
				needSpace = true
			}
			if content, ok := m["content"].([]any); ok {
				for _, item := range content {
					walk(item)
				}
			}
		} else if arr, ok := v.([]any); ok {
			for _, item := range arr {
				walk(item)
			}
		}
	}
	walk(description)
	return sb.String()
}
