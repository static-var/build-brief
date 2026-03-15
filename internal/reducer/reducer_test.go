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

func TestReduceContextCaptureIgnoresBlankLines(t *testing.T) {
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
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"",
			"Execution failed for task ':app:test'.",
			"",
			"> The failing test report is available at: build/reports/tests/test/index.html",
			"BUILD FAILED in 2s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce blank-line context log: %v", err)
	}

	if !contains(summary.ImportantLines, "Execution failed for task ':app:test'.") {
		t.Fatalf("expected first context line in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "> The failing test report is available at: build/reports/tests/test/index.html") {
		t.Fatalf("expected second context line in important lines: %v", summary.ImportantLines)
	}
}

func TestReduceHandlesVeryLongLines(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			strings.Repeat("x", 1_500_000),
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce long-line log: %v", err)
	}
	if summary.BuildStatusLine != "BUILD SUCCESSFUL in 1s" {
		t.Fatalf("unexpected build status line: %q", summary.BuildStatusLine)
	}
}

func TestReduceEnrichesFailedTestsFromJUnitXml(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "app", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}

	report := `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="example.FailingTest" tests="1" skipped="0" failures="1" errors="0">
  <testcase name="intentionalFailure()" classname="example.FailingTest">
    <failure message="org.opentest4j.AssertionFailedError: expected: &lt;expected&gt; but was: &lt;hello, build-brief&gt;" type="org.opentest4j.AssertionFailedError">org.opentest4j.AssertionFailedError: expected: &lt;expected&gt; but was: &lt;hello, build-brief&gt;
	at org.junit.jupiter.api.Assertions.assertEquals(Assertions.java:1145)
	at example.FailingTest.intentionalFailure(FailingTest.java:10)
</failure>
  </testcase>
</testsuite>`
	if err := os.WriteFile(filepath.Join(reportDir, "TEST-example.FailingTest.xml"), []byte(report), 0o644); err != nil {
		t.Fatalf("write junit report: %v", err)
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "test", "--tests", "example.FailingTest"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :test FAILED",
			"FailingTest > intentionalFailure() FAILED",
			"org.opentest4j.AssertionFailedError at FailingTest.java:10",
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Execution failed for task ':test'.",
			"> There were failing tests. See the report at: file:///tmp/project/build/reports/tests/test/index.html",
			"BUILD FAILED in 500ms",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce junit-enriched failure log: %v", err)
	}

	if !contains(summary.FailedTests, "FailingTest > intentionalFailure()") {
		t.Fatalf("expected failed test to be present: %v", summary.FailedTests)
	}
	if !contains(summary.ImportantLines, "FailingTest > intentionalFailure(): org.opentest4j.AssertionFailedError: expected: <expected> but was: <hello, build-brief>") {
		t.Fatalf("expected assertion detail in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "at example.FailingTest.intentionalFailure(FailingTest.java:10)") {
		t.Fatalf("expected user stack frame in important lines: %v", summary.ImportantLines)
	}
}

func TestReduceIgnoresMalformedJUnitXml(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "module", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir malformed report dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "TEST-bad.xml"), []byte("<testsuite><broken>"), 0o644); err != nil {
		t.Fatalf("write malformed junit report: %v", err)
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :test FAILED",
			"ExampleTest > works FAILED",
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Execution failed for task ':test'.",
			"BUILD FAILED in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce with malformed junit xml: %v", err)
	}

	if !contains(summary.FailedTests, "ExampleTest > works") {
		t.Fatalf("expected console-derived failed test to remain: %v", summary.FailedTests)
	}
}

func TestReduceCapturesJavaSyntaxErrorFromConsole(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "compileJava"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :compileJava FAILED",
			"/tmp/project/src/main/java/example/App.java:5: error: ';' expected",
			"        System.out.println(greeting(\"build-brief\"))",
			"                                                   ^",
			"1 error",
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Execution failed for task ':compileJava'.",
			"> Compilation failed; see the compiler output below.",
			"  /tmp/project/src/main/java/example/App.java:5: error: ';' expected",
			"      System.out.println(greeting(\"build-brief\"))",
			"                                                 ^",
			"  1 error",
			"BUILD FAILED in 300ms",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce compile syntax error log: %v", err)
	}

	if !contains(summary.FailedTasks, ":compileJava") {
		t.Fatalf("expected failed compile task: %v", summary.FailedTasks)
	}
	if !contains(summary.ImportantLines, "/tmp/project/src/main/java/example/App.java:5: error: ';' expected") {
		t.Fatalf("expected syntax error line in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "^") {
		t.Fatalf("expected caret line in important lines: %v", summary.ImportantLines)
	}
}

func TestReduceCapturesJavaSymbolErrorFromConsole(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "compileJava"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :compileJava FAILED",
			"/tmp/project/src/main/java/example/App.java:9: error: cannot find symbol",
			"        return missingSymbol(name);",
			"               ^",
			"  symbol:   method missingSymbol(String)",
			"  location: class App",
			"1 error",
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Execution failed for task ':compileJava'.",
			"> Compilation failed; see the compiler output below.",
			"  /tmp/project/src/main/java/example/App.java:9: error: cannot find symbol",
			"      return missingSymbol(name);",
			"             ^",
			"    symbol:   method missingSymbol(String)",
			"    location: class App",
			"  1 error",
			"BUILD FAILED in 300ms",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce compile symbol error log: %v", err)
	}

	if !contains(summary.ImportantLines, "/tmp/project/src/main/java/example/App.java:9: error: cannot find symbol") {
		t.Fatalf("expected symbol error line in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "symbol:   method missingSymbol(String)") {
		t.Fatalf("expected symbol detail in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "location: class App") {
		t.Fatalf("expected location detail in important lines: %v", summary.ImportantLines)
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
