# Annotation CLI

A CLI tool for creating and storing annotation properties in Harness CI/CD pipelines, similar to Buildkite's annotate functionality.

## Features

- **File-based summaries**: Reads markdown content from user-created files
- **Execution context**: Automatically captures Harness environment variables
- **Cross-platform**: Compatible with Linux, Mac, and Windows
- **Multi-step support**: Single annotations.json file for multiple pipeline steps
- **Append functionality**: Can append additional content to existing steps

## Usage

```bash
./cli annotate --context "context-name" --summary "summary-file.md" [options]
```

### Required Parameters

- `--context`: Context identifier (used as grouping key)
- `--summary`: Path to markdown file containing summary content

### Optional Parameters

- `--style`: Annotation style (replaces existing value)
- `--stepid`: Step identifier (auto-generated if not provided)
- `--priority`: Priority level (default: 5, replaces existing value)

### Environment Variables

The CLI automatically reads these Harness environment variables:

- `HARNESS_EXECUTION_ID`: Current execution ID
- `HARNESS_STEP_ID`: Current step ID
- `HARNESS_ACCOUNT_ID`: Account identifier
- `HARNESS_PROJECT_ID`: Project identifier

## Examples

### Basic annotation
```bash
./cli annotate --context "build-validation" --summary "test-results.md" --priority 8
```

### Update existing step (append summary)
```bash
./cli annotate --context "build-validation" --stepid "abc12345" --summary "additional-notes.md"
```

### With custom style
```bash
./cli annotate --context "deployment" --summary "deploy-results.md" --style "success" --priority 9
```

## Output Files

- **annotations.json**: Structured data containing all annotations and metadata
- User-created **.md files**: Summary content files (not modified by CLI)

## Build

```bash
go build -o cli main.go
```

For cross-platform builds:
```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o cli-linux main.go

# Windows
GOOS=windows GOARCH=amd64 go build -o cli-windows.exe main.go

# macOS
GOOS=darwin GOARCH=amd64 go build -o cli-macos main.go
```