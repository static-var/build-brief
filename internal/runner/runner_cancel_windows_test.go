//go:build windows

package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"build-brief/internal/gradle"
)

func TestRunTerminatesProcessOnCancel(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()
	scriptPath := filepath.Join(t.TempDir(), "fake-gradle.bat")

	// Batch script that loops until interrupted. When CTRL_BREAK_EVENT
	// is sent, cmd.exe terminates the script with STATUS_CONTROL_C_EXIT,
	// which signalExitCode maps to 130.
	script := "@echo off\r\necho started\r\n:loop\r\nping -n 2 127.0.0.1 >nul\r\ngoto loop\r\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	start := time.Now()
	result, err := Run(ctx, gradle.Command{
		Executable: scriptPath,
		ProjectDir: projectDir,
		Source:     gradle.SourceExplicit,
	}, logDir)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("run with cancellation: %v", err)
	}

	// Process should terminate well before the 5s WaitDelay hard-kill.
	if elapsed > 8*time.Second {
		t.Fatalf("process took %v to terminate, expected prompt cancellation", elapsed)
	}

	if result.ExitCode != 130 {
		t.Fatalf("expected exit code 130 after interrupt, got %d", result.ExitCode)
	}

	info, statErr := os.Stat(result.RawLogPath)
	if statErr != nil {
		t.Fatalf("stat cancellation log: %v", statErr)
	}
	if info.IsDir() {
		t.Fatalf("expected raw log file, got directory: %s", result.RawLogPath)
	}
}
