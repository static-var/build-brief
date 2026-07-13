package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"build-brief/internal/artifacts"
	"build-brief/internal/gradle"
)

func TestRunUsesUniqueRawLogPerRun(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	resultOne, err := Run(context.Background(), runnerTestCommand(t, projectDir, "first"), logDir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	resultTwo, err := Run(context.Background(), runnerTestCommand(t, projectDir, "second"), logDir)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if resultOne.RawLogPath == resultTwo.RawLogPath {
		t.Fatalf("expected unique log paths, got %q and %q", resultOne.RawLogPath, resultTwo.RawLogPath)
	}

	contentOne, err := os.ReadFile(resultOne.RawLogPath)
	if err != nil {
		t.Fatalf("read first raw log: %v", err)
	}
	if string(contentOne) != "first run\n" {
		t.Fatalf("unexpected first raw log contents: %q", string(contentOne))
	}

	contentTwo, err := os.ReadFile(resultTwo.RawLogPath)
	if err != nil {
		t.Fatalf("read second raw log: %v", err)
	}
	if string(contentTwo) != "second run\n" {
		t.Fatalf("unexpected second raw log contents: %q", string(contentTwo))
	}
}

func TestRunPreservesCombinedOutputOrder(t *testing.T) {
	t.Setenv("BUILD_BRIEF_RUNNER_HELPER", "1")
	projectDir := t.TempDir()
	logDir := t.TempDir()

	var want strings.Builder
	for i := 0; i < 128; i++ {
		fmt.Fprintf(&want, "stdout-%03d\n", i)
		fmt.Fprintf(&want, "stderr-%03d\n", i)
	}

	for run := 0; run < 20; run++ {
		result, err := Run(context.Background(), gradle.Command{
			Executable: os.Args[0],
			Args:       []string{"-test.run=TestRunnerOutputHelper"},
			ProjectDir: projectDir,
			Source:     gradle.SourceExplicit,
		}, logDir)
		if err != nil {
			t.Fatalf("run %d: %v", run, err)
		}

		content, err := os.ReadFile(result.RawLogPath)
		if err != nil {
			t.Fatalf("read run %d raw log: %v", run, err)
		}
		if got := string(content); got != want.String() {
			t.Fatalf("run %d reordered combined output; got %q, want %q", run, got, want.String())
		}
	}
}

func TestRunnerOutputHelper(t *testing.T) {
	if os.Getenv("BUILD_BRIEF_RUNNER_HELPER") != "1" {
		return
	}

	for i := 0; i < 128; i++ {
		fmt.Fprintf(os.Stdout, "stdout-%03d\n", i)
		fmt.Fprintf(os.Stderr, "stderr-%03d\n", i)
	}
	os.Exit(0)
}

func TestRunnerProcessHelper(t *testing.T) {
	if os.Getenv("BUILD_BRIEF_RUNNER_HELPER") != "1" {
		return
	}

	switch os.Args[len(os.Args)-1] {
	case "first":
		fmt.Fprintln(os.Stdout, "first run")
	case "second":
		time.Sleep(40 * time.Millisecond)
		fmt.Fprintln(os.Stdout, "second run")
	case "current":
		fmt.Fprintln(os.Stdout, "current run")
	case "failed":
		fmt.Fprintln(os.Stdout, "> Task :app:test FAILED")
		os.Exit(7)
	case "slow":
		time.Sleep(120 * time.Millisecond)
		fmt.Fprintln(os.Stdout, "done")
	case "long":
		fmt.Fprintln(os.Stdout, strings.Repeat("x", 1_500_000))
	case "noop":
		fmt.Fprintln(os.Stdout, "done")
	case "cancel":
		fmt.Fprintln(os.Stdout, "started")
		// A zero-case select makes the Go test binary report a runtime deadlock
		// and exit 2 before the parent can deliver CTRL_BREAK_EVENT. Sleep keeps
		// this helper alive indefinitely without coordinating test timing.
		for {
			time.Sleep(time.Hour)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown runner helper mode %q\n", os.Args[len(os.Args)-1])
		os.Exit(2)
	}
	os.Exit(0)
}

func runnerTestCommand(t *testing.T, projectDir, mode string) gradle.Command {
	t.Helper()
	if os.Getenv("BUILD_BRIEF_RUNNER_HELPER") != "1" {
		t.Setenv("BUILD_BRIEF_RUNNER_HELPER", "1")
	}
	return gradle.Command{
		Executable: os.Args[0],
		Args:       []string{"-test.run=^TestRunnerProcessHelper$", "--", mode},
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}
}

func TestRunWaitsForDescendantOutput(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	t.Setenv("BUILD_BRIEF_RUNNER_DESCENDANT", "parent")
	result, err := Run(context.Background(), gradle.Command{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestRunnerDescendantOutputHelper$"},
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}

	content, err := os.ReadFile(result.RawLogPath)
	if err != nil {
		t.Fatalf("read raw log: %v", err)
	}
	if got := string(content); got != "early\nlate\n" {
		t.Fatalf("unexpected raw log contents: %q", got)
	}
}

func TestRunnerDescendantOutputHelper(t *testing.T) {
	switch os.Getenv("BUILD_BRIEF_RUNNER_DESCENDANT") {
	case "parent":
		fmt.Fprintln(os.Stdout, "early")
		child := exec.Command(os.Args[0], "-test.run=TestRunnerDescendantOutputHelper$")
		child.Env = append(os.Environ(), "BUILD_BRIEF_RUNNER_DESCENDANT=child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		time.Sleep(250 * time.Millisecond)
		fmt.Fprintln(os.Stdout, "late")
		os.Exit(0)
	case "failed-parent":
		fmt.Fprintln(os.Stdout, "early")
		child := exec.Command(os.Args[0], "-test.run=TestRunnerDescendantOutputHelper$")
		child.Env = append(os.Environ(), "BUILD_BRIEF_RUNNER_DESCENDANT=failed-child")
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(23)
	case "failed-child":
		time.Sleep(commandWaitDelay + time.Second)
		fmt.Fprintln(os.Stdout, "late")
		os.Exit(0)
	}
}

func TestRunSurfacesTruncatedOutputAfterFailedParent(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	t.Setenv("BUILD_BRIEF_RUNNER_DESCENDANT", "failed-parent")
	result, err := Run(context.Background(), gradle.Command{
		Executable: os.Args[0],
		Args:       []string{"-test.run=TestRunnerDescendantOutputHelper$"},
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
	if err == nil {
		t.Fatal("expected output capture truncation to be surfaced")
	}
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("expected ErrWaitDelay capture error, got %v", err)
	}
	if result.ExitCode != 23 {
		t.Fatalf("expected parent exit code 23, got %d", result.ExitCode)
	}

	content, readErr := os.ReadFile(result.RawLogPath)
	if readErr != nil {
		t.Fatalf("read raw log: %v", readErr)
	}
	if got := string(content); got != "early\n" {
		t.Fatalf("expected only pre-delay output in truncated log, got %q", got)
	}
}

func TestRawLogWriterReportsWriteFailure(t *testing.T) {
	wantErr := errors.New("injected write failure")
	cancelCalls := 0
	writer := &rawLogWriter{
		destination: failingWriter{err: wantErr},
		onError: func() {
			cancelCalls++
		},
	}

	if _, err := writer.Write([]byte("output")); !errors.Is(err, wantErr) {
		t.Fatalf("expected write error %v, got %v", wantErr, err)
	}
	if !errors.Is(writer.Err(), wantErr) {
		t.Fatalf("expected writer to retain write error %v, got %v", wantErr, writer.Err())
	}
	if cancelCalls != 1 {
		t.Fatalf("expected one cancellation callback, got %d", cancelCalls)
	}

	_, _ = writer.Write([]byte("another output"))
	if cancelCalls != 1 {
		t.Fatalf("expected cancellation callback to run once, got %d calls", cancelCalls)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func TestRunConcurrentInvocationsUseSeparateLogs(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	type runResult struct {
		result Result
		err    error
	}

	t.Setenv("BUILD_BRIEF_RUNNER_HELPER", "1")
	results := make(chan runResult, 2)
	runCommand := func(mode string) {
		result, err := Run(context.Background(), runnerTestCommand(t, projectDir, mode), logDir)
		results <- runResult{result: result, err: err}
	}

	go runCommand("first")
	go runCommand("second")

	first := <-results
	second := <-results

	if first.err != nil {
		t.Fatalf("first concurrent run: %v", first.err)
	}
	if second.err != nil {
		t.Fatalf("second concurrent run: %v", second.err)
	}
	if first.result.RawLogPath == second.result.RawLogPath {
		t.Fatalf("expected concurrent runs to use separate logs, got %q", first.result.RawLogPath)
	}

	contentA, err := os.ReadFile(first.result.RawLogPath)
	if err != nil {
		t.Fatalf("read first concurrent raw log: %v", err)
	}
	contentB, err := os.ReadFile(second.result.RawLogPath)
	if err != nil {
		t.Fatalf("read second concurrent raw log: %v", err)
	}

	gotA := string(contentA)
	gotB := string(contentB)
	validOutputs := map[string]struct{}{
		"first run\n":  {},
		"second run\n": {},
	}
	if _, ok := validOutputs[gotA]; !ok {
		t.Fatalf("unexpected first concurrent raw log contents: %q", gotA)
	}
	if _, ok := validOutputs[gotB]; !ok {
		t.Fatalf("unexpected second concurrent raw log contents: %q", gotB)
	}
	if gotA == gotB {
		t.Fatalf("expected concurrent logs to stay isolated, got %q and %q", gotA, gotB)
	}
}

func TestRunPrunesOlderCompletedLogs(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	prefix := "build-brief-" + fmt.Sprintf("%08x", projectHash(projectDir)) + "-"
	baseTime := time.Now().Add(-time.Hour)
	for i := 0; i < maxProjectRawLogs+5; i++ {
		path := filepath.Join(logDir, fmt.Sprintf("%sold-%02d.log", prefix, i))
		if err := os.WriteFile(path, []byte("old run\n"), 0o644); err != nil {
			t.Fatalf("write old raw log %s: %v", path, err)
		}
		modTime := baseTime.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	result, err := Run(context.Background(), runnerTestCommand(t, projectDir, "current"), logDir)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}

	entries, err := os.ReadDir(logDir)
	if err != nil {
		t.Fatalf("read log dir: %v", err)
	}

	kept := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".partial.log") {
			kept++
		}
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".partial.log") {
			t.Fatalf("expected no partial logs to remain, found %q", name)
		}
	}

	if kept != maxProjectRawLogs {
		t.Fatalf("expected %d completed logs after prune, got %d", maxProjectRawLogs, kept)
	}
	if _, err := os.Stat(result.RawLogPath); err != nil {
		t.Fatalf("expected current raw log to remain after prune, stat err=%v", err)
	}
}

func TestRunPreservesExitCodeAndWritesLog(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	result, err := Run(context.Background(), runnerTestCommand(t, projectDir, "failed"), logDir)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}

	if result.ExitCode != 7 {
		t.Fatalf("expected exit code 7, got %d", result.ExitCode)
	}
	if result.StartTime.IsZero() {
		t.Fatal("expected start time to be recorded")
	}
	if !result.ArtifactSnapshot.Captured {
		t.Fatal("expected artifact snapshot to be captured")
	}

	content, err := os.ReadFile(result.RawLogPath)
	if err != nil {
		t.Fatalf("read raw log: %v", err)
	}

	if string(content) != "> Task :app:test FAILED\n" {
		t.Fatalf("unexpected raw log contents: %q", string(content))
	}
}

func TestRunWithOptionsReportsProgress(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	var (
		mu     sync.Mutex
		events []ProgressEvent
	)
	result, err := RunWithOptions(context.Background(), runnerTestCommand(t, projectDir, "slow"), logDir, Options{
		ProgressInterval: 25 * time.Millisecond,
		Progress: func(event ProgressEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, event)
		},
	})
	if err != nil {
		t.Fatalf("run with progress: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(events) == 0 {
		t.Fatal("expected at least one progress event")
	}
	if result.StartTime.IsZero() {
		t.Fatal("expected start time to be recorded")
	}
	if !strings.HasSuffix(events[0].RawLogPath, ".partial.log") {
		t.Fatalf("expected progress raw log path to point at a live partial log, got %q", events[0].RawLogPath)
	}
	expectedFinalPath := strings.TrimSuffix(events[0].RawLogPath, ".partial.log") + ".log"
	if result.RawLogPath != expectedFinalPath {
		t.Fatalf("expected finalized raw log path %q, got %q", expectedFinalPath, result.RawLogPath)
	}
}

func TestRunHandlesVeryLongLines(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	result, err := Run(context.Background(), runnerTestCommand(t, projectDir, "long"), logDir)
	if err != nil {
		t.Fatalf("run long-line command: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}
}

func TestRunCapturesArtifactSnapshotBeforeExecution(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	artifactPath := filepath.Join(projectDir, "app", "build", "libs", "existing.jar")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("jar"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	result, err := Run(context.Background(), runnerTestCommand(t, projectDir, "noop"), logDir)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}

	wantPath := "app/build/libs/existing.jar"
	if !result.ArtifactSnapshot.Captured {
		t.Fatal("expected artifact snapshot to be captured")
	}
	if _, ok := result.ArtifactSnapshot.ArtifactEntries[wantPath]; !ok {
		t.Fatalf("expected snapshot to contain %q, got %+v", wantPath, result.ArtifactSnapshot.ArtifactEntries)
	}
	if got := result.ArtifactSnapshot.ArtifactEntries[wantPath]; got == (artifacts.SnapshotEntry{}) {
		t.Fatalf("expected non-zero snapshot entry for %q", wantPath)
	}
}
