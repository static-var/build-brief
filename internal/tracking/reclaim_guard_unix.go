//go:build !windows

package tracking

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func lockReclaimGuard(file *os.File, deadline time.Time) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tracking lock reclamation is in progress: %s", file.Name())
		}
		time.Sleep(lockPollInterval)
	}
}

func unlockReclaimGuard(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
