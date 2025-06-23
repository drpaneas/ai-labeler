# JIRA Ticket Labeler with LocalAI

This project automatically applies appropriate labels to JIRA tickets based on their content using AI analysis. It uses LocalAI as the LLM provider for local inference instead of relying on OpenAI's cloud services.

## Features

- Automatically reviews JIRA tickets without labels
- Uses AI to analyze ticket content and suggest appropriate labels
- Works with LocalAI for fully local/on-premises inference
- Applies selected labels directly to JIRA tickets

## Prerequisites

- Go 1.18 or later
- A running LocalAI instance (see [LocalAI documentation](https://localai.io/basics/getting_started/))
- JIRA API access token

## Setup LocalAI

1. Install and run LocalAI following the [official documentation](https://localai.io/basics/getting_started/).

2. Start a LocalAI server with a model that supports chat completions. For example:

```bash
# Using Docker
docker run -ti --name local-ai -p 8080:8080 localai/localai:latest-cpu

# Or using the installer
curl https://localai.io/install.sh | sh
local-ai run llama-3.2-1b-instruct:q4_k_m  # Or any other compatible model
```

3. Ensure your model is configured for chat completions and is running correctly.

## Configuration

The application requires the following environment variables:

```bash
# Set your JIRA API token
export JIRA_API_TOKEN=your_jira_api_token_here

# Set LocalAI endpoint (defaults to http://localhost:8080/v1 if not set)
export LOCALAI_API_URL=http://localhost:8080/v1
```

## Compatible LLMs

LocalAI supports various models that can be used with this application:

- Llama-based models (llama-3.2-1b-instruct, llama2, etc.)
- Mistral models
- Phi-2
- Gemma
- Any other model LocalAI supports that can handle chat completions

Choose a model that offers good performance on classification tasks. Smaller models like Phi-2, Gemma 2B, or Llama-3.2-1b-instruct may be sufficient for this text classification use case.

## Running the Application

```bash
go run main.go
```

The application will:
1. Query JIRA tickets within the specified range
2. Check if they have labels from our predefined set
3. For tickets without appropriate labels, use LocalAI to analyze content and suggest a label
4. Apply the suggested label to the JIRA ticket

## Customizing Labels

These labels are used for Developer Sandbox

The current set of labels the system works with includes:
- quality - for tests, QA, QE, end-to-end or performance testing
- feature - for new tasks and feature implementations
- support - for tasks involving direct communication
- maintenance - for routine upkeep and automation
- alerts - for alert management tasks
- security - for security-related tasks
- aws-abuse - for AWS abuse reports

To modify these labels, edit the `labelInfo` variable in the `run()` function.

## Troubleshooting

- **LocalAI Connection Issues**: Ensure your LocalAI instance is running and accessible at the configured URL.
- **Model Selection**: If you're getting poor results, try using a different model with LocalAI.
- **Resource Usage**: Smaller models will use fewer resources but may provide less accurate results.

