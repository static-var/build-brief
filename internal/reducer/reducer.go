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
	"sort"
	"strings"
	"time"

	"build-brief/internal/artifacts"
	"build-brief/internal/config"
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
	maxCustomMatchLines              = config.CustomMatchUniqueResultLimitPerRule
	maxConfigCacheLines              = 8
	maxReportLines                   = 128
	maxFailedTasks                   = 64
	maxFailedTests                   = 128
	maxBuildScanURLs                 = 16
	maxCommandArgs                   = 128
	maxCustomMatchGroups             = config.CustomMatchRuleLimit
	maxSummaryCollectionBytes        = 64 * 1024
	configCacheCaptureLines          = 4
	contextCaptureLines              = 2
	compilerCaptureLines             = 3
	maxJUnitReportFiles              = 100
	maxJUnitWalkEntries              = 10_000
	maxJUnitScanErrors               = 8
	maxJUnitScanErrorBytes           = 64 * 1024
	maxJUnitFileBytes                = 1 << 20
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
	Discovered         int      `json:"discovered"`
	Parsed             int      `json:"parsed"`
	Skipped            int      `json:"skipped"`
	SkippedTests       int      `json:"skipped_tests,omitempty"`
	Errors             []string `json:"errors,omitempty"`
	ErrorCount         int      `json:"error_count,omitempty"`
	ErrorBytes         int64    `json:"error_bytes,omitempty"`
	ErrorsTruncated    bool     `json:"errors_truncated,omitempty"`
	FileBytesTruncated bool     `json:"file_bytes_truncated,omitempty"`
	WalkTruncated      bool     `json:"walk_truncated,omitempty"`
	Truncated          bool     `json:"truncated"`
}

type RawInputMetadata struct {
	Partial        bool  `json:"partial"`
	TruncatedLines int   `json:"truncated_lines"`
	TruncatedBytes int64 `json:"truncated_bytes"`
}

type ReducerCollectionMetadata struct {
	Observed       int    `json:"observed"`
	Retained       int    `json:"retained"`
	Omitted        int    `json:"omitted"`
	ObservedBytes  int64  `json:"observed_bytes"`
	RetainedBytes  int64  `json:"retained_bytes"`
	OmittedBytes   int64  `json:"omitted_bytes"`
	CountPrecision string `json:"count_precision,omitempty"`
	Truncated      bool   `json:"truncated"`
}

// CountPrecision describes collection count completeness. "exact" means the
// observed, omitted, and byte totals are exact; "lower_bound" means each is a
// minimum after the fixed auxiliary deduplication state was exhausted.

type ReducerMetadata struct {
	Partial       bool                                 `json:"partial"`
	PartialFields []string                             `json:"partial_fields,omitempty"`
	Collections   map[string]ReducerCollectionMetadata `json:"collections,omitempty"`
}

// Completeness aliases keep the additive metadata names explicit for callers.
type RawInputCompletenessMetadata = RawInputMetadata
type ReducerCompletenessMetadata = ReducerMetadata
type ReducerCollectionCompletenessMetadata = ReducerCollectionMetadata

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

type boundedStringCollector struct {
	values        []string
	seen          map[string]struct{}
	maxCount      int
	maxBytes      int64
	retainedBytes int64
	observed      int
	observedBytes int64
	truncated     bool
	deduplicate   bool
}

func newBoundedStringCollector(maxCount, maxBytes int, deduplicate bool) *boundedStringCollector {
	collector := &boundedStringCollector{
		values:      make([]string, 0, maxCount),
		maxCount:    maxCount,
		maxBytes:    int64(maxBytes),
		deduplicate: deduplicate,
	}
	if deduplicate {
		collector.seen = make(map[string]struct{}, maxCount)
	}
	return collector
}

func (c *boundedStringCollector) add(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if c.deduplicate {
		if _, ok := c.seen[value]; ok {
			return false
		}
	}
	c.observed++
	c.observedBytes += int64(len(value))
	if len(c.values) >= c.maxCount || c.retainedBytes+int64(len(value)) > c.maxBytes {
		c.truncated = true
		return false
	}
	value = strings.Clone(value)
	c.values = append(c.values, value)
	c.retainedBytes += int64(len(value))
	if c.deduplicate {
		c.seen[value] = struct{}{}
	}
	return true
}

func (c *boundedStringCollector) metadata() ReducerCollectionMetadata {
	metadata := ReducerCollectionMetadata{
		Observed:      c.observed,
		Retained:      len(c.values),
		Omitted:       c.observed - len(c.values),
		ObservedBytes: c.observedBytes,
		RetainedBytes: c.retainedBytes,
		OmittedBytes:  c.observedBytes - c.retainedBytes,
		Truncated:     c.truncated,
	}
	if metadata.Omitted > 0 || metadata.OmittedBytes > 0 {
		metadata.Truncated = true
	}
	return metadata
}

// buildScanURLCollector retains URLs and a separately bounded set of rejected
// URLs. This deduplicates over-cap repeats exactly until auxiliary state fills.
// Once it fills, totals remain stable lower bounds rather than counting an
// indistinguishable repeat as a new URL.
type buildScanURLCollector struct {
	values        []string
	retained      map[string]struct{}
	omitted       map[string]struct{}
	maxCount      int
	maxBytes      int64
	retainedBytes int64
	omittedBytes  int64
	observed      int
	observedBytes int64
	exact         bool
	truncated     bool
}

func newBuildScanURLCollector(maxCount, maxBytes int) *buildScanURLCollector {
	return &buildScanURLCollector{
		values:   make([]string, 0, maxCount),
		retained: make(map[string]struct{}, maxCount),
		omitted:  make(map[string]struct{}, maxCount),
		maxCount: maxCount,
		maxBytes: int64(maxBytes),
		exact:    true,
	}
}

func (c *buildScanURLCollector) add(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, ok := c.retained[value]; ok {
		return false
	}
	if _, ok := c.omitted[value]; ok {
		return false
	}
	if !c.exact {
		return false
	}

	c.observed++
	c.observedBytes += int64(len(value))
	if len(c.values) < c.maxCount && c.retainedBytes+int64(len(value)) <= c.maxBytes {
		value = strings.Clone(value)
		c.values = append(c.values, value)
		c.retained[value] = struct{}{}
		c.retainedBytes += int64(len(value))
		return true
	}

	c.truncated = true
	if len(c.omitted) < c.maxCount && c.omittedBytes+int64(len(value)) <= c.maxBytes {
		value = strings.Clone(value)
		c.omitted[value] = struct{}{}
		c.omittedBytes += int64(len(value))
		return false
	}

	// This URL is certainly distinct from all remembered URLs. Do not retain
	// it: that would make later repeats indistinguishable from new URLs.
	c.exact = false
	return false
}

func (c *buildScanURLCollector) metadata() ReducerCollectionMetadata {
	metadata := ReducerCollectionMetadata{
		Observed:       c.observed,
		Retained:       len(c.values),
		Omitted:        c.observed - len(c.values),
		ObservedBytes:  c.observedBytes,
		RetainedBytes:  c.retainedBytes,
		OmittedBytes:   c.observedBytes - c.retainedBytes,
		Truncated:      c.truncated,
		CountPrecision: "exact",
	}
	if !c.exact {
		metadata.CountPrecision = "lower_bound"
	}
	return metadata
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
	RawInput                  *RawInputMetadata         `json:"raw_input,omitempty"`
	Reducer                   *ReducerMetadata          `json:"reducer,omitempty"`
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

// semanticInvocation contains task semantics derived from the complete,
// unbounded invocation. It is intentionally distinct from Summary.Command,
// which is redacted and bounded output metadata.
type semanticInvocation struct {
	TaskSelectors       []string
	IsPureInformational bool
}

func analyzeSemanticInvocation(args []string) semanticInvocation {
	shape := gradle.AnalyzeArgs(args)
	return semanticInvocation{
		TaskSelectors:       shape.TaskSelectors,
		IsPureInformational: shape.IsPureInformational,
	}
}

func Reduce(command gradle.Command, result runner.Result) (Summary, error) {
	return ReduceWithOptions(command, result, Options{})
}

// readLogLines uses a fixed reader buffer and a bounded line prefix. A very
// long line is drained through ReadSlice without retaining its full contents;
// the reducer still receives all ordinary lines, including long compiler
// lines up to maxReducerLineBytes.
func readLogLines(reader *bufio.Reader, visit func([]byte, bool, int64) error) error {
	line := make([]byte, 0, maxReducerLineBytes)
	lineBytes := int64(0)
	truncated := false
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > 0 {
			lineBytes += int64(len(fragment))
			content := fragment
			terminatorBytes := 0
			if err != bufio.ErrBufferFull && content[len(content)-1] == '\n' {
				// The bound applies to line content, excluding LF or CRLF terminators.
				content = content[:len(content)-1]
				terminatorBytes++
				if len(content) > 0 && content[len(content)-1] == '\r' {
					content = content[:len(content)-1]
					terminatorBytes++
				}
			}
			if !truncated {
				remaining := maxReducerLineBytes - len(line)
				if len(content) > remaining {
					line = append(line, content[:remaining]...)
					truncated = true
				} else {
					line = append(line, content...)
				}
			}
			if err != bufio.ErrBufferFull {
				contentBytes := lineBytes - int64(terminatorBytes)
				if visitErr := visit(line, truncated, contentBytes-int64(len(line))); visitErr != nil {
					return visitErr
				}
				line = line[:0]
				lineBytes = 0
				truncated = false
			}
		}

		switch err {
		case nil, bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				if visitErr := visit(line, truncated, lineBytes-int64(len(line))); visitErr != nil {
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
	invocation := analyzeSemanticInvocation(command.Args)
	sanitizedArgs := gradle.SanitizeArgs(command.Args)
	commandArgs := newBoundedStringCollector(maxCommandArgs, maxSummaryCollectionBytes, false)
	for _, arg := range sanitizedArgs {
		commandArgs.add(arg)
	}
	customRulesObserved := len(opts.CustomMatches)
	customRulesBytes := customMatchRuleBytes(opts.CustomMatches)
	customRules := opts.CustomMatches
	customRulesTruncated := len(customRules) > maxCustomMatchGroups
	if customRulesTruncated {
		customRules = customRules[:maxCustomMatchGroups]
	}
	customRulesRetainedBytes := customMatchRuleBytes(customRules)
	summary := Summary{
		SchemaVersion: "v1",
		Tool:          "build-brief",
		Success:       result.ExitCode == 0,
		ExitCode:      result.ExitCode,
		Duration:      formatDuration(result.Duration),
		DurationMs:    result.Duration.Milliseconds(),
		ProjectDir:    command.ProjectDir,
		Executable:    command.Executable,
		Command:       append([]string{command.Executable}, commandArgs.values...),
		CommandLine: strings.Join(
			append([]string{filepath.Base(command.Executable)}, commandArgs.values...),
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
		CustomMatches:       customMatchResults(customRules),
		Artifacts:           []Artifact{},
	}

	failedTasks := newBoundedStringCollector(maxFailedTasks, maxSummaryCollectionBytes, true)
	failedTests := newBoundedStringCollector(maxFailedTests, maxSummaryCollectionBytes, true)
	warnings := newBoundedStringCollector(maxWarnings, maxSummaryCollectionBytes, true)
	important := newBoundedStringCollector(maxImportantLines, maxSummaryCollectionBytes, true)
	buildScanURLs := newBuildScanURLCollector(maxBuildScanURLs, maxSummaryCollectionBytes)
	configCacheProblems := newBoundedStringCollector(maxConfigCacheLines, maxSummaryCollectionBytes, true)
	reportLines := newBoundedStringCollector(maxReportLines, maxSummaryCollectionBytes, false)
	customMatches := make([]*boundedStringCollector, len(customRules))
	customMatchBytes := maxSummaryCollectionBytes
	if len(customRules) > 0 {
		customMatchBytes /= len(customRules)
	}
	for i := range customMatches {
		customMatches[i] = newBoundedStringCollector(maxCustomMatchLines, customMatchBytes, true)
	}
	commandArgsTruncated := commandArgs.truncated
	artifactHintCollector := newArtifactHintCollector()
	diagnosticEvidence := newDiagnosticEvidence()
	rawInput := RawInputMetadata{}

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
	readErr := readLogLines(reader, func(rawLine []byte, lineTruncated bool, truncatedBytes int64) error {
		if lineTruncated {
			rawInput.Partial = true
			rawInput.TruncatedLines++
			rawInput.TruncatedBytes += truncatedBytes
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
			failedTasks.add(matches[1])
		case taskExecutionPattern.MatchString(text):
			matches := taskExecutionPattern.FindStringSubmatch(text)
			failedTasks.add(matches[1])
		case testFailurePattern.MatchString(text):
			matches := testFailurePattern.FindStringSubmatch(text)
			failedTests.add(matches[1] + " > " + matches[2])
		}

		if isWarningLine(text) {
			summary.WarningCount++
			warnings.add(text)
		}

		if strings.HasPrefix(text, "BUILD SUCCESSFUL") || strings.HasPrefix(text, "BUILD FAILED") {
			summary.BuildStatusLine = strings.Clone(text)
		}

		if invocation.IsPureInformational && shouldPreserveReportLine(text) {
			reportLines.add(text)
		}

		if isImportantLine(text) {
			important.add(text)
		}

		if isGeneratedOutputLocationLine(text) {
			important.add(text)
		}

		isBuildScanMarker := isBuildScanMarkerLine(text)
		if isBuildScanMarker || captureBuildScanURLRemaining > 0 {
			foundBuildScanURL := scanBuildScanURLs(text, buildScanURLs)
			if isBuildScanMarker {
				if foundBuildScanURL {
					captureBuildScanURLRemaining = 0
				} else {
					captureBuildScanURLRemaining = 3
				}
			} else if foundBuildScanURL {
				captureBuildScanURLRemaining = 0
			} else {
				captureBuildScanURLRemaining--
			}
		}

		if status, ok := configCacheStatus(text); ok {
			summary.ConfigCacheStatus = strings.Clone(status)
		}
		if m := configCacheReportPattern.FindStringSubmatch(text); m != nil {
			summary.ConfigCacheReportURL = strings.Clone(m[1])
		}
		if isConfigCacheProblemSummary(text) {
			configCacheProblems.add(text)
			captureConfigCacheRemaining = configCacheCaptureLines
		} else if captureConfigCacheRemaining > 0 {
			if isConfigCacheProblemDetail(text) {
				configCacheProblems.add(strings.TrimPrefix(text, "- "))
				captureConfigCacheRemaining = configCacheCaptureLines
			} else {
				captureConfigCacheRemaining--
			}
		}

		for i, rule := range customRules {
			if rule.Pattern != nil {
				scanCustomMatches(rule.Pattern, text, customMatches[i])
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
			important.add(text)
			captureContextRemaining--
		}

		if opensCompilerContext(text) {
			captureCompilerRemaining = compilerCaptureLines
		} else if captureCompilerRemaining > 0 && shouldCaptureCompilerContextLine(text) {
			important.add(text)
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

	if len(important.values) == 0 {
		important.add(summary.BuildStatusLine)
	}
	syncSummaryCollections(&summary, failedTasks, failedTests, warnings, important, buildScanURLs, configCacheProblems, reportLines, customMatches)

	if !invocation.IsPureInformational {
		enrichWithJUnitResults(command.ProjectDir, result, invocation, &summary, failedTests, important, warnings)
		enrichWithArtifacts(command.ProjectDir, result, invocation, &summary, artifactHintCollector.hints, warnings)
	}
	syncSummaryCollections(&summary, failedTasks, failedTests, warnings, important, buildScanURLs, configCacheProblems, reportLines, customMatches)
	summary.ArtifactHintScan = artifactHintCollector.finish()
	summary.RawInput = finishRawInputMetadata(rawInput)
	summary.Reducer = finishReducerMetadata(summary.RawInput, summary, commandArgs, commandArgsTruncated, customRulesTruncated, customRulesObserved, customRulesBytes, customRulesRetainedBytes, failedTasks, failedTests, warnings, important, buildScanURLs, configCacheProblems, reportLines, customMatches)
	summary.Diagnostics = Diagnose(diagnosticEvidence, summary)

	return summary, nil
}

func customMatchRuleBytes(rules []CustomMatchRule) int64 {
	var total int64
	for _, rule := range rules {
		total += int64(len(strings.TrimSpace(rule.Name)))
		if rule.Pattern != nil {
			total += int64(len(rule.Pattern.String()))
		}
	}
	return total
}

// scanCustomMatches streams regexp matches so early duplicates cannot hide a
// later unique value. It retains no match slice and relies on the collector's
// existing count and byte bounds.
func scanCustomMatches(pattern *regexp.Regexp, text string, collector *boundedStringCollector) {
	for offset := 0; offset <= len(text); {
		match := pattern.FindStringIndex(text[offset:])
		if match == nil {
			return
		}
		start, end := offset+match[0], offset+match[1]
		collector.add(strings.TrimRight(text[start:end], ".,;:"))
		if end > offset {
			offset = end
			continue
		}
		// Empty matches otherwise make no progress. Advancing a byte is safe for
		// string slicing and keeps allocations bounded even for such a pattern.
		offset++
	}
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

func syncSummaryCollections(summary *Summary, failedTasks, failedTests, warnings, important *boundedStringCollector, buildScanURLs *buildScanURLCollector, configCacheProblems, reportLines *boundedStringCollector, customMatches []*boundedStringCollector) {
	summary.FailedTasks = failedTasks.values
	summary.FailedTests = failedTests.values
	summary.Warnings = warnings.values
	summary.ImportantLines = important.values
	summary.BuildScanURLs = buildScanURLs.values
	summary.ConfigCacheProblems = configCacheProblems.values
	summary.ReportLines = reportLines.values
	for i := range customMatches {
		summary.CustomMatches[i].Matches = customMatches[i].values
	}
}

func finishRawInputMetadata(metadata RawInputMetadata) *RawInputMetadata {
	if !metadata.Partial {
		return nil
	}
	return &metadata
}

func finishReducerMetadata(rawInput *RawInputMetadata, summary Summary, commandArgs *boundedStringCollector, commandArgsTruncated, customRulesTruncated bool, customRulesObserved int, customRulesBytes, customRulesRetainedBytes int64, failedTasks, failedTests, warnings, important *boundedStringCollector, buildScanURLs *buildScanURLCollector, configCacheProblems, reportLines *boundedStringCollector, customMatches []*boundedStringCollector) *ReducerMetadata {
	metadata := &ReducerMetadata{Collections: make(map[string]ReducerCollectionMetadata)}
	partialFields := make(map[string]struct{})
	if rawInput != nil {
		for _, field := range []string{"artifact_hint_scan", "artifacts", "build_status_line", "build_scan_urls", "config_cache_problems", "config_cache_report_url", "config_cache_status", "custom_matches", "diagnostics", "failed_tasks", "failed_tests", "important_lines", "junit_scan", "passed_test_count", "failed_test_count", "report_lines", "warning_count", "warnings"} {
			partialFields[field] = struct{}{}
		}
	}
	addReducerCollectionMetadata(metadata, partialFields, "failed_tasks", failedTasks)
	addReducerCollectionMetadata(metadata, partialFields, "failed_tests", failedTests)
	addReducerCollectionMetadata(metadata, partialFields, "warnings", warnings)
	addReducerCollectionMetadata(metadata, partialFields, "important_lines", important)
	addBuildScanCollectionMetadata(metadata, partialFields, buildScanURLs)
	addReducerCollectionMetadata(metadata, partialFields, "config_cache_problems", configCacheProblems)
	addReducerCollectionMetadata(metadata, partialFields, "report_lines", reportLines)
	for i, collector := range customMatches {
		addReducerCollectionMetadata(metadata, partialFields, fmt.Sprintf("custom_matches[%d].matches", i), collector)
	}
	if commandArgsTruncated {
		partialFields["command"] = struct{}{}
		metadata.Collections["command"] = commandArgs.metadata()
	}
	if customRulesTruncated {
		partialFields["custom_matches"] = struct{}{}
		metadata.Collections["custom_matches"] = ReducerCollectionMetadata{
			Observed:      customRulesObserved,
			Retained:      len(customMatches),
			Omitted:       customRulesObserved - len(customMatches),
			ObservedBytes: customRulesBytes,
			RetainedBytes: customRulesRetainedBytes,
			OmittedBytes:  customRulesBytes - customRulesRetainedBytes,
			Truncated:     true,
		}
	}
	if summary.JUnitScan != nil && (summary.JUnitScan.Truncated || summary.JUnitScan.ErrorCount > 0) {
		for _, field := range []string{"junit_scan", "failed_tests", "passed_test_count", "failed_test_count", "important_lines"} {
			partialFields[field] = struct{}{}
		}
	}
	if summary.ArtifactHintScan != nil && summary.ArtifactHintScan.Truncated {
		for _, field := range []string{"artifact_hint_scan", "artifacts"} {
			partialFields[field] = struct{}{}
		}
	}
	if summary.ArtifactScan != nil && (summary.ArtifactScan.Truncated || summary.ArtifactScan.ErrorCount > 0) {
		for _, field := range []string{"artifacts", "artifact_scan", "generated_class_file_count", "generated_codegen_file_count"} {
			partialFields[field] = struct{}{}
		}
	}
	if len(partialFields) == 0 {
		return nil
	}
	metadata.Partial = true
	metadata.PartialFields = make([]string, 0, len(partialFields))
	for field := range partialFields {
		metadata.PartialFields = append(metadata.PartialFields, field)
	}
	sort.Strings(metadata.PartialFields)
	return metadata
}

func addReducerCollectionMetadata(metadata *ReducerMetadata, partialFields map[string]struct{}, name string, collector *boundedStringCollector) {
	collection := collector.metadata()
	if !collection.Truncated {
		return
	}
	metadata.Collections[name] = collection
	partialFields[name] = struct{}{}
}

func addBuildScanCollectionMetadata(metadata *ReducerMetadata, partialFields map[string]struct{}, collector *buildScanURLCollector) {
	collection := collector.metadata()
	if !collection.Truncated {
		return
	}
	metadata.Collections["build_scan_urls"] = collection
	partialFields["build_scan_urls"] = struct{}{}
}

func enrichWithArtifacts(projectDir string, result runner.Result, invocation semanticInvocation, summary *Summary, hints []string, warnings *boundedStringCollector) {
	if !summary.Success || result.StartTime.IsZero() {
		return
	}

	generated := artifacts.FindGeneratedWithMetadata(projectDir, result.StartTime, result.ArtifactSnapshot, hints)
	found := generated.Artifacts
	metadata := generated.Metadata
	classCount := generated.ClassCount
	codegenCount := generated.CodegenCount
	if len(found) == 0 && shouldReportAvailableArtifacts(invocation) {
		prefixes := commandProjectPrefixes(invocation)
		available := artifacts.FindAvailableScopedWithMetadata(projectDir, hints, prefixes)
		// Gradle project paths can be remapped in settings. Scope is therefore a
		// preference, but an unscoped fallback is accepted only when it finds one
		// complete candidate; multiple candidates are ambiguous and remain scoped.
		if len(prefixes) > 0 && len(available.Artifacts) == 0 {
			unscoped := artifacts.FindAvailableWithMetadata(projectDir, hints)
			if unscoped.Metadata.Discovered == 1 && len(unscoped.Artifacts) == 1 && !unscoped.Metadata.Truncated && unscoped.Metadata.ErrorCount == 0 {
				available = unscoped
			}
		}
		found = available.Artifacts
		metadata = artifacts.MergeScanMetadata(metadata, available.Metadata)
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

func shouldReportAvailableArtifacts(invocation semanticInvocation) bool {
	for _, arg := range invocation.TaskSelectors {
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

func commandProjectPrefixes(invocation semanticInvocation) []string {
	seen := make(map[string]struct{})
	prefixes := make([]string, 0)
	for _, arg := range invocation.TaskSelectors {
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
	errorBytes      int64
	errorsTruncated bool
	walkTruncated   bool
	truncated       bool
}

func enrichWithJUnitResults(projectDir string, result runner.Result, invocation semanticInvocation, summary *Summary, failedTests, important, warnings *boundedStringCollector) {
	if !summary.Success && !shouldReadJUnitReportsOnFailure(summary) {
		return
	}
	selection := selectJUnitReportFiles(projectDir, result.StartTime, summary.Success && shouldFallbackToAvailableJUnitReports(invocation))
	metadata := &JUnitScanMetadata{
		Discovered:      selection.discovered,
		Errors:          append([]string(nil), selection.errors...),
		ErrorCount:      selection.errorCount,
		ErrorBytes:      selection.errorBytes,
		ErrorsTruncated: selection.errorsTruncated,
		WalkTruncated:   selection.walkTruncated,
		Truncated:       selection.truncated,
	}
	passedCount := 0
	failedCount := 0
	for _, path := range selection.files {
		content, truncated, err := readJUnitReport(path)
		if err != nil {
			addJUnitScanError(metadata, projectDir, path, err)
			continue
		}
		if truncated {
			metadata.FileBytesTruncated = true
			metadata.Truncated = true
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
			failedTests.add(failedTestName)

			detail := buildJUnitFailureDetail(failedTestName, failure)
			important.add(detail)

			if location := extractRelevantStackFrame(failure.Body); location != "" {
				important.add(location)
			}
		}
	}
	metadata.Skipped = metadata.Discovered - metadata.Parsed
	if metadata.Skipped < 0 {
		metadata.Skipped = 0
	}
	if metadata.Parsed > 0 || metadata.ErrorCount > 0 || metadata.Truncated {
		summary.JUnitScan = metadata
	}
	if summary.JUnitScan != nil && (metadata.Truncated || metadata.WalkTruncated || metadata.ErrorCount > 0) {
		message := fmt.Sprintf("JUnit report scan incomplete: discovered %d, parsed %d, skipped %d", metadata.Discovered, metadata.Parsed, metadata.Skipped)
		switch {
		case metadata.FileBytesTruncated && selection.truncated:
			message += " (file byte and reporting limits reached)"
		case metadata.FileBytesTruncated:
			message += " (file byte limit reached)"
		case metadata.Truncated:
			message += " (truncated at the reporting limit)"
		case metadata.WalkTruncated:
			message += " (walk limit reached)"
		}
		addEnrichmentWarning(summary, warnings, message)
	}

	if passedCount > 0 || failedCount > 0 {
		summary.PassedTestCount = passedCount
		summary.FailedTestCount = failedCount
	}
}

func readJUnitReport(path string) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()

	content, err := io.ReadAll(io.LimitReader(file, int64(maxJUnitFileBytes)+1))
	if err != nil {
		return nil, false, err
	}
	if len(content) > maxJUnitFileBytes {
		return nil, true, nil
	}
	return content, false, nil
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
	message := relativeScanErrorPath(projectDir, path) + ": " + sanitizeScanErrorText(projectDir, err.Error())
	retainedBytes := int64(0)
	for _, existing := range metadata.Errors {
		retainedBytes += int64(len(existing))
	}
	if retainedBytes+int64(len(message)) > int64(maxJUnitScanErrorBytes) {
		metadata.ErrorsTruncated = true
		return
	}
	metadata.Errors = append(metadata.Errors, message)
	metadata.ErrorBytes += int64(len(message))
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

func addEnrichmentWarning(summary *Summary, warnings *boundedStringCollector, message string) {
	if warnings.add(message) {
		summary.WarningCount++
	}
}

// findJUnitReportFiles evaluates freshness while walking, before applying the
// report cap. Thus stale reports neither consume report capacity nor create
// incomplete-scan metadata. The walk itself is bounded independently.
func findJUnitReportFiles(projectDir string, startedAt time.Time, freshOnly bool) junitReportSelection {
	selection := junitReportSelection{files: make([]string, 0, maxJUnitReportFiles)}
	threshold := startedAt.Add(-junitTimeSkew)
	walked := 0
	_ = filepath.WalkDir(projectDir, func(path string, entry fs.DirEntry, walkErr error) error {
		walked++
		if walked > maxJUnitWalkEntries {
			selection.walkTruncated = true
			return fs.SkipAll
		}
		if walkErr != nil {
			if isJUnitScanErrorPath(projectDir, path) {
				addSelectionError(&selection, projectDir, path, walkErr)
			}
			return nil
		}
		if isJUnitReportPath(path, entry.Name()) {
			if freshOnly {
				info, err := entry.Info()
				if err != nil {
					addSelectionError(&selection, projectDir, path, err)
					return nil
				}
				if info.ModTime().Before(threshold) {
					return nil
				}
			}
			selection.discovered++
			if len(selection.files) < maxJUnitReportFiles {
				selection.files = append(selection.files, path)
			} else {
				selection.truncated = true
			}
			return nil
		}
		if entry.IsDir() && artifacts.ShouldSkipDir(entry) {
			return filepath.SkipDir
		}
		return nil
	})
	return selection
}

func selectJUnitReportFiles(projectDir string, startedAt time.Time, allowFallback bool) junitReportSelection {
	if startedAt.IsZero() {
		if !allowFallback {
			return junitReportSelection{files: make([]string, 0, maxJUnitReportFiles)}
		}
		return findJUnitReportFiles(projectDir, startedAt, false)
	}

	selection := findJUnitReportFiles(projectDir, startedAt, true)
	if len(selection.files) > 0 || !allowFallback {
		return selection
	}
	return findJUnitReportFiles(projectDir, startedAt, false)
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
	message := relativeScanErrorPath(projectDir, path) + ": " + sanitizeScanErrorText(projectDir, err.Error())
	if selection.errorBytes+int64(len(message)) > int64(maxJUnitScanErrorBytes) {
		selection.errorsTruncated = true
		return
	}
	selection.errors = append(selection.errors, message)
	selection.errorBytes += int64(len(message))
}

// scanBuildScanURLs streams URL candidates and retains only build scans. This
// keeps unrelated links from consuming the build-scan capacity and leaves the
// collector as the sole bounded state owner and truncation authority.
func scanBuildScanURLs(text string, items *buildScanURLCollector) bool {
	found := false
	for offset := 0; offset < len(text); {
		match := urlPattern.FindStringIndex(text[offset:])
		if match == nil {
			break
		}
		start, end := offset+match[0], offset+match[1]
		url := strings.TrimRight(text[start:end], ".,;:")
		if isBuildScanURL(url) {
			items.add(url)
			found = true
		}
		offset = end
	}
	return found
}

func isBuildScanURL(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "/s/")
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

func shouldFallbackToAvailableJUnitReports(invocation semanticInvocation) bool {
	for _, arg := range invocation.TaskSelectors {
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
