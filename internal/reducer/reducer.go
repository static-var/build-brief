package reducer

import (
	"bufio"
	"encoding/xml"
	"io/fs"
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
	javacErrorPattern    = regexp.MustCompile(`^.+\.(java|groovy|scala):\d+(?::\d+)?: error: .+$`)
	ansiPattern          = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	maxWarnings          = 8
	maxImportantLines    = 12
	contextCaptureLines  = 2
	compilerCaptureLines = 3
	maxJUnitReportFiles  = 100
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
	RawOutputTokens int      `json:"raw_output_tokens"`
	EmittedTokens   int      `json:"emitted_output_tokens"`
	SavedTokens     int      `json:"saved_output_tokens"`
	SavingsPct      float64  `json:"savings_pct"`
	BuildStatusLine string   `json:"build_status_line"`
	FailedTasks     []string `json:"failed_tasks"`
	FailedTests     []string `json:"failed_tests"`
	WarningCount    int      `json:"warning_count"`
	Warnings        []string `json:"warnings"`
	ImportantLines  []string `json:"important_lines"`
	TotalLines      int      `json:"total_lines"`
}

type junitTestSuite struct {
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure"`
	Error     *junitFailure `xml:"error"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
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
	captureCompilerRemaining := 0
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

		if opensContextCapture(text) {
			captureContextRemaining = contextCaptureLines
		} else if captureContextRemaining > 0 && shouldCaptureContextLine(text) {
			addUnique(&summary.ImportantLines, important, text, maxImportantLines)
			captureContextRemaining--
		}

		if opensCompilerContext(text) {
			captureCompilerRemaining = compilerCaptureLines
		} else if captureCompilerRemaining > 0 && shouldCaptureCompilerContextLine(text) {
			addUnique(&summary.ImportantLines, important, text, maxImportantLines)
			captureCompilerRemaining--
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

	enrichWithJUnitFailures(command.ProjectDir, &summary, failedTests, important)

	return summary, nil
}

func enrichWithJUnitFailures(projectDir string, summary *Summary, failedTests, important map[string]struct{}) {
	if !shouldEnrichWithJUnit(summary) {
		return
	}

	reportFiles := findJUnitReportFiles(projectDir)
	for _, path := range reportFiles {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		var suite junitTestSuite
		if err := xml.Unmarshal(content, &suite); err != nil {
			continue
		}

		for _, testCase := range suite.TestCases {
			failure := testCase.Failure
			if failure == nil {
				failure = testCase.Error
			}
			if failure == nil {
				continue
			}

			failedTestName := formatJUnitFailedTest(testCase.ClassName, testCase.Name)
			addUnique(&summary.FailedTests, failedTests, failedTestName, 0)

			detail := buildJUnitFailureDetail(failedTestName, failure)
			addUnique(&summary.ImportantLines, important, detail, maxImportantLines)

			if location := extractRelevantStackFrame(failure.Body); location != "" {
				addUnique(&summary.ImportantLines, important, location, maxImportantLines)
			}
		}
	}
}

func findJUnitReportFiles(projectDir string) []string {
	reportFiles := make([]string, 0)
	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if shouldSkipJUnitWalkDir(path, entry) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(reportFiles) >= maxJUnitReportFiles {
			return fs.SkipAll
		}
		if isJUnitReportPath(path, entry.Name()) {
			reportFiles = append(reportFiles, path)
		}
		return nil
	})

	return reportFiles
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
	case opensCompilerContext(text):
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

func opensCompilerContext(text string) bool {
	switch {
	case javacErrorPattern.MatchString(text):
		return true
	case strings.HasPrefix(text, "e: ") && (strings.Contains(text, ".kt:") || strings.Contains(text, ".kts:")):
		return true
	default:
		return false
	}
}

func formatJUnitFailedTest(className, testName string) string {
	shortName := className
	if lastDot := strings.LastIndex(shortName, "."); lastDot >= 0 {
		shortName = shortName[lastDot+1:]
	}
	if shortName == "" {
		shortName = className
	}
	if shortName == "" {
		return testName
	}
	return shortName + " > " + testName
}

func buildJUnitFailureDetail(failedTestName string, failure *junitFailure) string {
	message := strings.TrimSpace(failure.Message)
	if message == "" {
		message = firstNonEmptyLine(failure.Body)
	}
	if message == "" {
		message = strings.TrimSpace(failure.Type)
	}
	if message == "" {
		return failedTestName
	}
	return failedTestName + ": " + message
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func extractRelevantStackFrame(stack string) string {
	for _, line := range strings.Split(stack, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "at ") {
			continue
		}
		if strings.Contains(line, "org.junit.") || strings.Contains(line, "org.gradle.") || strings.Contains(line, "java.base/") {
			continue
		}
		return line
	}
	return ""
}

func shouldEnrichWithJUnit(summary *Summary) bool {
	if len(summary.FailedTests) > 0 {
		return true
	}

	for _, task := range summary.FailedTasks {
		lower := strings.ToLower(task)
		if strings.Contains(lower, "test") {
			return true
		}
	}

	return false
}

func shouldSkipJUnitWalkDir(path string, entry fs.DirEntry) bool {
	name := entry.Name()
	switch name {
	case ".git", ".gradle", ".idea", ".vscode", "node_modules":
		return true
	default:
		return false
	}
}

func isJUnitReportPath(path, fileName string) bool {
	if !strings.HasPrefix(fileName, "TEST-") || !strings.HasSuffix(fileName, ".xml") {
		return false
	}

	path = filepath.ToSlash(path)
	return strings.Contains(path, "/build/test-results/")
}

func shouldCaptureContextLine(text string) bool {
	if text == "" || isWarningLine(text) {
		return false
	}
	return !opensContextCapture(text)
}

func shouldCaptureCompilerContextLine(text string) bool {
	if text == "" {
		return false
	}

	switch {
	case strings.HasPrefix(text, "^"):
		return true
	case strings.HasPrefix(text, "symbol:"):
		return true
	case strings.HasPrefix(text, "location:"):
		return true
	case strings.HasPrefix(text, "required:"):
		return true
	case strings.HasPrefix(text, "found:"):
		return true
	case strings.HasPrefix(text, "reason:"):
		return true
	case strings.HasPrefix(text, "where:"):
		return true
	case strings.HasPrefix(text, "note:"):
		return true
	case strings.HasPrefix(text, "type mismatch:"):
		return true
	case strings.HasPrefix(text, "unresolved reference:"):
		return true
	case strings.HasPrefix(text, "none of the following functions can be called"):
		return true
	default:
		return false
	}
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
