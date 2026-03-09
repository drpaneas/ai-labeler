# ai-labeler

`ai-labeler` labels JIRA tickets with help from an LLM.
It reads ticket text, chooses one label from your config, and writes it back.
Supported providers are OpenAI, Gemini, and Claude.

## Why this tool

In projects that track work by type (feature, maintenance, quality, support, and so on), unlabeled tickets break dashboards and metrics. This tool keeps labels consistent at scale by having an LLM pick the right label from your definitions.

## Requirements

- Go 1.26.1+
- JIRA Cloud URL and project key
- `JIRA_EMAIL`
- `JIRA_API_TOKEN`
- one of `OPENAI_API_KEY`, `GOOGLE_API_KEY`, `ANTHROPIC_API_KEY`

Auth mode:
- Jira Cloud uses Basic auth with `JIRA_EMAIL` and `JIRA_API_TOKEN`

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

LLM settings:
- `llm.provider` accepts `openai`, `gemini`, `googleai`, `claude`, or `anthropic`
- `llm.model` may be any model name supported by the selected provider
- if `llm.model` is omitted, the defaults are:
- `openai` -> `gpt-4`
- `gemini` or `googleai` -> `gemini-2.5-flash`
- `claude` or `anthropic` -> `claude-3-opus-20240229`

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

LLM content handling:
- `llm.ticket_content_mode` defaults to `redacted`
- `llm.max_description_chars` defaults to `4000`

## Run

One ticket:

```bash
./ai-labeler --ticket 105 --dry-run
```

Range:

```bash
./ai-labeler --start 100 --end 200 --workers 5 --write
```

Dry run:

```bash
./ai-labeler --start 100 --end 105 --dry-run
```

One mode flag is required: `--dry-run` or `--write`.

Useful flags: `--config`, `--project`, `--verbose`, `--json-log`, `--version`.

## Checks

```bash
make check
go test -race ./...
```

