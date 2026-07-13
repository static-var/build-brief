package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"build-brief/internal/gradle"
	"build-brief/internal/reducer"
	"build-brief/internal/runner"
	"build-brief/internal/tracking"
)

func TestParseArgsStopsAtGradleArgs(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--mode", "raw", "test", "--stacktrace"})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}

	if opts.Mode != "raw" {
		t.Fatalf("expected raw mode, got %s", opts.Mode)
	}

	if len(gradleArgs) != 2 || gradleArgs[0] != "test" || gradleArgs[1] != "--stacktrace" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsTreatsJSONModeAsHuman(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--mode", "json", "test"})
	if err != nil {
		t.Fatalf("parse json compatibility mode: %v", err)
	}

	if opts.Mode != "human" {
		t.Fatalf("expected json mode to normalize to human, got %s", opts.Mode)
	}

	if len(gradleArgs) != 1 || gradleArgs[0] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsReadsGradleUserHome(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--gradle-user-home", "/tmp/shared-home", "test"})
	if err != nil {
		t.Fatalf("parse gradle user home args: %v", err)
	}

	if opts.GradleUserHome != "/tmp/shared-home" {
		t.Fatalf("expected gradle user home to be parsed, got %s", opts.GradleUserHome)
	}

	if len(gradleArgs) != 1 || gradleArgs[0] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsReadsConfigPath(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--config", "/tmp/build-brief.json", "test"})
	if err != nil {
		t.Fatalf("parse config path args: %v", err)
	}

	if opts.ConfigPath != "/tmp/build-brief.json" {
		t.Fatalf("expected config path to be parsed, got %s", opts.ConfigPath)
	}

	if len(gradleArgs) != 1 || gradleArgs[0] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsReadsConfigPathFromEnv(t *testing.T) {
	t.Setenv("BUILD_BRIEF_CONFIG", "/tmp/env-build-brief.json")

	opts, gradleArgs, err := parseArgs([]string{"test"})
	if err != nil {
		t.Fatalf("parse config env args: %v", err)
	}

	if opts.ConfigPath != "/tmp/env-build-brief.json" {
		t.Fatalf("expected env config path to be parsed, got %s", opts.ConfigPath)
	}

	if len(gradleArgs) != 1 || gradleArgs[0] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsRejectsUnknownBuildBriefFlag(t *testing.T) {
	_, _, err := parseArgs([]string{"--daemon-mode", "on", "test"})
	if err == nil {
		t.Fatal("expected unknown build-brief flag error")
	}

	if !strings.Contains(err.Error(), "unknown build-brief flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsHonorsDelimiter(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--project-dir", "/tmp/project", "--", "--stacktrace", "test"})
	if err != nil {
		t.Fatalf("parse args with delimiter: %v", err)
	}

	if opts.ProjectDir != "/tmp/project" {
		t.Fatalf("unexpected project dir: %s", opts.ProjectDir)
	}

	if len(gradleArgs) != 2 || gradleArgs[0] != "--stacktrace" || gradleArgs[1] != "test" {
		t.Fatalf("unexpected gradle args: %v", gradleArgs)
	}
}

func TestParseArgsRejectsUnknownBuildBriefGradleLookingFlag(t *testing.T) {
	_, _, err := parseArgs([]string{"--stacktrace", "test"})
	if err == nil {
		t.Fatal("expected unknown build-brief flag error")
	}
}

func TestParseArgsReadsVersionFlag(t *testing.T) {
	opts, gradleArgs, err := parseArgs([]string{"--version"})
	if err != nil {
		t.Fatalf("parse version flag: %v", err)
	}

	if !opts.Version {
		t.Fatal("expected version flag to be set")
	}

	if len(gradleArgs) != 0 {
		t.Fatalf("expected no gradle args, got %v", gradleArgs)
	}
}

func TestParseArgsRejectsGlobalInstallForce(t *testing.T) {
	_, _, err := parseArgs([]string{"--global", "--install-force"})
	if err == nil {
		t.Fatal("expected --global --install-force to be rejected")
	}

	if !strings.Contains(err.Error(), "--install-force cannot be combined with --global") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseArgsRejectsGlobalInstall(t *testing.T) {
	_, _, err := parseArgs([]string{"--global", "--install"})
	if err == nil {
		t.Fatal("expected --global --install to be rejected")
	}

	if !strings.Contains(err.Error(), "--install cannot be combined with --global") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunPrintsVersion(t *testing.T) {
	originalVersion := Version
	Version = "test-version"
	t.Cleanup(func() {
		Version = originalVersion
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	if strings.TrimSpace(stdout.String()) != "build-brief test-version" {
		t.Fatalf("unexpected version output %q", stdout.String())
	}
}

func TestRunPrintsHelp(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}

	help := stdout.String()
	for _, expected := range []string{
		"Common commands:",
		"build-brief --global",
		"build-brief gains --history",
		"build-brief gains --reset",
		"build-brief rewrite 'gradle test'",
		"build-brief rewrite 'gradle test && gradle check'",
		"build-brief gradle test",
		"build-brief ./gradlew test",
		"[gradle|./gradlew|PATH-TO-GRADLE]",
		"In interactive terminals, use Up/Down to move, Space to toggle, and Enter to install.",
		"Non-interactive stdin falls back to comma-separated numbers, '*' or 'all', or blank to cancel.",
		"Only existing global instruction files are updated; supported tools may also install managed plugin/extension files.",
		"Must be used by itself; do not combine it with --install or --install-force.",
		"including chained `&&`, `||`, and `;` segments.",
		"Intended for hooks/plugins such as the OpenCode tool.execute.before hook.",
	} {
		if !strings.Contains(help, expected) {
			t.Fatalf("expected help to contain %q, got %q", expected, help)
		}
	}

	for _, unexpected := range []string{
		"--mode [human|json|raw]",
		"build-brief --mode json build",
		"--daemon-mode MODE",
		"BUILD_BRIEF_DAEMON_MODE",
	} {
		if strings.Contains(help, unexpected) {
			t.Fatalf("expected help not to contain %q, got %q", unexpected, help)
		}
	}
}

func TestRunRewrite(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"rewrite", "gradle clean"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "build-brief gradle clean" {
		t.Fatalf("unexpected rewrite output %q", stdout.String())
	}
}

func TestRunRewriteCommandChain(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"rewrite", "gradle test && gradle check"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "build-brief gradle test && build-brief gradle check" {
		t.Fatalf("unexpected rewrite output %q", stdout.String())
	}
}

func TestParseGainsArgs(t *testing.T) {
	opts, err := parseGainsArgs([]string{"--project", "--history", "--format", "json"})
	if err != nil {
		t.Fatalf("parse gains args: %v", err)
	}

	if !opts.Project || !opts.History || opts.Format != "json" {
		t.Fatalf("unexpected gains options: %+v", opts)
	}
}

func TestParseGainsArgsReset(t *testing.T) {
	opts, err := parseGainsArgs([]string{"--reset"})
	if err != nil {
		t.Fatalf("parse gains reset args: %v", err)
	}

	if !opts.Reset || opts.Project || opts.History || opts.Format != "text" {
		t.Fatalf("unexpected reset gains options: %+v", opts)
	}
}

func TestRunInstallsLocalAgentsFile(t *testing.T) {
	originalCurrentDir := currentDir
	tempDir := t.TempDir()
	currentDir = func() (string, error) { return tempDir, nil }
	t.Cleanup(func() {
		currentDir = originalCurrentDir
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{"--install-force"}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	content, err := os.ReadFile(filepath.Join(tempDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}

	if !strings.Contains(string(content), "build-brief") {
		t.Fatalf("expected build-brief instructions in AGENTS.md, got %q", string(content))
	}

	if strings.Contains(stdout.String(), "RTK detected on this machine") {
		t.Fatalf("expected no RTK notice when RTK is not detected, got %q", stdout.String())
	}
}

func TestRunReturnsWrapperFailureWhenRawOutputRenderingFails(t *testing.T) {
	for _, gradleExitCode := range []int{0, 9} {
		t.Run(fmt.Sprintf("gradle-%d", gradleExitCode), func(t *testing.T) {
			projectDir := t.TempDir()
			rawLogPath := writeAppRawLog(t, "raw output\n")
			stubAppRunResult(t, runner.Result{ExitCode: gradleExitCode, RawLogPath: rawLogPath})

			var stderr bytes.Buffer
			exitCode := Run(context.Background(), []string{
				"--mode", "raw",
				"--project-dir", projectDir,
				"--gradle", os.Args[0],
				"test",
			}, strings.NewReader(""), failingWriter{err: errors.New("stdout closed")}, &stderr)

			assertWrapperOutputFailure(t, exitCode, gradleExitCode, stderr.String(), "render raw output")
		})
	}
}

func TestRunReturnsWrapperFailureWhenLogReductionFails(t *testing.T) {
	for _, gradleExitCode := range []int{0, 9} {
		t.Run(fmt.Sprintf("gradle-%d", gradleExitCode), func(t *testing.T) {
			projectDir := t.TempDir()
			stubAppRunResult(t, runner.Result{
				ExitCode:   gradleExitCode,
				RawLogPath: filepath.Join(t.TempDir(), "missing.log"),
			})

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), []string{
				"--project-dir", projectDir,
				"--gradle", os.Args[0],
				"test",
			}, strings.NewReader(""), &stdout, &stderr)

			assertWrapperOutputFailure(t, exitCode, gradleExitCode, stderr.String(), "reduce log output")
		})
	}
}

func TestRunReturnsWrapperFailureWhenSummaryRenderingFails(t *testing.T) {
	for _, gradleExitCode := range []int{0, 9} {
		t.Run(fmt.Sprintf("gradle-%d", gradleExitCode), func(t *testing.T) {
			projectDir := t.TempDir()
			rawLogPath := writeAppRawLog(t, "BUILD SUCCESSFUL in 1s\n")
			stubAppRunResult(t, runner.Result{ExitCode: gradleExitCode, RawLogPath: rawLogPath})
			stubAppSummaryRenderFailure(t)

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := Run(context.Background(), []string{
				"--project-dir", projectDir,
				"--gradle", os.Args[0],
				"test",
			}, strings.NewReader(""), &stdout, &stderr)

			assertWrapperOutputFailure(t, exitCode, gradleExitCode, stderr.String(), "render summary")
		})
	}
}

func TestRunReturnsWrapperFailureWhenSummaryWriteFails(t *testing.T) {
	for _, gradleExitCode := range []int{0, 9} {
		t.Run(fmt.Sprintf("gradle-%d", gradleExitCode), func(t *testing.T) {
			projectDir := t.TempDir()
			rawLogPath := writeAppRawLog(t, "BUILD SUCCESSFUL in 1s\n")
			stubAppRunResult(t, runner.Result{ExitCode: gradleExitCode, RawLogPath: rawLogPath})

			var stderr bytes.Buffer
			exitCode := Run(context.Background(), []string{
				"--project-dir", projectDir,
				"--gradle", os.Args[0],
				"test",
			}, strings.NewReader(""), failingWriter{err: errors.New("stdout closed")}, &stderr)

			assertWrapperOutputFailure(t, exitCode, gradleExitCode, stderr.String(), "write summary")
		})
	}
}

func stubAppSummaryRenderFailure(t *testing.T) {
	t.Helper()
	original := renderSummaryFn
	renderSummaryFn = func(reducer.Summary) (string, error) {
		return "", errors.New("summary renderer unavailable")
	}
	t.Cleanup(func() {
		renderSummaryFn = original
	})
}

func stubAppRunResult(t *testing.T, result runner.Result) {
	t.Helper()
	original := runGradle
	runGradle = func(context.Context, gradle.Command, string, runner.Options) (runner.Result, error) {
		return result, nil
	}
	t.Cleanup(func() {
		runGradle = original
	})
}

func writeAppRawLog(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "build.log")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write raw log: %v", err)
	}
	return path
}

func assertWrapperOutputFailure(t *testing.T, got, gradleExitCode int, stderr, branch string) {
	t.Helper()
	want := gradleExitCode
	if want == 0 {
		want = 1
	}
	if got != want {
		t.Fatalf("%s: expected exit code %d, got %d stderr=%q", branch, want, got, stderr)
	}
	if !strings.Contains(stderr, branch) {
		t.Fatalf("expected %s failure to be visible, got stderr=%q", branch, stderr)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestRunPreservesGradleExitCodeWhenLogPruningFails(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(projectDir))
	prefix := "build-brief-" + fmt.Sprintf("%08x", hash.Sum32()) + "-"
	for i := 0; i < 21; i++ {
		oldLogDir := filepath.Join(logDir, fmt.Sprintf("%sold-%02d.log", prefix, i))
		if err := os.Mkdir(oldLogDir, 0o755); err != nil {
			t.Fatalf("create old log directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(oldLogDir, "keep"), []byte("old log\n"), 0o644); err != nil {
			t.Fatalf("write old log contents: %v", err)
		}
	}

	scriptPath := appGradleCommand(t, "BUILD SUCCESSFUL in 1s\n", "", 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"--log-dir", logDir,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected Gradle exit code 0 despite pruning failure, got %d stderr=%q", exitCode, stderr.String())
	}
	assertPruningWarning(t, stderr.String())
	if !strings.Contains(stdout.String(), "BUILD SUCCESSFUL in 1s") {
		t.Fatalf("expected normal summary after pruning warning, got stdout=%q", stdout.String())
	}
}

func TestRunPreservesNonzeroGradleExitCodeWhenLogPruningFails(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(projectDir))
	prefix := "build-brief-" + fmt.Sprintf("%08x", hash.Sum32()) + "-"
	for i := 0; i < 21; i++ {
		oldLogDir := filepath.Join(logDir, fmt.Sprintf("%sold-%02d.log", prefix, i))
		if err := os.Mkdir(oldLogDir, 0o755); err != nil {
			t.Fatalf("create old log directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(oldLogDir, "keep"), []byte("old log\n"), 0o644); err != nil {
			t.Fatalf("write old log contents: %v", err)
		}
	}

	scriptPath := appGradleCommand(t, "> Task :test FAILED\nBUILD FAILED in 1s\n", "", 9)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"--log-dir", logDir,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 9 {
		t.Fatalf("expected Gradle exit code 9 despite pruning failure, got %d stderr=%q", exitCode, stderr.String())
	}
	assertPruningWarning(t, stderr.String())
	if !strings.Contains(stdout.String(), "BUILD FAILED in 1s") {
		t.Fatalf("expected normal summary after pruning warning, got stdout=%q", stdout.String())
	}
}

func TestRunContinuesAfterTokenEstimationFailureOnSuccess(t *testing.T) {
	setAppTrackingEnv(t)
	failRawTokenEstimation(t)

	projectDir := t.TempDir()
	logDir := t.TempDir()
	scriptPath := appGradleCommand(t, "BUILD SUCCESSFUL in 1s\n", "", 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"--log-dir", logDir,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected Gradle exit code 0 despite token estimation failure, got %d stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: estimate raw tokens: token estimator unavailable") {
		t.Fatalf("expected token estimation warning, got stderr=%q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "BUILD SUCCESSFUL in 1s") {
		t.Fatalf("expected normal summary after token estimation warning, got stdout=%q", stdout.String())
	}

	record := readLastTrackedRecord(t)
	if !record.Success {
		t.Fatalf("expected successful run to be tracked as successful, got %+v", record)
	}
	assertZeroTokenMetrics(t, record)
}

func TestRunContinuesAfterTokenEstimationFailureOnGradleFailure(t *testing.T) {
	setAppTrackingEnv(t)
	failRawTokenEstimation(t)

	projectDir := t.TempDir()
	logDir := t.TempDir()
	scriptPath := appGradleCommand(t, "> Task :test FAILED\nBUILD FAILED in 1s\n", "", 9)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"--log-dir", logDir,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 9 {
		t.Fatalf("expected exact Gradle exit code 9 despite token estimation failure, got %d stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: estimate raw tokens: token estimator unavailable") {
		t.Fatalf("expected token estimation warning, got stderr=%q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "BUILD FAILED in 1s") {
		t.Fatalf("expected normal failure summary after token estimation warning, got stdout=%q", stdout.String())
	}

	record := readLastTrackedRecord(t)
	if record.Success {
		t.Fatalf("expected failed run to be tracked as failed, got %+v", record)
	}
	assertZeroTokenMetrics(t, record)
}

func failRawTokenEstimation(t *testing.T) {
	t.Helper()
	original := estimateFileTokens
	estimateFileTokens = func(string) (int, error) {
		return 0, errors.New("token estimator unavailable")
	}
	t.Cleanup(func() {
		estimateFileTokens = original
	})
}

func setAppTrackingEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
}

func readLastTrackedRecord(t *testing.T) tracking.Record {
	t.Helper()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatalf("resolve config dir: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "build-brief", "tracking.jsonl"))
	if err != nil {
		t.Fatalf("read tracking data: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatal("expected a tracked run")
	}
	var record tracking.Record
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &record); err != nil {
		t.Fatalf("decode tracked run: %v", err)
	}
	return record
}

func assertZeroTokenMetrics(t *testing.T, record tracking.Record) {
	t.Helper()
	if record.RawTokens != 0 || record.EmittedTokens != 0 || record.SavedTokens != 0 || record.SavingsPct != 0 {
		t.Fatalf("expected unavailable token metrics to remain zero, got %+v", record)
	}
}

func assertPruningWarning(t *testing.T, stderr string) {
	t.Helper()
	if !strings.Contains(stderr, "build-brief: warning:") {
		t.Fatalf("expected pruning failure to be labeled as a warning, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "prune raw log files") {
		t.Fatalf("expected pruning failure to remain observable, got stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "continuing with Gradle result") {
		t.Fatalf("expected pruning warning to indicate continuation, got stderr=%q", stderr)
	}
}

func appGradleCommand(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	originalRunGradle := runGradle
	runGradle = func(ctx context.Context, command gradle.Command, logDir string, opts runner.Options) (runner.Result, error) {
		command.Args = []string{"-test.run=TestAppGradleHelper$"}
		return runner.RunWithOptions(ctx, command, logDir, opts)
	}
	t.Cleanup(func() {
		runGradle = originalRunGradle
	})

	t.Setenv("BUILD_BRIEF_APP_GRADLE_HELPER", "1")
	t.Setenv("BUILD_BRIEF_APP_GRADLE_STDOUT", stdout)
	t.Setenv("BUILD_BRIEF_APP_GRADLE_STDERR", stderr)
	t.Setenv("BUILD_BRIEF_APP_GRADLE_EXIT", strconv.Itoa(exitCode))
	return os.Args[0]
}

func TestAppGradleHelper(t *testing.T) {
	if os.Getenv("BUILD_BRIEF_APP_GRADLE_HELPER") != "1" {
		return
	}

	_, _ = io.WriteString(os.Stdout, os.Getenv("BUILD_BRIEF_APP_GRADLE_STDOUT"))
	_, _ = io.WriteString(os.Stderr, os.Getenv("BUILD_BRIEF_APP_GRADLE_STDERR"))
	exitCode, err := strconv.Atoi(os.Getenv("BUILD_BRIEF_APP_GRADLE_EXIT"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid helper exit code: %v", err)
		os.Exit(1)
	}
	os.Exit(exitCode)
}

func TestRunRawModeStreamsOutput(t *testing.T) {
	projectDir := t.TempDir()
	scriptPath := appGradleCommand(t, "raw-line-1\nraw-line-2\n", "", 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"--mode", "raw",
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	if stdout.String() != "raw-line-1\nraw-line-2\n" {
		t.Fatalf("unexpected raw mode output %q", stdout.String())
	}
}

func TestRunHumanModePreservesInformationalTaskOutput(t *testing.T) {
	projectDir := t.TempDir()
	scriptPath := appGradleCommand(t, "> Task :tasks\nTasks runnable from root project 'sample'\nassemble - Assembles the outputs of this project.\nBUILD SUCCESSFUL in 1s\n", "", 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		":tasks",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	rendered := stdout.String()
	for _, expected := range []string{
		"> Task :tasks",
		"Tasks runnable from root project 'sample'",
		"assemble - Assembles the outputs of this project.",
		"BUILD SUCCESSFUL in 1s",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected human output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRunHumanModeHighlightsGeneratedOutputLocations(t *testing.T) {
	projectDir := t.TempDir()
	scriptPath := appGradleCommand(t, "AgentPreview snapshots written to: /tmp/project/build/agentPreviewSnapshots\nAgentPreview report written to: /tmp/project/build/agentPreviewReports/capture-report.json\nBUILD SUCCESSFUL in 1s\n", "", 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"captureComposePreviews",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	rendered := stdout.String()
	for _, expected := range []string{
		"BUILD SUCCESSFUL in 1s",
		"Highlights:",
		"AgentPreview snapshots written to: /tmp/project/build/agentPreviewSnapshots",
		"AgentPreview report written to: /tmp/project/build/agentPreviewReports/capture-report.json",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("expected human output to contain %q, got %q", expected, rendered)
		}
	}
}

func TestRunHumanModeUsesDefaultProjectConfig(t *testing.T) {
	projectDir := t.TempDir()
	configPath := filepath.Join(projectDir, ".build-brief.json")
	if err := os.WriteFile(configPath, []byte(`{
		"matches": [
			{"name": "Firebase Test Lab", "pattern": "https://console\\.firebase\\.google\\.com/[^\\s.]+"}
		]
	}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	scriptPath := appGradleCommand(t, "Firebase result https://console.firebase.google.com/testlab\nBUILD SUCCESSFUL in 1s\n", "", 0)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run(context.Background(), []string{
		"--project-dir", projectDir,
		"--gradle", scriptPath,
		"test",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q", exitCode, stderr.String())
	}

	rendered := stdout.String()
	if !strings.Contains(rendered, "Custom matches:") ||
		!strings.Contains(rendered, "Firebase Test Lab:") ||
		!strings.Contains(rendered, "https://console.firebase.google.com/testlab") {
		t.Fatalf("expected custom match output, got %q", rendered)
	}
}

func TestRunDoctorHealthyProject(t *testing.T) {
	projectDir := t.TempDir()
	writeExecutable(t, filepath.Join(projectDir, "gradlew"))
	if err := os.MkdirAll(filepath.Join(projectDir, "gradle", "wrapper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "gradle", "wrapper", "gradle-wrapper.properties"), []byte("distributionUrl=https://example.invalid/gradle.zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "gradle", "wrapper", "gradle-wrapper.jar"), []byte("jar"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"doctor", "--project-dir", projectDir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	out := stdout.String()
	for _, expected := range []string{"Build Brief Doctor", "Project", "Gradle", "PASS project directory", "Result: healthy"} {
		if !strings.Contains(out, expected) {
			t.Fatalf("expected doctor output to contain %q, got %q", expected, out)
		}
	}
}

func TestRunDoctorFailureExitsOne(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"doctor", "--project-dir", filepath.Join(t.TempDir(), "missing")}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAIL project directory") {
		t.Fatalf("expected failure in stdout, got %q", stdout.String())
	}
}

func TestRunDoctorUnknownFlagExitsTwo(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"doctor", "--bad-flag"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "unknown doctor flag") {
		t.Fatalf("expected usage error in stderr, got %q", stderr.String())
	}
}

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("portable executable placeholder\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunDoctorJSONCLIModeIsCompatibleWithHuman(t *testing.T) {
	projectDir := t.TempDir()
	writeExecutable(t, filepath.Join(projectDir, "gradlew"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"doctor", "--project-dir", projectDir, "--mode", "json"}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stdout=%q stderr=%q", exitCode, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "PASS mode: human") {
		t.Fatalf("expected json mode to normalize to human, got %q", stdout.String())
	}
}

func TestRunDoctorJSONEnvModeIsCompatibleWithHuman(t *testing.T) {
	t.Setenv("BUILD_BRIEF_MODE", "json")
	projectDir := t.TempDir()
	writeExecutable(t, filepath.Join(projectDir, "gradlew"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"doctor", "--project-dir", projectDir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "PASS mode: human") {
		t.Fatalf("expected env json mode to normalize to human, got %q", stdout.String())
	}
}

func TestRunDoctorGradleDirectoryFails(t *testing.T) {
	projectDir := t.TempDir()
	gradleDir := filepath.Join(projectDir, "not-gradle")
	if err := os.Mkdir(gradleDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), []string{"doctor", "--project-dir", projectDir, "--gradle", gradleDir}, strings.NewReader(""), &stdout, &stderr)

	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d stderr=%q stdout=%q", exitCode, stderr.String(), stdout.String())
	}
	if !strings.Contains(stdout.String(), "FAIL resolution") {
		t.Fatalf("expected gradle resolution failure, got %q", stdout.String())
	}
}
