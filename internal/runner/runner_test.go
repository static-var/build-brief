package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
