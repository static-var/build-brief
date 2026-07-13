//go:build !windows

package tracking

import (
	"fmt"
	"os"
)

const privateTrackingDirMode = 0o700

func ensureTrackingDir(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("tracking directory is a symlink: %s", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("tracking directory is not a directory: %s", dir)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(dir, privateTrackingDirMode); err != nil {
		return err
	}
	info, err = os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("tracking directory changed while creating it: %s", dir)
	}
	return nil
}
