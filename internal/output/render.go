package output

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"build-brief/internal/reducer"
)

// RenderGitHubAnnotations emits generic workflow commands only. Callers decide
// whether the current environment is GitHub Actions.
func RenderGitHubAnnotations(w io.Writer, summary reducer.Summary) error {
	properties := githubAnnotationProperties()
	if !summary.Success {
		if _, err := fmt.Fprintf(w, "::error %s::%s\n", properties, escapeGitHubWorkflowCommand("build-brief: Gradle build failed; see human summary and raw log")); err != nil {
			return err
		}
	}
	if !summary.Success && summaryPartial(summary) {
		if _, err := fmt.Fprintf(w, "::warning %s::%s\n", properties, escapeGitHubWorkflowCommand("build-brief: summary may be partial; see human summary and raw log")); err != nil {
			return err
		}
	}
	return nil
}

func githubAnnotationProperties() string {
	return fmt.Sprintf("file=%s,line=%s,endLine=%s,title=%s",
		escapeGitHubWorkflowCommandProperty("build-brief"),
		escapeGitHubWorkflowCommandProperty("1"),
		escapeGitHubWorkflowCommandProperty("1"),
		escapeGitHubWorkflowCommandProperty("build-brief"),
	)
}

func escapeGitHubWorkflowCommand(message string) string {
	message = strings.ReplaceAll(message, "%", "%25")
	message = strings.ReplaceAll(message, "\r", "%0D")
	message = strings.ReplaceAll(message, "\n", "%0A")
	return strings.ReplaceAll(message, ":", "%3A")
}

func escapeGitHubWorkflowCommandProperty(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	value = strings.ReplaceAll(value, "\n", "%0A")
	value = strings.ReplaceAll(value, ":", "%3A")
	return strings.ReplaceAll(value, ",", "%2C")
}

// SanitizeGitHubHumanSummary prevents untrusted summary content from becoming
// workflow commands. GitHub-only rendering normalizes all line boundaries and
// prefixes a non-whitespace sentinel because the runner trims leading Unicode
// whitespace before parsing commands.
func SanitizeGitHubHumanSummary(rendered string) string {
	rendered = strings.ReplaceAll(rendered, "\r\n", "\n")
	rendered = strings.ReplaceAll(rendered, "\r", "\n")
	lines := strings.Split(rendered, "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), "::") {
			lines[i] = "| " + line
		}
	}
	return strings.Join(lines, "\n")
}

func summaryPartial(summary reducer.Summary) bool {
	return summary.RawInput != nil && summary.RawInput.Partial ||
		summary.Reducer != nil && summary.Reducer.Partial
}

func RenderHuman(w io.Writer, summary reducer.Summary) error {
	bw := bufio.NewWriter(w)
	statusLine := renderStatusLine(summary)
	if len(summary.ReportLines) > 0 {
		for _, line := range summary.ReportLines {
			if _, err := fmt.Fprintln(bw, line); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(bw); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(bw, statusLine); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(bw, statusLine); err != nil {
			return err
		}
	}

	if raw := summary.RawInput; raw != nil && raw.Partial {
		if _, err := fmt.Fprintf(bw, "WARNING: raw input incomplete: %d line(s) exceeded the %d-byte reducer limit; summary fields may be partial.\n", raw.TruncatedLines, 1<<20); err != nil {
			return err
		}
	}
	if reducer := summary.Reducer; reducer != nil && reducer.Partial && len(reducer.PartialFields) > 0 {
		if _, err := fmt.Fprintf(bw, "WARNING: reducer summary partial for: %s\n", strings.Join(reducer.PartialFields, ", ")); err != nil {
			return err
		}
	}

	if summary.PassedTestCount > 0 || summary.FailedTestCount > 0 {
		if _, err := fmt.Fprintf(bw, "Tests: %d passed, %d failed\n", summary.PassedTestCount, summary.FailedTestCount); err != nil {
			return err
		}
	}

	if summary.WarningCount > 0 {
		if _, err := fmt.Fprintf(bw, "Warnings: %d\n", summary.WarningCount); err != nil {
			return err
		}
		for _, warning := range summary.Warnings {
			if _, err := fmt.Fprintf(bw, "  - %s\n", warning); err != nil {
				return err
			}
		}
	}

	if scan := summary.JUnitScan; scan != nil {
		line := fmt.Sprintf("JUnit reports: %d discovered, %d parsed, %d skipped", scan.Discovered, scan.Parsed, scan.Skipped)
		if scan.FileBytesTruncated {
			line += " (file byte limit reached)"
		} else if scan.Truncated {
			line += " (truncated at the reporting limit)"
		}
		if scan.ErrorsTruncated {
			line += " (error details truncated)"
		}
		if _, err := fmt.Fprintln(bw, line); err != nil {
			return err
		}
		if scan.SkippedTests > 0 {
			if _, err := fmt.Fprintf(bw, "  - %d skipped tests\n", scan.SkippedTests); err != nil {
				return err
			}
		}
		if len(scan.Errors) > 0 {
			label := "JUnit report scan errors:"
			if scan.ErrorCount > 0 {
				label = fmt.Sprintf("JUnit report scan errors: %d total", scan.ErrorCount)
			}
			if _, err := fmt.Fprintln(bw, label); err != nil {
				return err
			}
			for _, scanError := range scan.Errors {
				if _, err := fmt.Fprintf(bw, "  - %s\n", scanError); err != nil {
					return err
				}
			}
			if scan.ErrorsTruncated {
				if _, err := fmt.Fprintln(bw, "  - additional scan errors omitted"); err != nil {
					return err
				}
			}
		}
	}

	if scan := summary.ArtifactScan; scan != nil {
		line := fmt.Sprintf("Artifacts scan: %d discovered, %d reported, %d skipped", scan.Discovered, scan.Reported, scan.Skipped)
		if scan.Truncated {
			line += " (truncated at the reporting limit)"
		}
		if scan.ErrorsTruncated {
			line += " (error details truncated)"
		}
		if _, err := fmt.Fprintln(bw, line); err != nil {
			return err
		}
		if len(scan.Errors) > 0 {
			label := "Artifact scan errors:"
			if scan.ErrorCount > 0 {
				label = fmt.Sprintf("Artifact scan errors: %d total", scan.ErrorCount)
			}
			if _, err := fmt.Fprintln(bw, label); err != nil {
				return err
			}
			for _, scanError := range scan.Errors {
				if _, err := fmt.Fprintf(bw, "  - %s\n", scanError); err != nil {
					return err
				}
			}
			if scan.ErrorsTruncated {
				if _, err := fmt.Fprintln(bw, "  - additional scan errors omitted"); err != nil {
					return err
				}
			}
		}
	}

	if scan := summary.ArtifactHintScan; scan != nil && scan.Truncated {
		line := fmt.Sprintf("Artifact hints: %d observed, %d retained, %d omitted", scan.Observed, scan.Retained, scan.Omitted)
		line += " (truncated at the retention limit)"
		if _, err := fmt.Fprintln(bw, line); err != nil {
			return err
		}
	}

	if len(summary.BuildScanURLs) > 0 {
		label := "Build scan:"
		if len(summary.BuildScanURLs) > 1 {
			label = "Build scans:"
		}
		if _, err := fmt.Fprintln(bw, label); err != nil {
			return err
		}
		for _, url := range summary.BuildScanURLs {
			if _, err := fmt.Fprintf(bw, "  - %s\n", url); err != nil {
				return err
			}
		}
	}

	if summary.ConfigCacheStatus != "" && len(summary.ConfigCacheProblems) == 0 && summary.ConfigCacheReportURL == "" {
		if _, err := fmt.Fprintf(bw, "Configuration cache entry %s.\n", summary.ConfigCacheStatus); err != nil {
			return err
		}
	}

	if len(summary.ConfigCacheProblems) > 0 || summary.ConfigCacheReportURL != "" {
		if _, err := fmt.Fprintln(bw, "Configuration cache:"); err != nil {
			return err
		}
		for _, p := range summary.ConfigCacheProblems {
			if _, err := fmt.Fprintf(bw, "  - %s\n", p); err != nil {
				return err
			}
		}
		if summary.ConfigCacheReportURL != "" {
			if _, err := fmt.Fprintf(bw, "  Report: %s\n", summary.ConfigCacheReportURL); err != nil {
				return err
			}
		}
	}

	if customMatches := nonEmptyCustomMatches(summary.CustomMatches); len(customMatches) > 0 {
		if _, err := fmt.Fprintln(bw, "Custom matches:"); err != nil {
			return err
		}
		for _, group := range customMatches {
			if _, err := fmt.Fprintf(bw, "  %s:\n", group.Name); err != nil {
				return err
			}
			for _, match := range group.Matches {
				if _, err := fmt.Fprintf(bw, "    - %s\n", match); err != nil {
					return err
				}
			}
		}
	}

	if len(summary.Artifacts) > 0 {
		if _, err := fmt.Fprintln(bw, "Artifacts:"); err != nil {
			return err
		}
		for _, artifact := range summary.Artifacts {
			if _, err := fmt.Fprintf(bw, "  - %s: %s (%s)\n", artifact.Kind, artifact.Path, formatArtifactSize(artifact.SizeBytes)); err != nil {
				return err
			}
		}
	}

	if summary.GeneratedClassFileCount > 0 || summary.GeneratedCodegenFileCount > 0 {
		if _, err := fmt.Fprintln(bw, "Compilation outputs omitted:"); err != nil {
			return err
		}
		if summary.GeneratedClassFileCount > 0 {
			if _, err := fmt.Fprintf(bw, "  - %s .class %s generated.\n", formatCount(summary.GeneratedClassFileCount), pluralize(summary.GeneratedClassFileCount, "file", "files")); err != nil {
				return err
			}
		}
		if summary.GeneratedCodegenFileCount > 0 {
			if _, err := fmt.Fprintf(bw, "  - %s generated source/codegen %s updated.\n", formatCount(summary.GeneratedCodegenFileCount), pluralize(summary.GeneratedCodegenFileCount, "file", "files")); err != nil {
				return err
			}
		}
	}

	if summary.Success {
		if importantLines := filteredImportantLines(summary, statusLine); len(importantLines) > 0 {
			if _, err := fmt.Fprintln(bw, "Highlights:"); err != nil {
				return err
			}
			for _, line := range importantLines {
				if _, err := fmt.Fprintf(bw, "  - %s\n", line); err != nil {
					return err
				}
			}
		}
		return bw.Flush()
	}

	if _, err := fmt.Fprintf(bw, "Command: %s\n", summary.CommandLine); err != nil {
		return err
	}

	if len(summary.Diagnostics) > 0 {
		if err := renderDiagnostic(bw, summary.Diagnostics[0]); err != nil {
			return err
		}
	}

	if len(summary.FailedTasks) > 0 {
		if _, err := fmt.Fprintln(bw, "Failed tasks:"); err != nil {
			return err
		}
		for _, task := range summary.FailedTasks {
			if _, err := fmt.Fprintf(bw, "  - %s\n", task); err != nil {
				return err
			}
		}
	}

	if len(summary.FailedTests) > 0 {
		if _, err := fmt.Fprintln(bw, "Failed tests:"); err != nil {
			return err
		}
		for _, test := range summary.FailedTests {
			if _, err := fmt.Fprintf(bw, "  - %s\n", test); err != nil {
				return err
			}
		}
	}

	if len(summary.Diagnostics) == 0 {
		if importantLines := filteredImportantLines(summary, statusLine); len(importantLines) > 0 {
			if _, err := fmt.Fprintln(bw, "Highlights:"); err != nil {
				return err
			}
			for _, line := range importantLines {
				if _, err := fmt.Fprintf(bw, "  - %s\n", line); err != nil {
					return err
				}
			}
		}
	}

	if _, err := fmt.Fprintf(bw, "Raw log: %s\n", summary.RawLogPath); err != nil {
		return err
	}

	return bw.Flush()
}

func renderDiagnostic(w io.Writer, diagnostic reducer.Diagnostic) error {
	if _, err := fmt.Fprintf(w, "Diagnosis: %s\n", diagnostic.Summary); err != nil {
		return err
	}
	if len(diagnostic.Evidence) > 0 {
		if _, err := fmt.Fprintln(w, "Evidence:"); err != nil {
			return err
		}
		for _, evidence := range diagnostic.Evidence {
			if _, err := fmt.Fprintf(w, "  - %s\n", evidence); err != nil {
				return err
			}
		}
	}
	if len(diagnostic.NextChecks) > 0 {
		if _, err := fmt.Fprintln(w, "Next checks:"); err != nil {
			return err
		}
		for _, nextCheck := range diagnostic.NextChecks {
			if _, err := fmt.Fprintf(w, "  - %s\n", nextCheck); err != nil {
				return err
			}
		}
	}
	return nil
}

func nonEmptyCustomMatches(groups []reducer.CustomMatchResult) []reducer.CustomMatchResult {
	nonEmpty := make([]reducer.CustomMatchResult, 0, len(groups))
	for _, group := range groups {
		if len(group.Matches) > 0 {
			nonEmpty = append(nonEmpty, group)
		}
	}
	return nonEmpty
}

func RenderRaw(w io.Writer, rawLogPath string) error {
	file, err := os.Open(rawLogPath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(w, file)
	return err
}

func renderStatusLine(summary reducer.Summary) string {
	statusLine := strings.TrimSpace(summary.BuildStatusLine)
	if statusLine != "" {
		return statusLine
	}

	if summary.Success {
		return "BUILD SUCCESSFUL"
	}
	return "BUILD FAILED"
}

func filteredImportantLines(summary reducer.Summary, statusLine string) []string {
	filtered := make([]string, 0, len(summary.ImportantLines))
	for _, line := range summary.ImportantLines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == statusLine {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func formatArtifactSize(sizeBytes int64) string {
	const unit = 1024
	if sizeBytes < unit {
		return fmt.Sprintf("%d B", sizeBytes)
	}

	divisor := int64(unit)
	suffix := "KB"
	for _, next := range []string{"MB", "GB", "TB"} {
		if sizeBytes < divisor*unit {
			break
		}
		divisor *= unit
		suffix = next
	}
	return fmt.Sprintf("%.1f %s", float64(sizeBytes)/float64(divisor), suffix)
}

func formatCount(count int) string {
	return fmt.Sprintf("%d", count)
}

func pluralize(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
