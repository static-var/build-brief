package runner

import (
	"context"
	"fmt"
	"os"
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
	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.sh")

	writeExecutable(t, scriptPath, "#!/bin/sh\necho \"first run\"\nexit 0\n")
	resultOne, err := Run(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	writeExecutable(t, scriptPath, "#!/bin/sh\necho \"second run\"\nexit 0\n")
	resultTwo, err := Run(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
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

func TestRunConcurrentInvocationsUseSeparateLogs(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()
	firstScriptPath := filepath.Join(t.TempDir(), "first-gradle.sh")
	secondScriptPath := filepath.Join(t.TempDir(), "second-gradle.sh")

	writeExecutable(t, firstScriptPath, "#!/bin/sh\npython3 -c 'import time; time.sleep(0.12); print(\"first run\")'\n")
	writeExecutable(t, secondScriptPath, "#!/bin/sh\npython3 -c 'import time; time.sleep(0.04); print(\"second run\")'\n")

	type runResult struct {
		result Result
		err    error
	}

	results := make(chan runResult, 2)
	runCommand := func(executable string) {
		result, err := Run(context.Background(), gradle.Command{
			Executable: executable,
			ProjectDir: projectDir,
			Source:     gradle.SourceExplicit,
		}, logDir)
		results <- runResult{result: result, err: err}
	}

	go runCommand(firstScriptPath)
	go runCommand(secondScriptPath)

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
	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.sh")
	writeExecutable(t, scriptPath, "#!/bin/sh\necho \"current run\"\nexit 0\n")

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

	result, err := Run(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
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
	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.sh")

	writeExecutable(t, scriptPath, "#!/bin/sh\necho \"> Task :app:test FAILED\"\nexit 7\n")
	result, err := Run(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
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
	scriptPath := filepath.Join(t.TempDir(), "slow-gradle.sh")

	writeExecutable(t, scriptPath, "#!/bin/sh\npython3 -c 'import time; time.sleep(0.12); print(\"done\")'\n")

	var (
		mu     sync.Mutex
		events []ProgressEvent
	)
	result, err := RunWithOptions(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir, Options{
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
	scriptPath := filepath.Join(t.TempDir(), "long-line-gradle.sh")

	writeExecutable(t, scriptPath, "#!/bin/sh\npython3 -c 'print(\"x\" * 1500000)'\n")

	result, err := Run(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
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
	scriptPath := filepath.Join(t.TempDir(), "noop-gradle.sh")
	writeExecutable(t, scriptPath, "#!/bin/sh\necho \"done\"\n")

	artifactPath := filepath.Join(projectDir, "app", "build", "libs", "existing.jar")
	if err := os.MkdirAll(filepath.Dir(artifactPath), 0o755); err != nil {
		t.Fatalf("mkdir artifact dir: %v", err)
	}
	if err := os.WriteFile(artifactPath, []byte("jar"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	result, err := Run(context.Background(), gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
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

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
