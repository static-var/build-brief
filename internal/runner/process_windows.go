//go:build windows

package runner

import (
	"errors"
	"os"
	"os/exec"
)

func configureCommand(cmd *exec.Cmd) {}

func interruptProcess(process *os.Process) error {
	if process == nil {
		return nil
	}

	err := process.Signal(os.Interrupt)
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
