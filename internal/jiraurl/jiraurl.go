package jiraurl

import (
	"fmt"
	"net/url"
	"strings"
)

// Normalize validates a Jira base URL and returns the canonical host-only form.
func Normalize(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("JIRA URL is required")
	}

	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid JIRA URL: %w", err)
	}

	if parsed.Scheme != "https" {
		return "", fmt.Errorf("JIRA URL must use https")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("JIRA URL must include a host")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("JIRA URL must not include user credentials")
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("JIRA URL must not include a query string")
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("JIRA URL must not include a fragment")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return "", fmt.Errorf("JIRA URL must not include a path")
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}

func SameHost(a, b string) bool {
	aURL, err := url.Parse(a)
	if err != nil {
		return false
	}
	bURL, err := url.Parse(b)
	if err != nil {
		return false
	}

	return strings.EqualFold(aURL.Host, bURL.Host)
}
