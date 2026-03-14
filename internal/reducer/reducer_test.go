package reducer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"build-brief/internal/gradle"
	"build-brief/internal/runner"
)

func TestReduceFailureSummary(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: 3*time.Second + 250*time.Millisecond,
		RawLogPath: writeTestLog(t, []string{
			"> Task :app:test FAILED",
			"ExampleTest > works FAILED",
			"warning: unstable API usage",
			"Deprecated Gradle features were used in this build, making it incompatible with Gradle 9.0.",
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Execution failed for task ':app:test'.",
			"BUILD FAILED in 3s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce failure log: %v", err)
	}

	if summary.Success {
		t.Fatal("expected failed summary")
	}
	if summary.BuildStatusLine != "BUILD FAILED in 3s" {
		t.Fatalf("unexpected build status line: %q", summary.BuildStatusLine)
	}
	if summary.SchemaVersion != "v1" {
		t.Fatalf("unexpected schema version: %q", summary.SchemaVersion)
	}
	if summary.WarningCount != 2 {
		t.Fatalf("expected 2 warnings, got %d", summary.WarningCount)
	}
	if len(summary.FailedTasks) != 1 || summary.FailedTasks[0] != ":app:test" {
		t.Fatalf("unexpected failed tasks: %v", summary.FailedTasks)
	}
	if len(summary.FailedTests) != 1 || summary.FailedTests[0] != "ExampleTest > works" {
		t.Fatalf("unexpected failed tests: %v", summary.FailedTests)
	}
	if len(summary.ImportantLines) == 0 {
		t.Fatal("expected important lines")
	}
}

func TestReduceSuccessSummary(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: 5 * time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :compileKotlin",
			"BUILD SUCCESSFUL in 5s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce success log: %v", err)
	}

	if !summary.Success {
		t.Fatal("expected successful summary")
	}
	if summary.BuildStatusLine != "BUILD SUCCESSFUL in 5s" {
		t.Fatalf("unexpected build status line: %q", summary.BuildStatusLine)
	}
	if summary.WarningCount != 0 {
		t.Fatalf("expected 0 warnings, got %d", summary.WarningCount)
	}
	if summary.Duration != "5s" {
		t.Fatalf("unexpected duration: %s", summary.Duration)
	}
	if summary.FailedTasks == nil || summary.FailedTests == nil || summary.Warnings == nil || summary.ImportantLines == nil {
		t.Fatal("expected summary slices to be initialized")
	}
}

func TestReduceCapturesContextAndStripsANSI(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"\x1b[31mwarning: noisy warning 1\x1b[0m",
			"\x1b[31mwarning: noisy warning 2\x1b[0m",
			"\x1b[31mwarning: noisy warning 3\x1b[0m",
			"\x1b[31mwarning: noisy warning 4\x1b[0m",
			"\x1b[31mwarning: noisy warning 5\x1b[0m",
			"\x1b[31mwarning: noisy warning 6\x1b[0m",
			"\x1b[31mwarning: noisy warning 7\x1b[0m",
			"\x1b[31mwarning: noisy warning 8\x1b[0m",
			"\x1b[31mwarning: noisy warning 9\x1b[0m",
			"\x1b[31mwarning: noisy warning 10\x1b[0m",
			"\x1b[31mFAILURE: Build failed with an exception.\x1b[0m",
			"* What went wrong:",
			"Execution failed for task ':app:test'.",
			"> There were failing tests. See the report at: build/reports/tests/test/index.html",
			"BUILD FAILED in 2s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce ansi/context log: %v", err)
	}

	if summary.WarningCount != 10 {
		t.Fatalf("expected 10 warnings, got %d", summary.WarningCount)
	}
	if !contains(summary.ImportantLines, "FAILURE: Build failed with an exception.") {
		t.Fatalf("expected failure line in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "Execution failed for task ':app:test'.") {
		t.Fatalf("expected context line in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "> There were failing tests. See the report at: build/reports/tests/test/index.html") {
		t.Fatalf("expected follow-up context line in important lines: %v", summary.ImportantLines)
	}
}

func writeTestLog(t *testing.T, lines []string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "build-brief.log")
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test log: %v", err)
	}

	return path
}
