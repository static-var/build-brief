package reducer

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"build-brief/internal/artifacts"
	"build-brief/internal/gradle"
	"build-brief/internal/runner"
)

const (
	logReadBufferSize   = 64 * 1024
	maxReducerLineBytes = 1 << 20
)

var (
	taskFailurePattern   = regexp.MustCompile(`^> Task (.+) FAILED$`)
	taskExecutionPattern = regexp.MustCompile(`Execution failed for task '([^']+)'\.`)
	testFailurePattern   = regexp.MustCompile(`^(.+?) > (.+?) FAILED$`)
	sourceErrorPattern   = regexp.MustCompile(`^.+\.(java|groovy|scala|kt|kts):\d+(?::\d+)?: error: .+$`)
	urlPattern           = regexp.MustCompile(`https?://[^\s<>"'\)\]]+`)
	ansiPattern          = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	// Status verbs and summary verbs mirror Gradle's own output (ConfigurationCacheProblems.kt
	// status lines + ConfigurationCacheProblemsFixture.groovy header regex): the cache action is
	// one of store/load/update -> stored/reused/updated and storing/reusing/updating.
	configCacheStatusPattern         = regexp.MustCompile(`^Configuration cache entry (reused|stored|discarded|updated)\b`)
	configCacheProblemSummaryPattern = regexp.MustCompile(`^\d+ problems? (?:was|were) found (?:storing|reusing|updating) the configuration cache`)
	configCacheReportPattern         = regexp.MustCompile(`^See the complete report at (file://\S+)`)
	maxWarnings                      = 8
	maxImportantLines                = 12
	maxCustomMatchLines              = 8
	maxConfigCacheLines              = 8
	configCacheCaptureLines          = 4
	contextCaptureLines              = 2
	compilerCaptureLines             = 3
	maxJUnitReportFiles              = 100
	maxJUnitScanErrors               = 8
	maxArtifactHints                 = 64
	maxArtifactHintBytes             = 64 * 1024
	maxArtifactHintLength            = 4096
	junitTimeSkew                    = time.Second
)

type Artifact = artifacts.Artifact

type Options struct {
	CustomMatches []CustomMatchRule
}

type CustomMatchRule struct {
	Name    string
	Pattern *regexp.Regexp
}

type CustomMatchResult struct {
	Name    string   `json:"name"`
	Matches []string `json:"matches"`
}

type JUnitScanMetadata struct {
	Discovered      int      `json:"discovered"`
	Parsed          int      `json:"parsed"`
	Skipped         int      `json:"skipped"`
	SkippedTests    int      `json:"skipped_tests,omitempty"`
	Errors          []string `json:"errors,omitempty"`
	ErrorCount      int      `json:"error_count,omitempty"`
	ErrorsTruncated bool     `json:"errors_truncated,omitempty"`
	Truncated       bool     `json:"truncated"`
}

type ArtifactScanMetadata = artifacts.ScanMetadata

// ArtifactHintScanMetadata describes raw-log hint retention, not artifacts.
// Observed counts normalized hint occurrences, including repeats. Retained is
// a bounded top-value set passed to artifact enrichment; Omitted is the number
// of observed occurrences not retained. It must not be used as an artifact
// count: ArtifactScanMetadata counts artifact paths after bounded scan
// deduplication and standard-root hint classification.
type ArtifactHintScanMetadata struct {
	Observed      int  `json:"observed"`
	Retained      int  `json:"retained"`
	Omitted       int  `json:"omitted"`
	RetainedBytes int  `json:"retained_bytes"`
	Truncated     bool `json:"truncated"`
}

type artifactHintCollector struct {
	hints         []string
	seen          map[string]struct{}
	retainedBytes int
	metadata      ArtifactHintScanMetadata
}

func newArtifactHintCollector() *artifactHintCollector {
	return &artifactHintCollector{
		hints: make([]string, 0, maxArtifactHints),
		seen:  make(map[string]struct{}, maxArtifactHints),
	}
}

func (c *artifactHintCollector) add(hint string) {
	if strings.TrimSpace(hint) == "" {
		return
	}
	c.metadata.Observed++
	if len(hint) > maxArtifactHintLength+64 {
		c.metadata.Truncated = true
		return
	}
	hint = artifacts.NormalizeHint(hint)
	if len(hint) > maxArtifactHintLength {
		c.metadata.Truncated = true
		return
	}
	if _, ok := c.seen[hint]; ok {
		return
	}
	hint = strings.Clone(hint)

	if len(c.hints) < maxArtifactHints && c.retainedBytes+len(hint) <= maxArtifactHintBytes {
		c.retain(len(c.hints), hint)
		return
	}

	worst := 0
	for i := 1; i < len(c.hints); i++ {
		if artifactHintLess(c.hints[worst], c.hints[i]) {
			worst = i
		}
	}
	if len(c.hints) == 0 || !artifactHintLess(hint, c.hints[worst]) {
		c.metadata.Truncated = true
		return
	}
	if c.retainedBytes-len(c.hints[worst])+len(hint) > maxArtifactHintBytes {
		c.metadata.Truncated = true
		return
	}
	delete(c.seen, c.hints[worst])
	c.retainedBytes -= len(c.hints[worst])
	c.retain(worst, hint)
}

func (c *artifactHintCollector) retain(index int, hint string) {
	if index == len(c.hints) {
		c.hints = append(c.hints, hint)
	} else {
		c.hints[index] = hint
	}
	c.seen[hint] = struct{}{}
	c.retainedBytes += len(hint)
}

func (c *artifactHintCollector) markTruncated() {
	c.metadata.Truncated = true
}

func (c *artifactHintCollector) finish() *ArtifactHintScanMetadata {
	if c.metadata.Observed == 0 && !c.metadata.Truncated {
		return nil
	}
	c.metadata.Retained = len(c.hints)
	c.metadata.RetainedBytes = c.retainedBytes
	c.metadata.Omitted = c.metadata.Observed - c.metadata.Retained
	if c.metadata.Omitted > 0 {
		c.metadata.Truncated = true
	}
	metadata := c.metadata
	return &metadata
}

func artifactHintLess(left, right string) bool {
	leftPriority := artifactHintPriority(left)
	rightPriority := artifactHintPriority(right)
	if leftPriority != rightPriority {
		return leftPriority < rightPriority
	}
	if len(left) != len(right) {
		return len(left) < len(right)
	}
	return left < right
}

func artifactHintPriority(hint string) int {
	lower := strings.ToLower(hint)
	switch {
	case strings.HasSuffix(lower, ".apk"):
		return 0
	case strings.HasSuffix(lower, ".aab"):
		return 1
	case strings.HasSuffix(lower, ".xcframework"):
		return 2
	case strings.HasSuffix(lower, ".framework"):
		return 3
	case strings.HasSuffix(lower, ".zip"), strings.HasSuffix(lower, ".tar"), strings.HasSuffix(lower, ".tgz"), strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".war"), strings.HasSuffix(lower, ".ear"), strings.HasSuffix(lower, ".kexe"):
		return 4
	case strings.HasSuffix(lower, ".jar"):
		return 5
	case strings.HasSuffix(lower, ".klib"):
		return 6
	case strings.HasSuffix(lower, ".aar"):
		return 7
	default:
		return 8
	}
}

type Diagnostic struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Severity   string   `json:"severity"`
	Summary    string   `json:"summary"`
	Evidence   []string `json:"evidence"`
	NextChecks []string `json:"next_checks,omitempty"`
	Confidence string   `json:"confidence"`
}

type Summary struct {
	SchemaVersion             string                    `json:"schema_version"`
	Tool                      string                    `json:"tool"`
	Success                   bool                      `json:"success"`
	ExitCode                  int                       `json:"exit_code"`
	Duration                  string                    `json:"duration"`
	DurationMs                int64                     `json:"duration_ms"`
	ProjectDir                string                    `json:"project_dir"`
	Executable                string                    `json:"executable"`
	Command                   []string                  `json:"command"`
	CommandLine               string                    `json:"command_line"`
	Source                    string                    `json:"source"`
	RawLogPath                string                    `json:"raw_log_path"`
	RawOutputTokens           int                       `json:"raw_output_tokens"`
	EmittedTokens             int                       `json:"emitted_output_tokens"`
	SavedTokens               int                       `json:"saved_output_tokens"`
	SavingsPct                float64                   `json:"savings_pct"`
	BuildStatusLine           string                    `json:"build_status_line"`
	FailedTasks               []string                  `json:"failed_tasks"`
	FailedTests               []string                  `json:"failed_tests"`
	PassedTestCount           int                       `json:"passed_test_count,omitempty"`
	FailedTestCount           int                       `json:"failed_test_count,omitempty"`
	WarningCount              int                       `json:"warning_count"`
	Warnings                  []string                  `json:"warnings"`
	ImportantLines            []string                  `json:"important_lines"`
	Diagnostics               []Diagnostic              `json:"diagnostics,omitempty"`
	BuildScanURLs             []string                  `json:"build_scan_urls,omitempty"`
	ConfigCacheStatus         string                    `json:"config_cache_status,omitempty"`
	ConfigCacheProblems       []string                  `json:"config_cache_problems,omitempty"`
	ConfigCacheReportURL      string                    `json:"config_cache_report_url,omitempty"`
	ReportLines               []string                  `json:"report_lines,omitempty"`
	CustomMatches             []CustomMatchResult       `json:"custom_matches,omitempty"`
	Artifacts                 []Artifact                `json:"artifacts,omitempty"`
	JUnitScan                 *JUnitScanMetadata        `json:"junit_scan,omitempty"`
	ArtifactScan              *ArtifactScanMetadata     `json:"artifact_scan,omitempty"`
	ArtifactHintScan          *ArtifactHintScanMetadata `json:"artifact_hint_scan,omitempty"`
	GeneratedClassFileCount   int                       `json:"generated_class_file_count,omitempty"`
	GeneratedCodegenFileCount int                       `json:"generated_codegen_file_count,omitempty"`
	TotalLines                int                       `json:"total_lines"`
}

type junitTestSuite struct {
	TestCases []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Failure   *junitFailure `xml:"failure"`
	Error     *junitFailure `xml:"error"`
	Skipped   *struct{}     `xml:"skipped"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

func Reduce(command gradle.Command, result runner.Result) (Summary, error) {
	return ReduceWithOptions(command, result, Options{})
}

// readLogLines uses a fixed reader buffer and a bounded line prefix. A very
// long line is drained through ReadSlice without retaining its full contents;
// the reducer still receives all ordinary lines, including long compiler
// lines up to maxReducerLineBytes.
func readLogLines(reader *bufio.Reader, visit func([]byte, bool) error) error {
	line := make([]byte, 0, maxReducerLineBytes)
	truncated := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			if !truncated {
				remaining := maxReducerLineBytes - len(line)
				if remaining <= 0 {
					truncated = true
				} else {
					if len(fragment) > remaining {
						fragment = fragment[:remaining]
						truncated = true
					}
					line = append(line, fragment...)
				}
			}
			if err != bufio.ErrBufferFull {
				if visitErr := visit(line, truncated); visitErr != nil {
					return visitErr
				}
				line = line[:0]
				truncated = false
			}
		}

		switch err {
		case nil, bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				if visitErr := visit(line, truncated); visitErr != nil {
					return visitErr
				}
			}
			return nil
		default:
			return err
		}
	}
}

func ReduceWithOptions(command gradle.Command, result runner.Result, opts Options) (Summary, error) {
	sanitizedArgs := gradle.SanitizeArgs(command.Args)
	summary := Summary{
		SchemaVersion: "v1",
		Tool:          "build-brief",
		Success:       result.ExitCode == 0,
		ExitCode:      result.ExitCode,
		Duration:      formatDuration(result.Duration),
		DurationMs:    result.Duration.Milliseconds(),
		ProjectDir:    command.ProjectDir,
		Executable:    command.Executable,
		Command:       append([]string{command.Executable}, sanitizedArgs...),
		CommandLine: strings.Join(
			append([]string{filepath.Base(command.Executable)}, sanitizedArgs...),
			" ",
		),
		Source:              string(command.Source),
		RawLogPath:          result.RawLogPath,
		FailedTasks:         []string{},
		FailedTests:         []string{},
		Warnings:            []string{},
		ImportantLines:      []string{},
		BuildScanURLs:       []string{},
		ConfigCacheProblems: []string{},
		ReportLines:         []string{},
		CustomMatches:       customMatchResults(opts.CustomMatches),
		Artifacts:           []Artifact{},
	}

	failedTasks := make(map[string]struct{})
	failedTests := make(map[string]struct{})
	warnings := make(map[string]struct{})
	important := make(map[string]struct{})
	buildScanURLs := make(map[string]struct{})
	configCacheProblemSeen := make(map[string]struct{})
	customMatchSeen := make([]map[string]struct{}, len(opts.CustomMatches))
	for i := range customMatchSeen {
		customMatchSeen[i] = make(map[string]struct{})
	}
	artifactHintCollector := newArtifactHintCollector()
	diagnosticEvidence := newDiagnosticEvidence()
	invocationShape := gradle.AnalyzeArgs(command.Args)

	file, err := os.Open(result.RawLogPath)
	if err != nil {
		return Summary{}, err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, logReadBufferSize)
	captureContextRemaining := 0
	captureCompilerRemaining := 0
	captureBuildScanURLRemaining := 0
	captureConfigCacheRemaining := 0
	readErr := readLogLines(reader, func(rawLine []byte, lineTruncated bool) error {
		if lineTruncated {
			artifactHintCollector.markTruncated()
		}
		summary.TotalLines++
		text := normalizeLine(strings.TrimRight(string(rawLine), "\r\n"))
		if text == "" {
			return nil
		}
		diagnosticEvidence.collect(text)

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

		if invocationShape.IsPureInformational && shouldPreserveReportLine(text) {
			summary.ReportLines = append(summary.ReportLines, text)
		}

		if isImportantLine(text) {
			addUnique(&summary.ImportantLines, important, text, maxImportantLines)
		}

		if isGeneratedOutputLocationLine(text) {
			addUnique(&summary.ImportantLines, important, text, maxImportantLines)
		}

		lineURLs := extractURLs(text)
		if isBuildScanMarkerLine(text) {
			if len(lineURLs) > 0 {
				addUniqueBuildScanURLs(&summary.BuildScanURLs, buildScanURLs, lineURLs)
				captureBuildScanURLRemaining = 0
			} else {
				captureBuildScanURLRemaining = 3
			}
		} else if captureBuildScanURLRemaining > 0 {
			if len(lineURLs) > 0 {
				addUniqueBuildScanURLs(&summary.BuildScanURLs, buildScanURLs, lineURLs)
				captureBuildScanURLRemaining = 0
			} else {
				captureBuildScanURLRemaining--
			}
		}

		if status, ok := configCacheStatus(text); ok {
			summary.ConfigCacheStatus = status
		}
		if m := configCacheReportPattern.FindStringSubmatch(text); m != nil {
			summary.ConfigCacheReportURL = m[1]
		}
		if isConfigCacheProblemSummary(text) {
			addUnique(&summary.ConfigCacheProblems, configCacheProblemSeen, text, maxConfigCacheLines)
			captureConfigCacheRemaining = configCacheCaptureLines
		} else if captureConfigCacheRemaining > 0 {
			if isConfigCacheProblemDetail(text) {
				addUnique(&summary.ConfigCacheProblems, configCacheProblemSeen, strings.TrimPrefix(text, "- "), maxConfigCacheLines)
				captureConfigCacheRemaining = configCacheCaptureLines
			} else {
				captureConfigCacheRemaining--
			}
		}

		for i, rule := range opts.CustomMatches {
			if rule.Pattern == nil {
				continue
			}
			matches := rule.Pattern.FindAllString(text, -1)
			for _, match := range matches {
				addUnique(&summary.CustomMatches[i].Matches, customMatchSeen[i], strings.TrimRight(match, ".,;:"), maxCustomMatchLines)
			}
		}

		// Stream every candidate from this line. ScanHints keeps only three
		// regexp positions live; the collector owns the bounded retained state
		// and reports exact observed/omitted counts without sacrificing late
		// high-priority hints.
		artifacts.ScanHints(text, 0, 0, func(raw string) {
			artifactHintCollector.add(raw)
		})

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
		return nil
	})
	if readErr != nil {
		return Summary{}, readErr
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

	enrichWithJUnitResults(command.ProjectDir, result, &summary, failedTests, important, warnings)
	enrichWithArtifacts(command.ProjectDir, result, &summary, artifactHintCollector.hints, warnings)
	summary.ArtifactHintScan = artifactHintCollector.finish()
	summary.Diagnostics = Diagnose(diagnosticEvidence, summary)

	return summary, nil
}

func customMatchResults(rules []CustomMatchRule) []CustomMatchResult {
	if len(rules) == 0 {
		return nil
	}
	results := make([]CustomMatchResult, 0, len(rules))
	for _, rule := range rules {
		results = append(results, CustomMatchResult{
			Name:    strings.TrimSpace(rule.Name),
			Matches: []string{},
		})
	}
	return results
}

func enrichWithArtifacts(projectDir string, result runner.Result, summary *Summary, hints []string, warnings map[string]struct{}) {
	if !summary.Success || result.StartTime.IsZero() {
		return
	}

	generated := artifacts.FindGeneratedWithMetadata(projectDir, result.StartTime, result.ArtifactSnapshot, hints)
	found := generated.Artifacts
	metadata := generated.Metadata
	classCount := generated.ClassCount
	codegenCount := generated.CodegenCount
	if len(found) == 0 && shouldReportAvailableArtifacts(summary.Command) {
		available := artifacts.FindAvailableScopedWithMetadata(projectDir, hints, commandProjectPrefixes(summary.Command))
		found = available.Artifacts
		metadata = available.Metadata
	}
	summary.Artifacts = found
	summary.GeneratedClassFileCount = classCount
	summary.GeneratedCodegenFileCount = codegenCount
	if metadata.Discovered > 0 || metadata.ErrorCount > 0 || metadata.Truncated {
		summary.ArtifactScan = &metadata
	}
	if metadata.Truncated || metadata.ErrorCount > 0 {
		message := fmt.Sprintf("Artifact scan incomplete: discovered %d, reported %d, skipped %d", metadata.Discovered, metadata.Reported, metadata.Skipped)
		if metadata.Truncated {
			message += " (" + artifactScanTruncationReason(metadata) + ")"
		}
		addEnrichmentWarning(summary, warnings, message)
	}
}

func artifactScanTruncationReason(metadata artifacts.ScanMetadata) string {
	switch {
	case metadata.HintsTruncated && metadata.Skipped > 0:
		return "hint input and reporting bounds reached"
	case metadata.HintsTruncated:
		return "hint input bound reached"
	case metadata.Skipped > 0:
		return "truncated at the reporting limit"
	default:
		return "scan bound reached"
	}
}

func shouldReportAvailableArtifacts(command []string) bool {
	for _, arg := range gradle.AnalyzeArgs(command).TaskSelectors {
		taskName := strings.ToLower(arg)
		if index := strings.LastIndex(taskName, ":"); index >= 0 {
			taskName = taskName[index+1:]
		}
		switch {
		case strings.HasPrefix(taskName, "assemble"),
			strings.HasPrefix(taskName, "bundle"),
			strings.HasPrefix(taskName, "publish"),
			strings.HasPrefix(taskName, "archive"),
			strings.HasPrefix(taskName, "package"),
			strings.HasPrefix(taskName, "bootjar"),
			strings.HasPrefix(taskName, "shadowjar"),
			strings.HasPrefix(taskName, "distzip"),
			strings.HasPrefix(taskName, "disttar"),
			strings.HasPrefix(taskName, "installdist"),
			strings.Contains(taskName, "xcframework"),
			strings.Contains(taskName, "framework"),
			strings.Contains(taskName, "klib"),
			strings.Contains(taskName, "kexe"):
			return true
		case taskName == "jar", taskName == "war", taskName == "ear":
			return true
		}
	}
	return false
}

func commandProjectPrefixes(command []string) []string {
	seen := make(map[string]struct{})
	prefixes := make([]string, 0)
	for _, arg := range gradle.AnalyzeArgs(command).TaskSelectors {
		lastColon := strings.LastIndex(arg, ":")
		if lastColon <= 0 {
			continue
		}
		projectPath := strings.TrimPrefix(arg[:lastColon], ":")
		projectPath = strings.TrimSpace(projectPath)
		if projectPath == "" {
			continue
		}
		projectPath = strings.ReplaceAll(projectPath, ":", "/")
		if _, ok := seen[projectPath]; ok {
			continue
		}
		seen[projectPath] = struct{}{}
		prefixes = append(prefixes, projectPath)
	}
	return prefixes
}

type junitReportSelection struct {
	files           []string
	discovered      int
	errors          []string
	errorCount      int
	errorsTruncated bool
	truncated       bool
}

func enrichWithJUnitResults(projectDir string, result runner.Result, summary *Summary, failedTests, important, warnings map[string]struct{}) {
	if !summary.Success && !shouldReadJUnitReportsOnFailure(summary) {
		return
	}
	selection := selectJUnitReportFiles(projectDir, result.StartTime, summary.Success && shouldFallbackToAvailableJUnitReports(summary.Command))
	metadata := &JUnitScanMetadata{
		Discovered:      selection.discovered,
		Errors:          append([]string(nil), selection.errors...),
		ErrorCount:      selection.errorCount,
		ErrorsTruncated: selection.errorsTruncated,
		Truncated:       selection.truncated,
	}
	passedCount := 0
	failedCount := 0
	for _, path := range selection.files {
		content, err := os.ReadFile(path)
		if err != nil {
			addJUnitScanError(metadata, projectDir, path, err)
			continue
		}

		var suite junitTestSuite
		if err := xml.Unmarshal(content, &suite); err != nil {
			addJUnitScanError(metadata, projectDir, path, err)
			continue
		}
		metadata.Parsed++

		for _, testCase := range suite.TestCases {
			failure := testCase.Failure
			if failure == nil {
				failure = testCase.Error
			}
			if failure == nil && testCase.Skipped != nil {
				metadata.SkippedTests++
				continue
			}
			if failure == nil {
				passedCount++
				continue
			}
			failedCount++

			failedTestName := formatJUnitFailedTest(testCase.ClassName, testCase.Name)
			addUnique(&summary.FailedTests, failedTests, failedTestName, 0)

			detail := buildJUnitFailureDetail(failedTestName, failure)
			addUnique(&summary.ImportantLines, important, detail, maxImportantLines)

			if location := extractRelevantStackFrame(failure.Body); location != "" {
				addUnique(&summary.ImportantLines, important, location, maxImportantLines)
			}
		}
	}
	metadata.Skipped = metadata.Discovered - metadata.Parsed
	if metadata.Skipped < 0 {
		metadata.Skipped = 0
	}
	if metadata.Discovered > 0 || metadata.ErrorCount > 0 || metadata.Truncated {
		summary.JUnitScan = metadata
	}
	if metadata.Truncated || metadata.ErrorCount > 0 {
		message := fmt.Sprintf("JUnit report scan incomplete: discovered %d, parsed %d, skipped %d", metadata.Discovered, metadata.Parsed, metadata.Skipped)
		if metadata.Truncated {
			message += " (truncated at the reporting limit)"
		}
		addEnrichmentWarning(summary, warnings, message)
	}

	if passedCount > 0 || failedCount > 0 {
		summary.PassedTestCount = passedCount
		summary.FailedTestCount = failedCount
	}
}

func addJUnitScanError(metadata *JUnitScanMetadata, projectDir, path string, err error) {
	if err == nil {
		return
	}
	metadata.ErrorCount++
	if len(metadata.Errors) >= maxJUnitScanErrors {
		metadata.ErrorsTruncated = true
		return
	}
	metadata.Errors = append(metadata.Errors, relativeScanErrorPath(projectDir, path)+": "+sanitizeScanErrorText(projectDir, err.Error()))
}

func relativeScanErrorPath(projectDir, path string) string {
	if relative, err := filepath.Rel(projectDir, path); err == nil {
		return filepath.ToSlash(relative)
	}
	return sanitizeScanErrorText(projectDir, filepath.ToSlash(path))
}

func sanitizeScanErrorText(projectDir, text string) string {
	for _, root := range scanErrorPathVariants(projectDir) {
		text = strings.ReplaceAll(text, root, "<project>")
	}
	return text
}

func scanErrorPathVariants(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	candidates := []string{projectDir, filepath.Clean(projectDir)}
	if absolute, err := filepath.Abs(projectDir); err == nil {
		candidates = append(candidates, absolute, filepath.Clean(absolute))
	}
	variants := make([]string, 0, len(candidates)*2)
	seen := make(map[string]struct{}, len(candidates)*2)
	for _, candidate := range candidates {
		for _, variant := range []string{candidate, filepath.ToSlash(candidate)} {
			if variant == "" {
				continue
			}
			if _, ok := seen[variant]; ok {
				continue
			}
			seen[variant] = struct{}{}
			variants = append(variants, variant)
		}
	}
	return variants
}

func isJUnitScanErrorPath(projectDir, path string) bool {
	relative := relativeScanErrorPath(projectDir, path)
	normalized := "/" + strings.Trim(relative, "/") + "/"
	return strings.Contains(normalized, "/build/test-results/")
}

func addEnrichmentWarning(summary *Summary, warnings map[string]struct{}, message string) {
	before := len(summary.Warnings)
	addUnique(&summary.Warnings, warnings, message, maxWarnings)
	if len(summary.Warnings) > before {
		summary.WarningCount++
	}
}

func findJUnitReportFiles(projectDir string) junitReportSelection {
	selection := junitReportSelection{files: make([]string, 0, maxJUnitReportFiles)}
	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if isJUnitScanErrorPath(projectDir, path) {
				addSelectionError(&selection, projectDir, path, walkErr)
			}
			return nil
		}
		if isJUnitReportPath(path, entry.Name()) {
			selection.discovered++
			if len(selection.files) < maxJUnitReportFiles {
				selection.files = append(selection.files, path)
			} else {
				selection.truncated = true
			}
			return nil
		}
		if entry.IsDir() {
			if artifacts.ShouldSkipDir(entry) {
				return filepath.SkipDir
			}
			return nil
		}
		return nil
	})

	return selection
}

func selectJUnitReportFiles(projectDir string, startedAt time.Time, allowFallback bool) junitReportSelection {
	selection := findJUnitReportFiles(projectDir)
	if len(selection.files) == 0 {
		return selection
	}
	if startedAt.IsZero() {
		if allowFallback {
			return selection
		}
		selection.files = nil
		return selection
	}

	threshold := startedAt.Add(-junitTimeSkew)
	fresh := make([]string, 0, len(selection.files))
	for _, path := range selection.files {
		info, err := os.Stat(path)
		if err != nil {
			addSelectionError(&selection, projectDir, path, err)
			continue
		}
		if !info.ModTime().Before(threshold) {
			fresh = append(fresh, path)
		}
	}
	if len(fresh) > 0 {
		selection.files = fresh
		return selection
	}
	if allowFallback {
		return selection
	}
	selection.files = nil
	return selection
}

func addSelectionError(selection *junitReportSelection, projectDir, path string, err error) {
	if err == nil {
		return
	}
	selection.errorCount++
	if len(selection.errors) >= maxJUnitScanErrors {
		selection.errorsTruncated = true
		return
	}
	selection.errors = append(selection.errors, relativeScanErrorPath(projectDir, path)+": "+sanitizeScanErrorText(projectDir, err.Error()))
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

func addUniqueBuildScanURLs(items *[]string, seen map[string]struct{}, values []string) {
	for _, value := range values {
		if isBuildScanURL(value) {
			addUnique(items, seen, value, 0)
		}
	}
}

func isBuildScanURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "/s/")
}

func extractURLs(text string) []string {
	matches := urlPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}

	urls := make([]string, 0, len(matches))
	for _, match := range matches {
		url := strings.TrimRight(match, ".,;:")
		if url != "" {
			urls = append(urls, url)
		}
	}
	return urls
}

func isBuildScanMarkerLine(text string) bool {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "build scan") {
		switch {
		case strings.Contains(lower, "publishing"),
			strings.Contains(lower, "published"),
			strings.Contains(lower, "available"),
			strings.Contains(lower, "view"),
			strings.Contains(lower, "url"),
			strings.Contains(lower, "develocity"):
			return true
		}
	}

	return strings.Contains(lower, "develocity") &&
		(strings.Contains(lower, "publishing") || strings.Contains(lower, "published"))
}

func configCacheStatus(text string) (string, bool) {
	m := configCacheStatusPattern.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return m[1], true
}

func isConfigCacheProblemSummary(text string) bool {
	return configCacheProblemSummaryPattern.MatchString(text)
}

func isConfigCacheProblemDetail(text string) bool {
	return strings.HasPrefix(text, "- ")
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
	case sourceErrorPattern.MatchString(text):
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

func shouldFallbackToAvailableJUnitReports(command []string) bool {
	for _, arg := range gradle.AnalyzeArgs(command).TaskSelectors {
		taskName := strings.ToLower(arg)
		if index := strings.LastIndex(taskName, ":"); index >= 0 {
			taskName = taskName[index+1:]
		}
		if strings.Contains(taskName, "test") || taskName == "check" {
			return true
		}
	}
	return false
}

func shouldReadJUnitReportsOnFailure(summary *Summary) bool {
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

func shouldPreserveReportLine(text string) bool {
	if text == "" {
		return false
	}
	switch {
	case strings.HasPrefix(text, "BUILD SUCCESSFUL"):
		return false
	case strings.HasPrefix(text, "BUILD FAILED"):
		return false
	case strings.Contains(text, " actionable task"):
		return false
	case strings.HasPrefix(text, "Consider enabling configuration cache"):
		return false
	case strings.HasPrefix(text, "Configuration cache entry "):
		return false
	default:
		return true
	}
}

func isGeneratedOutputLocationLine(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, " written to: ") &&
		(strings.Contains(text, "/") || strings.Contains(text, `\`))
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
