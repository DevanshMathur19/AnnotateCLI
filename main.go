package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const MaxSummaryFileBytes = 64 * 1024 // 64KB limit for a single summary file

// On-disk structure (annotations-only):
// {
//   "annotations": [
//     {
//       "context_name": "build",
//       "timestamp": "RFC3339",
//       "style": "info|success|warning|error",
//       "summary": "markdown...",
//       "summary_file": "path (echo)",
//       "priority": 0,
//       "mode": "append|replace|delete" // optional; defaults to append at engine side if omitted
//     }
//   ]
// }

type AnnotationEntry struct {
	ContextName string `json:"context_name"`
	Timestamp   string `json:"timestamp"`
	Style       string `json:"style"`
	Summary     string `json:"summary"`
	SummaryFile string `json:"summary_file"`
	Priority    int    `json:"priority"`
	Mode        string `json:"mode,omitempty"`
}

type AnnotationsEnvelope struct {
	PlanExecutionID string            `json:"planExecutionId,omitempty"`
	Annotations     []AnnotationEntry `json:"annotations"`
}

type CLI struct {
	annotationsFile string
}

func NewCLI() *CLI {
	outputPath := os.Getenv("HARNESS_ANNOTATIONS_FILE")
	if outputPath == "" {
		outputPath = "annotations.json"
	}
	return &CLI{
		annotationsFile: outputPath,
	}
}

func (c *CLI) loadEnvelope() (AnnotationsEnvelope, error) {
	env := AnnotationsEnvelope{}

	if _, err := os.Stat(c.annotationsFile); os.IsNotExist(err) {
		return env, nil
	}

	data, err := os.ReadFile(c.annotationsFile)
	if err != nil {
		return env, err
	}

	if len(data) == 0 {
		return env, nil
	}

	if err := json.Unmarshal(data, &env); err != nil {
		return env, fmt.Errorf("invalid annotations file format: %w", err)
	}
	return env, nil
}

func (c *CLI) saveEnvelope(env AnnotationsEnvelope) error {
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(c.annotationsFile)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("failed to create parent dir: %w", err)
		}
	}

	// Atomic write pattern: write to tmp and then rename to final
	tmp := c.annotationsFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := os.Rename(tmp, c.annotationsFile); err != nil {
		// On Windows, rename may fail if destination exists. Try removing and renaming again.
		_ = os.Remove(c.annotationsFile)
		if err2 := os.Rename(tmp, c.annotationsFile); err2 != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("failed to finalize write: %w", err2)
		}
	}
	return nil
}

// minimal harness env for messaging only
func (c *CLI) getStepID() string {
	return os.Getenv("HARNESS_STEP_ID")
}

func (c *CLI) getPlanExecutionID() string {
	return os.Getenv("HARNESS_EXECUTION_ID")
}

func (c *CLI) readSummaryFile(filePath string) (string, error) {
	if filePath == "" {
		return "", nil
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to stat summary file '%s': %v", filePath, err)
	}
	if info.Size() > MaxSummaryFileBytes {
		return "", fmt.Errorf("summary file '%s' exceeds %d bytes (64KB) with size %d bytes", filePath, MaxSummaryFileBytes, info.Size())
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read summary file '%s': %v", filePath, err)
	}

	return string(data), nil
}

func (c *CLI) annotate(contextName, style, summaryFile, mode string, priority int) (map[string]interface{}, error) {
	env, err := c.loadEnvelope()
	if err != nil {
		return nil, err
	}

	// Ensure planExecutionId is present at the root for lite-engine to post annotations
	if strings.TrimSpace(env.PlanExecutionID) == "" {
		if pe := c.getPlanExecutionID(); strings.TrimSpace(pe) != "" {
			env.PlanExecutionID = pe
		}
	}

	summary, err := c.readSummaryFile(summaryFile)
	if err != nil {
		return nil, err
	}

	stepId := c.getStepID()

	// Normalize mode
	switch mode {
	case "replace", "append", "delete":
		// ok
	case "":
		mode = "replace"
	default:
		// unknown -> default to replace
		mode = "replace"
	}

	// Find existing entry for this context
	idx := -1
	for i := range env.Annotations {
		if env.Annotations[i].ContextName == contextName {
			idx = i
			break
		}
	}

	if idx == -1 {
		// New context entry
		env.Annotations = append(env.Annotations, AnnotationEntry{
			ContextName: contextName,
			Timestamp:   time.Now().Format(time.RFC3339),
			Style:       style,
			Summary:     summary,
			SummaryFile: summaryFile,
			Priority:    priority,
			Mode:        mode,
		})
	} else {
		// Merge into existing entry based on mode
		entry := env.Annotations[idx]
		entry.Timestamp = time.Now().Format(time.RFC3339)
		if mode == "delete" {
			// mark as delete; content not needed
			entry.Mode = "delete"
			entry.Summary = ""
			entry.Style = ""
			entry.Priority = 0
		} else if mode == "replace" {
			if style != "" {
				entry.Style = style
			}
			entry.Summary = summary
			entry.Mode = "replace"
			if priority > 0 {
				entry.Priority = priority
			}
			if summaryFile != "" {
				entry.SummaryFile = summaryFile
			}
		} else { // append
			if style != "" {
				entry.Style = style
			}
			if summary != "" {
				if entry.Summary != "" {
					entry.Summary += "\n" + summary
				} else {
					entry.Summary = summary
				}
			}
			entry.Mode = "append"
			if priority > 0 {
				entry.Priority = priority
			}
			if summaryFile != "" {
				entry.SummaryFile = summaryFile
			}
		}
		env.Annotations[idx] = entry
	}

	if err := c.saveEnvelope(env); err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"context": contextName,
		"stepid":  stepId,
		"message": fmt.Sprintf("Annotation stored for context '%s' with step ID '%s'", contextName, stepId),
	}
	return result, nil
}

func main() {
	prog := filepath.Base(os.Args[0])
	if len(os.Args) < 2 {
		fmt.Printf("Usage: %s annotate [flags]\n", prog)
		// Non-fatal for pipelines
		os.Exit(0)
	}

	command := os.Args[1]

	if command != "annotate" {
		fmt.Printf("Usage: %s annotate [flags]\n", prog)
		fmt.Println("Available commands: annotate")
		os.Exit(0)
	}

	fs := flag.NewFlagSet("annotate", flag.ContinueOnError)
	// suppress default usage output on parse errors; we'll control messaging
	fs.SetOutput(io.Discard)
	context := fs.String("context", "", "Context of the step (used as ID) - required")
	style := fs.String("style", "", "Annotation style (info|success|warning|error)")
	summary := fs.String("summary", "", "Path to summary file (markdown content)")
	mode := fs.String("mode", "replace", "Annotation mode (append|replace|delete). Optional; defaults to replace")
	priority := fs.Int("priority", 0, "Annotation priority (int). Optional")

	if err := fs.Parse(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "[ANN_CLI] warning: failed to parse flags: %v\n", err)
		os.Exit(0)
	}

	if *context == "" {
		fmt.Fprintln(os.Stderr, "[ANN_CLI] warning: --context is required")
		os.Exit(0)
	}

	cli := NewCLI()
	result, err := cli.annotate(*context, *style, *summary, *mode, *priority)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ANN_CLI] warning: %v\n", err)
		os.Exit(0)
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(resultJSON))
}
