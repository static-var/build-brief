//go:build windows

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGenerateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

const (
	createNewProcessGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	ctrlBreakEvent        = 1          // CTRL_BREAK_EVENT
	errorInvalidParameter = 87
)

// configureCommand places the child in its own process group so that
// GenerateConsoleCtrlEvent can target it without affecting the parent.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNewProcessGroup,
	}
}

// interruptProcess sends CTRL_BREAK_EVENT to the child's process group.
// This is the closest Windows equivalent to the Unix "kill(-pgid, SIGINT)"
// used in process_unix.go. CTRL_C_EVENT is unreliable when targeted at a
// specific process group, so CTRL_BREAK_EVENT is the standard choice.
//
// If the process has already exited, the call fails harmlessly.
// If the signal is not handled, Go's WaitDelay (set in runner.go) ensures
// a hard kill after the grace period.
func interruptProcess(process *os.Process) error {
	if process == nil {
		return nil
	}

	r, _, err := procGenerateConsoleCtrlEvent.Call(
		uintptr(ctrlBreakEvent),
		uintptr(process.Pid),
	)
	if r != 0 {
		return nil // API call succeeded
	}

	// When the process group no longer exists (process already exited),
	// Windows returns ERROR_INVALID_PARAMETER (87).
	if errno, ok := err.(syscall.Errno); ok && errno == errorInvalidParameter {
		return nil
	}
	return err
}
