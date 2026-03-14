//go:build !windows

package runner

import (
	"errors"
	"os/exec"
	"syscall"
)

func signalExitCode(waitErr error) (int, bool) {
	if waitErr == nil {
		return 0, false
	}

	var exitErr *exec.ExitError
	if !errors.As(waitErr, &exitErr) {
		return 0, false
	}

	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return 0, false
	}

	if status.Signaled() {
		return 128 + int(status.Signal()), true
	}

	return status.ExitStatus(), true
}
