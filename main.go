package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/memory"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	// Define command line flags
	projectKey := flag.String("project", "SANDBOX", "JIRA project key")
	startTicket := flag.Int("start", 150, "Starting ticket number")
	endTicket := flag.Int("end", 200, "Ending ticket number")

	// Parse command line flags
	flag.Parse()

	// Validate inputs
	if *startTicket > *endTicket {
		fmt.Fprintf(os.Stderr, "Error: start ticket (%d) cannot be greater than end ticket (%d)\n", *startTicket, *endTicket)
		os.Exit(1)
	}

	if err := run(*projectKey, *startTicket, *endTicket); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(projectKey string, startTicket, endTicket int) error {
	// Get the LocalAI API endpoint from the environment variable or use default
	localAIURL := os.Getenv("LOCALAI_API_URL")
	if localAIURL == "" {
		localAIURL = "http://localhost:8080/v1" // Default LocalAI endpoint
	}

	// Initialize the LLM with LocalAI endpoint
	llm, err := openai.New(
		openai.WithBaseURL(localAIURL),
		openai.WithToken(""), // LocalAI may not require a token, or you can set it via OPENAI_API_KEY env var
	)
	if err != nil {
		return err
	}

	// Initialize the Jira issue URL template
	urlTemplate := "https://issues.redhat.com/rest/api/2/issue/%s-%d"

	conversationBuffer := memory.NewConversationBuffer()
	llmChain := chains.NewConversation(llm, conversationBuffer)
	ctx := context.Background()
	labelInfo := "I have the following JIRA labels: quality, feature, support, maintenance, alerts, security, aws-abuse\nquality label is used for tests, qa, qe and other end to end or performance test and benchmarks\nfeature label is used for new tasks, implementing new features such as onboarding new operators in Sandbox\nsupport label is used for tasks that involve direct communication—sending emails, chatting on Slack, or handling inquiries.\nmaintenance label is used for routine upkeep such as updating automation scripts in your SRE repository, ArgoCD, and other scheduled toil work typical for SRE teams.\nalerts label is used for all tasks related to alert management—reviewing, fixing, adding, or deleting alerts. Including SOPs for these alerts.\nsecurity label is used for banning, vulnerabili(ty assessments, CVEs, and other security-related tasks.\naws-abuse label is used for AWS abuse report e-mails specifically, which is a unique case."

	// Query the JIRA issues to find out if we need a label or not
	for i := startTicket; i <= endTicket; i++ {
		url := fmt.Sprintf(urlTemplate, projectKey, i)

		summary, description, labels := queryJIRA(url)
		if summary == "" && description == "" {
			fmt.Println("No JIRA ticket found")
			continue
		}

		// Check if there are any labels
		shouldAddLabels := false
		if len(labels) == 0 {
			fmt.Println("No labels found")
			shouldAddLabels = true
		} else {
			// Since there are labels found, check if the labels are any of the labels we have
			// and if yes then we do not need to add any labels
			for _, label := range labels {
				shouldAddLabels = true
				if strings.EqualFold(label, "quality") || strings.EqualFold(label, "support") || strings.EqualFold(label, "feature") || strings.EqualFold(label, "maintenance") || strings.EqualFold(label, "alerts") || strings.EqualFold(label, "security") || strings.EqualFold(label, "aws-abuse") {
					shouldAddLabels = false
					break
				}
			}
		}

		if shouldAddLabels {
			req := fmt.Sprintf("I need to create a JIRA ticket with the following information:\nSummary: %s\nDescription: %s\nLabels: %s\n. What label would you chose? Please tell me only the name of the label, nothing else. Just one word.", summary, description, labelInfo)

			out, err := chains.Run(ctx, llmChain, req)
			if err != nil {
				return err
			}

			fmt.Println("Output from llm: ", out)
			label := extractLabel(out)
			fmt.Println("Label chosen by llm: ", label)
			if label == "" {
				fmt.Println("No label found by llm")
				continue
			} else {
				fmt.Printf("Adding label %s to %s\n", label, url)
				// Add the new label to the existing labels
				labels = append(labels, label)
				// Update the JIRA issue with the new labels
				err := updateJIRALabels(url, labels)
				if err != nil {
					return err
				}
				fmt.Printf("Added label %s to %s\n", label, url)
			}
		} else {
			fmt.Printf("No need to add labels for %s, because we found the following labels: %v\n", url, labels)
		}
	}

	return nil
}

func extractLabel(output string) string {
	// Convert the output to lowercase
	output = strings.ToLower(output)

	// Check if the label is one of the specified labels
	validLabels := []string{"quality", "feature", "support", "maintenance", "alerts", "security", "aws-abuse"}
	for _, validLabel := range validLabels {
		if strings.Contains(output, validLabel) {
			return validLabel
		}
	}

	// Return an empty string if no valid label is found
	return ""
}

// Issue represents the structure of the JSON response we're interested in.
type Issue struct {
	Fields struct {
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Labels      []string `json:"labels"`
	} `json:"fields"`
}

func queryJIRA(url string) (summary, description string, labels []string) {
	// Get the Jira API token from the environment.
	token := os.Getenv("JIRA_API_TOKEN")
	if token == "" {
		log.Fatal("JIRA_API_TOKEN environment variable not set")
	}

	// Create a new GET request.
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		log.Fatalf("Error creating request: %v", err)
	}

	// Set the Authorization header.
	req.Header.Set("Authorization", "Bearer "+token)

	// Execute the HTTP request.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Error making HTTP request: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("Error closing response body: %v", err)
		}
	}()

	// Check if the response status is OK.
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("Request failed with status: %s", resp.Status)
	}

	// Read the response body.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalf("Error reading response body: %v", err)
	}

	// Parse the JSON response.
	var issue Issue
	if err := json.Unmarshal(body, &issue); err != nil {
		log.Fatalf("Error parsing JSON: %v", err)
	}

	// Print the summary field.
	return issue.Fields.Summary, issue.Fields.Description, issue.Fields.Labels
}

func updateJIRALabels(url string, labels []string) error {

	// Get the Jira API token from the environment.
	token := os.Getenv("JIRA_API_TOKEN")
	if token == "" {
		log.Fatal("JIRA_API_TOKEN environment variable not set")
	}

	// Create the payload with the new labels.
	payload := map[string]interface{}{
		"fields": map[string]interface{}{
			"labels": labels,
		},
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("error marshaling payload: %v", err)
	}

	// Create a new PUT request.
	req, err := http.NewRequest("PUT", url, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return fmt.Errorf("error creating request: %v", err)
	}

	// Set the headers.
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	// Execute the HTTP request.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error making HTTP request: %v", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("error closing response body: %v", err)
		}
	}()

	// Check if the response status is OK.
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("request failed with status: %s", resp.Status)
	}

	return nil
}
