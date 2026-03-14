package reducer

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"build-brief/internal/gradle"
	"build-brief/internal/runner"
)

var (
	taskFailurePattern   = regexp.MustCompile(`^> Task (.+) FAILED$`)
	taskExecutionPattern = regexp.MustCompile(`Execution failed for task '([^']+)'\.`)
	testFailurePattern   = regexp.MustCompile(`^(.+?) > (.+?) FAILED$`)
	ansiPattern          = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	maxWarnings          = 8
	maxImportantLines    = 12
	contextCaptureLines  = 2
)

type Summary struct {
	SchemaVersion   string   `json:"schema_version"`
	Tool            string   `json:"tool"`
	Success         bool     `json:"success"`
	ExitCode        int      `json:"exit_code"`
	Duration        string   `json:"duration"`
	DurationMs      int64    `json:"duration_ms"`
	ProjectDir      string   `json:"project_dir"`
	Executable      string   `json:"executable"`
	Command         []string `json:"command"`
	CommandLine     string   `json:"command_line"`
	Source          string   `json:"source"`
	RawLogPath      string   `json:"raw_log_path"`
	BuildStatusLine string   `json:"build_status_line"`
	FailedTasks     []string `json:"failed_tasks"`
	FailedTests     []string `json:"failed_tests"`
	WarningCount    int      `json:"warning_count"`
	Warnings        []string `json:"warnings"`
	ImportantLines  []string `json:"important_lines"`
	TotalLines      int      `json:"total_lines"`
}

func Reduce(command gradle.Command, result runner.Result) (Summary, error) {
	summary := Summary{
		SchemaVersion: "v1",
		Tool:          "build-brief",
		Success:       result.ExitCode == 0,
		ExitCode:      result.ExitCode,
		Duration:      formatDuration(result.Duration),
		DurationMs:    result.Duration.Milliseconds(),
		ProjectDir:    command.ProjectDir,
		Executable:    command.Executable,
		Command:       append([]string{command.Executable}, command.Args...),
		CommandLine: strings.Join(
			append([]string{filepath.Base(command.Executable)}, command.Args...),
			" ",
		),
		Source:         string(command.Source),
		RawLogPath:     result.RawLogPath,
		FailedTasks:    []string{},
		FailedTests:    []string{},
		Warnings:       []string{},
		ImportantLines: []string{},
	}

	failedTasks := make(map[string]struct{})
	failedTests := make(map[string]struct{})
	warnings := make(map[string]struct{})
	important := make(map[string]struct{})

	file, err := os.Open(result.RawLogPath)
	if err != nil {
		return Summary{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	captureContextRemaining := 0
	for scanner.Scan() {
		summary.TotalLines++
		text := normalizeLine(scanner.Text())
		if text == "" {
			continue
		}

		switch {
		case taskFailurePattern.MatchString(text):
			matches := taskFailurePattern.FindStringSubmatch(text)
			addUnique(&summary.FailedTasks, failedTasks, matches[1], 0)
		case taskExecutionPattern.MatchString(text):
			matches := taskExecutionPattern.FindStringSubmatch(text)
			addUnique(&summary.FailedTasks, failedTasks, matches[1], 0)
		case testFailurePattern.MatchString(text):
			matches := testFailurePattern.FindStringSubmatch(text)
			addUnique(&summary.FailedTests, failedTests, matches[1]+" > "+matches[2], 0)
		}

		if isWarningLine(text) {
			summary.WarningCount++
			addUnique(&summary.Warnings, warnings, text, maxWarnings)
		}

		if strings.HasPrefix(text, "BUILD SUCCESSFUL") || strings.HasPrefix(text, "BUILD FAILED") {
			summary.BuildStatusLine = text
		}

		if isImportantLine(text) {
			addUnique(&summary.ImportantLines, important, text, maxImportantLines)
		}

		if captureContextRemaining > 0 && shouldCaptureContextLine(text) {
			addUnique(&summary.ImportantLines, important, text, maxImportantLines)
			captureContextRemaining--
		}

		if opensContextCapture(text) {
			captureContextRemaining = contextCaptureLines
		}
	}

	if summary.BuildStatusLine == "" {
		if summary.Success {
			summary.BuildStatusLine = "BUILD SUCCESSFUL"
		} else {
			summary.BuildStatusLine = "BUILD FAILED"
		}
	}

	if len(summary.ImportantLines) == 0 {
		summary.ImportantLines = append(summary.ImportantLines, summary.BuildStatusLine)
	}

	if err := scanner.Err(); err != nil {
		return Summary{}, err
	}

	return summary, nil
}

func addUnique(items *[]string, seen map[string]struct{}, value string, limit int) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := seen[value]; ok {
		return
	}
	seen[value] = struct{}{}
	if limit == 0 || len(*items) < limit {
		*items = append(*items, value)
	}
}

func isWarningLine(text string) bool {
	lower := strings.ToLower(text)
	switch {
	case strings.HasPrefix(lower, "warning:"):
		return true
	case strings.Contains(lower, " warning:"):
		return true
	case strings.Contains(lower, "deprecated gradle features were used"):
		return true
	case strings.Contains(lower, "deprecation warning"):
		return true
	default:
		return false
	}
}

func isImportantLine(text string) bool {
	switch {
	case strings.HasPrefix(text, "FAILURE:"):
		return true
	case strings.HasPrefix(text, "* What went wrong:"):
		return true
	case strings.HasPrefix(text, "* Try:"):
		return true
	case strings.HasPrefix(text, "* Exception is:"):
		return true
	case strings.HasPrefix(text, "BUILD SUCCESSFUL"):
		return true
	case strings.HasPrefix(text, "BUILD FAILED"):
		return true
	case taskFailurePattern.MatchString(text):
		return true
	case taskExecutionPattern.MatchString(text):
		return true
	case testFailurePattern.MatchString(text):
		return true
	default:
		return false
	}
}

func opensContextCapture(text string) bool {
	return strings.HasPrefix(text, "* What went wrong:") ||
		strings.HasPrefix(text, "* Try:") ||
		strings.HasPrefix(text, "* Exception is:")
}

func shouldCaptureContextLine(text string) bool {
	if text == "" || isWarningLine(text) {
		return false
	}
	return !opensContextCapture(text)
}

func normalizeLine(text string) string {
	return strings.TrimSpace(ansiPattern.ReplaceAllString(text, ""))
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "0s"
	}
	return duration.Round(100 * time.Millisecond).String()
}
