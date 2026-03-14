//go:build !windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"build-brief/internal/gradle"
)

func TestRunGracefullyInterruptsProcessGroup(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.sh")

	writeExecutable(t, scriptPath, "#!/bin/sh\ntrap 'echo interrupted; exit 130' INT\nprintf 'started\\n'\nwhile true; do sleep 1; done\n")

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)

	result, err := Run(ctx, gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
	if err != nil {
		t.Fatalf("run with cancellation: %v", err)
	}

	if result.ExitCode != 130 {
		t.Fatalf("expected exit code 130 after interrupt, got %d", result.ExitCode)
	}

	info, err := os.Stat(result.RawLogPath)
	if err != nil {
		t.Fatalf("stat cancellation log: %v", err)
	}

	if info.IsDir() {
		t.Fatalf("expected raw log file, got directory: %s", result.RawLogPath)
	}
}
