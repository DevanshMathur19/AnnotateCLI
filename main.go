package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const MaxSummaryFileBytes = 64 * 1024 // 64KB limit for a single summary file

// New on-disk structure:
// {
//   "annotations": [ { ... per-context annotation ... } ],
//   "planExecutionId": "...",
//   "stageExecutionId": "...",
//   "created_at": "...",
//   "account": "...",
//   "project": "...",
//   "org": "...",
//   "pipeline": "..."
// }

type AnnotationEntry struct {
	ContextName string `json:"context_name"`
	Timestamp   string `json:"timestamp"`
	Style       string `json:"style"`
	Summary     string `json:"summary"`
	SummaryFile string `json:"summary_file"`
	Priority    int    `json:"priority"`
}

type AnnotationsEnvelope struct {
	Annotations      []AnnotationEntry `json:"annotations"`
	PlanExecutionId  string            `json:"planExecutionId"`
	StageExecutionId string            `json:"stageExecutionId"`
	CreatedAt        string            `json:"created_at"`
	Account          string            `json:"account"`
	Project          string            `json:"project"`
	Org              string            `json:"org"`
	Pipeline         string            `json:"pipeline"`
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

// get harness env needed for envelope metadata and messaging
func (c *CLI) getHarnessEnv() (account, project, org, pipeline, executionId, stageId, stageUuid, stepId string) {
	executionId = os.Getenv("HARNESS_EXECUTION_ID")
	stageId = os.Getenv("HARNESS_STAGE_ID")
	stageUuid = os.Getenv("HARNESS_STAGE_UUID")
	account = os.Getenv("HARNESS_ACCOUNT_ID")
	project = os.Getenv("HARNESS_PROJECT_ID")
	org = os.Getenv("HARNESS_ORG_ID")
	pipeline = os.Getenv("HARNESS_PIPELINE_ID")
	stepId = os.Getenv("HARNESS_STEP_ID")
	return
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

func (c *CLI) annotate(contextName, style, summaryFile string, priority int) (map[string]interface{}, error) {
	env, err := c.loadEnvelope()
	if err != nil {
		return nil, err
	}

	summary, err := c.readSummaryFile(summaryFile)
	if err != nil {
		return nil, err
	}

	account, project, org, pipeline, executionId, stageId, stageUuid, stepId := c.getHarnessEnv()

	// Initialize top-level metadata once (when file is new or fields are empty)
	if env.Account == "" {
		env.Account = account
	}
	if env.Project == "" {
		env.Project = project
	}
	if env.Org == "" {
		env.Org = org
	}
	if env.Pipeline == "" {
		env.Pipeline = pipeline
	}
	if env.PlanExecutionId == "" {
		env.PlanExecutionId = executionId
	}
	if env.StageExecutionId == "" {
		if stageUuid != "" {
			env.StageExecutionId = stageUuid
		} else {
			env.StageExecutionId = stageId
		}
	}
	if env.CreatedAt == "" {
		env.CreatedAt = time.Now().Format(time.RFC3339)
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
		})
	} else {
		// Merge into existing entry
		entry := env.Annotations[idx]
		if style != "" && entry.Style != style {
			// Replace semantics when style changes
			entry.Style = style
			entry.Summary = summary
		} else if summary != "" {
			if entry.Summary != "" {
				entry.Summary += "\n" + summary
			} else {
				entry.Summary = summary
			}
		}
		if priority > 0 {
			entry.Priority = priority
		}
		if summaryFile != "" {
			entry.SummaryFile = summaryFile
		}
		entry.Timestamp = time.Now().Format(time.RFC3339)
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
		os.Exit(1)
	}

	command := os.Args[1]

	if command != "annotate" {
		fmt.Printf("Usage: %s annotate [flags]\n", prog)
		fmt.Println("Available commands: annotate")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("annotate", flag.ExitOnError)
	context := fs.String("context", "", "Context of the step (used as ID) - required")
	style := fs.String("style", "", "Style for the annotation (replace)")
	summary := fs.String("summary", "", "Path to summary file (markdown content to append)")
	priority := fs.Int("priority", 0, "Priority level (replace, 0 means no change for existing steps)")

	fs.Parse(os.Args[2:])

	if *context == "" {
		fmt.Println("Error: --context is required")
		fs.Usage()
		os.Exit(1)
	}

	cli := NewCLI()
	result, err := cli.annotate(*context, *style, *summary, *priority)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	resultJSON, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(resultJSON))
}
