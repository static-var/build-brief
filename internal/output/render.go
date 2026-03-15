package output

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"build-brief/internal/reducer"
)

func RenderHuman(w io.Writer, summary reducer.Summary) error {
	bw := bufio.NewWriter(w)
	statusLine := renderStatusLine(summary)
	if _, err := fmt.Fprintln(bw, statusLine); err != nil {
		return err
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
		return bw.Flush()
	}

	if _, err := fmt.Fprintf(bw, "Command: %s\n", summary.CommandLine); err != nil {
		return err
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

	if _, err := fmt.Fprintf(bw, "Raw log: %s\n", summary.RawLogPath); err != nil {
		return err
	}

	return bw.Flush()
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
