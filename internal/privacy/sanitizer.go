package privacy

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

type Policy struct {
	Mode                string
	MaxDescriptionChars int
}

type SanitizedTicket struct {
	Summary        string
	Description    string
	RedactionCount int
	DescriptionCut bool
}

var redactionPatterns = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`), "[REDACTED_EMAIL]"},
	{regexp.MustCompile(`https?://[^\s]+`), "[REDACTED_URL]"},
	{regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`), "[REDACTED_IP]"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "[REDACTED_AWS_ACCESS_KEY]"},
	{regexp.MustCompile(`(?i)\b(?:bearer|token|api[_-]?key|secret|password)\b\s*[:=]\s*[A-Za-z0-9_\-./+=]{8,}`), "[REDACTED_SECRET]"},
}

func SanitizeTicket(summary, description string, policy Policy) SanitizedTicket {
	if policy.MaxDescriptionChars <= 0 {
		policy.MaxDescriptionChars = 4000
	}

	result := SanitizedTicket{
		Summary:     strings.TrimSpace(summary),
		Description: strings.TrimSpace(description),
	}

	switch policy.Mode {
	case "summary_only":
		result.Description = ""
	case "full":
		// Keep original content, but still apply size limits below.
	default:
		result.Summary, result.RedactionCount = redact(result.Summary, result.RedactionCount)
		result.Description, result.RedactionCount = redact(result.Description, result.RedactionCount)
	}

	result.Description, result.DescriptionCut = truncate(result.Description, policy.MaxDescriptionChars)
	return result
}

func redact(input string, count int) (string, int) {
	output := input
	for _, pattern := range redactionPatterns {
		matches := pattern.re.FindAllStringIndex(output, -1)
		if len(matches) == 0 {
			continue
		}
		count += len(matches)
		output = pattern.re.ReplaceAllString(output, pattern.replacement)
	}
	return redactLongSecrets(output, count)
}

func truncate(input string, maxChars int) (string, bool) {
	if maxChars <= 0 || utf8.RuneCountInString(input) <= maxChars {
		return input, false
	}

	runes := []rune(input)
	return string(runes[:maxChars]) + " [TRUNCATED]", true
}

func redactLongSecrets(input string, count int) (string, int) {
	words := strings.Fields(input)
	for i, word := range words {
		candidate := strings.Trim(word, `"'()[]{}<>,.;:`)
		if !looksLikeSecret(candidate) {
			continue
		}
		words[i] = strings.Replace(word, candidate, "[REDACTED_SECRET]", 1)
		count++
	}
	return strings.Join(words, " "), count
}

func looksLikeSecret(candidate string) bool {
	if len(candidate) < 24 {
		return false
	}

	hasLetter := false
	hasDigit := false
	for _, r := range candidate {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '/', r == '_', r == '+', r == '=', r == '-':
		default:
			return false
		}
	}

	return hasLetter && hasDigit
}
