package reducer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"build-brief/internal/artifacts"
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
		ProjectDir: t.TempDir(),
		Source:     gradle.SourceSystem,
	}
	startTime := time.Now()
	reportDir := filepath.Join(command.ProjectDir, "app", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}
	report := `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="example.SampleTest" tests="3" skipped="1" failures="1" errors="0">
  <testcase name="passesOne" classname="example.SampleTest"></testcase>
  <testcase name="passesTwo" classname="example.SampleTest"></testcase>
  <testcase name="fails" classname="example.SampleTest"><failure message="boom" type="AssertionError">boom</failure></testcase>
  <testcase name="skipped" classname="example.SampleTest"><skipped/></testcase>
</testsuite>`
	if err := os.WriteFile(filepath.Join(reportDir, "TEST-example.SampleTest.xml"), []byte(report), 0o644); err != nil {
		t.Fatalf("write junit report: %v", err)
	}
	result := runner.Result{
		ExitCode:  0,
		Duration:  5 * time.Second,
		StartTime: startTime,
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
	if summary.PassedTestCount != 2 || summary.FailedTestCount != 1 {
		t.Fatalf("expected junit counts 2 passed / 1 failed, got %d passed / %d failed", summary.PassedTestCount, summary.FailedTestCount)
	}
	if summary.JUnitScan == nil || summary.JUnitScan.Discovered != 1 || summary.JUnitScan.Parsed != 1 || summary.JUnitScan.Skipped != 0 || summary.JUnitScan.SkippedTests != 1 || summary.JUnitScan.Truncated {
		t.Fatalf("unexpected complete junit scan metadata: %+v", summary.JUnitScan)
	}
	if summary.FailedTasks == nil || summary.FailedTests == nil || summary.Warnings == nil || summary.ImportantLines == nil {
		t.Fatal("expected summary slices to be initialized")
	}
	if summary.Artifacts == nil {
		t.Fatal("expected artifacts slice to be initialized")
	}
}

func TestReduceCapturesDevelocityBuildScanURL(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :build",
			"Publishing Build Scan to Develocity...",
			"https://develocity.internal.example/s/abc123",
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce build scan log: %v", err)
	}

	if len(summary.BuildScanURLs) != 1 || summary.BuildScanURLs[0] != "https://develocity.internal.example/s/abc123" {
		t.Fatalf("expected Develocity build scan URL, got %v", summary.BuildScanURLs)
	}
}

func TestReduceCapturesSameLineBuildScanURL(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"Build scan: https://develocity.internal.example/s/same-line.",
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce same-line build scan log: %v", err)
	}

	if len(summary.BuildScanURLs) != 1 || summary.BuildScanURLs[0] != "https://develocity.internal.example/s/same-line" {
		t.Fatalf("expected same-line build scan URL without trailing punctuation, got %v", summary.BuildScanURLs)
	}
}

func TestReduceDoesNotTreatGradleHelpURLAsBuildScan(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"* Try:",
			"> Run with --scan to get full insights from a Build Scan (powered by Develocity).",
			"> Get more help at https://help.gradle.org.",
			"BUILD FAILED in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce gradle help URL log: %v", err)
	}

	if len(summary.BuildScanURLs) != 0 {
		t.Fatalf("did not expect Gradle help URL as build scan, got %v", summary.BuildScanURLs)
	}
}

func TestReduceDoesNotTreatUnrelatedURLsAsBuildScans(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"Download https://repo.maven.apache.org/maven2/com/example/library.jar",
			"> Android Gradle plugin requires a newer compileSdk. See https://developer.android.com/build/releases/gradle-plugin for details.",
			"BUILD FAILED in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce unrelated URL log: %v", err)
	}

	if len(summary.BuildScanURLs) != 0 {
		t.Fatalf("did not expect unrelated URLs as build scans, got %v", summary.BuildScanURLs)
	}
}

func TestReduceCapturesCustomRegexMatches(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "connectedCheck"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"Firebase Test Lab results: https://console.firebase.google.com/project/sample/testlab/histories/bh.123",
			"emulator.wtf run: https://app.emulator.wtf/runs/abc123.",
			"Duplicate emulator.wtf run: https://app.emulator.wtf/runs/abc123.",
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := ReduceWithOptions(command, result, Options{
		CustomMatches: []CustomMatchRule{
			{Name: "Firebase Test Lab", Pattern: regexp.MustCompile(`https://console\.firebase\.google\.com/[^\s.]+(?:\.[^\s.]+)*`)},
			{Name: "emulator.wtf", Pattern: regexp.MustCompile(`https://app\.emulator\.wtf/[^\s.]+(?:\.[^\s.]+)*`)},
		},
	})
	if err != nil {
		t.Fatalf("reduce custom matches: %v", err)
	}

	if len(summary.CustomMatches) != 2 {
		t.Fatalf("expected two custom match groups, got %+v", summary.CustomMatches)
	}
	if summary.CustomMatches[0].Name != "Firebase Test Lab" ||
		len(summary.CustomMatches[0].Matches) != 1 ||
		summary.CustomMatches[0].Matches[0] != "https://console.firebase.google.com/project/sample/testlab/histories/bh.123" {
		t.Fatalf("unexpected Firebase matches: %+v", summary.CustomMatches[0])
	}
	if summary.CustomMatches[1].Name != "emulator.wtf" ||
		len(summary.CustomMatches[1].Matches) != 1 ||
		summary.CustomMatches[1].Matches[0] != "https://app.emulator.wtf/runs/abc123" {
		t.Fatalf("unexpected emulator.wtf matches: %+v", summary.CustomMatches[1])
	}
}

func TestReduceFallsBackToAvailableArtifactsForWarmAssemble(t *testing.T) {
	projectDir := t.TempDir()
	writeGeneratedFile(t, filepath.Join(projectDir, "androidApp", "build", "outputs", "apk", "debug", "androidApp-debug.apk"), "apk")
	writeGeneratedFile(t, filepath.Join(projectDir, "benchmark", "build", "outputs", "apk", "debug", "benchmark-debug.apk"), "apk")
	snapshot := artifacts.Capture(projectDir)

	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", ":androidApp:assembleDebug"},
		ProjectDir: projectDir,
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         3 * time.Second,
		StartTime:        time.Now(),
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"> Task :androidApp:assembleDebug UP-TO-DATE",
			"BUILD SUCCESSFUL in 3s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce warm assemble log: %v", err)
	}

	if !containsArtifact(summary.Artifacts, "APK", "androidApp/build/outputs/apk/debug/androidApp-debug.apk") {
		t.Fatalf("expected warm assemble to surface existing apk, got %+v", summary.Artifacts)
	}
	if containsArtifact(summary.Artifacts, "APK", "benchmark/build/outputs/apk/debug/benchmark-debug.apk") {
		t.Fatalf("did not expect warm assemble fallback to include unrelated module artifact, got %+v", summary.Artifacts)
	}
	if summary.ArtifactScan == nil || summary.ArtifactScan.Discovered != 1 || summary.ArtifactScan.Reported != 1 || summary.ArtifactScan.Skipped != 0 || summary.ArtifactScan.Truncated {
		t.Fatalf("expected truthful scoped artifact metadata, got %+v", summary.ArtifactScan)
	}
}

func TestReduceWarmFallbackRetainsHintUnderExcludedBuildSrcRoot(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "buildSrc", "build", "libs", "convention.jar")
	writeGeneratedFile(t, path, "jar")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat warm artifact: %v", err)
	}
	snapshot := artifacts.Capture(projectDir)

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", ":buildSrc:assemble"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         time.Second,
		StartTime:        info.ModTime().Add(2 * time.Second),
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"Generated output: ./buildSrc/build/libs/convention.jar",
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce warm excluded buildSrc artifact: %v", err)
	}
	if !containsArtifact(summary.Artifacts, "JAR", "buildSrc/build/libs/convention.jar") {
		t.Fatalf("expected warm hint fallback under excluded buildSrc root, got %+v", summary.Artifacts)
	}
	if summary.ArtifactScan == nil || summary.ArtifactScan.Discovered != 1 || summary.ArtifactScan.Reported != 1 || summary.ArtifactScan.Skipped != 0 || summary.ArtifactScan.Truncated {
		t.Fatalf("expected truthful warm fallback metadata, got %+v", summary.ArtifactScan)
	}
}

func TestReduceWarmFallbackAppliesProjectScopeBeforeArtifactCap(t *testing.T) {
	projectDir := t.TempDir()
	for i := 0; i < 25; i++ {
		writeGeneratedFile(t, filepath.Join(projectDir, "unrelated", "build", "outputs", "apk", "debug", fmt.Sprintf("unrelated-%03d.apk", i)), "apk")
	}
	writeGeneratedFile(t, filepath.Join(projectDir, "target", "build", "libs", "target.jar"), "jar")
	snapshot := artifacts.Capture(projectDir)

	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", ":target:assemble"},
		ProjectDir: projectDir,
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         3 * time.Second,
		StartTime:        time.Now(),
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"> Task :target:assemble UP-TO-DATE",
			"BUILD SUCCESSFUL in 3s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce scoped warm assemble log: %v", err)
	}
	if len(summary.Artifacts) != 1 || !containsArtifact(summary.Artifacts, "JAR", "target/build/libs/target.jar") {
		t.Fatalf("expected only scoped target artifact, got %+v", summary.Artifacts)
	}
	if summary.ArtifactScan == nil || summary.ArtifactScan.Discovered != 1 || summary.ArtifactScan.Reported != 1 || summary.ArtifactScan.Skipped != 0 || summary.ArtifactScan.Truncated {
		t.Fatalf("expected truthful scoped artifact metadata, got %+v", summary.ArtifactScan)
	}
}

func TestReduceWarmFallbackWithNoMatchingArtifactsStaysEmpty(t *testing.T) {
	projectDir := t.TempDir()
	for i := 0; i < 25; i++ {
		writeGeneratedFile(t, filepath.Join(projectDir, "unrelated", "build", "outputs", "apk", "debug", fmt.Sprintf("unrelated-%03d.apk", i)), "apk")
	}
	snapshot := artifacts.Capture(projectDir)

	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", ":missing:assemble"},
		ProjectDir: projectDir,
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         3 * time.Second,
		StartTime:        time.Now(),
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"> Task :missing:assemble UP-TO-DATE",
			"BUILD SUCCESSFUL in 3s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce empty scoped warm assemble log: %v", err)
	}
	if len(summary.Artifacts) != 0 {
		t.Fatalf("expected no artifacts for empty scope, got %+v", summary.Artifacts)
	}
	if summary.ArtifactScan != nil && (summary.ArtifactScan.Discovered != 0 || summary.ArtifactScan.Reported != 0 || summary.ArtifactScan.Skipped != 0) {
		t.Fatalf("expected empty scoped metadata, got %+v", summary.ArtifactScan)
	}
}

func TestReduceSanitizesJUnitScanErrorText(t *testing.T) {
	projectDir := t.TempDir()
	path := filepath.Join(projectDir, "module", "build", "test-results", "test", "TEST-bad.xml")
	metadata := &JUnitScanMetadata{}
	addJUnitScanError(metadata, projectDir, path, &fs.PathError{Op: "open", Path: path, Err: fmt.Errorf("permission denied")})

	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("marshal junit metadata: %v", err)
	}
	if strings.Contains(string(encoded), projectDir) {
		t.Fatalf("machine scan error leaked project directory %q: %s", projectDir, encoded)
	}
	for _, scanError := range metadata.Errors {
		if strings.Contains(scanError, projectDir) {
			t.Fatalf("scan error leaked project directory %q: %q", projectDir, scanError)
		}
	}
}

func TestReduceDoesNotFallbackToAvailableArtifactsForNonArtifactTasks(t *testing.T) {
	projectDir := t.TempDir()
	writeGeneratedFile(t, filepath.Join(projectDir, "androidApp", "build", "outputs", "apk", "debug", "androidApp-debug.apk"), "apk")
	snapshot := artifacts.Capture(projectDir)

	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: projectDir,
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         2 * time.Second,
		StartTime:        time.Now(),
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"> Task :test UP-TO-DATE",
			"BUILD SUCCESSFUL in 2s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce warm test log: %v", err)
	}

	if len(summary.Artifacts) != 0 {
		t.Fatalf("expected non-artifact task to avoid stale artifact fallback, got %+v", summary.Artifacts)
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

func TestReduceBoundsUnterminatedLineHintAcquisition(t *testing.T) {
	projectDir := t.TempDir()
	startTime := time.Now()
	latePath := filepath.Join(projectDir, "custom-output", "late.jar")
	writeGeneratedFile(t, latePath, "jar")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		StartTime:  startTime,
		RawLogPath: writeTestLog(t, []string{strings.Repeat("x", (1<<20)+1) + " ./custom-output/late.jar", "BUILD SUCCESSFUL in 1s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce oversized unterminated line: %v", err)
	}
	if summary.RawInput == nil || !summary.RawInput.Partial || summary.RawInput.TruncatedLines != 1 {
		t.Fatalf("expected explicit raw input truncation metadata, got %+v", summary.RawInput)
	}
	if summary.ArtifactHintScan != nil {
		t.Fatalf("expected no artifact hint metadata for a non-artifact fragmented line, got %+v", summary.ArtifactHintScan)
	}
	if len(summary.Artifacts) != 0 {
		t.Fatalf("expected discarded hints after the bounded prefix, got artifacts=%+v", summary.Artifacts)
	}
	if summary.BuildStatusLine != "BUILD SUCCESSFUL in 1s" {
		t.Fatalf("expected reducer to continue after oversized line, got %q", summary.BuildStatusLine)
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
<testsuite name="example.FailingTest" tests="2" skipped="0" failures="1" errors="0">
  <testcase name="passes()" classname="example.FailingTest"></testcase>
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
	startTime := time.Now()
	result := runner.Result{
		ExitCode:  1,
		Duration:  time.Second,
		StartTime: startTime,
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
	if summary.PassedTestCount != 1 || summary.FailedTestCount != 1 {
		t.Fatalf("expected junit counts 1 passed / 1 failed, got %d passed / %d failed", summary.PassedTestCount, summary.FailedTestCount)
	}
	if !contains(summary.ImportantLines, "FailingTest > intentionalFailure(): org.opentest4j.AssertionFailedError: expected: <expected> but was: <hello, build-brief>") {
		t.Fatalf("expected assertion detail in important lines: %v", summary.ImportantLines)
	}
	if !contains(summary.ImportantLines, "at example.FailingTest.intentionalFailure(FailingTest.java:10)") {
		t.Fatalf("expected user stack frame in important lines: %v", summary.ImportantLines)
	}
}

func TestReduceDoesNotReuseStaleJUnitCountsOnEarlyFailure(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}

	report := `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="example.StaleSuite" tests="2" skipped="0" failures="0" errors="0">
  <testcase name="passesOne" classname="example.StaleSuite"></testcase>
  <testcase name="passesTwo" classname="example.StaleSuite"></testcase>
</testsuite>`
	reportPath := filepath.Join(reportDir, "TEST-example.StaleSuite.xml")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatalf("write junit report: %v", err)
	}

	startTime := time.Now()
	oldTime := startTime.Add(-2 * time.Second)
	if err := os.Chtimes(reportPath, oldTime, oldTime); err != nil {
		t.Fatalf("age junit report: %v", err)
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "jvmTest"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:  1,
		Duration:  time.Second,
		StartTime: startTime,
		RawLogPath: writeTestLog(t, []string{
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Task 'jvmTest' not found in root project 'sample'.",
			"BUILD FAILED in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce stale junit fallback failure: %v", err)
	}

	if summary.PassedTestCount != 0 || summary.FailedTestCount != 0 {
		t.Fatalf("expected no stale junit counts, got %d passed / %d failed", summary.PassedTestCount, summary.FailedTestCount)
	}
}

func TestReduceReportsJUnitScanTruncation(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "module", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}

	for i := 0; i < maxJUnitReportFiles+1; i++ {
		report := fmt.Sprintf(`<testsuite><testcase name="test-%03d" classname="ExampleTest"></testcase></testsuite>`, i)
		path := filepath.Join(reportDir, fmt.Sprintf("TEST-%03d.xml", i))
		if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
			t.Fatalf("write junit report %s: %v", path, err)
		}
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		RawLogPath: writeTestLog(t, []string{"BUILD SUCCESSFUL in 1s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce truncated junit scan: %v", err)
	}
	if summary.JUnitScan == nil {
		t.Fatal("expected junit scan metadata")
	}
	if summary.JUnitScan.Discovered != maxJUnitReportFiles+1 || summary.JUnitScan.Parsed != maxJUnitReportFiles {
		t.Fatalf("unexpected junit scan counts: %+v", summary.JUnitScan)
	}
	if summary.JUnitScan.Skipped != 1 || !summary.JUnitScan.Truncated {
		t.Fatalf("expected one skipped report and truncation, got %+v", summary.JUnitScan)
	}
}

func TestReduceReportsMalformedAndUnreadableJUnitReports(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "module", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(reportDir, "TEST-bad.xml"), []byte("<testsuite><broken>"), 0o644); err != nil {
		t.Fatalf("write malformed junit report: %v", err)
	}
	if err := os.Mkdir(filepath.Join(reportDir, "TEST-unreadable.xml"), 0o755); err != nil {
		t.Fatalf("mkdir unreadable junit report: %v", err)
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:  1,
		Duration:  time.Second,
		StartTime: time.Now(),
		RawLogPath: writeTestLog(t, []string{
			"> Task :test FAILED",
			"FAILURE: Build failed with an exception.",
			"Execution failed for task ':test'.",
			"BUILD FAILED in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce malformed/unreadable junit reports: %v", err)
	}
	if summary.JUnitScan == nil {
		t.Fatal("expected junit scan metadata")
	}
	if summary.JUnitScan.Discovered != 2 || summary.JUnitScan.Parsed != 0 || summary.JUnitScan.Skipped != 2 {
		t.Fatalf("unexpected junit scan counts: %+v", summary.JUnitScan)
	}
	if len(summary.JUnitScan.Errors) != 2 {
		t.Fatalf("expected two junit scan errors, got %+v", summary.JUnitScan)
	}
	if len(summary.Warnings) == 0 {
		t.Fatal("expected malformed/unreadable junit warning")
	}
}

func TestReduceReportsAllJUnitScanErrorsAndRelativePaths(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "module", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}
	startTime := time.Now()
	for i := 0; i < maxJUnitScanErrors+2; i++ {
		path := filepath.Join(reportDir, fmt.Sprintf("TEST-bad-%02d.xml", i))
		if err := os.WriteFile(path, []byte("<testsuite><broken>"), 0o644); err != nil {
			t.Fatalf("write malformed junit report: %v", err)
		}
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:  1,
		Duration:  time.Second,
		StartTime: startTime,
		RawLogPath: writeTestLog(t, []string{
			"> Task :test FAILED",
			"FAILURE: Build failed with an exception.",
			"Execution failed for task ':test'.",
			"BUILD FAILED in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce malformed junit reports: %v", err)
	}
	if summary.JUnitScan == nil {
		t.Fatal("expected junit scan metadata")
	}
	if summary.JUnitScan.ErrorCount != maxJUnitScanErrors+2 || len(summary.JUnitScan.Errors) != maxJUnitScanErrors || !summary.JUnitScan.ErrorsTruncated {
		t.Fatalf("expected bounded error details with totals, got %+v", summary.JUnitScan)
	}
	for _, scanError := range summary.JUnitScan.Errors {
		if strings.HasPrefix(scanError, projectDir) {
			t.Fatalf("expected project-relative scan error path, got %q", scanError)
		}
	}
}

func TestReduceKeepsEnrichmentFieldsOmittedWhenUnavailable(t *testing.T) {
	projectDir := t.TempDir()
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   1,
		Duration:   time.Second,
		RawLogPath: writeTestLog(t, []string{"BUILD FAILED in 1s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce compatibility summary: %v", err)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal compatibility summary: %v", err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatalf("decode compatibility summary: %v", err)
	}
	for _, field := range []string{"junit_scan", "artifact_scan"} {
		if _, ok := shape[field]; ok {
			t.Fatalf("expected unavailable enrichment field %q to remain omitted: %s", field, encoded)
		}
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

func TestReduceCapturesKotlinSourceErrorFromConsole(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"--console=plain", ":shared:embedAndSignAppleFrameworkForXcode"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :shared-ui:compileKotlinIosSimulatorArm64",
			"/tmp/project/shared-ui/src/commonMain/kotlin/example/SharedNetworkIcon.kt:98:17: error: 'when' expression must be exhaustive. Add the 'Inverted' branch or an 'else' branch.",
			"> Task :shared-ui:compileKotlinIosSimulatorArm64 FAILED",
			"error: Compilation finished with errors",
			"FAILURE: Build failed with an exception.",
			"* What went wrong:",
			"Execution failed for task ':shared-ui:compileKotlinIosSimulatorArm64'.",
			"> Compilation finished with errors",
			"BUILD FAILED in 2s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce kotlin source error log: %v", err)
	}

	expected := "/tmp/project/shared-ui/src/commonMain/kotlin/example/SharedNetworkIcon.kt:98:17: error: 'when' expression must be exhaustive. Add the 'Inverted' branch or an 'else' branch."
	if !contains(summary.ImportantLines, expected) {
		t.Fatalf("expected kotlin source error line in important lines: %v", summary.ImportantLines)
	}
}

func TestReducePreservesInformationalReportOutput(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "-p", "smoke/projects/jvm-junit", ":tasks", "--all"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :tasks",
			"",
			"------------------------------------------------------------",
			"Tasks runnable from root project 'jvm-junit-smoke'",
			"------------------------------------------------------------",
			"",
			"Build tasks",
			"-----------",
			"assemble - Assembles the outputs of this project.",
			"BUILD SUCCESSFUL in 1s",
			"1 actionable task: 1 executed",
			"Configuration cache entry stored.",
			"Consider enabling configuration cache to speed up this build: https://docs.gradle.org/9.5.1/userguide/configuration_cache_enabling.html",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce informational report: %v", err)
	}

	for _, expected := range []string{
		"> Task :tasks",
		"Tasks runnable from root project 'jvm-junit-smoke'",
		"Build tasks",
		"assemble - Assembles the outputs of this project.",
	} {
		if !contains(summary.ReportLines, expected) {
			t.Fatalf("expected report line %q in %v", expected, summary.ReportLines)
		}
	}
	for _, unexpected := range []string{
		"BUILD SUCCESSFUL in 1s",
		"1 actionable task: 1 executed",
		"Configuration cache entry stored.",
		"Consider enabling configuration cache to speed up this build: https://docs.gradle.org/9.5.1/userguide/configuration_cache_enabling.html",
	} {
		if contains(summary.ReportLines, unexpected) {
			t.Fatalf("did not expect report line %q in %v", unexpected, summary.ReportLines)
		}
	}
}

func TestReduceHighlightsGeneratedOutputLocations(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "captureComposePreviews"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			"AgentPreview snapshots written to: /tmp/project/build/agentPreviewSnapshots",
			"AgentPreview report written to: /tmp/project/build/agentPreviewReports/capture-report.json",
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce generated output locations: %v", err)
	}

	for _, expected := range []string{
		"AgentPreview snapshots written to: /tmp/project/build/agentPreviewSnapshots",
		"AgentPreview report written to: /tmp/project/build/agentPreviewReports/capture-report.json",
	} {
		if !contains(summary.ImportantLines, expected) {
			t.Fatalf("expected generated output highlight %q in %v", expected, summary.ImportantLines)
		}
	}
}

func TestReduceRetainsArtifactTruncationWhenWarningsAreSaturated(t *testing.T) {
	projectDir := t.TempDir()
	startTime := time.Now()
	for i := 0; i < 21; i++ {
		writeGeneratedFile(t, filepath.Join(projectDir, "app", "build", "libs", fmt.Sprintf("artifact-%03d.jar", i)), "jar")
	}
	lines := make([]string, 0, 10)
	for i := 0; i < maxWarnings; i++ {
		lines = append(lines, fmt.Sprintf("warning: existing warning %02d", i))
	}
	lines = append(lines, "BUILD SUCCESSFUL in 1s")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		StartTime:  startTime,
		RawLogPath: writeTestLog(t, lines),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce saturated-warning build: %v", err)
	}
	if summary.WarningCount != maxWarnings || len(summary.Warnings) != maxWarnings {
		t.Fatalf("expected warning cap with total count, got count=%d warnings=%v", summary.WarningCount, summary.Warnings)
	}
	if summary.ArtifactScan == nil || !summary.ArtifactScan.Truncated {
		t.Fatalf("expected artifact truncation metadata despite warning cap, got %+v", summary.ArtifactScan)
	}
}

func TestReduceDoesNotDoubleCountEvictedStandardRootHint(t *testing.T) {
	projectDir := t.TempDir()
	snapshot := artifacts.Capture(projectDir)
	startTime := time.Now()
	for i := 0; i < 21; i++ {
		writeGeneratedFile(t, filepath.Join(projectDir, "app", "build", "libs", fmt.Sprintf("artifact-%03d.jar", i)), "jar")
	}

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         time.Second,
		StartTime:        startTime,
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"Generated output: ./app/build/libs/artifact-020.jar",
			"BUILD SUCCESSFUL in 1s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce repeated standard-root hint: %v", err)
	}
	if summary.ArtifactScan == nil || summary.ArtifactScan.Discovered != 21 || summary.ArtifactScan.Reported != 20 || summary.ArtifactScan.Skipped != 1 {
		t.Fatalf("expected 21 unique artifacts despite repeated standard-root hint, got %+v", summary.ArtifactScan)
	}
}

func TestReduceReportsBoundedArtifactHintRetention(t *testing.T) {
	projectDir := t.TempDir()
	const hintCount = 10_000
	lines := make([]string, 0, hintCount+1)
	for i := 0; i < hintCount; i++ {
		lines = append(lines, fmt.Sprintf("Generated output: ./custom-output/artifact-%06d.jar", i))
	}
	lines = append(lines, "BUILD SUCCESSFUL in 1s")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		StartTime:  time.Now(),
		RawLogPath: writeTestLog(t, lines),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce many artifact hints: %v", err)
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal hint metadata: %v", err)
	}
	var shape struct {
		ArtifactHintScan *struct {
			Observed      int  `json:"observed"`
			Retained      int  `json:"retained"`
			Omitted       int  `json:"omitted"`
			RetainedBytes int  `json:"retained_bytes"`
			Truncated     bool `json:"truncated"`
		} `json:"artifact_hint_scan"`
	}
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatalf("decode hint metadata: %v", err)
	}
	if shape.ArtifactHintScan == nil {
		t.Fatalf("expected bounded artifact hint metadata, got %s", encoded)
	}
	if shape.ArtifactHintScan.Observed != hintCount || shape.ArtifactHintScan.Retained == 0 || shape.ArtifactHintScan.Omitted != hintCount-shape.ArtifactHintScan.Retained || !shape.ArtifactHintScan.Truncated {
		t.Fatalf("unexpected artifact hint completeness metadata: %+v", shape.ArtifactHintScan)
	}
	if shape.ArtifactHintScan.Retained > 64 || shape.ArtifactHintScan.RetainedBytes > 64*1024 {
		t.Fatalf("artifact hint retention exceeded bounds: %+v", shape.ArtifactHintScan)
	}
}

func TestReduceSurfacesQuotedArtifactsAndKeepsHintMetadataTruthful(t *testing.T) {
	projectDir := t.TempDir()
	startTime := time.Now()
	firstPath := filepath.Join(projectDir, "custom output", "first.jar")
	secondPath := filepath.Join(projectDir, "custom output", "second.apk")
	writeGeneratedFile(t, firstPath, "jar")
	writeGeneratedFile(t, secondPath, "apk")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		StartTime:  startTime,
		RawLogPath: writeTestLog(t, []string{`Artifacts: './custom output/first.jar ./custom output/second.apk'`, "BUILD SUCCESSFUL in 1s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce quoted artifacts: %v", err)
	}
	if summary.ArtifactHintScan == nil || summary.ArtifactHintScan.Observed != 2 || summary.ArtifactHintScan.Retained != 2 || summary.ArtifactHintScan.Omitted != 0 || summary.ArtifactHintScan.Truncated {
		t.Fatalf("expected truthful quoted hint metadata, got %+v", summary.ArtifactHintScan)
	}
	if !containsArtifact(summary.Artifacts, "JAR", "custom output/first.jar") || !containsArtifact(summary.Artifacts, "APK", "custom output/second.apk") {
		t.Fatalf("expected both quoted artifacts, got %+v", summary.Artifacts)
	}
}

func TestReduceBoundsOneLineArtifactHintExtractionAndKeepsPriority(t *testing.T) {
	projectDir := t.TempDir()
	apkPath := filepath.Join(projectDir, "custom-output", "late-release.apk")
	writeGeneratedFile(t, apkPath, "apk")

	const hintCount = 10_000
	var line strings.Builder
	line.Grow(hintCount * 40)
	for i := 0; i < hintCount; i++ {
		if i > 0 {
			line.WriteByte(' ')
		}
		fmt.Fprintf(&line, "./custom-output/artifact-%06d.jar", i)
	}
	line.WriteString(" ./custom-output/late-release.apk")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		StartTime:  time.Now(),
		RawLogPath: writeTestLog(t, []string{line.String(), "BUILD SUCCESSFUL in 1s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce one-line many artifact hints: %v", err)
	}
	if summary.ArtifactHintScan == nil {
		t.Fatal("expected one-line hint metadata")
	}
	if summary.ArtifactHintScan.Observed != hintCount+1 {
		t.Fatalf("expected exact streamed hint count, got %+v", summary.ArtifactHintScan)
	}
	if summary.ArtifactHintScan.Retained == 0 || summary.ArtifactHintScan.Retained > maxArtifactHints || summary.ArtifactHintScan.RetainedBytes > maxArtifactHintBytes {
		t.Fatalf("hint retention exceeded bounds: %+v", summary.ArtifactHintScan)
	}
	if summary.ArtifactHintScan.Omitted != summary.ArtifactHintScan.Observed-summary.ArtifactHintScan.Retained || !summary.ArtifactHintScan.Truncated {
		t.Fatalf("expected truthful truncation metadata, got %+v", summary.ArtifactHintScan)
	}
	if !containsArtifact(summary.Artifacts, "APK", "custom-output/late-release.apk") {
		t.Fatalf("expected late high-priority apk to survive hint retention, got %+v", summary.Artifacts)
	}
}

func TestReduceFindsGeneratedArtifactsAndOmittedCompilationOutputs(t *testing.T) {
	projectDir := t.TempDir()
	staleArtifact := filepath.Join(projectDir, "legacy", "build", "libs", "legacy.jar")
	writeGeneratedFile(t, staleArtifact, "stale")
	snapshot := artifacts.Capture(projectDir)
	startTime := time.Now()

	writeGeneratedFile(t, filepath.Join(projectDir, "androidApp", "build", "outputs", "apk", "debug", "androidApp-debug.apk"), "apk")
	writeGeneratedFile(t, filepath.Join(projectDir, "shared", "build", "outputs", "aar", "shared-release.aar"), "aar")
	writeGeneratedFile(t, filepath.Join(projectDir, "server", "build", "libs", "server.jar"), "jar")
	writeGeneratedFile(t, filepath.Join(projectDir, "cli", "build", "distributions", "cli.zip"), "zip")
	writeGeneratedFile(t, filepath.Join(projectDir, "core", "build", "classes", "kotlin", "main", "Example.class"), "class")
	writeGeneratedFile(t, filepath.Join(projectDir, "core", "build", "generated", "ksp", "main", "kotlin", "Generated.kt"), "generated")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         5 * time.Second,
		StartTime:        startTime,
		ArtifactSnapshot: snapshot,
		RawLogPath:       writeTestLog(t, []string{"BUILD SUCCESSFUL in 5s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce success with artifacts: %v", err)
	}

	if !containsArtifact(summary.Artifacts, "APK", "androidApp/build/outputs/apk/debug/androidApp-debug.apk") {
		t.Fatalf("expected apk artifact in summary: %+v", summary.Artifacts)
	}
	if !containsArtifact(summary.Artifacts, "AAR", "shared/build/outputs/aar/shared-release.aar") {
		t.Fatalf("expected aar artifact in summary: %+v", summary.Artifacts)
	}
	if !containsArtifact(summary.Artifacts, "JAR", "server/build/libs/server.jar") {
		t.Fatalf("expected jar artifact in summary: %+v", summary.Artifacts)
	}
	if !containsArtifact(summary.Artifacts, "ZIP", "cli/build/distributions/cli.zip") {
		t.Fatalf("expected zip artifact in summary: %+v", summary.Artifacts)
	}
	if containsArtifact(summary.Artifacts, "JAR", "legacy/build/libs/legacy.jar") {
		t.Fatalf("did not expect stale artifact in summary: %+v", summary.Artifacts)
	}
	if summary.GeneratedClassFileCount != 1 {
		t.Fatalf("expected 1 generated class file, got %d", summary.GeneratedClassFileCount)
	}
	if summary.GeneratedCodegenFileCount != 1 {
		t.Fatalf("expected 1 generated codegen file, got %d", summary.GeneratedCodegenFileCount)
	}
	if summary.ArtifactScan == nil {
		t.Fatal("expected artifact scan metadata")
	}
	if summary.ArtifactScan.Discovered != 4 || summary.ArtifactScan.Reported != 4 || summary.ArtifactScan.Skipped != 0 {
		t.Fatalf("unexpected artifact scan metadata: %+v", summary.ArtifactScan)
	}
}

func TestReduceFindsKMPArtifactsAndVerifiedLogHints(t *testing.T) {
	projectDir := t.TempDir()
	snapshot := artifacts.Capture(projectDir)
	startTime := time.Now()

	writeGeneratedFile(t, filepath.Join(projectDir, "shared", "build", "bin", "iosSimulatorArm64", "releaseFramework", "Shared.framework", "Shared"), "framework-binary")
	writeGeneratedFile(t, filepath.Join(projectDir, "shared", "build", "XCFrameworks", "release", "Shared.xcframework", "ios-arm64", "Shared.framework", "Shared"), "xcframework-binary")
	writeGeneratedFile(t, filepath.Join(projectDir, "shared", "build", "bin", "linuxX64", "releaseExecutable", "app.kexe"), "kexe")
	writeGeneratedFile(t, filepath.Join(projectDir, "shared", "build", "bin", "iosArm64", "releaseLibrary", "shared.klib"), "klib")
	customFramework := filepath.Join(projectDir, "custom-output", "Fancy.xcframework", "ios-arm64", "Fancy.framework", "Fancy")
	writeGeneratedFile(t, customFramework, "custom-xcframework")

	command := gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "assemble"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}
	result := runner.Result{
		ExitCode:         0,
		Duration:         7 * time.Second,
		StartTime:        startTime,
		ArtifactSnapshot: snapshot,
		RawLogPath: writeTestLog(t, []string{
			"Shared framework generated successfully at " + filepath.Join(projectDir, "custom-output", "Fancy.xcframework"),
			"BUILD SUCCESSFUL in 7s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce kmp artifacts: %v", err)
	}

	for _, expected := range []struct {
		kind string
		path string
	}{
		{kind: "FRAMEWORK", path: "shared/build/bin/iosSimulatorArm64/releaseFramework/Shared.framework"},
		{kind: "XCFRAMEWORK", path: "shared/build/XCFrameworks/release/Shared.xcframework"},
		{kind: "KEXE", path: "shared/build/bin/linuxX64/releaseExecutable/app.kexe"},
		{kind: "KLIB", path: "shared/build/bin/iosArm64/releaseLibrary/shared.klib"},
		{kind: "XCFRAMEWORK", path: "custom-output/Fancy.xcframework"},
	} {
		if !containsArtifact(summary.Artifacts, expected.kind, expected.path) {
			t.Fatalf("expected %s artifact %q in %+v", expected.kind, expected.path, summary.Artifacts)
		}
	}
}

func TestReduceSanitizesSensitiveCommandArguments(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args: []string{
			"--console=plain",
			"test",
			"-P", "split.project=split-project-secret",
			"-D", "split.system=split-system-secret",
			"-Pjoined.project=joined-project-secret",
			"-Djoined.system=joined-system-secret",
			"--project-prop", "long.project=long-project-secret",
			"--system-prop", "long.system=long-system-secret",
			"--project-prop=equals.project=equals-project-secret",
			"--system-prop=equals.system=equals-system-secret",
			"--tests", "com.example.SafeTest",
		},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		RawLogPath: writeTestLog(t, []string{"BUILD SUCCESSFUL in 1s"}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce command summary: %v", err)
	}

	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{
		"split-project-secret",
		"split-system-secret",
		"joined-project-secret",
		"joined-system-secret",
		"long-project-secret",
		"long-system-secret",
		"equals-project-secret",
		"equals-system-secret",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("structured summary leaked %q: %s", secret, text)
		}
	}
	for _, safe := range []string{"test", "--tests", "com.example.SafeTest"} {
		if !strings.Contains(text, safe) {
			t.Fatalf("structured summary lost safe argument %q: %s", safe, text)
		}
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

func writeGeneratedFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir generated file dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}
}

func containsArtifact(artifacts []Artifact, kind, path string) bool {
	for _, artifact := range artifacts {
		if artifact.Kind == kind && artifact.Path == path {
			return true
		}
	}
	return false
}

func TestReduceConfigCacheStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		line   string
		status string
	}{
		// Status lines mirror Gradle's ConfigurationCacheProblems.kt beforeComplete handler.
		{"reused", "Configuration cache entry reused.", "reused"},
		{"reused-with-problems", "Configuration cache entry reused with 3 problems.", "reused"},
		{"stored", "Configuration cache entry stored.", "stored"},
		{"stored-with-problems", "Configuration cache entry stored with 2 problems.", "stored"},
		{"updated", "Configuration cache entry updated for 1 project, 2 up-to-date.", "updated"},
		{"updated-with-problems", "Configuration cache entry updated for 1 project with 2 problems, 3 up-to-date.", "updated"},
		{"discarded-with-problem", "Configuration cache entry discarded with 1 problem.", "discarded"},
		{"discarded-too-many", "Configuration cache entry discarded with too many problems (512).", "discarded"},
		{"discarded-serialization", "Configuration cache entry discarded due to serialization error.", "discarded"},
		{"discarded", "Configuration cache entry discarded.", "discarded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := gradle.Command{
				Executable: "/tmp/gradlew",
				Args:       []string{"assemble"},
				ProjectDir: "/tmp/project",
				Source:     gradle.SourceWrapper,
			}
			result := runner.Result{
				ExitCode: 0,
				Duration: 2 * time.Second,
				RawLogPath: writeTestLog(t, []string{
					tc.line,
					"BUILD SUCCESSFUL in 2s",
				}),
			}
			summary, err := Reduce(command, result)
			if err != nil {
				t.Fatalf("reduce: %v", err)
			}
			if summary.ConfigCacheStatus != tc.status {
				t.Fatalf("expected status %q, got %q", tc.status, summary.ConfigCacheStatus)
			}
		})
	}
}

func TestReduceConfigCacheProblems(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"assemble"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	// Real Gradle 9.x configuration-cache output: a summary line, problem entries with
	// varied location prefixes (Build file / Settings file / Script), indented per-problem
	// doc-hint lines that must NOT be captured as problems, and the report URL.
	result := runner.Result{
		ExitCode: 0,
		Duration: 4 * time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :app:compileDebugKotlin",
			"3 problems were found storing the configuration cache.",
			"- Build file 'build.gradle': line 3: external process started 'git --version'",
			"  See https://docs.gradle.org/9.5.1/userguide/configuration_cache.html#config_cache:requirements:external_processes",
			"- Settings file 'settings.gradle': line 5: external process started 'uname -a'",
			"  See https://docs.gradle.org/9.5.1/userguide/configuration_cache.html#config_cache:requirements:external_processes",
			"- Script 'gradle/scripts/build-logic.gradle': line 8: external process started 'sw_vers'",
			"See the complete report at file:///tmp/project/build/reports/configuration-cache/abc123/configuration-cache-report.html",
			"BUILD SUCCESSFUL in 4s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce config cache log: %v", err)
	}

	expected := []string{
		"3 problems were found storing the configuration cache.",
		"Build file 'build.gradle': line 3: external process started 'git --version'",
		"Settings file 'settings.gradle': line 5: external process started 'uname -a'",
		"Script 'gradle/scripts/build-logic.gradle': line 8: external process started 'sw_vers'",
	}
	if len(summary.ConfigCacheProblems) != len(expected) {
		t.Fatalf("expected %d config cache problems, got %d: %v", len(expected), len(summary.ConfigCacheProblems), summary.ConfigCacheProblems)
	}
	for i, want := range expected {
		if summary.ConfigCacheProblems[i] != want {
			t.Fatalf("unexpected config cache problem %d: got %q, want %q", i, summary.ConfigCacheProblems[i], want)
		}
	}
	for _, problem := range summary.ConfigCacheProblems {
		if strings.HasPrefix(problem, "See https://docs.gradle.org") {
			t.Fatalf("doc-hint line should not be captured as a problem: %q", problem)
		}
	}
	if summary.ConfigCacheReportURL != "file:///tmp/project/build/reports/configuration-cache/abc123/configuration-cache-report.html" {
		t.Fatalf("unexpected report URL: %q", summary.ConfigCacheReportURL)
	}
}

func TestReduceConfigCacheUpdatingSummaryAndTaskProblem(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"someTask"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	// Partial/incremental reuse: Gradle emits an "updating" summary header and a Kotlin-style
	// "- Task `:x` of type `Y`:" problem line (backticks, not quotes). Gradle identifies problem
	// lines with "- (.*)", which our "- " prefix check mirrors.
	result := runner.Result{
		ExitCode: 0,
		Duration: 3 * time.Second,
		RawLogPath: writeTestLog(t, []string{
			"> Task :someTask",
			"1 problem was found updating the configuration cache.",
			"- Task `:someTask` of type `org.gradle.api.DefaultTask`: invocation of 'Task.project' at execution time is unsupported with the configuration cache.",
			"  See https://docs.gradle.org/9.5.1/userguide/configuration_cache_requirements.html#config_cache:requirements:use_project_during_execution",
			"See the complete report at file:///tmp/project/build/reports/configuration-cache/ghi789/configuration-cache-report.html",
			"Configuration cache entry updated for 1 project, 2 up-to-date.",
			"BUILD SUCCESSFUL in 3s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce updating config cache log: %v", err)
	}

	if summary.ConfigCacheStatus != "updated" {
		t.Fatalf("expected updated status, got %q", summary.ConfigCacheStatus)
	}
	expected := []string{
		"1 problem was found updating the configuration cache.",
		"Task `:someTask` of type `org.gradle.api.DefaultTask`: invocation of 'Task.project' at execution time is unsupported with the configuration cache.",
	}
	if len(summary.ConfigCacheProblems) != len(expected) {
		t.Fatalf("expected %d config cache problems, got %d: %v", len(expected), len(summary.ConfigCacheProblems), summary.ConfigCacheProblems)
	}
	for i, want := range expected {
		if summary.ConfigCacheProblems[i] != want {
			t.Fatalf("unexpected config cache problem %d: got %q, want %q", i, summary.ConfigCacheProblems[i], want)
		}
	}
}

func TestReduceConfigCacheSingularProblemAndDiscardedStatus(t *testing.T) {
	command := gradle.Command{
		Executable: "/tmp/gradlew",
		Args:       []string{"assemble"},
		ProjectDir: "/tmp/project",
		Source:     gradle.SourceWrapper,
	}
	result := runner.Result{
		ExitCode: 1,
		Duration: 2 * time.Second,
		RawLogPath: writeTestLog(t, []string{
			"1 problem was found storing the configuration cache.",
			"- Build file 'build.gradle': line 3: external process started 'git --version'",
			"  See https://docs.gradle.org/9.5.1/userguide/configuration_cache.html#config_cache:requirements:external_processes",
			"See the complete report at file:///tmp/project/build/reports/configuration-cache/def456/configuration-cache-report.html",
			"Configuration cache entry discarded with 1 problem.",
			"BUILD FAILED in 2s",
		}),
	}

	summary, err := Reduce(command, result)
	if err != nil {
		t.Fatalf("reduce singular config cache log: %v", err)
	}

	if summary.ConfigCacheStatus != "discarded" {
		t.Fatalf("expected discarded status, got %q", summary.ConfigCacheStatus)
	}
	expected := []string{
		"1 problem was found storing the configuration cache.",
		"Build file 'build.gradle': line 3: external process started 'git --version'",
	}
	if len(summary.ConfigCacheProblems) != len(expected) {
		t.Fatalf("expected %d config cache problems, got %d: %v", len(expected), len(summary.ConfigCacheProblems), summary.ConfigCacheProblems)
	}
	for i, want := range expected {
		if summary.ConfigCacheProblems[i] != want {
			t.Fatalf("unexpected config cache problem %d: got %q, want %q", i, summary.ConfigCacheProblems[i], want)
		}
	}
	if summary.ConfigCacheReportURL != "file:///tmp/project/build/reports/configuration-cache/def456/configuration-cache-report.html" {
		t.Fatalf("unexpected report URL: %q", summary.ConfigCacheReportURL)
	}
}

func TestReduceBoundsSummaryCollectionsByCountAndBytes(t *testing.T) {
	projectDir := t.TempDir()
	lines := make([]string, 0, maxReportLines+maxFailedTasks+maxFailedTests+maxBuildScanURLs+8)
	for i := 0; i < maxReportLines+10; i++ {
		lines = append(lines, fmt.Sprintf("report line %03d", i))
	}
	for i := 0; i < maxFailedTasks+10; i++ {
		lines = append(lines, fmt.Sprintf("> Task :task-%03d FAILED", i))
	}
	for i := 0; i < maxFailedTests+10; i++ {
		lines = append(lines, fmt.Sprintf("ExampleTest%03d > test FAILED", i))
	}
	for i := 0; i < maxBuildScanURLs+10; i++ {
		lines = append(lines, fmt.Sprintf("Build scan: https://develocity.example/s/%03d", i))
	}
	lines = append(lines, "BUILD SUCCESSFUL in 1s")

	summary, err := Reduce(gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "-p", "smoke/projects/jvm-junit", ":tasks", "--all"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}, runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		RawLogPath: writeTestLog(t, lines),
	})
	if err != nil {
		t.Fatalf("reduce bounded collections: %v", err)
	}

	if len(summary.ReportLines) > maxReportLines || len(summary.FailedTasks) > maxFailedTasks || len(summary.FailedTests) > maxFailedTests || len(summary.BuildScanURLs) > maxBuildScanURLs {
		t.Fatalf("summary collection count exceeded bounds: reports=%d tasks=%d tests=%d scans=%d", len(summary.ReportLines), len(summary.FailedTasks), len(summary.FailedTests), len(summary.BuildScanURLs))
	}
	if summary.Reducer == nil || len(summary.Reducer.Collections) < 4 {
		t.Fatalf("expected reducer collection completeness metadata, got %+v", summary.Reducer)
	}
	for name, collection := range summary.Reducer.Collections {
		if !collection.Truncated {
			t.Fatalf("expected %s collection to be marked truncated: %+v", name, collection)
		}
		if collection.RetainedBytes > int64(maxSummaryCollectionBytes) {
			t.Fatalf("%s retained bytes exceeded bound: %+v", name, collection)
		}
	}
}

func TestReduceBoundsSummaryCollectionBytes(t *testing.T) {
	long := strings.Repeat("x", maxSummaryCollectionBytes+1)
	summary, err := Reduce(gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "-p", "smoke/projects/jvm-junit", ":tasks", "--all"},
		ProjectDir: t.TempDir(),
		Source:     gradle.SourceSystem,
	}, runner.Result{
		ExitCode: 0,
		Duration: time.Second,
		RawLogPath: writeTestLog(t, []string{
			long,
			"> Task :" + long + " FAILED",
			"ExampleTest > " + long + " FAILED",
			"Build scan: https://develocity.example/s/" + long,
			"BUILD SUCCESSFUL in 1s",
		}),
	})
	if err != nil {
		t.Fatalf("reduce byte-bounded collections: %v", err)
	}
	if summary.Reducer == nil {
		t.Fatal("expected reducer completeness metadata")
	}
	for _, name := range []string{"report_lines", "failed_tasks", "failed_tests", "build_scan_urls"} {
		collection, ok := summary.Reducer.Collections[name]
		if !ok || !collection.Truncated {
			t.Fatalf("expected byte truncation metadata for %s, got %+v", name, summary.Reducer.Collections)
		}
		if collection.RetainedBytes > int64(maxSummaryCollectionBytes) {
			t.Fatalf("%s retained bytes exceeded bound: %+v", name, collection)
		}
	}
}

func TestReduceLongFragmentedLineUsesRawInputCompleteness(t *testing.T) {
	line := strings.Repeat("x", maxReducerLineBytes+1) + " > Task :trailing FAILED"
	summary, err := Reduce(gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "build"},
		ProjectDir: t.TempDir(),
		Source:     gradle.SourceSystem,
	}, runner.Result{
		ExitCode:   0,
		Duration:   time.Second,
		RawLogPath: writeTestLog(t, []string{line, "BUILD SUCCESSFUL in 1s"}),
	})
	if err != nil {
		t.Fatalf("reduce fragmented line: %v", err)
	}
	if summary.RawInput == nil || summary.RawInput.TruncatedLines != 1 || summary.RawInput.TruncatedBytes == 0 {
		t.Fatalf("expected raw input truncation metadata, got %+v", summary.RawInput)
	}
	if summary.ArtifactHintScan != nil {
		t.Fatalf("long non-artifact line must not report artifact hint truncation: %+v", summary.ArtifactHintScan)
	}
	if summary.Reducer == nil || !summary.Reducer.Partial || !contains(summary.Reducer.PartialFields, "failed_tasks") {
		t.Fatalf("expected failed task field to be partial, got %+v", summary.Reducer)
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal fragmented summary: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"raw_input"`) || !strings.Contains(text, `"reducer"`) || !strings.Contains(text, `"partial":true`) {
		t.Fatalf("expected additive completeness metadata in JSON: %s", text)
	}
}

func TestReduceSkipsOversizedJUnitBeforeParsing(t *testing.T) {
	projectDir := t.TempDir()
	reportDir := filepath.Join(projectDir, "module", "build", "test-results", "test")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatalf("mkdir report dir: %v", err)
	}
	startTime := time.Now()
	content := []byte(`<testsuite><testcase name="before" classname="ExampleTest"></testcase>` + strings.Repeat("x", maxJUnitFileBytes) + `</testsuite>`)
	if err := os.WriteFile(filepath.Join(reportDir, "TEST-oversized.xml"), content, 0o644); err != nil {
		t.Fatalf("write oversized junit report: %v", err)
	}

	summary, err := Reduce(gradle.Command{
		Executable: "/tmp/gradle",
		Args:       []string{"--console=plain", "test"},
		ProjectDir: projectDir,
		Source:     gradle.SourceSystem,
	}, runner.Result{
		ExitCode:  1,
		Duration:  time.Second,
		StartTime: startTime,
		RawLogPath: writeTestLog(t, []string{
			"> Task :test FAILED",
			"Execution failed for task ':test'.",
			"BUILD FAILED in 1s",
		}),
	})
	if err != nil {
		t.Fatalf("reduce oversized junit report: %v", err)
	}
	if summary.JUnitScan == nil || !summary.JUnitScan.FileBytesTruncated || summary.JUnitScan.Parsed != 0 {
		t.Fatalf("expected oversized junit metadata without parsing, got %+v", summary.JUnitScan)
	}
	if len(summary.JUnitScan.Errors) != 0 {
		t.Fatalf("oversized junit should not be parsed as malformed XML: %+v", summary.JUnitScan)
	}
}
