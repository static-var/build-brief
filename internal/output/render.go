package output

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"build-brief/internal/reducer"
)

func RenderHuman(w io.Writer, summary reducer.Summary) error {
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	statusLabel := "SUCCESS"
	if !summary.Success {
		statusLabel = "FAILED"
	}

	if _, err := fmt.Fprintf(bw, "%s in %s (exit %d)\n", statusLabel, summary.Duration, summary.ExitCode); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(bw, "Command: %s\n", summary.CommandLine); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(bw, "Resolver: %s\n", summary.Source); err != nil {
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

	if len(summary.ImportantLines) > 0 {
		if _, err := fmt.Fprintln(bw, "Highlights:"); err != nil {
			return err
		}
		for _, line := range summary.ImportantLines {
			if _, err := fmt.Fprintf(bw, "  - %s\n", line); err != nil {
				return err
			}
		}
	}

	if _, err := fmt.Fprintf(bw, "Raw log: %s\n", summary.RawLogPath); err != nil {
		return err
	}

	return nil
}

func RenderJSON(w io.Writer, summary reducer.Summary) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(summary)
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
