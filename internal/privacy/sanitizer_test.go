package privacy

import (
	"strings"
	"testing"
)

func TestSanitizeTicket_RedactedMode(t *testing.T) {
	result := SanitizeTicket(
		"Contact alice@example.com about token=supersecret123",
		"See https://jira.example.com from 10.0.0.1 with aws-access-key-123456789 and password=hunter22",
		Policy{Mode: "redacted", MaxDescriptionChars: 200},
	)

	if strings.Contains(result.Summary, "alice@example.com") {
		t.Fatal("summary still contains email")
	}
	if strings.Contains(result.Summary, "supersecret123") {
		t.Fatal("summary still contains token")
	}
	if strings.Contains(result.Description, "https://jira.example.com") {
		t.Fatal("description still contains URL")
	}
	if strings.Contains(result.Description, "10.0.0.1") {
		t.Fatal("description still contains IP")
	}
	if strings.Contains(result.Description, "aws-access-key-123456789") {
		t.Fatal("description still contains AWS key")
	}
	if result.RedactionCount < 4 {
		t.Fatalf("RedactionCount = %d, want at least 4", result.RedactionCount)
	}
}

func TestSanitizeTicket_SummaryOnlyMode(t *testing.T) {
	result := SanitizeTicket("summary", "description", Policy{Mode: "summary_only", MaxDescriptionChars: 200})
	if result.Summary != "summary" {
		t.Errorf("Summary = %q, want %q", result.Summary, "summary")
	}
	if result.Description != "" {
		t.Errorf("Description = %q, want empty", result.Description)
	}
}

func TestSanitizeTicket_FullModeStillTruncates(t *testing.T) {
	result := SanitizeTicket("summary", "1234567890", Policy{Mode: "full", MaxDescriptionChars: 5})
	if !result.DescriptionCut {
		t.Fatal("expected description truncation")
	}
	if result.Description != "12345 [TRUNCATED]" {
		t.Errorf("Description = %q, want truncated output", result.Description)
	}
}
