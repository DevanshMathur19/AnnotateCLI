package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type ContextData struct {
	Timestamp        string `json:"timestamp"`
	Style            string `json:"style"`
	Summary          string `json:"summary"`
	SummaryFile      string `json:"summary_file"`
	Priority         int    `json:"priority"`
	PlanExecutionId  string `json:"planExecutionId"`
	StageExecutionId string `json:"stageExecutionId"`
}

type ExecutionContext struct {
	ExecutionID string `json:"execution_id"`
	StepID      string `json:"harness_step_id"`
	Account     string `json:"account"`
	Project     string `json:"project"`
	Org         string `json:"org"`
	Pipeline    string `json:"pipeline"`
	Stage       string `json:"stage"`
}

type Annotation struct {
	CreatedAt string           `json:"created_at"`
	Context   ExecutionContext `json:"execution_context"`
	Data      ContextData      `json:"data"`
}

type AnnotationStore map[string]Annotation

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

func (c *CLI) loadAnnotations() (AnnotationStore, error) {
	store := make(AnnotationStore)

	if _, err := os.Stat(c.annotationsFile); os.IsNotExist(err) {
		return store, nil
	}

	data, err := os.ReadFile(c.annotationsFile)
	if err != nil {
		return nil, err
	}

	if len(data) == 0 {
		return store, nil
	}

	err = json.Unmarshal(data, &store)
	if err != nil {
		return nil, err
	}

	return store, nil
}

func (c *CLI) saveAnnotations(store AnnotationStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
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

func (c *CLI) getExecutionContext() (ExecutionContext, string, string) {
	executionId := os.Getenv("HARNESS_EXECUTION_ID")
	stageId := os.Getenv("HARNESS_STAGE_ID")
	stageUuid := os.Getenv("HARNESS_STAGE_UUID")

	stageExecutionId := stageUuid
	if stageExecutionId == "" {
		stageExecutionId = stageId
	}

	context := ExecutionContext{
		ExecutionID: executionId,
		StepID:      os.Getenv("HARNESS_STEP_ID"),
		Account:     os.Getenv("HARNESS_ACCOUNT_ID"),
		Project:     os.Getenv("HARNESS_PROJECT_ID"),
		Org:         os.Getenv("HARNESS_ORG_ID"),
		Pipeline:    os.Getenv("HARNESS_PIPELINE_ID"),
		Stage:       stageId,
	}

	return context, executionId, stageExecutionId
}

func (c *CLI) readSummaryFile(filePath string) (string, error) {
	if filePath == "" {
		return "", nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read summary file '%s': %v", filePath, err)
	}

	return string(data), nil
}

func (c *CLI) annotate(context, style, summaryFile string, priority int) (map[string]interface{}, error) {
	store, err := c.loadAnnotations()
	if err != nil {
		return nil, err
	}

	summary, err := c.readSummaryFile(summaryFile)
	if err != nil {
		return nil, err
	}

	execContext, planExecId, stageExecId := c.getExecutionContext()

	annotation, exists := store[context]
	if !exists {
		annotation = Annotation{
			CreatedAt: time.Now().Format(time.RFC3339),
			Context:   execContext,
			Data: ContextData{
				Timestamp:        time.Now().Format(time.RFC3339),
				Style:            style,
				Summary:          summary,
				SummaryFile:      summaryFile,
				Priority:         priority,
				PlanExecutionId:  planExecId,
				StageExecutionId: stageExecId,
			},
		}
	} else {
		annotation.Context = execContext

		if style != "" && annotation.Data.Style != style {
			annotation.Data.Summary = summary
			annotation.Data.Style = style
		} else if summary != "" {
			if annotation.Data.Summary != "" {
				annotation.Data.Summary += "\n" + summary
			} else {
				annotation.Data.Summary = summary
			}
		}

		if priority > 0 {
			annotation.Data.Priority = priority
		}
		if summaryFile != "" {
			annotation.Data.SummaryFile = summaryFile
		}
		annotation.Data.Timestamp = time.Now().Format(time.RFC3339)
		annotation.Data.PlanExecutionId = planExecId
		annotation.Data.StageExecutionId = stageExecId
	}

	store[context] = annotation

	err = c.saveAnnotations(store)
	if err != nil {
		return nil, err
	}

	result := map[string]interface{}{
		"context": context,
		"stepid":  execContext.StepID,
		"message": fmt.Sprintf("Annotation stored for context '%s' with step ID '%s'", context, execContext.StepID),
	}

	return result, nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: cli annotate [flags]")
		os.Exit(1)
	}

	command := os.Args[1]

	if command != "annotate" {
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
