//go:build windows

package runner

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestRunTerminatesProcessOnCancel(t *testing.T) {
	projectDir := t.TempDir()
	logDir := t.TempDir()

	// Run a native helper process rather than cmd.exe. CTRL_BREAK_EVENT
	// terminates it with STATUS_CONTROL_C_EXIT, which signalExitCode maps to 130.

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(500*time.Millisecond, cancel)

	start := time.Now()
	result, err := Run(ctx, runnerTestCommand(t, projectDir, "cancel"), logDir)
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
