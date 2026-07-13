//go:build windows

package tracking

import (
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func lockReclaimGuard(file *os.File, deadline time.Time) error {
	for {
		err := windows.LockFileEx(
			windows.Handle(file.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1,
			0,
			&windows.Overlapped{},
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("tracking lock reclamation is in progress: %s", file.Name())
		}
		time.Sleep(lockPollInterval)
	}
}

func unlockReclaimGuard(file *os.File) error {
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &windows.Overlapped{})
}
