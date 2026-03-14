//go:build !windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interruptProcess(process *os.Process) error {
	if process == nil {
		return nil
	}

	err := syscall.Kill(-process.Pid, syscall.SIGINT)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
