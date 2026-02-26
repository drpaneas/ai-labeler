#!/bin/bash

# Example script to demonstrate ai-labeler usage
# This script shows various ways to use the ai-labeler tool

set -e

echo "=== AI Labeler Example Script ==="
echo

# Check if binary exists
if [ ! -f "./ai-labeler" ]; then
    echo "Building ai-labeler..."
    make build
fi

# Check for required environment variables
if [ -z "$JIRA_EMAIL" ] || [ -z "$JIRA_API_TOKEN" ]; then
    echo "Error: JIRA_EMAIL and JIRA_API_TOKEN environment variables must be set"
    echo "Example:"
    echo "  export JIRA_EMAIL=your-email@example.com"
    echo "  export JIRA_API_TOKEN=your-api-token"
    exit 1
fi

# Check for LLM API key
if [ -z "$GOOGLE_API_KEY" ] && [ -z "$OPENAI_API_KEY" ] && [ -z "$ANTHROPIC_API_KEY" ]; then
    echo "Error: At least one LLM API key must be set"
    echo "Set one of: GOOGLE_API_KEY, OPENAI_API_KEY, or ANTHROPIC_API_KEY"
    exit 1
fi

# Use example config if no config.json exists
CONFIG_FILE="config.json"
if [ ! -f "$CONFIG_FILE" ]; then
    echo "No config.json found, using config-example.json"
    CONFIG_FILE="config-example.json"
fi

echo "Using configuration: $CONFIG_FILE"
echo

# Example 1: Show version
echo "1. Showing version information:"
./ai-labeler --version
echo

# Example 2: Show help
echo "2. Showing help:"
./ai-labeler --help
echo

# Example 3: Dry run on a single ticket
echo "3. Dry run on a single ticket (no changes will be made):"
echo "   Command: ./ai-labeler --config $CONFIG_FILE --ticket 1 --dry-run --verbose"
./ai-labeler --config "$CONFIG_FILE" --ticket 1 --dry-run --verbose || {
    echo "Note: This may fail if ticket 1 doesn't exist in your JIRA project"
}
echo

# Example 4: Process a range with multiple workers
echo "4. Process a range of tickets with concurrent workers (dry-run):"
echo "   Command: ./ai-labeler --config $CONFIG_FILE --start 1 --end 5 --workers 3 --dry-run"
read -p "Press enter to continue (or Ctrl+C to stop)..."
./ai-labeler --config "$CONFIG_FILE" --start 1 --end 5 --workers 3 --dry-run || {
    echo "Note: Some tickets may not exist in your JIRA project"
}
echo

# Example 5: Real run (with confirmation)
echo "5. Process a ticket for real (this will modify JIRA):"
echo "   Command: ./ai-labeler --config $CONFIG_FILE --ticket 1"
read -p "Do you want to run this command? (y/N) " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    ./ai-labeler --config "$CONFIG_FILE" --ticket 1 || {
        echo "Note: This may fail if ticket 1 doesn't exist or already has labels"
    }
else
    echo "Skipped real run"
fi

echo
echo "=== Example completed ==="
echo
echo "Tips:"
echo "- Use --dry-run to preview changes without modifying JIRA"
echo "- Use --workers N for concurrent processing of large ranges"
echo "- Use --verbose for detailed logging"
echo "- Use --json-log for structured JSON output"
echo "- Check the README.md for more examples and documentation"
