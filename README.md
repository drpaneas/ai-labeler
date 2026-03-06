# ai-labeler

`ai-labeler` labels JIRA tickets with help from an LLM.
It reads ticket text, chooses one label from your config, and writes it back.
Supported providers are OpenAI, Gemini, and Claude.

## Why this tool

In projects that track work by type (feature, maintenance, quality, support, and so on), unlabeled tickets break dashboards and metrics. This tool keeps labels consistent at scale by having an LLM pick the right label from your definitions.

## Requirements

- Go 1.26+
- JIRA Cloud URL and project key
- `JIRA_API_TOKEN`
- one of `OPENAI_API_KEY`, `GOOGLE_API_KEY`, `ANTHROPIC_API_KEY`

Auth mode:
- if `JIRA_EMAIL` is set, use Basic auth
- otherwise, use Bearer auth with `JIRA_API_TOKEN`

## Build

```bash
make build
```

This produces `./ai-labeler`.

## Configure

Create `config.json` (see `config-example.json`):

```json
{
  "labels": [
    { "name": "quality", "description": "tests and QA work" },
    { "name": "feature", "description": "new functionality" }
  ],
  "jira": {
    "url": "https://your-domain.atlassian.net",
    "project": "YOUR_PROJECT"
  },
  "llm": {
    "provider": "gemini",
    "model": "gemini-2.5-flash"
  }
}
```

Rules:
- `labels` must not be empty
- label names must be unique
- each label needs a description

Optional overrides:
- `CONFIG_FILE`
- `JIRA_URL`
- `JIRA_PROJECT`
- `LLM_PROVIDER`
- `LLM_MODEL`

## Run

One ticket:

```bash
./ai-labeler --ticket 105
```

Range:

```bash
./ai-labeler --start 100 --end 200 --workers 5
```

Dry run:

```bash
./ai-labeler --start 100 --end 105 --dry-run
```

Useful flags: `--config`, `--project`, `--verbose`, `--json-log`, `--version`.

## Checks

```bash
make check
go test -race ./...
```

