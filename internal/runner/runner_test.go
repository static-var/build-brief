package runner

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"build-brief/internal/gradle"
)

func TestRunReusesProjectLatestLog(t *testing.T) {
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

	if resultOne.RawLogPath != resultTwo.RawLogPath {
		t.Fatalf("expected log path reuse, got %q and %q", resultOne.RawLogPath, resultTwo.RawLogPath)
	}

	content, err := os.ReadFile(resultTwo.RawLogPath)
	if err != nil {
		t.Fatalf("read latest log: %v", err)
	}

	if string(content) != "second run\n" {
		t.Fatalf("expected truncated latest log, got %q", string(content))
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
	if events[0].RawLogPath != result.RawLogPath {
		t.Fatalf("expected progress raw log path %q, got %q", result.RawLogPath, events[0].RawLogPath)
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

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
