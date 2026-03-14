//go:build windows

package runner

import (
	"errors"
	"os/exec"
)

// signalExitCode detects STATUS_CONTROL_C_EXIT (0xC000013A), which Windows
// sets when a process is terminated by a console control event (Ctrl+C or
// Ctrl+Break). We map it to 130 (128 + SIGINT) for consistency with Unix.
func signalExitCode(waitErr error) (int, bool) {
	if waitErr == nil {
		return 0, false
	}

	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		return 0, false
	}

	// STATUS_CONTROL_C_EXIT is 0xC000013A. Using uint32 handles both
	// 32-bit (where ExitCode() returns the signed representation) and
	// 64-bit (where it returns the unsigned value) correctly.
	if uint32(exitErr.ExitCode()) == 0xC000013A {
		return 130, true
	}

	return 0, false
}
